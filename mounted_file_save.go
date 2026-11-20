package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

type MountedFileSaveProgress struct {
	Active bool   `json:"active"`
	Bytes  int64  `json:"bytes"`
	Total  int64  `json:"total"`
	Index  int    `json:"index"`
	Count  int    `json:"count"`
	Name   string `json:"name"`
	Stage  string `json:"stage,omitempty"`
}

var errMountedSaveExists = errors.New("目标文件已存在，请另选文件名")

func (s *DesktopService) MountedFileSaveProgress() MountedFileSaveProgress {
	s.fileSaveMu.Lock()
	defer s.fileSaveMu.Unlock()
	return s.fileSave
}

func (s *DesktopService) setMountedFileSaveProgress(progress MountedFileSaveProgress) {
	s.fileSaveMu.Lock()
	defer s.fileSaveMu.Unlock()
	s.fileSave = progress
}

func safeWindowsFileParts(relative string) bool {
	if !filepath.IsLocal(filepath.FromSlash(relative)) {
		return false
	}
	for _, part := range strings.Split(relative, "/") {
		if part == "" || strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") ||
			strings.ContainsAny(part, ":*?<>|\"") || strings.ContainsFunc(part, func(r rune) bool { return r < 32 || r == 127 }) {
			return false
		}
	}
	return true
}

// Copy an explicitly selected, ordinary file from an identified read-only
// mount. The destination is chosen by the user through the native dialog.
func (s *DesktopService) SaveMountedFile(id, remotePath string) (string, error) {
	if remotePath == "" || !validSearchPath(remotePath) || !safeWindowsFileParts(remotePath) {
		return "", errors.New("该文件路径无法在 Windows 磁盘上安全访问")
	}
	if s.chooseMountedFile == nil {
		return "", errors.New("当前环境无法打开文件保存窗口")
	}
	ctx, finish, err := s.beginOperation("mount.file.save")
	if err != nil {
		return "", err
	}
	defer finish()
	destination, err := s.chooseMountedFile(path.Base(remotePath))
	if err != nil {
		return "", errors.New("无法打开文件保存窗口")
	}
	if destination == "" {
		return "", nil
	}
	if err := ctx.Err(); err != nil {
		return "", errors.New("操作已取消")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	root, err := s.mountedSaveRoot(ctx, id, destination)
	if err != nil {
		return "", err
	}
	defer root.Close()
	dir, err := mountedSaveDestination(destination)
	if err != nil {
		return "", err
	}
	defer dir.Close()
	if err := s.copyMountedFile(ctx, root, remotePath, destination, MountedFileSaveProgress{}); err != nil {
		return "", err
	}
	return destination, nil
}

// The caller holds s.mu through the copy so the drive cannot be reassigned.
func (s *DesktopService) mountedSaveRoot(ctx context.Context, id, destination string) (*os.Root, error) {
	if err := s.connectionManagementReadyLocked(); err != nil {
		return nil, err
	}
	p, err := s.find(id)
	if err != nil {
		return nil, err
	}
	if !s.engineOnline() {
		return nil, errors.New("挂载服务不可用，请先恢复挂载")
	}
	raw, err := s.callContext(ctx, "mount/listmounts", nil)
	if err != nil {
		return nil, err
	}
	mounts, err := identifiedMounts(raw, s.profiles)
	if err != nil {
		return nil, err
	}
	owned := false
	destinationDrive := filepath.VolumeName(destination)
	for _, mount := range mounts {
		letter := mount["MountPoint"].(string)
		if strings.EqualFold(letter, destinationDrive) {
			return nil, errors.New("请选择本机目录保存文件")
		}
		if mount["ProfileID"] == p.ID && strings.EqualFold(letter, p.Drive) {
			owned = true
		}
	}
	if !owned {
		return nil, errors.New("请先挂载此网盘，再另存文件")
	}
	root, err := os.OpenRoot(p.Drive + `\`)
	if err != nil {
		return nil, errors.New("无法访问挂载磁盘，请检查挂载状态")
	}
	return root, nil
}

func (s *DesktopService) copyMountedFile(ctx context.Context, root *os.Root, remotePath, destination string, progress MountedFileSaveProgress) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	localPath := filepath.FromSlash(remotePath)
	if noReparse(filepath.Join(root.Name(), localPath)) != nil {
		return errors.New("无法确认所选文件，请刷新目录后重试")
	}
	source, err := root.Open(localPath)
	if err != nil {
		return errors.New("无法读取所选文件，请刷新目录后重试")
	}
	defer source.Close()
	info, err := source.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("请选择一个普通文件")
	}
	progress.Active = true
	progress.Total = info.Size()
	s.setMountedFileSaveProgress(progress)
	defer func() {
		progress.Active = false
		s.setMountedFileSaveProgress(progress)
	}()
	return saveMountedFileCopy(ctx, source, info, destination, func(count int64) {
		progress.Bytes = count
		s.setMountedFileSaveProgress(progress)
	})
}

func mountedSaveDestination(destination string) (*os.File, error) {
	if !safeWindowsFileParts(filepath.Base(destination)) {
		return nil, errors.New("请选择本机磁盘上的普通文件路径")
	}
	if filepath.Clean(destination) != destination || !filepath.IsAbs(destination) {
		return nil, errors.New("请选择本机磁盘上的普通文件路径")
	}
	dir, err := mountedSaveFolder(filepath.Dir(destination))
	if err != nil {
		return nil, err
	}
	if err := mountedSaveTargetAvailable(destination); err != nil {
		dir.Close()
		return nil, err
	}
	return dir, nil
}

func mountedSaveTargetAvailable(destination string) error {
	if _, err := os.Lstat(destination); err == nil {
		return errMountedSaveExists
	} else if !os.IsNotExist(err) {
		return errors.New("目标文件路径不可访问，请另选位置")
	}
	return nil
}

func mountedSaveFolder(folder string) (*os.File, error) {
	volume := filepath.VolumeName(folder)
	relative := strings.TrimPrefix(folder, volume+`\`)
	if !filepath.IsAbs(folder) || len(volume) != 2 || volume[1] != ':' || filepath.Clean(folder) != folder ||
		(relative != "" && !safeWindowsFileParts(filepath.ToSlash(relative))) {
		return nil, errors.New("请选择本机磁盘上的普通文件夹")
	}
	root, err := windows.UTF16PtrFromString(volume + `\`)
	if err != nil {
		return nil, errors.New("保存路径无效")
	}
	switch windows.GetDriveType(root) {
	case windows.DRIVE_FIXED, windows.DRIVE_REMOVABLE, windows.DRIVE_RAMDISK:
	default:
		return nil, errors.New("请选择本机磁盘上的文件夹")
	}
	dir, _, err := migrationOpen(folder, windows.FILE_READ_ATTRIBUTES, windows.OPEN_EXISTING, true)
	if err != nil {
		return nil, errors.New("无法打开目标目录，请选择本机普通文件夹")
	}
	return dir, nil
}

func saveMountedFileCopy(ctx context.Context, source *os.File, info os.FileInfo, destination string, progress func(int64)) error {
	tmp, err := os.CreateTemp(filepath.Dir(destination), ".wdm-save-*.tmp")
	if err != nil {
		return errors.New("无法创建临时文件，请检查目标目录权限")
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	buffer := make([]byte, 256<<10)
	if err := copyMountedFileContents(ctx, source, info, tmp, buffer, progress); err != nil {
		return err
	}
	return publishMountedSave(ctx, tmp, destination)
}

func copyMountedFileContents(ctx context.Context, source *os.File, info os.FileInfo, target io.Writer, buffer []byte, progress func(int64)) error {
	var copied int64
	for copied < info.Size() {
		if ctx.Err() != nil {
			return errors.New("操作已取消")
		}
		n, err := io.ReadFull(source, buffer[:min(int64(len(buffer)), info.Size()-copied)])
		if ctx.Err() != nil {
			return errors.New("操作已取消")
		}
		if err != nil {
			return errors.New("读取网盘文件中断，请检查连接后重试")
		}
		if written, err := target.Write(buffer[:n]); err != nil || written != n {
			return errors.New("本机文件写入失败，请检查磁盘空间")
		}
		copied += int64(n)
		progress(copied)
	}
	if after, err := source.Stat(); err != nil || after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime()) {
		return errors.New("网盘文件在另存期间已发生变化，请重新选择")
	}
	return nil
}

func publishMountedSave(ctx context.Context, tmp *os.File, destination string) error {
	if err := tmp.Sync(); err != nil {
		return errors.New("无法写入本机磁盘，请检查磁盘空间")
	}
	if err := tmp.Close(); err != nil {
		return errors.New("无法完成本机文件写入")
	}
	if ctx.Err() != nil {
		return errors.New("操作已取消")
	}
	if noReparse(destination) != nil {
		return errors.New("保存路径已变化，请重新选择")
	}
	from, err := windows.UTF16PtrFromString(tmp.Name())
	if err != nil {
		return errors.New("保存路径无效")
	}
	to, err := windows.UTF16PtrFromString(destination)
	if err != nil {
		return errors.New("保存路径无效")
	}
	if err := windows.MoveFileEx(from, to, windows.MOVEFILE_WRITE_THROUGH); err != nil {
		if errors.Is(err, windows.ERROR_ALREADY_EXISTS) || errors.Is(err, windows.ERROR_FILE_EXISTS) {
			return errMountedSaveExists
		}
		return errors.New("目标文件已存在或保存目录不可用，请另选文件名")
	}
	return nil
}

func (s *DesktopService) localUserFileFolder(local string) (*os.File, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !filepath.IsAbs(local) || filepath.Clean(local) != local || !safeWindowsFileParts(filepath.Base(local)) {
		return nil, errors.New("请选择本机普通文件路径")
	}
	folder := filepath.Dir(local)
	if migrationWithin(s.dir, local) || migrationWithin(s.cacheRoot, local) {
		return nil, errors.New("请选择程序数据和缓存目录以外的本机位置")
	}
	for _, p := range s.profiles {
		if strings.EqualFold(filepath.VolumeName(local), p.Drive) {
			return nil, errors.New("请选择本机磁盘保存和提交编辑副本")
		}
	}
	return mountedSaveFolder(folder)
}
