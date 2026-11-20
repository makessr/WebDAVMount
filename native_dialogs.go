package main

import (
	"errors"
	"github.com/wailsapp/wails/v3/pkg/application"
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"time"
)

func configureFileDialogs(s *DesktopService, app *application.App, window *application.WebviewWindow) {
	s.chooseMountedFile = func(filename string) (string, error) {
		return normalizeDialogResult(app.Dialog.SaveFileWithOptions(&application.SaveFileDialogOptions{Title: "另存网盘文件到本机", Filename: filename, CanCreateDirectories: true, Window: window}).PromptForSingleSelection())
	}
	s.chooseMountedArchive = func(filename string) (string, error) {
		return normalizeDialogResult(app.Dialog.SaveFileWithOptions(&application.SaveFileDialogOptions{
			Title: "保存 ZIP 文件", Filename: filename, CanCreateDirectories: true, Window: window,
			Filters: []application.FileFilter{{DisplayName: "ZIP 文件", Pattern: "*.zip"}},
		}).PromptForSingleSelection())
	}
	s.chooseMountedSaveFolder = func() (string, error) {
		return normalizeDialogResult(app.Dialog.OpenFile().CanChooseFiles(false).CanChooseDirectories(true).CanCreateDirectories(true).SetTitle("选择批量另存的位置").AttachToWindow(window).PromptForSingleSelection())
	}
	s.chooseConnectionExport = func() (string, error) {
		return normalizeDialogResult(app.Dialog.SaveFileWithOptions(&application.SaveFileDialogOptions{Title: "导出网盘连接备份", Filename: "WebDAVMount-connections-" + time.Now().Format("20060102-150405") + ".wdmconnections", CanCreateDirectories: true, Window: window, Filters: []application.FileFilter{{DisplayName: "加密网盘连接备份", Pattern: "*.wdmconnections"}}}).PromptForSingleSelection())
	}
	s.chooseConnectionImport = func() (string, error) {
		return normalizeDialogResult(app.Dialog.OpenFile().CanChooseFiles(true).CanChooseDirectories(false).SetTitle("选择网盘连接备份").AttachToWindow(window).PromptForSingleSelection())
	}
	s.openDirectory = func(path string) error {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		initErr := windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED|windows.COINIT_DISABLE_OLE1DDE)
		if initErr == nil || initErr == syscall.Errno(1) {
			defer windows.CoUninitialize()
		} else if initErr != syscall.Errno(0x80010106) {
			return initErr
		}
		verb, _ := windows.UTF16PtrFromString("explore")
		folder, err := windows.UTF16PtrFromString(path)
		if err != nil {
			return err
		}
		return windows.ShellExecute(0, verb, folder, nil, nil, 1)
	}
	s.chooseCacheDirectory = func(current string) (string, error) {
		dialog := app.Dialog.OpenFile().CanChooseFiles(false).CanChooseDirectories(true).CanCreateDirectories(false).SetTitle("选择缓存所在文件夹").AttachToWindow(window)
		if info, err := os.Stat(current); err == nil && info.IsDir() {
			dialog.SetDirectory(current)
		}
		return normalizeDialogResult(dialog.PromptForSingleSelection())
	}
	s.chooseDiagnosticFile = func() (string, error) {
		return normalizeDialogResult(app.Dialog.SaveFileWithOptions(&application.SaveFileDialogOptions{Title: "导出诊断日志", Filename: "WebDAVMount-diagnostics-" + time.Now().Format("20060102-150405") + ".json", CanCreateDirectories: true, Window: window, Filters: []application.FileFilter{{DisplayName: "JSON 诊断日志", Pattern: "*.json"}}}).PromptForSingleSelection())
	}
	s.chooseCheckReportFile = func() (string, error) {
		return normalizeDialogResult(app.Dialog.SaveFileWithOptions(&application.SaveFileDialogOptions{
			Title: "保存脱敏体检报告", Filename: "WebDAVMount-check-" + time.Now().Format("20060102-150405") + ".json",
			CanCreateDirectories: true, Window: window, Filters: []application.FileFilter{{DisplayName: "JSON 体检报告", Pattern: "*.json"}},
		}).PromptForSingleSelection())
	}
	s.chooseUpdateFile = func() (string, error) {
		filename := "WebDAVMount-update-setup.exe"
		if version := s.UpdateInfo().AvailableVersion; version != "" {
			filename = "WebDAVMount-" + version + "-setup.exe"
		}
		return normalizeDialogResult(app.Dialog.SaveFileWithOptions(&application.SaveFileDialogOptions{Title: "保存更新安装包", Filename: filename, CanCreateDirectories: true, Window: window, Filters: []application.FileFilter{{DisplayName: "Windows 安装程序", Pattern: "*.exe"}}}).PromptForSingleSelection())
	}
}

func normalizeDialogResult(path string, err error) (string, error) {
	// Wails 3.1.0's internal CFD sentinel is not importable by applications.
	if err != nil && err.Error() == "cancelled by user" {
		return "", nil
	}
	return path, err
}

// Choosing changes only the form. SaveCacheSettings remains the explicit commit.
func (s *DesktopService) ChooseCacheDirectory(current string) (string, error) {
	if s.chooseCacheDirectory == nil {
		return "", errors.New("当前环境无法打开文件夹选择窗口")
	}
	path, err := s.chooseCacheDirectory(current)
	if err != nil {
		return "", errors.New("无法打开缓存文件夹选择窗口")
	}
	if path == "" {
		return "", nil
	}
	info, err := os.Stat(path)
	if !filepath.IsAbs(path) || err != nil || !info.IsDir() {
		return "", errors.New("请选择已存在的文件夹")
	}
	if err = noReparse(path); err != nil {
		return "", errors.New("该文件夹不支持作为缓存位置")
	}
	return path, nil
}
