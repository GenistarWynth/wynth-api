package service

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/model"

	"gorm.io/gorm"
)

func ResolveAccountPoolRuntimeProxyURL(accountProxyID int, poolDefaultProxyID int) (string, error) {
	proxyID := accountProxyID
	if proxyID <= 0 {
		proxyID = poolDefaultProxyID
	}
	if proxyID <= 0 {
		return "", nil
	}
	proxy, err := resolveEnabledAccountPoolRuntimeProxy(proxyID)
	if err != nil {
		return "", err
	}
	return buildAccountPoolRuntimeProxyURL(proxy)
}

func resolveEnabledAccountPoolRuntimeProxy(proxyID int) (model.AccountPoolProxy, error) {
	visited := map[int]struct{}{}
	for proxyID > 0 {
		if _, ok := visited[proxyID]; ok {
			return model.AccountPoolProxy{}, fmt.Errorf("account pool proxy fallback chain contains a cycle at proxy %d", proxyID)
		}
		visited[proxyID] = struct{}{}

		var proxy model.AccountPoolProxy
		if err := model.DB.First(&proxy, proxyID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return model.AccountPoolProxy{}, fmt.Errorf("account pool proxy %d not found", proxyID)
			}
			return model.AccountPoolProxy{}, err
		}
		if proxy.Status == model.AccountPoolProxyStatusEnabled {
			return proxy, nil
		}
		proxyID = proxy.FallbackProxyID
	}
	return model.AccountPoolProxy{}, errors.New("no enabled account pool proxy in fallback chain")
}

func buildAccountPoolRuntimeProxyURL(proxy model.AccountPoolProxy) (string, error) {
	protocol := strings.ToLower(strings.TrimSpace(proxy.Protocol))
	switch protocol {
	case "http", "https", "socks5", "socks5h":
	default:
		return "", fmt.Errorf("unsupported account pool proxy protocol: %s", proxy.Protocol)
	}
	host := strings.TrimSpace(proxy.Host)
	if host == "" {
		return "", errors.New("account pool proxy host is required")
	}
	if proxy.Port <= 0 {
		return "", errors.New("account pool proxy port is required")
	}

	proxyURL := &url.URL{
		Scheme: protocol,
		Host:   net.JoinHostPort(host, strconv.Itoa(proxy.Port)),
	}
	authConfig, err := DecryptAccountPoolProxyAuthConfig(proxy.Password)
	if err != nil {
		return "", fmt.Errorf("decrypt account pool proxy auth: %w", err)
	}
	username := strings.TrimSpace(proxy.Username)
	if username == "" {
		username = strings.TrimSpace(authConfig.Username)
	}
	if username != "" {
		if authConfig.Password != "" {
			proxyURL.User = url.UserPassword(username, authConfig.Password)
		} else {
			proxyURL.User = url.User(username)
		}
	}
	return proxyURL.String(), nil
}
