package updatefeed

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const TransferChunkSize int64 = 2 << 20

var (
	ErrWorkspaceBusy          = errors.New("update transfer workspace is in use")
	ErrUnsafePath             = errors.New("unsafe update transfer path")
	ErrRemoteChanged          = errors.New("update source representation changed")
	ErrArtifactIntegrity      = errors.New("update artifact integrity mismatch")
	ErrDestinationUnavailable = errors.New("update destination exists or is unavailable")
	ErrTransferRedirect       = errors.New("update redirects refused")
	ErrTransferTLS            = errors.New("update TLS certificate verification failed")
	errRangeUnsupported       = errors.New("server ignored update range")
	errTransferResponse       = errors.New("unexpected update transfer response")
	cacheName                 = regexp.MustCompile(`^(part-[0-9]{5}\.(bin|json)|\.(chunk|state)-[A-Za-z0-9-]+\.tmp)$`)
)

// TransferOptions is local policy, never taken from an untrusted update feed.
// CacheDirectory contains a single resumable release, bounded by signed Size.
type TransferOptions struct {
	CacheDirectory string
	Connections    int
	MirrorOrigins  []string
	// Tests may shorten bounded retries. Zero selects the production defaults.
	requestTimeout time.Duration
	retryDelay     time.Duration
}
type transferMetadata struct {
	Version   int    `json:"format"`
	Product   string `json:"product"`
	Release   string `json:"release"`
	URL       string `json:"url"`
	Size      int64  `json:"size"`
	SHA256    string `json:"sha256"`
	ChunkSize int64  `json:"chunkSize"`
	ETag      string `json:"etag"`
}
type chunkReceipt struct {
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}
type transferSession struct {
	ctx      context.Context
	manifest Manifest
	root     string
	options  TransferOptions
	meta     transferMetadata
	mu       sync.Mutex
	counts   []int64
	network  int64
	resumed  int64
	retries  int
	mode     string
	progress func(DownloadProgress)
}

func transferIdentity(m Manifest) transferMetadata {
	return transferMetadata{Version: 1, Product: m.Product, Release: m.Version, URL: m.URL, Size: m.Size, SHA256: m.SHA256, ChunkSize: TransferChunkSize}
}
func (a transferMetadata) same(b transferMetadata) bool { a.ETag = ""; b.ETag = ""; return a == b }
func (t *transferSession) partPath(i int, extension string) string {
	return filepath.Join(t.root, fmt.Sprintf("part-%05d.%s", i, extension))
}
func (t *transferSession) partSize(i int) int64 {
	return min(TransferChunkSize, t.manifest.Size-int64(i)*TransferChunkSize)
}
func regularTransferFile(path string) bool {
	if transferSafePath(path) != nil {
		return false
	}
	info, e := os.Lstat(path)
	return e == nil && info.Mode().IsRegular()
}
func boundedJSON(path string, target any) error {
	if !regularTransferFile(path) {
		return ErrUnsafePath
	}
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	d := json.NewDecoder(io.LimitReader(f, 16385))
	d.DisallowUnknownFields()
	if d.Decode(target) != nil || d.Decode(new(any)) != io.EOF {
		return errTransferResponse
	}
	return nil
}
func writeTransferJSON(root, path string, value any) error {
	if transferSafePath(path) != nil {
		return ErrUnsafePath
	}
	data, e := json.Marshal(value)
	if e != nil {
		return e
	}
	f, e := os.CreateTemp(root, ".state-*.tmp")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, e = f.Write(data); e != nil {
		return e
	}
	if e = f.Sync(); e != nil {
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	return os.Rename(f.Name(), path)
}
func (t *transferSession) clearParts() error {
	entries, e := os.ReadDir(t.root)
	if e != nil {
		return e
	}
	for _, entry := range entries {
		if cacheName.MatchString(entry.Name()) {
			path := filepath.Join(t.root, entry.Name())
			if !regularTransferFile(path) {
				return ErrUnsafePath
			}
			if e = os.Remove(path); e != nil {
				return e
			}
		}
	}
	return nil
}
func (t *transferSession) initialize() error {
	entries, err := os.ReadDir(t.root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") && cacheName.MatchString(entry.Name()) {
			path := filepath.Join(t.root, entry.Name())
			if !regularTransferFile(path) {
				return ErrUnsafePath
			}
			if err = os.Remove(path); err != nil {
				return err
			}
		}
	}
	expected := transferIdentity(t.manifest)
	var prior transferMetadata
	metadataPath := filepath.Join(t.root, "manifest.json")
	if e := boundedJSON(metadataPath, &prior); e == nil && prior.same(expected) && (prior.ETag == "" || strongETag(prior.ETag) == prior.ETag) {
		t.meta = prior
	} else {
		if transferSafePath(metadataPath) != nil {
			return ErrUnsafePath
		}
		if e = t.clearParts(); e != nil {
			return e
		}
		t.meta = expected
		if e = writeTransferJSON(t.root, metadataPath, t.meta); e != nil {
			return e
		}
	}
	for i := range t.counts {
		if t.ctx.Err() != nil {
			return t.ctx.Err()
		}
		var receipt chunkReceipt
		path := t.partPath(i, "bin")
		if boundedJSON(t.partPath(i, "json"), &receipt) != nil || receipt.Size != t.partSize(i) || !regularTransferFile(path) {
			continue
		}
		info, e := os.Stat(path)
		if e != nil || info.Size() != receipt.Size {
			continue
		}
		f, e := os.Open(path)
		if e != nil {
			return e
		}
		h := sha256.New()
		n, e := io.Copy(h, io.LimitReader(transferContextReader{t.ctx, f}, receipt.Size+1))
		f.Close()
		if e == nil && n == receipt.Size && hex.EncodeToString(h.Sum(nil)) == receipt.SHA256 {
			t.counts[i] = n
			t.resumed += n
		}
	}
	return nil
}

type transferContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r transferContextReader) Read(buffer []byte) (int, error) {
	if r.ctx.Err() != nil {
		return 0, r.ctx.Err()
	}
	return r.reader.Read(buffer)
}

// CachedTransferBytes is a UI hint based on bounded receipts and file sizes.
// DownloadResumable rehashes every byte before trusting these chunks for resume.
func CachedTransferBytes(directory string, m Manifest) int64 {
	if directory == "" || !filepath.IsAbs(directory) || transferSafePath(directory) != nil || m.Size <= 0 || m.Size > 2<<30 {
		return 0
	}
	var meta transferMetadata
	if boundedJSON(filepath.Join(directory, "manifest.json"), &meta) != nil || !meta.same(transferIdentity(m)) {
		return 0
	}
	t := &transferSession{root: directory, manifest: m}
	bytes := int64(0)
	for i := 0; int64(i)*TransferChunkSize < m.Size; i++ {
		var receipt chunkReceipt
		path := t.partPath(i, "bin")
		if boundedJSON(t.partPath(i, "json"), &receipt) != nil || receipt.Size != t.partSize(i) || len(receipt.SHA256) != 64 || !regularTransferFile(path) {
			continue
		}
		info, e := os.Stat(path)
		if e == nil && info.Size() == receipt.Size {
			bytes += receipt.Size
		}
	}
	return bytes
}

// ClearTransferCache removes only known chunk/receipt/metadata files in the
// explicitly supplied application workspace, never an installer destination.
func ClearTransferCache(directory string) error {
	if directory == "" {
		return nil
	}
	if !filepath.IsAbs(directory) || transferSafePath(directory) != nil {
		return ErrUnsafePath
	}
	if _, e := os.Stat(directory); os.IsNotExist(e) {
		return nil
	}
	release, e := lockTransfer(filepath.Join(directory, "session.lock"))
	if e != nil {
		return e
	}
	defer release()
	t := &transferSession{root: directory}
	if e = t.clearParts(); e != nil {
		return e
	}
	path := filepath.Join(directory, "manifest.json")
	if _, e = os.Lstat(path); os.IsNotExist(e) {
		return nil
	}
	if !regularTransferFile(path) {
		return ErrUnsafePath
	}
	return os.Remove(path)
}
func (t *transferSession) notifyLocked(verifying bool) {
	if t.progress == nil {
		return
	}
	n := int64(0)
	for _, count := range t.counts {
		n += count
	}
	t.progress(DownloadProgress{DownloadedBytes: n, TotalBytes: t.manifest.Size, Verifying: verifying, NetworkBytes: t.network, ResumedBytes: t.resumed, Connections: t.options.Connections, RetryCount: t.retries, Mode: t.mode})
}
func (t *transferSession) report(i int, n int64, network int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.counts[i] = n
	t.network += network
	t.notifyLocked(false)
}
func (t *transferSession) complete() bool {
	for i, n := range t.counts {
		if n != t.partSize(i) {
			return false
		}
	}
	return true
}
func (t *transferSession) fresh() error {
	if e := t.clearParts(); e != nil {
		return e
	}
	clear(t.counts)
	t.resumed = 0
	t.meta = transferIdentity(t.manifest)
	return writeTransferJSON(t.root, filepath.Join(t.root, "manifest.json"), t.meta)
}

// Every worker has a separate transport pool when using Go's standard client,
// so HTTP/2 does not collapse all workers into a single connection. Custom
// transports remain injectable for trusted tests; global network state is unchanged.
func transferClient(base *http.Client) (*http.Client, func()) {
	if base == nil {
		base = &http.Client{}
	}
	client := *base
	client.Jar = nil
	client.CheckRedirect = CheckReleaseRedirect
	transport := client.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	if standard, ok := transport.(*http.Transport); ok {
		clone := standard.Clone()
		clone.MaxConnsPerHost = 1
		clone.MaxIdleConnsPerHost = 1
		client.Transport = clone
		return &client, clone.CloseIdleConnections
	}
	return &client, func() {}
}
func validTransferEncoding(r *http.Response) bool {
	encoding := strings.TrimSpace(r.Header.Get("Content-Encoding"))
	return !r.Uncompressed && (encoding == "" || strings.EqualFold(encoding, "identity"))
}
func rangeHeader(first, last, total int64) string {
	return fmt.Sprintf("bytes %d-%d/%d", first, last, total)
}
func strongETag(value string) string {
	if len(value) > 256 || len(value) < 2 || value[0] != '"' || value[len(value)-1] != '"' || strings.ContainsAny(value, "\r\n") {
		return ""
	}
	for _, b := range []byte(value[1 : len(value)-1]) {
		if b < 0x21 || b == '"' || b == 0x7f {
			return ""
		}
	}
	return value
}
func transientStatus(code int) bool {
	return code == 408 || code == 429 || code == 500 || code == 502 || code == 503 || code == 504
}

type transferAttemptError struct {
	retry bool
	after time.Duration
	cause error
}

func (e *transferAttemptError) Error() string { return e.cause.Error() }
func (e *transferAttemptError) Unwrap() error { return e.cause }
func retryAfter(r *http.Response) time.Duration {
	s := r.Header.Get("Retry-After")
	if seconds, e := strconv.Atoi(s); e == nil && seconds > 0 {
		return time.Duration(min(seconds, 10)) * time.Second
	}
	if date, e := http.ParseTime(s); e == nil {
		return min(max(time.Until(date), 0), 10*time.Second)
	}
	return 0
}
func (t *transferSession) attemptContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(t.ctx, t.options.requestTimeout)
}
func (t *transferSession) retry(fn func() error) error {
	var err error
	for attempt := 0; attempt < 4; attempt++ {
		if t.ctx.Err() != nil {
			return t.ctx.Err()
		}
		err = fn()
		if err == nil {
			return nil
		}
		if t.ctx.Err() != nil {
			return t.ctx.Err()
		}
		var failure *transferAttemptError
		if !errors.As(err, &failure) || !failure.retry || attempt == 3 {
			return err
		}
		t.mu.Lock()
		t.retries++
		t.notifyLocked(false)
		t.mu.Unlock()
		delay := max(t.options.retryDelay*time.Duration(1<<attempt), failure.after)
		timer := time.NewTimer(delay)
		select {
		case <-t.ctx.Done():
			timer.Stop()
			return t.ctx.Err()
		case <-timer.C:
		}
	}
	return err
}
func (t *transferSession) request(client *http.Client, ctx context.Context, first, last int64) (*http.Response, error) {
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, t.manifest.URL, nil)
	if e != nil {
		return nil, e
	}
	req.Header.Set("Accept-Encoding", "identity")
	if first >= 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", first, last))
		if t.meta.ETag != "" {
			req.Header.Set("If-Range", t.meta.ETag)
		}
	}
	r, e := client.Do(req)
	if e != nil {
		if errors.Is(e, ErrTransferRedirect) {
			return nil, ErrTransferRedirect
		}
		var unknown x509.UnknownAuthorityError
		var invalid x509.CertificateInvalidError
		var hostname x509.HostnameError
		if errors.As(e, &unknown) || errors.As(e, &invalid) || errors.As(e, &hostname) {
			return nil, ErrTransferTLS
		}
		return nil, &transferAttemptError{retry: t.ctx.Err() == nil, cause: errors.New("update connection interrupted")}
	}
	if transientStatus(r.StatusCode) {
		r.Body.Close()
		return nil, &transferAttemptError{retry: true, after: retryAfter(r), cause: errors.New("update source temporarily unavailable")}
	}
	return r, nil
}
func (t *transferSession) probe(client *http.Client) error {
	return t.retry(func() error {
		ctx, cancel := t.attemptContext()
		defer cancel()
		r, e := t.request(client, ctx, 0, 0)
		if e != nil {
			return e
		}
		defer r.Body.Close()
		if !validTransferEncoding(r) {
			return errTransferResponse
		}
		if r.StatusCode == 200 {
			if t.meta.ETag != "" && strongETag(r.Header.Get("ETag")) != t.meta.ETag {
				return ErrRemoteChanged
			}
			return errRangeUnsupported
		}
		if r.StatusCode != 206 || r.Header.Get("Content-Range") != rangeHeader(0, 0, t.manifest.Size) || r.ContentLength >= 0 && r.ContentLength != 1 {
			return errTransferResponse
		}
		body, e := io.ReadAll(io.LimitReader(r.Body, 2))
		if e != nil || len(body) != 1 {
			return &transferAttemptError{retry: true, cause: errors.New("range probe interrupted")}
		}
		etag := strongETag(r.Header.Get("ETag"))
		if t.resumed > 0 && t.meta.ETag != "" && etag != t.meta.ETag {
			if e = t.fresh(); e != nil {
				return e
			}
		}
		t.meta.ETag = etag
		return writeTransferJSON(t.root, filepath.Join(t.root, "manifest.json"), t.meta)
	})
}

type transferWriter struct {
	session *transferSession
	index   int
	written int64
	output  io.Writer
}

func (w *transferWriter) Write(data []byte) (int, error) {
	n, e := w.output.Write(data)
	w.written += int64(n)
	w.session.report(w.index, w.written, int64(n))
	return n, e
}
func (t *transferSession) saveChunk(i int, source io.Reader) error {
	f, e := os.CreateTemp(t.root, ".chunk-*.tmp")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	defer f.Close()
	h := sha256.New()
	writer := &transferWriter{session: t, index: i, output: io.MultiWriter(f, h)}
	n, e := io.Copy(writer, io.LimitReader(source, t.partSize(i)+1))
	if e != nil {
		var localError *os.PathError
		if errors.As(e, &localError) {
			return e
		}
	}
	if n > t.partSize(i) {
		return errTransferResponse
	}
	if e != nil || n != t.partSize(i) {
		return &transferAttemptError{retry: true, cause: errors.New("update segment interrupted or length mismatch")}
	}
	if e = t.ctx.Err(); e != nil {
		return e
	}
	if e = f.Sync(); e != nil {
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	if transferSafePath(t.partPath(i, "bin")) != nil {
		return ErrUnsafePath
	}
	if e = os.Rename(f.Name(), t.partPath(i, "bin")); e != nil {
		return e
	}
	return writeTransferJSON(t.root, t.partPath(i, "json"), chunkReceipt{n, hex.EncodeToString(h.Sum(nil))})
}
func (t *transferSession) downloadPart(client *http.Client, i int) error {
	return t.retry(func() error {
		t.report(i, 0, 0)
		ctx, cancel := t.attemptContext()
		defer cancel()
		first := int64(i) * TransferChunkSize
		last := first + t.partSize(i) - 1
		r, e := t.request(client, ctx, first, last)
		if e != nil {
			return e
		}
		defer r.Body.Close()
		if !validTransferEncoding(r) {
			return errTransferResponse
		}
		if r.StatusCode != 200 && r.StatusCode != 206 {
			return errTransferResponse
		}
		if t.meta.ETag != "" && strongETag(r.Header.Get("ETag")) != t.meta.ETag {
			return ErrRemoteChanged
		}
		if r.StatusCode == 200 {
			return errRangeUnsupported
		}
		if r.StatusCode != 206 || r.Header.Get("Content-Range") != rangeHeader(first, last, t.manifest.Size) || r.ContentLength >= 0 && r.ContentLength != t.partSize(i) {
			return errTransferResponse
		}
		return t.saveChunk(i, r.Body)
	})
}
func (t *transferSession) ranges(base *http.Client) error {
	t.mode = "ranges"
	t.mu.Lock()
	t.notifyLocked(false)
	t.mu.Unlock()
	ctx, cancel := context.WithCancel(t.ctx)
	defer cancel()
	// Only this phase uses the derived cancellation; wait for workers before
	// returning so a fallback can never race a stale checkpoint writer.
	original := t.ctx
	t.ctx = ctx
	defer func() { t.ctx = original }()
	missing := []int{}
	for i, count := range t.counts {
		if count != t.partSize(i) {
			missing = append(missing, i)
		}
	}
	jobs := make(chan int)
	var wg sync.WaitGroup
	var once sync.Once
	var firstError error
	for w := 0; w < t.options.Connections; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			client, closeClient := transferClient(base)
			defer closeClient()
			for i := range jobs {
				if e := t.downloadPart(client, i); e != nil {
					once.Do(func() { firstError = e; cancel() })
					return
				}
			}
		}()
	}
send:
	for _, i := range missing {
		select {
		case jobs <- i:
		case <-ctx.Done():
			break send
		}
	}
	close(jobs)
	wg.Wait()
	if firstError != nil {
		return firstError
	}
	return ctx.Err()
}

// Sources which ignore Range are read from byte zero. Completed small chunks
// still survive failures, but cannot save bandwidth without range support.
func (t *transferSession) sequential(client *http.Client) error {
	t.mode = "sequential"
	t.options.Connections = 1
	return t.retry(func() error {
		if e := t.fresh(); e != nil {
			return e
		}
		t.mu.Lock()
		t.notifyLocked(false)
		t.mu.Unlock()
		ctx, cancel := context.WithTimeout(t.ctx, 30*time.Minute)
		defer cancel()
		r, e := t.request(client, ctx, -1, -1)
		if e != nil {
			return e
		}
		defer r.Body.Close()
		if r.StatusCode != 200 || !validTransferEncoding(r) || r.ContentLength >= 0 && r.ContentLength != t.manifest.Size {
			return errTransferResponse
		}
		for i := range t.counts {
			if e = t.saveChunk(i, io.LimitReader(r.Body, t.partSize(i))); e != nil {
				return e
			}
		}
		extra := make([]byte, 1)
		n, e := r.Body.Read(extra)
		if n != 0 || e != io.EOF {
			return errTransferResponse
		}
		return nil
	})
}

// DownloadResumable verifies fresh signed metadata before touching the network.
// Cancellation preserves only complete, flushed, receipted chunks. On every
// restart they are independently rehashed; metadata is never an authority for
// installation. Only the signed whole-file hash permits no-overwrite publication.
func DownloadResumable(ctx context.Context, envelope []byte, policy Policy, base *http.Client, destination string, options TransferOptions, progress func(DownloadProgress)) (Manifest, error) {
	started := time.Now()
	m, e := Verify(envelope, policy)
	if e != nil {
		return Manifest{}, e
	}
	if !filepath.IsAbs(destination) || transferSafePath(destination) != nil {
		return Manifest{}, ErrUnsafePath
	}
	if _, e = os.Lstat(destination); !os.IsNotExist(e) {
		return Manifest{}, ErrDestinationUnavailable
	}
	info, e := os.Stat(filepath.Dir(destination))
	if e != nil || !info.IsDir() {
		return Manifest{}, ErrUnsafePath
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	routes := transferRoutes(m, options.MirrorOrigins)
	routes = prioritizeTransferRoutes(ctx, base, routes, options.CacheDirectory)
	for _, route := range routes {
		if e = ctx.Err(); e != nil {
			return Manifest{}, e
		}
		e = downloadRoute(ctx, route, base, destination, options, progress, func() error {
			fresh := policy
			fresh.Now = policy.Now.Add(time.Since(started))
			_, err := Verify(envelope, fresh)
			return err
		})
		if e == nil {
			return m, nil
		}
		if !canSwitchTransferRoute(e) {
			break
		}
	}
	return Manifest{}, e
}

func downloadRoute(ctx context.Context, m Manifest, base *http.Client, destination string, options TransferOptions, progress func(DownloadProgress), verifyFresh func() error) error {
	var e error
	if options.Connections <= 0 {
		options.Connections = 4
	}
	options.Connections = min(options.Connections, 4)
	if options.requestTimeout <= 0 {
		options.requestTimeout = 90 * time.Second
	}
	if options.retryDelay <= 0 {
		options.retryDelay = 500 * time.Millisecond
	}
	root := options.CacheDirectory
	ephemeral := root == ""
	if ephemeral {
		root, e = os.MkdirTemp("", "WebDAVMount-update-")
	} else {
		if !filepath.IsAbs(root) || transferSafePath(root) != nil {
			return ErrUnsafePath
		}
		e = os.MkdirAll(root, 0700)
	}
	if e != nil {
		return e
	}
	if transferSafePath(root) != nil {
		return ErrUnsafePath
	}
	release, e := lockTransfer(filepath.Join(root, "session.lock"))
	if e != nil {
		return e
	}
	defer func() {
		release()
		if ephemeral {
			entries, _ := os.ReadDir(root)
			for _, entry := range entries {
				if cacheName.MatchString(entry.Name()) || entry.Name() == "session.lock" || entry.Name() == "manifest.json" {
					os.Remove(filepath.Join(root, entry.Name()))
				}
			}
			os.Remove(root)
		}
	}()
	t := &transferSession{ctx: ctx, manifest: m, root: root, options: options, counts: make([]int64, (m.Size+TransferChunkSize-1)/TransferChunkSize), progress: progress, mode: "preparing"}
	if e = t.initialize(); e != nil {
		return e
	}
	t.mu.Lock()
	t.notifyLocked(false)
	t.mu.Unlock()
	if !t.complete() {
		client, closeClient := transferClient(base)
		defer closeClient()
		if m.Size <= TransferChunkSize {
			e = t.sequential(client)
		} else {
			e = t.probe(client)
			if e == nil {
				closeClient()
				e = t.ranges(base)
			}
			if errors.Is(e, errRangeUnsupported) {
				e = t.sequential(client)
			}
		}
		if e != nil {
			if errors.Is(e, ErrRemoteChanged) {
				t.fresh()
			}
			return e
		}
	}
	if e = ctx.Err(); e != nil {
		return e
	}
	t.mu.Lock()
	t.notifyLocked(true)
	t.mu.Unlock()
	assembled, e := os.CreateTemp(filepath.Dir(destination), ".webdavmount-update-*.tmp")
	if e != nil {
		return e
	}
	defer os.Remove(assembled.Name())
	defer assembled.Close()
	for i := range t.counts {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		part, e := os.Open(t.partPath(i, "bin"))
		if e != nil {
			return e
		}
		n, copyErr := io.Copy(assembled, io.LimitReader(transferContextReader{ctx, part}, t.partSize(i)+1))
		part.Close()
		if copyErr != nil || n != t.partSize(i) {
			return ErrArtifactIntegrity
		}
	}
	if _, e = assembled.Seek(0, io.SeekStart); e != nil {
		return e
	}
	if e = CheckArtifact(transferContextReader{ctx, assembled}, m); e != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		t.fresh()
		return ErrArtifactIntegrity
	}
	if e = ctx.Err(); e != nil {
		return e
	}
	if e = assembled.Sync(); e != nil {
		return e
	}
	if e = assembled.Close(); e != nil {
		return e
	}
	if e = verifyFresh(); e != nil {
		return e
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if transferSafePath(destination) != nil {
		return ErrUnsafePath
	}
	if e = publishTransfer(assembled.Name(), destination); e != nil {
		return e
	}
	// Cache cleanup is best effort after the no-overwrite publication commits.
	// A cleanup failure cannot turn a successfully saved installer into a failure.
	if t.clearParts() == nil {
		os.Remove(filepath.Join(root, "manifest.json"))
	}
	return nil
}
