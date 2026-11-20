package updatefeed

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T) (Manifest, Policy, ed25519.PrivateKey, []byte) {
	t.Helper()
	pub, priv, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	body := []byte("synthetic update, never executable")
	sum := sha256.Sum256(body)
	return Manifest{"WebDAVMount", "0.24.0", "https://updates.example/package.exe", int64(len(body)), hex.EncodeToString(sum[:]), "2027-01-01T00:00:00Z"}, Policy{
		Product: "WebDAVMount", CurrentVersion: "0.23.1", PublicKey: pub, AllowedHosts: []string{"updates.example"},
		MaxBytes: 1 << 20, Now: time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC),
	}, priv, body
}
func signed(t *testing.T, m Manifest, k ed25519.PrivateKey) []byte {
	t.Helper()
	b, e := json.Marshal(m)
	if e != nil {
		t.Fatal(e)
	}
	out, e := json.Marshal(Envelope{b, ed25519.Sign(k, signingMessage(b))})
	if e != nil {
		t.Fatal(e)
	}
	return out
}
func TestVerifyRefusesUntrustedMetadata(t *testing.T) {
	m, p, k, _ := fixture(t)
	if _, e := Verify(signed(t, m, k), p); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"product", "same", "downgrade", "version", "http", "credentials", "host", "port", "query", "fragment", "size", "oversize", "hash", "expiry", "unknown", "duplicate", "bad-signature", "large"} {
		t.Run(name, func(t *testing.T) {
			v := m
			var data []byte
			switch name {
			case "product":
				v.Product = "Other"
			case "same":
				v.Version = p.CurrentVersion
			case "downgrade":
				v.Version = "0.1.0"
			case "version":
				v.Version = "00.24.0"
			case "http":
				v.URL = "http://updates.example/p"
			case "credentials":
				v.URL = "https://user:secret@updates.example/p"
			case "host":
				v.URL = "https://other.example/p"
			case "port":
				v.URL = "https://updates.example:8443/p"
			case "query":
				v.URL += "?token=x"
			case "fragment":
				v.URL += "#x"
			case "size":
				v.Size = 0
			case "oversize":
				v.Size = p.MaxBytes + 1
			case "hash":
				v.SHA256 = "invalid"
			case "expiry":
				v.Expires = "2020-01-01T00:00:00Z"
			}
			data = signed(t, v, k)
			if name == "unknown" || name == "duplicate" {
				b, _ := json.Marshal(v)
				extra := `,"extra":1}`
				if name == "duplicate" {
					extra = `,"product":"WebDAVMount"}`
				}
				b = append(b[:len(b)-1], []byte(extra)...)
				data, _ = json.Marshal(Envelope{b, ed25519.Sign(k, signingMessage(b))})
			}
			if name == "bad-signature" {
				var env Envelope
				json.Unmarshal(data, &env)
				env.Signature[0] ^= 1
				data, _ = json.Marshal(env)
			}
			if name == "large" {
				data = []byte(strings.Repeat("x", 65537))
			}
			if _, e := Verify(data, p); e == nil {
				t.Fatal("accepted invalid manifest")
			}
		})
	}
}

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestDownloadValidatesBeforePublishing(t *testing.T) {
	for _, scenario := range []string{"success", "existing", "tampered", "truncated", "oversized", "redirect", "invalid-signature"} {
		t.Run(scenario, func(t *testing.T) {
			m, p, k, body := fixture(t)
			envelope := signed(t, m, k)
			if scenario == "invalid-signature" {
				envelope = []byte("invalid")
			}
			destination := filepath.Join(t.TempDir(), "update.exe")
			if scenario == "existing" {
				os.WriteFile(destination, []byte("keep"), 0600)
			}
			calls := 0
			client := &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.Scheme != "https" || r.URL.Host != "updates.example" {
					t.Error("unexpected endpoint")
				}
				payload := append([]byte{}, body...)
				status := 200
				headers := http.Header{}
				switch scenario {
				case "tampered":
					payload[0] ^= 1
				case "truncated":
					payload = payload[:len(payload)-1]
				case "oversized":
					payload = append(payload, 'x')
				case "redirect":
					status = 302
					headers.Set("Location", "https://attacker.example/p")
				}
				return &http.Response{StatusCode: status, Body: io.NopCloser(bytes.NewReader(payload)), ContentLength: -1, Header: headers, Request: r}, nil
			})}
			_, e := Download(context.Background(), envelope, p, client, destination)
			if scenario == "success" {
				if e != nil {
					t.Fatal(e)
				}
				got, _ := os.ReadFile(destination)
				if !bytes.Equal(got, body) {
					t.Fatal("wrong bytes")
				}
			} else {
				if e == nil {
					t.Fatal("bad download accepted")
				}
				if scenario == "existing" {
					got, _ := os.ReadFile(destination)
					if string(got) != "keep" {
						t.Fatal("existing overwritten")
					}
				} else if _, e := os.Stat(destination); !os.IsNotExist(e) {
					t.Fatal("failed download published")
				}
			}
			if (scenario == "existing" || scenario == "invalid-signature") && calls != 0 {
				t.Fatal("request before validation")
			}
			files, _ := filepath.Glob(filepath.Join(filepath.Dir(destination), "*.tmp"))
			if len(files) != 0 {
				t.Fatal("temporary residue")
			}
		})
	}
}
