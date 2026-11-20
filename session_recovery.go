package main

import (
	"encoding/json"
	"errors"
	"io"
	"maps"
	"os"
	"path/filepath"
	"sync"
)

const sessionRecoveryFile = "mount-session.enc"
const sessionRecoveryLimit = 200
const sessionRecoveryMaxBytes = 64 << 10

// Only connection IDs and their last mounted letters are persisted. Previous
// entries are never added to the automatic recovery set for this process.
type sessionRecoveryJournal struct {
	mu       sync.Mutex
	path     string
	previous map[string]string
	current  map[string]string
	warning  string
	damaged  bool
	closed   bool
	dirty    bool
}

type SessionRecoveryStatus struct {
	Previous int    `json:"previous"`
	Warning  string `json:"warning,omitempty"`
}

func loadSessionRecovery(dir string, profiles []Profile) *sessionRecoveryJournal {
	j := &sessionRecoveryJournal{path: filepath.Join(dir, sessionRecoveryFile), previous: map[string]string{}, current: map[string]string{}}
	rows, err := readSessionRecovery(j.path)
	if err != nil {
		j.damaged = true
		j.warning = "上次挂载记录无法读取。可在恢复中心清除记录"
		return j
	}
	j.dirty = len(rows) != 0
	for _, profile := range profiles {
		if drive, ok := rows[profile.ID]; ok && drive == profile.Drive {
			j.previous[profile.ID] = profile.Drive
		}
	}
	return j
}

func readSessionRecovery(path string) (map[string]string, error) {
	if err := noReparse(path); err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("invalid mount journal")
	}
	data, err := io.ReadAll(io.LimitReader(f, sessionRecoveryMaxBytes+1))
	if err != nil || len(data) > sessionRecoveryMaxBytes {
		return nil, errors.New("invalid mount journal size")
	}
	plain, err := crypt(data, true)
	if err != nil {
		return nil, err
	}
	defer clear(plain)
	var rows map[string]string
	if json.Unmarshal(plain, &rows) != nil || rows == nil || len(rows) > sessionRecoveryLimit {
		return nil, errors.New("invalid mount journal data")
	}
	for id, drive := range rows {
		if id == "" || len(id) > 80 || len(drive) != 2 || drive[0] < 'D' || drive[0] > 'Z' || drive[1] != ':' {
			return nil, errors.New("invalid mount journal row")
		}
	}
	return rows, nil
}

func (j *sessionRecoveryJournal) persistLocked() error {
	rows := maps.Clone(j.previous)
	maps.Copy(rows, j.current)
	if len(rows) > sessionRecoveryLimit {
		return errors.New("mount journal has too many entries")
	}
	plain, err := json.Marshal(rows)
	if err != nil {
		return err
	}
	defer clear(plain)
	data, err := crypt(plain, false)
	if err != nil {
		return err
	}
	if err := atomicWriteSettings(j.path, data); err != nil {
		return err
	}
	j.dirty = len(rows) != 0
	j.warning = ""
	return nil
}

func (j *sessionRecoveryJournal) update(p Profile, mounted bool) {
	if j == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return
	}
	delete(j.previous, p.ID)
	if mounted {
		// A new occupant supersedes any previous session record for that letter.
		maps.DeleteFunc(j.previous, func(_ string, drive string) bool { return drive == p.Drive })
		j.current[p.ID] = p.Drive
	} else {
		delete(j.current, p.ID)
	}
	if !j.damaged && j.persistLocked() != nil {
		j.warning = "挂载记录未能保存，请检查本机配置目录"
	}
}

func (j *sessionRecoveryJournal) previousEntries() (map[string]string, SessionRecoveryStatus) {
	if j == nil {
		return nil, SessionRecoveryStatus{}
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	return maps.Clone(j.previous), SessionRecoveryStatus{Previous: len(j.previous), Warning: j.warning}
}

func (j *sessionRecoveryJournal) finish() {
	if j == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.closed = true
	if !j.damaged && j.dirty {
		clear(j.previous)
		clear(j.current)
		_ = j.persistLocked()
	}
}

func (s *DesktopService) DismissPreviousMounts() error {
	_, finish, err := s.beginOperation("recovery.dismiss")
	if err != nil {
		return err
	}
	defer finish()
	j := s.sessionRecovery
	if j == nil {
		return nil
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return errors.New("程序正在退出")
	}
	previous := j.previous
	j.previous = map[string]string{}
	if err := j.persistLocked(); err != nil {
		j.previous = previous
		return errors.New("无法清除上次挂载记录，请检查本机配置目录")
	}
	j.damaged = false
	return nil
}
