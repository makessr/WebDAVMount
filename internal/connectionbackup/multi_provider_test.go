package connectionbackup

import (
	"reflect"
	"testing"
)

func TestMultiProviderArchivePreservesTypeAndRejectsUnsafeEndpoints(t *testing.T) {
	const password = "isolated fixture passphrase"
	a := fixture()
	a.Version = 3
	a.Connections = append(a.Connections,
		Connection{Provider: "mega", Name: "MEGA", User: "user@example.invalid", Drive: "X:"},
		Connection{Provider: "r2", Name: "R2", URL: "https://0123456789abcdef0123456789abcdef.r2.cloudflarestorage.com/example-bucket", User: "test-access-id", Drive: "Y:"},
	)
	encoded, err := Seal(a, password)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := Open(encoded, password)
	if err != nil || !reflect.DeepEqual(a, decoded) {
		t.Fatal("provider format did not round-trip", err)
	}
	a.Version = 2
	if _, err := Seal(a, password); err == nil {
		t.Fatal("legacy schema accepted a typed provider")
	}
	a.Version = 3
	for _, bad := range []string{
		"https://0123456789abcdef0123456789abcdef.r2.cloudflarestorage.com.example.invalid/example-bucket",
		"https://user:pass@0123456789abcdef0123456789abcdef.r2.cloudflarestorage.com/example-bucket",
		"https://0123456789abcdef0123456789abcdef.r2.cloudflarestorage.com/example-bucket/nested",
	} {
		a.Connections[len(a.Connections)-1].URL = bad
		if _, err := Seal(a, password); err == nil {
			t.Fatal("invalid R2 endpoint or bucket accepted")
		}
	}
}
