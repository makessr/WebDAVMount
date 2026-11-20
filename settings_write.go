package main

import (
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
)

// Write a fresh sibling, flush and close before replacement. Failure preserves
// the old destination and removes only this attempt's temporary file.
func atomicWriteSettings(path string, data []byte) error {
	if err := noReparse(path); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".settings-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, writeErr := f.Write(data)
	syncErr := f.Sync()
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	if syncErr != nil {
		return syncErr
	}
	if closeErr != nil {
		return closeErr
	}
	from, err := windows.UTF16PtrFromString(f.Name())
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	// Complete the same-volume rename with a write-through barrier. The
	// original file survives any failure before this final publication.
	return windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}
