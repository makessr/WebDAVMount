package main

import (
	"errors"
	"strings"
	"webdavmount/internal/providers"
)

func (p Profile) public() Profile {
	p.Password = ""
	p.MEGASession = nil
	if p.Storage != nil {
		settings := *p.Storage
		settings.SessionToken = ""
		p.Storage = &settings
	}
	if p.Proxy != nil {
		config := *p.Proxy
		config.Password = ""
		p.Proxy = &config
	}
	return p
}

func sameConnectionProxy(a, b *ProxySettings) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func sameObjectAuthentication(old, next Profile) bool {
	if old.Storage == nil && next.Storage == nil {
		return true
	}
	var a, b providers.ObjectOptions
	if old.Storage != nil {
		a = *old.Storage
	}
	if next.Storage != nil {
		b = *next.Storage
	}
	a.SessionToken, b.SessionToken = "", ""
	return a == b && old.URL == next.URL && old.User == next.User
}

func (s *DesktopService) normalizeConnectionProxy(provider string, input, previous *ProxySettings) (*ProxySettings, error) {
	if input == nil || input.Mode == "" || input.Mode == "inherit" {
		return nil, nil
	}
	switch providers.Kind(provider) {
	case providers.SFTP, providers.FTP:
		return nil, errors.New("此连接类型不使用网盘 HTTP 代理")
	}
	config := *input
	if config.Mode != "direct" && config.Mode != "manual" {
		return nil, errors.New("请选择有效的网盘代理方式")
	}
	config.Bypass = ""
	if config.Mode == "manual" && previous != nil && config.Password == "" && config.Username != "" &&
		previous.Protocol == config.Protocol && previous.Host == strings.TrimSpace(config.Host) &&
		previous.Port == config.Port && previous.Username == config.Username {
		config.Password = previous.Password
	}
	normalized, err := normalizeProxySettings(config)
	if err != nil {
		return nil, err
	}
	if !s.connectionProxyAvailable {
		return nil, errors.New("当前挂载引擎不支持单独代理，请使用本版本附带的引擎")
	}
	return &normalized, nil
}

func (s *DesktopService) applyConnectionProxy(profile Profile, parameters map[string]string) error {
	if profile.Proxy == nil {
		return nil
	}
	config, err := s.normalizeConnectionProxy(profile.Provider, profile.Proxy, nil)
	if err != nil || config == nil {
		return err
	}
	if config.Mode == "direct" {
		parameters["override.http_proxy"] = "direct"
		return nil
	}
	route, err := resolveEngineProxy(*config, nil)
	if err != nil {
		return err
	}
	parameters["override.http_proxy"] = route.https
	return nil
}

func (s *DesktopService) ProbeConnectionMEGA(id string, input *ProxySettings) (string, error) {
	ctx, finish, err := s.beginOperation("proxy.probe")
	if err != nil {
		return "", err
	}
	defer finish()
	s.mu.Lock()
	var previous *ProxySettings
	if id != "" {
		p, err := s.find(id)
		if err != nil || p.Provider != providers.MEGA {
			s.mu.Unlock()
			return "", errors.New("未找到 MEGA 连接")
		}
		previous = p.Proxy
	}
	route := s.proxy
	config, err := s.normalizeConnectionProxy(providers.MEGA, input, previous)
	s.mu.Unlock()
	if err != nil {
		return "", err
	}
	if config != nil {
		route, err = resolveEngineProxy(*config, nil)
		if err != nil {
			return "", err
		}
	}
	if route.warning != "" {
		return "", errors.New(route.warning)
	}
	return route.probeMEGA(ctx)
}
