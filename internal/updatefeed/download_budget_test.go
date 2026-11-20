package updatefeed

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Validate the request's actual budget without making a regression test wait
// minutes. A slow update must have >2 minutes, yet remain bounded and cancellable.
func TestDownloadBudgetAllowsSlowNetworksAndHonorsCaller(t *testing.T) {
	for _, shortCaller := range []bool{false, true} {
		t.Run(map[bool]string{false: "slow network budget", true: "earlier caller deadline"}[shortCaller], func(t *testing.T) {
			m, p, key, body := fixture(t)
			envelope := signed(t, m, key)
			ctx := context.Background()
			var cancel context.CancelFunc
			if shortCaller {
				ctx, cancel = context.WithTimeout(ctx, time.Second)
				defer cancel()
			}
			client := &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
				deadline, ok := r.Context().Deadline()
				remaining := time.Until(deadline)
				if !ok || remaining > 30*time.Minute || !shortCaller && remaining <= 2*time.Minute || shortCaller && remaining > time.Second {
					t.Error("unsuitable request budget", remaining, ok)
				}
				return &http.Response{StatusCode: 200, ContentLength: int64(len(body)), Body: io.NopCloser(bytes.NewReader(body)), Header: http.Header{}, Request: r}, nil
			})}
			path := filepath.Join(t.TempDir(), "setup.exe")
			if _, e := Download(ctx, envelope, p, client, path); e != nil {
				t.Fatal(e)
			}
			if _, e := os.Stat(path); e != nil {
				t.Fatal(e)
			}
		})
	}
}
