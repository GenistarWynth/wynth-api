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
	return resolveEnabledAccountPoolRuntimeProxyAt(proxyID, 0)
}

// resolveEnabledAccountPoolRuntimeProxyAt walks the fallback chain starting at proxyID,
// skipping proxies that are disabled OR currently known-unhealthy (as reported by the
// in-memory health store). Unknown/never-probed proxies are treated as healthy (fail-open).
//
// When every candidate in the chain is unhealthy (but enabled), the function falls back
// to the last enabled candidate so the caller always gets a non-empty result — preserving
// the original contract that a configured proxy is never silently dropped.
//
// now is a Unix timestamp (seconds); pass 0 to use the current time.
func resolveEnabledAccountPoolRuntimeProxyAt(proxyID int, now int64) (model.AccountPoolProxy, error) {
	visited := map[int]struct{}{}

	// lastEnabled holds the final enabled proxy seen so we can fall back to it if all
	// candidates are unhealthy.
	var lastEnabled *model.AccountPoolProxy

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
			// Track the last enabled proxy we encountered for the all-unhealthy fallback.
			proxyCopy := proxy
			lastEnabled = &proxyCopy

			if accountPoolProxyHealthy(proxy.Id, now) {
				// Healthy (or unknown) — use it.
				return proxy, nil
			}
			// Unhealthy — continue to fallback.
		}
		proxyID = proxy.FallbackProxyID
	}

	// All enabled proxies in the chain are currently known-unhealthy.
	// Fall back to the last enabled candidate (never return empty when a proxy is configured).
	if lastEnabled != nil {
		return *lastEnabled, nil
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
