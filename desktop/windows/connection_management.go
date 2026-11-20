package desktop

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/sys/windows"
	"webdavmount/internal/connectionbackup"
	"webdavmount/internal/providers"
)

type ConnectionChangeResult struct {
	Changed int    `json:"changed"`
	Warning string `json:"warning"`
}

type ConnectionCopyOptions struct {
	Name          string   `json:"name"`
	Drives        []string `json:"drives"`
	Group         string   `json:"group"`
	HasPassword   bool     `json:"hasPassword"`
	FavoriteCount int      `json:"favoriteCount"`
}

func selectedConnectionSet(profiles []Profile, ids []string) (map[string]bool, error) {
	if len(ids) < 1 || len(ids) > 200 {
		return nil, errors.New("请选择1至200个网盘")
	}
	selected := map[string]bool{}
	for _, id := range ids {
		if id == "" || selected[id] {
			return nil, errors.New("网盘选择有重复或无效项，请重新选择")
		}
		selected[id] = true
	}
	found := 0
	for _, p := range profiles {
		if selected[p.ID] {
			found++
		}
	}
	if found != len(selected) {
		return nil, errors.New("部分网盘已变化，请刷新后重新选择")
	}
	return selected, nil
}

func (s *DesktopService) connectionManagementReadyLocked() error {
	s.startupMu.Lock()
	defer s.startupMu.Unlock()
	if s.startupActive || s.startupRetryActive {
		return errors.New("请等待启动挂载任务结束后再管理连接")
	}
	return nil
}

// Auto-mount is a next-launch preference. This does not mount/unmount anything,
// change the current recovery intent, or recreate credentials/engine identities.
func (s *DesktopService) SetConnectionsAutoMount(ids []string, enabled bool) (result ConnectionChangeResult, err error) {
	started := time.Now()
	defer func() { s.recordDiagnostic("connections.autostart", err, time.Since(started)) }()
	ctx, finish, err := s.beginOperation("connections.autostart")
	if err != nil {
		return result, err
	}
	defer finish()
	s.mu.Lock()
	defer s.mu.Unlock()
	if err = s.connectionManagementReadyLocked(); err != nil {
		return result, err
	}
	selected, err := selectedConnectionSet(s.profiles, ids)
	if err != nil {
		return result, err
	}
	next := append([]Profile{}, s.profiles...)
	for i := range next {
		if selected[next[i].ID] && next[i].AutoMount != enabled {
			next[i].AutoMount = enabled
			result.Changed++
		}
	}
	if ctx.Err() != nil {
		return ConnectionChangeResult{}, errors.New("操作已取消")
	}
	if result.Changed == 0 {
		return result, nil
	}
	if err = s.persist(next); err != nil {
		return ConnectionChangeResult{}, errors.New("自动挂载设置保存失败，原设置已保留")
	}
	s.profiles = next
	return result, nil
}

// Preflight every selected identity before the single encrypted commit. A
// mounted/unknown selection refuses the whole batch, never partially deleting.
func (s *DesktopService) DeleteConnections(ids []string) (result ConnectionChangeResult, err error) {
	started := time.Now()
	defer func() { s.recordDiagnostic("connections.delete", err, time.Since(started)) }()
	ctx, finish, err := s.beginOperation("connections.delete")
	if err != nil {
		return result, err
	}
	defer finish()
	s.mu.Lock()
	defer s.mu.Unlock()
	if err = s.connectionManagementReadyLocked(); err != nil {
		return result, err
	}
	selected, err := selectedConnectionSet(s.profiles, ids)
	if err != nil {
		return result, err
	}
	mounts, err := s.callContext(ctx, "mount/listmounts", nil)
	if err != nil {
		return result, err
	}
	next := make([]Profile, 0, len(s.profiles)-len(selected))
	for _, p := range s.profiles {
		if !selected[p.ID] {
			next = append(next, p)
			continue
		}
		_, owned, e := connectionMountState(mounts, p)
		if e != nil {
			return result, e
		}
		if owned {
			return result, errors.New("所选网盘中仍有已挂载项，请先卸载；本次未删除任何连接")
		}
		if err = s.checkConnectionDirectoryReview(p); err != nil {
			return result, err
		}
	}
	if ctx.Err() != nil {
		return result, errors.New("操作已取消")
	}
	if err = s.persist(next); err != nil {
		return result, errors.New("删除保存失败，所有原连接已保留")
	}
	s.profiles = next
	result.Changed = len(selected)
	for _, id := range ids {
		delete(s.desired, id)
		delete(s.health, id)
		delete(s.recovery, id)
		if _, e := s.call("config/delete", map[string]any{"name": id}); e != nil {
			result.Warning = "连接记录已删除，部分后台配置暂未清理；退出并重新打开程序后清除。"
		}
	}
	return result, nil
}

func occupiedWindowsDrives() ([]string, error) {
	mask, err := windows.GetLogicalDrives()
	if err != nil {
		return nil, errors.New("无法检查可用盘符，请稍后重试")
	}
	drives := []string{}
	for letter := 'A'; letter <= 'Z'; letter++ {
		if mask&(1<<uint(letter-'A')) != 0 {
			drives = append(drives, string(letter)+":")
		}
	}
	return drives, nil
}

func connectionCopyDrives(profiles []Profile) ([]string, error) {
	occupied, err := occupiedWindowsDrives()
	if err != nil {
		return nil, err
	}
	used := map[string]bool{}
	for _, drive := range occupied {
		used[drive] = true
	}
	for _, p := range profiles {
		used[strings.ToUpper(p.Drive)] = true
	}
	drives := []string{}
	for c := 'D'; c <= 'Z'; c++ {
		drive := string(c) + ":"
		if !used[drive] {
			drives = append(drives, drive)
		}
	}
	return drives, nil
}

func (s *DesktopService) ConnectionCopyOptions(id string) (ConnectionCopyOptions, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.find(id)
	if err != nil {
		return ConnectionCopyOptions{}, err
	}
	drives, err := connectionCopyDrives(s.profiles)
	if err != nil {
		return ConnectionCopyOptions{}, err
	}
	base := []rune(p.Name)
	if len(base) > 60 {
		base = base[:60]
	}
	names := map[string]bool{}
	for _, p := range s.profiles {
		names[p.Name] = true
	}
	name := ""
	for n := 1; ; n++ {
		name = fmt.Sprintf("%s（副本%d）", string(base), n)
		if !names[name] {
			break
		}
	}
	return ConnectionCopyOptions{name, drives, p.Group, p.Password != "" && !providers.IsOAuth(p.Provider), len(p.DirectoryFavorites)}, nil
}

// Credentials never leave the backend. Copying them requires an explicit flag;
// all copies start with auto-mount disabled and fresh profile/favorite IDs.
func (s *DesktopService) CopyConnection(id, name, drive string, includePassword bool) (result Profile, err error) {
	started := time.Now()
	defer func() { s.recordDiagnostic("connections.copy", err, time.Since(started)) }()
	ctx, finish, err := s.beginOperation("connections.copy")
	if err != nil {
		return result, err
	}
	defer finish()
	s.mu.Lock()
	defer s.mu.Unlock()
	if err = s.connectionManagementReadyLocked(); err != nil {
		return result, err
	}
	p, err := s.find(id)
	if err != nil {
		return result, err
	}
	if len(s.profiles) >= 200 {
		return result, errors.New("最多保留200个连接，请先整理现有连接")
	}
	name = strings.TrimSpace(name)
	if !validFavoriteName(name) {
		return result, errors.New("连接名称须为1至80个字符，不能包含控制字符")
	}
	for _, old := range s.profiles {
		if old.Name == name {
			return result, errors.New("连接名称已存在，请为副本选择新名称")
		}
	}
	drives, err := connectionCopyDrives(s.profiles)
	if err != nil {
		return result, err
	}
	available := false
	for _, free := range drives {
		available = available || drive == free
	}
	if !available {
		return result, errors.New("盘符已占用或无效，请重新选择可用盘符")
	}
	if err = validateDirectoryFavorites(p.DirectoryFavorites); err != nil {
		return result, err
	}
	p.ID, p.Name, p.Drive, p.AutoMount, p.ClearPassword = "dav"+randomID(), name, drive, false, false
	p.PresetKey = ""
	p.MEGASession, p.Proxy = nil, nil
	if !includePassword || providers.IsOAuth(p.Provider) {
		p = p.public()
	}
	p.DirectoryFavorites = append([]DirectoryFavorite(nil), p.DirectoryFavorites...)
	for i := range p.DirectoryFavorites {
		p.DirectoryFavorites[i].ID = randomID()
	}
	if err = validate(p); err != nil {
		return result, err
	}
	if ctx.Err() != nil {
		return result, errors.New("操作已取消")
	}
	// Configuration is in-memory engine state, and can be removed if the
	// encrypted local commit fails. Source profile/mounts remain untouched.
	if err = s.configure(p); err != nil {
		return result, err
	}
	next := append(append([]Profile{}, s.profiles...), p)
	if ctx.Err() != nil {
		err = errors.New("操作已取消")
	} else if e := s.persist(next); e != nil {
		err = errors.New("副本保存失败，原连接已保留")
	}
	if err != nil {
		s.call("config/delete", map[string]any{"name": p.ID})
		return result, err
	}
	s.profiles = next
	p = p.public()
	p.DirectoryFavorites = nil
	return p, nil
}

func (s *DesktopService) SaveConnectionNote(id, note string) error {
	note = strings.TrimSpace(note)
	if !connectionbackup.ValidNote(note) {
		return errors.New("备注最多 120 个字符，请使用单行文字")
	}
	_, finish, err := s.beginOperation("connections.note")
	if err != nil {
		return err
	}
	defer finish()
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, profile := range s.profiles {
		if profile.ID != id {
			continue
		}
		if profile.Note == note {
			return nil
		}
		next := append([]Profile(nil), s.profiles...)
		next[i].Note = note
		if err := s.persist(next); err != nil {
			return errors.New("备注未保存，请检查本机配置目录权限")
		}
		s.profiles = next
		return nil
	}
	return errors.New("网盘已不存在，请刷新列表")
}
