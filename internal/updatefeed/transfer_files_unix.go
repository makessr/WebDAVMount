//go:build !windows

package updatefeed

import (
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
)

func transferSafePath(path string) error {
	for p := filepath.Clean(path); ; p = filepath.Dir(p) {
		info, e := os.Lstat(p)
		if e == nil && info.Mode()&os.ModeSymlink != 0 {
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
	f, e := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	if e = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); e != nil {
		f.Close()
		return nil, ErrWorkspaceBusy
	}
	return func() { unix.Flock(int(f.Fd()), unix.LOCK_UN); f.Close() }, nil
}
func publishTransfer(source, destination string) error { return os.Link(source, destination) }
