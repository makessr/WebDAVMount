package updatefeed

import (
	"crypto/ed25519"
	"encoding/json"
	"strings"
	"testing"
)

func TestStrictSchemaAndPolicyRefusals(t *testing.T) {
	m, p, k, _ := fixture(t)
	plain, _ := json.Marshal(m)
	for _, name := range []string{"case-alias", "null", "nested", "deep", "missing", "trailing"} {
		t.Run(name, func(t *testing.T) {
			body := string(plain)
			switch name {
			case "case-alias":
				body = strings.Replace(body, `"product":`, `"Product":`, 1)
			case "null":
				body = strings.Replace(body, `"product":"WebDAVMount"`, `"product":null`, 1)
			case "nested":
				body = strings.Replace(body, `"product":"WebDAVMount"`, `"product":{}`, 1)
			case "deep":
				body = `{"product":` + strings.Repeat("[", 10000)
			case "missing":
				body = strings.Replace(body, `"product":"WebDAVMount",`, "", 1)
			case "trailing":
				body += " {}"
			}
			b, _ := json.Marshal(Envelope{[]byte(body), ed25519.Sign(k, signingMessage([]byte(body)))})
			if _, e := Verify(b, p); e == nil {
				t.Fatal("accepted malformed signed payload")
			}
		})
	}
	envelope := signed(t, m, k)
	for _, message := range [][]byte{plain, append([]byte("WebDAVMount/license/v1\x00"), plain...)} {
		wrongDomain, _ := json.Marshal(Envelope{plain, ed25519.Sign(k, message)})
		if _, e := Verify(wrongDomain, p); e == nil {
			t.Fatal("cross-protocol signature accepted")
		}
	}
	for _, b := range [][]byte{[]byte(strings.Replace(string(envelope), `"payload":`, `"Payload":`, 1)), []byte(`{"payload":null,"signature":null}`), append(envelope, []byte(` {}`)...)} {
		if _, e := Verify(b, p); e == nil {
			t.Fatal("accepted malformed envelope")
		}
	}
	p.AllowedHosts = nil
	if _, e := Verify(envelope, p); e == nil {
		t.Fatal("empty host allowlist accepted")
	}
}
