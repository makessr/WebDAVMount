package desktop

import "errors"

type ConnectionCheckReportRow struct {
	State string `json:"state"`
	Mount string `json:"mount"`
}

func validConnectionCheckState(state string) bool {
	return state == "success" || state == "failed" || state == "skipped"
}

func validConnectionCheckMount(mount string) bool {
	switch mount {
	case "挂载状态未知", "已挂载", "未挂载", "盘符被其他挂载占用", "盘符被 Windows 占用":
		return true
	}
	return false
}

// Only these fixed values cross the export boundary. Profile identifiers,
// endpoint details, timestamps, and backend messages are excluded.
func (s *DesktopService) ExportConnectionCheckReport(rows []ConnectionCheckReportRow) (string, error) {
	if len(rows) == 0 || len(rows) > 1000 {
		return "", errors.New("请显示 1 至 1000 条体检结果后导出")
	}
	for _, row := range rows {
		if !validConnectionCheckState(row.State) {
			return "", errors.New("报告包含无效的体检状态")
		}
		if !validConnectionCheckMount(row.Mount) {
			return "", errors.New("报告包含无效的盘符状态")
		}
	}
	_, finish, err := s.beginOperation("connections.check.export")
	if err != nil {
		return "", err
	}
	defer finish()
	report := struct {
		Version string                     `json:"appVersion"`
		Rows    []ConnectionCheckReportRow `json:"results"`
	}{appVersion, rows}
	return exportJSONReportAs(report, s.chooseCheckReportFile)
}
