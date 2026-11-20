package main

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

const connectionGroupLimit = 64

func normalizedConnectionGroup(name string) (string, error) {
	name = strings.TrimSpace(name)
	if !utf8.ValidString(name) || utf8.RuneCountInString(name) > 32 {
		return "", errors.New("分组名称最多32个字符")
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return "", errors.New("分组名称不能包含控制字符")
		}
	}
	if name == "未分组" {
		return "", errors.New("“未分组”为保留名称，请选择移入未分组")
	}
	return name, nil
}

// Group/order edits touch only one encrypted local profile file. Never change
// engine identities or unmount a disk, including while a profile is mounted.
func (s *DesktopService) changeConnectionGroups(change func([]Profile) error) error {
	_, finish, err := s.beginOperation("groups.save")
	if err != nil {
		return err
	}
	defer finish()
	s.mu.Lock()
	defer s.mu.Unlock()
	next := append([]Profile{}, s.profiles...)
	if err = change(next); err != nil {
		return err
	}
	groups := map[string]bool{}
	for _, p := range next {
		name, e := normalizedConnectionGroup(p.Group)
		if e != nil || name != p.Group {
			return errors.New("分组数据无法读取，请保留本机配置排查")
		}
		if name != "" {
			groups[name] = true
		}
	}
	if len(groups) > connectionGroupLimit {
		return errors.New("最多保留64个分组，请合并已有分组")
	}
	if err = s.persist(next); err != nil {
		return errors.New("分组保存失败，原分组、顺序和连接设置已保留")
	}
	s.profiles = next
	return nil
}

func (s *DesktopService) MoveConnectionsToGroup(ids []string, group string) error {
	name, err := normalizedConnectionGroup(group)
	if err != nil {
		return err
	}
	if len(ids) == 0 || len(ids) > 200 {
		return errors.New("请选择1至200个网盘")
	}
	selected := map[string]bool{}
	for _, id := range ids {
		if id == "" || selected[id] {
			return errors.New("网盘选择有重复或无效项，请重新选择")
		}
		selected[id] = true
	}
	return s.changeConnectionGroups(func(next []Profile) error {
		found := 0
		for i := range next {
			if selected[next[i].ID] {
				next[i].Group = name
				found++
			}
		}
		if found != len(selected) {
			return errors.New("部分网盘已变化，请刷新后重新选择")
		}
		return nil
	})
}

// An empty target dissolves a group, retaining every connection and its mount.
func (s *DesktopService) RenameConnectionGroup(oldName, newName string) error {
	old, err := normalizedConnectionGroup(oldName)
	if err != nil || old == "" {
		return errors.New("请选择一个已有的命名分组")
	}
	name, err := normalizedConnectionGroup(newName)
	if err != nil {
		return err
	}
	return s.changeConnectionGroups(func(next []Profile) error {
		found := false
		for _, p := range next {
			if p.Group == old {
				found = true
			}
			if name != "" && name != old && p.Group == name {
				return errors.New("分组名称已存在，请移动网盘以合并分组")
			}
		}
		if !found {
			return errors.New("分组已不存在，请刷新列表")
		}
		for i := range next {
			if next[i].Group == old {
				next[i].Group = name
			}
		}
		return nil
	})
}

func (s *DesktopService) MoveConnectionInGroup(id string, direction int) error {
	if direction != -1 && direction != 1 {
		return errors.New("请选择上移或下移")
	}
	return s.changeConnectionGroups(func(next []Profile) error {
		index := -1
		for i, p := range next {
			if p.ID == id {
				index = i
				break
			}
		}
		if index < 0 {
			return errors.New("网盘已不存在，请刷新列表")
		}
		for j := index + direction; j >= 0 && j < len(next); j += direction {
			if next[j].Group == next[index].Group {
				next[index], next[j] = next[j], next[index]
				return nil
			}
		}
		return errors.New("已到达分组内的首项或末项")
	})
}
