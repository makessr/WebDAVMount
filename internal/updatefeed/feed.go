// Package updatefeed verifies signed update metadata. Downloads require an
// explicit call; it never launches an installer or changes the installed version.
package updatefeed

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Manifest struct {
	Product string `json:"product"`
	Version string `json:"version"`
	URL     string `json:"url"`
	Size    int64  `json:"size"`
	SHA256  string `json:"sha256"`
	Expires string `json:"expires"`
}
type Envelope struct {
	Payload   []byte `json:"payload"`
	Signature []byte `json:"signature"`
}
type Policy struct {
	Product, CurrentVersion string
	PublicKey               ed25519.PublicKey
	AllowedHosts            []string
	GitHubRepository        string
	MaxBytes                int64
	Now                     time.Time
}

func signingMessage(payload []byte) []byte {
	return append([]byte("WebDAVMount/update-feed/v1\x00"), payload...)
}

func strict(data []byte, target any) error {
	allowed := map[string]bool{}
	switch target.(type) {
	case *Manifest:
		for _, k := range []string{"product", "version", "url", "size", "sha256", "expires"} {
			allowed[k] = true
		}
	case *Envelope:
		allowed["payload"] = true
		allowed["signature"] = true
	default:
		return errors.New("unknown schema")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	tok, e := d.Token()
	if e != nil || tok != json.Delim('{') {
		return errors.New("object required")
	}
	seen := map[string]bool{}
	for d.More() {
		tok, e = d.Token()
		if e != nil {
			return e
		}
		key, ok := tok.(string)
		if !ok || !allowed[key] || seen[key] {
			return errors.New("unknown, duplicate or non-exact JSON key")
		}
		seen[key] = true
		tok, e = d.Token()
		if e != nil {
			return e
		}
		if tok == nil {
			return errors.New("null field")
		}
		if _, nested := tok.(json.Delim); nested {
			return errors.New("nested values refused")
		}
	}
	tok, e = d.Token()
	if e != nil || tok != json.Delim('}') || len(seen) != len(allowed) {
		return errors.New("incomplete object")
	}
	if _, e = d.Token(); e != io.EOF {
		return errors.New("extra JSON")
	}
	d = json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	return d.Decode(target)
}
func version(s string) ([3]uint64, error) {
	var v [3]uint64
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return v, errors.New("invalid version")
	}
	for i, p := range parts {
		if p == "" || len(p) > 1 && p[0] == '0' {
			return v, errors.New("invalid version")
		}
		for _, c := range p {
			if c < '0' || c > '9' {
				return v, errors.New("invalid version")
			}
		}
		n, e := strconv.ParseUint(p, 10, 32)
		if e != nil {
			return v, e
		}
		v[i] = n
	}
	return v, nil
}

// ErrCurrentVersion is returned only after all trust checks pass for the exact
// current version. A downgraded manifest remains a rejection.
var ErrCurrentVersion = errors.New("verified current version")

// ErrOlderVersion distinguishes a fully trusted but older release from a
// malformed/untrusted feed. Download still rejects it, preventing downgrades.
var ErrOlderVersion = errors.New("verified older version")

func Verify(data []byte, p Policy) (Manifest, error) {
	var m Manifest
	fail := func() (Manifest, error) { return Manifest{}, errors.New("update manifest rejected") }
	if len(data) > 65536 || len(p.PublicKey) != ed25519.PublicKeySize || p.Product == "" || p.MaxBytes <= 0 || p.MaxBytes > 2<<30 || p.Now.IsZero() {
		return fail()
	}
	var env Envelope
	if strict(data, &env) != nil || !ed25519.Verify(p.PublicKey, signingMessage(env.Payload), env.Signature) {
		return fail()
	}
	if strict(env.Payload, &m) != nil || m.Product != p.Product || m.Size <= 0 || m.Size > p.MaxBytes {
		return fail()
	}
	next, e := version(m.Version)
	if e != nil {
		return fail()
	}
	current, e := version(p.CurrentVersion)
	if e != nil {
		return fail()
	}
	newer := false
	for i := range next {
		if next[i] != current[i] {
			newer = next[i] > current[i]
			break
		}
	}
	expiry, e := time.Parse(time.RFC3339, m.Expires)
	if e != nil || !expiry.After(p.Now) {
		return fail()
	}
	sum, e := hex.DecodeString(m.SHA256)
	if e != nil || len(sum) != sha256.Size || strings.ToLower(m.SHA256) != m.SHA256 {
		return fail()
	}
	u, e := url.Parse(m.URL)
	if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" || u.RawQuery != "" || u.Port() != "" && u.Port() != "443" {
		return fail()
	}
	allowed := false
	for _, host := range p.AllowedHosts {
		if strings.EqualFold(u.Hostname(), host) {
			allowed = true
		}
	}
	if !allowed || p.GitHubRepository != "" &&
		(!strings.EqualFold(u.Hostname(), "github.com") ||
			u.EscapedPath() != "/"+p.GitHubRepository+"/releases/download/v"+m.Version+"/"+m.Product+"-"+m.Version+"-setup.exe") {
		return fail()
	}
	if !newer {
		if next == current {
			return Manifest{}, ErrCurrentVersion
		}
		return Manifest{}, ErrOlderVersion
	}
	return m, nil
}

// CheckArtifact validates bytes from a caller-owned stream. Call Verify first;
// this function does not authenticate a Manifest constructed by the caller.
func CheckArtifact(r io.Reader, m Manifest) error {
	if m.Size <= 0 || m.Size > 2<<30 {
		return errors.New("invalid artifact size")
	}
	expected, e := hex.DecodeString(m.SHA256)
	if e != nil || len(expected) != sha256.Size {
		return errors.New("invalid artifact hash")
	}
	h := sha256.New()
	n, e := io.Copy(h, io.LimitReader(r, m.Size+1))
	if e != nil {
		return e
	}
	if n != m.Size || !bytes.Equal(h.Sum(nil), expected) {
		return errors.New("artifact mismatch")
	}
	return nil
}
