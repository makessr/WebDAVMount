package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"webdavmount/internal/providers"
)

type cloudLoginSession struct {
	id, remote, provider, state, site string
	clientID, clientSecret            string
	expires                           time.Time
	options                           []CloudLoginChoice
	ready                             bool
	proxy                             *ProxySettings
	engineProxy                       string
	profileID                         string
}

type CloudLoginChoice struct {
	Value string `json:"value"`
	Title string `json:"title"`
}

type CloudLoginStep struct {
	Ticket  string             `json:"ticket"`
	Ready   bool               `json:"ready"`
	Choices []CloudLoginChoice `json:"choices"`
}

type engineCloudStep struct {
	State, Error string
	Option       *struct {
		Name     string
		Examples []struct{ Value, Help string }
	}
}

var sharePointHost = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}\.sharepoint\.com$`)
var graphSiteID = regexp.MustCompile(`^[a-z0-9-]+\.sharepoint\.com,[A-Fa-f0-9-]{36},[A-Fa-f0-9-]{36}$`)

func validSharePointSite(site string) bool {
	u, err := url.Parse(site)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" ||
		u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(site, "#") ||
		!sharePointHost.MatchString(u.Hostname()) {
		return false
	}
	path := strings.Trim(u.Path, "/")
	return (strings.HasPrefix(path, "sites/") || strings.HasPrefix(path, "teams/")) &&
		strings.Count(path, "/") == 1 && len(path) <= 200 && !strings.HasSuffix(path, "/.") && !strings.HasSuffix(path, "/..")
}

func (s *DesktopService) discardCloudLoginLocked() {
	if s.cloudLogin == nil {
		return
	}
	remote := s.cloudLogin.remote
	s.cloudLogin = nil
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _ = s.callContext(ctx, "config/delete", map[string]any{"name": remote})
}

func (s *DesktopService) DiscardCloudLogin(ticket string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cloudLogin != nil && s.cloudLogin.id == ticket {
		s.discardCloudLoginLocked()
	}
}

// AuthorizeCloud uses rclone's non-interactive configuration state machine.
// Only server-held states and a small set of known questions may be answered.
// Raw config, provider errors and tokens never reach the frontend.
func (s *DesktopService) AuthorizeCloud(provider, ticket, answer, site, clientID, clientSecret string) (CloudLoginStep, error) {
	return s.AuthorizeCloudWithProxy(provider, ticket, answer, site, clientID, clientSecret, "", nil)
}

func (s *DesktopService) AuthorizeCloudWithProxy(provider, ticket, answer, site, clientID, clientSecret, profileID string, input *ProxySettings) (CloudLoginStep, error) {
	op, finish, err := s.beginOperation("cloud.authorize")
	if err != nil {
		return CloudLoginStep{}, err
	}
	defer finish()
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.connectionManagementReadyLocked(); err != nil {
		return CloudLoginStep{}, err
	}
	ctx, cancel := context.WithTimeout(op, 3*time.Minute)
	defer cancel()
	fail := func() (CloudLoginStep, error) {
		s.discardCloudLoginLocked()
		if op.Err() != nil {
			return CloudLoginStep{}, errors.New("已取消网盘授权")
		}
		switch provider {
		case providers.Workspace:
			return CloudLoginStep{}, errors.New("Workspace 授权未完成。请确认组织已允许此应用，并且当前账号拥有共享云端硬盘访问权限")
		case providers.SharePoint:
			return CloudLoginStep{}, errors.New("SharePoint 授权未完成。请确认工作账号、站点地址、文档库权限和组织审批")
		case providers.Microsoft365:
			return CloudLoginStep{}, errors.New("Microsoft 365 授权未完成。请使用工作或学校账号，并确认组织已批准访问")
		default:
			return CloudLoginStep{}, errors.New("网盘授权未完成，请检查浏览器登录、账户权限及网络后重试")
		}
	}
	var raw map[string]any
	if ticket == "" {
		if !providers.IsOAuth(provider) || (provider == providers.SharePoint && !validSharePointSite(site)) {
			return CloudLoginStep{}, errors.New("请选择支持浏览器授权的类型，SharePoint 需填写完整站点 URL")
		}
		backend, params, err := providers.OAuthConfig(provider, "", "", "")
		if err != nil {
			return CloudLoginStep{}, err
		}
		var previous *ProxySettings
		if profileID != "" {
			p, err := s.find(profileID)
			if err != nil || p.Provider != provider {
				return CloudLoginStep{}, errors.New("待授权连接已变化，请重新打开编辑窗口")
			}
			previous = p.Proxy
		}
		proxy, err := s.normalizeConnectionProxy(provider, input, previous)
		if err != nil {
			return CloudLoginStep{}, err
		}
		if err = s.applyConnectionProxy(Profile{Provider: provider, Proxy: proxy}, params); err != nil {
			return CloudLoginStep{}, err
		}
		if providers.IsGoogle(provider) {
			if !providers.ValidGoogleClient(clientID, clientSecret) {
				return CloudLoginStep{}, errors.New("请填写 Google 桌面应用 Client ID 和 Client Secret")
			}
			params["client_id"], params["client_secret"] = clientID, clientSecret
		} else {
			clientID, clientSecret = "", ""
		}
		s.discardCloudLoginLocked()
		s.cloudLogin = &cloudLoginSession{id: randomID(), remote: "login" + randomID(), provider: provider,
			site: site, clientID: clientID, clientSecret: clientSecret, expires: time.Now().Add(10 * time.Minute),
			proxy: proxy, engineProxy: params["override.http_proxy"], profileID: profileID}
		request := map[string]any{"name": s.cloudLogin.remote, "type": backend, "parameters": params,
			"opt": map[string]bool{"nonInteractive": true}}
		s.cloudLogin.addProxyContext(request)
		raw, err = s.callContext(ctx, "config/create", request)
		if err != nil {
			return fail()
		}
	} else {
		login := s.cloudLogin
		if login == nil || login.id != ticket || login.provider != provider || time.Now().After(login.expires) || login.ready {
			return CloudLoginStep{}, errors.New("授权会话已失效，请重新开始")
		}
		valid := false
		for _, option := range login.options {
			valid = valid || option.Value == answer
		}
		if !valid {
			return CloudLoginStep{}, errors.New("请选择当前列表中的驱动器")
		}
		raw, err = s.continueCloudLogin(ctx, login, answer)
		if err != nil {
			return fail()
		}
	}

	login := s.cloudLogin
	for range 12 {
		var step engineCloudStep
		data, err := json.Marshal(raw)
		if err != nil || json.Unmarshal(data, &step) != nil || step.Error != "" {
			return fail()
		}
		if step.State == "" && step.Option == nil {
			if _, _, err := s.cloudGrant(ctx, login); err != nil {
				return fail()
			}
			login.ready = true
			return CloudLoginStep{Ticket: login.id, Ready: true}, nil
		}
		if step.State == "" || step.Option == nil || len(step.State) > 4096 {
			return fail()
		}
		login.state = step.State
		switch step.Option.Name {
		case "config_is_local", "config_drive_ok":
			answer = "true"
		case "config_change_team_drive":
			answer = "false"
			if provider == providers.Workspace {
				answer = "true"
			}
		case "config_type":
			answer = "onedrive"
			if provider == providers.SharePoint {
				answer = "siteid"
			}
		case "config_siteid":
			if provider != providers.SharePoint {
				return fail()
			}
			answer, err = s.resolveSharePointSite(ctx, login)
			if err != nil {
				return fail()
			}
		case "config_driveid", "config_team_drive":
			login.options = nil
			for _, option := range step.Option.Examples {
				if option.Value != "" && len(option.Value) <= 256 && len(option.Help) <= 512 {
					login.options = append(login.options, CloudLoginChoice{option.Value, option.Help})
				}
				if len(login.options) == 200 {
					break
				}
			}
			if len(login.options) == 0 {
				return fail()
			}
			return CloudLoginStep{Ticket: login.id, Choices: login.options}, nil
		default:
			return fail()
		}
		raw, err = s.continueCloudLogin(ctx, login, answer)
		if err != nil {
			return fail()
		}
	}
	return fail()
}

func (s *DesktopService) resolveSharePointSite(ctx context.Context, login *cloudLoginSession) (string, error) {
	form, err := s.callContext(ctx, "config/get", map[string]any{"name": login.remote})
	if err != nil {
		return "", err
	}
	var token struct {
		Access string `json:"access_token"`
	}
	raw, _ := form["token"].(string)
	if !providers.ValidOAuthToken(raw) || json.Unmarshal([]byte(raw), &token) != nil {
		return "", errors.New("Microsoft 授权无效")
	}
	site, _ := url.Parse(login.site)
	endpoint := url.URL{Scheme: "https", Host: "graph.microsoft.com", Path: "/v1.0/sites/" + site.Hostname() + ":" + strings.TrimRight(site.Path, "/"), RawQuery: "%24select=id"}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token.Access)
	route := s.proxy
	if login.proxy != nil {
		route, err = resolveEngineProxy(*login.proxy, nil)
		if err != nil {
			return "", err
		}
	}
	transport := route.httpsTransport()
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	result, err := decodeEngineResponse(resp.Body, 64<<10)
	id, _ := result["id"].(string)
	if err != nil || resp.StatusCode != 200 || !graphSiteID.MatchString(id) || !strings.HasPrefix(id, site.Hostname()+",") {
		return "", errors.New("无法确认所选 SharePoint 站点")
	}
	return id, nil
}

func (s *DesktopService) continueCloudLogin(ctx context.Context, login *cloudLoginSession, answer string) (map[string]any, error) {
	request := map[string]any{"name": login.remote, "parameters": map[string]string{},
		"opt": map[string]any{"nonInteractive": true, "continue": true, "state": login.state, "result": answer}}
	login.addProxyContext(request)
	return s.callContext(ctx, "config/update", request)
}

func (login *cloudLoginSession) addProxyContext(request map[string]any) {
	if login.engineProxy != "" {
		request["_config"] = map[string]string{"HTTPProxy": login.engineProxy}
	}
}

func (s *DesktopService) cloudGrant(ctx context.Context, login *cloudLoginSession) (token, resource string, err error) {
	raw, err := s.callContext(ctx, "config/get", map[string]any{"name": login.remote})
	if err != nil {
		return "", "", err
	}
	token, _ = raw["token"].(string)
	switch login.provider {
	case providers.Workspace:
		resource, _ = raw["team_drive"].(string)
	case providers.OneDrive, providers.Microsoft365, providers.SharePoint:
		id, _ := raw["drive_id"].(string)
		kind, _ := raw["drive_type"].(string)
		resource = kind + ":" + id
	}
	if !providers.ValidOAuthToken(token) {
		return "", "", errors.New("未获得可续期的网盘授权")
	}
	credential := (providers.CloudGrant{Token: token, ClientID: login.clientID, ClientSecret: login.clientSecret}).Encode()
	if _, _, err := providers.OAuthConfig(login.provider, "", resource, credential); err != nil {
		return "", "", err
	}
	return credential, resource, nil
}

func (s *DesktopService) SaveCloudConnection(p Profile, ticket string) (Profile, error) {
	ctx, finish, err := s.beginOperation("cloud.save")
	if err != nil {
		return Profile{}, err
	}
	defer finish()
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.connectionManagementReadyLocked(); err != nil {
		return Profile{}, err
	}
	login := s.cloudLogin
	if login == nil || login.id != ticket || login.provider != p.Provider || !login.ready || time.Now().After(login.expires) ||
		login.profileID != "" && login.profileID != p.ID {
		return Profile{}, errors.New("授权已失效，请重新授权")
	}
	token, resource, err := s.cloudGrant(ctx, login)
	if err != nil {
		return Profile{}, errors.New("授权状态已变化，请重新授权")
	}
	p.Password, p.User, p.URL, p.HostKey, p.ClearPassword = token, resource, "", "", false
	p.Proxy = login.proxy
	result, err := s.saveProfileLocked(p)
	if err == nil {
		s.discardCloudLoginLocked()
	}
	return result, err
}

func (s *DesktopService) persistRenewedCloudTokens() {
	if !s.mu.TryLock() {
		return
	}
	defer s.mu.Unlock()
	if s.cloudLogin != nil && time.Now().After(s.cloudLogin.expires) {
		s.discardCloudLoginLocked()
	}
	s.persistRenewedCloudTokensLocked()
}

func (s *DesktopService) latestCloudCredential(ctx context.Context, p Profile) string {
	grant, valid := providers.DecodeGrant(p.Password)
	if !valid {
		return p.Password
	}
	data, err := s.callContext(ctx, "config/get", map[string]any{"name": p.ID})
	token, _ := data["token"].(string)
	if err != nil || token == grant.Token || !providers.ValidOAuthToken(token) {
		return p.Password
	}
	grant.Token = token
	return grant.Encode()
}

func (s *DesktopService) persistRenewedCloudTokensLocked() {
	if !s.engineOnline() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	next := append([]Profile(nil), s.profiles...)
	changed := false
	for i, p := range next {
		if ctx.Err() != nil {
			break
		}
		if !providers.IsOAuth(p.Provider) || p.Password == "" {
			continue
		}
		if credential := s.latestCloudCredential(ctx, p); credential != p.Password {
			next[i].Password = credential
			changed = true
		}
	}
	if changed && s.persist(next) == nil {
		s.profiles = next
	}
}

func (s *DesktopService) startCloudTokenMonitor() {
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-s.lifeCtx.Done():
				return
			case <-ticker.C:
				s.persistRenewedCloudTokens()
			}
		}
	}()
}
