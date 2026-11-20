// Package connectionbackup is the portable, password-encrypted client connection
// format. It never contains author keys, licensing identities or machine paths.
package connectionbackup

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"
	"webdavmount/internal/providers"
)

const (
	MaxBytes       = 2 << 20
	MaxConnections = 200
	MaxNoteLength  = 120
	MaxPresets     = 16
	iterations     = 600000
	headerSize     = 60
	magic          = "WDMCON01"
)

var (
	ErrInvalid  = errors.New("备份文件无效、版本不支持或内容超出限制")
	ErrUnlock   = errors.New("备份密码不正确，或备份文件已损坏")
	ErrPassword = errors.New("备份密码须为12至128个字符，且不超过512字节")
)

type Connection struct {
	Storage   *providers.ObjectOptions `json:"storage,omitempty"`
	Provider  string                   `json:"provider,omitempty"`
	HostKey   string                   `json:"hostKey,omitempty"`
	Name      string                   `json:"name"`
	URL       string                   `json:"url"`
	User      string                   `json:"user"`
	Password  string                   `json:"password"`
	Drive     string                   `json:"drive"`
	Group     string                   `json:"group,omitempty"`
	Note      string                   `json:"note,omitempty"`
	Favorites []Favorite               `json:"favorites,omitempty"`
}
type Favorite struct {
	Name string `json:"name"`
	Path string `json:"path"`
}
type Preset struct {
	Name    string `json:"name"`
	Members []int  `json:"members"`
}
type Archive struct {
	Version           int          `json:"version"`
	CreatedAt         string       `json:"createdAt"`
	IncludesPasswords bool         `json:"includesPasswords"`
	Connections       []Connection `json:"connections"`
	Presets           []Preset     `json:"presets,omitempty"`
}

func ValidPassword(password string) bool {
	n := utf8.RuneCountInString(password)
	return utf8.ValidString(password) && n >= 12 && n <= 128 && len(password) <= 512 && strings.TrimSpace(password) != ""
}

func ValidNote(note string) bool {
	return validLabel(note, MaxNoteLength, true)
}

func valid(a Archive) bool {
	if (a.Version < 1 || a.Version > 6) || len(a.CreatedAt) > 64 || len(a.Connections) == 0 || len(a.Connections) > MaxConnections {
		return false
	}
	if len(a.Presets) > MaxPresets || (len(a.Presets) != 0 && a.Version < 6) {
		return false
	}
	for i, preset := range a.Presets {
		if !validLabel(preset.Name, 32, false) || len(preset.Members) == 0 || len(preset.Members) > len(a.Connections) {
			return false
		}
		for _, other := range a.Presets[:i] {
			if strings.EqualFold(other.Name, preset.Name) {
				return false
			}
		}
		seen := map[int]bool{}
		for _, index := range preset.Members {
			if index < 0 || index >= len(a.Connections) || seen[index] {
				return false
			}
			seen[index] = true
		}
	}
	groups := map[string]bool{}
	for _, c := range a.Connections {
		if a.Version == 1 && (c.Group != "" || len(c.Favorites) != 0) {
			return false
		}
		if !ValidNote(c.Note) || (a.Version < 5 && c.Note != "") {
			return false
		}
		if !validLabel(c.Group, 32, true) || c.Group == "未分组" || len(c.Favorites) > 100 {
			return false
		}
		if c.Group != "" {
			groups[c.Group] = true
		}
		seen := map[string]bool{}
		for _, f := range c.Favorites {
			if !validLabel(f.Name, 80, false) || !utf8.ValidString(f.Path) || len(f.Path) > 4096 || strings.ContainsAny(f.Path, "\\\x00\r\n") || strings.HasPrefix(f.Path, "/") || seen[f.Path] {
				return false
			}
			if f.Path != "" && (path.Clean(f.Path) != f.Path || f.Path == "." || f.Path == ".." || strings.HasPrefix(f.Path, "../")) {
				return false
			}
			for _, r := range f.Path {
				if r < 32 || r == 127 {
					return false
				}
			}
			seen[f.Path] = true
		}
		if a.Version < 3 && (c.Provider != "" || c.HostKey != "") {
			return false
		}
		if c.Storage != nil && (a.Version < 4 || !a.IncludesPasswords && c.Storage.SessionToken != "") {
			return false
		}
		if _, _, err := providers.ObjectEngineConfig(c.Provider, c.URL, c.User, c.Password, c.HostKey, c.Storage); err != nil {
			return false
		}
		if providers.IsOAuth(c.Provider) && c.Password != "" {
			return false
		}
		if strings.TrimSpace(c.Name) == "" || len(c.Name) > 320 || len(c.URL) > 8192 || len(c.User) > 4096 || len(c.Password) > 16384 {
			return false
		}
		if len(c.Drive) != 2 || c.Drive[0] < 'D' || c.Drive[0] > 'Z' || c.Drive[1] != ':' {
			return false
		}
		if !a.IncludesPasswords && c.Password != "" {
			return false
		}
		for _, s := range []string{c.Name, c.URL, c.User, c.Password} {
			if !utf8.ValidString(s) || strings.ContainsRune(s, 0) {
				return false
			}
		}
	}
	return len(groups) <= 64
}
func validLabel(s string, max int, empty bool) bool {
	if !utf8.ValidString(s) || strings.TrimSpace(s) != s || utf8.RuneCountInString(s) > max || (!empty && s == "") {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func aead(password string, salt []byte) (cipher.AEAD, error) {
	key, err := pbkdf2.Key(sha256.New, password, salt, iterations, 32)
	if err != nil {
		return nil, err
	}
	defer clear(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func Seal(a Archive, password string) ([]byte, error) {
	if !ValidPassword(password) {
		return nil, ErrPassword
	}
	if !valid(a) {
		return nil, ErrInvalid
	}
	plain, err := json.Marshal(a)
	if err != nil {
		return nil, ErrInvalid
	}
	defer clear(plain)
	if len(plain) > MaxBytes-headerSize-16 {
		return nil, ErrInvalid
	}
	header := make([]byte, headerSize)
	copy(header, magic)
	binary.BigEndian.PutUint32(header[8:12], iterations)
	if _, err = rand.Read(header[12:56]); err != nil {
		return nil, err
	}
	binary.BigEndian.PutUint32(header[56:60], uint32(len(plain)+16))
	c, err := aead(password, header[12:44])
	if err != nil {
		return nil, err
	}
	return c.Seal(header, header[44:56], plain, header[:headerSize]), nil
}

func Open(data []byte, password string) (Archive, error) {
	if !ValidPassword(password) {
		return Archive{}, ErrPassword
	}
	if len(data) < headerSize+16 || len(data) > MaxBytes || string(data[:8]) != magic || binary.BigEndian.Uint32(data[8:12]) != iterations || int(binary.BigEndian.Uint32(data[56:60])) != len(data)-headerSize {
		return Archive{}, ErrInvalid
	}
	c, err := aead(password, data[12:44])
	if err != nil {
		return Archive{}, err
	}
	plain, err := c.Open(nil, data[44:56], data[headerSize:], data[:headerSize])
	if err != nil {
		return Archive{}, ErrUnlock
	}
	defer clear(plain)
	var a Archive
	d := json.NewDecoder(bytes.NewReader(plain))
	d.DisallowUnknownFields()
	if d.Decode(&a) != nil || d.Decode(new(any)) != io.EOF || !valid(a) {
		return Archive{}, ErrInvalid
	}
	return a, nil
}
