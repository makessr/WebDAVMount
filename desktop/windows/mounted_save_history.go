package desktop

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"time"
)

const mountedSaveHistoryFile = "mounted-save-history.enc"
const mountedSaveHistoryLimit = 15
const mountedSaveHistoryMaxBytes = 16 << 20

type mountedSaveHistoryStore struct {
	loaded, unreadable, unsaved bool
	records                     []mountedSaveJournalRecord
}

type mountedSaveJournalRecord struct {
	ID          string                  `json:"id"`
	SourceID    string                  `json:"sourceId"`
	SourceName  string                  `json:"sourceName"`
	SourceKey   string                  `json:"sourceKey"`
	Folder      string                  `json:"folder"`
	FolderID    *migrationIdentity      `json:"folderId,omitempty"`
	Paths       []string                `json:"paths"`
	Items       []MountedFileSaveResult `json:"items"`
	CompletedAt time.Time               `json:"completedAt"`
	Retried     bool                    `json:"retried"`
}

type MountedSaveHistoryEntry struct {
	ID            string                  `json:"id"`
	SourceID      string                  `json:"sourceId"`
	SourceName    string                  `json:"sourceName"`
	Folder        string                  `json:"folder"`
	Items         []MountedFileSaveResult `json:"items"`
	CompletedAt   time.Time               `json:"completedAt"`
	RetryCount    int                     `json:"retryCount"`
	SourceMatches bool                    `json:"sourceMatches"`
	Retried       bool                    `json:"retried"`
}

type MountedSaveHistoryReport struct {
	Entries []MountedSaveHistoryEntry `json:"entries"`
	Warning string                    `json:"warning,omitempty"`
}

func mountedSaveSourceKey(p Profile) string {
	p = p.public()
	data, _ := json.Marshal([]any{p.presetIdentity(), p.ID, p.Provider, p.URL, p.User, p.Drive, p.HostKey, p.Storage})
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func (record mountedSaveJournalRecord) retryPaths() []string {
	if record.Retried {
		return nil
	}
	var paths []string
	for index, item := range record.Items {
		switch item.State {
		case "failed", "cancelled", "unprocessed":
			paths = append(paths, record.Paths[index])
		}
	}
	return paths
}

func (s *DesktopService) MountedSaveHistory() MountedSaveHistoryReport {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loadMountedSaveHistoryLocked()
	store := &s.saveHistory
	report := MountedSaveHistoryReport{Entries: make([]MountedSaveHistoryEntry, 0, len(store.records))}
	for _, record := range store.records {
		p, err := s.find(record.SourceID)
		report.Entries = append(report.Entries, MountedSaveHistoryEntry{
			ID: record.ID, SourceID: record.SourceID, SourceName: record.SourceName, Folder: record.Folder,
			Items: append([]MountedFileSaveResult(nil), record.Items...), CompletedAt: record.CompletedAt,
			RetryCount: len(record.retryPaths()), SourceMatches: err == nil && mountedSaveSourceKey(p) == record.SourceKey, Retried: record.Retried,
		})
	}
	if store.unreadable {
		report.Warning = "无法读取原另存记录。新记录暂存在本次运行中；可清除记录后重新保存。"
	} else if store.unsaved {
		report.Warning = "另存已处理，最近的记录暂时无法写入本机。"
	}
	return report
}

func (s *DesktopService) ClearMountedSaveHistory() error {
	_, finish, err := s.beginOperation("mount.file.save")
	if err != nil {
		return err
	}
	defer finish()
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.writeMountedSaveHistoryLocked([]mountedSaveJournalRecord{}); err != nil {
		return errors.New("无法清除另存记录，请检查本机配置目录")
	}
	s.saveHistory = mountedSaveHistoryStore{loaded: true}
	return nil
}

func (s *DesktopService) ForgetMountedSaveRecord(id string) error {
	_, finish, err := s.beginOperation("mount.file.save")
	if err != nil {
		return err
	}
	defer finish()
	s.mu.Lock()
	defer s.mu.Unlock()
	index, err := s.mountedSaveRecordIndexLocked(id)
	if err != nil {
		return err
	}
	store := &s.saveHistory
	remaining := append(make([]mountedSaveJournalRecord, 0, len(store.records)-1), store.records[:index]...)
	remaining = append(remaining, store.records[index+1:]...)
	if !store.unreadable {
		if err := s.writeMountedSaveHistoryLocked(remaining); err != nil {
			return errors.New("无法移除这批另存记录，请检查本机配置目录")
		}
		store.unsaved = false
	}
	store.records = remaining
	return nil
}

func (s *DesktopService) OpenMountedSaveFolder(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	index, err := s.mountedSaveRecordIndexLocked(id)
	if err != nil {
		return err
	}
	record := s.saveHistory.records[index]
	dir, err := mountedSaveFolder(record.Folder)
	if err != nil {
		return errors.New("保存位置不可用，请检查本地磁盘或文件夹")
	}
	defer dir.Close()
	if record.FolderID != nil {
		id, err := migrationFileIdentity(dir)
		if err != nil || id != *record.FolderID {
			return errors.New("保存文件夹已更换，请核对记录中的路径")
		}
	}
	if s.openDirectory == nil || s.openDirectory(record.Folder) != nil {
		return errors.New("无法在资源管理器中打开保存位置")
	}
	return nil
}

func (s *DesktopService) mountedSaveRecordIndexLocked(id string) (int, error) {
	s.loadMountedSaveHistoryLocked()
	for index, record := range s.saveHistory.records {
		if record.ID == id {
			return index, nil
		}
	}
	return -1, errors.New("这批另存记录已不存在，请重新打开另存记录")
}

func (s *DesktopService) retrySaveRecordLocked(id string) (mountedSaveJournalRecord, error) {
	s.loadMountedSaveHistoryLocked()
	for _, record := range s.saveHistory.records {
		if record.ID == id && len(record.retryPaths()) > 0 {
			p, err := s.find(record.SourceID)
			if err != nil || mountedSaveSourceKey(p) != record.SourceKey {
				return mountedSaveJournalRecord{}, errors.New("原网盘连接已变化，请重新选择文件")
			}
			return record, nil
		}
	}
	return mountedSaveJournalRecord{}, errors.New("没有可重试的文件，请重新打开另存记录")
}

func (s *DesktopService) recordMountedSaveLocked(p Profile, paths []string, result *MountedFileBatchResult, retryID string, folderID *migrationIdentity) {
	s.loadMountedSaveHistoryLocked()
	store := &s.saveHistory
	for i := range store.records {
		if store.records[i].ID == retryID {
			store.records[i].Retried = true
		}
	}
	result.RecordID = randomID()
	record := mountedSaveJournalRecord{
		ID: result.RecordID, SourceID: p.ID, SourceName: p.Name, SourceKey: mountedSaveSourceKey(p), Folder: result.Folder, FolderID: folderID,
		Paths: append([]string(nil), paths...), Items: append([]MountedFileSaveResult(nil), result.Items...), CompletedAt: time.Now().UTC(),
	}
	store.records = append([]mountedSaveJournalRecord{record}, store.records...)
	if len(store.records) > mountedSaveHistoryLimit {
		store.records = store.records[:mountedSaveHistoryLimit]
	}
	if !store.unreadable {
		store.unsaved = s.writeMountedSaveHistoryLocked(store.records) != nil
	}
}

func (s *DesktopService) loadMountedSaveHistoryLocked() {
	store := &s.saveHistory
	if store.loaded {
		return
	}
	store.loaded = true
	store.records, store.unreadable = s.readMountedSaveHistoryLocked()
}

func (s *DesktopService) readMountedSaveHistoryLocked() ([]mountedSaveJournalRecord, bool) {
	filename := filepath.Join(s.dir, mountedSaveHistoryFile)
	if noReparse(filename) != nil {
		return nil, true
	}
	f, err := os.Open(filename)
	if os.IsNotExist(err) {
		return nil, false
	}
	if err != nil {
		return nil, true
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > mountedSaveHistoryMaxBytes {
		return nil, true
	}
	data, err := io.ReadAll(io.LimitReader(f, mountedSaveHistoryMaxBytes+1))
	if err != nil || len(data) > mountedSaveHistoryMaxBytes {
		return nil, true
	}
	plain, err := crypt(data, true)
	if err != nil {
		return nil, true
	}
	defer clear(plain)
	var records []mountedSaveJournalRecord
	if json.Unmarshal(plain, &records) != nil || !validMountedSaveHistory(records) {
		return nil, true
	}
	return records, false
}

func validMountedSaveHistory(records []mountedSaveJournalRecord) bool {
	if records == nil || len(records) > mountedSaveHistoryLimit {
		return false
	}
	seen := map[string]bool{}
	for _, record := range records {
		if !historyID.MatchString(record.ID) || seen[record.ID] || record.SourceID == "" ||
			len(record.SourceKey) != sha256.Size*2 || record.CompletedAt.IsZero() || !filepath.IsAbs(record.Folder) ||
			len(record.Paths) != len(record.Items) || validateMountedSavePaths(record.Paths) != nil {
			return false
		}
		seen[record.ID] = true
		for i, item := range record.Items {
			if item.Name != path.Base(record.Paths[i]) {
				return false
			}
			switch item.State {
			case "saved", "skipped", "failed", "cancelled", "unprocessed":
			default:
				return false
			}
		}
	}
	return true
}

func (s *DesktopService) writeMountedSaveHistoryLocked(records []mountedSaveJournalRecord) error {
	if !validMountedSaveHistory(records) {
		return errors.New("另存记录格式无效")
	}
	plain, err := json.Marshal(records)
	if err != nil {
		return err
	}
	defer clear(plain)
	data, err := crypt(plain, false)
	if err != nil {
		return err
	}
	if len(data) > mountedSaveHistoryMaxBytes {
		return errors.New("另存记录超出大小限制")
	}
	return atomicWriteSettings(filepath.Join(s.dir, mountedSaveHistoryFile), data)
}
