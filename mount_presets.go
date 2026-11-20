package main

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

const maxMountPresets = 16
const maxMountPresetsBytes = 512 << 10

type MountPreset struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	ConnectionIDs []string `json:"connectionIDs"`
}

func (p Profile) presetIdentity() string {
	if p.PresetKey != "" {
		return p.PresetKey
	}
	return p.ID
}

func (s *DesktopService) resolveMountPresetMembersLocked(members []string) ([]string, error) {
	current := make(map[string]string, len(s.profiles))
	for _, profile := range s.profiles {
		key := profile.presetIdentity()
		if _, already := current[key]; already {
			current[key] = ""
		} else {
			current[key] = profile.ID
		}
	}
	ids := make([]string, 0, len(members))
	for _, key := range members {
		id, ok := current[key]
		if !ok {
			return nil, errors.New("方案包含已删除的网盘，请编辑方案后重试")
		}
		if id == "" {
			return nil, errors.New("无法确认方案中的网盘身份，请检查连接配置")
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func mountPresetName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || !utf8.ValidString(name) || utf8.RuneCountInString(name) > 32 || strings.ContainsFunc(name, unicode.IsControl) {
		return "", errors.New("方案名称需要 1 至 32 个字符")
	}
	return name, nil
}

func (s *DesktopService) loadMountPresetsLocked() ([]MountPreset, error) {
	path := filepath.Join(s.dir, "mount-presets.enc")
	invalid := errors.New("无法读取本机挂载方案。请保留原文件并检查配置目录")
	if err := noReparse(path); err != nil {
		return nil, invalid
	}
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return []MountPreset{}, nil
	}
	if err != nil {
		return nil, invalid
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, invalid
	}
	data, err := io.ReadAll(io.LimitReader(file, maxMountPresetsBytes+1))
	if err != nil || len(data) > maxMountPresetsBytes {
		return nil, invalid
	}
	plain, err := crypt(data, true)
	if err != nil {
		return nil, invalid
	}
	defer clear(plain)
	var presets []MountPreset
	if err = json.Unmarshal(plain, &presets); err != nil || presets == nil || len(presets) > maxMountPresets {
		return nil, invalid
	}
	ids := map[string]bool{}
	for i, preset := range presets {
		name, err := mountPresetName(preset.Name)
		if err != nil || name != preset.Name || preset.ID == "" || ids[preset.ID] || !validPresetMembers(preset.ConnectionIDs) {
			return nil, invalid
		}
		ids[preset.ID] = true
		if slices.ContainsFunc(presets[:i], func(other MountPreset) bool { return strings.EqualFold(other.Name, name) }) {
			return nil, invalid
		}
	}
	return presets, nil
}

func validPresetMembers(ids []string) bool {
	if len(ids) == 0 || len(ids) > 200 {
		return false
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if id == "" || len(id) > 80 || seen[id] {
			return false
		}
		seen[id] = true
	}
	return true
}

func (s *DesktopService) MountPresets() ([]MountPreset, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadMountPresetsLocked()
}

func (s *DesktopService) mountPresetProfilesLocked(id string) (MountPreset, []Profile, error) {
	presets, err := s.loadMountPresetsLocked()
	if err != nil {
		return MountPreset{}, nil, err
	}
	i := slices.IndexFunc(presets, func(p MountPreset) bool { return p.ID == id })
	if i < 0 {
		return MountPreset{}, nil, errors.New("挂载方案已不存在")
	}
	preset := presets[i]
	resolved, err := s.resolveMountPresetMembersLocked(preset.ConnectionIDs)
	if err != nil {
		return preset, nil, err
	}
	selected, err := selectedConnectionSet(s.profiles, resolved)
	if err != nil {
		return preset, nil, err
	}
	profiles := make([]Profile, 0, len(resolved))
	for _, p := range s.profiles {
		if selected[p.ID] {
			profiles = append(profiles, p)
		}
	}
	return preset, profiles, nil
}

func (s *DesktopService) persistMountPresetsLocked(presets []MountPreset) error {
	plain, err := json.Marshal(presets)
	if err != nil {
		return err
	}
	defer clear(plain)
	ciphertext, err := crypt(plain, false)
	if err != nil || len(ciphertext) > maxMountPresetsBytes || atomicWriteSettings(filepath.Join(s.dir, "mount-presets.enc"), ciphertext) != nil {
		return errors.New("挂载方案未保存，请检查本机配置目录")
	}
	return nil
}

func (s *DesktopService) SaveMountPreset(id, name string, members []string) (MountPreset, error) {
	name, err := mountPresetName(name)
	if err != nil {
		return MountPreset{}, err
	}
	if !validPresetMembers(members) {
		return MountPreset{}, errors.New("请选择 1 至 200 个不同的网盘")
	}
	_, finish, err := s.beginOperation("mount.presets")
	if err != nil {
		return MountPreset{}, err
	}
	defer finish()
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err = s.resolveMountPresetMembersLocked(members); err != nil {
		return MountPreset{}, err
	}
	presets, err := s.loadMountPresetsLocked()
	if err != nil {
		return MountPreset{}, err
	}
	if slices.ContainsFunc(presets, func(p MountPreset) bool { return p.ID != id && strings.EqualFold(p.Name, name) }) {
		return MountPreset{}, errors.New("方案名称已存在")
	}
	preset := MountPreset{ID: id, Name: name, ConnectionIDs: members}
	if id == "" {
		if len(presets) >= maxMountPresets {
			return MountPreset{}, errors.New("最多保留 16 个挂载方案")
		}
		preset.ID = randomID()
		presets = append(presets, preset)
	} else {
		i := slices.IndexFunc(presets, func(p MountPreset) bool { return p.ID == id })
		if i < 0 {
			return MountPreset{}, errors.New("挂载方案已不存在")
		}
		presets[i] = preset
	}
	if err = s.persistMountPresetsLocked(presets); err != nil {
		return MountPreset{}, err
	}
	return preset, nil
}

func (s *DesktopService) DeleteMountPreset(id string) error {
	_, finish, err := s.beginOperation("mount.presets")
	if err != nil {
		return err
	}
	defer finish()
	s.mu.Lock()
	defer s.mu.Unlock()
	presets, err := s.loadMountPresetsLocked()
	if err != nil {
		return err
	}
	i := slices.IndexFunc(presets, func(p MountPreset) bool { return p.ID == id })
	if i < 0 {
		return errors.New("挂载方案已不存在")
	}
	startupID, err := s.readStartupPresetLocked()
	if err != nil {
		return err
	}
	if startupID == id {
		return errors.New("此方案正在用于启动挂载，请先切换启动挂载方式")
	}
	return s.persistMountPresetsLocked(slices.Delete(presets, i, i+1))
}

func (s *DesktopService) RunMountPreset(id string, mount bool) (BatchReport, error) {
	if id == "" {
		return BatchReport{}, errors.New("请选择挂载方案")
	}
	return s.batchConnectionSelection(mount, nil, nil, id, "")
}
