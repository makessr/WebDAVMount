package desktop

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"golang.org/x/sys/windows"
)

const diagnosticArchiveLimit = 256 << 10

type DiagnosticSession struct {
	ID        string            `json:"id"`
	StartedAt string            `json:"startedAt"`
	Version   string            `json:"version"`
	Events    []DiagnosticEvent `json:"events"`
}
type DiagnosticSessionsReport struct {
	CurrentID string              `json:"currentID"`
	Sessions  []DiagnosticSession `json:"sessions"`
	Warning   string              `json:"warning"`
}
type diagnosticArchive struct {
	Format   int                 `json:"format"`
	Sessions []DiagnosticSession `json:"sessions"`
}
type diagnosticStore struct {
	mu         sync.Mutex
	dir        string
	current    DiagnosticSession
	history    []DiagnosticSession
	warning    string
	blocked    bool
	loadPhase  string
	loadError  error
	writeError error
}

var diagnosticSessionID = regexp.MustCompile(`^[a-f0-9]{32}$`)
var diagnosticSessionVersion = regexp.MustCompile(`^(legacy|[0-9]{1,6}\.[0-9]{1,6}\.[0-9]{1,6})$`)
var diagnosticResults = map[string]bool{"success": true, "cancelled": true, "authentication_or_permission": true, "rate_limited": true, "tls_certificate": true, "timeout": true, "engine_unavailable": true, "network_unavailable": true, "operation_failed": true}

func validDiagnosticEvents(events []DiagnosticEvent) bool {
	if len(events) > 256 {
		return false
	}
	for _, event := range events {
		if !diagnosticOperations[event.Operation] || !diagnosticResults[event.Result] || len(event.Time) > 64 || event.DurationMs < 0 || event.DurationMs > 86400000 {
			return false
		}
		if _, e := time.Parse(time.RFC3339, event.Time); e != nil {
			return false
		}
	}
	return true
}
func decodeDiagnosticJSON(data []byte, value any) bool {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	return d.Decode(value) == nil && d.Decode(new(any)) == io.EOF
}

func openDiagnosticArchive(path string) (*os.File, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	// MoveFileEx opens the target for deletion while atomically publishing a
	// new snapshot. Read handles must permit that access and remain bound to
	// whichever complete file they opened.
	handle, err := windows.CreateFile(name, windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	var info windows.ByHandleFileInformation
	if err = windows.GetFileInformationByHandle(handle, &info); err != nil {
		windows.CloseHandle(handle)
		return nil, err
	}
	if info.FileAttributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DIRECTORY) != 0 {
		windows.CloseHandle(handle)
		return nil, windows.ERROR_INVALID_DATA
	}
	return os.NewFile(uintptr(handle), path), nil
}

func newDiagnosticStore(dir string) *diagnosticStore {
	store := &diagnosticStore{dir: dir, current: DiagnosticSession{ID: randomID(), StartedAt: time.Now().UTC().Format(time.RFC3339), Version: appVersion, Events: []DiagnosticEvent{}}, history: []DiagnosticSession{}}
	if dir == "" {
		return store
	}
	fail := func(phase string, err error) *diagnosticStore {
		store.history = []DiagnosticSession{}
		store.blocked = true
		store.loadPhase, store.loadError = phase, err
		store.warning = "历史日志无法安全读取，原文件已保留；本次日志仍可查看和导出，但暂不写入历史文件。"
		return store
	}
	file := filepath.Join(dir, "diagnostics.json")
	if e := noReparse(file); e != nil {
		return fail("path", e)
	}
	f, e := openDiagnosticArchive(file)
	if os.IsNotExist(e) {
		return store
	}
	if e != nil {
		return fail("open", e)
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil || !info.Mode().IsRegular() || info.Size() > diagnosticArchiveLimit {
		return fail("stat", e)
	}
	data, e := io.ReadAll(io.LimitReader(f, diagnosticArchiveLimit+1))
	if e != nil || len(data) > diagnosticArchiveLimit {
		return fail("read", e)
	}
	data = bytes.TrimSpace(bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf}))
	if len(data) == 0 {
		return fail("empty", nil)
	}
	if data[0] == '[' {
		var events []DiagnosticEvent
		if !decodeDiagnosticJSON(data, &events) || !validDiagnosticEvents(events) {
			return fail("legacy validation", nil)
		}
		if len(events) > 0 {
			store.history = append(store.history, DiagnosticSession{ID: randomID(), StartedAt: events[0].Time, Version: "legacy", Events: events})
		}
		return store
	}
	var archive diagnosticArchive
	if !decodeDiagnosticJSON(data, &archive) || archive.Format != 1 || len(archive.Sessions) > 5 {
		return fail("archive validation", nil)
	}
	seen := map[string]bool{}
	for _, session := range archive.Sessions {
		if !diagnosticSessionID.MatchString(session.ID) || seen[session.ID] || !diagnosticSessionVersion.MatchString(session.Version) || !validDiagnosticEvents(session.Events) {
			return fail("session validation", nil)
		}
		if _, e = time.Parse(time.RFC3339, session.StartedAt); e != nil {
			return fail("session time", e)
		}
		seen[session.ID] = true
		if len(session.Events) > 0 && len(store.history) < 4 {
			store.history = append(store.history, session)
		}
	}
	return store
}
func (s *DesktopService) sessionLog() *diagnosticStore {
	s.logMu.Lock()
	defer s.logMu.Unlock()
	if s.diagnosticLog == nil {
		s.diagnosticLog = newDiagnosticStore(s.dir)
	}
	return s.diagnosticLog
}
func cloneDiagnosticSession(session DiagnosticSession) DiagnosticSession {
	session.Events = append([]DiagnosticEvent{}, session.Events...)
	return session
}
func (s *DesktopService) DiagnosticSessions() DiagnosticSessionsReport {
	store := s.sessionLog()
	store.mu.Lock()
	defer store.mu.Unlock()
	report := DiagnosticSessionsReport{CurrentID: store.current.ID, Sessions: []DiagnosticSession{cloneDiagnosticSession(store.current)}, Warning: store.warning}
	for _, session := range store.history {
		report.Sessions = append(report.Sessions, cloneDiagnosticSession(session))
	}
	return report
}
func (store *diagnosticStore) persistLocked() {
	if store.dir == "" || store.blocked {
		return
	}
	sessions := append([]DiagnosticSession{store.current}, store.history...)
	data, e := json.Marshal(diagnosticArchive{Format: 1, Sessions: sessions})
	if e == nil && len(data) <= diagnosticArchiveLimit {
		e = atomicWriteSettings(filepath.Join(store.dir, "diagnostics.json"), data)
	}
	if e != nil || len(data) > diagnosticArchiveLimit {
		store.writeError = e
		store.warning = "日志暂未能保存到磁盘，本次记录仍可查看和导出；请检查数据目录和磁盘空间。"
	} else {
		store.writeError = nil
		store.warning = ""
	}
}
