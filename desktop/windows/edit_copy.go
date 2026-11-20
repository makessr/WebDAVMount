package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows"

	"webdavmount/internal/providers"
)

const (
	editCopyFile = "webdav-edit-copy.enc"
	editCopyMax  = 256 << 20
)

var (
	errEditCopyConflict    = errors.New("远端文件已更改或不存在。本机编辑副本已保留，请核对远端版本")
	errEditCopyUnsupported = errors.New("服务器未提供可核对的强 ETag 或未通过条件请求检查，无法使用编辑副本")
	errEditCopyRecord      = errors.New("无法读取编辑副本记录，已保留现有本机文件")
)

type editCopyRecord struct {
	ProfileID, Fingerprint, Remote, Local, ETag string
}

type EditCopyStatus struct {
	ProfileID string `json:"profileId"`
	Remote    string `json:"remote"`
	Local     string `json:"local"`
}

func (s *DesktopService) EditCopy() (*EditCopyStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, err := s.readEditCopy()
	if err != nil || record == nil {
		return nil, err
	}
	return &EditCopyStatus{ProfileID: record.ProfileID, Remote: record.Remote, Local: record.Local}, nil
}

func (s *DesktopService) readEditCopy() (*editCopyRecord, error) {
	filename := filepath.Join(s.dir, editCopyFile)
	if err := noReparse(filename); err != nil {
		return nil, errEditCopyRecord
	}
	info, err := os.Stat(filename)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > 64<<10 {
		return nil, errEditCopyRecord
	}
	blob, err := os.ReadFile(filename)
	if err != nil {
		return nil, errEditCopyRecord
	}
	plain, err := crypt(blob, true)
	var record editCopyRecord
	if err != nil || json.Unmarshal(plain, &record) != nil || record.ProfileID == "" ||
		len(record.Fingerprint) != 64 || !validEditCopyPath(record.Remote) ||
		!filepath.IsAbs(record.Local) || !strongEditETag(record.ETag) {
		return nil, errEditCopyRecord
	}
	return &record, nil
}

func (s *DesktopService) writeEditCopy(record editCopyRecord) error {
	plain, err := json.Marshal(record)
	if err != nil {
		return errEditCopyRecord
	}
	blob, err := crypt(plain, false)
	if err != nil || atomicWriteSettings(filepath.Join(s.dir, editCopyFile), blob) != nil {
		return errEditCopyRecord
	}
	return nil
}

func validEditCopyPath(remote string) bool {
	return remote != "" && validSearchPath(remote) && safeWindowsFileParts(remote)
}

// Strong HTTP entity-tags are quoted opaque values without spaces, internal
// quotes, or control bytes. Never use a weak tag as an If-Match validator.
func strongEditETag(tag string) bool {
	if len(tag) < 2 || len(tag) > 1024 || tag[0] != '"' || tag[len(tag)-1] != '"' {
		return false
	}
	for _, b := range []byte(tag[1 : len(tag)-1]) {
		if b <= ' ' || b == '"' || b == 0x7f {
			return false
		}
	}
	return true
}

func (s *DesktopService) editCopyProfile(id string) (Profile, engineProxy, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	profile, err := s.find(id)
	if err != nil || !supportsWritable(profile) {
		return Profile{}, engineProxy{}, errors.New("编辑副本仅适用于已有的 WebDAV 连接")
	}
	if session := s.writable.Load(); session != nil && (session.ID == "" || session.ID == id) {
		return Profile{}, engineProxy{}, errWritableSession
	}
	if _, _, err = providers.ObjectEngineConfig(profile.Provider, profile.URL, profile.User, profile.Password, profile.HostKey, profile.Storage); err != nil {
		return Profile{}, engineProxy{}, errors.New("WebDAV 连接地址或登录信息不可用")
	}
	route := s.proxy
	config, err := s.normalizeConnectionProxy(profile.Provider, profile.Proxy, nil)
	if err != nil {
		return Profile{}, engineProxy{}, err
	}
	if config != nil {
		route, err = resolveEngineProxy(*config, nil)
	}
	if err != nil || route.warning != "" {
		return Profile{}, engineProxy{}, errors.New("WebDAV 代理配置不可用")
	}
	return profile, route, nil
}

func editCopyClient(route engineProxy) *http.Client {
	transport := route.httpsTransport()
	return &http.Client{
		Transport: transport, Timeout: 10 * time.Minute,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func editCopyRequest(ctx context.Context, p Profile, method, remote, tag string, body io.Reader, size int64) (*http.Request, error) {
	endpoint, err := url.Parse(p.URL)
	if err != nil || !validEditCopyPath(remote) {
		return nil, errors.New("编辑副本路径无法访问")
	}
	// Keep the base URL's escaped path and escape each remote component once.
	parts := strings.Split(remote, "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	endpoint.RawPath = strings.TrimRight(endpoint.EscapedPath(), "/") + "/" + strings.Join(parts, "/")
	endpoint.Path, err = url.PathUnescape(endpoint.RawPath)
	if err != nil {
		return nil, errors.New("编辑副本路径无法访问")
	}
	if size == 0 {
		body = nil
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint.String(), body)
	if err != nil {
		return nil, errors.New("编辑副本请求无法建立")
	}
	if p.User != "" || p.Password != "" {
		req.SetBasicAuth(p.User, p.Password)
	}
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("Cache-Control", "no-cache")
	if tag != "" {
		req.Header.Set("If-Match", tag)
	}
	if method == http.MethodPut {
		req.ContentLength = size
		contentType := mime.TypeByExtension(path.Ext(remote))
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		req.Header.Set("Content-Type", contentType)
	}
	return req, nil
}

func doEditCopy(client *http.Client, req *http.Request) (*http.Response, error) {
	resp, err := client.Do(req)
	if err != nil {
		if req.Context().Err() != nil {
			return nil, errors.New("操作已取消，本机编辑副本会保留")
		}
		return nil, errors.New("WebDAV 请求未完成，请检查连接。本机编辑副本会保留")
	}
	return resp, nil
}

func responseEditETag(resp *http.Response) string {
	tags := resp.Header.Values("ETag")
	if len(tags) == 1 && strongEditETag(tags[0]) {
		return tags[0]
	}
	return ""
}

func (s *DesktopService) StartEditCopy(id, remote string) (string, error) {
	if !validEditCopyPath(remote) || s.chooseMountedFile == nil {
		return "", errors.New("请选择可另存的 WebDAV 文件")
	}
	ctx, finish, err := s.beginOperation("webdav.edit-copy.download")
	if err != nil {
		return "", err
	}
	defer finish()
	s.mu.Lock()
	record, err := s.readEditCopy()
	s.mu.Unlock()
	if err != nil {
		return "", err
	}
	if record != nil {
		return "", errors.New("请先提交或保留当前编辑副本，再选择其他文件")
	}
	p, route, err := s.editCopyProfile(id)
	if err != nil {
		return "", err
	}
	local, err := s.chooseMountedFile(path.Base(remote))
	if err != nil {
		return "", errors.New("无法打开文件保存窗口")
	}
	if local == "" {
		return "", nil
	}
	folder, err := s.localUserFileFolder(local)
	if err != nil {
		return "", err
	}
	defer folder.Close()
	if err = mountedSaveTargetAvailable(local); err != nil {
		return "", err
	}
	client := editCopyClient(route)
	defer client.CloseIdleConnections()
	probe, err := editCopyRequest(ctx, p, http.MethodHead, remote, `"webdavmount-`+randomID()+`"`, nil, 0)
	if err != nil {
		return "", err
	}
	check, err := doEditCopy(client, probe)
	if err != nil {
		return "", err
	}
	check.Body.Close()
	if check.StatusCode != http.StatusPreconditionFailed {
		return "", errEditCopyUnsupported
	}
	request, err := editCopyRequest(ctx, p, http.MethodGet, remote, "", nil, 0)
	if err != nil {
		return "", err
	}
	resp, err := doEditCopy(client, request)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	tag := responseEditETag(resp)
	if resp.StatusCode != http.StatusOK || tag == "" || resp.Header.Get("Content-Range") != "" ||
		(resp.Header.Get("Content-Encoding") != "" && !strings.EqualFold(resp.Header.Get("Content-Encoding"), "identity")) {
		return "", errEditCopyUnsupported
	}
	if resp.ContentLength > editCopyMax {
		return "", errors.New("编辑副本暂支持最大 256 MiB 的文件")
	}
	tmp, err := os.CreateTemp(filepath.Dir(local), ".wdm-edit-*.tmp")
	if err != nil {
		return "", errors.New("无法创建本机编辑副本")
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	copied, err := io.Copy(tmp, io.LimitReader(resp.Body, editCopyMax+1))
	if err != nil || copied > editCopyMax || (resp.ContentLength >= 0 && copied != resp.ContentLength) {
		return "", errors.New("未完整收到文件，或文件超过 256 MiB")
	}
	if err = publishMountedSave(ctx, tmp, local); err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	err = s.writeEditCopy(editCopyRecord{ProfileID: id, Fingerprint: writableFingerprint(p), Remote: remote, Local: local, ETag: tag})
	if err != nil {
		return "", errors.New("本机副本已保存，但无法记录远端版本。本次副本不能直接提交")
	}
	return local, nil
}

func (s *DesktopService) CommitEditCopy() error {
	ctx, finish, err := s.beginOperation("webdav.edit-copy.upload")
	if err != nil {
		return err
	}
	defer finish()
	s.mu.Lock()
	record, err := s.readEditCopy()
	s.mu.Unlock()
	if err != nil {
		return err
	}
	if record == nil {
		return errors.New("没有可提交的编辑副本")
	}
	p, route, err := s.editCopyProfile(record.ProfileID)
	if err != nil {
		return err
	}
	if writableFingerprint(p) != record.Fingerprint {
		return errors.New("网盘连接已变化，本机编辑副本已保留")
	}
	folder, err := s.localUserFileFolder(record.Local)
	if err != nil {
		return err
	}
	defer folder.Close()
	file, _, err := migrationOpen(record.Local, windows.GENERIC_READ, windows.OPEN_EXISTING, false)
	if err != nil {
		return errors.New("无法读取本机副本，请保存文件并关闭编辑器后重试")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > editCopyMax {
		return errors.New("编辑副本须为不超过 256 MiB 的本机普通文件")
	}
	client := editCopyClient(route)
	defer client.CloseIdleConnections()
	head, err := editCopyRequest(ctx, p, http.MethodHead, record.Remote, record.ETag, nil, 0)
	if err != nil {
		return err
	}
	current, err := doEditCopy(client, head)
	if err != nil {
		return err
	}
	current.Body.Close()
	if current.StatusCode == http.StatusPreconditionFailed || current.StatusCode == http.StatusNotFound ||
		(current.StatusCode == http.StatusOK && responseEditETag(current) != "" && responseEditETag(current) != record.ETag) {
		return errEditCopyConflict
	}
	if current.StatusCode != http.StatusOK || responseEditETag(current) != record.ETag {
		return errEditCopyUnsupported
	}
	if err = verifyConditionalEditPut(ctx, client, p, record.Remote); err != nil {
		return err
	}
	put, err := editCopyRequest(ctx, p, http.MethodPut, record.Remote, record.ETag, io.NewSectionReader(file, 0, info.Size()), info.Size())
	if err != nil {
		return err
	}
	resp, err := doEditCopy(client, put)
	if err != nil {
		return err
	}
	resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusPreconditionFailed, http.StatusConflict, http.StatusNotFound:
		return errEditCopyConflict
	case http.StatusOK, http.StatusNoContent:
		s.mu.Lock()
		defer s.mu.Unlock()
		if err = os.Remove(filepath.Join(s.dir, editCopyFile)); err != nil {
			return errors.New("远端已确认保存。无法清除编辑记录，本机副本已保留")
		}
		return nil
	default:
		return errors.New("服务器未确认保存，编辑副本已保留。可先核对远端，再尝试提交")
	}
}

func (s *DesktopService) ForgetEditCopy() error {
	_, finish, err := s.beginOperation("webdav.edit-copy.forget")
	if err != nil {
		return err
	}
	defer finish()
	s.mu.Lock()
	defer s.mu.Unlock()
	filename := filepath.Join(s.dir, editCopyFile)
	if err = noReparse(filename); err != nil {
		return errEditCopyRecord
	}
	if err = os.Remove(filename); err != nil && !errors.Is(err, os.ErrNotExist) {
		return errors.New("无法移除编辑记录")
	}
	return nil
}

func (s *DesktopService) OpenEditCopyFolder() error {
	s.mu.Lock()
	record, err := s.readEditCopy()
	s.mu.Unlock()
	if err != nil {
		return err
	}
	if record == nil || s.openDirectory == nil {
		return errors.New("没有可打开的本机副本目录")
	}
	folder, err := s.localUserFileFolder(record.Local)
	if err != nil {
		return err
	}
	defer folder.Close()
	if err = s.openDirectory(filepath.Dir(record.Local)); err != nil {
		return errors.New("无法打开本机副本目录")
	}
	return nil
}

// RFC-compliant HEAD does not prove that a DAV handler evaluates If-Match
// before PUT. Exercise PUT on an app-owned random sibling before touching the
// original file. Only a successfully removed probe permits the real upload.
func verifyConditionalEditPut(ctx context.Context, client *http.Client, p Profile, remote string) (result error) {
	nonce := randomID()
	probe := path.Join(path.Dir(remote), "WebDAVMount-check-"+nonce+path.Ext(remote))
	probeTag := ""
	cleanupNeeded := false
	defer func() {
		if !cleanupNeeded {
			return
		}
		cleanup, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		req, err := editCopyRequest(cleanup, p, http.MethodDelete, probe, probeTag, nil, 0)
		if err == nil {
			var resp *http.Response
			resp, err = doEditCopy(client, req)
			if err == nil {
				resp.Body.Close()
				switch resp.StatusCode {
				case http.StatusOK, http.StatusNoContent, http.StatusNotFound:
					return
				}
			}
		}
		result = errors.New("验证文件未能确认清理，未提交原文件。请检查远端文件 " + probe)
	}()

	body := "WebDAVMount conditional PUT " + nonce
	create, err := editCopyRequest(ctx, p, http.MethodPut, probe, "", strings.NewReader(body), int64(len(body)))
	if err != nil {
		return err
	}
	create.Header.Set("If-None-Match", "*")
	if err := ctx.Err(); err != nil {
		return errors.New("操作已取消，本机编辑副本会保留")
	}
	cleanupNeeded = true
	resp, err := doEditCopy(client, create)
	if err != nil {
		return err
	}
	resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusCreated, http.StatusNoContent, http.StatusOK:
	case http.StatusPreconditionFailed, http.StatusConflict, http.StatusUnauthorized, http.StatusForbidden, http.StatusMethodNotAllowed, http.StatusNotFound:
		cleanupNeeded = false
		return errEditCopyUnsupported
	default:
		return errEditCopyUnsupported
	}

	stale := "WebDAVMount invalid condition " + nonce
	wrong, err := editCopyRequest(ctx, p, http.MethodPut, probe, `"wdm-mismatch-`+nonce+`"`, strings.NewReader(stale), int64(len(stale)))
	if err != nil {
		return err
	}
	resp, err = doEditCopy(client, wrong)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusPreconditionFailed {
		return errEditCopyUnsupported
	}

	get, err := editCopyRequest(ctx, p, http.MethodGet, probe, "", nil, 0)
	if err != nil {
		return err
	}
	resp, err = doEditCopy(client, get)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	got, err := io.ReadAll(io.LimitReader(resp.Body, int64(len(body)+1)))
	probeTag = responseEditETag(resp)
	if err != nil || resp.StatusCode != http.StatusOK || string(got) != body || probeTag == "" {
		return errEditCopyUnsupported
	}
	return nil
}
