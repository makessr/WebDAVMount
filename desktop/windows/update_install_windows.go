package desktop

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"webdavmount/internal/updatefeed"

	"golang.org/x/sys/windows"
)

// This is the only child explicitly detached from the application's kill job.
// rclone and other ordinary children remain contained. The installer waits for
// this process to exit before inspecting or replacing the installed payload.
func startUpdateInstaller(path string) error {
	cmd := exec.Command(path, "/WAITPID="+strconv.Itoa(os.Getpid()))
	cmd.Dir = filepath.Dir(path)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_BREAKAWAY_FROM_JOB}
	if err := cmd.Start(); err != nil {
		return err
	}
	// Start succeeded: even a handle-release error must not leave the
	// installer waiting on a client that mistakenly decided to stay open.
	_ = cmd.Process.Release()
	return nil
}

func openVerifiedUpdateInstaller(ctx context.Context, path string, m *updatefeed.Manifest) (*os.File, error) {
	if !filepath.IsAbs(path) || strings.HasPrefix(path, `\\`) || !strings.EqualFold(filepath.Ext(path), ".exe") {
		return nil, errors.New("安装包路径无效")
	}
	if err := noReparse(path); err != nil {
		return nil, err
	}
	ptr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(ptr, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(h), path)
	fail := func(e error) (*os.File, error) { f.Close(); return nil, e }
	var info windows.ByHandleFileInformation
	if err = windows.GetFileInformationByHandle(h, &info); err != nil {
		return fail(err)
	}
	if info.NumberOfLinks != 1 || info.FileAttributes&(windows.FILE_ATTRIBUTE_DIRECTORY|windows.FILE_ATTRIBUTE_REPARSE_POINT) != 0 {
		return fail(errors.New("安装包不是普通文件"))
	}
	stat, err := f.Stat()
	if err != nil {
		return fail(err)
	}
	if stat.Size() != m.Size {
		return fail(errors.New("安装包大小已变化"))
	}
	hash := sha256.New()
	buffer := make([]byte, 1<<20)
	for {
		if err = ctx.Err(); err != nil {
			return fail(err)
		}
		n, e := f.Read(buffer)
		if n > 0 {
			hash.Write(buffer[:n])
		}
		if e == io.EOF {
			break
		}
		if e != nil {
			return fail(e)
		}
	}
	if hex.EncodeToString(hash.Sum(nil)) != strings.ToLower(m.SHA256) {
		return fail(errors.New("安装包校验失败"))
	}
	return f, nil
}

// Retrying never accepts a path from JavaScript: only the verified download's
// in-memory path and signed envelope are eligible.
func (s *DesktopService) InstallDownloadedUpdate() (UpdateState, error) {
	ctx, finish, err := s.beginOperation("update.install")
	if err != nil {
		return s.UpdateInfo(), err
	}
	defer finish()
	return s.installDownloadedUpdate(ctx)
}

func (s *DesktopService) installDownloadedUpdate(ctx context.Context) (UpdateState, error) {
	s.updateMu.Lock()
	path := s.updateState.DownloadedPath
	envelope := append([]byte{}, s.updateEnvelope...)
	launch := s.startUpdateInstaller
	s.updateMu.Unlock()
	failure := func(message string) (UpdateState, error) {
		s.updateMu.Lock()
		s.updateState.State = "downloaded"
		s.updateState.Message = message
		s.updateMu.Unlock()
		return s.UpdateInfo(), errors.New(message)
	}
	if path == "" || launch == nil || s.exit == nil {
		return failure("当前环境无法启动安装程序，请保留安装包并手动安装")
	}
	trust, _ := s.updateTrust()
	if trust == nil {
		return failure("更新信任配置不可用，未启动安装程序")
	}
	m, err := updatefeed.Verify(envelope, trust.Policy)
	if err != nil {
		return failure("更新清单已失效，请重新检查更新；未启动安装程序")
	}
	f, err := openVerifiedUpdateInstaller(ctx, path, &m)
	if err != nil {
		return failure("安装包无法读取或校验未通过，未启动安装程序；请重新下载")
	}
	defer f.Close()
	trust, _ = s.updateTrust()
	if trust == nil {
		return failure("更新信任配置不可用，未启动安装程序")
	}
	if _, err = updatefeed.Verify(envelope, trust.Policy); err != nil {
		return failure("更新清单已失效，未启动安装程序")
	}
	if err = ctx.Err(); err != nil {
		return failure("操作已取消")
	}
	s.opMu.Lock()
	if err = s.writableGuard(); err != nil {
		s.opMu.Unlock()
		return failure(err.Error())
	}
	if s.closing {
		s.opMu.Unlock()
		return failure("程序正在退出，安装包已保留")
	}
	err = launch(path)
	if err == nil {
		s.closing = true
	}
	s.opMu.Unlock()
	if err != nil {
		return failure("安装程序未能启动，已下载文件保留；请点击重试安装")
	}
	s.updateMu.Lock()
	s.updateState.State = "installing"
	s.updateState.Message = "安装程序已启动，正在退出客户端并停止传输…"
	s.updateMu.Unlock()
	go s.exit()
	return s.UpdateInfo(), nil
}
