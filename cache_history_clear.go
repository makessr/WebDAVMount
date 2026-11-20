package main

import (
	"errors"
	"os"
	"path/filepath"
	"time"
)

// ClearRetainedCache leaves the record, containing directory, and unrelated
// files intact. The caller confirms this destructive action in a dedicated dialog.
func (s *DesktopService) ClearRetainedCache(id string) error {
	return s.clearRetainedCache(id, nil, "")
}
func (s *DesktopService) clearRetainedCache(id string, expected *migrationIdentity, fingerprint string) error {
	ctx, finish, err := s.beginOperation("cache.history.clear")
	if err != nil {
		return err
	}
	defer finish()
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.requireNoMigration(); err != nil {
		return err
	}
	if err = s.cacheIdle(); err != nil {
		return err
	}
	entry, err := s.historyEntry(id)
	if err != nil {
		return err
	}
	old, lease, err := migrationCanonicalDirectory(entry.Path)
	if err != nil {
		return errors.New("旧缓存目录不可访问或包含链接，停止清理")
	}
	defer lease.Close()
	if expected != nil {
		policy, e := s.readCleanupPolicy()
		date, dateErr := time.Parse(time.RFC3339, entry.RecordedAt)
		if e != nil || !policy.Enabled || entry.Kind != "migrated" || entry.CleanupIdentity == nil || *entry.CleanupIdentity != *expected || entry.CleanupFingerprint != fingerprint || dateErr != nil || time.Since(date) < time.Duration(policy.RetainDays)*24*time.Hour {
			return errors.New("旧缓存不再满足自动清理条件，已跳过")
		}
		identity, e := migrationFileIdentity(lease)
		if e != nil || identity != *expected {
			return errors.New("旧缓存目录身份已变化，自动清理已跳过")
		}
		digest, e := cleanupFingerprint(old)
		if e != nil || digest != fingerprint {
			return errors.New("旧缓存内容已变化，自动清理已跳过")
		}
	}
	current, currentLease, err := migrationCanonicalDirectory(s.cacheRoot)
	if err != nil {
		return errors.New("无法确认当前缓存位置，停止清理")
	}
	defer currentLease.Close()
	if migrationWithin(old, current) || migrationWithin(current, old) {
		return errors.New("该目录正在使用或与当前缓存重叠，请使用当前缓存的清理入口")
	}
	targets := []string{}
	for _, name := range []string{"vfs", "vfsMeta"} {
		path := filepath.Join(entry.Path, name)
		info, e := os.Lstat(path)
		if os.IsNotExist(e) {
			continue
		}
		if e != nil || !info.IsDir() {
			return errors.New("旧缓存目录结构异常，停止清理")
		}
		if _, e = scanCacheUsageContext(ctx, path, nil); e != nil {
			return errors.New("旧缓存预检失败，文件占用、链接或目录变化；尚未开始清理")
		}
		targets = append(targets, path)
	}
	if ctx.Err() != nil {
		return errors.New("清理已取消，尚未开始删除")
	}
	// os.RemoveAll does not follow directory symlinks. The retained root is held
	// open without delete sharing so that it cannot be renamed during cleanup.
	for _, path := range targets {
		if err = os.RemoveAll(path); err != nil {
			return errors.New("部分旧缓存未能清理，可能有文件占用；记录保留，可稍后重试")
		}
	}
	if expected != nil {
		s.historyMu.Lock()
		defer s.historyMu.Unlock()
		entries, e := s.readCacheHistoryLocked()
		if e != nil {
			return errors.New("缓存已清理，但清理状态无法保存；目录记录保留")
		}
		for i := range entries {
			if entries[i].ID == id && entries[i].CleanupFingerprint == fingerprint {
				entries[i].CleanupIdentity = nil
				entries[i].CleanupFingerprint = ""
			}
		}
		if e = s.writeCacheHistoryLocked(entries); e != nil {
			return errors.New("缓存已清理，但清理状态无法保存；目录记录保留")
		}
	}
	return nil
}
