package connectionbackup

import (
	"bytes"
	"encoding/binary"
	"errors"
	"reflect"
	"testing"
)

const testPassword = "only-a-test-password-2026"

func fixture() Archive {
	return Archive{Version: 1, CreatedAt: "2026-11-17T00:00:00Z", IncludesPasswords: true, Connections: []Connection{{Name: "测试网盘", URL: "https://dav.example.com/中文/", User: "tester", Password: "fixture-credential", Drive: "W:"}}}
}
func TestPortableArchiveRoundTripAndRandomness(t *testing.T) {
	a := fixture()
	data, e := Seal(a, testPassword)
	if e != nil {
		t.Fatal(e)
	}
	second, e := Seal(a, testPassword)
	if e != nil {
		t.Fatal(e)
	}
	if bytes.Equal(data, second) || bytes.Contains(data, []byte(a.Connections[0].Password)) || bytes.Contains(data, []byte(a.Connections[0].URL)) {
		t.Fatal("plaintext or deterministic encryption")
	}
	got, e := Open(data, testPassword)
	if e != nil || !reflect.DeepEqual(got.Connections[0], a.Connections[0]) {
		t.Fatal(got, e)
	}
	if _, e = Open(data, "a-different-test-password"); !errors.Is(e, ErrUnlock) {
		t.Fatal(e)
	}
	for _, index := range []int{12, 44, 60, len(data) - 1} {
		tampered := append([]byte(nil), data...)
		tampered[index] ^= 1
		if _, e = Open(tampered, testPassword); !errors.Is(e, ErrUnlock) {
			t.Fatal(index, e)
		}
	}
}
func TestArchiveRejectsBoundsAndUnsafeData(t *testing.T) {
	a := fixture()
	data, _ := Seal(a, testPassword)
	for _, b := range [][]byte{nil, data[:10], append(append([]byte(nil), data...), 0), bytes.Repeat([]byte{1}, MaxBytes+1)} {
		if _, e := Open(b, testPassword); !errors.Is(e, ErrInvalid) {
			t.Fatal(e)
		}
	}
	binary.BigEndian.PutUint32(data[8:12], 1)
	if _, e := Open(data, testPassword); !errors.Is(e, ErrInvalid) {
		t.Fatal(e)
	}
	for _, password := range []string{"short", "            ", string(bytes.Repeat([]byte{'x'}, 129))} {
		if _, e := Seal(a, password); !errors.Is(e, ErrPassword) {
			t.Fatal(e)
		}
	}
	for _, endpoint := range []string{"https://user:secret@dav.example.com/", "https://dav.example.com/?token=secret", "http://remote.example.com/"} {
		a = fixture()
		a.Connections[0].URL = endpoint
		if _, e := Seal(a, testPassword); !errors.Is(e, ErrInvalid) {
			t.Fatal(endpoint, e)
		}
	}
	a = fixture()
	a.IncludesPasswords = false
	if _, e := Seal(a, testPassword); !errors.Is(e, ErrInvalid) {
		t.Fatal(e)
	}
	a.Connections[0].Password = ""
	if _, e := Seal(a, testPassword); e != nil {
		t.Fatal(e)
	}
}
