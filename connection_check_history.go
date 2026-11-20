package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"
)

const connectionCheckHistoryFile = "connection-check-history.enc"
const connectionCheckHistoryLimit = 12
const connectionCheckHistoryMaxBytes = 1 << 20

type connectionCheckHistoryRow struct {
	Key   string `json:"key"`
	State string `json:"state"`
	Mount string `json:"mount"`
}

type connectionCheckHistoryRun struct {
	ID         string                      `json:"id"`
	RecordedAt time.Time                   `json:"recordedAt"`
	Rows       []connectionCheckHistoryRow `json:"rows"`
}

type ConnectionCheckHistoryItem struct {
	Name     string `json:"name,omitempty"`
	State    string `json:"state"`
	Mount    string `json:"mount"`
	Previous string `json:"previous,omitempty"`
}

type ConnectionCheckHistoryEntry struct {
	ID         string                       `json:"id"`
	RecordedAt time.Time                    `json:"recordedAt"`
	Items      []ConnectionCheckHistoryItem `json:"items"`
}

type ConnectionCheckHistoryReport struct {
	Entries []ConnectionCheckHistoryEntry `json:"entries"`
	Warning string                        `json:"warning,omitempty"`
}

func connectionCheckHistoryKey(profile Profile) string {
	key := sha256.Sum256([]byte(profile.presetIdentity()))
	return hex.EncodeToString(key[:])
}

func (s *DesktopService) ConnectionCheckHistory() ConnectionCheckHistoryReport {
	s.mu.Lock()
	defer s.mu.Unlock()
	report := ConnectionCheckHistoryReport{Entries: []ConnectionCheckHistoryEntry{}}
	runs, err := s.readConnectionCheckHistoryLocked()
	if err != nil {
		report.Warning = err.Error()
		return report
	}
	names := make(map[string]string, len(s.profiles))
	for _, profile := range s.profiles {
		key := connectionCheckHistoryKey(profile)
		if _, exists := names[key]; exists {
			names[key] = ""
		} else {
			names[key] = profile.Name
		}
	}
	previous := map[string]string{}
	report.Entries = make([]ConnectionCheckHistoryEntry, len(runs))
	for i := len(runs) - 1; i >= 0; i-- {
		run := runs[i]
		entry := ConnectionCheckHistoryEntry{ID: run.ID, RecordedAt: run.RecordedAt, Items: make([]ConnectionCheckHistoryItem, 0, len(run.Rows))}
		for _, row := range run.Rows {
			item := ConnectionCheckHistoryItem{Name: names[row.Key], State: row.State, Mount: row.Mount}
			if row.State != "skipped" {
				item.Previous = previous[row.Key]
				previous[row.Key] = row.State
			}
			entry.Items = append(entry.Items, item)
		}
		report.Entries[i] = entry
	}
	if s.checkHistoryWriteFailed {
		report.Warning = "最近一轮体检未能写入历史，请检查本机配置目录"
	}
	return report
}

func (s *DesktopService) ClearConnectionCheckHistory() error {
	_, finish, err := s.beginOperation("connections.check.history")
	if err != nil {
		return err
	}
	defer finish()
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.writeConnectionCheckHistoryLocked([]connectionCheckHistoryRun{}); err != nil {
		return errors.New("无法清除体检历史，请检查本机配置目录")
	}
	s.checkHistoryWriteFailed = false
	return nil
}

func (s *DesktopService) recordConnectionCheckLocked(rows []connectionCheckHistoryRow) error {
	runs, err := s.readConnectionCheckHistoryLocked()
	if err != nil {
		return err
	}
	next := append([]connectionCheckHistoryRun{{ID: randomID(), RecordedAt: time.Now().UTC(), Rows: rows}}, runs...)
	if len(next) > connectionCheckHistoryLimit {
		next = next[:connectionCheckHistoryLimit]
	}
	return s.writeConnectionCheckHistoryLocked(next)
}

func (s *DesktopService) readConnectionCheckHistoryLocked() ([]connectionCheckHistoryRun, error) {
	path := filepath.Join(s.dir, connectionCheckHistoryFile)
	unreadable := errors.New("无法读取体检历史。可先保留原文件，或确认清除后重新记录")
	if err := noReparse(path); err != nil {
		return nil, unreadable
	}
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, unreadable
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > connectionCheckHistoryMaxBytes {
		return nil, unreadable
	}
	data, err := io.ReadAll(io.LimitReader(f, connectionCheckHistoryMaxBytes+1))
	if err != nil || len(data) > connectionCheckHistoryMaxBytes {
		return nil, unreadable
	}
	plain, err := crypt(data, true)
	if err != nil {
		return nil, unreadable
	}
	defer clear(plain)
	var runs []connectionCheckHistoryRun
	if json.Unmarshal(plain, &runs) != nil || !validConnectionCheckHistory(runs) {
		return nil, unreadable
	}
	return runs, nil
}

func validConnectionCheckHistory(runs []connectionCheckHistoryRun) bool {
	if runs == nil || len(runs) > connectionCheckHistoryLimit {
		return false
	}
	seen := map[string]bool{}
	for _, run := range runs {
		if !historyID.MatchString(run.ID) || seen[run.ID] || run.RecordedAt.IsZero() || len(run.Rows) == 0 || len(run.Rows) > 200 {
			return false
		}
		seen[run.ID] = true
		keys := map[string]bool{}
		for _, row := range run.Rows {
			if len(row.Key) != sha256.Size*2 || keys[row.Key] || !validConnectionCheckState(row.State) || !validConnectionCheckMount(row.Mount) {
				return false
			}
			if _, err := hex.DecodeString(row.Key); err != nil {
				return false
			}
			keys[row.Key] = true
		}
	}
	return true
}

func (s *DesktopService) writeConnectionCheckHistoryLocked(runs []connectionCheckHistoryRun) error {
	if !validConnectionCheckHistory(runs) {
		return errors.New("体检历史格式无效")
	}
	plain, err := json.Marshal(runs)
	if err != nil {
		return err
	}
	defer clear(plain)
	data, err := crypt(plain, false)
	if err != nil {
		return err
	}
	if len(data) > connectionCheckHistoryMaxBytes {
		return errors.New("体检历史大小异常")
	}
	return atomicWriteSettings(filepath.Join(s.dir, connectionCheckHistoryFile), data)
}
