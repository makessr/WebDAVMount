package updatefeed

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"path/filepath"
	"sync"
	"testing"
)

func TestGitHubUpdateRoutesPreferMirrorAndRetryVerifiedArtifact(t *testing.T) {
	m, policy, key, body := fixture(t)
	m.URL = "https://github.com/makessr/WebDAVMount/releases/download/v0.24.0/WebDAVMount-0.24.0-setup.exe"
	policy.AllowedHosts = []string{"github.com"}
	policy.GitHubRepository = "makessr/WebDAVMount"
	wrong := m
	wrong.URL = "https://github.com/another/project/releases/download/v0.24.0/WebDAVMount-0.24.0-setup.exe"
	if _, err := Verify(signed(t, wrong, key), policy); err == nil {
		t.Fatal("release URL from another repository accepted")
	}
	var mu sync.Mutex
	var downloads []string
	client := &http.Client{Transport: transferRoundTripper(func(req *http.Request) (*http.Response, error) {
		host := req.URL.Host
		status, data := http.StatusOK, body
		if req.Header.Get("Range") == "bytes=0-0" {
			if host == "mirror-one.example" {
				status, data = http.StatusNotFound, nil
			}
		} else {
			mu.Lock()
			downloads = append(downloads, host)
			mu.Unlock()
			if host == "mirror-two.example" {
				data = bytes.Repeat([]byte{'x'}, len(body))
			}
		}
		if host != "github.com" && req.URL.Path != "/"+m.URL {
			t.Error("mirror URL lost original signed path")
		}
		return &http.Response{StatusCode: status, ContentLength: int64(len(data)), Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(data)), Request: req}, nil
	})}
	destination := filepath.Join(t.TempDir(), "setup.exe")
	got, err := DownloadResumable(context.Background(), signed(t, m, key), policy, client, destination,
		TransferOptions{MirrorOrigins: []string{"https://mirror-one.example", "https://mirror-two.example"}}, nil)
	if err != nil || got != m {
		t.Fatal(got, err)
	}
	assertDownloaded(t, destination, body)
	if len(downloads) != 2 || downloads[0] != "mirror-two.example" || downloads[1] != "github.com" {
		t.Fatal(downloads)
	}
}

func TestGitHubUpdateRedirectOnlyAllowsDirectAssetHost(t *testing.T) {
	request, _ := http.NewRequest("GET", "https://github.com/makessr/WebDAVMount/releases/download/stable-v2/latest.json", nil)
	asset, _ := http.NewRequest("GET", "https://release-assets.githubusercontent.com/object?token=public-download", nil)
	if err := CheckReleaseRedirect(asset, []*http.Request{request}); err != nil {
		t.Fatal(err)
	}
	mirror, _ := http.NewRequest("GET", "https://ghproxy.net/"+request.URL.String(), nil)
	unsafe, _ := http.NewRequest("GET", "https://release-assets.githubusercontent.com.bad.example/object", nil)
	if CheckReleaseRedirect(asset, []*http.Request{mirror}) == nil ||
		CheckReleaseRedirect(unsafe, []*http.Request{request}) == nil ||
		CheckReleaseRedirect(asset, []*http.Request{request, asset}) == nil {
		t.Fatal("untrusted redirect allowed")
	}
}
