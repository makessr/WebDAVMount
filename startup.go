package main

import (
	"context"
	"errors"
	"time"
)

func (s *DesktopService) mountAutomaticContext(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mountLocked(ctx, id)
}

type StartupState struct {
	State   string `json:"state"`
	Message string `json:"message"`
}

// Called once by main, never when an engine is restarted after a manual unmount.
func (s *DesktopService) startAutoMount() {
	s.mu.Lock()
	profiles := append([]Profile{}, s.profiles...)
	presetID, presetName, targets, selectionErr := s.startupSelectionLocked()
	s.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	s.startupMu.Lock()
	s.startupProfiles = append([]Profile{}, profiles...)
	for i := range s.startupProfiles {
		s.startupProfiles[i] = s.startupProfiles[i].public()
		s.startupProfiles[i].DirectoryFavorites = nil
	}
	s.startupStates = make(map[string]StartupState)
	s.startupStopRequested = false
	s.startupPresetID = presetID
	s.startupPresetName = presetName
	s.startupPresetWarning = ""
	if selectionErr != nil {
		s.startupPresetWarning = selectionErr.Error()
	}
	for _, p := range targets {
		s.startupStates[p.ID] = StartupState{"queued", "等待自动挂载"}
	}
	s.startupActive = len(targets) > 0
	s.startupCancel = cancel
	s.startupDone = make(chan struct{})
	s.startupMu.Unlock()
	go func() {
		defer close(s.startupDone)
		defer func() { s.startupMu.Lock(); s.startupActive = false; s.startupMu.Unlock() }()
		for _, p := range targets {
			s.startupMu.Lock()
			stopRequested := s.startupStopRequested
			s.startupMu.Unlock()
			if ctx.Err() != nil || stopRequested {
				s.setStartup(p.ID, StartupState{"cancelled", "未处理：已停止后续启动挂载"})
				continue
			}
			s.setStartup(p.ID, StartupState{"running", "正在自动挂载…"})
			err := s.mountAutomaticContext(ctx, p.ID)
			if err != nil {
				if ctx.Err() != nil {
					s.setStartup(p.ID, StartupState{"cancelled", "已停止等待，请核对当前挂载状态"})
				} else {
					s.setStartup(p.ID, StartupState{"failed", "自动挂载失败：" + err.Error()})
				}
			} else {
				s.setStartup(p.ID, StartupState{"mounted", "自动挂载成功"})
			}
		}
	}()
}

type StartupTaskRow struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Drive   string `json:"drive"`
	State   string `json:"state"`
	Message string `json:"message"`
}
type StartupTaskReport struct {
	Active     bool             `json:"active"`
	Retrying   bool             `json:"retrying"`
	Stopping   bool             `json:"stopping"`
	PresetName string           `json:"presetName,omitempty"`
	Warning    string           `json:"warning,omitempty"`
	Items      []StartupTaskRow `json:"items"`
}

// This report never takes the mount lock, so it remains available during a
// slow initial mount or retry. Credentials and URLs are not part of its schema.
func (s *DesktopService) StartupMountTasks() StartupTaskReport {
	s.startupMu.Lock()
	defer s.startupMu.Unlock()
	r := StartupTaskReport{
		Active: s.startupActive || s.startupRetryActive, Retrying: s.startupRetryActive,
		Stopping: s.startupActive && s.startupStopRequested, PresetName: s.startupPresetName,
		Warning: s.startupPresetWarning, Items: []StartupTaskRow{},
	}
	for _, p := range s.startupProfiles {
		if state, ok := s.startupStates[p.ID]; ok {
			r.Items = append(r.Items, StartupTaskRow{p.ID, p.Name, p.Drive, state.State, state.Message})
		}
	}
	return r
}
func (s *DesktopService) CancelStartupMount() {
	s.startupMu.Lock()
	defer s.startupMu.Unlock()
	if s.startupActive {
		s.startupStopRequested = true
	}
}

// Retry only original failed/cancelled startup tasks, after resolving current
// profiles again. It never retries a successfully mounted task later unmounted
// by the user, and cannot adopt or replace another connection's drive.
func (s *DesktopService) RetryFailedStartupMounts() (report BatchReport, err error) {
	started := time.Now()
	defer func() {
		outcome := err
		if outcome == nil {
			if report.Cancelled {
				outcome = errors.New("操作已取消")
			} else {
				for _, item := range report.Items {
					if item.State == "failed" {
						outcome = errors.New("操作失败")
						break
					}
				}
			}
		}
		s.recordDiagnostic("startup.retry", outcome, time.Since(started))
	}()

	ctx, finish, err := s.beginOperation("startup.retry")
	if err != nil {
		return BatchReport{}, err
	}
	defer finish()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.startupMu.Lock()
	if s.startupActive {
		s.startupMu.Unlock()
		return BatchReport{}, errors.New("请先停止或等待启动挂载完成")
	}
	selected := []Profile{}
	startedPreset := s.startupPresetID
	for _, p := range s.startupProfiles {
		state := s.startupStates[p.ID].State
		if state == "failed" || state == "cancelled" {
			selected = append(selected, p)
		}
	}
	s.startupMu.Unlock()
	if len(selected) == 0 {
		return BatchReport{}, errors.New("没有需要重试的启动挂载任务")
	}
	currentPreset, _, eligible, err := s.startupSelectionLocked()
	if err != nil {
		return BatchReport{}, err
	}
	if currentPreset != startedPreset {
		return BatchReport{}, errors.New("启动挂载方式已更改，请手动挂载网盘或重新打开程序")
	}
	eligibleIDs := make(map[string]bool, len(eligible))
	for _, p := range eligible {
		eligibleIDs[p.ID] = true
	}
	if !s.engineOnline() {
		return BatchReport{}, errors.New("请先恢复挂载服务，再重试启动任务")
	}
	s.startupMu.Lock()
	s.startupRetryActive = true
	s.startupMu.Unlock()
	defer func() { s.startupMu.Lock(); s.startupRetryActive = false; s.startupMu.Unlock() }()
	report = runConnectionBatch(ctx, selected, func(original Profile) (state, message string) {
		defer func() {
			next := state
			if next == "success" {
				next = "mounted"
			}
			s.setStartup(original.ID, StartupState{next, message})
		}()
		p, e := s.find(original.ID)
		if e != nil {
			return "skipped", "连接已编辑或删除，请使用当前网盘的挂载按钮"
		}
		if !eligibleIDs[p.ID] {
			if startedPreset != "" {
				return "skipped", "此网盘已移出启动方案"
			}
			return "skipped", "此网盘已关闭自动挂载，保持当前设置"
		}
		result, e := s.callContext(ctx, "mount/listmounts", nil)
		if e != nil {
			return "failed", e.Error()
		}
		present, owned, e := connectionMountState(result, p)
		if e != nil {
			return "failed", e.Error()
		}
		if owned {
			return "success", "已挂载，保持当前磁盘"
		}
		if present {
			return "failed", "盘符已被其他连接占用，请调整连接盘符"
		}
		s.setStartup(original.ID, StartupState{"running", "正在重试启动挂载…"})
		// As with an explicit batch, a submitted native mount finishes normally;
		// cancellation stops subsequent tasks, preserving completed results.
		if e = s.mountLocked(context.Background(), p.ID); e != nil {
			return "failed", e.Error()
		}
		return "success", "已挂载；远端访问请读取目录确认"
	})
	for _, item := range report.Items {
		if item.State == "cancelled" {
			s.setStartup(item.ID, StartupState{"cancelled", item.Message})
		}
	}
	return report, nil
}
func (s *DesktopService) setStartup(id string, state StartupState) {
	s.startupMu.Lock()
	defer s.startupMu.Unlock()
	s.startupStates[id] = state
}
func (s *DesktopService) startupSnapshot() (map[string]StartupState, bool, []Profile, []map[string]any) {
	s.startupMu.Lock()
	defer s.startupMu.Unlock()
	states := make(map[string]StartupState)
	for id, status := range s.startupStates {
		states[id] = status
	}
	profiles := append([]Profile{}, s.startupProfiles...)
	mounts := []map[string]any{}
	for _, p := range profiles {
		if states[p.ID].State == "mounted" {
			mounts = append(mounts, map[string]any{"MountPoint": p.Drive, "ProfileID": p.ID})
		}
	}
	return states, s.startupActive, profiles, mounts
}
