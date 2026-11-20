package main

import (
	"errors"
	"strings"
	"unicode/utf8"
)

const directoryFavoriteLimit = 100

type DirectoryFavorite struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Path string `json:"path"`
}

func validateDirectoryFavorites(items []DirectoryFavorite) error {
	if len(items) > directoryFavoriteLimit {
		return errors.New("常用目录数据超出上限，请保留配置文件排查")
	}
	ids, paths := map[string]bool{}, map[string]bool{}
	for _, item := range items {
		if item.ID == "" || len(item.ID) > 80 || ids[item.ID] || paths[item.Path] || !validSearchPath(item.Path) || !validFavoriteName(item.Name) {
			return errors.New("常用目录数据无法读取，请保留配置文件排查")
		}
		ids[item.ID] = true
		paths[item.Path] = true
	}
	return nil
}
func validFavoriteName(name string) bool {
	if strings.TrimSpace(name) != name || name == "" || !utf8.ValidString(name) || utf8.RuneCountInString(name) > 80 {
		return false
	}
	for _, r := range name {
		if r < 32 || r == 127 {
			return false
		}
	}
	return true
}
func (s *DesktopService) DirectoryFavorites(profileID string) ([]DirectoryFavorite, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.find(profileID)
	if err != nil {
		return nil, err
	}
	if err = validateDirectoryFavorites(p.DirectoryFavorites); err != nil {
		return nil, err
	}
	return append([]DirectoryFavorite{}, p.DirectoryFavorites...), nil
}

// A single encrypted profile write commits local preferences with connection
// data. The engine identity and mounts remain untouched. Always copy slices so
// existing snapshots/import baselines cannot change before publication.
func (s *DesktopService) changeDirectoryFavorites(profileID string, change func([]DirectoryFavorite) ([]DirectoryFavorite, error)) ([]DirectoryFavorite, error) {
	_, finish, err := s.beginOperation("favorites.save")
	if err != nil {
		return nil, err
	}
	defer finish()
	s.mu.Lock()
	defer s.mu.Unlock()
	index := -1
	for i, p := range s.profiles {
		if p.ID == profileID {
			index = i
			break
		}
	}
	if index < 0 {
		return nil, errors.New("网盘连接不存在，请刷新列表")
	}
	old := s.profiles[index].DirectoryFavorites
	if err = validateDirectoryFavorites(old); err != nil {
		return nil, err
	}
	items, err := change(append([]DirectoryFavorite{}, old...))
	if err != nil {
		return nil, err
	}
	if err = validateDirectoryFavorites(items); err != nil {
		return nil, err
	}
	next := append([]Profile{}, s.profiles...)
	next[index].DirectoryFavorites = items
	if err = s.persist(next); err != nil {
		return nil, errors.New("常用目录保存失败，原收藏和连接设置已保留")
	}
	s.profiles = next
	return append([]DirectoryFavorite{}, items...), nil
}
func (s *DesktopService) SaveDirectoryFavorite(profileID string, item DirectoryFavorite) ([]DirectoryFavorite, error) {
	item.Name = strings.TrimSpace(item.Name)
	if !validFavoriteName(item.Name) {
		return nil, errors.New("收藏名称须为1至80个字符，不能含换行或控制字符")
	}
	if !validSearchPath(item.Path) {
		return nil, errors.New("收藏位置无效，请从网盘目录中重新选择")
	}
	return s.changeDirectoryFavorites(profileID, func(items []DirectoryFavorite) ([]DirectoryFavorite, error) {
		if item.ID != "" {
			for i, old := range items {
				if old.ID == item.ID {
					if old.Path != item.Path {
						return nil, errors.New("收藏路径不能通过重命名修改，请新增收藏")
					}
					items[i].Name = item.Name
					return items, nil
				}
			}
			return nil, errors.New("收藏已变化，请刷新后重试")
		}
		for _, old := range items {
			if old.Path == item.Path {
				return nil, errors.New("此目录已经收藏，可在常用目录中重命名")
			}
		}
		if len(items) >= directoryFavoriteLimit {
			return nil, errors.New("每个网盘最多收藏100个目录，请先移除不需要的收藏")
		}
		item.ID = randomID()
		return append(items, item), nil
	})
}
func (s *DesktopService) RemoveDirectoryFavorite(profileID, id string) ([]DirectoryFavorite, error) {
	return s.changeDirectoryFavorites(profileID, func(items []DirectoryFavorite) ([]DirectoryFavorite, error) {
		for i, item := range items {
			if item.ID == id {
				return append(items[:i], items[i+1:]...), nil
			}
		}
		return nil, errors.New("收藏不存在，请刷新后重试")
	})
}
func (s *DesktopService) MoveDirectoryFavorite(profileID, id string, direction int) ([]DirectoryFavorite, error) {
	if direction != -1 && direction != 1 {
		return nil, errors.New("收藏移动方向无效")
	}
	return s.changeDirectoryFavorites(profileID, func(items []DirectoryFavorite) ([]DirectoryFavorite, error) {
		for i, item := range items {
			if item.ID == id {
				next := i + direction
				if next >= 0 && next < len(items) {
					items[i], items[next] = items[next], items[i]
				}
				return items, nil
			}
		}
		return nil, errors.New("收藏不存在，请刷新后重试")
	})
}
