package desktop

import (
	"archive/zip"
	"context"
	"errors"
	"io"
	"math"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const mountedArchiveEntryLimit = 10000
const mountedArchiveDepthLimit = 32

type mountedArchiveEntry struct {
	source string
	name   string
	info   os.FileInfo
}

type mountedArchivePlan struct {
	entries []mountedArchiveEntry
	files   int
	bytes   int64
}

func (s *DesktopService) SaveMountedFolderArchive(id, remotePath string) (string, error) {
	if remotePath == "" || !validSearchPath(remotePath) || !safeWindowsFileParts(remotePath) {
		return "", errors.New("请选择可在 Windows 访问的网盘文件夹")
	}
	if s.chooseMountedArchive == nil {
		return "", errors.New("当前环境无法打开文件保存窗口")
	}
	ctx, finish, err := s.beginOperation("mount.file.save")
	if err != nil {
		return "", err
	}
	defer finish()
	destination, err := s.chooseMountedArchive(path.Base(remotePath) + ".zip")
	if err != nil {
		return "", errors.New("无法打开文件保存窗口")
	}
	if destination == "" {
		return "", nil
	}
	if !strings.EqualFold(filepath.Ext(destination), ".zip") {
		return "", errors.New("请使用 .zip 扩展名")
	}
	if ctx.Err() != nil {
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
	defer s.setMountedFileSaveProgress(MountedFileSaveProgress{})
	s.setMountedFileSaveProgress(MountedFileSaveProgress{Active: true, Stage: "scanning"})
	plan, err := planMountedArchive(ctx, root, remotePath)
	if err != nil {
		return "", err
	}
	if err := writeMountedArchive(ctx, root, plan, destination, s.setMountedFileSaveProgress); err != nil {
		return "", err
	}
	return destination, nil
}

func planMountedArchive(ctx context.Context, root *os.Root, source string) (mountedArchivePlan, error) {
	result := mountedArchivePlan{}
	names := make(map[string]bool)
	var scan func(string, string, int) error
	scan = func(relative, name string, depth int) error {
		if ctx.Err() != nil {
			return errors.New("操作已取消")
		}
		if len(result.entries) >= mountedArchiveEntryLimit {
			return errors.New("文件夹超过打包上限（10,000 项或 32 层子目录），请分批另存")
		}
		if !validSearchPath(relative) || !safeWindowsFileParts(relative) || names[strings.ToUpper(name)] {
			return errors.New("文件夹包含 Windows 无法安全解压的名称，请改选子目录")
		}
		names[strings.ToUpper(name)] = true
		local := filepath.FromSlash(relative)
		if noReparse(filepath.Join(root.Name(), local)) != nil {
			return errors.New("无法确认目录项目，打包不会跟随链接")
		}
		info, err := root.Lstat(local)
		if err != nil {
			return errors.New("无法读取网盘目录，请刷新后重试")
		}
		if !info.IsDir() {
			if depth == 0 || !info.Mode().IsRegular() {
				return errors.New("请选择文件夹，目录内须为普通文件或子目录")
			}
			if info.Size() < 0 || info.Size() > math.MaxInt64-result.bytes {
				return errors.New("文件大小超出可打包范围")
			}
			result.entries = append(result.entries, mountedArchiveEntry{local, name, info})
			result.files++
			result.bytes += info.Size()
			return nil
		}
		if depth > mountedArchiveDepthLimit {
			return errors.New("文件夹超过 32 层子目录，请分批另存")
		}
		result.entries = append(result.entries, mountedArchiveEntry{local, name + "/", info})
		dir, err := root.Open(local)
		if err != nil {
			return errors.New("无法读取网盘目录，请刷新后重试")
		}
		defer dir.Close()
		for {
			if ctx.Err() != nil {
				return errors.New("操作已取消")
			}
			items, err := dir.ReadDir(128)
			for _, item := range items {
				part := item.Name()
				if !validSearchPath(part) || !safeWindowsFileParts(part) || strings.Contains(part, "/") {
					return errors.New("文件夹包含 Windows 无法安全解压的名称，请改选子目录")
				}
				if err := scan(relative+"/"+part, name+"/"+part, depth+1); err != nil {
					return err
				}
			}
			if err == io.EOF {
				return nil
			}
			if err != nil {
				return errors.New("网盘目录读取中断，请检查连接后重试")
			}
		}
	}
	err := scan(source, path.Base(source), 0)
	return result, err
}

func writeMountedArchive(ctx context.Context, root *os.Root, plan mountedArchivePlan, destination string, update func(MountedFileSaveProgress)) error {
	tmp, err := os.CreateTemp(filepath.Dir(destination), ".wdm-save-*.tmp")
	if err != nil {
		return errors.New("无法创建临时文件，请检查目标目录权限")
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	writer := zip.NewWriter(tmp)
	buffer := make([]byte, 256<<10)
	progress := MountedFileSaveProgress{Active: true, Stage: "archiving", Count: plan.files, Total: plan.bytes}
	update(progress)
	for _, entry := range plan.entries {
		if ctx.Err() != nil {
			return errors.New("操作已取消")
		}
		header, err := zip.FileInfoHeader(entry.info)
		if err != nil {
			return errors.New("无法生成 ZIP 文件项")
		}
		header.Name = entry.name
		header.Method = zip.Store
		target, err := writer.CreateHeader(header)
		if err != nil {
			return errors.New("无法写入 ZIP 文件，请检查磁盘空间")
		}
		if entry.info.IsDir() {
			continue
		}
		progress.Index++
		progress.Name = path.Base(entry.name)
		update(progress)
		base := progress.Bytes
		if err := copyMountedArchiveEntry(ctx, root, entry, target, buffer, func(count int64) {
			progress.Bytes = base + count
			update(progress)
		}); err != nil {
			return err
		}
	}
	if err := writer.Close(); err != nil {
		return errors.New("无法完成 ZIP 文件，请检查磁盘空间")
	}
	return publishMountedSave(ctx, tmp, destination)
}

func copyMountedArchiveEntry(ctx context.Context, root *os.Root, entry mountedArchiveEntry, target io.Writer, buffer []byte, progress func(int64)) error {
	if noReparse(filepath.Join(root.Name(), entry.source)) != nil {
		return errors.New("网盘路径已变化，请刷新目录后重试")
	}
	source, err := root.Open(entry.source)
	if err != nil {
		return errors.New("无法读取网盘文件，请刷新目录后重试")
	}
	defer source.Close()
	info, err := source.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != entry.info.Size() || !info.ModTime().Equal(entry.info.ModTime()) {
		return errors.New("网盘文件已变化，请刷新目录后重试")
	}
	return copyMountedFileContents(ctx, source, info, target, buffer, progress)
}
