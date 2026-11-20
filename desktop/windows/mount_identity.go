package desktop

import (
	"errors"
	"strings"
)

// rclone 1.76 reports both Fs (the configuration identity) and MountPoint.
// A drive letter alone cannot identify a connection: profiles can share it.
func identifiedMounts(result map[string]any, profiles []Profile) ([]map[string]any, error) {
	list, ok := result["mountPoints"].([]any)
	if !ok {
		return nil, errors.New("无法确认挂载状态")
	}
	mounts := []map[string]any{}
	seen := map[string]bool{}
	for _, entry := range list {
		m, ok := entry.(map[string]any)
		if !ok {
			return nil, errors.New("无法确认挂载状态")
		}
		point, fs := mountIdentity(m)
		if point == "" || fs == "" {
			return nil, errors.New("无法确认磁盘所属连接，已停止操作")
		}
		key := strings.ToUpper(point)
		if seen[key] {
			return nil, errors.New("磁盘挂载信息重复，已停止操作")
		}
		seen[key] = true
		mount := map[string]any{"MountPoint": point, "ProfileID": ""}
		for _, p := range profiles {
			if !strings.EqualFold(p.Drive, point) {
				continue
			}
			writable := writableMountProfileSupported(p) && writableRemoteMatches(p, fs)
			if fs == p.remoteFS() || writable {
				mount["ProfileID"] = p.ID
				if writable {
					mount["Writable"] = true
				}
				break
			}
		}
		mounts = append(mounts, mount)
	}
	return mounts, nil
}

func mountIdentity(m map[string]any) (point, remote string) {
	point, _ = m["MountPoint"].(string)
	if point == "" {
		point, _ = m["mountPoint"].(string)
	}
	remote, _ = m["Fs"].(string)
	if remote == "" {
		remote, _ = m["fs"].(string)
	}
	return point, remote
}

func connectionMountState(result map[string]any, p Profile) (present, owned bool, err error) {
	mounts, err := identifiedMounts(result, []Profile{p})
	if err != nil {
		return false, false, err
	}
	for _, m := range mounts {
		if strings.EqualFold(m["MountPoint"].(string), p.Drive) {
			if present {
				return false, false, errors.New("磁盘挂载信息重复，已停止操作")
			}
			present = true
			owned = m["ProfileID"] == p.ID
		}
	}
	return present, owned, nil
}

func isWritableMount(result map[string]any, p Profile) bool {
	mounts, _ := identifiedMounts(result, []Profile{p})
	for _, mount := range mounts {
		if mount["ProfileID"] == p.ID && mount["Writable"] == true {
			return true
		}
	}
	return false
}
