package desktop

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Never expose raw backend text: it may contain addresses or credentials.
type remoteAccessError struct {
	message    string
	stopSearch bool
}

func (e remoteAccessError) Error() string { return e.message }

// The RC endpoint often wraps upstream failures in HTTP 500. Recognize the
// status positions used by rclone and its SDKs rather than arbitrary digits
// in a host, filename, request ID, or response body.
var remoteHTTPStatus = regexp.MustCompile(`(?:^|: +)(?:\[|http(?:/\d(?:\.\d)?| error| status(?: code)?)? +|response +|error +)?([45]\d{2})(?:$|[ ,:(\]])|\bstatus ?code: *([45]\d{2})(?:$|[ ,:(])`)

func upstreamStatus(status int, detail string) int {
	if status >= 400 && status < 500 {
		return status
	}
	if match := remoteHTTPStatus.FindStringSubmatch(detail); match != nil {
		code, _ := strconv.Atoi(match[1] + match[2])
		return code
	}
	return 0
}

func diagnosticError(status int, detail string) error {
	text := strings.TrimSpace(strings.ToLower(detail))
	upstream := upstreamStatus(status, text)
	switch {
	case strings.Contains(text, "proxy authentication required") || strings.Contains(text, "proxy status 407"):
		return errProxyAuthentication
	case strings.Contains(text, "proxyconnect") || strings.Contains(text, "socks connect"):
		return errors.New("代理连接失败，请检查网盘代理地址、端口和服务状态")
	case strings.Contains(text, "multi-factor authentication required"):
		return errors.New("MEGA 要求双重验证。编辑网盘，填入当前六位验证码并保存")
	case strings.Contains(text, "login with previous auth keys failed"):
		return errors.New("MEGA 会话无法恢复。请检查网络，或编辑网盘并重新验证")
	case upstream == 401 || upstream == 403 || upstream == 0 && (text == "unauthorized" || strings.HasSuffix(text, ": unauthorized")):
		auth := upstream != 403
		return remoteAccessError{"账号或应用密码无效，或账号没有访问权限；请检查连接配置", auth}
	case upstream == 429 || upstream == 0 && (text == "too many requests" || strings.HasSuffix(text, ": too many requests")):
		return errors.New("网盘请求过于频繁，请稍后重试")
	case strings.Contains(text, "x509") || strings.Contains(text, "certificate"):
		return errors.New("TLS 证书验证失败，请检查网盘地址、证书和系统时间")
	case strings.Contains(text, "timeout") || strings.Contains(text, "deadline"):
		return errors.New("远端连接超时，请检查网络或稍后重试")
	case strings.Contains(text, "no such host") || strings.Contains(text, "connection refused") || strings.Contains(text, "actively refused") || strings.Contains(text, "network is unreachable"):
		return errors.New("无法连接网盘，请检查网络、域名或服务是否运行")
	case strings.Contains(text, "couldn't login"):
		return errors.New("网盘登录失败。请检查账号和密码，或到「设置 → 网盘代理」测试 MEGA 网络")
	default:
		return fmt.Errorf("操作失败（HTTP %d），请检查地址、网盘权限和网络后重试", status)
	}
}

func (s *DesktopService) engineOnline() bool {
	select {
	case <-s.done:
		return false
	default:
		return true
	}
}

func copyStatus(input map[string]string) map[string]string {
	output := make(map[string]string, len(input))
	for k, v := range input {
		output[k] = v
	}
	return output
}

// Explicit recovery restores only connections that mounted successfully this session.
func (s *DesktopService) Recover() (map[string]string, error) {
	ctx, finish, err := s.beginOperation("recovery")
	if err != nil {
		return nil, err
	}
	defer finish()
	s.startupMu.Lock()
	starting := s.startupActive
	s.startupMu.Unlock()
	if starting {
		return nil, errors.New("请等待启动挂载完成后重试")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	result, err := s.recoverLocked(ctx)
	if err == nil && recoverySucceeded(result) {
		s.settleManualRecoveryLocked("手动恢复已完成")
	}
	return result, err
}

func (s *DesktopService) settleManualRecoveryLocked(message string) {
	if s.autoRecovery == nil {
		return
	}
	s.autoRecovery.mu.Lock()
	defer s.autoRecovery.mu.Unlock()
	s.autoRecovery.retry = recoveryRetry{}
	s.autoRecovery.message = message
}

func (s *DesktopService) recoverLocked(ctx context.Context) (map[string]string, error) {
	return s.recoverSelectionLocked(ctx, nil)
}

// A nil selection retains the legacy recover-all behavior. An empty selection
// explicitly starts only the engine.
func (s *DesktopService) recoverSelectionLocked(ctx context.Context, selected map[string]bool) (map[string]string, error) {
	if ctx.Err() != nil {
		return nil, errors.New("操作已取消")
	}
	if !s.engineOnline() {
		next, err := newDesktop(s.dir, s.cmd.Path, s.sessionLog())
		if err != nil {
			return nil, err
		}
		if ctx.Err() != nil {
			next.close()
			return nil, errors.New("操作已取消")
		}
		s.adoptEngine(next)
		s.retiredDrives = nil
	}
	if selected != nil && len(selected) == 0 {
		s.recovery = map[string]string{}
		return s.recovery, nil
	}
	mounts, err := s.callContext(ctx, "mount/listmounts", nil)
	if err != nil {
		return nil, err
	}
	mounted := make(map[string]bool)
	identified, err := identifiedMounts(mounts, s.profiles)
	if err != nil {
		return nil, err
	}
	for _, m := range identified {
		mounted[m["ProfileID"].(string)] = true
	}
	result := make(map[string]string)
	for _, p := range s.profiles {
		if ctx.Err() != nil {
			s.recovery = result
			return result, errors.New("操作已取消")
		}
		if (selected == nil && !s.desired[p.ID]) || (selected != nil && !selected[p.ID]) {
			continue
		}
		if mounted[p.ID] {
			result[p.ID] = "盘符保持挂载；请测试连接确认远端可访问"
			continue
		}
		err = s.mountLocked(context.Background(), p.ID)
		if err != nil {
			result[p.ID] = "恢复失败：" + err.Error()
		} else {
			result[p.ID] = "已恢复挂载"
		}
	}
	s.recovery = result
	return result, nil
}
