package main

import (
	"errors"
	"slices"
	"strings"
	"time"
	"webdavmount/internal/providers"
)

type PresetImportRow struct {
	Index       int    `json:"index"`
	Name        string `json:"name"`
	MemberCount int    `json:"memberCount"`
	Status      string `json:"status"`
	Message     string `json:"message"`
}

func (s *DesktopService) presetImportSessionLocked(token string) (*connectionImportSession, error) {
	p := s.connectionImport
	if p == nil || token == "" || token != p.preview.Token {
		return nil, errors.New("挂载方案预览已失效，请重新打开备份")
	}
	expires, _ := time.Parse(time.RFC3339, p.preview.ExpiresAt)
	if !time.Now().Before(expires) {
		s.clearConnectionImportLocked()
		return nil, errors.New("挂载方案预览已过期，请重新打开备份")
	}
	return p, nil
}

func planPresetImport(p *connectionImportSession, profiles []Profile, existing []MountPreset) ([]PresetImportRow, map[int][]string) {
	byID := map[string]int{}
	keys := map[string][]string{}
	for _, profile := range profiles {
		id := profile.presetIdentity()
		byID[id]++
		key := portableConnectionKey(profile.Provider, profile.URL, profile.User, profile.Name)
		keys[key] = append(keys[key], id)
	}
	rows := make([]PresetImportRow, 0, len(p.archive.Presets))
	ready := map[int][]string{}
	for i, preset := range p.archive.Presets {
		row := PresetImportRow{Index: i, Name: preset.Name, MemberCount: len(preset.Members), Status: "ready", Message: "可恢复"}
		if slices.ContainsFunc(existing, func(other MountPreset) bool { return strings.EqualFold(other.Name, preset.Name) }) {
			row.Status, row.Message = "duplicate", "已保留本机同名方案"
			rows = append(rows, row)
			continue
		}
		var members []string
		for _, member := range preset.Members {
			key := p.imported[member]
			if key == "" {
				connection := p.archive.Connections[member]
				// Existing OAuth profiles can represent different signed-in
				// accounts with the same resource metadata.
				if !providers.IsOAuth(connection.Provider) {
					matches := keys[portableConnectionKey(connection.Provider, connection.URL, connection.User, connection.Name)]
					if len(matches) == 1 {
						key = matches[0]
					}
				}
			}
			if key == "" || byID[key] != 1 || slices.Contains(members, key) {
				break
			}
			members = append(members, key)
		}
		if len(members) != len(preset.Members) {
			row.Status, row.Message = "missing", "部分成员缺失或无法确认身份"
		} else {
			ready[i] = members
		}
		rows = append(rows, row)
	}
	return rows, ready
}

func (s *DesktopService) PreviewWorkspacePresetImport(token string) ([]PresetImportRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.presetImportSessionLocked(token)
	if err != nil {
		return nil, err
	}
	existing, err := s.loadMountPresetsLocked()
	if err != nil {
		return nil, err
	}
	rows, _ := planPresetImport(p, s.profiles, existing)
	return rows, nil
}

func (s *DesktopService) RestoreWorkspacePresets(token string, indices []int) (int, error) {
	ctx, finish, err := s.beginOperation("connections.presets.restore")
	if err != nil {
		return 0, err
	}
	defer finish()
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.presetImportSessionLocked(token)
	if err != nil {
		return 0, err
	}
	existing, err := s.loadMountPresetsLocked()
	if err != nil {
		return 0, err
	}
	if len(indices) == 0 || len(indices)+len(existing) > maxMountPresets {
		return 0, errors.New("请选择要恢复的方案，本机最多保留 16 个")
	}
	_, ready := planPresetImport(p, s.profiles, existing)
	seen := map[int]bool{}
	for _, index := range indices {
		members, ok := ready[index]
		if !ok || seen[index] {
			return 0, errors.New("可恢复方案已变化，请刷新预览")
		}
		seen[index] = true
		existing = append(existing, MountPreset{ID: randomID(), Name: p.archive.Presets[index].Name, ConnectionIDs: members})
	}
	if err = checkConnectionImportContext(ctx); err != nil {
		return 0, err
	}
	if err = s.persistMountPresetsLocked(existing); err != nil {
		return 0, err
	}
	return len(indices), nil
}
