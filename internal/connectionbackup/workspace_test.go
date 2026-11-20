package connectionbackup

import (
	"reflect"
	"testing"
)

func TestWorkspaceArchiveMetadataRoundTripAndValidation(t *testing.T) {
	a := fixture()
	a.Version = 2
	a.Connections[0].Group = "工作"
	a.Connections[0].Favorites = []Favorite{{Name: "根目录", Path: ""}, {Name: "文档", Path: "中文/文档"}}
	b, e := Seal(a, testPassword)
	if e != nil {
		t.Fatal(e)
	}
	got, e := Open(b, testPassword)
	if e != nil || !reflect.DeepEqual(got, a) {
		t.Fatal(got, e)
	}
	for _, p := range []string{"../secret", "a/../b", "/root", `a\b`, "a//b", "a/", "a\x00b", "a\nb", "."} {
		t.Run(p, func(t *testing.T) {
			bad := a
			bad.Connections = append([]Connection(nil), a.Connections...)
			bad.Connections[0].Favorites = []Favorite{{Name: "坏路径", Path: p}}
			if valid(bad) {
				t.Fatal("unsafe path accepted")
			}
		})
	}
	a.Version = 1
	if valid(a) {
		t.Fatal("metadata accepted as legacy archive")
	}
	a.Version = 2
	a.Connections[0].Favorites = append(a.Connections[0].Favorites, Favorite{Name: "重复根", Path: ""})
	if valid(a) {
		t.Fatal("duplicate favorite accepted")
	}
}
