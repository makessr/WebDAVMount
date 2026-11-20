package desktop

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
)

type ConnectionDriveRow struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Drive string `json:"drive"`
}

type ConnectionDrivePlan struct {
	Rows      []ConnectionDriveRow `json:"rows"`
	Available []string             `json:"available"`
}

type ConnectionDriveChange struct {
	ID   string `json:"id"`
	From string `json:"from"`
	To   string `json:"to"`
}

// availableDrivePlanLocked leaves selected profiles' original letters available
// for swapping. The final assignment is checked separately before saving.
func (s *DesktopService) availableDrivePlanLocked(ctx context.Context, ids []string) (ConnectionDrivePlan, error) {
	if err := s.connectionManagementReadyLocked(); err != nil {
		return ConnectionDrivePlan{}, err
	}
	selected, err := selectedConnectionSet(s.profiles, ids)
	if err != nil {
		return ConnectionDrivePlan{}, err
	}
	if !s.engineOnline() {
		return ConnectionDrivePlan{}, errors.New("请先恢复挂载服务，再调整盘符")
	}
	state, err := s.callContext(ctx, "mount/listmounts", nil)
	if err != nil {
		return ConnectionDrivePlan{}, err
	}
	mounts, err := identifiedMounts(state, s.profiles)
	if err != nil {
		return ConnectionDrivePlan{}, err
	}
	reserved := make(map[string]bool)
	for _, mount := range mounts {
		if selected[mount["ProfileID"].(string)] {
			return ConnectionDrivePlan{}, errors.New("所选网盘中有已挂载项，请先卸载再调整盘符")
		}
		reserved[strings.ToUpper(mount["MountPoint"].(string))] = true
	}
	occupied, err := occupiedWindowsDrives()
	if err != nil {
		return ConnectionDrivePlan{}, err
	}
	for _, drive := range occupied {
		reserved[drive] = true
	}
	plan := ConnectionDrivePlan{Rows: make([]ConnectionDriveRow, 0, len(ids)), Available: []string{}}
	for _, p := range s.profiles {
		if selected[p.ID] {
			plan.Rows = append(plan.Rows, ConnectionDriveRow{ID: p.ID, Name: p.Name, Drive: p.Drive})
		} else {
			reserved[strings.ToUpper(p.Drive)] = true
		}
	}
	for letter := 'D'; letter <= 'Z'; letter++ {
		drive := string(letter) + ":"
		if !reserved[drive] && !s.retiredDrives[drive] {
			plan.Available = append(plan.Available, drive)
		}
	}
	return plan, nil
}

func (s *DesktopService) PreviewConnectionDrives(ids []string) (ConnectionDrivePlan, error) {
	ctx, finish, err := s.beginOperation("connections.drives.preview")
	if err != nil {
		return ConnectionDrivePlan{}, err
	}
	defer finish()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.availableDrivePlanLocked(ctx, ids)
}

func (s *DesktopService) SaveConnectionDrives(changes []ConnectionDriveChange) (ConnectionChangeResult, error) {
	if len(changes) == 0 || len(changes) > 200 {
		return ConnectionChangeResult{}, errors.New("请选择1至200个网盘")
	}
	ctx, finish, err := s.beginOperation("connections.drives.save")
	if err != nil {
		return ConnectionChangeResult{}, err
	}
	defer finish()
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]string, 0, len(changes))
	byID := make(map[string]ConnectionDriveChange, len(changes))
	for _, change := range changes {
		ids = append(ids, change.ID)
		byID[change.ID] = change
	}
	plan, err := s.availableDrivePlanLocked(ctx, ids)
	if err != nil {
		return ConnectionChangeResult{}, err
	}
	counts := make(map[string]int)
	for _, row := range plan.Rows {
		change := byID[row.ID]
		if row.Drive != change.From {
			return ConnectionChangeResult{}, errors.New("原盘符已变化，请重新读取盘符再调整")
		}
		counts[strings.ToUpper(change.To)]++
	}
	next := append([]Profile(nil), s.profiles...)
	changed := 0
	for i, p := range next {
		change, selected := byID[p.ID]
		if !selected || change.To == p.Drive {
			continue
		}
		if !slices.Contains(plan.Available, change.To) {
			return ConnectionChangeResult{}, errors.New("目标盘符不可用，请重新读取盘符后改选")
		}
		if counts[change.To] > 1 {
			return ConnectionChangeResult{}, fmt.Errorf("目标盘符 %s 重复，请为网盘选择不同盘符", change.To)
		}
		next[i].Drive = change.To
		changed++
	}
	if err := ctx.Err(); err != nil {
		return ConnectionChangeResult{}, errors.New("操作已取消")
	}
	if changed == 0 {
		return ConnectionChangeResult{}, nil
	}
	if err := s.persist(next); err != nil {
		return ConnectionChangeResult{}, errors.New("盘符保存失败，原配置已保留")
	}
	for _, row := range plan.Rows {
		if byID[row.ID].To != row.Drive {
			delete(s.recovery, row.ID)
		}
	}
	s.profiles = next
	return ConnectionChangeResult{Changed: changed}, nil
}
