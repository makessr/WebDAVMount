package main

import (
	"context"
	"encoding/json"
	"errors"
	"golang.org/x/sys/windows"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type CacheMigrationPreview struct {
	Source string `json:"source"`
	Target string `json:"target"`
}
type CacheMigrationStatus struct {
	HistoryWarning    string `json:"historyWarning,omitempty"`
	Active            bool   `json:"active"`
	Phase             string `json:"phase"`
	Source            string `json:"source"`
	Target            string `json:"target"`
	RetainedDirectory string `json:"retainedDirectory"`
	Files             int    `json:"files"`
	TotalFiles        int    `json:"totalFiles"`
	Bytes             int64  `json:"bytes"`
	TotalBytes        int64  `json:"totalBytes"`
	Message           string `json:"message"`
	ReusedFiles       int    `json:"reusedFiles"`
	ReusedBytes       int64  `json:"reusedBytes"`
}
type migrationEntry struct {
	Path     string
	Size     int64
	Modified time.Time
}
type migrationPlan struct {
	Files             []migrationEntry
	Directories       []string
	Logical, Required int64
}
type migrationProblem string

func (e migrationProblem) Error() string { return string(e) }

func (s *DesktopService) CacheMigrationStatus() CacheMigrationStatus {
	s.migrationMu.Lock()
	defer s.migrationMu.Unlock()
	return s.migration
}
func (s *DesktopService) migrationUpdate(fn func(*CacheMigrationStatus)) {
	s.migrationMu.Lock()
	defer s.migrationMu.Unlock()
	fn(&s.migration)
}
func migrationWithin(parent, child string) bool {
	parent = strings.TrimRight(strings.ToLower(parent), `\`)
	child = strings.TrimRight(strings.ToLower(child), `\`)
	return parent == child || strings.HasPrefix(child, parent+`\`)
}

func (s *DesktopService) migrationPaths(config CacheSettings) (CacheMigrationPreview, []*os.File, error) {
	empty := CacheMigrationPreview{}
	if err := s.cacheIdle(); err != nil {
		return empty, nil, err
	}
	if err := checkCacheLocationReviews(s.cacheRoot); err != nil {
		return empty, nil, err
	}
	expected, err := s.cachePath(s.cacheSettings)
	if err != nil || !strings.EqualFold(filepath.Clean(expected), filepath.Clean(s.cacheRoot)) {
		return empty, nil, migrationProblem("当前缓存目录与配置不一致，停止迁移")
	}
	target, err := s.cachePath(config)
	if err != nil {
		return empty, nil, err
	}
	source := s.cacheRoot
	canonicalSource, sourceLease, err := migrationCanonicalDirectory(source)
	if err != nil {
		return empty, nil, migrationProblem("无法安全访问原缓存目录")
	}
	parent := filepath.Dir(target)
	canonicalParent, parentLease, err := migrationCanonicalDirectory(parent)
	if err != nil {
		sourceLease.Close()
		return empty, nil, migrationProblem("无法安全访问目标文件夹")
	}
	leases := []*os.File{sourceLease, parentLease}
	fail := func(message string) (CacheMigrationPreview, []*os.File, error) {
		for _, f := range leases {
			f.Close()
		}
		return empty, nil, migrationProblem(message)
	}
	canonicalTarget := canonicalParent + `\` + filepath.Base(target)
	if migrationWithin(canonicalSource, canonicalTarget) || migrationWithin(canonicalTarget, canonicalSource) {
		return fail("新旧缓存目录相同或相互包含，请选择其他位置")
	}
	if _, err = os.Lstat(target); !os.IsNotExist(err) {
		return fail("目标专用缓存目录已存在或不可访问；迁移不会覆盖或合并，请选择新文件夹")
	}
	return CacheMigrationPreview{source, target}, leases, nil
}
func (s *DesktopService) PreviewCacheMigration(config CacheSettings) (CacheMigrationPreview, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.requireNoMigration(); err != nil {
		return CacheMigrationPreview{}, err
	}
	preview, leases, err := s.migrationPaths(config)
	for _, f := range leases {
		f.Close()
	}
	return preview, err
}

func planCacheMigration(ctx context.Context, root string) (migrationPlan, error) {
	plan := migrationPlan{}
	for _, name := range []string{"vfs", "vfsMeta"} {
		tree := filepath.Join(root, name)
		if _, err := os.Lstat(tree); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return plan, err
		}
		err := filepath.WalkDir(tree, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if err = ctx.Err(); err != nil {
				return err
			}
			if len(plan.Files) >= 200000 || len(plan.Directories) >= 200000 {
				return migrationProblem("缓存文件或目录超过迁移上限")
			}
			if err = noReparse(path); err != nil {
				return err
			}
			relative, err := filepath.Rel(root, path)
			if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
				return migrationProblem("缓存迁移路径越界")
			}
			if d.IsDir() {
				plan.Directories = append(plan.Directories, relative)
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() || info.Size() < 0 {
				return migrationProblem("缓存包含无法迁移的特殊文件")
			}
			if info.Size() > int64(^uint64(0)>>1)-plan.Logical {
				return migrationProblem("缓存容量超出支持范围")
			}
			plan.Logical += info.Size()
			required := info.Size()
			ptr, _ := windows.UTF16PtrFromString(path)
			attrs, err := windows.GetFileAttributes(ptr)
			if err != nil {
				return err
			}
			if attrs&windows.FILE_ATTRIBUTE_SPARSE_FILE != 0 {
				if allocated, e := fileAllocatedBytes(path); e == nil {
					required = allocated
				}
			}
			if required > int64(^uint64(0)>>1)-plan.Required-65536 {
				return migrationProblem("缓存容量超出支持范围")
			}
			plan.Required += required + 65536
			plan.Files = append(plan.Files, migrationEntry{relative, info.Size(), info.ModTime()})
			return nil
		})
		if err != nil {
			return plan, err
		}
	}
	return plan, nil
}
func sameMigrationPlan(a, b migrationPlan) bool {
	if len(a.Files) != len(b.Files) || len(a.Directories) != len(b.Directories) {
		return false
	}
	for i, e := range a.Files {
		v := b.Files[i]
		if e.Path != v.Path || e.Size != v.Size || !e.Modified.Equal(v.Modified) {
			return false
		}
	}
	for i, path := range a.Directories {
		if path != b.Directories[i] {
			return false
		}
	}
	return true
}

// Copy -> verify -> publish -> switch. Original cache data is never removed.
// A failed/cancelled stage is retained separately and cannot be used implicitly.
func (s *DesktopService) MigrateCache(config CacheSettings) (CacheMigrationStatus, error) {
	return s.migrateCache(config, false)
}

func (s *DesktopService) migrateCache(config CacheSettings, resume bool) (result CacheMigrationStatus, err error) {
	ctx, finish, err := s.beginOperation("cache.migrate")
	if err != nil {
		return result, err
	}
	defer finish()
	s.mu.Lock()
	defer s.mu.Unlock()
	var journal *migrationJournal
	var preview CacheMigrationPreview
	var leases []*os.File
	var stage string
	var published bool
	if resume {
		journal, err = s.readMigrationJournal()
		if err != nil || journal == nil {
			return result, migrationProblem("迁移任务不可用，请刷新任务列表")
		}
		config = journal.Config
		preview, leases, stage, published, err = s.resumeMigrationPaths(journal)
	} else {
		if err = s.requireNoMigration(); err != nil {
			return result, err
		}
		preview, leases, err = s.migrationPaths(config)
	}
	if err != nil {
		return result, err
	}
	defer func() {
		for _, f := range leases {
			f.Close()
		}
	}()
	settingPath := filepath.Join(s.dir, "cache-settings.json")
	original, readErr := os.ReadFile(settingPath)
	if readErr != nil && !os.IsNotExist(readErr) {
		return result, migrationProblem("无法读取原缓存设置")
	}
	originalExists := readErr == nil
	s.migrationUpdate(func(p *CacheMigrationStatus) {
		*p = CacheMigrationStatus{Active: true, Phase: "preparing", Source: preview.Source, Target: preview.Target, RetainedDirectory: stage, Message: "正在准备迁移"}
	})
	started := time.Now()
	stopped, settingsWritten, success := false, false, false
	defer func() {
		if stopped && !success {
			var restoreErr error
			if settingsWritten {
				if originalExists {
					restoreErr = atomicWriteSettings(settingPath, original)
				} else {
					restoreErr = os.Remove(settingPath)
				}
			}
			if restoreErr == nil {
				var next *DesktopService
				next, restoreErr = newDesktop(s.dir, s.cmd.Path, s.sessionLog())
				if restoreErr == nil {
					s.adoptEngine(next)
				}
			}
			if restoreErr != nil {
				err = migrationProblem("原缓存已保留，但设置或挂载服务恢复失败，请保留配置文件并重启程序")
			}
		}
		if err != nil {
			if errors.Is(err, context.Canceled) {
				err = errors.New("操作已取消")
			} else {
				var known migrationProblem
				if !errors.As(err, &known) {
					err = migrationProblem("缓存迁移未完成，原缓存已保留；请检查目标磁盘或文件占用")
				}
			}
		}
		s.migrationUpdate(func(p *CacheMigrationStatus) {
			p.Active = false
			if success {
				p.Phase = "done"
				p.Message = "缓存已迁移并校验，原缓存仍保留"
			} else {
				p.Phase = "failed"
				if err != nil && err.Error() == "操作已取消" {
					p.Phase = "cancelled"
				}
				if err != nil {
					p.Message = err.Error()
					if p.Phase == "cancelled" {
						p.Message = "缓存迁移已暂停，原位置继续使用；可稍后继续迁移"
					}
				}
			}
		})
		status := s.CacheMigrationStatus()
		retained, kind := status.RetainedDirectory, "incomplete"
		if success {
			retained = preview.Source
			kind = "migrated"
		}
		if retained != "" && !success {
			if historyErr := s.recordCacheHistory(retained, kind); historyErr != nil {
				s.migrationUpdate(func(p *CacheMigrationStatus) {
					p.HistoryWarning = "保留目录未能写入历史记录，请记下上方路径。"
				})
			}
		}
		result = s.CacheMigrationStatus()
		s.recordDiagnostic("cache.migrate", err, time.Since(started))
	}()
	if err = ctx.Err(); err != nil {
		return result, err
	}
	s.close()
	stopped = true
	plan, err := planCacheMigration(ctx, preview.Source)
	if err != nil {
		return result, err
	}
	s.migrationUpdate(func(p *CacheMigrationStatus) { p.TotalFiles = len(plan.Files); p.TotalBytes = plan.Logical })
	ptr, _ := windows.UTF16PtrFromString(filepath.Dir(preview.Target))
	var free uint64
	if err = windows.GetDiskFreeSpaceEx(ptr, &free, nil, nil); err != nil {
		return result, err
	}
	if free < uint64(plan.Required)+1048576 {
		// Existing complete files need no second allocation on resume. The
		// write itself still handles exhaustion and leaves the journal intact.
		if journal == nil || free < 1048576 {
			return result, migrationProblem("目标磁盘剩余空间不足，未切换缓存位置")
		}
	}
	if journal == nil {
		if len(plan.Files) > 200000 || len(plan.Directories) > 200000 {
			return result, migrationProblem("缓存文件过多，请先清理缓存或直接更换位置")
		}
		stage, err = os.MkdirTemp(filepath.Dir(preview.Target), ".WebDAVMount-migration-")
		if err != nil {
			return result, err
		}
		s.migrationUpdate(func(p *CacheMigrationStatus) { p.RetainedDirectory = stage })
		_, lease, e := migrationCanonicalDirectory(stage)
		if e != nil {
			return result, e
		}
		leases = append(leases, lease)
		sourceID, e := migrationFileIdentity(leases[0])
		if e != nil {
			return result, e
		}
		stageID, e := migrationFileIdentity(lease)
		if e != nil {
			return result, e
		}
		journal = &migrationJournal{Version: 1, Created: time.Now().UTC(), Source: preview.Source, Target: preview.Target, Stage: stage, Config: config, SourceID: sourceID, StageID: stageID, Plan: plan}
		if err = s.writeMigrationJournal(journal); err != nil {
			return result, err
		}
	} else if !sameMigrationPlan(journal.Plan, plan) {
		return result, migrationProblem("原缓存内容已变化，不能继续旧迁移；请放弃任务后重新迁移，副本会保留")
	}
	// Unknown root content is not part of this journal and must not be
	// published as active cache. Never delete it as part of recovery.
	rootEntries, e := os.ReadDir(stage)
	if e != nil {
		return result, e
	}
	for _, entry := range rootEntries {
		if entry.Name() != "vfs" && entry.Name() != "vfsMeta" && entry.Name() != migrationScratchName {
			return result, migrationProblem("迁移副本中出现未知文件，未继续迁移")
		}
	}
	scratch := filepath.Join(stage, migrationScratchName)
	if err = noReparse(scratch); err != nil {
		return result, err
	}
	if info, e := os.Lstat(scratch); e == nil {
		if !info.Mode().IsRegular() {
			return result, migrationProblem("迁移临时文件异常，未继续")
		}
		if err = os.Remove(scratch); err != nil {
			return result, err
		}
	} else if !os.IsNotExist(e) {
		return result, e
	}
	s.migrationUpdate(func(p *CacheMigrationStatus) {
		p.RetainedDirectory = stage
		p.Phase = "copying"
		p.Message = "正在复制现有缓存"
	})
	for _, directory := range plan.Directories {
		if err = ctx.Err(); err != nil {
			return result, err
		}
		if err = os.MkdirAll(filepath.Join(stage, directory), 0700); err != nil {
			return result, err
		}
	}
	completed := int64(0)
	for i, entry := range plan.Files {
		source, target := filepath.Join(preview.Source, entry.Path), filepath.Join(stage, entry.Path)
		progress := func(n int64) { s.migrationUpdate(func(p *CacheMigrationStatus) { p.Bytes = completed + n }) }
		if _, e := os.Lstat(target); e == nil {
			if err = verifyMigrationFile(ctx, source, target, entry, progress); err != nil {
				return result, err
			}
			s.migrationUpdate(func(p *CacheMigrationStatus) { p.ReusedFiles++; p.ReusedBytes += entry.Size })
		} else if !os.IsNotExist(e) {
			return result, e
		} else {
			if published {
				return result, migrationProblem("已发布的迁移副本不完整，未切换缓存")
			}
			if err = copyMigrationFile(ctx, source, scratch, entry, progress); err != nil {
				return result, err
			}
			if err = verifyMigrationFile(ctx, source, scratch, entry, progress); err != nil {
				return result, err
			}
			if err = ctx.Err(); err != nil {
				return result, err
			}
			if err = migrationPublishFile(scratch, target); err != nil {
				return result, err
			}
		}
		completed += entry.Size
		s.migrationUpdate(func(p *CacheMigrationStatus) { p.Files = i + 1 })
	}
	s.migrationUpdate(func(p *CacheMigrationStatus) {
		p.Phase = "verifying"
		p.Message = "正在逐文件校验缓存内容"
		p.Bytes = 0
		p.Files = 0
	})
	completed = 0
	for i, entry := range plan.Files {
		err = verifyMigrationFile(ctx, filepath.Join(preview.Source, entry.Path), filepath.Join(stage, entry.Path), entry, func(n int64) { s.migrationUpdate(func(p *CacheMigrationStatus) { p.Bytes = completed + n }) })
		if err != nil {
			return result, err
		}
		completed += entry.Size
		s.migrationUpdate(func(p *CacheMigrationStatus) { p.Files = i + 1 })
	}
	fresh, err := planCacheMigration(ctx, preview.Source)
	if err != nil {
		return result, err
	}
	if !sameMigrationPlan(plan, fresh) {
		return result, migrationProblem("迁移期间原缓存发生变化，未切换缓存位置")
	}
	copied, err := planCacheMigration(ctx, stage)
	if err != nil {
		return result, err
	}
	if !sameMigrationPlan(plan, copied) {
		return result, migrationProblem("目标缓存校验未通过，未切换缓存位置")
	}
	if err = ctx.Err(); err != nil {
		return result, err
	}
	// Commit is deliberately not cancellable: the verified destination is complete.
	s.migrationUpdate(func(p *CacheMigrationStatus) {
		p.Phase = "switching"
		p.Message = "校验通过，正在切换缓存位置"
	})
	journal.Ready = true
	if err = s.writeMigrationJournal(journal); err != nil {
		return result, err
	}
	s.migrationCommitPoint("ready")
	if !published {
		leases[len(leases)-1].Close()
		if err = migrationPublishFile(stage, preview.Target); err != nil {
			return result, err
		}
	}
	s.migrationCommitPoint("published")
	s.migrationUpdate(func(p *CacheMigrationStatus) { p.RetainedDirectory = preview.Target })
	data, err := json.Marshal(config)
	if err != nil {
		return result, err
	}
	if err = atomicWriteSettings(settingPath, data); err != nil {
		return result, err
	}
	settingsWritten = true
	s.migrationCommitPoint("settings")
	next, err := newDesktop(s.dir, s.cmd.Path, s.sessionLog())
	if err != nil {
		return result, err
	}
	if !strings.EqualFold(filepath.Clean(next.cacheRoot), filepath.Clean(preview.Target)) {
		next.close()
		return result, migrationProblem("新缓存位置未能启用，正在恢复原设置")
	}
	s.adoptEngine(next)
	s.migrationCommitPoint("engine")
	s.retiredDrives = nil
	success = true
	if e := s.recordCacheHistory(preview.Source, "migrated"); e != nil {
		s.migrationUpdate(func(p *CacheMigrationStatus) {
			p.HistoryWarning = "旧缓存历史未能保存，迁移记录已保留，可继续迁移补齐记录"
		})
	} else {
		s.migrationCommitPoint("history")
		if e := os.Remove(filepath.Join(s.dir, migrationJournalName)); e != nil && !os.IsNotExist(e) {
			s.migrationUpdate(func(p *CacheMigrationStatus) {
				p.HistoryWarning = "迁移已完成，但任务记录未能移除；可放弃任务记录，文件不受影响"
			})
		}
	}
	s.migrationCommitPoint("finished")
	stopped = false
	s.migrationUpdate(func(p *CacheMigrationStatus) { p.RetainedDirectory = "" })
	return result, nil
}

func (s *DesktopService) migrationCommitPoint(phase string) {
	if s.migrationCommitHook != nil {
		s.migrationCommitHook(phase)
	}
}
