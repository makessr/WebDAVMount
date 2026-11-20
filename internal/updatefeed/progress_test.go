package updatefeed

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

type smallChunks struct{ reader *bytes.Reader }

func (r smallChunks) Read(p []byte) (int, error) {
	if len(p) > 5 {
		p = p[:5]
	}
	return r.reader.Read(p)
}

func TestDownloadProgressBeforePublication(t *testing.T) {
	m, p, k, body := fixture(t)
	destination := filepath.Join(t.TempDir(), "update.exe")
	client := &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, ContentLength: int64(len(body)), Body: io.NopCloser(smallChunks{bytes.NewReader(body)}), Header: http.Header{}, Request: r}, nil
	})}
	var events []DownloadProgress
	_, err := DownloadWithProgress(context.Background(), signed(t, m, k), p, client, destination, func(event DownloadProgress) {
		if _, e := os.Stat(destination); !os.IsNotExist(e) {
			t.Fatal("published before verification")
		}
		if event.TotalBytes != int64(len(body)) {
			t.Fatal("untrusted total")
		}
		events = append(events, event)
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) < 3 || events[0].DownloadedBytes >= m.Size || !events[len(events)-1].Verifying {
		t.Fatal("missing live progress or verification phase", events)
	}
	for i := 1; i < len(events); i++ {
		if events[i].DownloadedBytes < events[i-1].DownloadedBytes {
			t.Fatal("progress regressed")
		}
	}
	data, err := os.ReadFile(destination)
	if err != nil || !bytes.Equal(data, body) {
		t.Fatal("published content mismatch")
	}
}

func TestDownloadProgressDoesNotBypassHashFailure(t *testing.T) {
	m, p, k, body := fixture(t)
	body[0] ^= 1
	client := &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, ContentLength: int64(len(body)), Body: io.NopCloser(bytes.NewReader(body)), Header: http.Header{}, Request: r}, nil
	})}
	destination := filepath.Join(t.TempDir(), "update.exe")
	_, err := DownloadWithProgress(context.Background(), signed(t, m, k), p, client, destination, func(DownloadProgress) {})
	if err == nil {
		t.Fatal("tampered payload accepted")
	}
	if _, err = os.Stat(destination); !os.IsNotExist(err) {
		t.Fatal("tampered destination published")
	}
	files, err := os.ReadDir(filepath.Dir(destination))
	if err != nil || len(files) != 0 {
		t.Fatal("partial file remains")
	}
}

func TestProgressCancellationAtVerificationDoesNotPublish(t *testing.T) {
	m, p, k, body := fixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, ContentLength: int64(len(body)), Body: io.NopCloser(bytes.NewReader(body)), Header: http.Header{}, Request: r}, nil
	})}
	destination := filepath.Join(t.TempDir(), "update.exe")
	_, err := DownloadWithProgress(ctx, signed(t, m, k), p, client, destination, func(progress DownloadProgress) {
		if progress.Verifying {
			cancel()
		}
	})
	if err == nil {
		t.Fatal("cancelled verification published")
	}
	files, err := os.ReadDir(filepath.Dir(destination))
	if err != nil || len(files) != 0 {
		t.Fatal("cancelled download left files")
	}
}
