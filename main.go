package main

import (
	"crypto/sha256"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"sync/atomic"
	"unsafe"
	"webdavmount/internal/branding"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	dir, _ := os.UserConfigDir()
	defaultDir := filepath.Join(dir, "WebDAVMount")
	dataDir := flag.String("data-dir", defaultDir, "独立数据目录（用于隔离验证）")
	checkOnly := flag.Bool("check-dependencies", false, "仅检查依赖，不打开窗口或读取账号")
	reportPath := flag.String("report", "", "依赖检查报告保存位置（新文件）")
	flag.Parse()
	exe, _ := os.Executable()
	engine := filepath.Join(filepath.Dir(exe), "rclone.exe")
	if *checkOnly {
		report := checkDependencies(engine)
		data, _ := json.MarshalIndent(report, "", "  ")
		fmt.Println(string(data))
		if *reportPath != "" {
			f, e := os.OpenFile(*reportPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
			if e != nil {
				fmt.Fprintln(os.Stderr, "无法创建检查报告")
				os.Exit(3)
			}
			_, e = f.Write(data)
			closeErr := f.Close()
			if e != nil || closeErr != nil {
				os.Exit(3)
			}
		}
		if !report.Ready {
			os.Exit(2)
		}
		return
	}
	if err := checkPendingProgramUpgrade(filepath.Dir(exe)); err != nil {
		startupError(err.Error())
		return
	}
	resolved, err := filepath.Abs(*dataDir)
	if err != nil {
		startupError(err.Error())
		return
	}
	key := `Local\WebDAVMountDesktop`
	if resolved != defaultDir {
		key += fmt.Sprintf("-%x", sha256.Sum256([]byte(resolved)))
	}
	name, _ := windows.UTF16PtrFromString(key)
	lock, err := windows.CreateMutex(nil, false, name)
	if lock != 0 {
		defer windows.CloseHandle(lock)
	}
	if err == windows.ERROR_ALREADY_EXISTS {
		eventName, _ := windows.UTF16PtrFromString(key + "-show")
		if event, e := windows.OpenEvent(windows.EVENT_MODIFY_STATE, false, eventName); e == nil {
			windows.SetEvent(event)
			windows.CloseHandle(event)
		} else {
			startupError("已有程序正在运行，请从系统托盘显示窗口")
		}
		return
	}
	if err != nil {
		startupError("程序已运行或无法锁定本机数据")
		return
	}
	if report := checkDependencies(engine); !report.Ready {
		startupError("启动前检查发现缺少运行组件：\n\n" + report.problemText() + "\n\n安装完成后重新打开程序。程序不会自动安装组件。")
		return
	}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		startupError(err.Error())
		return
	}
	defer windows.CloseHandle(job)
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE | windows.JOB_OBJECT_LIMIT_BREAKAWAY_OK
	if _, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		startupError(err.Error())
		return
	}
	if err = windows.AssignProcessToJobObject(job, windows.CurrentProcess()); err != nil {
		startupError(err.Error())
		return
	}
	service, err := newDesktop(resolved, engine)
	if err != nil {
		startupError(err.Error())
		return
	}
	defer service.shutdown()
	service.sessionRecovery = loadSessionRecovery(resolved, service.profiles)
	service.startCacheCleanup()
	app := application.New(application.Options{Name: "WebDAVMount", Icon: branding.PNG(256), Services: []application.Service{application.NewService(service)}, Assets: application.AssetOptions{Handler: application.AssetFileServerFS(assets)}})
	window := app.Window.NewWithOptions(application.WebviewWindowOptions{Title: "WebDAVMount · 网盘挂载", Width: 980, Height: 680, MinWidth: 860, MinHeight: 580, URL: "/", BackgroundColour: application.NewRGB(244, 246, 243)})
	configureFileDialogs(service, app, window)
	configureLoginStartup(service, exe, filepath.Clean(resolved) == filepath.Clean(defaultDir))
	var exiting atomic.Bool
	var trayNoticeShowing atomic.Bool
	show := func() { window.Show(); window.Restore(); window.Focus() }
	quit := func() { exiting.Store(true); service.CancelOperation(); app.Quit() }
	service.exit = quit
	service.startUpdateInstaller = startUpdateInstaller
	tray := app.SystemTray.New()
	tray.SetIcon(trayIcon())
	tray.SetTooltip("WebDAVMount · 关闭窗口后保持挂载")
	tray.OnClick(show)
	tray.OnDoubleClick(show)
	menu := app.NewMenu()
	menu.Add("显示窗口").OnClick(func(*application.Context) { show() })
	menu.AddSeparator()
	requestShortcut := func(action trayShortcut) {
		service.trayShortcuts.request(action)
		show()
		app.Event.Emit(trayShortcutEvent)
	}
	menu.Add("打开已挂载网盘…").OnClick(func(*application.Context) { requestShortcut(trayMountedDrives) })
	menu.Add("挂载方案…").OnClick(func(*application.Context) { requestShortcut(trayMountPresets) })
	menu.Add("切换挂载方案…").OnClick(func(*application.Context) { requestShortcut(trayPresetSwitch) })
	menu.AddSeparator()
	menu.Add("退出并卸载全部磁盘").OnClick(func(*application.Context) {
		if err := service.Exit(); err != nil {
			show()
			go startupError(err.Error())
		}
	})
	tray.SetMenu(menu)
	window.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		if !exiting.Load() {
			e.Cancel()
			window.Hide()
			if trayNoticeShowing.CompareAndSwap(false, true) {
				go func() {
					defer trayNoticeShowing.Store(false)
					showTrayNotice()
				}()
			}
		}
	})
	eventName, _ := windows.UTF16PtrFromString(key + "-show")
	event, e := windows.CreateEvent(nil, 0, 0, eventName)
	if e != nil {
		startupError("无法创建窗口恢复事件")
		return
	}
	defer windows.CloseHandle(event)
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
			}
			if result, e := windows.WaitForSingleObject(event, 500); e == nil && result == windows.WAIT_OBJECT_0 {
				show()
			}
		}
	}()
	service.startAutoMount()
	service.startRecoveryMonitor()
	service.startMaintenanceMonitor()
	service.startCloudTokenMonitor()
	if err = app.Run(); err != nil {
		startupError(err.Error())
	} else {
		service.sessionRecovery.finish()
	}
}
func startupError(msg string) {
	t, _ := windows.UTF16PtrFromString(msg)
	title, _ := windows.UTF16PtrFromString("WebDAVMount")
	windows.MessageBox(0, t, title, windows.MB_OK|windows.MB_ICONERROR)
	fmt.Fprintln(os.Stderr, msg)
}
