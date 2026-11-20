package desktop

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"golang.org/x/sys/windows"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"
	"webdavmount/internal/providers"
)

type Profile struct {
	Storage            *providers.ObjectOptions `json:"storage,omitempty"`
	Proxy              *ProxySettings           `json:"proxy,omitempty"`
	MEGASession        *megaSession             `json:"megaSession,omitempty"`
	Provider           string                   `json:"provider,omitempty"`
	HostKey            string                   `json:"hostKey,omitempty"`
	ID                 string                   `json:"id"`
	PresetKey          string                   `json:"presetKey,omitempty"`
	Name               string                   `json:"name"`
	URL                string                   `json:"url"`
	User               string                   `json:"user"`
	Password           string                   `json:"password"`
	Drive              string                   `json:"drive"`
	ClearPassword      bool                     `json:"clearPassword,omitempty"`
	AutoMount          bool                     `json:"autoMount"`
	Group              string                   `json:"group,omitempty"`
	Note               string                   `json:"note,omitempty"`
	DirectoryFavorites []DirectoryFavorite      `json:"directoryFavorites,omitempty"`
}
type DesktopService struct {
	sessionRecovery          *sessionRecoveryJournal
	trayShortcuts            trayShortcutQueue
	proxy                    engineProxy
	cloudLogin               *cloudLoginSession
	migrationCommitHook      func(string)
	cleanupMu                sync.Mutex
	cleanupRunMu             sync.Mutex
	cleanupItems             []CacheCleanupItem
	cleanupAfterID           string
	opMu                     sync.Mutex
	opCancel                 context.CancelFunc
	opDone                   chan struct{}
	closing                  bool
	lifeCtx                  context.Context
	lifeCancel               context.CancelFunc
	logMu                    sync.Mutex
	diagnosticLog            *diagnosticStore
	cacheSettings            CacheSettings
	cacheRoot                string
	cacheWarning             string
	cacheMigrationFallback   atomic.Bool
	writable                 atomic.Pointer[writableSession]
	startupMu                sync.Mutex
	startupActive            bool
	startupRetryActive       bool
	startupStopRequested     bool
	startupProfiles          []Profile
	startupPresetID          string
	startupPresetName        string
	startupPresetWarning     string
	startupStates            map[string]StartupState
	startupCancel            context.CancelFunc
	startupDone              chan struct{}
	exit                     func()
	chooseCacheDirectory     func(string) (string, error)
	chooseDiagnosticFile     func() (string, error)
	chooseCheckReportFile    func() (string, error)
	checkProgressMu          sync.Mutex
	checkProgress            ConnectionCheckProgress
	checkHistoryWriteFailed  bool
	chooseUpdateFile         func() (string, error)
	startUpdateInstaller     func(string) error
	chooseConnectionExport   func() (string, error)
	chooseConnectionImport   func() (string, error)
	connectionImport         *connectionImportSession
	updateMu                 sync.Mutex
	updatePolicy             *DesktopUpdatePolicy
	updateClient             *http.Client
	updateNow                func() time.Time
	updateState              UpdateState
	updateEnvelope           []byte
	openDirectory            func(string) error
	chooseMountedFile        func(string) (string, error)
	chooseMountedArchive     func(string) (string, error)
	chooseMountedSaveFolder  func() (string, error)
	fileSaveMu               sync.Mutex
	fileSave                 MountedFileSaveProgress
	saveHistory              mountedSaveHistoryStore
	loginStartup             *loginStartupController
	autoRecovery             *automaticRecovery
	maintenance              *connectionMaintenance
	migrationMu              sync.Mutex
	remoteSearch             remoteSearchTracker
	multiSearchMu            sync.Mutex
	multiSearch              MultiRemoteSearchProgress
	waitRemoteSearch         func(context.Context) error
	migration                CacheMigrationStatus
	historyMu                sync.Mutex
	mu                       sync.Mutex
	dir, addr, secret        string
	cmd                      *exec.Cmd
	done                     chan struct{}
	profiles                 []Profile
	client                   *http.Client
	retiredDrives            map[string]bool
	safeDriveReuse           bool
	s3Preview                bool
	connectionProxyAvailable bool
	desired                  map[string]bool
	health                   map[string]string
	recovery                 map[string]string
}

// Exit is an explicit application exit, unlike closing the main window.
func (s *DesktopService) Exit() error {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	if err := s.writableGuard(); err != nil {
		return err
	}
	if s.exit != nil && !s.closing {
		s.closing = true
		go s.exit()
	}
	return nil
}

func randomID() string {
	b := make([]byte, 16)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b)
}
func crypt(data []byte, decrypt bool) ([]byte, error) {
	if len(data) == 0 {
		return nil, errors.New("本机数据为空")
	}
	in := windows.DataBlob{Size: uint32(len(data)), Data: &data[0]}
	var out windows.DataBlob
	var err error
	if decrypt {
		err = windows.CryptUnprotectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out)
	} else {
		err = windows.CryptProtectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out)
	}
	if err != nil {
		return nil, err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return append([]byte(nil), unsafe.Slice(out.Data, int(out.Size))...), nil
}
func newDesktop(dir, engine string, sharedLog ...*diagnosticStore) (*DesktopService, error) {
	s := &DesktopService{dir: dir, secret: randomID(), client: &http.Client{Timeout: 30 * time.Second}, done: make(chan struct{}), profiles: []Profile{}, updatePolicy: compiledUpdatePolicy()}
	s.lifeCtx, s.lifeCancel = context.WithCancel(context.Background())
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	if len(sharedLog) > 0 && sharedLog[0] != nil {
		if !strings.EqualFold(filepath.Clean(sharedLog[0].dir), filepath.Clean(dir)) {
			return nil, errors.New("诊断日志的数据目录不匹配")
		}
		s.diagnosticLog = sharedLog[0]
	} else {
		s.sessionLog()
	}
	if err := s.loadCacheSettings(); err != nil {
		return nil, err
	}
	s.loadWritableSession()
	s.autoRecovery = loadAutomaticRecovery(dir)
	s.maintenance = loadMaintenance(dir)
	data, err := os.ReadFile(filepath.Join(dir, "profiles.enc"))
	if err == nil {
		plain, e := crypt(data, true)
		if e != nil {
			return nil, errors.New("无法解密连接配置，请保留 profiles.enc")
		}
		if e = json.Unmarshal(plain, &s.profiles); e != nil {
			return nil, e
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	s.addr = "http://" + listener.Addr().String()
	listener.Close()
	s.cmd = exec.Command(engine, "rcd", "--config", "", "--rc-addr", strings.TrimPrefix(s.addr, "http://"), "--cache-dir", s.cacheRoot, "--timeout", "15s", "--contimeout", "10s", "--retries", "1", "--low-level-retries", "1")
	s.loadEngineProxy()
	s.cmd.Env = s.proxy.environment(os.Environ())
	s.cmd.Env = append(s.cmd.Env, "RCLONE_RC_USER=desktop", "RCLONE_RC_PASS="+s.secret)
	s.cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}

	if err = s.cmd.Start(); err != nil {
		return nil, errors.New("无法启动同目录 rclone.exe")
	}
	go func() { s.cmd.Wait(); close(s.done) }()
	ready := false
	var startupErr error
	for i := 0; i < 50; i++ {
		if version, e := s.call("core/version", nil); e == nil {
			s.safeDriveReuse = engineSupportsDriveReuse(version)
			s.connectionProxyAvailable = engineSupportsConnectionProxy(version)
			s.s3Preview = os.Getenv("WEBDAVMOUNT_S3_PREVIEW") == "1" && version["version"] == s3CandidateEngine
			ready = true
			break
		} else {
			startupErr = e
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !ready {
		s.close()
		return nil, fmt.Errorf("挂载引擎启动失败: %w", startupErr)
	}
	for _, p := range s.profiles {
		if err = s.configure(p); err != nil {
			s.close()
			return nil, err
		}
	}
	return s, nil
}

// Only the exact engine revision whose asynchronous cleanup was verified may
// bypass the old-engine drive retirement workaround. Unknown engines fail safe.
func engineSupportsDriveReuse(version map[string]any) bool {
	return version["version"] == "v1.76.0-webdavmount-nosmb-remount1" || engineSupportsConnectionProxy(version)
}

func engineSupportsConnectionProxy(version map[string]any) bool {
	return version["version"] == "v1.76.0-webdavmount-nosmb-remount1-proxy2" ||
		version["version"] == "v1.76.0-webdavmount-nosmb-remount1-proxy1" ||
		engineSupportsProtectedWriteback(version)
}
func engineSupportsProtectedWriteback(version map[string]any) bool {
	return version["version"] == "v1.76.0-webdavmount-nosmb-remount1-proxy2-writeback2" ||
		version["version"] == "v1.76.0-webdavmount-nosmb-remount1-proxy2-writeback2-move1" ||
		version["version"] == "v1.76.0-webdavmount-nosmb-remount1-proxy2-writeback2-move1-delete1" ||
		version["version"] == s3CandidateEngine
}

func (s *DesktopService) call(method string, input any) (map[string]any, error) {
	return s.callContext(context.Background(), method, input)
}
func (s *DesktopService) callContext(ctx context.Context, method string, input any) (output map[string]any, err error) {
	started := time.Now()
	defer func() { s.recordDiagnostic(method, err, time.Since(started)) }()
	if s.lifeCtx != nil && method != "core/quit" && method != "config/get" {
		merged, cancel := context.WithCancel(ctx)
		stop := context.AfterFunc(s.lifeCtx, cancel)
		defer stop()
		defer cancel()
		ctx = merged
	}
	b, _ := json.Marshal(input)
	req, e := http.NewRequestWithContext(ctx, "POST", s.addr+"/"+method, bytes.NewReader(b))
	if e != nil {
		return nil, e
	}
	req.SetBasicAuth("desktop", s.secret)
	req.Header.Set("Content-Type", "application/json")
	client := s.client
	if method == "config/update" {
		// The browser grant can take longer than the normal RC request.
		// A separate context bounds the authorization flow.
		copy := *client
		copy.Timeout = 0
		client = &copy
	}
	resp, e := client.Do(req)
	if e != nil {
		if ctx.Err() != nil {
			return nil, errors.New("操作已取消")
		}
		if timeout, ok := e.(net.Error); ok && timeout.Timeout() {
			return nil, errors.New("操作超时，请检查网络或网盘服务后重试")
		}
		return nil, errors.New("挂载引擎未响应，请使用恢复引擎/挂载")
	}
	defer resp.Body.Close()
	limit := int64(16 << 20)
	if method == "operations/list" {
		limit = 64 << 20
	}
	result, e := decodeEngineResponse(resp.Body, limit)
	if e != nil {
		if ctx.Err() != nil {
			return nil, errors.New("操作已取消")
		}
		var timeout net.Error
		if errors.As(e, &timeout) && timeout.Timeout() {
			return nil, errors.New("操作超时，请检查网络或网盘服务后重试")
		}
		return nil, e
	}
	if resp.StatusCode != 200 {
		detail, _ := result["error"].(string)
		return nil, diagnosticError(resp.StatusCode, detail)
	}
	return result, nil
}
func (s *DesktopService) configure(p Profile) error {
	return s.configureProfile(context.Background(), p, "")
}
func (s *DesktopService) configureProfile(ctx context.Context, p Profile, megaCode string) error {
	backend, params, e := providers.ObjectEngineConfig(p.Provider, p.URL, p.User, p.Password, p.HostKey, p.Storage)
	if e != nil {
		return e
	}
	if e = s.applyConnectionProxy(p, params); e != nil {
		return e
	}
	if backend == providers.MEGA {
		if megaCode != "" {
			params["2fa"] = megaCode
		} else if p.MEGASession != nil {
			params["session_id"] = p.MEGASession.ID
			params["master_key"] = p.MEGASession.MasterKey
		}
	}
	_, e = s.callContext(ctx, "config/create", map[string]any{"name": p.ID, "type": backend, "parameters": params, "opt": map[string]bool{"obscure": true, "nonInteractive": true}})
	return e
}
func (p Profile) remoteFS() string {
	return p.ID + ":" + providers.RemoteRoot(p.Provider, p.URL)
}
func (s *DesktopService) callProfileContext(ctx context.Context, p Profile, method string, input map[string]any) (map[string]any, error) {
	if providers.IsOAuth(p.Provider) && p.Password == "" {
		return nil, errors.New("请编辑连接并完成浏览器授权")
	}
	if p.Password == "" && (p.Provider == providers.SFTP || p.Provider == providers.MEGA || providers.IsObjectStorage(p.Provider)) {
		return nil, errors.New("请编辑连接并补填密码或对象存储密钥")
	}
	if p.Storage != nil && p.Storage.Mode == "session" && p.Storage.SessionToken == "" {
		return nil, errors.New("请编辑连接并填写 AWS 会话令牌")
	}
	input["fs"] = p.remoteFS()
	return s.callContext(ctx, method, input)
}
func (s *DesktopService) persist(p []Profile) error {
	if session := s.writable.Load(); session != nil && !slices.ContainsFunc(p, func(profile Profile) bool {
		return profile.ID == session.ID && writableFingerprint(profile) == session.Fingerprint
	}) {
		return s.writableGuard()
	}
	b, e := json.Marshal(p)
	if e != nil {
		return e
	}
	b, e = crypt(b, false)
	if e != nil {
		return e
	}
	return atomicWriteSettings(filepath.Join(s.dir, "profiles.enc"), b)
}
func (s *DesktopService) Snapshot() (snapshot map[string]any, err error) {
	defer func() {
		if snapshot != nil {
			snapshot["version"] = appVersion
			if session := s.writable.Load(); session != nil {
				snapshot["writableProfile"] = session.ID
				snapshot["writableJournalError"] = session.ID == ""
			}
			snapshot["cacheMigrationFallback"] = s.cacheMigrationFallback.Load()
			snapshot["maintenance"] = s.MaintenanceInfo()
			snapshot["startupTasks"] = s.StartupMountTasks()
			_, snapshot["sessionRecovery"] = s.sessionRecovery.previousEntries()
			if drives, e := occupiedWindowsDrives(); e == nil {
				snapshot["occupiedDrives"] = drives
			}
		}
	}()
	states, active, bootProfiles, bootMounts := s.startupSnapshot()
	auto, repairing := s.automaticRecoverySnapshot()
	if repairing != nil {
		repairing["autoRecovery"] = auto
		repairing["startup"] = states
		return repairing, nil
	}
	if active {
		return map[string]any{"profiles": bootProfiles, "mounts": bootMounts, "autoRunning": true, "startup": states, "autoRecovery": auto}, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	profiles := append([]Profile{}, s.profiles...)
	for i := range profiles {
		profiles[i] = profiles[i].public()
		profiles[i].DirectoryFavorites = nil
	}
	engineOnline := s.engineOnline()
	view := map[string]any{"profiles": profiles, "mounts": []any{}, "autoRunning": false, "startup": states, "engineOnline": engineOnline, "health": copyStatus(s.health), "recovery": copyStatus(s.recovery), "autoRecovery": auto}
	view["pendingDirectoryReviews"] = s.pendingDirectoryReviewIDs()
	if !engineOnline {
		return view, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	result, e := s.callContext(ctx, "mount/listmounts", nil)
	mounts, stateErr := identifiedMounts(result, profiles)
	if e != nil || stateErr != nil {
		// An unavailable listing proves neither that the disk is unmounted nor
		// that the engine has exited. Still return local recovery information.
		online := s.engineOnline()
		view["engineOnline"] = online
		view["mountStateUnknown"] = online
		return view, nil
	}
	view["mounts"] = mounts
	if s.s3Preview {
		candidates := make([]string, 0)
		for _, profile := range profiles {
			if s3CandidateProfile(profile) {
				candidates = append(candidates, profile.ID)
			}
		}
		view["s3WritableProfiles"] = candidates
	}
	for _, mount := range mounts {
		if mount["Writable"] == true {
			if p, err := s.find(mount["ProfileID"].(string)); err == nil {
				view["writableStatus"] = s.writableStats(ctx, p)
			}
			break
		}
	}
	return view, nil
}
func validate(p Profile) error {
	if _, _, err := providers.ObjectEngineConfig(p.Provider, p.URL, p.User, p.Password, p.HostKey, p.Storage); err != nil {
		return err
	}
	if strings.TrimSpace(p.Name) == "" {
		return errors.New("请填写连接名称")
	}
	if !regexp.MustCompile(`^[D-Z]:$`).MatchString(p.Drive) {
		return errors.New("请选择 D 至 Z 盘符")
	}
	return nil
}
func (s *DesktopService) Save(p Profile) (Profile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if providers.IsOAuth(p.Provider) {
		if p.ID == "" || p.Password != "" || p.ClearPassword {
			return Profile{}, errors.New("请通过浏览器授权保存连接")
		}
		old, err := s.find(p.ID)
		if err != nil {
			return Profile{}, err
		}
		p.User = old.User
	}
	return s.saveProfileLocked(p)
}

func (s *DesktopService) saveProfileLocked(p Profile) (Profile, error) {
	return s.saveProfileWithCodeLocked(context.Background(), p, "")
}

func (s *DesktopService) saveProfileWithCodeLocked(ctx context.Context, p Profile, megaCode string) (Profile, error) {
	// Preferences are managed by their bounded API, never injected through Save.
	p.DirectoryFavorites = nil
	p.Group = "" // Grouping is local metadata managed independently of engine edits.
	p.Note = ""
	p.PresetKey = ""
	p.MEGASession = nil // Only a verified engine login can supply a session.
	if e := validate(p); e != nil {
		return Profile{}, e
	}
	if p.Storage != nil {
		options := *p.Storage
		p.Storage = &options
	}
	previousID := p.ID
	index := -1
	keepEngineConfig := false
	var oldProxy *ProxySettings
	if previousID != "" {
		old, err := s.find(previousID)
		if err != nil {
			return Profile{}, err
		}
		if providers.Kind(p.Provider) != providers.Kind(old.Provider) {
			return Profile{}, errors.New("连接类型不可修改，请新建另一种连接")
		}
		if err = s.ensureUnmounted(old); err != nil {
			return Profile{}, err
		}
		oldProxy = old.Proxy
		sameAuth := sameObjectAuthentication(old, p)
		if !sameAuth && p.Password == "" && !p.ClearPassword {
			return Profile{}, errors.New("对象存储认证信息已更改，请重新填写凭据")
		}
		if p.Password == "" && !p.ClearPassword {
			if sameAuth {
				p.Password = old.Password
			}
			if sameAuth && p.Storage != nil && old.Storage != nil && p.Storage.SessionToken == "" {
				p.Storage.SessionToken = old.Storage.SessionToken
			}
			if megaCode == "" && p.User == old.User {
				p.MEGASession = old.MEGASession
			}
			// OAuth metadata edits don't change the backend or selected resource.
			// Keep the live configuration so a concurrent token refresh isn't discarded.
			keepEngineConfig = providers.IsOAuth(old.Provider)
			if keepEngineConfig && p.Password != "" {
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				p.Password = s.latestCloudCredential(ctx, old)
				cancel()
			}
		}
		p.DirectoryFavorites = append([]DirectoryFavorite(nil), old.DirectoryFavorites...)
		p.Group = old.Group
		p.Note = old.Note
		p.PresetKey = old.presetIdentity()
		for i := range s.profiles {
			if s.profiles[i].ID == previousID {
				index = i
				break
			}
		}
	}
	proxy, err := s.normalizeConnectionProxy(p.Provider, p.Proxy, oldProxy)
	if err != nil {
		return Profile{}, err
	}
	p.Proxy = proxy
	if !sameConnectionProxy(oldProxy, proxy) {
		keepEngineConfig = false
	}
	if p.Storage != nil && !p.ClearPassword {
		if p.Storage.Mode != "" && p.Password == "" {
			return Profile{}, errors.New("请填写所选对象存储认证方式的凭据")
		}
		if p.Storage.Mode == "session" && p.Storage.SessionToken == "" {
			return Profile{}, errors.New("请填写 AWS 会话令牌")
		}
	}
	if p.ClearPassword {
		p.Password = ""
		if p.Storage != nil {
			p.Storage.SessionToken = ""
		}
	}
	p.ClearPassword = false
	if megaCode != "" && p.Password == "" {
		return Profile{}, errors.New("请填写 MEGA 账号密码")
	}
	if !keepEngineConfig {
		// A new engine identity prevents cached clients from retaining old credentials or endpoints.
		p.ID = "dav" + randomID()
		if e := s.configureProfile(ctx, p, megaCode); e != nil {
			if megaCode != "" {
				s.call("config/delete", map[string]any{"name": p.ID})
			}
			return Profile{}, e
		}
		if megaCode != "" {
			session, e := s.finishMEGALogin(ctx, p)
			if e != nil {
				s.call("config/delete", map[string]any{"name": p.ID})
				return Profile{}, e
			}
			p.MEGASession = session
			if ctx.Err() != nil {
				s.call("config/delete", map[string]any{"name": p.ID})
				return Profile{}, errors.New("操作已取消")
			}
		}
	}
	next := append([]Profile{}, s.profiles...)
	if index >= 0 {
		next[index] = p
	} else {
		next = append(next, p)
	}
	if e := s.persist(next); e != nil {
		if !keepEngineConfig {
			s.call("config/delete", map[string]any{"name": p.ID})
		}
		return Profile{}, e
	}
	s.profiles = next
	if previousID != "" && !keepEngineConfig {
		s.call("config/delete", map[string]any{"name": previousID})
	}
	p = p.public()
	p.DirectoryFavorites = nil
	return p, nil
}

func (s *DesktopService) ensureUnmounted(p Profile) error {
	result, err := s.call("mount/listmounts", nil)
	if err != nil {
		return err
	}
	_, owned, err := connectionMountState(result, p)
	if err != nil {
		return err
	}
	if owned {
		return errors.New("请先卸载该连接的磁盘，再编辑或删除连接")
	}
	return s.checkConnectionDirectoryReview(p)
}

// Delete removes only this application's encrypted local connection record.
func (s *DesktopService) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.find(id)
	if err != nil {
		return err
	}
	if err = s.ensureUnmounted(p); err != nil {
		return err
	}
	next := make([]Profile, 0, len(s.profiles)-1)
	for _, entry := range s.profiles {
		if entry.ID != id {
			next = append(next, entry)
		}
	}
	if err = s.persist(next); err != nil {
		return err
	}
	s.profiles = next
	s.call("config/delete", map[string]any{"name": id})
	return nil
}
func (s *DesktopService) find(id string) (Profile, error) {
	for _, p := range s.profiles {
		if p.ID == id {
			return p, nil
		}
	}
	return Profile{}, errors.New("连接不存在")
}
func (s *DesktopService) Browse(id, path string) (map[string]any, error) {
	ctx, finish, err := s.beginOperation("browse")
	if err != nil {
		return nil, err
	}
	defer finish()
	s.mu.Lock()
	defer s.mu.Unlock()
	p, e := s.find(id)
	if e != nil {
		return nil, e
	}
	result, e := s.callProfileContext(ctx, p, "operations/list", map[string]any{"remote": path, "opt": map[string]bool{"recurse": false}})
	s.maintenanceAccess(id, e)
	// A cancelled attempt supplies no new evidence about remote availability.
	// Keep the last completed read result (including an unknown or failed one).
	if e != nil && e.Error() == "操作已取消" {
		return result, e
	}
	if s.health == nil {
		s.health = make(map[string]string)
	}
	if e != nil {
		s.health[id] = "最近读取失败：" + e.Error()
	} else {
		s.health[id] = "最近目录读取成功"
	}
	return result, e
}
func (s *DesktopService) Mount(id string) error {
	ctx, finish, err := s.beginOperation("mount")
	if err != nil {
		return err
	}
	defer finish()
	return s.mountContext(ctx, id)
}
func (s *DesktopService) mountContext(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mountLocked(ctx, id)
}
func (s *DesktopService) mountLocked(ctx context.Context, id string) error {
	p, e := s.find(id)
	if e != nil {
		return e
	}
	if session := s.writable.Load(); session != nil && session.ID == id {
		return errors.New("此网盘有可写会话，请使用「恢复可写挂载」")
	}
	if e = s.checkMountDriveLocked(p); e != nil {
		return e
	}
	if e = s.mountProfileLocked(ctx, p, true); e != nil {
		return e
	}
	if s.desired == nil {
		s.desired = make(map[string]bool)
	}
	s.desired[id] = true
	s.sessionRecovery.update(p, true)
	return nil
}

func (s *DesktopService) checkMountDriveLocked(p Profile) error {
	if s.retiredDrives[p.Drive] {
		return errors.New("请先卸载本程序的其他磁盘，再重新挂载此盘符")
	}
	// Detect assigned letters without opening a Windows disk or network path.
	occupied, assignmentErr := occupiedWindowsDrives()
	inUse := slices.Contains(occupied, strings.ToUpper(p.Drive))
	if assignmentErr != nil {
		_, statErr := os.Stat(p.Drive + `\`)
		inUse = statErr == nil
	}
	if inUse {
		return errors.New("盘符已被占用，请编辑连接并选择其他盘符")
	}
	return nil
}

func (s *DesktopService) mountProfileLocked(ctx context.Context, p Profile, readOnly bool) error {
	limit := s.cacheSettings.MaxGiB
	if limit == 0 {
		limit = 2
	}
	// Persist downloaded ranges at the last file close. A grace-period handle
	// can otherwise outlive an immediate unmount/engine restart and lose its index.
	_, e := s.callProfileContext(ctx, p, "mount/mount", map[string]any{"mountPoint": p.Drive, "mountType": "cmount", "mountOpt": map[string]any{"ReadOnly": readOnly, "VolumeName": p.Name}, "vfsOpt": map[string]any{"CacheMode": 3, "ReadOnly": readOnly, "HandleCaching": 0, "CacheMaxSize": limit * 1024 * 1024 * 1024}})
	return e
}
func (s *DesktopService) Unmount(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.unmountLocked(id)
}
func (s *DesktopService) unmountLocked(id string) error {
	p, e := s.find(id)
	if e != nil {
		return e
	}
	state, e := s.call("mount/listmounts", nil)
	if e != nil {
		return e
	}
	present, owned, e := connectionMountState(state, p)
	if e != nil {
		return e
	}
	if !owned {
		if present {
			return errors.New("该盘符属于其他连接，未执行卸载")
		}
		return errors.New("该连接未挂载")
	}
	writable := isWritableMount(state, p)
	if writable {
		if e = s.checkWritableUnmount(p); e != nil {
			return e
		}
	}
	_, e = s.call("mount/unmount", map[string]any{"mountPoint": p.Drive})
	if e != nil {
		return e
	}
	delete(s.desired, id)
	s.sessionRecovery.update(p, false)
	if !s.safeDriveReuse {
		if s.retiredDrives == nil {
			s.retiredDrives = make(map[string]bool)
		}
		s.retiredDrives[p.Drive] = true
	}
	if writable {
		if e = s.finishWritableUnmount(p); e != nil {
			return e
		}
	}
	result, e := s.call("mount/listmounts", nil)
	if e != nil {
		return e
	}
	if list, ok := result["mountPoints"].([]any); ok && len(list) == 0 && !s.safeDriveReuse {
		// rclone v1.76's asynchronous Wait cleanup can delete a newly reused
		// drive's registration. Reap the old engine before reusing its last drive.
		engine := s.cmd.Path
		s.close()
		next, err := newDesktop(s.dir, engine, s.sessionLog())
		if err != nil {
			return fmt.Errorf("已卸载，但引擎重启失败，请重启程序: %w", err)
		}
		s.adoptEngine(next)
		s.retiredDrives = nil
	}
	return nil
}
func (s *DesktopService) close() {
	if s.startupCancel != nil {
		s.startupCancel()
		<-s.startupDone
	}
	s.persistRenewedCloudTokensLocked()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	s.callContext(ctx, "core/quit", nil)
	select {
	case <-s.done:
	case <-time.After(5 * time.Second):
		s.cmd.Process.Kill()
		<-s.done
	}
}
