package updatefeed

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// Download verifies signed metadata before making an explicitly requested HTTPS
// request. It publishes a new file only, never overwrites or executes it.
func Download(ctx context.Context, envelope []byte, policy Policy, client *http.Client, destination string) (Manifest, error) {
	return DownloadWithProgress(ctx, envelope, policy, client, destination, nil)
}

type DownloadProgress struct {
	DownloadedBytes int64
	TotalBytes      int64
	Verifying       bool
	NetworkBytes    int64
	ResumedBytes    int64
	Connections     int
	RetryCount      int
	Mode            string
}

type progressWriter struct {
	destination io.Writer
	downloaded  int64
	total       int64
	progress    func(DownloadProgress)
}

func (w *progressWriter) Write(p []byte) (int, error) {
	n, err := w.destination.Write(p)
	w.downloaded += int64(n)
	if w.progress != nil {
		w.progress(DownloadProgress{DownloadedBytes: w.downloaded, TotalBytes: w.total})
	}
	return n, err
}

// DownloadWithProgress reports bytes actually written; completion still requires
// the existing signature, length, SHA256 and no-overwrite publication checks.
func DownloadWithProgress(ctx context.Context, envelope []byte, policy Policy, client *http.Client, destination string, progress func(DownloadProgress)) (Manifest, error) {
	m, err := Verify(envelope, policy)
	if err != nil {
		return Manifest{}, err
	}
	if !filepath.IsAbs(destination) {
		return Manifest{}, errors.New("absolute destination required")
	}
	for path := filepath.Dir(destination); ; path = filepath.Dir(path) {
		info, e := os.Lstat(path)
		if e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return Manifest{}, errors.New("destination parent unavailable")
		}
		if filepath.Dir(path) == path {
			break
		}
	}
	if _, err = os.Lstat(destination); !os.IsNotExist(err) {
		return Manifest{}, errors.New("destination already exists or unavailable")
	}
	if client == nil {
		client = &http.Client{}
	}
	bounded := *client
	bounded.CheckRedirect = CheckReleaseRedirect
	// Release installers can exceed 60 MiB. A one-minute total deadline cuts
	// off valid transfers on slower links. Keep a finite ceiling while honoring
	// an earlier caller deadline and explicit cancellation.
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.URL, nil)
	if err != nil {
		return Manifest{}, err
	}
	req.Header.Set("Accept-Encoding", "identity")
	response, err := bounded.Do(req)
	if err != nil {
		return Manifest{}, errors.New("update download failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.Uncompressed || response.ContentLength >= 0 && response.ContentLength != m.Size {
		return Manifest{}, errors.New("unexpected update response")
	}
	file, err := os.CreateTemp(filepath.Dir(destination), ".webdavmount-update-*.tmp")
	if err != nil {
		return Manifest{}, err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	writer := &progressWriter{destination: file, total: m.Size, progress: progress}
	n, err := io.Copy(writer, io.LimitReader(response.Body, m.Size+1))
	if err != nil || n != m.Size {
		return Manifest{}, errors.New("update length mismatch")
	}
	if err = ctx.Err(); err != nil {
		return Manifest{}, err
	}
	if progress != nil {
		progress(DownloadProgress{DownloadedBytes: n, TotalBytes: m.Size, Verifying: true})
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		return Manifest{}, err
	}
	if err = CheckArtifact(file, m); err != nil {
		return Manifest{}, err
	}
	if err = ctx.Err(); err != nil {
		return Manifest{}, err
	}
	if err = file.Sync(); err != nil {
		return Manifest{}, err
	}
	if err = file.Close(); err != nil {
		return Manifest{}, err
	}
	// Link provides no-overwrite publication on supported local filesystems.
	// Unsupported filesystems fail rather than falling back to overwrite rename.
	if err = os.Link(file.Name(), destination); err != nil {
		return Manifest{}, err
	}
	return m, nil
}
