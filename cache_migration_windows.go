package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"golang.org/x/sys/windows"
	"io"
	"os"
	"strings"
	"unsafe"
)

type migrationRange struct{ Offset, Length int64 }

func migrationOpen(path string, access, disposition uint32, directory bool) (*os.File, windows.ByHandleFileInformation, error) {
	var info windows.ByHandleFileInformation
	if err := noReparse(path); err != nil {
		return nil, info, err
	}
	ptr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, info, err
	}
	flags := uint32(windows.FILE_FLAG_OPEN_REPARSE_POINT)
	share := uint32(windows.FILE_SHARE_READ)
	if directory {
		flags |= windows.FILE_FLAG_BACKUP_SEMANTICS
		share |= windows.FILE_SHARE_WRITE
	}
	handle, err := windows.CreateFile(ptr, access, share, nil, disposition, flags, 0)
	if err != nil {
		return nil, info, err
	}
	f := os.NewFile(uintptr(handle), path)
	if err = windows.GetFileInformationByHandle(handle, &info); err != nil {
		f.Close()
		return nil, info, err
	}
	isDir := info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || isDir != directory || (!directory && info.NumberOfLinks != 1) {
		f.Close()
		return nil, info, errors.New("迁移不跟随链接或特殊文件")
	}
	return f, info, nil
}

func migrationCanonicalDirectory(path string) (string, *os.File, error) {
	f, _, err := migrationOpen(path, windows.FILE_READ_ATTRIBUTES, windows.OPEN_EXISTING, true)
	if err != nil {
		return "", nil, err
	}
	buffer := make([]uint16, 32768)
	// A volume GUID and normalized name also detect drive-letter/short-name aliases.
	n, err := windows.GetFinalPathNameByHandle(windows.Handle(f.Fd()), &buffer[0], uint32(len(buffer)), 1)
	if err != nil || n == 0 || n >= uint32(len(buffer)) {
		f.Close()
		return "", nil, errors.New("无法确认缓存目录的实际位置")
	}
	return strings.TrimRight(windows.UTF16ToString(buffer[:n]), `\`), f, nil
}

func migrationRanges(f *os.File, size int64, sparse bool) ([]migrationRange, error) {
	if size == 0 {
		return nil, nil
	}
	if !sparse {
		return []migrationRange{{0, size}}, nil
	}
	ranges := []migrationRange{}
	for offset := int64(0); offset < size; {
		input := migrationRange{offset, size - offset}
		output := make([]migrationRange, 256)
		var count uint32
		err := windows.DeviceIoControl(windows.Handle(f.Fd()), windows.FSCTL_QUERY_ALLOCATED_RANGES, (*byte)(unsafe.Pointer(&input)), 16, (*byte)(unsafe.Pointer(&output[0])), uint32(len(output)*16), &count, nil)
		if err != nil && err != windows.ERROR_MORE_DATA {
			return nil, err
		}
		if count%16 != 0 || count > uint32(len(output)*16) {
			return nil, errors.New("稀疏文件范围无效")
		}
		next := offset
		for _, r := range output[:count/16] {
			if r.Offset < 0 || r.Length <= 0 || r.Offset > size || r.Length > int64(^uint64(0)>>1)-r.Offset {
				return nil, errors.New("稀疏文件范围越界")
			}
			end := r.Offset + r.Length
			if end > size {
				end = size
			}
			start := r.Offset
			if start < next {
				start = next
			}
			if end > start {
				ranges = append(ranges, migrationRange{start, end - start})
				next = end
			}
		}
		if err == nil {
			break
		}
		if next <= offset || len(ranges) > 1000000 {
			return nil, errors.New("稀疏文件范围无法继续")
		}
		offset = next
	}
	return ranges, nil
}

func copyMigrationFile(ctx context.Context, source, target string, entry migrationEntry, progress func(int64)) error {
	src, basic, err := migrationOpen(source, windows.GENERIC_READ, windows.OPEN_EXISTING, false)
	if err != nil {
		return err
	}
	defer src.Close()
	info, err := src.Stat()
	if err != nil || info.Size() != entry.Size || !info.ModTime().Equal(entry.Modified) {
		return errors.New("迁移期间源文件发生变化")
	}
	sparse := basic.FileAttributes&windows.FILE_ATTRIBUTE_SPARSE_FILE != 0
	ranges, err := migrationRanges(src, entry.Size, sparse)
	if err != nil {
		return err
	}
	dst, _, err := migrationOpen(target, windows.GENERIC_READ|windows.GENERIC_WRITE, windows.CREATE_NEW, false)
	if err != nil {
		return err
	}
	defer dst.Close()
	if sparse {
		var returned uint32
		if err = windows.DeviceIoControl(windows.Handle(dst.Fd()), windows.FSCTL_SET_SPARSE, nil, 0, nil, 0, &returned, nil); err != nil {
			return migrationProblem("目标文件系统无法保留稀疏缓存，请选择支持稀疏文件的本机磁盘")
		}
	}
	if err = dst.Truncate(entry.Size); err != nil {
		return err
	}
	buffer := make([]byte, 1<<20)
	for _, r := range ranges {
		for offset := r.Offset; offset < r.Offset+r.Length; {
			if err = ctx.Err(); err != nil {
				return err
			}
			n := int64(len(buffer))
			if remaining := r.Offset + r.Length - offset; remaining < n {
				n = remaining
			}
			if _, err = src.ReadAt(buffer[:n], offset); err != nil {
				return err
			}
			if _, err = dst.WriteAt(buffer[:n], offset); err != nil {
				return err
			}
			offset += n
			progress(offset)
		}
	}
	progress(entry.Size)
	return dst.Sync()
}

func verifyMigrationFile(ctx context.Context, source, target string, entry migrationEntry, progress func(int64)) error {
	src, _, err := migrationOpen(source, windows.GENERIC_READ, windows.OPEN_EXISTING, false)
	if err != nil {
		return err
	}
	defer src.Close()
	dst, _, err := migrationOpen(target, windows.GENERIC_READ|windows.FILE_WRITE_ATTRIBUTES, windows.OPEN_EXISTING, false)
	if err != nil {
		return err
	}
	defer dst.Close()
	a, err := src.Stat()
	if err != nil {
		return err
	}
	b, err := dst.Stat()
	if err != nil {
		return err
	}
	if a.Size() != entry.Size || b.Size() != entry.Size || !a.ModTime().Equal(entry.Modified) {
		return errors.New("迁移文件大小或来源已变化")
	}
	hashA, hashB := sha256.New(), sha256.New()
	buffer := make([]byte, 1<<20)
	for offset := int64(0); offset < entry.Size; {
		if err = ctx.Err(); err != nil {
			return err
		}
		n := int64(len(buffer))
		if entry.Size-offset < n {
			n = entry.Size - offset
		}
		if _, err = io.ReadFull(src, buffer[:n]); err != nil {
			return err
		}
		hashA.Write(buffer[:n])
		if _, err = io.ReadFull(dst, buffer[:n]); err != nil {
			return err
		}
		hashB.Write(buffer[:n])
		offset += n
		progress(offset)
	}
	if !bytes.Equal(hashA.Sum(nil), hashB.Sum(nil)) {
		return migrationProblem("迁移校验不一致，未切换缓存位置")
	}
	modified := windows.NsecToFiletime(entry.Modified.UnixNano())
	return windows.SetFileTime(windows.Handle(dst.Fd()), nil, nil, &modified)
}
