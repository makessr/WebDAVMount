package main

import (
	"context"
	"crypto/sha256"
	"errors"
)

type RecoveryPreview struct {
	Online   bool                  `json:"online"`
	Items    []RecoveryPreviewItem `json:"items"`
	Session  SessionRecoveryStatus `json:"session"`
	Revision string                `json:"revision"`
}

type RecoveryPreviewItem struct {
	BatchItem
	Previous bool `json:"previous,omitempty"`
}

func (s *DesktopService) recoveryPreviewLocked(ctx context.Context) (RecoveryPreview, error) {
	previous, session := s.sessionRecovery.previousEntries()
	preview := RecoveryPreview{Online: s.engineOnline(), Items: []RecoveryPreviewItem{}, Session: session}
	profiles := []Profile{}
	for _, profile := range s.profiles {
		drive, prior := previous[profile.ID]
		if s.desired[profile.ID] || (prior && drive == profile.Drive) {
			profiles = append(profiles, profile)
		}
	}
	if len(profiles) != 0 {
		var mounts []map[string]any
		var retired map[string]bool
		if preview.Online {
			raw, err := s.callContext(ctx, "mount/listmounts", nil)
			if err != nil {
				return RecoveryPreview{}, err
			}
			mounts, err = identifiedMounts(raw, s.profiles)
			if err != nil {
				return RecoveryPreview{}, err
			}
			retired = s.retiredDrives
		}
		occupied, err := occupiedWindowsDrives()
		if err != nil {
			return RecoveryPreview{}, errors.New("无法读取 Windows 盘符，请重新检查")
		}
		for _, item := range planMountPreset(MountPreset{}, profiles, mounts, occupied, retired, true).Items {
			drive, prior := previous[item.ID]
			preview.Items = append(preview.Items, RecoveryPreviewItem{BatchItem: item, Previous: prior && drive == item.Drive && !s.desired[item.ID]})
		}
	}
	preview.Revision = mountPreviewRevision(preview)
	return preview, nil
}

func (s *DesktopService) PreviewRecovery() (RecoveryPreview, error) {
	ctx, finish, err := s.beginOperation("recovery.preview")
	if err != nil {
		return RecoveryPreview{}, err
	}
	defer finish()
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.connectionManagementReadyLocked(); err != nil {
		return RecoveryPreview{}, err
	}
	return s.recoveryPreviewLocked(ctx)
}

// The same live checks protect both selected recovery and starting the engine
// alone. Selection is restricted to verified current or previous mounts.
func (s *DesktopService) ApplyRecovery(ids []string, restartOnly bool, revision string) error {
	if len(revision) != sha256.Size*2 || (restartOnly && len(ids) != 0) {
		return errors.New("请重新检查恢复状态")
	}
	ctx, finish, err := s.beginOperation("recovery")
	if err != nil {
		return err
	}
	defer finish()
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.connectionManagementReadyLocked(); err != nil {
		return err
	}
	preview, err := s.recoveryPreviewLocked(ctx)
	if err != nil {
		return err
	}
	if revision != preview.Revision {
		return errors.New("挂载状态已变化，请重新检查后确认")
	}
	selected := map[string]bool{}
	if restartOnly {
		if preview.Online {
			return errors.New("挂载服务已启动")
		}
	} else {
		selected, err = selectedConnectionSet(s.profiles, ids)
		if err != nil {
			return err
		}
		if err := validateRecoverySelection(preview, selected); err != nil {
			return err
		}
	}
	_, err = s.recoverSelectionLocked(ctx, selected)
	if s.engineOnline() {
		// A manual selection replaces the pending retry from this interruption.
		// Keep the user's automatic recovery setting for future interruptions.
		s.settleManualRecoveryLocked("已按手动选择处理本次挂载恢复")
	}
	return err
}

func validateRecoverySelection(preview RecoveryPreview, selected map[string]bool) error {
	drives := map[string]bool{}
	accepted := 0
	for _, item := range preview.Items {
		if !selected[item.ID] {
			continue
		}
		if item.State != "ready" || drives[item.Drive] {
			return errors.New("选中的网盘有盘符冲突或状态已变化，请重新选择")
		}
		drives[item.Drive] = true
		accepted++
	}
	if accepted != len(selected) {
		return errors.New("只能恢复挂载记录中有匹配项的网盘")
	}
	return nil
}
