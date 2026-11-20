package main

import (
	"errors"
	"path"
	"path/filepath"
)

const mountedSaveBatchLimit = 100

type MountedFileSaveResult struct {
	Name    string `json:"name"`
	State   string `json:"state"`
	Message string `json:"message,omitempty"`
}

type MountedFileBatchResult struct {
	Folder   string                  `json:"folder"`
	Items    []MountedFileSaveResult `json:"items"`
	RecordID string                  `json:"recordId"`
}

func validateMountedSavePaths(paths []string) error {
	if len(paths) == 0 || len(paths) > mountedSaveBatchLimit {
		return errors.New("请选中 1 至 100 个文件")
	}
	seen := make(map[string]bool, len(paths))
	parent := path.Dir(paths[0])
	for _, remote := range paths {
		if !validSearchPath(remote) || !safeWindowsFileParts(remote) || path.Dir(remote) != parent || seen[remote] {
			return errors.New("请重新选择同一目录中的普通文件")
		}
		seen[remote] = true
	}
	return nil
}

func (s *DesktopService) SaveMountedFiles(id string, paths []string) (*MountedFileBatchResult, error) {
	if err := validateMountedSavePaths(paths); err != nil {
		return nil, err
	}
	return s.saveMountedBatch(id, paths, "")
}

func (s *DesktopService) RetryMountedFileBatch(recordID string) (*MountedFileBatchResult, error) {
	if !historyID.MatchString(recordID) {
		return nil, errors.New("另存记录无效，请重新打开")
	}
	return s.saveMountedBatch("", nil, recordID)
}

func (s *DesktopService) saveMountedBatch(id string, paths []string, retryID string) (*MountedFileBatchResult, error) {
	if s.chooseMountedSaveFolder == nil {
		return nil, errors.New("当前环境无法打开文件夹选择窗口")
	}
	ctx, finish, err := s.beginOperation("mount.file.save")
	if err != nil {
		return nil, err
	}
	defer finish()
	var sourceKey string
	if retryID != "" {
		s.mu.Lock()
		record, err := s.retrySaveRecordLocked(retryID)
		s.mu.Unlock()
		if err != nil {
			return nil, err
		}
		id, paths, sourceKey = record.SourceID, record.retryPaths(), record.SourceKey
	}
	folder, err := s.chooseMountedSaveFolder()
	if err != nil {
		return nil, errors.New("无法打开文件夹选择窗口")
	}
	if folder == "" {
		return nil, nil
	}
	if ctx.Err() != nil {
		return nil, errors.New("操作已取消")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	profile, err := s.find(id)
	if err != nil || (sourceKey != "" && sourceKey != mountedSaveSourceKey(profile)) {
		return nil, errors.New("原网盘连接已变化，请重新选择文件")
	}
	root, err := s.mountedSaveRoot(ctx, id, folder)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	dir, err := mountedSaveFolder(folder)
	if err != nil {
		return nil, err
	}
	defer dir.Close()

	result := &MountedFileBatchResult{Folder: folder, Items: make([]MountedFileSaveResult, 0, len(paths))}
	for index, remote := range paths {
		name := path.Base(remote)
		item := MountedFileSaveResult{Name: name, State: "unprocessed"}
		if ctx.Err() == nil {
			target := filepath.Join(folder, name)
			err := mountedSaveTargetAvailable(target)
			if err == nil {
				err = s.copyMountedFile(ctx, root, remote, target, MountedFileSaveProgress{Index: index + 1, Count: len(paths), Name: name})
			}
			switch {
			case err == nil:
				item.State = "saved"
			case ctx.Err() != nil:
				item.State = "cancelled"
			case errors.Is(err, errMountedSaveExists):
				item.State = "skipped"
				item.Message = "同名文件已存在"
			default:
				item.State = "failed"
				item.Message = err.Error()
			}
		}
		result.Items = append(result.Items, item)
	}
	var folderID *migrationIdentity
	if id, err := migrationFileIdentity(dir); err == nil {
		folderID = &id
	}
	s.recordMountedSaveLocked(profile, paths, result, retryID, folderID)
	return result, nil
}
