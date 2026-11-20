package main

import "errors"

// Only a verified, currently mounted drive root is handed to Windows Shell.
// Remote names, URLs and file contents are never used as executable arguments.
func (s *DesktopService) OpenMountedDrive(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.find(id)
	if err != nil {
		return err
	}
	if !s.engineOnline() {
		return errors.New("挂载服务不可用，请先恢复挂载")
	}
	result, err := s.call("mount/listmounts", nil)
	if err != nil {
		return err
	}
	_, owned, err := connectionMountState(result, p)
	if err != nil {
		return err
	}
	if !owned {
		return errors.New("请先挂载此网盘，再打开磁盘")
	}
	if s.openDirectory == nil {
		return errors.New("当前环境无法打开资源管理器")
	}
	if err = s.openDirectory(p.Drive + `\`); err != nil {
		return errors.New("无法打开资源管理器，请手动从此电脑访问磁盘")
	}
	return nil
}
