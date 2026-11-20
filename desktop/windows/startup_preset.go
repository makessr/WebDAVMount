package desktop

import (
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const startupPresetFile = "startup-preset"
const startupPresetIDLength = 32

type StartupPresetSetting struct {
	ID      string `json:"id"`
	Warning string `json:"warning,omitempty"`
}

func (s *DesktopService) StartupPreset() StartupPresetSetting {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, err := s.readStartupPresetLocked()
	if err != nil {
		return StartupPresetSetting{Warning: err.Error()}
	}
	return StartupPresetSetting{ID: id}
}

func (s *DesktopService) SaveStartupPreset(id string) error {
	_, finish, err := s.beginOperation("mount.presets")
	if err != nil {
		return err
	}
	defer finish()
	s.mu.Lock()
	defer s.mu.Unlock()
	if id != "" {
		if !validStartupPresetID(id) {
			return errors.New("请选择有效的启动挂载方案")
		}
		if _, _, err := s.startupPresetProfilesLocked(id); err != nil {
			return err
		}
	}
	if err := atomicWriteSettings(filepath.Join(s.dir, startupPresetFile), []byte(id)); err != nil {
		return errors.New("无法保存启动挂载设置，请检查本机配置目录")
	}
	return nil
}

func validStartupPresetID(id string) bool {
	if len(id) != startupPresetIDLength || strings.ToLower(id) != id {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}

func (s *DesktopService) readStartupPresetLocked() (string, error) {
	path := filepath.Join(s.dir, startupPresetFile)
	unreadable := errors.New("启动挂载设置无法读取，请在「挂载方案」重新保存")
	if err := noReparse(path); err != nil {
		return "", unreadable
	}
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", unreadable
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return "", unreadable
	}
	data, err := io.ReadAll(io.LimitReader(file, startupPresetIDLength+1))
	if err != nil || (len(data) > 0 && !validStartupPresetID(string(data))) {
		return "", unreadable
	}
	return string(data), nil
}

func (s *DesktopService) startupPresetProfilesLocked(id string) (MountPreset, []Profile, error) {
	preset, profiles, err := s.mountPresetProfilesLocked(id)
	if err != nil {
		return preset, nil, errors.New(err.Error() + "。请在「挂载方案」检查启动设置")
	}
	drives := make(map[string]bool, len(profiles))
	for _, p := range profiles {
		drive := strings.ToUpper(p.Drive)
		if drives[drive] {
			return preset, nil, errors.New("启动方案有重复盘符，请在「挂载方案」调整盘符或更换方案")
		}
		drives[drive] = true
	}
	return preset, profiles, nil
}

func (s *DesktopService) startupSelectionLocked() (id, name string, profiles []Profile, err error) {
	id, err = s.readStartupPresetLocked()
	if err != nil {
		return "", "", nil, err
	}
	if id != "" {
		preset, profiles, err := s.startupPresetProfilesLocked(id)
		return id, preset.Name, profiles, err
	}
	for _, p := range s.profiles {
		if p.AutoMount {
			profiles = append(profiles, p)
		}
	}
	return "", "", profiles, nil
}
