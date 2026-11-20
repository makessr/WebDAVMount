package desktop

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type DiagnosticReport struct {
	Version string            `json:"version"`
	OS      string            `json:"os"`
	Arch    string            `json:"arch"`
	Events  []DiagnosticEvent `json:"events"`
}

// The viewer and export share exactly the same allowlisted session metadata.
func (s *DesktopService) Diagnostics() DiagnosticReport {
	store := s.sessionLog()
	store.mu.Lock()
	defer store.mu.Unlock()
	return DiagnosticReport{appVersion, runtime.GOOS, runtime.GOARCH, append([]DiagnosticEvent{}, store.current.Events...)}
}

func (s *DesktopService) ExportDiagnosticsAs() (string, error) {
	return s.exportDiagnosticReportAs(s.Diagnostics())
}

func (s *DesktopService) ExportDiagnosticSessionAs(id string) (string, error) {
	for _, session := range s.DiagnosticSessions().Sessions {
		if session.ID == id {
			report := struct {
				DiagnosticReport
				SessionID        string `json:"sessionID"`
				SessionStartedAt string `json:"sessionStartedAt"`
				SessionVersion   string `json:"sessionVersion"`
			}{DiagnosticReport{appVersion, runtime.GOOS, runtime.GOARCH, session.Events}, session.ID, session.StartedAt, session.Version}
			return s.exportDiagnosticReportAs(report)
		}
	}
	return "", errors.New("该日志会话已不在保留范围内，请刷新后重新选择")
}

func (s *DesktopService) exportDiagnosticReportAs(report any) (string, error) {
	return exportJSONReportAs(report, s.chooseDiagnosticFile)
}

func exportJSONReportAs(report any, choose func() (string, error)) (string, error) {
	if choose == nil {
		return "", errors.New("当前环境无法打开保存窗口")
	}
	path, err := choose()
	if err != nil {
		return "", errors.New("无法打开报告保存窗口")
	}
	if path == "" {
		return "", nil
	}
	if filepath.Ext(path) == "" {
		path += ".json"
	}
	if !filepath.IsAbs(path) {
		return "", errors.New("请选择完整的导出路径")
	}
	if err = noReparse(path); err != nil {
		return "", errors.New("导出路径不可用，请选择其他位置")
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return "", err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if os.IsExist(err) {
		return "", errors.New("该文件已存在，请使用新的文件名导出")
	}
	if err != nil {
		return "", errors.New("无法创建导出文件，请检查文件夹是否存在且可写")
	}
	_, writeErr := f.Write(data)
	if writeErr == nil {
		writeErr = f.Sync()
	}
	closeErr := f.Close()
	if writeErr != nil || closeErr != nil {
		return "", errors.New("报告写入未完成，请检查磁盘空间后重试")
	}
	return path, nil
}

type DiagnosticEvent struct {
	Time       string `json:"time"`
	Operation  string `json:"operation"`
	Result     string `json:"result"`
	DurationMs int64  `json:"durationMs"`
}

var diagnosticOperations = map[string]bool{"connections.autostart": true, "connections.delete": true, "connections.copy": true, "operations/list": true, "mount/mount": true, "mount/unmount": true, "config/create": true, "config/delete": true, "core/quit": true, "cache.scan": true, "cache.migrate": true, "remote.search": true, "startup.save": true, "startup.retry": true, "recovery.auto": true, "license.import": true, "update.check": true, "update.download": true}

func diagnosticCategory(err error) string {
	if err == nil {
		return "success"
	}
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "取消") || strings.Contains(message, "canceled"):
		return "cancelled"
	case strings.HasPrefix(message, "操作失败（http "):
		// The generic advice mentions permissions without identifying an auth failure.
		return "operation_failed"
	case strings.Contains(message, "密码") || strings.Contains(message, "权限"):
		return "authentication_or_permission"
	case strings.Contains(message, "频繁"):
		return "rate_limited"
	case strings.Contains(message, "证书"):
		return "tls_certificate"
	case strings.Contains(message, "超时"):
		return "timeout"
	case strings.Contains(message, "引擎"):
		return "engine_unavailable"
	case strings.Contains(message, "连接网盘"):
		return "network_unavailable"
	default:
		return "operation_failed"
	}
}
func (s *DesktopService) recordDiagnostic(operation string, err error, duration time.Duration) {
	if !diagnosticOperations[operation] {
		return
	}
	store := s.sessionLog()
	store.mu.Lock()
	defer store.mu.Unlock()
	ms := duration.Milliseconds()
	if ms < 0 {
		ms = 0
	}
	if ms > 86400000 {
		ms = 86400000
	}
	event := DiagnosticEvent{time.Now().UTC().Format(time.RFC3339), operation, diagnosticCategory(err), ms}
	store.current.Events = append(store.current.Events, event)
	if len(store.current.Events) > 256 {
		store.current.Events = append([]DiagnosticEvent{}, store.current.Events[len(store.current.Events)-256:]...)
	}
	store.persistLocked()
}

// Export contains only allowlisted metadata, never requests, raw errors or profiles.
func (s *DesktopService) ExportDiagnostics() (string, error) {
	report := s.Diagnostics()
	data, e := json.MarshalIndent(report, "", "  ")
	if e != nil {
		return "", e
	}
	root := filepath.Join(s.dir, "diagnostic-exports")
	if e = noReparse(root); e != nil {
		return "", e
	}
	if e = os.MkdirAll(root, 0700); e != nil {
		return "", e
	}
	file, e := os.CreateTemp(root, "WebDAVMount-diagnostics-*.json")
	if e != nil {
		return "", e
	}
	_, writeErr := file.Write(data)
	closeErr := file.Close()
	if writeErr != nil {
		return "", writeErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	return file.Name(), nil
}
