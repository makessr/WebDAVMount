package desktop

import (
	"context"
	"errors"
)

const multiSearchDriveLimit = 8

type MultiRemoteSearchRequest struct {
	ProfileIDs []string `json:"profileIds"`
	Query      string   `json:"query"`
	Kind       string   `json:"kind"`
	Recursive  bool     `json:"recursive"`
}

type MultiRemoteSearchItem struct {
	RemoteSearchItem
	ProfileID   string `json:"profileId"`
	ProfileName string `json:"profileName"`
}

type MultiRemoteSearchDrive struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Phase   string `json:"phase"`
	Message string `json:"message"`
	Matched int    `json:"matched"`
}

type MultiRemoteSearchReport struct {
	Query  string                   `json:"query"`
	Phase  string                   `json:"phase"`
	Items  []MultiRemoteSearchItem  `json:"items"`
	Drives []MultiRemoteSearchDrive `json:"drives"`
}

type MultiRemoteSearchProgress struct {
	Active    bool   `json:"active"`
	Completed int    `json:"completed"`
	Total     int    `json:"total"`
	Name      string `json:"name,omitempty"`
}

func (s *DesktopService) MultiRemoteSearchProgress() MultiRemoteSearchProgress {
	s.multiSearchMu.Lock()
	defer s.multiSearchMu.Unlock()
	return s.multiSearch
}

func (s *DesktopService) setMultiRemoteSearchProgress(progress MultiRemoteSearchProgress) {
	s.multiSearchMu.Lock()
	defer s.multiSearchMu.Unlock()
	s.multiSearch = progress
}

// Divide the existing search budget across selected connections, so one large
// drive cannot consume the other drives' directory or result allowance.
func (s *DesktopService) SearchMultipleRemotes(request MultiRemoteSearchRequest) (MultiRemoteSearchReport, error) {
	report := MultiRemoteSearchReport{Phase: "done", Items: []MultiRemoteSearchItem{}, Drives: []MultiRemoteSearchDrive{}}
	total := len(request.ProfileIDs)
	if total < 1 || total > multiSearchDriveLimit {
		return report, errors.New("请选择 1 至 8 个网盘")
	}
	query := RemoteSearchRequest{ProfileID: request.ProfileIDs[0], Query: request.Query, Kind: request.Kind, Recursive: request.Recursive}
	if err := validateSearchRequest(&query); err != nil {
		return report, err
	}
	ctx, finish, err := s.beginOperation("remote.search")
	if err != nil {
		return report, err
	}
	defer finish()
	ctx, cancel := context.WithTimeout(ctx, searchTimeLimit)
	defer cancel()
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.connectionManagementReadyLocked(); err != nil {
		return report, err
	}
	selected, err := selectedConnectionSet(s.profiles, request.ProfileIDs)
	if err != nil {
		return report, err
	}
	if !s.engineOnline() {
		return report, errors.New("挂载服务已停止，请先恢复服务")
	}
	report.Query = query.Query
	progress := MultiRemoteSearchProgress{Active: true, Total: total}
	s.setMultiRemoteSearchProgress(progress)
	defer func() {
		progress.Active, progress.Name = false, ""
		s.setMultiRemoteSearchProgress(progress)
	}()
	limits := remoteSearchLimits{searchDirectoryLimit / total, searchEntryLimit / total, searchResultLimit / total}
	var tracker remoteSearchTracker
	for _, profile := range s.profiles {
		if !selected[profile.ID] {
			continue
		}
		drive := MultiRemoteSearchDrive{ID: profile.ID, Name: profile.Name}
		switch {
		case ctx.Err() != nil:
			drive.Phase, drive.Message = "not_started", "搜索已停止，尚未检查"
		case !s.engineOnline():
			drive.Phase, drive.Message = "not_started", "挂载服务已停止，尚未检查"
		default:
			query.ProfileID = profile.ID
			progress.Name = profile.Name
			s.setMultiRemoteSearchProgress(progress)
			result, _ := s.searchRemoteLocked(ctx, profile, query, &tracker, limits)
			drive.Phase, drive.Message, drive.Matched = result.Phase, result.Message, result.Matched
			for _, item := range result.Items {
				report.Items = append(report.Items, MultiRemoteSearchItem{RemoteSearchItem: item, ProfileID: profile.ID, ProfileName: profile.Name})
			}
			progress.Completed++
		}
		report.Drives = append(report.Drives, drive)
		if drive.Phase != "done" {
			report.Phase = "partial"
		}
	}
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		report.Phase = "limited"
	case ctx.Err() != nil:
		report.Phase = "cancelled"
	}
	return report, nil
}
