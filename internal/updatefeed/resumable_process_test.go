package updatefeed

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type transferChildConfig struct {
	Envelope    []byte
	Policy      Policy
	Address     string
	Certificate []byte
	Cache       string
	Destination string
}

func childTransferClient(config transferChildConfig) *http.Client {
	certificate, e := x509.ParseCertificate(config.Certificate)
	if e != nil {
		panic(e)
	}
	roots := x509.NewCertPool()
	roots.AddCert(certificate)
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}, ForceAttemptHTTP2: true, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", config.Address)
	}}
	return &http.Client{Transport: transport}
}
func TestTransferCrashHelper(t *testing.T) {
	if os.Getenv("WEBDAVMOUNT_TRANSFER_CHILD") != "1" {
		t.Skip("isolated subprocess helper")
	}
	data, e := os.ReadFile(os.Getenv("WEBDAVMOUNT_TRANSFER_CHILD_CONFIG"))
	if e != nil {
		t.Fatal(e)
	}
	var config transferChildConfig
	if json.Unmarshal(data, &config) != nil {
		t.Fatal("invalid synthetic child fixture")
	}
	_, e = DownloadResumable(context.Background(), config.Envelope, config.Policy, childTransferClient(config), config.Destination, TransferOptions{CacheDirectory: config.Cache, Connections: 1}, nil)
	if e != nil {
		t.Fatal(e)
	}
}

func TestResumableForcedProcessTerminationAndRealIndependentConnections(t *testing.T) {
	f := newTransferFixture(t)
	options := f.options(t)
	var crashPhase atomic.Bool
	crashPhase.Store(true)
	var firstRequests atomic.Int32
	var concurrent atomic.Int32
	gate := make(chan struct{})
	var gateOnce sync.Once
	var remotesMu sync.Mutex
	remotes := map[string]bool{}
	secondEntered := make(chan struct{})
	var secondOnce sync.Once
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var first, last int64
		if _, e := fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-%d", &first, &last); e != nil {
			w.WriteHeader(400)
			return
		}
		if last > 0 && first == 0 {
			firstRequests.Add(1)
		}
		if last > 0 && first >= TransferChunkSize {
			if crashPhase.Load() {
				secondOnce.Do(func() { close(secondEntered) })
				<-r.Context().Done()
				return
			}
			current := concurrent.Add(1)
			defer concurrent.Add(-1)
			remotesMu.Lock()
			remotes[r.RemoteAddr] = true
			remotesMu.Unlock()
			if current >= 2 {
				gateOnce.Do(func() { close(gate) })
			}
			select {
			case <-gate:
			case <-r.Context().Done():
				return
			}
		}
		w.Header().Set("ETag", `"fixture-v1"`)
		w.Header().Set("Content-Range", rangeHeader(first, last, int64(len(f.body))))
		w.Header().Set("Content-Length", strconv.FormatInt(last-first+1, 10))
		w.WriteHeader(206)
		w.Write(f.body[first : last+1])
	}))
	defer server.Close()
	// The signed origin uses port 443; a trusted fixture-only dialer routes it to
	// the local TLS server and validates its test CA, without disabling TLS checks.
	_, policy, key, _ := fixture(t)
	manifest := f.manifest
	manifest.URL = "https://127.0.0.1/setup.exe"
	policy.AllowedHosts = []string{"127.0.0.1"}
	policy.MaxBytes = 32 << 20
	config := transferChildConfig{Envelope: signed(t, manifest, key), Policy: policy, Address: server.Listener.Addr().String(), Certificate: server.Certificate().Raw, Cache: options.CacheDirectory, Destination: filepath.Join(t.TempDir(), "child.exe")}
	configPath := filepath.Join(t.TempDir(), "fixture.json")
	data, _ := json.Marshal(config)
	os.WriteFile(configPath, data, 0600)
	command := exec.Command(os.Args[0], "-test.run=^TestTransferCrashHelper$", "-test.timeout=30s")
	command.Env = append(os.Environ(), "WEBDAVMOUNT_TRANSFER_CHILD=1", "WEBDAVMOUNT_TRANSFER_CHILD_CONFIG="+configPath)
	if e := command.Start(); e != nil {
		t.Fatal(e)
	}
	defer func() {
		if command.ProcessState == nil {
			command.Process.Kill()
			command.Wait()
		}
	}()
	select {
	case <-secondEntered:
	case <-time.After(12 * time.Second):
		t.Fatal("child did not persist first chunk and request second")
	}
	if !regularTransferFile(filepath.Join(options.CacheDirectory, "part-00000.json")) {
		t.Fatal("first receipt missing before crash")
	}
	if e := command.Process.Kill(); e != nil {
		t.Fatal(e)
	}
	command.Wait()
	crashPhase.Store(false)
	assertNotPublished(t, config.Destination)
	target := filepath.Join(t.TempDir(), "resumed.exe")
	options.Connections = 2
	var resumed int64
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	if _, e := DownloadResumable(ctx, config.Envelope, policy, childTransferClient(config), target, options, func(p DownloadProgress) { resumed = p.ResumedBytes }); e != nil {
		t.Fatal(e)
	}
	assertDownloaded(t, target, f.body)
	if resumed != TransferChunkSize || firstRequests.Load() != 1 {
		t.Fatal("crash checkpoint was not reused", resumed, firstRequests.Load())
	}
	remotesMu.Lock()
	count := len(remotes)
	remotesMu.Unlock()
	if count != 2 {
		t.Fatal("workers did not use independent TCP connections", count)
	}
}
