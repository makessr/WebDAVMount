package main

import (
	"golang.org/x/sys/windows"
	"webdavmount/internal/branding"
)

func trayIcon() []byte { return branding.PNG(32) }

func showTrayNotice() {
	title, _ := windows.UTF16PtrFromString("已最小化到系统托盘")
	message, _ := windows.UTF16PtrFromString("WebDAVMount 已最小化到系统托盘，已挂载磁盘将继续保持连接。\n\n如需退出：右键点击系统托盘中的 W 图标，选择「退出并卸载全部磁盘」。\n\n如需恢复窗口：点击托盘图标，或再次打开 WebDAVMount。")
	windows.MessageBox(0, message, title, windows.MB_OK|windows.MB_ICONINFORMATION|windows.MB_SETFOREGROUND|windows.MB_TOPMOST)
}
