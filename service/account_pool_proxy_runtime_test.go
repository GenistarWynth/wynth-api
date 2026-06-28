package service

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveAccountPoolRuntimeProxyURLUsesAccountProxyBeforePoolDefault(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	accountProxy := createAccountPoolRuntimeTestProxy(t, service, AccountPoolProxyCreateParams{
		Name:     "account-proxy",
		Protocol: "http",
		Host:     "account-proxy.local",
		Port:     8080,
		Username: "proxy-user",
		Password: "p@ss word",
	})
	poolProxy := createAccountPoolRuntimeTestProxy(t, service, AccountPoolProxyCreateParams{
		Name:     "pool-proxy",
		Protocol: "socks5",
		Host:     "pool-proxy.local",
		Port:     1080,
	})

	proxyURL, err := ResolveAccountPoolRuntimeProxyURL(accountProxy.Id, poolProxy.Id)

	require.NoError(t, err)
	assert.Equal(t, "http://proxy-user:p%40ss%20word@account-proxy.local:8080", proxyURL)
}

func TestResolveAccountPoolRuntimeProxyURLUsesFallbackForDisabledProxy(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	fallback := createAccountPoolRuntimeTestProxy(t, service, AccountPoolProxyCreateParams{
		Name:     "fallback-proxy",
		Protocol: "socks5h",
		Host:     "fallback.local",
		Port:     1080,
	})
	disabled := createAccountPoolRuntimeTestProxy(t, service, AccountPoolProxyCreateParams{
		Name:            "disabled-proxy",
		Protocol:        "http",
		Host:            "disabled.local",
		Port:            8080,
		Status:          model.AccountPoolProxyStatusDisabled,
		FallbackProxyID: fallback.Id,
	})

	proxyURL, err := ResolveAccountPoolRuntimeProxyURL(disabled.Id, 0)

	require.NoError(t, err)
	assert.Equal(t, "socks5h://fallback.local:1080", proxyURL)
}

func TestResolveAccountPoolRuntimeProxyURLErrorsForConfiguredProxyWithoutEnabledRoute(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	disabled := createAccountPoolRuntimeTestProxy(t, service, AccountPoolProxyCreateParams{
		Name:     "disabled-proxy",
		Protocol: "http",
		Host:     "disabled.local",
		Port:     8080,
		Status:   model.AccountPoolProxyStatusDisabled,
	})

	proxyURL, err := ResolveAccountPoolRuntimeProxyURL(disabled.Id, 0)

	require.Error(t, err)
	assert.Empty(t, proxyURL)
	assert.Contains(t, err.Error(), "no enabled account pool proxy")
}

func TestSelectAccountPoolAccountIncludesProxyURL(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	accountProxy := createAccountPoolRuntimeTestProxy(t, service, AccountPoolProxyCreateParams{
		Name:     "account-proxy",
		Protocol: "http",
		Host:     "account-proxy.local",
		Port:     8080,
	})
	poolProxy := createAccountPoolRuntimeTestProxy(t, service, AccountPoolProxyCreateParams{
		Name:     "pool-proxy",
		Protocol: "socks5",
		Host:     "pool-proxy.local",
		Port:     1080,
	})
	pool := createAccountPoolServiceTestPool(t, service)
	require.NoError(t, model.DB.Model(&model.AccountPool{}).
		Where("id = ?", pool.Id).
		Update("default_proxy_id", poolProxy.Id).Error)
	channel := createAccountPoolServiceTestChannel(t, common.ChannelStatusManuallyDisabled)
	createEnabledAccountPoolSchedulerBinding(t, pool.Id, channel.Id, AccountPoolAccountFilterConfig{}, AccountPoolModelPolicy{})
	account := createAccountPoolSchedulerAccount(t, service, pool.Id, AccountPoolAccountCreateParams{
		Name:    "proxied-account",
		ProxyID: accountProxy.Id,
	})

	selection, err := SelectAccountPoolAccount(AccountPoolSelectionRequest{
		ChannelID:            channel.Id,
		RequestModel:         "gpt-5",
		ChannelUpstreamModel: "gpt-5",
		Now:                  1000,
	})

	require.NoError(t, err)
	assert.Equal(t, account.Id, selection.AccountID)
	assert.Equal(t, "http://account-proxy.local:8080", selection.ProxyURL)
}

func createAccountPoolRuntimeTestProxy(
	t *testing.T,
	service AccountPoolService,
	params AccountPoolProxyCreateParams,
) AccountPoolProxyView {
	t.Helper()
	proxy, err := service.CreateProxy(params)
	require.NoError(t, err)
	return proxy
}
