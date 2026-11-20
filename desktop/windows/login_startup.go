package desktop

import (
	"errors"
	"golang.org/x/sys/windows/registry"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
)

const loginStartupKey = `Software\Microsoft\Windows\CurrentVersion\Run`

func loginStartupValue() string {
	if runtime.GOARCH == "arm64" {
		return "WebDAVMount-ARM64"
	}
	return "WebDAVMount"
}

type LoginStartupInfo struct {
	Available      bool   `json:"available"`
	Enabled        bool   `json:"enabled"`
	CurrentProgram bool   `json:"currentProgram"`
	Executable     string `json:"executable"`
	Warning        string `json:"warning"`
}

type loginStartupStore interface {
	Read() (string, error)
	Write(string) error
	Delete() error
}

type registryLoginStartup struct{ key string }

func (r registryLoginStartup) Read() (string, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, r.key, registry.QUERY_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	defer k.Close()
	value, _, err := k.GetStringValue(loginStartupValue())
	if errors.Is(err, registry.ErrNotExist) {
		return "", nil
	}
	return value, err
}
func (r registryLoginStartup) Write(value string) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, r.key, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	return k.SetStringValue(loginStartupValue(), value)
}
func (r registryLoginStartup) Delete() error {
	k, err := registry.OpenKey(registry.CURRENT_USER, r.key, registry.SET_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer k.Close()
	err = k.DeleteValue(loginStartupValue())
	if errors.Is(err, registry.ErrNotExist) {
		return nil
	}
	return err
}

type loginStartupController struct {
	mu         sync.Mutex
	executable string
	available  bool
	store      loginStartupStore
}

func loginStartupCommand(executable string) (string, error) {
	if !filepath.IsAbs(executable) || strings.ContainsAny(executable, "\"\r\n") {
		return "", errors.New("启动程序路径无效")
	}
	command := `"` + filepath.Clean(executable) + `"`
	if len(utf16.Encode([]rune(command))) > 260 {
		return "", errors.New("当前程序路径过长，请移动到较短的固定目录后再启用")
	}
	return command, nil
}

func (c *loginStartupController) infoLocked() (LoginStartupInfo, error) {
	info := LoginStartupInfo{Available: c.available, Executable: c.executable}
	if !c.available {
		info.Warning = "当前使用独立测试配置，不能修改 Windows 登录启动项"
		return info, nil
	}
	command, err := c.store.Read()
	if err != nil {
		return info, errors.New("无法读取登录启动设置，请检查当前用户权限")
	}
	info.Enabled = command != ""
	expected, err := loginStartupCommand(c.executable)
	info.CurrentProgram = err == nil && strings.EqualFold(strings.TrimSpace(command), expected)
	if info.Enabled && !info.CurrentProgram {
		info.Warning = "启动项指向其他位置；保持勾选并保存，将更新为当前程序路径"
	}
	return info, nil
}
func (c *loginStartupController) Info() (LoginStartupInfo, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.infoLocked()
}
func (c *loginStartupController) Save(enabled bool) (LoginStartupInfo, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.available {
		return LoginStartupInfo{}, errors.New("独立测试配置不能修改 Windows 登录启动项")
	}
	var err error
	if enabled {
		command, e := loginStartupCommand(c.executable)
		if e != nil {
			return LoginStartupInfo{}, e
		}
		if stat, e := os.Stat(c.executable); e != nil || stat.IsDir() {
			return LoginStartupInfo{}, errors.New("当前程序文件不可用，无法启用登录启动")
		}
		err = c.store.Write(command)
	} else {
		err = c.store.Delete()
	}
	if err != nil {
		return LoginStartupInfo{}, errors.New("保存登录启动设置失败，请检查当前用户权限")
	}
	return c.infoLocked()
}

func configureLoginStartup(s *DesktopService, executable string, available bool) {
	s.loginStartup = &loginStartupController{executable: executable, available: available, store: registryLoginStartup{key: loginStartupKey}}
}
func (s *DesktopService) LoginStartupInfo() (LoginStartupInfo, error) {
	if s.loginStartup == nil {
		return LoginStartupInfo{Warning: "当前环境未配置登录启动设置"}, nil
	}
	return s.loginStartup.Info()
}
func (s *DesktopService) SaveLoginStartup(enabled bool) (info LoginStartupInfo, err error) {
	started := time.Now()
	defer func() { s.recordDiagnostic("startup.save", err, time.Since(started)) }()
	if s.loginStartup == nil {
		return info, errors.New("当前环境不能修改登录启动设置")
	}
	return s.loginStartup.Save(enabled)
}
