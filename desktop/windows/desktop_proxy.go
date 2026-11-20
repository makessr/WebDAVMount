package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type ProxySettings struct {
	Mode     string `json:"mode"`
	Protocol string `json:"protocol"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password,omitempty"`
	Bypass   string `json:"bypass"`
}

type ProxyInfo struct {
	Settings    ProxySettings `json:"settings"`
	HasPassword bool          `json:"hasPassword"`
	Pending     bool          `json:"pending"`
	Warning     string        `json:"warning,omitempty"`
}

type engineProxy struct {
	settings    ProxySettings
	http, https string
	bypass      string
	warning     string
}

var proxyHostname = regexp.MustCompile(`(?i)^[a-z0-9](?:[a-z0-9_.-]{0,251}[a-z0-9])?\.?$`)

func splitProxyBypass(list string) []string {
	return strings.FieldsFunc(list, func(r rune) bool { return r == ',' || r == ';' || r == ' ' })
}

func normalizeProxyBypass(list string) (string, error) {
	if len(list) > 4096 {
		return "", errors.New("不经代理的地址列表过长")
	}
	items := splitProxyBypass(list)
	for _, item := range items {
		host := strings.TrimPrefix(strings.TrimPrefix(item, "*."), ".")
		if _, _, err := net.ParseCIDR(host); err == nil {
			continue
		}
		if net.ParseIP(host) == nil && !proxyHostname.MatchString(host) {
			return "", errors.New("不经代理的地址请填写主机名、IP、网段或 *.域名")
		}
	}
	return strings.Join(items, ","), nil
}

func normalizeProxySettings(config ProxySettings) (ProxySettings, error) {
	switch config.Mode {
	case "environment", "direct", "system":
		return ProxySettings{Mode: config.Mode}, nil
	case "manual":
	default:
		return ProxySettings{}, errors.New("请选择有效的代理方式")
	}
	switch config.Protocol {
	case "http", "https", "socks5":
	default:
		return ProxySettings{}, errors.New("请选择 HTTP、HTTPS 或 SOCKS5 代理")
	}
	config.Host = strings.TrimSpace(config.Host)
	if strings.HasPrefix(config.Host, "[") && strings.HasSuffix(config.Host, "]") {
		config.Host = config.Host[1 : len(config.Host)-1]
	}
	if (!proxyHostname.MatchString(config.Host) && net.ParseIP(config.Host) == nil) || config.Port < 1 || config.Port > 65535 {
		return ProxySettings{}, errors.New("请填写代理主机名或 IP，以及 1 至 65535 的端口")
	}
	if (config.Username == "") != (config.Password == "") || len(config.Username) > 255 || len(config.Password) > 255 ||
		strings.ContainsAny(config.Username+config.Password, "\r\n\x00") || strings.Contains(config.Username, ":") {
		return ProxySettings{}, errors.New("代理认证需要同时填写有效的用户名和密码")
	}
	bypass, err := normalizeProxyBypass(config.Bypass)
	config.Bypass = bypass
	return config, err
}

func resolveEngineProxy(config ProxySettings, system func() (string, string, error)) (engineProxy, error) {
	result := engineProxy{settings: config}
	switch config.Mode {
	case "manual":
		target := url.URL{Scheme: config.Protocol, Host: net.JoinHostPort(config.Host, strconv.Itoa(config.Port))}
		if config.Username != "" {
			target.User = url.UserPassword(config.Username, config.Password)
		}
		result.http, result.https, result.bypass = target.String(), target.String(), config.Bypass
	case "system":
		configured, bypass, err := system()
		if err != nil {
			return result, errors.New("无法读取 Windows 系统代理")
		}
		for _, scheme := range []string{"http", "https"} {
			target, err := windowsUpdateProxyURL(&url.URL{Scheme: scheme, Host: "example.invalid"}, configured, "")
			if err != nil {
				return result, errors.New("Windows 系统代理格式不可用，请检查系统设置或改用手动代理")
			}
			if target == nil {
				continue
			}
			if scheme == "http" {
				result.http = target.String()
			} else {
				result.https = target.String()
			}
		}
		if result.http == "" && result.https == "" {
			return result, errors.New("未检测到 Windows 手动代理。若系统使用自动脚本，请改用手动代理")
		}
		result.bypass, err = normalizeProxyBypass(bypass)
		if err != nil {
			return result, errors.New("无法保留 Windows 代理的绕过规则，请改用手动代理并填写不经代理的地址")
		}
	}
	return result, nil
}

func isProxyEnvironment(key string) bool {
	switch strings.ToUpper(key) {
	case "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "ALL_PROXY", "FTP_PROXY", "REQUEST_METHOD":
		return true
	}
	return false
}

func (p engineProxy) environment(inherited []string) []string {
	env := make([]string, 0, len(inherited)+3)
	legacy := p.settings.Mode == "" || p.settings.Mode == "environment"
	for _, entry := range inherited {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(strings.ToUpper(key), "RCLONE_") || !legacy && isProxyEnvironment(key) {
			continue
		}
		env = append(env, entry)
	}
	if !legacy {
		env = append(env, "HTTP_PROXY="+p.http, "HTTPS_PROXY="+p.https, "NO_PROXY="+p.bypass)
	}
	return env
}

func (p engineProxy) httpsTransport() *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if p.settings.Mode == "" || p.settings.Mode == "environment" {
		return transport
	}
	transport.Proxy = func(req *http.Request) (*url.URL, error) {
		host := strings.ToLower(req.URL.Hostname())
		for _, entry := range splitProxyBypass(strings.ToLower(p.bypass)) {
			domain := strings.TrimPrefix(strings.TrimPrefix(entry, "*."), ".")
			if strings.HasSuffix(host, "."+domain) || (host == domain && entry == domain) {
				return nil, nil
			}
		}
		if p.https == "" {
			return nil, nil
		}
		return url.Parse(p.https)
	}
	return transport
}

var errProxyAuthentication = errors.New("代理要求认证，请检查网盘代理的用户名和密码")

func (s *DesktopService) ProbeMEGANetwork() (string, error) {
	ctx, finish, err := s.beginOperation("proxy.probe")
	if err != nil {
		return "", err
	}
	defer finish()
	s.mu.Lock()
	proxy := s.proxy
	s.mu.Unlock()
	if proxy.warning != "" {
		return "", errors.New(proxy.warning)
	}
	return proxy.probeMEGA(ctx)
}

func (p engineProxy) probeMEGA(ctx context.Context) (string, error) {
	transport := p.httpsTransport()
	defer transport.CloseIdleConnections()
	transport.OnProxyConnectResponse = func(_ context.Context, _ *url.URL, _ *http.Request, response *http.Response) error {
		if response.StatusCode == http.StatusProxyAuthRequired {
			return errProxyAuthentication
		}
		return nil
	}
	client := &http.Client{
		Transport: transport, Timeout: 8 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodHead, "https://g.api.mega.co.nz/", nil)
	if err != nil {
		return "", errors.New("无法创建 MEGA 网络测试")
	}
	response, err := client.Do(request)
	if err != nil {
		switch {
		case ctx.Err() != nil:
			return "", errors.New("已取消 MEGA 网络测试")
		case errors.Is(err, errProxyAuthentication):
			return "", errProxyAuthentication
		default:
			result := diagnosticError(http.StatusBadGateway, err.Error())
			if strings.Contains(result.Error(), "HTTP 502") {
				result = errors.New("无法连接 MEGA API，请检查当前网络和代理")
			}
			return "", result
		}
	}
	defer response.Body.Close()
	return "已连通 MEGA API（HTTP " + strconv.Itoa(response.StatusCode) + "）。可回到网盘测试登录和目录读取。", nil
}

func (s *DesktopService) readProxySettings() (ProxySettings, error) {
	path := filepath.Join(s.dir, "proxy-settings.enc")
	if err := noReparse(path); err != nil {
		return ProxySettings{}, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return ProxySettings{Mode: "environment"}, nil
	}
	if err != nil || len(data) > 64<<10 {
		return ProxySettings{}, errors.New("网盘代理设置无法读取")
	}
	plain, err := crypt(data, true)
	var config ProxySettings
	if err != nil || json.Unmarshal(plain, &config) != nil {
		return ProxySettings{}, errors.New("网盘代理设置无法解密")
	}
	return normalizeProxySettings(config)
}

func (s *DesktopService) loadEngineProxy() {
	config, err := s.readProxySettings()
	if err == nil {
		s.proxy, err = resolveEngineProxy(config, readWindowsUpdateProxy)
	}
	if err != nil {
		// Keep settings accessible without silently switching to a direct connection.
		const unavailable = "http://127.0.0.1:0"
		s.proxy = engineProxy{settings: ProxySettings{Mode: "unavailable"}, http: unavailable, https: unavailable, warning: err.Error() + "。请重新保存网盘代理设置。"}
	}
}

func (s *DesktopService) ProxyInfo() ProxyInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.proxyInfoLocked()
}

func (s *DesktopService) proxyInfoLocked() ProxyInfo {
	config, err := s.readProxySettings()
	info := ProxyInfo{Settings: config, Warning: s.proxy.warning}
	if err != nil {
		info.Settings.Mode = "environment"
		info.Warning = "已保存的代理设置无法读取，请重新选择代理方式并保存"
		return info
	}
	info.Pending = config != s.proxy.settings
	info.HasPassword = config.Password != ""
	info.Settings.Password = ""
	return info
}

func (s *DesktopService) SaveProxySettings(config ProxySettings) (ProxyInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if config.Mode == "manual" && config.Username != "" && config.Password == "" {
		old, err := s.readProxySettings()
		if err == nil && old.Mode == "manual" && old.Protocol == config.Protocol && old.Host == strings.TrimSpace(config.Host) && old.Port == config.Port && old.Username == config.Username {
			config.Password = old.Password
		}
	}
	config, err := normalizeProxySettings(config)
	if err != nil {
		return ProxyInfo{}, err
	}
	if _, err := resolveEngineProxy(config, readWindowsUpdateProxy); err != nil {
		return ProxyInfo{}, err
	}
	plain, err := json.Marshal(config)
	if err != nil {
		return ProxyInfo{}, errors.New("无法编码网盘代理设置")
	}
	data, err := crypt(plain, false)
	if err != nil || atomicWriteSettings(filepath.Join(s.dir, "proxy-settings.enc"), data) != nil {
		return ProxyInfo{}, errors.New("无法保存网盘代理设置")
	}
	return s.proxyInfoLocked(), nil
}
