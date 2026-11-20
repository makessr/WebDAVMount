package main

import (
	"context"
	"encoding/base64"
	"errors"
	"regexp"
	"strings"
	"time"
	"webdavmount/internal/providers"
)

type megaSession struct {
	ID        string `json:"id"`
	MasterKey string `json:"masterKey"`
}

var megaCodePattern = regexp.MustCompile(`^[0-9]{6}$`)
var megaSessionPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{16,512}$`)

func (s *DesktopService) SaveMEGAConnection(profile Profile, code string) (Profile, error) {
	if profile.Provider != providers.MEGA || !megaCodePattern.MatchString(strings.TrimSpace(code)) || profile.ClearPassword {
		return Profile{}, errors.New("请输入 MEGA 验证器显示的六位验证码")
	}
	ctx, finish, err := s.beginOperation("mega.authorize")
	if err != nil {
		return Profile{}, err
	}
	defer finish()
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.connectionManagementReadyLocked(); err != nil {
		return Profile{}, err
	}
	return s.saveProfileWithCodeLocked(ctx, profile, strings.TrimSpace(code))
}

func (s *DesktopService) finishMEGALogin(ctx context.Context, profile Profile) (*megaSession, error) {
	if profile.Password == "" {
		return nil, errors.New("请填写 MEGA 账号密码")
	}
	_, err := s.callProfileContext(ctx, profile, "operations/stat", map[string]any{"remote": ""})
	if err != nil {
		return nil, err
	}
	data, err := s.callContext(ctx, "config/get", map[string]any{"name": profile.ID})
	if err != nil {
		return nil, errors.New("MEGA 登录后无法获取会话，请重新验证")
	}
	id, _ := data["session_id"].(string)
	key, _ := data["master_key"].(string)
	decoded, err := base64.StdEncoding.DecodeString(key)
	if !megaSessionPattern.MatchString(id) || err != nil || len(decoded) != 16 {
		return nil, errors.New("MEGA 登录后无法获取有效会话，请重新验证")
	}
	clean, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	if _, err := s.callContext(clean, "config/unset", map[string]any{"name": profile.ID, "keys": []string{"2fa"}}); err != nil {
		return nil, errors.New("无法清除一次性验证码，请重新验证")
	}
	return &megaSession{ID: id, MasterKey: key}, nil
}
