package desktop

import (
	"context"
	"crypto/sha256"
	"errors"
	"slices"
	"strings"
)

type MountPresetSwitchPreview struct {
	SourceID string      `json:"sourceID"`
	TargetID string      `json:"targetID"`
	Source   string      `json:"source"`
	Target   string      `json:"target"`
	Items    []BatchItem `json:"items"`
	Ready    int         `json:"ready"`
	Blocked  int         `json:"blocked"`
	Revision string      `json:"revision"`
}

type mountPresetSwitchStep struct {
	mountID, unmountID string
}

func (s *DesktopService) PreviewMountPresetSwitch(sourceID, targetID string) (MountPresetSwitchPreview, error) {
	_, finish, err := s.beginOperation("mount.presets")
	if err != nil {
		return MountPresetSwitchPreview{}, err
	}
	defer finish()
	s.mu.Lock()
	defer s.mu.Unlock()
	preview, _, err := s.mountPresetSwitchLocked(sourceID, targetID)
	return preview, err
}

func (s *DesktopService) ApplyMountPresetSwitch(sourceID, targetID, revision string) (BatchReport, error) {
	if len(revision) != sha256.Size*2 {
		return BatchReport{}, errors.New("请先预览方案切换")
	}
	ctx, finish, err := s.beginOperation("batch")
	if err != nil {
		return BatchReport{}, err
	}
	defer finish()
	s.mu.Lock()
	defer s.mu.Unlock()
	preview, steps, err := s.mountPresetSwitchLocked(sourceID, targetID)
	if err != nil {
		return BatchReport{}, err
	}
	if revision != preview.Revision {
		return BatchReport{}, errors.New("方案或盘符状态已变化，请重新检查后确认")
	}
	if preview.Blocked != 0 {
		return BatchReport{}, errors.New("方案中存在盘符冲突，请处理后重试")
	}
	return executeMountPresetSwitch(ctx, preview, steps,
		func(id string) error { return s.mountLocked(context.Background(), id) },
		s.unmountLocked,
	), nil
}

func (s *DesktopService) mountPresetSwitchLocked(sourceID, targetID string) (MountPresetSwitchPreview, []mountPresetSwitchStep, error) {
	var preview MountPresetSwitchPreview
	if sourceID == "" || targetID == "" || sourceID == targetID {
		return preview, nil, errors.New("请选择两套不同的挂载方案")
	}
	if err := s.connectionManagementReadyLocked(); err != nil {
		return preview, nil, err
	}
	source, sourceProfiles, err := s.mountPresetProfilesLocked(sourceID)
	if err != nil {
		return preview, nil, err
	}
	target, targetProfiles, err := s.mountPresetProfilesLocked(targetID)
	if err != nil {
		return preview, nil, err
	}
	if !s.engineOnline() {
		return preview, nil, errors.New("请先恢复后台引擎，再检查挂载方案")
	}
	raw, err := s.call("mount/listmounts", nil)
	if err != nil {
		return preview, nil, err
	}
	mounts, err := identifiedMounts(raw, s.profiles)
	if err != nil {
		return preview, nil, err
	}
	occupied, err := occupiedWindowsDrives()
	if err != nil {
		return preview, nil, errors.New("无法检查 Windows 盘符，请重新检查")
	}
	preview, steps := planMountPresetSwitch(source, target, sourceProfiles, targetProfiles, mounts, occupied, s.retiredDrives, s.safeDriveReuse)
	return preview, steps, nil
}

func planMountPresetSwitch(source, target MountPreset, sourceProfiles, targetProfiles []Profile, mounts []map[string]any, occupied []string, retired map[string]bool, safeReuse bool) (MountPresetSwitchPreview, []mountPresetSwitchStep) {
	preview := MountPresetSwitchPreview{SourceID: source.ID, TargetID: target.ID, Source: source.Name, Target: target.Name, Items: []BatchItem{}}
	targetIDs := map[string]bool{}
	for _, p := range targetProfiles {
		targetIDs[p.ID] = true
	}
	outgoing := map[string]Profile{}
	for _, p := range sourceProfiles {
		if !targetIDs[p.ID] {
			outgoing[p.ID] = p
		}
	}
	// A letter becomes available in the plan only if its current owner will
	// be removed by this switch and the engine supports immediate reuse.
	releases := map[string]string{}
	remaining := make([]map[string]any, 0, len(mounts))
	for _, entry := range mounts {
		id, point := entry["ProfileID"].(string), strings.ToUpper(entry["MountPoint"].(string))
		if _, willRelease := outgoing[id]; willRelease {
			if safeReuse {
				releases[point] = id
				continue
			}
		}
		remaining = append(remaining, entry)
	}
	unused := slices.DeleteFunc(slices.Clone(occupied), func(letter string) bool { return releases[letter] != "" })
	targetPlan := planMountPreset(target, targetProfiles, remaining, unused, retired, true)
	free, replace, release := []mountPresetSwitchStep{}, []mountPresetSwitchStep{}, []mountPresetSwitchStep{}
	paired := map[string]bool{}
	for _, item := range targetPlan.Items {
		if item.State == "ready" {
			oldID := releases[item.Drive]
			if oldID != "" {
				item.Message = "将接替「" + outgoing[oldID].Name + "」的盘符"
				replace = append(replace, mountPresetSwitchStep{mountID: item.ID, unmountID: oldID})
				paired[oldID] = true
			} else {
				free = append(free, mountPresetSwitchStep{mountID: item.ID})
			}
		}
		preview.Items = append(preview.Items, item)
	}
	preview.Blocked = targetPlan.Blocked
	preview.Ready = targetPlan.Ready
	sourcePlan := planMountPreset(source, sourceProfiles, mounts, nil, nil, false)
	for _, item := range sourcePlan.Items {
		if item.State != "ready" || targetIDs[item.ID] {
			continue
		}
		preview.Ready++
		preview.Items = append(preview.Items, item)
		if !paired[item.ID] {
			release = append(release, mountPresetSwitchStep{unmountID: item.ID})
		}
	}
	preview.Revision = mountPreviewRevision(preview)
	return preview, append(append(free, replace...), release...)
}

func executeMountPresetSwitch(ctx context.Context, preview MountPresetSwitchPreview, steps []mountPresetSwitchStep, mount, unmount func(string) error) BatchReport {
	report := BatchReport{Items: slices.Clone(preview.Items)}
	index := make(map[string]int, len(report.Items))
	for i, item := range report.Items {
		index[item.ID] = i
	}
	setState := func(id, state, message string) {
		item := &report.Items[index[id]]
		item.State, item.Message = state, message
	}
	for _, step := range steps {
		if ctx.Err() != nil {
			break
		}
		if step.unmountID != "" {
			if err := unmount(step.unmountID); err != nil {
				setState(step.unmountID, "failed", err.Error())
				break
			}
			setState(step.unmountID, "success", "已卸载")
		}
		if step.mountID == "" {
			continue
		}
		if err := mount(step.mountID); err != nil {
			setState(step.mountID, "failed", err.Error())
			if step.unmountID != "" {
				if restoreErr := mount(step.unmountID); restoreErr != nil {
					setState(step.unmountID, "failed", "原网盘未能恢复："+restoreErr.Error())
				} else {
					setState(step.unmountID, "skipped", "已恢复原网盘")
				}
			}
			break
		}
		setState(step.mountID, "success", "已挂载")
	}
	report.Cancelled = ctx.Err() != nil
	for i := range report.Items {
		if report.Items[i].State == "ready" {
			if report.Cancelled {
				report.Items[i].State, report.Items[i].Message = "cancelled", "已停止后续切换"
			} else {
				report.Items[i].State, report.Items[i].Message = "skipped", "前一步失败，未执行"
			}
		}
	}
	return report
}
