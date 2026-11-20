package desktop

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows"
)

// The single journal owns one staging directory. Completed files are published
// individually after Sync and SHA256 verification; a crash can leave only the
// one known scratch file incomplete. No source files are ever removed.
const migrationJournalName = "cache-migration.json"
const migrationScratchName = ".WebDAVMount-copy.partial"

type migrationIdentity struct{ Volume, High, Low uint32 }
type migrationJournal struct {
	Version               int
	Created               time.Time
	Source, Target, Stage string
	Config                CacheSettings
	SourceID, StageID     migrationIdentity
	Plan                  migrationPlan
	Ready                 bool
}

type PendingCacheMigration struct {
	Available bool      `json:"available"`
	Source    string    `json:"source"`
	Target    string    `json:"target"`
	Stage     string    `json:"stage"`
	Created   time.Time `json:"created"`
	Files     int       `json:"files"`
	Bytes     int64     `json:"bytes"`
	Warning   string    `json:"warning"`
}

func migrationFileIdentity(f *os.File) (migrationIdentity, error) {
	var info windows.ByHandleFileInformation
	err := windows.GetFileInformationByHandle(windows.Handle(f.Fd()), &info)
	return migrationIdentity{info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow}, err
}

func migrationRelative(path string) bool {
	if path == "" || filepath.IsAbs(path) || filepath.Clean(path) != path || strings.Contains(path, ":") {
		return false
	}
	return path == "vfs" || path == "vfsMeta" || strings.HasPrefix(path, `vfs\`) || strings.HasPrefix(path, `vfsMeta\`)
}

func (s *DesktopService) readMigrationJournal() (*migrationJournal, error) {
	path := filepath.Join(s.dir, migrationJournalName)
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		return nil, nil
	}
	f, _, err := migrationOpen(path, windows.GENERIC_READ, windows.OPEN_EXISTING, false)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, (32<<20)+1))
	if err != nil || len(data) > 32<<20 {
		return nil, errors.New("迁移任务无法读取或超出大小限制")
	}
	var j migrationJournal
	if json.Unmarshal(data, &j) != nil || j.Version != 1 || len(j.Plan.Files) > 200000 || len(j.Plan.Directories) > 200000 {
		return nil, errors.New("迁移任务记录损坏")
	}
	for _, p := range []string{j.Source, j.Target, j.Stage} {
		if !filepath.IsAbs(p) || strings.HasPrefix(p, `\\`) || filepath.Clean(p) != p {
			return nil, errors.New("迁移任务路径异常")
		}
	}
	if filepath.Dir(j.Stage) != filepath.Dir(j.Target) || !historyStage.MatchString(filepath.Base(j.Stage)) || migrationWithin(j.Source, j.Stage) || migrationWithin(j.Stage, j.Source) || migrationWithin(j.Source, j.Target) || migrationWithin(j.Target, j.Source) {
		return nil, errors.New("迁移任务目录关系异常")
	}
	seen := map[string]bool{}
	for _, e := range j.Plan.Files {
		key := strings.ToLower(e.Path)
		if !migrationRelative(e.Path) || e.Size < 0 || seen[key] {
			return nil, errors.New("迁移文件清单异常")
		}
		seen[key] = true
	}
	for _, p := range j.Plan.Directories {
		if !migrationRelative(p) {
			return nil, errors.New("迁移目录清单异常")
		}
	}
	return &j, nil
}

func (s *DesktopService) writeMigrationJournal(j *migrationJournal) error {
	data, err := json.Marshal(j)
	if err != nil || len(data) > 32<<20 {
		return errors.New("迁移任务过大，尚未开始复制")
	}
	if err = noReparse(filepath.Join(s.dir, migrationJournalName)); err != nil {
		return err
	}
	return atomicWriteSettings(filepath.Join(s.dir, migrationJournalName), data)
}

// The settings and cache may be on separate volumes. If the settings were
// persisted but the pending publication is missing or changed, do not create
// a new cache with the same name or start against the replacement directory.
func (s *DesktopService) recoverCacheSettingsForStartup(config CacheSettings) (CacheSettings, string, error) {
	j, err := s.readMigrationJournal()
	if err != nil || j == nil || !strings.EqualFold(config.BaseDir, j.Config.BaseDir) {
		return config, "", nil
	}
	if root, err := s.cachePath(config); err == nil && strings.EqualFold(root, j.Target) && j.Ready {
		if _, target, err := migrationCanonicalDirectory(root); err == nil {
			id, err := migrationFileIdentity(target)
			target.Close()
			if err == nil && id == j.StageID {
				return config, "", nil
			}
		}
	}

	failure := migrationProblem("无法确认迁移目标或原缓存，请连接原磁盘并保留迁移记录后重启")
	config.BaseDir = ""
	defaultRoot, err := s.cachePath(config)
	if err != nil {
		return config, "", failure
	}
	if !strings.EqualFold(defaultRoot, j.Source) {
		config.BaseDir = filepath.Dir(j.Source)
	}
	root, err := s.cachePath(config)
	if err != nil || !strings.EqualFold(root, j.Source) {
		return config, "", failure
	}
	_, source, err := migrationCanonicalDirectory(root)
	if err != nil {
		return config, "", failure
	}
	defer source.Close()
	id, err := migrationFileIdentity(source)
	if err != nil || id != j.SourceID {
		return config, "", failure
	}
	root, err = s.prepareCache(config)
	return config, root, err
}

func (s *DesktopService) requireNoMigration() error {
	if _, err := os.Lstat(filepath.Join(s.dir, migrationJournalName)); !os.IsNotExist(err) {
		return migrationProblem("存在未完成的缓存迁移，请先继续迁移或放弃任务（保留副本）")
	}
	return nil
}

func (s *DesktopService) PendingCacheMigration() PendingCacheMigration {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, err := s.readMigrationJournal()
	if err != nil {
		return PendingCacheMigration{Available: true, Warning: "迁移记录无法读取。请保留副本，放弃任务记录后重新迁移。"}
	}
	if j == nil {
		return PendingCacheMigration{}
	}
	stage := j.Stage
	if _, err := os.Lstat(stage); os.IsNotExist(err) && j.Ready {
		stage = j.Target
	}
	return PendingCacheMigration{Available: true, Source: j.Source, Target: j.Target, Stage: stage, Created: j.Created, Files: len(j.Plan.Files), Bytes: j.Plan.Logical}
}

// Only forgets our fixed journal; even a broken or unavailable destination is
// never scanned or deleted by this action.
func (s *DesktopService) DiscardCacheMigration() error {
	_, finish, err := s.beginOperation("cache.migrate.discard")
	if err != nil {
		return err
	}
	defer finish()
	s.mu.Lock()
	defer s.mu.Unlock()
	path := filepath.Join(s.dir, migrationJournalName)
	if err = noReparse(path); err != nil {
		return err
	}
	if s.cacheMigrationFallback.Load() {
		if err = s.writeCacheSettings(s.cacheSettings); err != nil {
			return migrationProblem("无法保留原缓存设置，迁移记录仍保留")
		}
	}
	if err = os.Remove(path); err != nil && !os.IsNotExist(err) {
		return errors.New("无法移除迁移记录，副本已保留")
	}
	if s.cacheMigrationFallback.Swap(false) {
		s.cacheWarning = ""
	}
	s.migrationUpdate(func(p *CacheMigrationStatus) {
		*p = CacheMigrationStatus{Phase: "discarded", Message: "迁移任务已放弃，原缓存和迁移副本均保留"}
	})
	return nil
}

func (s *DesktopService) ResumeCacheMigration() (CacheMigrationStatus, error) {
	return s.migrateCache(CacheSettings{}, true)
}

func (s *DesktopService) resumeMigrationPaths(j *migrationJournal) (CacheMigrationPreview, []*os.File, string, bool, error) {
	preview := CacheMigrationPreview{j.Source, j.Target}
	var leases []*os.File
	fail := func(err error) (CacheMigrationPreview, []*os.File, string, bool, error) {
		for _, f := range leases {
			f.Close()
		}
		return preview, nil, "", false, err
	}
	if err := s.cacheIdle(); err != nil {
		return fail(err)
	}
	if err := checkCacheLocationReviews(j.Source); err != nil {
		return fail(err)
	}
	target, err := s.cachePath(j.Config)
	if err != nil || !strings.EqualFold(target, j.Target) {
		return fail(migrationProblem("目标位置已变化，未继续迁移"))
	}
	if !strings.EqualFold(s.cacheRoot, j.Source) && !strings.EqualFold(s.cacheRoot, j.Target) {
		return fail(migrationProblem("当前缓存位置与迁移任务不一致，未继续迁移"))
	}
	for _, path := range []string{j.Source, filepath.Dir(j.Target)} {
		_, f, e := migrationCanonicalDirectory(path)
		if e != nil {
			return fail(migrationProblem("迁移磁盘不可访问或包含链接，请连接原磁盘后重试"))
		}
		leases = append(leases, f)
	}
	id, err := migrationFileIdentity(leases[0])
	if err != nil || id != j.SourceID {
		return fail(migrationProblem("原缓存目录身份已变化，未继续迁移"))
	}
	stage, published := j.Stage, false
	if _, err = os.Lstat(stage); os.IsNotExist(err) && j.Ready {
		stage, published = j.Target, true
	}
	_, f, err := migrationCanonicalDirectory(stage)
	if err != nil {
		return fail(migrationProblem("迁移副本不可访问，请连接原磁盘或放弃任务"))
	}
	leases = append(leases, f)
	id, err = migrationFileIdentity(f)
	if err != nil || id != j.StageID {
		return fail(migrationProblem("迁移副本目录身份已变化，未继续迁移"))
	}
	return preview, leases, stage, published, nil
}

func migrationPublishFile(source, target string) error {
	if err := noReparse(source); err != nil {
		return err
	}
	if err := noReparse(target); err != nil {
		return err
	}
	_, lease, err := migrationCanonicalDirectory(filepath.Dir(target))
	if err != nil {
		return err
	}
	defer lease.Close()
	a, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	b, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(a, b, windows.MOVEFILE_WRITE_THROUGH)
}
