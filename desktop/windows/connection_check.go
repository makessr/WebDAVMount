package desktop

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"
)

const connectionCheckTimeout = 8 * time.Second
const connectionCheckBudget = 90 * time.Second

type ConnectionCheckProgress struct {
	Completed int  `json:"completed"`
	Total     int  `json:"total"`
	Active    bool `json:"active"`
}

func (s *DesktopService) ConnectionCheckProgress() ConnectionCheckProgress {
	s.checkProgressMu.Lock()
	defer s.checkProgressMu.Unlock()
	return s.checkProgress
}

func (s *DesktopService) setConnectionCheckProgress(current ConnectionCheckProgress) {
	s.checkProgressMu.Lock()
	defer s.checkProgressMu.Unlock()
	s.checkProgress = current
}

type ConnectionCheckItem struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Drive     string `json:"drive"`
	Mount     string `json:"mount"`
	State     string `json:"state"`
	Message   string `json:"message"`
	CheckedAt string `json:"checkedAt"`
}

func (s *DesktopService) CheckConnections(ids []string) ([]ConnectionCheckItem, error) {
	ctx, finish, err := s.beginOperation("connections.check")
	if err != nil {
		return nil, err
	}
	defer finish()
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.connectionManagementReadyLocked(); err != nil {
		return nil, err
	}
	selected, err := selectedConnectionSet(s.profiles, ids)
	if err != nil {
		return nil, err
	}
	if !s.engineOnline() {
		return nil, errors.New("挂载服务已停止，请先恢复服务")
	}
	progress := ConnectionCheckProgress{Total: len(ids), Active: true}
	s.setConnectionCheckProgress(progress)
	defer func() {
		progress.Active = false
		s.setConnectionCheckProgress(progress)
	}()
	budget, cancel := context.WithTimeout(ctx, connectionCheckBudget)
	defer cancel()

	// Match both drive and engine identity. An unavailable mount listing
	// leaves drive status unknown while remote checks can still succeed.
	occupied, _ := occupiedWindowsDrives()
	statusCtx, stopStatus := context.WithTimeout(budget, connectionCheckTimeout)
	raw, statusErr := s.callContext(statusCtx, "mount/listmounts", nil)
	stopStatus()
	drives := map[string]string{}
	if statusErr == nil {
		var mounts []map[string]any
		mounts, statusErr = identifiedMounts(raw, s.profiles)
		for _, mount := range mounts {
			drives[strings.ToUpper(mount["MountPoint"].(string))] = mount["ProfileID"].(string)
		}
	}

	items := make([]ConnectionCheckItem, 0, len(ids))
	history := make([]connectionCheckHistoryRow, 0, len(ids))
	attempted := false
	for _, profile := range s.profiles {
		if !selected[profile.ID] {
			continue
		}
		item := ConnectionCheckItem{ID: profile.ID, Name: profile.Name, Drive: profile.Drive, Mount: "挂载状态未知"}
		if statusErr == nil {
			letter := strings.ToUpper(profile.Drive)
			owner, present := drives[letter]
			switch {
			case present && owner == profile.ID:
				item.Mount = "已挂载"
			case present:
				item.Mount = "盘符被其他挂载占用"
			case slices.Contains(occupied, letter):
				item.Mount = "盘符被 Windows 占用"
			default:
				item.Mount = "未挂载"
			}
		}
		s.checkConnectionDirectory(ctx, budget, profile, &item)
		items = append(items, item)
		history = append(history, connectionCheckHistoryRow{Key: connectionCheckHistoryKey(profile), State: item.State, Mount: item.Mount})
		attempted = attempted || item.State != "skipped"
		progress.Completed++
		s.setConnectionCheckProgress(progress)
	}
	if attempted {
		s.checkHistoryWriteFailed = s.recordConnectionCheckLocked(history) != nil
	}
	return items, nil
}

func (s *DesktopService) checkConnectionDirectory(operation, budget context.Context, profile Profile, item *ConnectionCheckItem) {
	switch {
	case operation.Err() != nil || (s.lifeCtx != nil && s.lifeCtx.Err() != nil):
		item.State, item.Message = "skipped", "已停止，未检查"
		return
	case budget.Err() != nil:
		item.State, item.Message = "skipped", "达到本轮时间上限，未检查"
		return
	case !s.engineOnline():
		item.State, item.Message = "skipped", "挂载服务已退出，请恢复后重试"
		return
	}
	ctx, cancel := context.WithTimeout(budget, connectionCheckTimeout)
	defer cancel()
	result, err := s.callProfileContext(ctx, profile, "operations/list", map[string]any{
		"remote": "",
		"opt":    map[string]bool{"recurse": false, "noModTime": true, "noMimeType": true},
	})
	item.CheckedAt = time.Now().Format(time.RFC3339)
	switch {
	case operation.Err() != nil || (s.lifeCtx != nil && s.lifeCtx.Err() != nil):
		item.State, item.Message = "skipped", "检查已取消"
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		item.State, item.Message = "failed", "读取超时，请检查网络或网盘服务"
	case err != nil:
		item.State, item.Message = "failed", connectionCheckMessage(diagnosticCategory(err))
	default:
		if _, ok := result["list"].([]any); !ok && (result["list"] != nil || !hasDirectoryList(result)) {
			item.State, item.Message = "failed", "挂载服务未返回有效目录"
			return
		}
		item.State, item.Message = "success", "已读取远端根目录"
	}
}

func hasDirectoryList(result map[string]any) bool {
	_, exists := result["list"]
	return exists
}

func connectionCheckMessage(category string) string {
	switch category {
	case "authentication_or_permission":
		return "认证或访问受限，请核对账号、应用密码和目录权限"
	case "tls_certificate":
		return "证书验证失败，请核对服务商地址和证书"
	case "rate_limited":
		return "服务商限流，请稍后重试"
	case "timeout", "network_unavailable":
		return "远端连接异常，请检查网络和网盘服务"
	case "engine_unavailable":
		return "挂载服务未响应，请检查服务状态后重试"
	default:
		return "读取目录失败，请检查网盘地址和连接设置，或查看诊断日志"
	}
}
