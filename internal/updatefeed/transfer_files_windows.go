//go:build windows

package updatefeed

import (
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
)

func transferSafePath(path string) error {
	for p := filepath.Clean(path); ; p = filepath.Dir(p) {
		name, e := windows.UTF16PtrFromString(p)
		if e != nil {
			return ErrUnsafePath
		}
		attrs, e := windows.GetFileAttributes(name)
		if e == nil && attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			return ErrUnsafePath
		}
		if e != nil && !os.IsNotExist(e) {
			return ErrUnsafePath
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	return nil
}
func lockTransfer(path string) (func(), error) {
	if transferSafePath(path) != nil {
		return nil, ErrUnsafePath
	}
	name, e := windows.UTF16PtrFromString(path)
	if e != nil {
		return nil, ErrUnsafePath
	}
	h, e := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_ALWAYS, windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if e != nil {
		return nil, ErrWorkspaceBusy
	}
	return func() { windows.CloseHandle(h) }, nil
}
func publishTransfer(source, destination string) error {
	from, e := windows.UTF16PtrFromString(source)
	if e != nil {
		return e
	}
	to, e := windows.UTF16PtrFromString(destination)
	if e != nil {
		return e
	}
	return windows.MoveFileEx(from, to, 0)
}
