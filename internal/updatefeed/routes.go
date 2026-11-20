package updatefeed

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// RoutedURLs puts GitHub mirrors ahead of the canonical release URL. Mirrors
// accept the complete GitHub URL as a path suffix.
func RoutedURLs(address string, origins []string) []string {
	routes := []string{address}
	source, err := url.Parse(address)
	if err != nil {
		return routes
	}
	seen := map[string]bool{strings.ToLower(source.Host): true}
	for _, value := range origins {
		origin, err := url.Parse(strings.TrimSpace(value))
		if err != nil || origin.Scheme != "https" || origin.Hostname() == "" ||
			origin.User != nil || origin.Opaque != "" || origin.ForceQuery || origin.RawQuery != "" ||
			strings.Contains(value, "#") || (origin.EscapedPath() != "" && origin.EscapedPath() != "/") {
			continue
		}
		host := strings.ToLower(origin.Host)
		if seen[host] {
			continue
		}
		seen[host] = true
		if githubReleaseURL(source) {
			routes = append(routes, "https://"+origin.Host+"/"+address)
		} else {
			mirror := *source
			mirror.Host = origin.Host
			routes = append(routes, mirror.String())
		}
		if len(routes) == 3 {
			break
		}
	}
	if githubReleaseURL(source) {
		routes = append(routes[1:], address)
	}
	return routes
}

func githubReleaseURL(u *url.URL) bool {
	return u.Scheme == "https" && strings.EqualFold(u.Hostname(), "github.com") && strings.Contains(u.Path, "/releases/download/")
}

// GitHub serves public release assets from its dedicated HTTPS asset host.
// Proxy responses and all subsequent redirects must stay on the selected route.
func CheckReleaseRedirect(req *http.Request, via []*http.Request) error {
	if len(via) == 1 && githubReleaseURL(via[0].URL) && req.URL.Scheme == "https" &&
		strings.EqualFold(req.URL.Hostname(), "release-assets.githubusercontent.com") &&
		(req.URL.Port() == "" || req.URL.Port() == "443") && req.URL.User == nil {
		req.Header.Del("Referer")
		return nil
	}
	return ErrTransferRedirect
}

func transferRoutes(manifest Manifest, origins []string) []Manifest {
	addresses := RoutedURLs(manifest.URL, origins)
	routes := make([]Manifest, 0, len(addresses))
	for _, address := range addresses {
		route := manifest
		route.URL = address
		routes = append(routes, route)
	}
	return routes
}

func probeTransferRoute(ctx context.Context, base *http.Client, route Manifest) bool {
	client, closeClient := transferClient(base)
	defer closeClient()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, route.URL, nil)
	if err != nil {
		return false
	}
	req.Header.Set("Range", "bytes=0-0")
	req.Header.Set("Accept-Encoding", "identity")
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if !validTransferEncoding(resp) {
		return false
	}
	if resp.StatusCode == http.StatusPartialContent {
		if resp.Header.Get("Content-Range") != rangeHeader(0, 0, route.Size) {
			return false
		}
	} else if resp.StatusCode != http.StatusOK || (resp.ContentLength >= 0 && resp.ContentLength != route.Size) {
		return false
	}
	var first [1]byte
	_, err = io.ReadFull(resp.Body, first[:])
	return err == nil
}

func prioritizeTransferRoutes(ctx context.Context, base *http.Client, routes []Manifest, cacheDirectory string) []Manifest {
	if len(routes) == 1 {
		return routes
	}
	cached := -1
	// A GitHub mirror is preferred even if the GitHub direct probe completes
	// first. The two mirrors are ordered by time to the first usable byte.
	direct := -1
	for i, route := range routes {
		u, _ := url.Parse(route.URL)
		if githubReleaseURL(u) {
			direct = i
		}
		size := CachedTransferBytes(cacheDirectory, route)
		if size == route.Size {
			ordered := []Manifest{route}
			ordered = append(ordered, routes[:i]...)
			return append(ordered, routes[i+1:]...)
		}
		if size > 0 {
			cached = i
		}
	}
	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	type result struct {
		index int
		ok    bool
	}
	results := make(chan result, len(routes))
	for i, route := range routes {
		go func() { results <- result{i, probeTransferRoute(probeCtx, base, route)} }()
	}
	ordered := make([]Manifest, 0, len(routes))
	reachable := make([]bool, len(routes))
	for range routes {
		select {
		case result := <-results:
			if result.ok {
				reachable[result.index] = true
				if result.index != cached && result.index != direct {
					ordered = append(ordered, routes[result.index])
				}
			}
		case <-probeCtx.Done():
		}
	}
	if cached >= 0 && cached != direct && reachable[cached] {
		ordered = append([]Manifest{routes[cached]}, ordered...)
	}
	if direct >= 0 && reachable[direct] {
		ordered = append(ordered, routes[direct])
	}
	for i, route := range routes {
		if !reachable[i] && i != direct {
			ordered = append(ordered, route)
		}
	}
	if direct >= 0 && !reachable[direct] {
		ordered = append(ordered, routes[direct])
	}
	return ordered
}

func canSwitchTransferRoute(err error) bool {
	var attempt *transferAttemptError
	return errors.As(err, &attempt) || errors.Is(err, errTransferResponse) ||
		errors.Is(err, ErrTransferRedirect) || errors.Is(err, ErrTransferTLS) ||
		errors.Is(err, ErrRemoteChanged) || errors.Is(err, ErrArtifactIntegrity)
}

// CachedRoutedTransferBytes includes the selected mirror's resumable chunks.
func CachedRoutedTransferBytes(directory string, manifest Manifest, origins []string) int64 {
	for _, route := range transferRoutes(manifest, origins) {
		if size := CachedTransferBytes(directory, route); size > 0 {
			return size
		}
	}
	return 0
}
