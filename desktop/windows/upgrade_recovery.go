package desktop

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

var upgradeBackupName = regexp.MustCompile(`^WebDAVMount-upgrade-backup-[a-f0-9]{32}$`)

// Never load credentials or start the mount engine from an unfinished package.
// The new external installer/updater owns verification and restoration.
func checkPendingProgramUpgrade(root string) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !upgradeBackupName.MatchString(entry.Name()) {
			continue
		}
		record := filepath.Join(root, entry.Name(), "UpgradeRecord.json")
		if _, e := os.Lstat(record); os.IsNotExist(e) {
			continue
		}
		problem := fmt.Errorf("检测到未完成的程序升级。请重新运行新版安装包；便携版请使用新版 Update-WebDAVMount.ps1 恢复后升级。备份位于：%s", filepath.Join(root, entry.Name()))
		if e := noReparse(record); e != nil {
			return problem
		}
		info, e := os.Stat(record)
		if e != nil || !info.Mode().IsRegular() || info.Size() > 4<<20 {
			return problem
		}
		data, e := os.ReadFile(record)
		if e != nil {
			return problem
		}
		var state struct {
			Status string `json:"status"`
		}
		if json.Unmarshal(data, &state) != nil {
			return problem
		}
		if state.Status != "complete" && state.Status != "rolled_back" && state.Status != "recovered" {
			return problem
		}
	}
	return nil
}
