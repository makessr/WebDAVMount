package desktop

import (
	"context"
	"errors"
	"math"
	"path"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const searchDirectoryLimit = 500
const searchEntryLimit = 50000
const searchResultLimit = 1000
const searchDepthLimit = 64
const searchTimeLimit = 10 * time.Minute

type RemoteSearchRequest struct {
	ProfileID string `json:"profileId"`
	Base      string `json:"base"`
	Query     string `json:"query"`
	Kind      string `json:"kind"`
	Recursive bool   `json:"recursive"`
}
type RemoteSearchItem struct {
	Path    string
	Name    string
	Size    int64
	IsDir   bool
	ModTime string
}
type RemoteSearchState struct {
	Active            bool               `json:"active"`
	Phase             string             `json:"phase"`
	Message           string             `json:"message"`
	ProfileID         string             `json:"profileId"`
	Base              string             `json:"base"`
	Query             string             `json:"query"`
	Directories       int                `json:"directories"`
	Scanned           int                `json:"scanned"`
	Matched           int                `json:"matched"`
	FailedDirectories int                `json:"failedDirectories"`
	Skipped           int                `json:"skipped"`
	Limit             string             `json:"limit"`
	Items             []RemoteSearchItem `json:"items,omitempty"`
}

func validSearchPath(p string) bool {
	if p == "" {
		return true
	}
	if len(p) > 4096 || strings.HasPrefix(p, "/") || strings.Contains(p, `\`) || !utf8.ValidString(p) || path.Clean(p) != p || p == "." || p == ".." || strings.HasPrefix(p, "../") {
		return false
	}
	for _, r := range p {
		if r < 32 || r == 127 {
			return false
		}
	}
	return true
}

func validateSearchRequest(r *RemoteSearchRequest) error {
	r.Query = strings.TrimSpace(r.Query)
	if r.ProfileID == "" || !validSearchPath(r.Base) {
		return errors.New("搜索位置无效，请重新选择网盘目录")
	}
	if !utf8.ValidString(r.Query) || utf8.RuneCountInString(r.Query) < 1 || utf8.RuneCountInString(r.Query) > 128 {
		return errors.New("请输入1至128个字符的文件名关键词")
	}
	for _, c := range r.Query {
		if c < 32 || c == 127 {
			return errors.New("搜索关键词不能包含控制字符")
		}
	}
	if r.Kind != "all" && r.Kind != "files" && r.Kind != "folders" {
		return errors.New("请选择有效的搜索类型")
	}
	return nil
}

func parseSearchItem(raw any, directory string) (RemoteSearchItem, bool) {
	v, ok := raw.(map[string]any)
	if !ok {
		return RemoteSearchItem{}, false
	}
	p, ok := v["Path"].(string)
	if !ok || p == "" || !validSearchPath(p) {
		return RemoteSearchItem{}, false
	}
	parent := path.Dir(p)
	if parent == "." {
		parent = ""
	}
	if parent != directory {
		return RemoteSearchItem{}, false
	}
	dir, ok := v["IsDir"].(bool)
	if !ok {
		return RemoteSearchItem{}, false
	}
	item := RemoteSearchItem{Path: p, Name: path.Base(p), IsDir: dir, Size: -1}
	if size, ok := v["Size"].(float64); ok && size >= 0 && size < math.MaxInt64 {
		item.Size = int64(size)
	}
	item.ModTime, _ = v["ModTime"].(string)
	if len(item.ModTime) > 64 {
		item.ModTime = ""
	}
	return item, true
}

type remoteSearchTracker struct {
	mu    sync.Mutex
	state RemoteSearchState
}

func (t *remoteSearchTracker) update(fn func(*RemoteSearchState)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	fn(&t.state)
}
func (t *remoteSearchTracker) snapshot(includeItems bool) RemoteSearchState {
	t.mu.Lock()
	defer t.mu.Unlock()
	result := t.state
	if includeItems {
		result.Items = append([]RemoteSearchItem{}, t.state.Items...)
	} else {
		result.Items = nil
	}
	return result
}
func (s *DesktopService) RemoteSearchProgress() RemoteSearchState {
	return s.remoteSearch.snapshot(false)
}
func (s *DesktopService) RemoteSearchResults() RemoteSearchState {
	return s.remoteSearch.snapshot(true)
}
func (s *DesktopService) ClearRemoteSearch() error {
	s.remoteSearch.mu.Lock()
	defer s.remoteSearch.mu.Unlock()
	if s.remoteSearch.state.Active {
		return errors.New("请先停止搜索")
	}
	s.remoteSearch.state = RemoteSearchState{}
	return nil
}

type remoteSearchLimits struct {
	directories, entries, results int
}

// Reads only directory metadata, one directory request at a time. The service
// mutex pins the selected profile/engine for the operation, like Browse.
func (s *DesktopService) SearchRemote(request RemoteSearchRequest) (result RemoteSearchState, err error) {
	if err = validateSearchRequest(&request); err != nil {
		return result, err
	}
	ctx, finish, err := s.beginOperation("remote.search")
	if err != nil {
		return result, err
	}
	defer finish()
	ctx, cancel := context.WithTimeout(ctx, searchTimeLimit)
	defer cancel()
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.find(request.ProfileID)
	if err != nil {
		return result, err
	}
	return s.searchRemoteLocked(ctx, p, request, &s.remoteSearch, remoteSearchLimits{searchDirectoryLimit, searchEntryLimit, searchResultLimit})
}

func (s *DesktopService) searchRemoteLocked(ctx context.Context, p Profile, request RemoteSearchRequest, tracker *remoteSearchTracker, limits remoteSearchLimits) (result RemoteSearchState, err error) {
	tracker.update(func(state *RemoteSearchState) {
		*state = RemoteSearchState{Active: true, Phase: "searching", Message: "正在读取目录并搜索名称…", ProfileID: p.ID, Base: request.Base, Query: request.Query}
	})
	started := time.Now()
	defer func() {
		diagnosticErr := err
		tracker.update(func(state *RemoteSearchState) {
			state.Active = false
			switch {
			case ctx.Err() != nil:
				diagnosticErr = ctx.Err()
				state.Phase = "cancelled"
				state.Message = "搜索已停止，已找到的结果保留，结果不完整"
				if errors.Is(ctx.Err(), context.DeadlineExceeded) {
					state.Phase = "limited"
					state.Limit = "time"
					state.Message = "已达到10分钟搜索上限，保留当前结果"
				}
				err = nil
			case err != nil:
				state.Phase = "failed"
				state.Message = "当前搜索目录无法读取，请检查网盘连接后重试"
			case state.Limit != "":
				state.Phase = "limited"
				state.Message = "已达到搜索范围上限，结果不完整；请缩小目录或关键词后重试"
				if state.Limit == "service" {
					state.Message = "服务异常或限流，已停止继续请求；当前结果不完整"
				}
			case state.FailedDirectories > 0 || state.Skipped > 0:
				state.Phase = "partial"
				state.Message = "部分目录或条目未能读取，已找到的结果保留"
			default:
				state.Phase = "done"
				state.Message = "搜索完成"
			}
		})
		result = tracker.snapshot(true)
		s.recordDiagnostic("remote.search", diagnosticErr, time.Since(started))
		if err != nil {
			err = errors.New(result.Message)
		}
	}()
	type location struct {
		path  string
		depth int
	}
	queue := []location{{request.Base, 0}}
	visited := map[string]bool{request.Base: true}
	needle := strings.ToLower(request.Query)
	seen := map[string]bool{}
	for len(queue) > 0 {
		if err = ctx.Err(); err != nil {
			return result, err
		}
		state := tracker.snapshot(false)
		if state.Directories >= limits.directories {
			tracker.update(func(p *RemoteSearchState) { p.Limit = "directories" })
			break
		}
		if state.Directories > 0 {
			if s.waitRemoteSearch != nil {
				if err = s.waitRemoteSearch(ctx); err != nil {
					return result, err
				}
			} else {
				timer := time.NewTimer(200 * time.Millisecond)
				select {
				case <-ctx.Done():
					timer.Stop()
					return result, ctx.Err()
				case <-timer.C:
				}
			}
		}
		current := queue[0]
		queue = queue[1:]
		data, e := s.callProfileContext(ctx, p, "operations/list", map[string]any{"remote": current.path, "opt": map[string]bool{"recurse": false}})
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		tracker.update(func(p *RemoteSearchState) { p.Directories++ })
		if e != nil {
			if current.path == request.Base {
				return result, e
			}
			tracker.update(func(p *RemoteSearchState) { p.FailedDirectories++ })
			var access remoteAccessError
			if errors.As(e, &access) && access.stopSearch {
				tracker.update(func(p *RemoteSearchState) { p.Limit = "service" })
				return result, nil
			}
			switch diagnosticCategory(e) {
			case "rate_limited", "tls_certificate", "timeout", "engine_unavailable", "network_unavailable":
				tracker.update(func(p *RemoteSearchState) { p.Limit = "service" })
				return result, nil
			}
			continue
		}
		value, exists := data["list"]
		items, ok := value.([]any)
		if !exists || (!ok && value != nil) {
			if current.path == request.Base {
				return result, errors.New("目录返回格式无效")
			}
			tracker.update(func(p *RemoteSearchState) { p.FailedDirectories++ })
			continue
		}
		for _, raw := range items {
			if err = ctx.Err(); err != nil {
				return result, err
			}
			state = tracker.snapshot(false)
			if state.Scanned >= limits.entries {
				tracker.update(func(p *RemoteSearchState) { p.Limit = "entries" })
				return result, nil
			}
			tracker.update(func(p *RemoteSearchState) { p.Scanned++ })
			item, valid := parseSearchItem(raw, current.path)
			if !valid || seen[item.Path] {
				tracker.update(func(p *RemoteSearchState) { p.Skipped++ })
				continue
			}
			seen[item.Path] = true
			if item.IsDir && request.Recursive && !visited[item.Path] {
				if current.depth >= searchDepthLimit {
					tracker.update(func(p *RemoteSearchState) {
						if p.Limit == "" {
							p.Limit = "depth"
						}
					})
				} else if len(visited) >= limits.directories {
					tracker.update(func(p *RemoteSearchState) {
						if p.Limit == "" {
							p.Limit = "directories"
						}
					})
				} else {
					visited[item.Path] = true
					queue = append(queue, location{item.Path, current.depth + 1})
				}
			}
			if strings.Contains(strings.ToLower(item.Name), needle) && (request.Kind == "all" || (request.Kind == "files" && !item.IsDir) || (request.Kind == "folders" && item.IsDir)) {
				tracker.update(func(p *RemoteSearchState) { p.Items = append(p.Items, item); p.Matched = len(p.Items) })
				if state.Matched+1 >= limits.results {
					tracker.update(func(p *RemoteSearchState) { p.Limit = "results" })
					return result, nil
				}
			}
		}
	}
	return result, nil
}
