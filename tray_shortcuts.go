package main

import "sync/atomic"

const trayShortcutEvent = "webdavmount:tray-shortcut"

type trayShortcut uint32

const (
	trayNoShortcut trayShortcut = iota
	trayMountedDrives
	trayMountPresets
	trayPresetSwitch
)

// Windows can receive a tray click before the WebView has registered its event
// listener. Retain the latest request and use the event only as a wake-up.
type trayShortcutQueue struct {
	next atomic.Uint32
}

func (q *trayShortcutQueue) request(action trayShortcut) {
	q.next.Store(uint32(action))
}

func (q *trayShortcutQueue) take() string {
	switch trayShortcut(q.next.Swap(uint32(trayNoShortcut))) {
	case trayMountedDrives:
		return "mounted-drives"
	case trayMountPresets:
		return "mount-presets"
	case trayPresetSwitch:
		return "preset-switch"
	default:
		return ""
	}
}

func (s *DesktopService) TakeTrayShortcut() string {
	return s.trayShortcuts.take()
}
