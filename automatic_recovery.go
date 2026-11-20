package main

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var recoveryDelays = [...]time.Duration{5 * time.Second, 15 * time.Second, 30 * time.Second}

type recoveryRetry struct {
	attempts        int
	pending, paused bool
	next, stable    time.Time
}

func (r *recoveryRetry) due(now time.Time, online, needed bool) bool {
	if !needed {
		*r = recoveryRetry{}
		return false
	}
	if online && !r.pending {
		if !r.stable.IsZero() && now.Sub(r.stable) >= time.Minute {
			*r = recoveryRetry{}
		}
		return false
	}
	r.stable = time.Time{}
	if r.attempts >= len(recoveryDelays) {
		r.paused = true
	}
	if r.paused {
		return false
	}
	if !r.pending {
		r.pending = true
	}
	if r.next.IsZero() {
		r.next = now.Add(recoveryDelays[r.attempts])
		return false
	}
	return !now.Before(r.next)
}
func (r *recoveryRetry) completed(now time.Time, success bool) {
	r.next = time.Time{}
	if success {
		r.pending = false
		r.stable = now
		return
	}
	r.pending = true
	if r.attempts >= len(recoveryDelays) {
		r.paused = true
		return
	}
	r.next = now.Add(recoveryDelays[r.attempts])
}

type AutoRecoveryInfo struct {
	Enabled   bool   `json:"enabled"`
	Active    bool   `json:"active"`
	Attempts  int    `json:"attempts"`
	NextRetry string `json:"nextRetry"`
	State     string `json:"state"`
	Message   string `json:"message"`
	Warning   string `json:"warning"`
}
type automaticRecovery struct {
	mu               sync.Mutex
	enabled, active  bool
	retry            recoveryRetry
	message, warning string
	profiles         []Profile
	health, results  map[string]string
	once             sync.Once
	done             chan struct{}
}

func loadAutomaticRecovery(dir string) *automaticRecovery {
	c := &automaticRecovery{}
	path := filepath.Join(dir, "recovery-settings.json")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return c
	}
	bad := func() *automaticRecovery {
		c.warning = "自动恢复设置无法读取，已保持关闭；可重新保存设置。"
		return c
	}
	if noReparse(path) != nil {
		return bad()
	}
	f, err := os.Open(path)
	if err != nil {
		return bad()
	}
	defer f.Close()
	if stat, err := f.Stat(); err != nil || stat.Size() > 1024 {
		return bad()
	}
	var config struct {
		Enabled *bool `json:"enabled"`
	}
	decoder := json.NewDecoder(io.LimitReader(f, 1025))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&config) != nil || config.Enabled == nil {
		return bad()
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return bad()
	}
	c.enabled = *config.Enabled
	return c
}

func (c *automaticRecovery) infoLocked() AutoRecoveryInfo {
	info := AutoRecoveryInfo{Enabled: c.enabled, Active: c.active, Attempts: c.retry.attempts, Message: c.message, Warning: c.warning, State: "watching"}
	switch {
	case !c.enabled:
		info.State = "disabled"
		info.Message = "自动恢复已关闭"
	case c.active:
		info.State = "running"
		info.Message = "正在自动恢复挂载，请稍候。"
	case c.retry.paused:
		info.State = "paused"
		info.Message = "已连续尝试3次，自动恢复已暂停；请手动恢复，或关闭后重新开启。"
	case !c.retry.next.IsZero():
		info.State = "waiting"
		info.Message = "挂载服务异常，已安排延迟重试。"
		info.NextRetry = c.retry.next.Format(time.RFC3339)
	case info.Message == "":
		info.Message = "已开启，仅在挂载服务异常时恢复本次成功挂载的网盘。"
	}
	return info
}
func (s *DesktopService) AutoRecoveryInfo() AutoRecoveryInfo {
	info, _ := s.automaticRecoverySnapshot()
	return info
}
func (s *DesktopService) automaticRecoverySnapshot() (AutoRecoveryInfo, map[string]any) {
	c := s.autoRecovery
	if c == nil {
		return AutoRecoveryInfo{State: "disabled", Message: "自动恢复已关闭"}, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	info := c.infoLocked()
	if !c.active {
		return info, nil
	}
	return info, map[string]any{"profiles": append([]Profile{}, c.profiles...), "mounts": []any{}, "autoRunning": false, "autoRecovering": true, "engineOnline": false, "health": copyStatus(c.health), "recovery": copyStatus(c.results)}
}
func (s *DesktopService) SaveAutoRecovery(enabled bool) (AutoRecoveryInfo, error) {
	_, finish, err := s.beginOperation("recovery.settings")
	if err != nil {
		return s.AutoRecoveryInfo(), err
	}
	defer finish()
	c := s.autoRecovery
	if c == nil {
		return AutoRecoveryInfo{}, errors.New("当前环境不支持自动恢复设置")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	fail := func() (AutoRecoveryInfo, error) {
		return c.infoLocked(), errors.New("无法保存自动恢复设置，请检查配置目录是否可写")
	}
	path := filepath.Join(s.dir, "recovery-settings.json")
	if noReparse(path) != nil {
		return fail()
	}
	f, err := os.CreateTemp(s.dir, "recovery-settings-*.tmp")
	if err != nil {
		return fail()
	}
	defer os.Remove(f.Name())
	data, _ := json.Marshal(struct {
		Enabled bool `json:"enabled"`
	}{enabled})
	_, writeErr := f.Write(data)
	syncErr := f.Sync()
	closeErr := f.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil {
		return fail()
	}
	if os.Rename(f.Name(), path) != nil {
		return fail()
	}
	c.enabled = enabled
	c.retry = recoveryRetry{}
	c.message = ""
	c.warning = ""
	return c.infoLocked(), nil
}

func recoverySucceeded(result map[string]string) bool {
	for _, message := range result {
		if strings.HasPrefix(message, "恢复失败：") {
			return false
		}
	}
	return true
}
func (s *DesktopService) startRecoveryMonitor() {
	c := s.autoRecovery
	if c == nil {
		return
	}
	c.once.Do(func() {
		c.done = make(chan struct{})
		go func() {
			defer close(c.done)
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-s.lifeCtx.Done():
					return
				case now := <-ticker.C:
					s.automaticRecoveryTick(now)
				}
			}
		}()
	})
}

func (s *DesktopService) automaticRecoveryTick(now time.Time) {
	c := s.autoRecovery
	if c == nil || !s.mu.TryLock() {
		return
	}
	defer s.mu.Unlock()
	s.startupMu.Lock()
	starting := s.startupActive
	s.startupMu.Unlock()
	if starting {
		return
	}
	c.mu.Lock()
	if !c.enabled {
		c.mu.Unlock()
		return
	}
	needed := false
	for _, p := range s.profiles {
		if s.desired[p.ID] {
			needed = true
			break
		}
	}
	if !c.retry.due(now, s.engineOnline(), needed) {
		c.mu.Unlock()
		return
	}
	ctx, finish, err := s.beginOperation("recovery.auto")
	if err != nil {
		c.mu.Unlock()
		return
	}
	defer finish()
	c.retry.attempts++
	c.active = true
	c.retry.next = time.Time{}
	c.profiles = append([]Profile{}, s.profiles...)
	for i := range c.profiles {
		c.profiles[i] = c.profiles[i].public()
	}
	c.health = copyStatus(s.health)
	c.results = copyStatus(s.recovery)
	c.mu.Unlock()
	started := time.Now()
	result, err := s.recoverLocked(ctx)
	success := err == nil && recoverySucceeded(result)
	if err == nil && !success {
		err = errors.New("自动恢复未全部完成")
	}
	s.recordDiagnostic("recovery.auto", err, time.Since(started))
	c.mu.Lock()
	defer c.mu.Unlock()
	c.active = false
	c.retry.completed(now.Add(time.Since(started)), success)
	if success {
		c.message = "自动恢复已完成，请刷新目录检查远端访问。"
	} else {
		c.message = "自动恢复未完成，请查看恢复结果或诊断日志。"
	}
}
