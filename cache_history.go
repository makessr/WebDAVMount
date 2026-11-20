package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const maxCacheHistory = 200

type CacheHistoryEntry struct {
	CleanupIdentity    *migrationIdentity `json:"cleanupIdentity,omitempty"`
	CleanupFingerprint string             `json:"cleanupFingerprint,omitempty"`
	ID                 string             `json:"id"`
	Path               string             `json:"path"`
	Kind               string             `json:"kind"`
	RecordedAt         string             `json:"recordedAt"`
	Current            bool               `json:"current"`
}
type CacheHistoryReport struct {
	Entries []CacheHistoryEntry `json:"entries"`
	Warning string              `json:"warning"`
}
type CacheHistoryUsage struct {
	LogicalBytes   int64  `json:"logicalBytes"`
	AllocatedBytes *int64 `json:"allocatedBytes"`
	Files          int    `json:"files"`
	Warning        string `json:"warning"`
}

var historyID = regexp.MustCompile(`^[a-f0-9]{32}$`)
var historyStage = regexp.MustCompile(`^\.WebDAVMount-migration-[a-zA-Z0-9]+$`)

func (s *DesktopService) managedHistoryPath(path string) bool {
	if !filepath.IsAbs(path) || strings.HasPrefix(path, `\\`) {
		return false
	}
	path = filepath.Clean(path)
	defaultPath, _ := filepath.Abs(filepath.Join(s.dir, "cache"))
	return strings.EqualFold(path, defaultPath) || strings.EqualFold(filepath.Base(path), s.customCacheDirectoryName()) || historyStage.MatchString(filepath.Base(path))
}
func validHistoryKind(kind string) bool {
	return kind == "migrated" || kind == "changed" || kind == "incomplete" || kind == "manual"
}
func (s *DesktopService) readCacheHistoryLocked() ([]CacheHistoryEntry, error) {
	path := filepath.Join(s.dir, "cache-history.enc")
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		return []CacheHistoryEntry{}, nil
	}
	if err := noReparse(path); err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.Size() > 8<<20 {
		return nil, errors.New("历史记录大小异常")
	}
	data, err := io.ReadAll(io.LimitReader(f, (8<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > 8<<20 {
		return nil, errors.New("历史记录大小异常")
	}
	plain, err := crypt(data, true)
	if err != nil {
		return nil, err
	}
	var entries []CacheHistoryEntry
	if json.Unmarshal(plain, &entries) != nil || len(entries) > maxCacheHistory {
		return nil, errors.New("历史记录格式异常")
	}
	seenID, seenPath := map[string]bool{}, map[string]bool{}
	for _, entry := range entries {
		key := strings.ToLower(filepath.Clean(entry.Path))
		_, dateErr := time.Parse(time.RFC3339, entry.RecordedAt)
		if !historyID.MatchString(entry.ID) || !s.managedHistoryPath(entry.Path) || !validHistoryKind(entry.Kind) || dateErr != nil || seenID[entry.ID] || seenPath[key] {
			return nil, errors.New("历史记录内容无效")
		}
		seenID[entry.ID] = true
		seenPath[key] = true
	}
	if entries == nil {
		entries = []CacheHistoryEntry{}
	}
	return entries, nil
}
func (s *DesktopService) writeCacheHistoryLocked(entries []CacheHistoryEntry) error {
	data, err := json.Marshal(entries)
	if err != nil {
		return err
	}
	encrypted, err := crypt(data, false)
	if err != nil {
		return err
	}
	return atomicWriteSettings(filepath.Join(s.dir, "cache-history.enc"), encrypted)
}
func (s *DesktopService) recordCacheHistory(path, kind string) error {
	absolute, err := filepath.Abs(path)
	if err != nil || !s.managedHistoryPath(absolute) || !validHistoryKind(kind) {
		return errors.New("该目录不是本程序可登记的缓存目录")
	}
	s.historyMu.Lock()
	defer s.historyMu.Unlock()
	entries, err := s.readCacheHistoryLocked()
	if err != nil {
		return errors.New("历史记录不可用，原记录已保留")
	}
	entry := CacheHistoryEntry{ID: randomID(), Path: filepath.Clean(absolute), Kind: kind, RecordedAt: time.Now().UTC().Format(time.RFC3339)}
	if kind == "migrated" {
		_, lease, e := migrationCanonicalDirectory(entry.Path)
		if e == nil {
			identity, e := migrationFileIdentity(lease)
			if e == nil {
				if digest, e := cleanupFingerprint(entry.Path); e == nil {
					entry.CleanupIdentity = &identity
					entry.CleanupFingerprint = digest
				}
			}
			lease.Close()
		}
	}
	next := make([]CacheHistoryEntry, 0, len(entries)+1)
	for _, existing := range entries {
		if strings.EqualFold(filepath.Clean(existing.Path), entry.Path) {
			entry.ID = existing.ID
		} else {
			existing.Current = false
			next = append(next, existing)
		}
	}
	if len(next) >= maxCacheHistory {
		return errors.New("最多保留200条目录记录，请先移除不需要的记录")
	}
	next = append([]CacheHistoryEntry{entry}, next...)
	if err = s.writeCacheHistoryLocked(next); err != nil {
		return errors.New("无法保存缓存目录记录")
	}
	return nil
}

// Listing never recursively scans directories or removes missing entries.
func (s *DesktopService) CacheHistory() CacheHistoryReport {
	s.mu.Lock()
	current := s.cacheRoot
	s.mu.Unlock()
	s.historyMu.Lock()
	entries, err := s.readCacheHistoryLocked()
	s.historyMu.Unlock()
	if err != nil {
		return CacheHistoryReport{Entries: []CacheHistoryEntry{}, Warning: "缓存目录记录无法读取，原文件已保留；不会自动扫描或删除磁盘目录。"}
	}
	for i := range entries {
		entries[i].Current = strings.EqualFold(filepath.Clean(entries[i].Path), filepath.Clean(current))
		entries[i].CleanupIdentity = nil
		entries[i].CleanupFingerprint = ""
	}
	report := CacheHistoryReport{Entries: entries}
	if len(entries) >= maxCacheHistory {
		report.Warning = "已达到200条记录上限；请先移除不需要的目录记录，文件不会被删除。"
	}
	return report
}
func (s *DesktopService) historyEntry(id string) (CacheHistoryEntry, error) {
	s.historyMu.Lock()
	defer s.historyMu.Unlock()
	entries, err := s.readCacheHistoryLocked()
	if err != nil {
		return CacheHistoryEntry{}, errors.New("缓存目录记录不可用")
	}
	for _, entry := range entries {
		if entry.ID == id {
			return entry, nil
		}
	}
	return CacheHistoryEntry{}, errors.New("缓存目录记录不存在，请刷新列表")
}
func (s *DesktopService) RememberCacheDirectory(path string) error {
	_, finish, err := s.beginOperation("cache.history")
	if err != nil {
		return err
	}
	defer finish()
	if !filepath.IsAbs(path) {
		return errors.New("请输入旧缓存目录的完整本机路径")
	}
	absolute, err := filepath.Abs(path)
	if err != nil || !s.managedHistoryPath(absolute) {
		return errors.New("请选择本程序的专用缓存目录本身，不要选择其父目录")
	}
	_, lease, err := migrationCanonicalDirectory(absolute)
	if err != nil {
		return errors.New("目录不存在、不可访问或包含链接，无法登记")
	}
	defer lease.Close()
	return s.recordCacheHistory(absolute, "manual")
}
func (s *DesktopService) ForgetCacheHistory(id string) error {
	_, finish, err := s.beginOperation("cache.history")
	if err != nil {
		return err
	}
	defer finish()
	s.historyMu.Lock()
	defer s.historyMu.Unlock()
	entries, err := s.readCacheHistoryLocked()
	if err != nil {
		return errors.New("缓存目录记录不可用")
	}
	next := make([]CacheHistoryEntry, 0, len(entries))
	found := false
	for _, entry := range entries {
		if entry.ID == id {
			found = true
		} else {
			entry.Current = false
			next = append(next, entry)
		}
	}
	if !found {
		return errors.New("缓存目录记录不存在，请刷新列表")
	}
	if err = s.writeCacheHistoryLocked(next); err != nil {
		return errors.New("无法移除缓存目录记录")
	}
	return nil
}
func (s *DesktopService) CacheHistoryUsage(id string) (CacheHistoryUsage, error) {
	ctx, finish, err := s.beginOperation("cache.history.scan")
	if err != nil {
		return CacheHistoryUsage{}, err
	}
	defer finish()
	entry, err := s.historyEntry(id)
	if err != nil {
		return CacheHistoryUsage{}, err
	}
	_, lease, err := migrationCanonicalDirectory(entry.Path)
	if err != nil {
		return CacheHistoryUsage{}, errors.New("目录不存在、不可访问或包含链接，记录仍保留")
	}
	defer lease.Close()
	usage, err := scanCacheUsageContext(ctx, entry.Path, fileAllocatedBytes)
	if errors.Is(err, context.Canceled) {
		return CacheHistoryUsage{}, errors.New("操作已取消")
	}
	if err != nil {
		return CacheHistoryUsage{}, errors.New("无法完整统计该目录，请检查磁盘或文件占用")
	}
	result := CacheHistoryUsage{LogicalBytes: usage.logical, Files: usage.files}
	if usage.allocationUnavailable {
		result.Warning = "部分文件磁盘占用无法读取，暂不显示合计"
	} else {
		result.AllocatedBytes = &usage.allocated
	}
	return result, nil
}
func (s *DesktopService) OpenCacheHistory(id string) error {
	entry, err := s.historyEntry(id)
	if err != nil {
		return err
	}
	_, lease, err := migrationCanonicalDirectory(entry.Path)
	if err != nil {
		return errors.New("目录不存在、不可访问或包含链接，无法打开")
	}
	defer lease.Close()
	if s.openDirectory == nil || s.openDirectory(entry.Path) != nil {
		return errors.New("无法打开该目录，请手动复制路径访问")
	}
	return nil
}
