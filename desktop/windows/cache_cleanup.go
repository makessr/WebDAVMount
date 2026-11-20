package desktop

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

type CacheCleanupPolicy struct {
	Enabled    bool `json:"enabled"`
	RetainDays int  `json:"retainDays"`
}
type CacheCleanupItem struct {
	ID      string `json:"id"`
	State   string `json:"state"`
	Message string `json:"message"`
}
type CacheCleanupInfo struct {
	Policy  CacheCleanupPolicy `json:"policy"`
	Items   []CacheCleanupItem `json:"items"`
	Warning string             `json:"warning"`
}

func cleanupFingerprint(root string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	plan, err := planCacheMigration(ctx, root)
	if err != nil {
		return "", err
	}
	if len(plan.Files) > 200000 || len(plan.Directories) > 200000 {
		return "", errors.New("旧缓存规模超出自动清理范围")
	}
	data, err := json.Marshal(plan)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:]), nil
}
func (s *DesktopService) readCleanupPolicy() (CacheCleanupPolicy, error) {
	p := CacheCleanupPolicy{RetainDays: 30}
	file := filepath.Join(s.dir, "cache-cleanup.json")
	if err := noReparse(file); err != nil {
		return p, err
	}
	data, err := os.ReadFile(file)
	if os.IsNotExist(err) {
		return p, nil
	}
	if err != nil {
		return p, err
	}
	if len(data) > 4096 || json.Unmarshal(data, &p) != nil || p.RetainDays < 7 || p.RetainDays > 365 {
		return CacheCleanupPolicy{RetainDays: 30}, errors.New("自动清理设置无法读取，已停止自动清理")
	}
	return p, nil
}
func (s *DesktopService) CacheCleanupInfo() CacheCleanupInfo {
	s.cleanupMu.Lock()
	defer s.cleanupMu.Unlock()
	p, err := s.readCleanupPolicy()
	info := CacheCleanupInfo{Policy: p, Items: append([]CacheCleanupItem{}, s.cleanupItems...)}
	if err != nil {
		info.Warning = err.Error()
	}
	return info
}
func (s *DesktopService) SaveCacheCleanupPolicy(p CacheCleanupPolicy) error {
	_, finish, err := s.beginOperation("cache.cleanup.policy")
	if err != nil {
		return err
	}
	defer finish()
	if p.RetainDays < 7 || p.RetainDays > 365 {
		return errors.New("旧缓存保留天数须为7至365天")
	}
	s.cleanupMu.Lock()
	defer s.cleanupMu.Unlock()
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return atomicWriteSettings(filepath.Join(s.dir, "cache-cleanup.json"), data)
}
func (s *DesktopService) RunRetainedCacheCleanup() (CacheCleanupInfo, error) {
	return s.runRetainedCacheCleanup(time.Now())
}
func (s *DesktopService) runRetainedCacheCleanup(now time.Time) (CacheCleanupInfo, error) {
	if !s.cleanupRunMu.TryLock() {
		return s.CacheCleanupInfo(), errors.New("旧缓存检查正在进行")
	}
	defer s.cleanupRunMu.Unlock()
	p, err := s.readCleanupPolicy()
	if err != nil {
		return s.CacheCleanupInfo(), err
	}
	if !p.Enabled {
		return s.CacheCleanupInfo(), nil
	}
	s.historyMu.Lock()
	entries, err := s.readCacheHistoryLocked()
	s.historyMu.Unlock()
	if err != nil {
		return s.CacheCleanupInfo(), err
	}
	items := []CacheCleanupItem{}
	s.cleanupMu.Lock()
	after := s.cleanupAfterID
	s.cleanupMu.Unlock()
	for i, entry := range entries {
		if entry.ID == after {
			entries = append(append([]CacheHistoryEntry{}, entries[i+1:]...), entries[:i+1]...)
			break
		}
	}
	for _, entry := range entries {
		if len(items) >= 3 {
			break
		}
		p, err = s.readCleanupPolicy()
		if err != nil || !p.Enabled {
			break
		}
		date, e := time.Parse(time.RFC3339, entry.RecordedAt)
		if e != nil || date.After(now) || now.Sub(date) < time.Duration(p.RetainDays)*24*time.Hour || entry.Kind != "migrated" || entry.CleanupIdentity == nil || entry.CleanupFingerprint == "" {
			continue
		}
		err = s.clearRetainedCache(entry.ID, entry.CleanupIdentity, entry.CleanupFingerprint)
		item := CacheCleanupItem{ID: entry.ID, State: "cleaned", Message: "已清理到期迁移缓存，目录和其它文件保留"}
		if err != nil {
			item.State = "skipped"
			item.Message = err.Error()
		}
		items = append(items, item)
		s.cleanupMu.Lock()
		s.cleanupAfterID = entry.ID
		s.cleanupMu.Unlock()
	}
	s.cleanupMu.Lock()
	s.cleanupItems = items
	s.cleanupMu.Unlock()
	return s.CacheCleanupInfo(), nil
}
func (s *DesktopService) startCacheCleanup() {
	go func() {
		timer := time.NewTimer(30 * time.Second)
		defer timer.Stop()
		for {
			select {
			case <-s.lifeCtx.Done():
				return
			case <-timer.C:
				s.RunRetainedCacheCleanup()
				timer.Reset(time.Hour)
			}
		}
	}()
}
