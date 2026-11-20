package main

import (
	"errors"
	"golang.org/x/sys/windows"
	"unsafe"
)

// Query metadata only, with sharing enabled so downloads need not stop.
func fileAllocatedBytes(path string) (int64, error) {
	ptr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	handle, err := windows.CreateFile(ptr, windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return 0, err
	}
	defer windows.CloseHandle(handle)
	var basic windows.ByHandleFileInformation
	if err = windows.GetFileInformationByHandle(handle, &basic); err != nil {
		return 0, err
	}
	if basic.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return 0, errors.New("缓存占用统计不跟随重解析点")
	}
	var size int64
	if basic.FileAttributes&(windows.FILE_ATTRIBUTE_SPARSE_FILE|windows.FILE_ATTRIBUTE_COMPRESSED) != 0 {
		var info struct {
			Size                                int64
			Format                              uint16
			UnitShift, ChunkShift, ClusterShift byte
			Reserved                            [3]byte
		}
		err = windows.GetFileInformationByHandleEx(handle, windows.FileCompressionInfo, (*byte)(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)))
		size = info.Size
	} else {
		var info struct {
			Allocation, End          int64
			Links                    uint32
			DeletePending, Directory byte
		}
		err = windows.GetFileInformationByHandleEx(handle, windows.FileStandardInfo, (*byte)(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)))
		size = info.Allocation
	}
	if err != nil {
		return 0, err
	}
	if size < 0 {
		return 0, errors.New("文件分配空间无效")
	}
	return size, nil
}
