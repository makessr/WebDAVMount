package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"time"
	"webdavmount/internal/updatefeed"
)

// Trust configuration is supplied by the release build, never by profile or
// runtime JSON. Nil is the current trial build's intentional offline default.
type DesktopUpdatePolicy struct {
	FeedURL       string
	Policy        updatefeed.Policy
	MirrorOrigins []string
}
type UpdateState struct {
	Configured       bool     `json:"configured"`
	State            string   `json:"state"`
	Message          string   `json:"message"`
	CurrentVersion   string   `json:"currentVersion"`
	AvailableVersion string   `json:"availableVersion"`
	DownloadedPath   string   `json:"downloadedPath"`
	Cancelled        bool     `json:"cancelled,omitempty"`
	DownloadedBytes  int64    `json:"downloadedBytes"`
	TotalBytes       int64    `json:"totalBytes"`
	BytesPerSecond   float64  `json:"bytesPerSecond"`
	ElapsedSeconds   float64  `json:"elapsedSeconds"`
	RemainingSeconds *float64 `json:"remainingSeconds,omitempty"`
	ResumeAvailable  bool     `json:"resumeAvailable"`
	ResumedBytes     int64    `json:"resumedBytes"`
	Connections      int      `json:"connections"`
	RetryCount       int      `json:"retryCount"`
	DownloadMode     string   `json:"downloadMode"`
	NetworkBytes     int64    `json:"networkBytes"`
	startedAt        time.Time
	sampleAt         time.Time
	lastByteAt       time.Time
	sampleBytes      int64
}

func (s *DesktopService) updateTrust() (*DesktopUpdatePolicy, *http.Client) {
	s.updateMu.Lock()
	defer s.updateMu.Unlock()
	if s.updatePolicy == nil {
		return nil, nil
	}
	p := *s.updatePolicy
	p.Policy.PublicKey = append([]byte{}, p.Policy.PublicKey...)
	p.Policy.AllowedHosts = append([]string{}, p.Policy.AllowedHosts...)
	p.MirrorOrigins = append([]string{}, p.MirrorOrigins...)
	p.Policy.CurrentVersion = appVersion
	p.Policy.Now = time.Now()
	if s.updateNow != nil {
		p.Policy.Now = s.updateNow()
	}
	var client *http.Client
	if s.updateClient != nil {
		copyClient := *s.updateClient
		client = &copyClient
	} else {
		client = newWindowsUpdateClient()
	}
	client.Jar = nil
	client.CheckRedirect = updatefeed.CheckReleaseRedirect
	return &p, client
}
func (s *DesktopService) UpdateInfo() UpdateState {
	s.updateMu.Lock()
	defer s.updateMu.Unlock()
	if s.updatePolicy == nil {
		return UpdateState{State: "unconfigured", Message: "在线更新尚未开放，请使用安装包更新", CurrentVersion: appVersion}
	}
	state := s.updateState
	if !state.startedAt.IsZero() && (state.State == "downloading" || state.State == "verifying") {
		now := time.Now()
		state.ElapsedSeconds = now.Sub(state.startedAt).Seconds()
		if state.lastByteAt.IsZero() || now.Sub(state.lastByteAt) > 3*time.Second || state.State == "verifying" {
			state.BytesPerSecond = 0
		}
		if state.BytesPerSecond > 0 && state.DownloadedBytes < state.TotalBytes {
			remaining := float64(state.TotalBytes-state.DownloadedBytes) / state.BytesPerSecond
			state.RemainingSeconds = &remaining
		}
	}
	state.Configured = true
	state.CurrentVersion = appVersion
	if state.State == "" {
		state.State = "idle"
		state.Message = "点击检查更新；程序不会自动下载或安装"
	}
	return state
}
func (s *DesktopService) setUpdateState(state UpdateState) UpdateState {
	s.updateMu.Lock()
	defer s.updateMu.Unlock()
	state.Configured = s.updatePolicy != nil
	state.CurrentVersion = appVersion
	s.updateState = state
	return state
}
func (s *DesktopService) updateFailure(ctx context.Context, message string) (UpdateState, error) {
	state := "error"
	if ctx.Err() != nil {
		prior := s.UpdateInfo()
		prior.Cancelled = true
		prior.Message = "操作已取消"
		if prior.AvailableVersion != "" {
			prior.State = "available"
		} else {
			prior.State = "idle"
		}
		return s.setUpdateState(prior), errors.New("操作已取消")
	}
	result := s.setUpdateState(UpdateState{State: state, Message: message})
	return result, errors.New(message)
}
func (s *DesktopService) CheckForUpdates() (result UpdateState, err error) {
	started := time.Now()
	defer func() { s.recordDiagnostic("update.check", err, time.Since(started)) }()
	trust, client := s.updateTrust()
	if trust == nil {
		return s.UpdateInfo(), nil
	}
	ctx, finish, err := s.beginOperation("update.check")
	if err != nil {
		return s.UpdateInfo(), err
	}
	defer finish()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	prior := s.UpdateInfo()
	s.updateMu.Lock()
	priorEnvelope := append([]byte{}, s.updateEnvelope...)
	s.updateEnvelope = nil
	s.updateMu.Unlock()
	defer func() {
		if ctx.Err() != nil && err != nil {
			prior.Cancelled = true
			prior.Message = "操作已取消"
			prior.State = "idle"
			if prior.AvailableVersion != "" && len(priorEnvelope) > 0 {
				prior.State = "available"
			}
			s.updateMu.Lock()
			s.updateEnvelope = priorEnvelope
			s.updateMu.Unlock()
			result = s.setUpdateState(prior)
		}
	}()
	s.setUpdateState(UpdateState{State: "checking", Message: "正在检查更新"})
	if len(trust.Policy.PublicKey) != 32 || trust.Policy.Product != "WebDAVMount" || len(trust.Policy.AllowedHosts) == 0 || trust.Policy.MaxBytes <= 0 || trust.Policy.MaxBytes > 2<<30 {
		return s.updateFailure(ctx, "更新服务配置不可用")
	}
	u, err := url.Parse(trust.FeedURL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Port() != "" && u.Port() != "443" {
		return s.updateFailure(ctx, "更新服务配置不可用")
	}
	urls := updatefeed.RoutedURLs(trust.FeedURL, trust.MirrorOrigins)
	var manifest updatefeed.Manifest
	var data []byte
	var message string
	var verifiedVersion error
	for _, address := range urls {
		requestCtx := ctx
		cancelRequest := func() {}
		if len(urls) > 1 {
			requestCtx, cancelRequest = context.WithTimeout(ctx, 8*time.Second)
		}
		data, message = fetchUpdateEnvelope(requestCtx, client, address)
		cancelRequest()
		if message == "" {
			manifest, err = updatefeed.Verify(data, trust.Policy)
			if err == nil {
				verifiedVersion = nil
				break
			}
			if errors.Is(err, updatefeed.ErrCurrentVersion) ||
				(errors.Is(err, updatefeed.ErrOlderVersion) && verifiedVersion == nil) {
				verifiedVersion = err
			}
			message = "更新清单校验未通过，未接受更新"
		}
		if ctx.Err() != nil {
			break
		}
	}
	if ctx.Err() != nil {
		return s.updateFailure(ctx, "无法连接更新服务，请稍后重试")
	}
	if errors.Is(verifiedVersion, updatefeed.ErrCurrentVersion) {
		return s.setUpdateState(UpdateState{State: "current", Message: "当前已是最新发布版本"}), nil
	}
	if errors.Is(verifiedVersion, updatefeed.ErrOlderVersion) {
		return s.setUpdateState(UpdateState{State: "current", Message: "当前版本高于线上发布版本，暂无可用更新"}), nil
	}
	if message != "" {
		return s.updateFailure(ctx, message)
	}
	s.updateMu.Lock()
	s.updateEnvelope = append([]byte{}, data...)
	s.updateMu.Unlock()
	cached := updatefeed.CachedRoutedTransferBytes(s.updateDownloadCache(), manifest, trust.MirrorOrigins)
	message = "发现新版本，可选择位置下载；不会自动安装"
	if cached > 0 {
		message = "发现新版本，已保留下载进度，可选择位置继续下载"
	}
	return s.setUpdateState(UpdateState{State: "available", Message: message, AvailableVersion: manifest.Version, ResumeAvailable: cached > 0, ResumedBytes: cached, DownloadedBytes: cached, TotalBytes: manifest.Size}), nil
}

func fetchUpdateEnvelope(ctx context.Context, client *http.Client, address string) ([]byte, string) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, "更新服务配置不可用"
	}
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("Cache-Control", "no-cache")
	response, err := client.Do(req)
	if err != nil {
		return nil, "无法连接更新服务，请稍后重试"
	}
	defer response.Body.Close()
	if response.StatusCode != 200 || response.Uncompressed || response.ContentLength > 65536 {
		return nil, "更新服务返回异常，未接受更新"
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 65537))
	if err != nil || len(data) > 65536 {
		return nil, "更新清单读取失败或超过大小限制"
	}
	return data, ""
}

func (s *DesktopService) updateDownloadCache() string {
	if s.dir == "" {
		return ""
	}
	return filepath.Join(s.dir, "update-downloads")
}

func (s *DesktopService) ClearUpdateDownloadCache() (UpdateState, error) {
	_, finish, e := s.beginOperation("update.clear")
	if e != nil {
		return s.UpdateInfo(), e
	}
	defer finish()
	if e = updatefeed.ClearTransferCache(s.updateDownloadCache()); e != nil {
		return s.UpdateInfo(), errors.New("无法清理下载缓存，请等待下载结束或检查本机目录权限")
	}
	state := s.UpdateInfo()
	state.ResumeAvailable = false
	state.ResumedBytes = 0
	state.DownloadedBytes = 0
	state.NetworkBytes = 0
	state.BytesPerSecond = 0
	state.RemainingSeconds = nil
	state.RetryCount = 0
	state.Message = "下载缓存已清理，已保存的安装包保留"
	return s.setUpdateState(state), nil
}

func (s *DesktopService) downloadFailure(ctx context.Context, m updatefeed.Manifest, mirrorOrigins []string, cause error) (UpdateState, error) {
	state := s.UpdateInfo()
	cached := updatefeed.CachedRoutedTransferBytes(s.updateDownloadCache(), m, mirrorOrigins)
	state.ResumeAvailable = cached > 0
	state.ResumedBytes = cached
	state.DownloadedBytes = cached
	state.BytesPerSecond = 0
	state.RemainingSeconds = nil
	state.State = "error"
	state.Message = "更新下载未完成，请检查网络或保存位置后重试"
	if cached > 0 {
		state.Message = "下载已中断，已完成分段保留，可继续下载"
	}
	switch {
	case errors.Is(cause, updatefeed.ErrArtifactIntegrity):
		state.Message = "安装包校验失败，异常下载缓存已清除，请重新检查更新"
	case errors.Is(cause, updatefeed.ErrRemoteChanged):
		state.Message = "更新源文件已变化，请重新检查更新后下载"
	case errors.Is(cause, updatefeed.ErrDestinationUnavailable):
		state.Message = "保存位置已有文件或不可用，请选择新文件名"
	case errors.Is(cause, updatefeed.ErrUnsafePath):
		state.Message = "保存位置或下载缓存路径不可用，请检查路径和权限"
	case errors.Is(cause, updatefeed.ErrWorkspaceBusy):
		state.Message = "下载缓存正被其他进程使用，请稍后重试"
	}
	if ctx.Err() != nil {
		state.Cancelled = true
		state.State = "available"
		state.Message = "下载已暂停，可稍后继续"
		if cached > 0 {
			state.Message = "下载已暂停，已完成分段保留，可稍后继续"
		}
		return s.setUpdateState(state), errors.New("操作已取消")
	}
	return s.setUpdateState(state), errors.New(state.Message)
}
func (s *DesktopService) DownloadUpdate() (result UpdateState, err error) {
	started := time.Now()
	defer func() { s.recordDiagnostic("update.download", err, time.Since(started)) }()
	trust, client := s.updateTrust()
	if trust == nil {
		return s.UpdateInfo(), nil
	}
	ctx, finish, err := s.beginOperation("update.download")
	if err != nil {
		return s.UpdateInfo(), err
	}
	defer finish()
	s.updateMu.Lock()
	envelope := append([]byte{}, s.updateEnvelope...)
	picker := s.chooseUpdateFile
	s.updateMu.Unlock()
	manifest, err := updatefeed.Verify(envelope, trust.Policy)
	if err != nil {
		return s.updateFailure(ctx, "请重新检查更新后再下载")
	}
	if picker == nil {
		return s.updateFailure(ctx, "当前环境无法打开更新保存窗口")
	}
	path, err := picker()
	if err != nil {
		return s.updateFailure(ctx, "无法打开更新保存窗口")
	}
	if path == "" {
		return s.setUpdateState(UpdateState{State: "available", Message: "已取消下载，新版本仍可稍后下载", AvailableVersion: manifest.Version, Cancelled: true}), nil
	}
	now := time.Now()
	s.setUpdateState(UpdateState{State: "downloading", Message: "正在连接下载服务器…", AvailableVersion: manifest.Version, TotalBytes: manifest.Size, startedAt: now, sampleAt: now})
	// A new clock sample prevents a long save dialog from bypassing expiry.
	trust, client = s.updateTrust()
	if trust == nil {
		return s.updateFailure(ctx, "更新服务配置不可用")
	}
	progress := func(p updatefeed.DownloadProgress) {
		s.updateMu.Lock()
		defer s.updateMu.Unlock()
		now := time.Now()
		state := &s.updateState
		counter := p.NetworkBytes
		if p.Mode == "" {
			counter = p.DownloadedBytes
		}
		if counter < state.NetworkBytes {
			state.BytesPerSecond = 0
			state.sampleAt, state.sampleBytes = now, counter
			state.lastByteAt = time.Time{}
		}
		if counter > state.NetworkBytes {
			state.lastByteAt = now
		}
		state.DownloadedBytes = p.DownloadedBytes
		state.NetworkBytes = counter
		state.ResumedBytes = p.ResumedBytes
		state.Connections = p.Connections
		state.RetryCount = p.RetryCount
		state.DownloadMode = p.Mode
		if elapsed := now.Sub(state.sampleAt).Seconds(); elapsed >= 0.5 {
			state.BytesPerSecond = float64(counter-state.sampleBytes) / elapsed
			state.sampleAt, state.sampleBytes = now, counter
		}
		state.State, state.Message = "downloading", "正在下载更新包"
		if p.Verifying {
			state.State, state.Message = "verifying", "下载完成，正在校验文件…"
		}
	}
	options := updatefeed.TransferOptions{CacheDirectory: s.updateDownloadCache(), Connections: 4, MirrorOrigins: trust.MirrorOrigins}
	if _, err = updatefeed.DownloadResumable(ctx, envelope, trust.Policy, client, path, options, progress); err != nil {
		return s.downloadFailure(ctx, manifest, trust.MirrorOrigins, err)
	}
	s.setUpdateState(UpdateState{State: "downloaded", Message: "更新文件已下载并校验", AvailableVersion: manifest.Version, DownloadedPath: path})
	if s.startUpdateInstaller != nil {
		return s.installDownloadedUpdate(ctx)
	}
	return s.UpdateInfo(), nil
}
