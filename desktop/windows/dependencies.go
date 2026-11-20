package desktop

import (
	"context"
	"debug/pe"
	"errors"
	"github.com/wailsapp/go-webview2/webviewloader"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

type Dependency struct {
	Name    string `json:"name"`
	Ready   bool   `json:"ready"`
	Version string `json:"version,omitempty"`
	Message string `json:"message"`
	HelpURL string `json:"helpUrl,omitempty"`
}
type DependencyReport struct {
	Version    string       `json:"version"`
	Ready      bool         `json:"ready"`
	Components []Dependency `json:"components"`
}

func probeRclone(path string) (string, error) {
	if st, e := os.Stat(path); e != nil || st.IsDir() {
		return "", errors.New("缺少同目录的rclone.exe；请完整解压便携包，不要只复制主程序")
	}
	if file, err := pe.Open(path); err != nil {
		return "", errors.New("无法识别 rclone.exe，请重新解压随包版本")
	} else {
		machine := file.FileHeader.Machine
		file.Close()
		expected := uint16(pe.IMAGE_FILE_MACHINE_AMD64)
		if runtime.GOARCH == "arm64" {
			expected = pe.IMAGE_FILE_MACHINE_ARM64
		}
		if machine != expected {
			return "", errors.New("rclone.exe 的架构与客户端不匹配，请使用随包版本")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "version", "--config", "")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	for _, env := range os.Environ() {
		if !strings.HasPrefix(strings.ToUpper(env), "RCLONE_") {
			cmd.Env = append(cmd.Env, env)
		}
	}
	data, e := cmd.Output()
	if e != nil {
		return "", errors.New("rclone.exe 无法运行，请使用同一便携包内的引擎")
	}
	text := string(data)
	if !strings.HasPrefix(text, "rclone v") || !strings.Contains(text, "cmount") {
		return "", errors.New("rclone版本不支持本程序所需的Windows挂载，请使用随包版本")
	}
	return strings.TrimSpace(strings.SplitN(text, "\n", 2)[0]), nil
}
func probeWinFsp() (string, error) {
	component := "x64"
	suffix := "x64"
	if runtime.GOARCH == "arm64" {
		component, suffix = "ARM64", "a64"
	}
	for _, view := range []uint32{registry.WOW64_32KEY, registry.WOW64_64KEY} {
		key, e := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\WinFsp`, registry.QUERY_VALUE|view)
		if e != nil {
			continue
		}
		root, _, e := key.GetStringValue("InstallDir")
		key.Close()
		if e != nil {
			continue
		}
		dll := filepath.Join(root, "bin", "winfsp-"+suffix+".dll")
		handle, e := windows.LoadLibraryEx(dll, 0, windows.LOAD_LIBRARY_SEARCH_DLL_LOAD_DIR|windows.LOAD_LIBRARY_SEARCH_DEFAULT_DIRS)
		if e == nil {
			windows.FreeLibrary(handle)
			return "已检测到可加载的 " + component + " 组件", nil
		}
	}
	return "", errors.New("未检测到可用 WinFsp " + component + " 组件，请安装 WinFsp 后重新打开程序")
}
func probeWebView() (string, error) {
	version, e := webviewloader.GetAvailableCoreWebView2BrowserVersionString("")
	if e != nil || version == "" {
		return "", errors.New("未检测到WebView2 Runtime；请安装Microsoft Edge WebView2 Evergreen Runtime")
	}
	return version, nil
}
func checkDependenciesWith(engine string, webview, winfsp func() (string, error)) DependencyReport {
	report := DependencyReport{Version: appVersion, Ready: true}
	probes := []struct {
		name, url string
		run       func() (string, error)
	}{
		{"rclone", "https://rclone.org/downloads/", func() (string, error) { return probeRclone(engine) }},
		{"WinFsp", "https://github.com/winfsp/winfsp/releases", winfsp},
		{"WebView2", "https://developer.microsoft.com/microsoft-edge/webview2/", webview},
	}
	for _, probe := range probes {
		version, e := probe.run()
		component := Dependency{Name: probe.name, Ready: e == nil, Version: version, Message: "检查通过", HelpURL: probe.url}
		if e != nil {
			component.Message = e.Error()
			report.Ready = false
		}
		report.Components = append(report.Components, component)
	}
	return report
}
func checkDependencies(engine string) DependencyReport {
	return checkDependenciesWith(engine, probeWebView, probeWinFsp)
}
func (report DependencyReport) problemText() string {
	var lines []string
	for _, c := range report.Components {
		if !c.Ready {
			lines = append(lines, c.Name+"："+c.Message+"\n"+c.HelpURL)
		}
	}
	return strings.Join(lines, "\n\n")
}
