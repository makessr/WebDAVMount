package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"webdavmount/internal/providers"
)

const writableJournal = "writable-mount.enc"

var (
	errWritableSession   = errors.New("请先关闭正在编辑的文件，等待上传完成并卸载可写网盘")
	errWritableJournal   = errors.New("无法确认可写挂载记录，已保留写回缓存")
	errWritableLegacy    = errors.New("待上传文件缺少原始 WebDAV 版本。请先导出写回副本，再到客户端用「编辑副本」核对并提交")
	errWritableS3Base    = errors.New("待上传文件的 S3 原始版本记录缺失或无效。请先导出写回副本")
	errWritableEngine    = errors.New("当前后台引擎缺少写回冲突保护，请使用本版本附带的引擎")
	errWritableCachePair = errors.New("写回缓存的数据与索引不完整，已保留原文件，无法安全恢复可写挂载")
	errWritableUploads   = errors.New("可写网盘还有待上传或正在编辑的文件，请关闭文件并等待上传后重试")
)

type writableSession struct {
	ID          string
	Fingerprint string
	Cache       string
	Epoch       string
}

func supportsWritable(p Profile) bool {
	return p.Provider == "" || p.Provider == providers.WebDAV
}

const s3CandidateEngine = "v1.76.0-webdavmount-s3-candidate"

func s3CandidateProfile(p Profile) bool {
	if p.Provider != providers.MinIO {
		return false
	}
	endpoint, err := url.Parse(p.URL)
	if err != nil || endpoint.Scheme != "http" || endpoint.Hostname() != "127.0.0.1" ||
		endpoint.Port() == "" || endpoint.User != nil || endpoint.RawQuery != "" ||
		endpoint.ForceQuery || endpoint.Fragment != "" || endpoint.RawPath != "" {
		return false
	}
	bucket := providers.RemoteRoot(p.Provider, p.URL)
	return bucket != "" && bucket != "." && bucket != ".." && !strings.ContainsAny(bucket, "/\\:")
}

func writableMountProfileSupported(p Profile) bool {
	return supportsWritable(p) || s3CandidateProfile(p)
}

func writableProfile(p Profile, epoch string) Profile {
	digest := sha256.Sum256([]byte(p.ID))
	p.ID = "wdmwrite-" + hex.EncodeToString(digest[:16])
	if epoch != "" {
		p.ID += "-" + epoch
	}
	return p
}

func validWritableEpoch(value string) bool {
	_, err := hex.DecodeString(value)
	return len(value) == 32 && err == nil && value == strings.ToLower(value)
}

func writableRemoteMatches(p Profile, remote string) bool {
	alias, root, ok := strings.Cut(remote, ":")
	if !ok || root != providers.RemoteRoot(p.Provider, p.URL) {
		return false
	}
	base := writableProfile(p, "").ID
	if alias == base {
		return true
	}
	suffix, ok := strings.CutPrefix(alias, base+"-")
	return ok && validWritableEpoch(suffix)
}

func (s *DesktopService) writableEpochPath(p Profile) string {
	return filepath.Join(s.dir, writableProfile(p, "").ID+".epoch")
}

func (s *DesktopService) readWritableEpoch(p Profile) (string, error) {
	path := s.writableEpochPath(p)
	if err := noReparse(path); err != nil {
		return "", errWritableJournal
	}
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() != 32 {
		return "", errWritableJournal
	}
	value, err := os.ReadFile(path)
	if err != nil || !validWritableEpoch(string(value)) {
		return "", errWritableJournal
	}
	return string(value), nil
}

func writableFingerprint(p Profile) string {
	config, _ := json.Marshal([]any{p.Provider, p.URL, p.User, p.Password, p.Drive, p.HostKey, p.Storage, p.Proxy})
	digest := sha256.Sum256(config)
	return hex.EncodeToString(digest[:])
}

func (s *DesktopService) loadWritableSession() {
	path := filepath.Join(s.dir, writableJournal)
	if err := noReparse(path); err != nil {
		s.writable.Store(&writableSession{})
		return
	}
	blob, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	var session writableSession
	if err == nil {
		if plain, e := crypt(blob, true); e == nil {
			if json.Unmarshal(plain, &session) == nil && session.ID != "" && len(session.Fingerprint) == 64 && filepath.IsAbs(session.Cache) && (session.Epoch == "" || validWritableEpoch(session.Epoch)) {
				s.writable.Store(&session)
				return
			}
		}
	}
	s.writable.Store(&writableSession{})
}

func (s *DesktopService) writableGuard() error {
	if session := s.writable.Load(); session != nil {
		if session.ID == "" {
			return errWritableJournal
		}
		return errWritableSession
	}
	return nil
}

func (s *DesktopService) reserveWritable(p Profile, epoch string) error {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	if s.closing {
		return errors.New("程序正在退出")
	}
	if current := s.writable.Load(); current != nil {
		if current.ID != p.ID {
			return s.writableGuard()
		}
		if current.Fingerprint != writableFingerprint(p) || !strings.EqualFold(current.Cache, s.cacheRoot) {
			return errWritableJournal
		}
		return nil
	}
	session := &writableSession{ID: p.ID, Fingerprint: writableFingerprint(p), Cache: s.cacheRoot, Epoch: epoch}
	plain, err := json.Marshal(session)
	if err != nil {
		return errWritableJournal
	}
	blob, err := crypt(plain, false)
	if err != nil || atomicWriteSettings(filepath.Join(s.dir, writableJournal), blob) != nil {
		return errWritableJournal
	}
	s.writable.Store(session)
	return nil
}

func (s *DesktopService) finishWritable() error {
	if err := os.Remove(filepath.Join(s.dir, writableJournal)); err != nil {
		return errWritableJournal
	}
	s.writable.Store(nil)
	return nil
}

// MountWritable is only used by the explicit, confirmed WebDAV action.
func (s *DesktopService) MountWritable(id string) error {
	ctx, finish, err := s.beginOperation("mount-writable")
	if err != nil {
		return err
	}
	defer finish()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mountWritableLocked(ctx, id)
}

func (s *DesktopService) mountWritableLocked(ctx context.Context, id string) error {
	p, err := s.find(id)
	if err != nil {
		return err
	}
	isS3 := s3CandidateProfile(p) && s.s3Preview
	if !supportsWritable(p) && !isS3 {
		return errors.New("可写挂载目前仅支持 WebDAV")
	}
	if !s.engineOnline() {
		if session := s.writable.Load(); session == nil || session.ID != id {
			return errors.New("请先恢复后台引擎，再继续可写挂载")
		}
		if _, err = s.recoverSelectionLocked(ctx, map[string]bool{}); err != nil {
			return err
		}
	}
	version, err := s.callContext(ctx, "core/version", nil)
	if err != nil {
		return err
	}
	if !engineSupportsProtectedWriteback(version) || (isS3 && version["version"] != s3CandidateEngine) {
		return errWritableEngine
	}
	if err = s.checkMountDriveLocked(p); err != nil {
		return err
	}
	session := s.writable.Load()
	fresh := session == nil
	var epoch string
	if fresh {
		epoch, err = s.readWritableEpoch(p)
		if err != nil {
			return err
		}
	} else {
		if session.ID != id {
			return s.writableGuard()
		}
		epoch = session.Epoch
	}
	remote := writableProfile(p, epoch)
	if isS3 {
		remote.Proxy = &ProxySettings{Mode: "direct"}
	}
	var validBase func(string) bool
	if !fresh {
		validBase = strongEditETag
		if isS3 {
			validBase = validS3CacheWriteBase
		}
	}
	dirty, err := scanWritableCache(s.cacheRoot, remote.ID, true, validBase)
	if errors.Is(err, errWritableLegacy) {
		if isS3 {
			return errWritableS3Base
		}
		return err
	}
	if err != nil {
		return errWritableCachePair
	}
	if fresh && dirty {
		return errWritableJournal
	}
	if err = s.reserveWritable(p, epoch); err != nil {
		return err
	}
	delete(s.desired, p.ID)
	s.sessionRecovery.update(p, false)
	if err = s.configureProfile(ctx, remote, ""); err == nil {
		err = s.mountProfileLocked(ctx, remote, false)
	}
	if err != nil && fresh {
		result, checkErr := s.call("mount/listmounts", nil)
		if checkErr == nil {
			present, _, stateErr := connectionMountState(result, p)
			dirty, cacheErr := scanWritableCache(s.cacheRoot, remote.ID, true, nil)
			if stateErr == nil && !present && cacheErr == nil && !dirty {
				_ = s.finishWritable()
			}
		}
	}
	return err
}

// RemountWritableForRename resets the mount-level protection after the user
// reviews the remote directory. The regular unmount path checks pending edits.
func (s *DesktopService) RemountWritableForRename(id string) error {
	ctx, finish, err := s.beginOperation("writable.rename.remount")
	if err != nil {
		return err
	}
	defer finish()
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.find(id)
	if err != nil {
		return err
	}
	state, err := s.callContext(ctx, "mount/listmounts", nil)
	if err != nil {
		return err
	}
	if !isWritableMount(state, p) {
		return errors.New("该连接没有处于可写挂载状态")
	}
	status := s.writableStats(ctx, p)
	if !status.Known || (status.RenameBlocked == 0 && status.Delete.Conflict == 0 && status.Delete.Unconfirmed == 0) {
		return errors.New("未发现需要重新挂载的文件操作")
	}
	if err := s.unmountLocked(id); err != nil {
		return err
	}
	if err := s.mountWritableLocked(ctx, id); err != nil {
		return fmt.Errorf("原盘符已卸载，请在客户端重新选择可写挂载：%w", err)
	}
	return nil
}

type writableDeleteStatus struct {
	Confirmed   int64 `json:"confirmed"`
	Blocked     int64 `json:"blocked"`
	Conflict    int64 `json:"conflict"`
	Unconfirmed int64 `json:"unconfirmed"`
}

type writableStatus struct {
	S3ReviewCount   int64                `json:"s3ReviewCount"`
	S3DeleteReview  bool                 `json:"s3DeleteReview"`
	DirectoryDelete bool                 `json:"directoryDelete"`
	DirectoryReview bool                 `json:"directoryReview"`
	Delete          writableDeleteStatus `json:"delete"`
	Known           bool                 `json:"known"`
	Queued          int64                `json:"queued"`
	Uploading       int64                `json:"uploading"`
	Errors          int64                `json:"errors"`
	Conflicts       int64                `json:"conflicts"`
	OutOfSpace      bool                 `json:"outOfSpace"`
	RenameBlocked   int64                `json:"renameBlocked"`
}

func (s *DesktopService) writableStats(ctx context.Context, p Profile) writableStatus {
	session := s.writable.Load()
	if session == nil || session.ID != p.ID {
		return writableStatus{}
	}
	result, err := s.callProfileContext(ctx, writableProfile(p, session.Epoch), "vfs/stats", map[string]any{})
	if err != nil {
		return writableStatus{}
	}
	disk, ok := result["diskCache"].(map[string]any)
	if !ok {
		return writableStatus{}
	}
	queued, q := disk["uploadsQueued"].(float64)
	uploading, u := disk["uploadsInProgress"].(float64)
	failed, f := disk["erroredFiles"].(float64)
	full, space := disk["outOfSpace"].(bool)
	conflicts, _ := disk["writeConflicts"].(float64)
	rename, _ := result["protectedRenameBlocked"].(float64)
	deletion, _ := result["protectedDeleteResults"].(map[string]any)
	confirmed, _ := deletion["confirmed"].(float64)
	blocked, _ := deletion["blocked"].(float64)
	conflict, _ := deletion["conflict"].(float64)
	unconfirmed, _ := deletion["unconfirmed"].(float64)
	directoryReview, _ := deletion["directoryReview"].(bool)
	directoryDelete, _ := deletion["directoryDelete"].(bool)
	s3ReviewCount, _ := disk["s3ReviewCount"].(float64)
	s3DeleteReview, _ := result["s3DeleteReview"].(bool)
	return writableStatus{S3ReviewCount: int64(s3ReviewCount), S3DeleteReview: s3DeleteReview, DirectoryDelete: directoryDelete, DirectoryReview: directoryReview, Delete: writableDeleteStatus{Confirmed: int64(confirmed), Blocked: int64(blocked), Conflict: int64(conflict), Unconfirmed: int64(unconfirmed)}, RenameBlocked: int64(rename), Known: q && u && f && space, Queued: int64(queued), Uploading: int64(uploading), Errors: int64(failed), Conflicts: int64(conflicts), OutOfSpace: full}
}

func (s *DesktopService) checkWritableUnmount(p Profile) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	status := s.writableStats(ctx, p)
	if !status.Known {
		return errWritableJournal
	}
	if status.Queued > 0 || status.Uploading > 0 || status.Errors > 0 {
		return errWritableUploads
	}
	return s.checkWritableCache(p)
}

func (s *DesktopService) checkWritableCache(p Profile) error {
	session := s.writable.Load()
	if session == nil || session.ID != p.ID || session.Fingerprint != writableFingerprint(p) || !strings.EqualFold(session.Cache, s.cacheRoot) {
		return errWritableJournal
	}
	if err := checkS3DeleteBeforeEnding(p, session); err != nil {
		return err
	}
	dirty, err := scanWritableCache(session.Cache, writableProfile(p, session.Epoch).ID, false, nil)
	if err != nil {
		return errWritableJournal
	}
	if dirty {
		return errWritableUploads
	}
	return nil
}

func validS3CacheWriteBase(base string) bool {
	// Versioned records append an opaque S3 version ID after a newline.
	// Older and unversioned records contain only the quoted ETag.
	tag, version, versioned := strings.Cut(base, "\n")
	if len(tag) < 3 || !strongEditETag(tag) || strings.IndexFunc(tag, func(r rune) bool { return r > 0x7e }) >= 0 {
		return false
	}
	return !versioned || (version != "" && strings.TrimSpace(version) == version && strings.IndexFunc(version, unicode.IsControl) < 0)
}

func scanWritableCache(root, remote string, validatePairs bool, validBase func(string) bool) (bool, error) {
	data := filepath.Join(root, "vfs", remote)
	meta := filepath.Join(root, "vfsMeta", remote)
	for _, name := range []string{data, meta} {
		if err := noReparse(name); err != nil {
			return false, err
		}
	}
	if info, err := os.Stat(meta); errors.Is(err, os.ErrNotExist) {
		if _, dataErr := os.Stat(data); !errors.Is(dataErr, os.ErrNotExist) {
			return false, errWritableJournal
		}
		return false, nil
	} else if err != nil {
		return false, err
	} else if !info.IsDir() {
		return false, errWritableJournal
	}
	dirty := false
	err := filepath.WalkDir(meta, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err = noReparse(path); err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return errWritableJournal
		}
		info, _, err := readWritableCacheMetadata(path)
		if err != nil {
			return err
		}
		if *info.Dirty {
			if validBase != nil && !info.WriteNew && !validBase(info.WriteBase) {
				return errWritableLegacy
			}
			dirty = true
			if !validatePairs {
				return fs.SkipAll
			}
			relative, err := filepath.Rel(meta, path)
			if err != nil {
				return err
			}
			return writableCacheRegularFile(filepath.Join(data, relative))
		}
		return nil
	})
	if err != nil || !validatePairs {
		return dirty, err
	}
	if info, err := os.Stat(data); errors.Is(err, os.ErrNotExist) {
		return dirty, nil
	} else if err != nil {
		return dirty, err
	} else if !info.IsDir() {
		return dirty, errWritableJournal
	}
	// The engine removes data whose metadata is missing or cannot be read.
	// Verify both trees before allowing a fresh engine to open this cache.
	err = filepath.WalkDir(data, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err = noReparse(path); err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return errWritableJournal
		}
		relative, err := filepath.Rel(data, path)
		if err != nil {
			return err
		}
		return writableCacheRegularFile(filepath.Join(meta, relative))
	})
	return dirty, err
}

func writableCacheRegularFile(path string) error {
	if err := noReparse(path); err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errWritableJournal
	}
	return nil
}

func (s *DesktopService) finishWritableUnmount(p Profile) error {
	if err := s.checkWritableCache(p); err != nil {
		return fmt.Errorf("磁盘已卸载，写回缓存已保留。请使用「恢复可写挂载」后重试。%w", err)
	}
	return s.finishWritable()
}

type writableCacheMetadata struct {
	WriteBase      string
	WriteNew       bool
	ModTime, ATime time.Time
	Size           *int64
	Rs             []struct{ Pos, Size int64 }
	Fingerprint    string
	Dirty          *bool
}

func readWritableCacheMetadata(filename string) (writableCacheMetadata, [32]byte, error) {
	var info writableCacheMetadata
	if err := noReparse(filename); err != nil {
		return info, [32]byte{}, err
	}
	file, err := os.Open(filename)
	if err != nil {
		return info, [32]byte{}, err
	}
	defer file.Close()
	const limit = 4 << 20
	raw, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || len(raw) > limit || json.Unmarshal(raw, &info) != nil || info.Dirty == nil {
		return info, [32]byte{}, errWritableJournal
	}
	return info, sha256.Sum256(raw), nil
}

func (info writableCacheMetadata) complete() bool {
	if info.Size == nil || *info.Size < 0 {
		return false
	}
	var covered int64
	for _, chunk := range info.Rs {
		if chunk.Pos < 0 || chunk.Pos > covered || chunk.Pos > *info.Size || chunk.Size <= 0 || chunk.Size > *info.Size-chunk.Pos {
			return false
		}
		covered = max(covered, chunk.Pos+chunk.Size)
	}
	return covered == *info.Size
}

type writableDirectoryReview struct {
	State    string `json:"state"`
	Source   string `json:"source"`
	Target   string `json:"target"`
	RecordID string `json:"recordID"`
}

func (s *DesktopService) ReviewWritableDirectory(id string) (writableDirectoryReview, error) {
	return s.writableDirectoryOperation(id, "")
}

func (s *DesktopService) AcknowledgeWritableDirectory(id, recordID string) error {
	if !validWritableEpoch(recordID) {
		return errors.New("核对记录无效，请重新核对")
	}
	result, err := s.writableDirectoryOperation(id, recordID)
	if err != nil {
		return err
	}
	if result.State != "acknowledged" {
		return errors.New("目录状态已变化，请重新核对")
	}
	return nil
}

func (s *DesktopService) writableCollectionCall(id, method string, args map[string]any) (map[string]any, error) {
	ctx, finish, err := s.beginOperation("writable-directory")
	if err != nil {
		return nil, err
	}
	defer finish()
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.find(id)
	if err != nil {
		return nil, err
	}
	session := s.writable.Load()
	if session == nil || session.ID != id {
		return nil, errors.New("请先恢复此网盘的可写挂载")
	}
	return s.callProfileContext(ctx, writableProfile(p, session.Epoch), method, args)
}

func (s *DesktopService) writableDirectoryOperation(id, recordID string) (writableDirectoryReview, error) {
	args := map[string]any{}
	if recordID != "" {
		args["recordID"] = recordID
	}
	result, err := s.writableCollectionCall(id, "vfs/wdm-collection-review", args)
	if err != nil {
		return writableDirectoryReview{}, errors.New("目录状态暂时无法核对，请重试")
	}
	state, _ := result["state"].(string)
	source, _ := result["source"].(string)
	target, _ := result["target"].(string)
	key, _ := result["recordID"].(string)
	if !validWritableEpoch(key) {
		key = ""
	}
	return writableDirectoryReview{State: state, Source: source, Target: target, RecordID: key}, nil
}

type writableFolderDeletion struct {
	Path    string `json:"path"`
	Token   string `json:"token"`
	Files   int64  `json:"files"`
	Folders int64  `json:"folders"`
}

func (s *DesktopService) PreviewWritableFolderDelete(id, path string) (writableFolderDeletion, error) {
	result, err := s.writableCollectionCall(id, "vfs/wdm-collection-delete", map[string]any{"path": path})
	if err != nil {
		return writableFolderDeletion{}, errors.New("暂时无法核对要删除的文件夹")
	}
	if result["state"] != "ready" {
		return writableFolderDeletion{}, folderDeleteError(result["state"])
	}
	folder, _ := result["path"].(string)
	token, _ := result["token"].(string)
	files, filesOK := result["files"].(float64)
	folders, foldersOK := result["folders"].(float64)
	if folder != path || !validWritableEpoch(token) || !filesOK || !foldersOK ||
		files < 0 || folders < 0 || files+folders > 4096 || float64(int64(files)) != files || float64(int64(folders)) != folders {
		return writableFolderDeletion{}, errors.New("文件夹删除范围无法验证")
	}
	return writableFolderDeletion{Path: folder, Token: token, Files: int64(files), Folders: int64(folders)}, nil
}

func (s *DesktopService) ConfirmWritableFolderDelete(id, path, token string) error {
	if !validWritableEpoch(token) {
		return folderDeleteError("stale")
	}
	result, err := s.writableCollectionCall(id, "vfs/wdm-collection-delete", map[string]any{"path": path, "token": token})
	if err != nil {
		return errors.New("暂时无法确认删除结果，请刷新目录并检查是否有待核对操作")
	}
	if result["state"] != "deleted" {
		return folderDeleteError(result["state"])
	}
	return nil
}

func folderDeleteError(state any) error {
	status, _ := state.(string)
	switch status {
	case "review":
		return errors.New("请先核对待确认的目录操作")
	case "stale":
		return errors.New("文件夹内容已变化或确认已过期，请重新核对删除范围")
	case "blocked":
		return errors.New("文件夹暂不能删除，请检查正在使用的文件和待写回缓存")
	case "busy":
		return errors.New("挂载盘正在处理其他操作，请稍后重试")
	default:
		return errors.New("暂时无法确认文件夹状态")
	}
}

func (s *DesktopService) RenameWritableFolderCase(id, path, newName string) error {
	result, err := s.writableCollectionCall(id, "vfs/wdm-collection-case-rename", map[string]any{"path": path, "newName": newName})
	if err != nil {
		return errors.New("暂时无法确认文件夹改名结果，请刷新目录")
	}
	switch result["state"] {
	case "renamed":
		return nil
	case "occupied":
		return errors.New("目标名称已存在，或服务端无法区分两种大小写")
	case "busy":
		return folderDeleteError("busy")
	default:
		return errors.New("暂时无法改名，请检查待写回缓存或待核对的目录操作")
	}
}

type writableS3Review struct {
	State    string   `json:"state"`
	Path     string   `json:"path"`
	RecordID string   `json:"recordID"`
	Paths    []string `json:"paths"`
	Total    int      `json:"total"`
}

func (s *DesktopService) ReviewWritableS3(id, name string) (writableS3Review, error) {
	return s.writableS3Review(id, name, "")
}

func (s *DesktopService) ReviewWritableS3Delete(id, recordID string) (writableS3Review, error) {
	args := map[string]any{}
	if recordID != "" {
		if _, err := hex.DecodeString(recordID); len(recordID) != 64 || err != nil {
			return writableS3Review{}, errors.New("核对记录已失效，请重新核对")
		}
		args["recordID"] = recordID
	}
	result, err := s.writableCollectionCall(id, "vfs/wdm-s3-delete-review", args)
	if err != nil {
		return writableS3Review{}, errors.New("暂时无法核对 S3 删除，请重试")
	}
	var view writableS3Review
	view.State, _ = result["state"].(string)
	view.Path, _ = result["path"].(string)
	view.RecordID, _ = result["recordID"].(string)
	return view, nil
}

func (s *DesktopService) AcknowledgeWritableS3(id, name, recordID string) error {
	if len(recordID) != 37 || !strings.HasPrefix(recordID, "wdm1-") || !validWritableEpoch(recordID[5:]) || name == "" {
		return errors.New("核对记录已失效，请重新核对")
	}
	result, err := s.writableS3Review(id, name, recordID)
	if err != nil {
		return err
	}
	if result.State != "acknowledged" {
		return errors.New("本地副本或远端状态已变化，请重新核对")
	}
	return nil
}

func (s *DesktopService) writableS3Review(id, name, recordID string) (writableS3Review, error) {
	args := map[string]any{}
	if name != "" {
		args["path"] = name
	}
	if recordID != "" {
		args["recordID"] = recordID
	}
	result, err := s.writableCollectionCall(id, "vfs/wdm-s3-review", args)
	if err != nil {
		return writableS3Review{}, errors.New("暂时无法核对 S3 写回，请重试")
	}
	var view writableS3Review
	view.State, _ = result["state"].(string)
	view.Path, _ = result["path"].(string)
	view.RecordID, _ = result["recordID"].(string)
	count, _ := result["total"].(float64)
	if count >= 0 && count <= 1<<31 {
		view.Total = int(count)
	}
	if items, ok := result["paths"].([]any); ok && len(items) <= 100 {
		for _, item := range items {
			if path, ok := item.(string); ok && path != "" {
				view.Paths = append(view.Paths, path)
			}
		}
	}
	return view, nil
}
