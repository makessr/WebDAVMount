package main

import (
	"context"
	"errors"
)

type BatchItem struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Drive   string `json:"drive"`
	State   string `json:"state"`
	Message string `json:"message"`
}
type BatchReport struct {
	Items     []BatchItem `json:"items"`
	Cancelled bool        `json:"cancelled"`
}

// Cancellation stops subsequent connections. The current native mount/unmount
// finishes normally so its result is not lost after a cancelled HTTP response.
func runConnectionBatch(ctx context.Context, profiles []Profile, action func(Profile) (string, string)) BatchReport {
	report := BatchReport{Items: []BatchItem{}}
	for _, p := range profiles {
		state, message := "cancelled", "未处理：已取消后续连接"
		if ctx.Err() == nil {
			state, message = action(p)
		}
		report.Items = append(report.Items, BatchItem{p.ID, p.Name, p.Drive, state, message})
	}
	report.Cancelled = ctx.Err() != nil
	return report
}

func (s *DesktopService) MountAll() (BatchReport, error)   { return s.batchConnections(true) }
func (s *DesktopService) UnmountAll() (BatchReport, error) { return s.batchConnections(false) }
func (s *DesktopService) batchConnections(mount bool) (BatchReport, error) {
	return s.batchConnectionsInGroup(mount, nil)
}
func (s *DesktopService) MountGroup(group string) (BatchReport, error) {
	return s.batchConnectionsInGroup(true, &group)
}
func (s *DesktopService) UnmountGroup(group string) (BatchReport, error) {
	return s.batchConnectionsInGroup(false, &group)
}
func (s *DesktopService) batchConnectionsInGroup(mount bool, group *string) (BatchReport, error) {
	return s.batchConnectionSelection(mount, group, nil, "", "")
}
func (s *DesktopService) MountConnections(ids []string) (BatchReport, error) {
	return s.batchConnectionSelection(true, nil, &ids, "", "")
}
func (s *DesktopService) UnmountConnections(ids []string) (BatchReport, error) {
	return s.batchConnectionSelection(false, nil, &ids, "", "")
}
func (s *DesktopService) batchConnectionSelection(mount bool, group *string, ids *[]string, presetID, previewRevision string) (BatchReport, error) {
	ctx, finish, err := s.beginOperation("batch")
	if err != nil {
		return BatchReport{}, err
	}
	defer finish()
	s.mu.Lock()
	defer s.mu.Unlock()
	profiles := s.profiles
	if ids != nil {
		selected, e := selectedConnectionSet(s.profiles, *ids)
		if e != nil {
			return BatchReport{}, e
		}
		profiles = []Profile{}
		for _, p := range s.profiles {
			if selected[p.ID] {
				profiles = append(profiles, p)
			}
		}
	}
	if group != nil {
		name, err := normalizedConnectionGroup(*group)
		if err != nil {
			return BatchReport{}, err
		}
		profiles = []Profile{}
		for _, p := range s.profiles {
			if p.Group == name {
				profiles = append(profiles, p)
			}
		}
		if len(profiles) == 0 {
			return BatchReport{}, errors.New("该分组已不存在或没有网盘，请刷新列表")
		}
	}
	s.startupMu.Lock()
	starting := s.startupActive
	s.startupMu.Unlock()
	if starting {
		return BatchReport{}, errors.New("请等待启动挂载完成")
	}
	if presetID != "" {
		profiles, err = s.validateMountPresetRunLocked(presetID, mount, previewRevision)
		if err != nil {
			return BatchReport{}, err
		}
	} else if !s.engineOnline() {
		return BatchReport{}, errors.New("请先恢复后台引擎，再批量操作")
	}
	return runConnectionBatch(ctx, profiles, func(p Profile) (string, string) {
		if !s.engineOnline() {
			return "failed", "后台引擎已退出，请先恢复引擎"
		}
		result, e := s.call("mount/listmounts", nil)
		if e != nil {
			return "failed", e.Error()
		}
		present, owned, e := connectionMountState(result, p)
		if e != nil {
			return "failed", e.Error()
		}
		// Profile names may share a configured letter. Never treat another
		// profile's mount as this connection's successful mount or unmount it.
		if mount {
			if owned {
				return "skipped", "已挂载，保持当前磁盘"
			}
			if present {
				return "failed", "盘符已被其他连接占用，请调整连接盘符"
			}
			e = s.mountLocked(context.Background(), p.ID)
			if e == nil {
				return "success", "已挂载；远端访问请测试连接确认"
			}
		} else {
			if !owned {
				return "skipped", "此连接未挂载"
			}
			e = s.unmountLocked(p.ID)
			if e == nil {
				return "success", "已卸载"
			}
		}
		return "failed", e.Error()
	}), nil
}
