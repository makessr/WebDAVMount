package desktop

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type MaintenanceSettings struct {
	RemoteChecks   bool `json:"remoteChecks"`
	EngineWatchdog bool `json:"engineWatchdog"`
}
type RemoteProbe struct {
	State     string `json:"state"`
	Message   string `json:"message"`
	CheckedAt string `json:"checkedAt"`
	NextRetry string `json:"nextRetry"`
	Failures  int    `json:"failures"`
	next      time.Time
}
type MaintenanceInfo struct {
	Settings        MaintenanceSettings    `json:"settings"`
	Connections     map[string]RemoteProbe `json:"connections"`
	WatchdogMessage string                 `json:"watchdogMessage"`
	Warning         string                 `json:"warning"`
}
type connectionMaintenance struct {
	mu                       sync.Mutex
	settings                 MaintenanceSettings
	connections              map[string]RemoteProbe
	warning, watchdogMessage string
	engine                   *exec.Cmd
	misses                   int
	watchNext                time.Time
	timeout                  time.Duration
	once                     sync.Once
}

func loadMaintenance(dir string) *connectionMaintenance {
	c := &connectionMaintenance{connections: map[string]RemoteProbe{}, timeout: 2 * time.Second}
	path := filepath.Join(dir, "maintenance-settings.json")
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		return c
	}
	bad := func() *connectionMaintenance {
		c.warning = "连接维护设置无法读取，已保持关闭；请重新保存。"
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
	data, err := io.ReadAll(io.LimitReader(f, 2049))
	if err != nil || len(data) > 2048 {
		return bad()
	}
	var saved struct {
		RemoteChecks   *bool `json:"remoteChecks"`
		EngineWatchdog *bool `json:"engineWatchdog"`
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if d.Decode(&saved) != nil || saved.RemoteChecks == nil || saved.EngineWatchdog == nil {
		return bad()
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return bad()
	}
	c.settings = MaintenanceSettings{*saved.RemoteChecks, *saved.EngineWatchdog}
	return c
}
func (s *DesktopService) MaintenanceInfo() MaintenanceInfo {
	result := MaintenanceInfo{Connections: map[string]RemoteProbe{}}
	c := s.maintenance
	if c == nil {
		return result
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	result.Settings = c.settings
	result.Warning = c.warning
	result.WatchdogMessage = c.watchdogMessage
	for id, p := range c.connections {
		result.Connections[id] = p
	}
	return result
}
func (s *DesktopService) SaveMaintenance(settings MaintenanceSettings) (MaintenanceInfo, error) {
	_, finish, err := s.beginOperation("maintenance.settings")
	if err != nil {
		return s.MaintenanceInfo(), err
	}
	defer finish()
	c := s.maintenance
	if c == nil {
		return s.MaintenanceInfo(), errors.New("当前环境不支持连接维护")
	}
	if settings.EngineWatchdog && !s.AutoRecoveryInfo().Enabled {
		return s.MaintenanceInfo(), errors.New("请先开启并保存挂载服务异常后自动恢复")
	}
	c.mu.Lock()
	data, _ := json.Marshal(settings)
	err = atomicWriteSettings(filepath.Join(s.dir, "maintenance-settings.json"), data)
	if err == nil {
		c.settings = settings
		c.warning = ""
		c.connections = map[string]RemoteProbe{}
		c.misses = 0
		c.watchNext = time.Time{}
		c.watchdogMessage = ""
	}
	c.mu.Unlock()
	if err != nil {
		return s.MaintenanceInfo(), errors.New("无法保存连接维护设置，请检查配置目录")
	}
	return s.MaintenanceInfo(), nil
}
func probeDelay(failures int) time.Duration {
	switch failures {
	case 0, 1:
		return 30 * time.Second
	case 2:
		return time.Minute
	case 3:
		return 2 * time.Minute
	default:
		return 5 * time.Minute
	}
}
func (s *DesktopService) startMaintenanceMonitor() {
	c := s.maintenance
	if c == nil {
		return
	}
	c.once.Do(func() {
		go func() {
			ticker := time.NewTicker(5 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-s.lifeCtx.Done():
					return
				case now := <-ticker.C:
					s.maintenanceTick(now)
				}
			}
		}()
	})
}

// All probes are bounded, read-only and serialized with engine changes. Never
// unmount a healthy disk just because the remote service is temporarily absent.
func (s *DesktopService) maintenanceTick(now time.Time) {
	c := s.maintenance
	if c == nil || !s.mu.TryLock() {
		return
	}
	defer s.mu.Unlock()
	s.opMu.Lock()
	busy := s.closing || s.opCancel != nil
	s.opMu.Unlock()
	if busy {
		return
	}
	s.startupMu.Lock()
	starting := s.startupActive
	s.startupMu.Unlock()
	if starting || !s.engineOnline() {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.settings.RemoteChecks && !c.settings.EngineWatchdog {
		return
	}
	// Writable mounts are excluded from automatic read-only remounting. Their
	// journal still matters to the watchdog, even when no read-only drive exists.
	protectedWritable := s.writable.Load() != nil
	needed := c.settings.EngineWatchdog && protectedWritable
	for _, p := range s.profiles {
		if s.desired[p.ID] {
			needed = true
			break
		}
	}
	if !needed {
		c.connections = map[string]RemoteProbe{}
		c.misses = 0
		c.watchdogMessage = ""
		return
	}
	for id := range c.connections {
		if !s.desired[id] {
			delete(c.connections, id)
		}
	}
	if c.engine != s.cmd {
		c.engine = s.cmd
		c.misses = 0
		c.watchNext = time.Time{}
		c.watchdogMessage = ""
		c.connections = map[string]RemoteProbe{}
	}
	timeout := c.timeout
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	if c.settings.EngineWatchdog && s.AutoRecoveryInfo().Enabled && !now.Before(c.watchNext) {
		c.watchNext = now.Add(15 * time.Second)
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		_, err := s.callContext(ctx, "core/version", nil)
		cancel()
		if s.lifeCtx != nil && s.lifeCtx.Err() != nil {
			return
		}
		if err == nil {
			c.misses = 0
			c.watchdogMessage = "挂载服务响应正常"
		} else {
			if protectedWritable {
				// An unresponsive engine cannot confirm whether a writable
				// handle or upload is active. Keep it and probe again later.
				c.misses = 0
				c.watchdogMessage = "挂载服务无响应，已暂停自动重启以保护可写会话"
				return
			}
			c.misses++
			c.watchdogMessage = "挂载服务响应异常，正在重复确认"
		}
		if c.misses >= 3 && s.cmd != nil && s.cmd.Process != nil {
			// Commit the restart decision only while no user operation or exit
			// has begun, and only if automatic recovery is still enabled.
			enabled := s.AutoRecoveryInfo().Enabled
			s.opMu.Lock()
			allowed := enabled && !s.closing && s.opCancel == nil
			if allowed && s.cmd.Process.Kill() == nil {
				c.watchdogMessage = "已停止连续无响应的服务，等待自动恢复挂载"
				if s.AutoRecoveryInfo().State == "paused" {
					c.watchdogMessage = "已停止无响应的服务；自动恢复达到上限，请手动恢复挂载"
				}
				c.misses = 0
			}
			s.opMu.Unlock()
			return
		}
	}
	if !c.settings.RemoteChecks {
		return
	}
	// At most one due connection per tick avoids a burst against every provider.
	for _, p := range s.profiles {
		if !s.desired[p.ID] {
			delete(c.connections, p.ID)
			continue
		}
		probe := c.connections[p.ID]
		if probe.State == "paused" {
			continue
		}
		if now.Before(probe.next) {
			continue
		}
		err := s.probeRemoteRoot(context.Background(), p, timeout)
		if s.lifeCtx != nil && s.lifeCtx.Err() != nil {
			return
		}
		probe.CheckedAt = now.Format(time.RFC3339)
		if err == nil {
			probe.State = "online"
			probe.Message = "网盘连接正常"
			if probe.Failures > 0 {
				probe.Message = "网盘连接已恢复"
			}
			probe.Failures = 0
		} else {
			probe.State = "offline"
			probe.Message = "后台检查未成功，将延迟重试：" + err.Error()
			if probe.Failures < 100 {
				probe.Failures++
			}
		}
		probe.next = now.Add(probeDelay(probe.Failures))
		probe.NextRetry = probe.next.Format(time.RFC3339)
		if err != nil && (strings.Contains(err.Error(), "账号或应用密码") || strings.Contains(err.Error(), "证书验证失败")) {
			probe.State = "paused"
			probe.Message = "认证或证书检查失败，后台重试已暂停。请核对连接设置"
			probe.NextRetry = ""
		}
		c.connections[p.ID] = probe
		break
	}
}

func (s *DesktopService) probeRemoteRoot(ctx context.Context, p Profile, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	result, err := s.callProfileContext(ctx, p, "operations/stat", map[string]any{
		"remote": "", "opt": map[string]bool{"noModTime": true, "noMimeType": true},
	})
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return errors.New("本次连接检查超时")
	}
	if err == nil && result["item"] == nil {
		return errors.New("无法确认远端根目录是否存在")
	}
	return err
}

// RetryMountConnection refreshes the selected connection's status without
// listing files or changing its mount. It can also retry a paused probe.
func (s *DesktopService) RetryMountConnection(id string) (MaintenanceInfo, error) {
	ctx, finish, err := s.beginOperation("maintenance.retry")
	if err != nil {
		return MaintenanceInfo{}, err
	}
	defer finish()
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.find(id)
	if err != nil {
		return MaintenanceInfo{}, err
	}
	if !s.engineOnline() || !s.desired[id] || !s.MaintenanceInfo().Settings.RemoteChecks {
		return MaintenanceInfo{}, errors.New("请先挂载网盘并开启后台连接检查")
	}
	err = s.probeRemoteRoot(ctx, p, connectionCheckTimeout)
	if ctx.Err() != nil || (s.lifeCtx != nil && s.lifeCtx.Err() != nil) {
		return MaintenanceInfo{}, errors.New("操作已取消")
	}
	s.maintenanceAccess(id, err)
	return s.MaintenanceInfo(), nil
}

// A fresh explicit read supersedes an older background result. A user cancel
// says nothing about connectivity and must not be reported as an outage.
func (s *DesktopService) maintenanceAccess(id string, err error) {
	c := s.maintenance
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.settings.RemoteChecks || err != nil && strings.Contains(err.Error(), "操作已取消") {
		return
	}
	if c.engine != s.cmd {
		c.engine = s.cmd
		c.connections = map[string]RemoteProbe{}
	}
	if c.connections == nil {
		c.connections = map[string]RemoteProbe{}
	}
	p := c.connections[id]
	now := time.Now()
	p.CheckedAt = now.Format(time.RFC3339)
	if err == nil {
		p.State = "online"
		p.Message = "网盘连接正常"
		p.Failures = 0
	} else {
		p.State = "offline"
		p.Message = "连接检查未成功，将延迟重试：" + err.Error()
		if p.Failures < 100 {
			p.Failures++
		}
	}
	p.next = now.Add(probeDelay(p.Failures))
	p.NextRetry = p.next.Format(time.RFC3339)
	if err != nil && (strings.Contains(err.Error(), "账号或应用密码") || strings.Contains(err.Error(), "证书验证失败")) {
		p.State = "paused"
		p.Message = "认证或证书检查失败，后台重试已暂停。请核对连接设置"
		p.NextRetry = ""
	}
	c.connections[id] = p
}
