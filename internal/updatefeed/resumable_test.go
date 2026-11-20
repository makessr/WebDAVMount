package updatefeed

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type transferRoundTripper func(*http.Request) (*http.Response, error)

func (f transferRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type requestRange struct {
	first, last int64
	header      string
	attempt     int
}
type transferFixture struct {
	body     []byte
	manifest Manifest
	policy   Policy
	envelope []byte
	client   *http.Client
	mu       sync.Mutex
	calls    []requestRange
	attempts map[string]int
	behavior func(*http.Request, int64, int64, int) (*http.Response, error)
}

func newTransferFixture(t *testing.T) *transferFixture {
	t.Helper()
	m, p, key, _ := fixture(t)
	body := bytes.Repeat([]byte("abcdefgh中文0123456789"), int(TransferChunkSize*3/24)+1)
	body = body[:TransferChunkSize*3-17]
	sum := sha256.Sum256(body)
	m.Size = int64(len(body))
	m.SHA256 = hex.EncodeToString(sum[:])
	p.MaxBytes = 32 << 20
	f := &transferFixture{body: body, manifest: m, policy: p, envelope: signed(t, m, key), attempts: map[string]int{}}
	f.client = &http.Client{Transport: transferRoundTripper(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Error("credentials sent")
		}
		first, last := int64(-1), int64(-1)
		if header := r.Header.Get("Range"); header != "" {
			if _, e := fmt.Sscanf(header, "bytes=%d-%d", &first, &last); e != nil || first < 0 || last < first || last >= int64(len(f.body)) {
				return nil, errors.New("invalid fixture range")
			}
		}
		f.mu.Lock()
		f.attempts[r.Header.Get("Range")]++
		attempt := f.attempts[r.Header.Get("Range")]
		f.calls = append(f.calls, requestRange{first, last, r.Header.Get("Range"), attempt})
		behavior := f.behavior
		f.mu.Unlock()
		if behavior != nil {
			return behavior(r, first, last, attempt)
		}
		return f.response(r, first, last), nil
	})}
	return f
}
func (f *transferFixture) response(r *http.Request, first, last int64) *http.Response {
	data := f.body
	code := 200
	headers := http.Header{"Etag": []string{`"fixture-v1"`}}
	if first >= 0 {
		data = data[first : last+1]
		code = 206
		headers.Set("Content-Range", rangeHeader(first, last, int64(len(f.body))))
	}
	return &http.Response{StatusCode: code, ContentLength: int64(len(data)), Header: headers, Body: io.NopCloser(bytes.NewReader(data)), Request: r}
}
func (f *transferFixture) options(t *testing.T) TransferOptions {
	return TransferOptions{CacheDirectory: t.TempDir(), Connections: 4, retryDelay: time.Millisecond, requestTimeout: time.Second}
}
func (f *transferFixture) callCount() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.calls) }
func (f *transferFixture) dataCalls(first int64) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, r := range f.calls {
		if r.first == first && r.last > 0 {
			n++
		}
	}
	return n
}
func assertDownloaded(t *testing.T, path string, expected []byte) {
	t.Helper()
	got, e := os.ReadFile(path)
	if e != nil || !bytes.Equal(got, expected) {
		t.Fatal("downloaded artifact differs", e)
	}
}
func assertNotPublished(t *testing.T, path string) {
	t.Helper()
	if _, e := os.Stat(path); !os.IsNotExist(e) {
		t.Fatal("unverified destination exists", e)
	}
}

func TestResumableParallelRangesAndWholeHash(t *testing.T) {
	f := newTransferFixture(t)
	options := f.options(t)
	path := filepath.Join(t.TempDir(), "setup.exe")
	var active, peak atomic.Int32
	gate := make(chan struct{})
	var release sync.Once
	f.behavior = func(r *http.Request, first, last int64, attempt int) (*http.Response, error) {
		if last > 0 {
			current := active.Add(1)
			for p := peak.Load(); current > p && !peak.CompareAndSwap(p, current); p = peak.Load() {
			}
			if current >= 3 {
				release.Do(func() { close(gate) })
			}
			select {
			case <-gate:
			case <-r.Context().Done():
				active.Add(-1)
				return nil, r.Context().Err()
			}
			active.Add(-1)
		}
		return f.response(r, first, last), nil
	}
	var last DownloadProgress
	got, e := DownloadResumable(context.Background(), f.envelope, f.policy, f.client, path, options, func(p DownloadProgress) { last = p })
	if e != nil || got != f.manifest || peak.Load() < 2 || peak.Load() > 4 {
		t.Fatal("parallel range download", got, e, peak.Load())
	}
	assertDownloaded(t, path, f.body)
	if !last.Verifying || last.DownloadedBytes != f.manifest.Size || last.TotalBytes != f.manifest.Size || last.Mode != "ranges" || last.NetworkBytes != f.manifest.Size || last.ResumedBytes != 0 {
		t.Fatal("progress", last)
	}
	if CachedTransferBytes(options.CacheDirectory, f.manifest) != 0 {
		t.Fatal("successful publication retained chunks")
	}
}

func TestResumableRetriesTransientStatusAndTruncatedBody(t *testing.T) {
	for _, kind := range []string{"503", "429", "truncated", "connection"} {
		t.Run(kind, func(t *testing.T) {
			f := newTransferFixture(t)
			options := f.options(t)
			f.behavior = func(r *http.Request, first, last int64, attempt int) (*http.Response, error) {
				if first == 0 && last > 0 && attempt == 1 {
					response := f.response(r, first, last)
					switch kind {
					case "503":
						response.StatusCode = 503
					case "429":
						response.StatusCode = 429
					case "connection":
						return nil, errors.New("synthetic connection interruption")
					case "truncated":
						response.Body = io.NopCloser(bytes.NewReader(f.body[:128]))
					}
					return response, nil
				}
				return f.response(r, first, last), nil
			}
			path := filepath.Join(t.TempDir(), "setup.exe")
			var last DownloadProgress
			if _, e := DownloadResumable(context.Background(), f.envelope, f.policy, f.client, path, options, func(p DownloadProgress) { last = p }); e != nil {
				t.Fatal(e)
			}
			assertDownloaded(t, path, f.body)
			if f.dataCalls(0) != 2 || last.RetryCount != 1 {
				t.Fatal("retry budget", f.dataCalls(0), last)
			}
		})
	}
}

func TestResumableSurvivesFailedRunAndSkipsDurableSegments(t *testing.T) {
	for _, corrupt := range []bool{false, true} {
		t.Run(fmt.Sprint(corrupt), func(t *testing.T) {
			f := newTransferFixture(t)
			options := f.options(t)
			options.Connections = 1
			f.behavior = func(r *http.Request, first, last int64, attempt int) (*http.Response, error) {
				response := f.response(r, first, last)
				if first >= TransferChunkSize {
					response.StatusCode = 503
				}
				return response, nil
			}
			path := filepath.Join(t.TempDir(), "unfinished.exe")
			if _, e := DownloadResumable(context.Background(), f.envelope, f.policy, f.client, path, options, nil); e == nil {
				t.Fatal("missing failure")
			}
			assertNotPublished(t, path)
			if CachedTransferBytes(options.CacheDirectory, f.manifest) != TransferChunkSize {
				t.Fatal("durable chunk not retained")
			}
			if corrupt {
				part := filepath.Join(options.CacheDirectory, "part-00000.bin")
				data, _ := os.ReadFile(part)
				data[0] ^= 1
				os.WriteFile(part, data, 0600)
			}
			f.behavior = nil
			target := filepath.Join(t.TempDir(), "different-save-location.exe")
			var firstProgress DownloadProgress
			firstEvent := true
			if _, e := DownloadResumable(context.Background(), f.envelope, f.policy, f.client, target, options, func(p DownloadProgress) {
				if firstEvent {
					firstProgress = p
					firstEvent = false
				}
			}); e != nil {
				t.Fatal(e)
			}
			assertDownloaded(t, target, f.body)
			expectedCalls := 1
			expectedResume := TransferChunkSize
			if corrupt {
				expectedCalls = 2
				expectedResume = 0
			}
			if f.dataCalls(0) != expectedCalls || firstProgress.ResumedBytes != expectedResume || firstProgress.NetworkBytes != 0 {
				t.Fatal("resume used unverified data or redownloaded durable chunk", f.dataCalls(0), firstProgress)
			}
		})
	}
}

func TestResumableRangeIgnoredFallsBackAndWrongResponsesFail(t *testing.T) {
	for _, kind := range []string{"ignored", "ignored-midway", "wrong-range", "wrong-length", "changed-etag", "encoding", "403", "bad-hash"} {
		t.Run(kind, func(t *testing.T) {
			f := newTransferFixture(t)
			options := f.options(t)
			options.Connections = 1
			f.behavior = func(r *http.Request, first, last int64, attempt int) (*http.Response, error) {
				response := f.response(r, first, last)
				if first >= 0 {
					switch kind {
					case "ignored":
						response = f.response(r, -1, -1)
					case "ignored-midway":
						if last > 0 {
							response = f.response(r, -1, -1)
						}
					case "wrong-range":
						response.Header.Set("Content-Range", "bytes 1-2/999")
					case "wrong-length":
						response.ContentLength++
					case "changed-etag":
						if last > 0 {
							response.Header.Set("ETag", `"different"`)
						}
					case "encoding":
						response.Header.Set("Content-Encoding", "gzip")
					case "403":
						response.StatusCode = 403
					case "bad-hash":
						if last > 0 {
							data := append([]byte(nil), f.body[first:last+1]...)
							data[0] ^= 1
							response.Body = io.NopCloser(bytes.NewReader(data))
						}
					}
				}
				return response, nil
			}
			target := filepath.Join(t.TempDir(), "setup.exe")
			var last DownloadProgress
			_, e := DownloadResumable(context.Background(), f.envelope, f.policy, f.client, target, options, func(p DownloadProgress) { last = p })
			if strings.HasPrefix(kind, "ignored") {
				if e != nil || last.Mode != "sequential" {
					t.Fatal(e, last)
				}
				assertDownloaded(t, target, f.body)
			} else {
				if e == nil {
					t.Fatal("unsafe response accepted")
				}
				assertNotPublished(t, target)
			}
			if kind == "403" && f.callCount() != 1 {
				t.Fatal("permanent failure retried")
			}
			if kind == "changed-etag" && !errors.Is(e, ErrRemoteChanged) {
				t.Fatal(e)
			}
			if kind == "bad-hash" && (!errors.Is(e, ErrArtifactIntegrity) || CachedTransferBytes(options.CacheDirectory, f.manifest) != 0) {
				t.Fatal("bad artifact cache reused", e)
			}
		})
	}
}

func TestResumableCancellationExpiryLockAndNoOverwrite(t *testing.T) {
	f := newTransferFixture(t)
	options := f.options(t)
	target := filepath.Join(t.TempDir(), "setup.exe")
	if e := os.WriteFile(target, []byte("keep"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := DownloadResumable(context.Background(), f.envelope, f.policy, f.client, target, options, nil); !errors.Is(e, ErrDestinationUnavailable) || f.callCount() != 0 {
		t.Fatal(e, f.callCount())
	}
	assertDownloaded(t, target, []byte("keep"))
	other := filepath.Join(t.TempDir(), "setup.exe")
	release, e := lockTransfer(filepath.Join(options.CacheDirectory, "session.lock"))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = DownloadResumable(context.Background(), f.envelope, f.policy, f.client, other, options, nil); !errors.Is(e, ErrWorkspaceBusy) || f.callCount() != 0 {
		release()
		t.Fatal(e)
	}
	release()
	late := f.policy
	late.Now = time.Date(2028, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, e = DownloadResumable(context.Background(), f.envelope, late, f.client, other, options, nil); e == nil || f.callCount() != 0 {
		t.Fatal("expired manifest used cache/network", e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	f.behavior = func(r *http.Request, first, last int64, attempt int) (*http.Response, error) {
		cancel()
		return nil, context.Canceled
	}
	start := time.Now()
	if _, e = DownloadResumable(ctx, f.envelope, f.policy, f.client, other, options, nil); e == nil || time.Since(start) > time.Second {
		t.Fatal("cancellation did not stop retries", e)
	}
	assertNotPublished(t, other)
}

func TestTransferCacheClearingPreservesOtherFilesAndReleasesLocks(t *testing.T) {
	root := t.TempDir()
	keep := filepath.Join(root, "keep.txt")
	os.WriteFile(keep, []byte("keep"), 0600)
	os.WriteFile(filepath.Join(root, "part-00000.bin"), []byte("chunk"), 0600)
	os.WriteFile(filepath.Join(root, "part-00000.json"), []byte(`{}`), 0600)
	os.WriteFile(filepath.Join(root, "manifest.json"), []byte(`{}`), 0600)
	if e := ClearTransferCache(root); e != nil {
		t.Fatal(e)
	}
	assertDownloaded(t, keep, []byte("keep"))
	if _, e := os.Stat(filepath.Join(root, "part-00000.bin")); !os.IsNotExist(e) {
		t.Fatal("chunk remains")
	}
	release, e := lockTransfer(filepath.Join(root, "session.lock"))
	if e != nil {
		t.Fatal(e)
	}
	if e = ClearTransferCache(root); !errors.Is(e, ErrWorkspaceBusy) {
		release()
		t.Fatal(e)
	}
	release()
	if e = ClearTransferCache(root); e != nil {
		t.Fatal(e)
	}
}

func TestResumableRefusesRedirectAndInvalidCertificatesWithoutRetry(t *testing.T) {
	for _, kind := range []string{"redirect", "certificate"} {
		t.Run(kind, func(t *testing.T) {
			f := newTransferFixture(t)
			f.behavior = func(r *http.Request, first, last int64, attempt int) (*http.Response, error) {
				if kind == "certificate" {
					return nil, x509.UnknownAuthorityError{Cert: &x509.Certificate{}}
				}
				response := f.response(r, first, last)
				response.StatusCode = 302
				response.Header.Set("Location", "https://other.example/setup.exe")
				return response, nil
			}
			target := filepath.Join(t.TempDir(), "setup.exe")
			_, e := DownloadResumable(context.Background(), f.envelope, f.policy, f.client, target, f.options(t), nil)
			if e == nil || f.callCount() != 1 {
				t.Fatal("permanent trust failure retried or accepted", e, f.callCount())
			}
			assertNotPublished(t, target)
		})
	}
}

func TestResumablePublicationRacePreservesDestinationAndCanReuseVerifiedCache(t *testing.T) {
	f := newTransferFixture(t)
	options := f.options(t)
	target := filepath.Join(t.TempDir(), "setup.exe")
	_, e := DownloadResumable(context.Background(), f.envelope, f.policy, f.client, target, options, func(p DownloadProgress) {
		if p.Verifying {
			os.WriteFile(target, []byte("keep"), 0600)
		}
	})
	if e == nil {
		t.Fatal("publication overwrote destination race")
	}
	assertDownloaded(t, target, []byte("keep"))
	requests := f.callCount()
	other := filepath.Join(t.TempDir(), "other.exe")
	if _, e = DownloadResumable(context.Background(), f.envelope, f.policy, f.client, other, options, nil); e != nil {
		t.Fatal(e)
	}
	assertDownloaded(t, other, f.body)
	if f.callCount() != requests {
		t.Fatal("already verified cache fetched again")
	}
}

func TestResumableRechecksExpiryBeforePublication(t *testing.T) {
	m, p, key, body := fixture(t)
	m.Expires = p.Now.Add(time.Second).Format(time.RFC3339)
	client := &http.Client{Transport: transferRoundTripper(func(r *http.Request) (*http.Response, error) {
		time.Sleep(1100 * time.Millisecond)
		return &http.Response{StatusCode: 200, ContentLength: int64(len(body)), Body: io.NopCloser(bytes.NewReader(body)), Header: http.Header{}, Request: r}, nil
	})}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	target := filepath.Join(t.TempDir(), "setup.exe")
	if _, e := DownloadResumable(ctx, signed(t, m, key), p, client, target, TransferOptions{CacheDirectory: t.TempDir()}, nil); e == nil {
		t.Fatal("expired feed published after transfer")
	}
	assertNotPublished(t, target)
}
