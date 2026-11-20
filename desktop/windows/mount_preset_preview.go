package desktop

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"
)

type MountPresetPreview struct {
	ID       string      `json:"id"`
	Name     string      `json:"name"`
	Mount    bool        `json:"mount"`
	Items    []BatchItem `json:"items"`
	Ready    int         `json:"ready"`
	Blocked  int         `json:"blocked"`
	Revision string      `json:"revision"`
}

func (s *DesktopService) PreviewMountPreset(id string, mount bool) (MountPresetPreview, error) {
	_, finish, err := s.beginOperation("mount.presets")
	if err != nil {
		return MountPresetPreview{}, err
	}
	defer finish()
	s.mu.Lock()
	defer s.mu.Unlock()
	preview, _, err := s.mountPresetPreviewLocked(id, mount)
	return preview, err
}

func (s *DesktopService) ApplyMountPreset(id string, mount bool, revision string) (BatchReport, error) {
	if id == "" || len(revision) != sha256.Size*2 {
		return BatchReport{}, errors.New("请先预览挂载方案")
	}
	return s.batchConnectionSelection(mount, nil, nil, id, revision)
}

func (s *DesktopService) validateMountPresetRunLocked(id string, mount bool, revision string) ([]Profile, error) {
	preview, profiles, err := s.mountPresetPreviewLocked(id, mount)
	if err != nil {
		return nil, err
	}
	if revision != "" && preview.Revision != revision {
		return nil, errors.New("方案或盘符状态已变化，请重新检查后确认")
	}
	if preview.Blocked > 0 {
		return nil, errors.New("方案中存在盘符冲突，请处理后重试")
	}
	return profiles, nil
}

func (s *DesktopService) mountPresetPreviewLocked(id string, mount bool) (MountPresetPreview, []Profile, error) {
	var preview MountPresetPreview
	preset, profiles, err := s.mountPresetProfilesLocked(id)
	if err != nil {
		return preview, nil, err
	}
	if !s.engineOnline() {
		return preview, nil, errors.New("请先恢复后台引擎，再检查挂载方案")
	}
	result, err := s.call("mount/listmounts", nil)
	if err != nil {
		return preview, nil, err
	}
	mounts, err := identifiedMounts(result, s.profiles)
	if err != nil {
		return preview, nil, err
	}
	var occupied []string
	if mount {
		occupied, err = occupiedWindowsDrives()
		if err != nil {
			return preview, nil, errors.New("无法检查 Windows 盘符，请重新检查")
		}
	}
	preview = planMountPreset(preset, profiles, mounts, occupied, s.retiredDrives, mount)
	return preview, profiles, nil
}

func planMountPreset(preset MountPreset, profiles []Profile, mounts []map[string]any, occupied []string, retired map[string]bool, mount bool) MountPresetPreview {
	preview := MountPresetPreview{ID: preset.ID, Name: preset.Name, Mount: mount, Items: make([]BatchItem, 0, len(profiles))}
	counts := map[string]int{}
	for _, p := range profiles {
		counts[strings.ToUpper(p.Drive)]++
	}
	owners := map[string]string{}
	for _, entry := range mounts {
		owners[strings.ToUpper(entry["MountPoint"].(string))] = entry["ProfileID"].(string)
	}
	for _, p := range profiles {
		drive := strings.ToUpper(p.Drive)
		owner, present := owners[drive]
		item := BatchItem{ID: p.ID, Name: p.Name, Drive: drive}
		switch {
		case !mount && present && owner == p.ID:
			item.State, item.Message = "ready", "将卸载此网盘"
		case !mount:
			item.State, item.Message = "skipped", "此网盘未挂载"
		case counts[drive] > 1:
			item.State, item.Message = "blocked", "方案内盘符重复，请调整盘符或编辑方案"
		case present && owner == p.ID:
			item.State, item.Message = "skipped", "已挂载，保持当前磁盘"
		case present:
			item.State, item.Message = "blocked", "盘符已被其他连接占用"
		case slices.Contains(occupied, drive):
			item.State, item.Message = "blocked", "盘符已被 Windows 占用，请释放或调整盘符"
		case retired[drive]:
			item.State, item.Message = "blocked", "请先卸载本程序的其他磁盘，再重新挂载此盘符"
		default:
			item.State, item.Message = "ready", "将挂载此网盘"
		}
		switch item.State {
		case "ready":
			preview.Ready++
		case "blocked":
			preview.Blocked++
		}
		preview.Items = append(preview.Items, item)
	}
	// The revision binds the displayed action and rows, including current mount ownership.
	preview.Revision = mountPreviewRevision(preview)
	return preview
}

func mountPreviewRevision(preview any) string {
	encoded, _ := json.Marshal(preview)
	revision := sha256.Sum256(encoded)
	return hex.EncodeToString(revision[:])
}
