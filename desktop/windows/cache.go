package desktop

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"golang.org/x/sys/windows"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type CacheSettings struct {
	BaseDir string `json:"baseDir"`
	MaxGiB  int64  `json:"maxGiB"`
}
type CacheInfo struct {
	Settings          CacheSettings `json:"settings"`
	Directory         string        `json:"directory"`
	LogicalBytes      int64         `json:"logicalBytes"`
	AllocatedBytes    *int64        `json:"allocatedBytes"`
	AllocationWarning string        `json:"allocationWarning"`
	Files             int           `json:"files"`
	Warning           string        `json:"warning"`
}

func noReparse(path string) error {
	absolute, e := filepath.Abs(path)
	if e != nil {
		return e
	}
	for current := absolute; ; current = filepath.Dir(current) {
		ptr, e := windows.UTF16PtrFromString(current)
		if e != nil {
			return e
		}
		attrs, e := windows.GetFileAttributes(ptr)
		if e == nil && attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			return errors.New("缓存路径不能包含符号链接或目录联接")
		}
		if e != nil && !os.IsNotExist(e) {
			return e
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
	}
	return nil
}
func (s *DesktopService) cachePath(config CacheSettings) (string, error) {
	if config.MaxGiB < 1 || config.MaxGiB > 1024 {
		return "", errors.New("缓存容量须为1至1024 GiB的整数")
	}
	if config.BaseDir == "" {
		return filepath.Abs(filepath.Join(s.dir, "cache"))
	}
	if !filepath.IsAbs(config.BaseDir) || strings.HasPrefix(config.BaseDir, `\\`) {
		return "", errors.New("缓存位置须为本机磁盘的绝对目录")
	}
	base, e := filepath.Abs(config.BaseDir)
	if e != nil {
		return "", e
	}
	stat, e := os.Stat(base)
	if e != nil || !stat.IsDir() {
		return "", errors.New("缓存位置必须是已存在的目录")
	}
	return filepath.Join(base, s.customCacheDirectoryName()), nil
}
func (s *DesktopService) customCacheDirectoryName() string {
	owner, _ := filepath.Abs(s.dir)
	return fmt.Sprintf("WebDAVMount-cache-%x", sha256.Sum256([]byte(strings.ToLower(owner))))[:32]
}
func (s *DesktopService) prepareCache(config CacheSettings) (string, error) {
	root, e := s.cachePath(config)
	if e != nil {
		return "", e
	}
	if e = noReparse(root); e != nil {
		return "", e
	}
	if e = os.MkdirAll(root, 0700); e != nil {
		return "", e
	}
	probe, e := os.CreateTemp(root, ".write-check-")
	if e != nil {
		return "", errors.New("缓存目录不可写")
	}
	probe.Close()
	if e = os.Remove(probe.Name()); e != nil {
		return "", e
	}
	return root, nil
}
func (s *DesktopService) loadCacheSettings() error {
	config := CacheSettings{MaxGiB: 2}
	data, e := os.ReadFile(filepath.Join(s.dir, "cache-settings.json"))
	if e == nil {
		if e = json.Unmarshal(data, &config); e != nil {
			return errors.New("缓存设置损坏，请保留cache-settings.json排查")
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	config, recoveredRoot, e := s.recoverCacheSettingsForStartup(config)
	if e != nil {
		return e
	}
	if recoveredRoot != "" {
		s.cacheSettings, s.cacheRoot = config, recoveredRoot
		s.cacheWarning = "迁移目标身份无法确认，已临时使用原缓存；迁移记录和设置均保留"
		s.cacheMigrationFallback.Store(true)
		return nil
	}
	root, e := s.prepareCache(config)
	if e != nil && config.BaseDir != "" && config.MaxGiB >= 1 && config.MaxGiB <= 1024 {
		config.BaseDir = ""
		root, e = s.prepareCache(config)
		if e == nil {
			s.cacheWarning = "原缓存位置不可用，已临时使用默认位置；原设置仍保留，请检查目录或重新保存设置"
		}
	}
	if e != nil {
		return e
	}
	s.cacheSettings = config
	s.cacheRoot = root
	return nil
}
func (s *DesktopService) writeCacheSettings(config CacheSettings) error {
	data, e := json.Marshal(config)
	if e != nil {
		return e
	}
	return atomicWriteSettings(filepath.Join(s.dir, "cache-settings.json"), data)
}
func (s *DesktopService) adoptEngine(next *DesktopService) {
	s.cloudLogin = nil
	s.proxy = next.proxy
	s.addr, s.secret, s.cmd, s.done, s.client = next.addr, next.secret, next.cmd, next.done, next.client
	s.safeDriveReuse = next.safeDriveReuse
	s.connectionProxyAvailable = next.connectionProxyAvailable
	s.s3Preview = next.s3Preview
	s.retiredDrives = nil // Retired letters belonged to the reaped engine instance.
	s.cacheSettings, s.cacheRoot = next.cacheSettings, next.cacheRoot
	s.cacheWarning = next.cacheWarning
	s.cacheMigrationFallback.Store(next.cacheMigrationFallback.Load())
}
func (s *DesktopService) cacheIdle() error {
	if err := s.writableGuard(); err != nil {
		return err
	}
	s.startupMu.Lock()
	active := s.startupActive
	s.startupMu.Unlock()
	if active {
		return errors.New("请等待启动挂载完成")
	}
	if !s.engineOnline() {
		return errors.New("请先恢复后台引擎，再操作缓存")
	}
	result, e := s.call("mount/listmounts", nil)
	if e != nil {
		return e
	}
	list, ok := result["mountPoints"].([]any)
	if !ok {
		return errors.New("无法确认挂载状态")
	}
	if len(list) != 0 {
		return errors.New("请先卸载本程序的全部磁盘")
	}
	return nil
}

// Directory-operation records are keyed by the absolute VFS cache path.
// Changing that path would leave any pending review behind.
func checkCacheLocationReviews(root string) error {
	path := filepath.Join(root, collectionJournalDirectory)
	unavailable := errors.New("无法确认原缓存中的目录操作记录，已保留原缓存位置")
	if err := noReparse(path); err != nil {
		return unavailable
	}
	dir, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return unavailable
	}
	defer dir.Close()
	entries, err := dir.ReadDir(1)
	if len(entries) != 0 {
		return errors.New("有目录操作待确认，请重新挂载对应网盘并核对结果，再更换缓存位置")
	}
	if err != io.EOF {
		return unavailable
	}
	return nil
}

func scanCache(root string) (int64, int, error) {
	return scanCacheContext(context.Background(), root)
}
func scanCacheContext(ctx context.Context, root string) (int64, int, error) {
	usage, err := scanCacheUsageContext(ctx, root, nil)
	return usage.logical, usage.files, err
}

type cacheUsage struct {
	logical, allocated    int64
	files                 int
	allocationUnavailable bool
}

func scanCacheUsageContext(ctx context.Context, root string, allocation func(string) (int64, error)) (cacheUsage, error) {
	var usage cacheUsage
	if e := ctx.Err(); e != nil {
		return usage, e
	}
	if e := noReparse(root); e != nil {
		return usage, e
	}
	e := filepath.WalkDir(root, func(path string, d fs.DirEntry, e error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if e != nil {
			return e
		}
		if e = noReparse(path); e != nil {
			return e
		}
		if !d.IsDir() {
			info, e := d.Info()
			if e != nil {
				return e
			}
			usage.logical += info.Size()
			usage.files++
			if allocation != nil {
				if size, err := allocation(path); err == nil {
					usage.allocated += size
				} else {
					usage.allocationUnavailable = true
				}
			}
		}
		return nil
	})
	return usage, e
}
func (s *DesktopService) CacheInfo() (CacheInfo, error) {
	ctx, finish, err := s.beginOperation("cache.scan")
	if err != nil {
		return CacheInfo{}, err
	}
	defer finish()
	s.mu.Lock()
	config, root, warning := s.cacheSettings, s.cacheRoot, s.cacheWarning
	s.mu.Unlock()
	started := time.Now()
	usage, e := scanCacheUsageContext(ctx, root, fileAllocatedBytes)
	s.recordDiagnostic("cache.scan", e, time.Since(started))
	if e == context.Canceled {
		return CacheInfo{}, errors.New("缓存扫描已取消")
	}
	info := CacheInfo{Settings: config, Directory: root, LogicalBytes: usage.logical, Files: usage.files, Warning: warning}
	if !usage.allocationUnavailable && e == nil {
		info.AllocatedBytes = &usage.allocated
	} else {
		info.AllocationWarning = "部分文件的磁盘占用无法读取，请稍后刷新；逻辑大小仍可查看"
	}
	return info, e
}
func (s *DesktopService) restartCacheEngine() error {
	engine := s.cmd.Path
	s.close()
	next, e := newDesktop(s.dir, engine, s.sessionLog())
	if e != nil {
		return e
	}
	s.adoptEngine(next)
	s.retiredDrives = nil
	return nil
}
func (s *DesktopService) SaveCacheSettings(config CacheSettings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.requireNoMigration(); err != nil {
		return err
	}
	if e := s.cacheIdle(); e != nil {
		return e
	}
	target, e := s.cachePath(config)
	if e != nil {
		return e
	}
	if filepath.Clean(target) != filepath.Clean(s.cacheRoot) {
		if e := checkCacheLocationReviews(s.cacheRoot); e != nil {
			return e
		}
	}
	if _, e := s.prepareCache(config); e != nil {
		return e
	}
	old := s.cacheSettings
	oldRoot := s.cacheRoot
	if e := s.writeCacheSettings(config); e != nil {
		return e
	}
	if e := s.restartCacheEngine(); e != nil {
		if rollback := s.writeCacheSettings(old); rollback != nil {
			return fmt.Errorf("应用缓存设置失败，恢复旧设置也失败，请保留配置文件排查: %w", rollback)
		}
		if rollback := s.restartCacheEngine(); rollback != nil {
			return fmt.Errorf("新设置失败，旧设置已恢复，请重启程序: %w", rollback)
		}
		return errors.New("新缓存设置未能应用，已恢复旧设置")
	}
	if !strings.EqualFold(filepath.Clean(oldRoot), filepath.Clean(s.cacheRoot)) {
		if err := s.recordCacheHistory(oldRoot, "changed"); err != nil {
			s.cacheWarning = "旧缓存目录记录未保存，请记下原位置：" + oldRoot
		}
	}
	return nil
}
func (s *DesktopService) ClearCache() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.requireNoMigration(); err != nil {
		return err
	}
	if e := s.cacheIdle(); e != nil {
		return e
	}
	root, e := filepath.Abs(s.cacheRoot)
	if e != nil {
		return e
	}
	expected, e := s.cachePath(s.cacheSettings)
	if e != nil || !strings.EqualFold(root, expected) {
		return errors.New("缓存目录与配置不一致，停止清理")
	}
	if _, _, e = scanCache(root); e != nil {
		return e
	}
	targets := []string{}
	for _, name := range []string{"vfs", "vfsMeta"} {
		target := filepath.Join(root, name)
		relative, e := filepath.Rel(root, target)
		if e != nil || relative != name || !filepath.IsAbs(target) {
			return errors.New("缓存清理目标越界")
		}
		targets = append(targets, target)
	}
	engine := s.cmd.Path
	s.close()
	var removeErr error
	for _, target := range targets {
		if e = os.RemoveAll(target); e != nil {
			removeErr = e
			break
		}
	}
	next, e := newDesktop(s.dir, engine, s.sessionLog())
	if e != nil {
		return errors.New("缓存处理后引擎未能启动，请重启程序")
	}
	s.adoptEngine(next)
	s.retiredDrives = nil
	return removeErr
}
