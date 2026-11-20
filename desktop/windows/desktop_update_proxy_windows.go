package desktop

import (
	"errors"
	"golang.org/x/sys/windows"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"unsafe"
)

type ieUpdateProxyConfig struct {
	AutoDetect    uint32
	AutoConfigURL *uint16
	Proxy         *uint16
	Bypass        *uint16
}

var updateWinHTTP = windows.NewLazySystemDLL("winhttp.dll")
var updateIEProxy = updateWinHTTP.NewProc("WinHttpGetIEProxyConfigForCurrentUser")
var updateGlobalFree = windows.NewLazySystemDLL("kernel32.dll").NewProc("GlobalFree")

func readWindowsUpdateProxy() (string, string, error) {
	if updateIEProxy.Find() != nil {
		return "", "", errors.New("Windows proxy settings unavailable")
	}
	var config ieUpdateProxyConfig
	result, _, callError := updateIEProxy.Call(uintptr(unsafe.Pointer(&config)))
	defer func() {
		for _, pointer := range []*uint16{config.Proxy, config.Bypass, config.AutoConfigURL} {
			if pointer != nil {
				updateGlobalFree.Call(uintptr(unsafe.Pointer(pointer)))
			}
		}
	}()
	if result == 0 {
		if callError == windows.ERROR_FILE_NOT_FOUND {
			return "", "", nil
		}
		return "", "", errors.New("Windows proxy settings unavailable")
	}
	proxy, bypass := "", ""
	if config.Proxy != nil {
		proxy = windows.UTF16PtrToString(config.Proxy)
	}
	if config.Bypass != nil {
		bypass = windows.UTF16PtrToString(config.Bypass)
	}
	// Adopt existing static settings only. This helper does not resolve PAC/WPAD,
	// transmit configuration URLs or automatically send Windows credentials.
	return proxy, bypass, nil
}

func windowsUpdateProxyURL(target *url.URL, configured, bypass string) (*url.URL, error) {
	host := strings.ToLower(target.Hostname())
	for _, pattern := range strings.FieldsFunc(bypass, func(r rune) bool { return r == ';' || r == ',' || r == ' ' }) {
		pattern = strings.ToLower(strings.TrimSpace(pattern))
		if pattern == "<local>" && !strings.ContainsAny(host, ".:") {
			return nil, nil
		}
		if pattern == host || pattern == strings.ToLower(target.Host) {
			return nil, nil
		}
		if matched, e := path.Match(pattern, host); e == nil && matched {
			return nil, nil
		}
	}
	server := ""
	for _, item := range strings.Split(configured, ";") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		key, value, hasEquals := strings.Cut(item, "=")
		mapping := hasEquals && (!strings.Contains(item, "://") || strings.Index(item, "=") < strings.Index(item, "://"))
		if mapping {
			if strings.EqualFold(strings.TrimSpace(key), target.Scheme) {
				server = strings.TrimSpace(value)
				break
			}
		} else if server == "" {
			server = item
		}
	}
	if server == "" {
		return nil, nil
	}
	if !strings.Contains(server, "://") {
		server = "http://" + server
	}
	parsed, e := url.Parse(server)
	if e != nil || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path != "" && parsed.Path != "/" || parsed.Scheme != "http" && parsed.Scheme != "https" && parsed.Scheme != "socks5" && parsed.Scheme != "socks5h" {
		return nil, errors.New("unsupported Windows update proxy configuration")
	}
	return parsed, nil
}

func newWindowsUpdateClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	explicitEnvironment := false
	for _, name := range []string{"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "no_proxy"} {
		if os.Getenv(name) != "" {
			explicitEnvironment = true
			break
		}
	}
	if !explicitEnvironment {
		configured, bypass, readError := readWindowsUpdateProxy()
		transport.Proxy = func(request *http.Request) (*url.URL, error) {
			if readError != nil {
				return nil, readError
			}
			return windowsUpdateProxyURL(request.URL, configured, bypass)
		}
	}
	return &http.Client{Transport: transport}
}
