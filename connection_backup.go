package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"
	"webdavmount/internal/connectionbackup"
	"webdavmount/internal/providers"

	"golang.org/x/sys/windows"
)

type ConnectionBackupResult struct {
	Path              string `json:"path"`
	Count             int    `json:"count"`
	IncludesPasswords bool   `json:"includesPasswords"`
	PresetCount       int    `json:"presetCount,omitempty"`
	PresetWarning     string `json:"presetWarning,omitempty"`
}
type ConnectionImportRow struct {
	Provider      string `json:"provider,omitempty"`
	Group         string `json:"group,omitempty"`
	FavoriteCount int    `json:"favoriteCount"`
	HasNote       bool   `json:"hasNote,omitempty"`
	Index         int    `json:"index"`
	Name          string `json:"name"`
	URL           string `json:"url"`
	User          string `json:"user"`
	OriginalDrive string `json:"originalDrive"`
	Drive         string `json:"drive"`
	Status        string `json:"status"`
	Message       string `json:"message"`
}
type ConnectionImportPreview struct {
	Token             string                `json:"token"`
	CreatedAt         string                `json:"createdAt"`
	ExpiresAt         string                `json:"expiresAt"`
	IncludesPasswords bool                  `json:"includesPasswords"`
	Rows              []ConnectionImportRow `json:"rows"`
	PresetCount       int                   `json:"presetCount,omitempty"`
}
type connectionImportSession struct {
	preview  ConnectionImportPreview
	archive  connectionbackup.Archive
	base     []Profile
	imported map[int]string
	timer    *time.Timer
}

func connectionBackupPath(path string) error {
	if !filepath.IsAbs(path) || len(path) < 3 || path[1] != ':' || strings.Contains(path[2:], ":") || strings.HasPrefix(path, `\\`) {
		return errors.New("请选择本机文件的完整路径")
	}
	for _, part := range strings.FieldsFunc(path[3:], func(r rune) bool { return r == '/' || r == '\\' }) {
		if part == "." || part == ".." || strings.TrimRight(part, " .") != part {
			return errors.New("备份路径不能包含相对目录、末尾空格或句点")
		}
		base := strings.ToUpper(strings.SplitN(part, ".", 2)[0])
		runes := []rune(base)
		if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" || (len(runes) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && strings.ContainsRune("123456789¹²³", runes[3])) {
			return errors.New("备份路径不能使用 Windows 设备名称")
		}
	}
	if err := noReparse(path); err != nil {
		return errors.New("备份路径不支持链接或目录联接")
	}
	if !strings.EqualFold(filepath.Ext(path), ".wdmconnections") {
		return errors.New("请选择 .wdmconnections 连接备份文件")
	}
	return nil
}

// Only ciphertext is written to a sibling; a no-replace move publishes it after
// flush. Existing files are never overwritten, including during save-dialog races.
func writeConnectionBackup(path string, data []byte) error {
	if err := connectionBackupPath(path); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".connection-backup-*.tmp")
	if err != nil {
		return errors.New("无法创建备份，请检查保存目录和权限")
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, err = f.Write(data); err != nil {
		return errors.New("备份写入失败")
	}
	if err = f.Sync(); err != nil {
		return errors.New("备份写入失败")
	}
	if err = f.Close(); err != nil {
		return errors.New("备份写入失败")
	}
	source, _ := windows.UTF16PtrFromString(f.Name())
	dest, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return errors.New("备份路径无效")
	}
	if err = windows.MoveFileEx(source, dest, 0); err != nil {
		return errors.New("未保存备份：文件已存在或保存目录不可用，请另选新文件名")
	}
	return nil
}

func (s *DesktopService) ExportConnectionBackup(ids []string, password string, includePasswords bool) (ConnectionBackupResult, error) {
	return s.exportConnectionBackup(ids, password, includePasswords, false)
}
func (s *DesktopService) ExportWorkspaceBackup(ids []string, password string, includePasswords bool) (ConnectionBackupResult, error) {
	return s.exportConnectionBackup(ids, password, includePasswords, true)
}
func (s *DesktopService) exportConnectionBackup(ids []string, password string, includePasswords, workspace bool) (ConnectionBackupResult, error) {
	ctx, finish, err := s.beginOperation("connections.backup")
	if err != nil {
		return ConnectionBackupResult{}, err
	}
	defer finish()
	if !connectionbackup.ValidPassword(password) {
		return ConnectionBackupResult{}, connectionbackup.ErrPassword
	}
	if len(ids) == 0 || len(ids) > connectionbackup.MaxConnections {
		return ConnectionBackupResult{}, errors.New("请选择1至200个网盘连接")
	}
	wanted := map[string]bool{}
	for _, id := range ids {
		if wanted[id] {
			return ConnectionBackupResult{}, errors.New("网盘选择重复")
		}
		wanted[id] = true
	}
	a := connectionbackup.Archive{Version: 1, CreatedAt: time.Now().UTC().Format(time.RFC3339), IncludesPasswords: includePasswords}
	if workspace {
		a.Version = 2
	}
	s.mu.Lock()
	positions := make(map[string]int)
	for _, p := range s.profiles {
		if wanted[p.ID] {
			positions[p.presetIdentity()] = len(a.Connections)
			entry := connectionbackup.Connection{Name: p.Name, URL: p.URL, User: p.User, Drive: p.Drive}
			if providers.Kind(p.Provider) != providers.WebDAV {
				entry.Provider = p.Provider
				entry.HostKey = p.HostKey
				a.Version = max(a.Version, 3)
			}
			if p.Storage != nil {
				a.Version = max(a.Version, 4)
				storage := *p.Storage
				if !includePasswords {
					storage.SessionToken = ""
				}
				entry.Storage = &storage
			}
			if workspace {
				entry.Group = p.Group
				if p.Note != "" {
					entry.Note = p.Note
					a.Version = 5
				}
				for _, f := range p.DirectoryFavorites {
					entry.Favorites = append(entry.Favorites, connectionbackup.Favorite{Name: f.Name, Path: f.Path})
				}
			}
			if includePasswords && !providers.IsOAuth(p.Provider) {
				entry.Password = p.Password
			}
			a.Connections = append(a.Connections, entry)
		}
	}
	var presetWarning string
	if workspace {
		presets, err := s.loadMountPresetsLocked()
		if err != nil {
			presetWarning = "本机挂载方案无法读取，本次已备份所选网盘。"
		}
		for _, preset := range presets {
			var members []int
			for _, id := range preset.ConnectionIDs {
				if position, selected := positions[id]; selected {
					members = append(members, position)
				}
			}
			if len(members) == len(preset.ConnectionIDs) {
				a.Presets = append(a.Presets, connectionbackup.Preset{Name: preset.Name, Members: members})
			}
		}
		if len(a.Presets) != 0 {
			a.Version = 6
		}
	}
	s.mu.Unlock()
	if len(a.Connections) != len(wanted) {
		return ConnectionBackupResult{}, errors.New("网盘列表已变化，请重新选择")
	}
	data, err := connectionbackup.Seal(a, password)
	if err != nil {
		return ConnectionBackupResult{}, err
	}
	defer clear(data)
	if ctx.Err() != nil {
		return ConnectionBackupResult{}, errors.New("操作已取消")
	}
	if s.chooseConnectionExport == nil {
		return ConnectionBackupResult{}, errors.New("当前环境无法选择备份位置")
	}
	path, err := s.chooseConnectionExport()
	if err != nil {
		return ConnectionBackupResult{}, errors.New("无法选择备份位置")
	}
	if path == "" {
		return ConnectionBackupResult{}, nil
	}
	if ctx.Err() != nil {
		return ConnectionBackupResult{}, errors.New("操作已取消")
	}
	if err = writeConnectionBackup(path, data); err != nil {
		return ConnectionBackupResult{}, err
	}
	return ConnectionBackupResult{Path: path, Count: len(a.Connections), IncludesPasswords: includePasswords, PresetCount: len(a.Presets), PresetWarning: presetWarning}, nil
}

func connectionKey(endpoint, user string) string {
	u, err := url.Parse(endpoint)
	if err != nil {
		return endpoint + "\x00" + user
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	return strings.TrimRight(u.String(), "/") + "\x00" + user
}

func portableConnectionKey(provider, endpoint, user, name string) string {
	if providers.IsOAuth(provider) && user == "" {
		// Personal Google and Yandex grants are deliberately omitted from
		// backups. A type alone cannot identify their underlying account.
		return provider + "\x00name\x00" + name
	}
	return providers.Kind(provider) + "\x00" + connectionKey(endpoint, user)
}

// Plan deterministically and reserve every local physical/configured drive.
// Existing endpoint+username pairs are skipped, never overwritten.
func planConnectionImport(a connectionbackup.Archive, existing []Profile, mask uint32) []ConnectionImportRow {
	keys := map[string]bool{}
	names := map[string]bool{}
	drives := map[string]bool{}
	for i := 0; i < 26; i++ {
		if mask&(1<<i) != 0 {
			drives[string(rune('A'+i))+":"] = true
		}
	}
	for _, p := range existing {
		keys[portableConnectionKey(p.Provider, p.URL, p.User, p.Name)] = true
		names[p.Name] = true
		drives[p.Drive] = true
	}
	rows := make([]ConnectionImportRow, 0, len(a.Connections))
	for i, c := range a.Connections {
		r := ConnectionImportRow{Index: i, Name: c.Name, URL: c.URL, User: c.User, OriginalDrive: c.Drive, Drive: c.Drive, Status: "ready", Message: "可导入"}
		r.Group = c.Group
		r.Provider = c.Provider
		r.FavoriteCount = len(c.Favorites)
		r.HasNote = c.Note != ""
		key := portableConnectionKey(c.Provider, c.URL, c.User, c.Name)
		if keys[key] {
			r.Status = "duplicate"
			r.Drive = ""
			r.Message = "相同地址和用户名已存在，保留原连接"
			rows = append(rows, r)
			continue
		}
		if drives[r.Drive] {
			r.Drive = ""
			for d := 'D'; d <= 'Z'; d++ {
				candidate := string(d) + ":"
				if !drives[candidate] {
					r.Drive = candidate
					break
				}
			}
		}
		if r.Drive == "" {
			r.Status = "unavailable"
			r.Message = "没有可分配的盘符"
			rows = append(rows, r)
			continue
		}
		if names[r.Name] {
			for suffix := 1; ; suffix++ {
				name := fmt.Sprintf("%s（导入%d）", c.Name, suffix)
				if !names[name] {
					r.Name = name
					break
				}
			}
		}
		if r.Drive != c.Drive || r.Name != c.Name {
			r.Message = "名称或盘符已调整，请核对"
		}
		keys[key] = true
		names[r.Name] = true
		drives[r.Drive] = true
		rows = append(rows, r)
	}
	return rows
}

func (s *DesktopService) clearConnectionImportLocked() {
	if s.connectionImport != nil {
		if s.connectionImport.timer != nil {
			s.connectionImport.timer.Stop()
		}
		s.connectionImport.archive = connectionbackup.Archive{}
		s.connectionImport = nil
	}
}

func (s *DesktopService) PreviewConnectionImport(password string) (ConnectionImportPreview, error) {
	ctx, finish, err := s.beginOperation("connections.preview")
	if err != nil {
		return ConnectionImportPreview{}, err
	}
	defer finish()
	s.mu.Lock()
	s.clearConnectionImportLocked()
	s.mu.Unlock()
	if !connectionbackup.ValidPassword(password) {
		return ConnectionImportPreview{}, connectionbackup.ErrPassword
	}
	if s.chooseConnectionImport == nil {
		return ConnectionImportPreview{}, errors.New("当前环境无法选择备份文件")
	}
	path, err := s.chooseConnectionImport()
	if err != nil {
		return ConnectionImportPreview{}, errors.New("无法选择备份文件")
	}
	if path == "" {
		return ConnectionImportPreview{}, nil
	}
	if err = connectionBackupPath(path); err != nil {
		return ConnectionImportPreview{}, err
	}
	f, err := os.Open(path)
	if err != nil {
		return ConnectionImportPreview{}, errors.New("无法读取备份文件，请检查文件是否存在及访问权限")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > connectionbackup.MaxBytes {
		return ConnectionImportPreview{}, connectionbackup.ErrInvalid
	}
	data, err := io.ReadAll(io.LimitReader(f, connectionbackup.MaxBytes+1))
	if err != nil {
		return ConnectionImportPreview{}, errors.New("备份文件读取失败")
	}
	defer clear(data)
	a, err := connectionbackup.Open(data, password)
	if err != nil {
		return ConnectionImportPreview{}, err
	}
	if ctx.Err() != nil {
		return ConnectionImportPreview{}, errors.New("操作已取消")
	}
	mask, err := windows.GetLogicalDrives()
	if err != nil {
		return ConnectionImportPreview{}, errors.New("无法检查本机盘符，请稍后重试")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	preview := ConnectionImportPreview{Token: randomID(), CreatedAt: a.CreatedAt, ExpiresAt: now.Add(10 * time.Minute).UTC().Format(time.RFC3339), IncludesPasswords: a.IncludesPasswords, Rows: planConnectionImport(a, s.profiles, mask), PresetCount: len(a.Presets)}
	session := &connectionImportSession{preview: preview, archive: a, base: append([]Profile(nil), s.profiles...)}
	s.connectionImport = session
	session.timer = time.AfterFunc(10*time.Minute, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.connectionImport == session {
			s.clearConnectionImportLocked()
		}
	})
	return preview, nil
}

func (s *DesktopService) DiscardConnectionImport(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.connectionImport != nil && s.connectionImport.preview.Token == token {
		s.clearConnectionImportLocked()
	}
}

// Commit only selected, previewed rows in one encrypted settings replacement.
// Engine configuration failures roll back new identities; no mount/remote read
// is issued. A new machine's local DPAPI protects the imported configuration.
func (s *DesktopService) ApplyConnectionImport(token string, indices []int) (int, error) {
	ctx, finish, err := s.beginOperation("connections.import")
	if err != nil {
		return 0, err
	}
	defer finish()
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.connectionImport
	if p == nil || p.preview.Token != token {
		return 0, errors.New("导入预览已失效，请重新选择备份文件")
	}
	if p.imported != nil {
		return 0, errors.New("此备份中的连接已处理，可继续恢复挂载方案")
	}
	expires, _ := time.Parse(time.RFC3339, p.preview.ExpiresAt)
	if !time.Now().Before(expires) {
		s.clearConnectionImportLocked()
		return 0, errors.New("导入预览已过期，请重新选择备份文件")
	}
	if len(indices) == 0 || len(indices) > len(p.preview.Rows) {
		return 0, errors.New("请选择要导入的网盘")
	}
	mask, err := windows.GetLogicalDrives()
	if err != nil {
		return 0, errors.New("无法检查本机盘符")
	}
	if !sameConnectionImportBase(s.profiles, p.base) || !slices.Equal(planConnectionImport(p.archive, s.profiles, mask), p.preview.Rows) {
		s.clearConnectionImportLocked()
		return 0, errors.New("网盘列表或盘符已变化，请重新预览")
	}
	s.startupMu.Lock()
	starting := s.startupActive
	s.startupMu.Unlock()
	if starting {
		return 0, errors.New("正在自动挂载，请稍后重新导入")
	}
	wanted := map[int]bool{}
	for _, i := range indices {
		if i < 0 || i >= len(p.preview.Rows) || wanted[i] || p.preview.Rows[i].Status != "ready" {
			return 0, errors.New("导入选择无效，请重新核对")
		}
		wanted[i] = true
	}
	next := append([]Profile(nil), s.profiles...)
	groups := map[string]bool{}
	for _, existing := range next {
		if existing.Group != "" {
			groups[existing.Group] = true
		}
	}
	for _, row := range p.preview.Rows {
		if wanted[row.Index] && row.Group != "" {
			groups[row.Group] = true
		}
	}
	if len(groups) > connectionGroupLimit {
		return 0, errors.New("导入后的分组超过64个，请减少所选连接或先合并本机分组")
	}
	added := []Profile{}
	imported := make(map[int]string, len(wanted))
	rollback := func() {
		for _, entry := range added {
			s.call("config/delete", map[string]any{"name": entry.ID})
		}
	}
	for _, row := range p.preview.Rows {
		if !wanted[row.Index] {
			continue
		}
		if ctx.Err() != nil {
			rollback()
			return 0, errors.New("操作已取消")
		}
		c := p.archive.Connections[row.Index]
		entry := Profile{ID: "dav" + randomID(), Provider: c.Provider, HostKey: c.HostKey, Name: row.Name, URL: c.URL, User: c.User, Password: c.Password, Drive: row.Drive, AutoMount: false, Storage: c.Storage}
		entry.Group = c.Group
		entry.Note = c.Note
		for _, f := range c.Favorites {
			entry.DirectoryFavorites = append(entry.DirectoryFavorites, DirectoryFavorite{ID: randomID(), Name: f.Name, Path: f.Path})
		}
		if err = validateDirectoryFavorites(entry.DirectoryFavorites); err != nil {
			rollback()
			return 0, err
		}
		if err = validate(entry); err != nil {
			rollback()
			return 0, errors.New("备份中的网盘信息无效")
		}
		added = append(added, entry)
		if err = s.configure(entry); err != nil {
			rollback()
			return 0, errors.New("无法准备导入连接，原连接未修改，请检查挂载服务后重试")
		}
		next = append(next, entry)
		imported[row.Index] = entry.presetIdentity()
	}
	if err = checkConnectionImportContext(ctx); err != nil {
		rollback()
		return 0, err
	}
	if err = s.persist(next); err != nil {
		rollback()
		return 0, errors.New("导入未保存，原连接未修改，请检查本机配置目录权限")
	}
	s.profiles = next
	if len(p.archive.Presets) == 0 {
		s.clearConnectionImportLocked()
	} else {
		// Keep the unlocked archive for a separate, retryable preset commit.
		// The existing profile snapshot remains stale to prevent replaying import.
		p.imported = imported
		p.base = nil
		for i := range p.archive.Connections {
			p.archive.Connections[i].Password = ""
			p.archive.Connections[i].Storage = nil
		}
	}
	return len(added), nil
}

func sameConnectionImportBase(a, b []Profile) bool {
	return slices.EqualFunc(a, b, func(x, y Profile) bool {
		xFavorites, yFavorites := x.DirectoryFavorites, y.DirectoryFavorites
		x.DirectoryFavorites = nil
		y.DirectoryFavorites = nil
		return reflect.DeepEqual(x, y) && slices.Equal(xFavorites, yFavorites)
	})
}

func checkConnectionImportContext(ctx context.Context) error {
	if ctx.Err() != nil {
		return errors.New("操作已取消")
	}
	return nil
}
