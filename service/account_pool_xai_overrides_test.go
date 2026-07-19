package service

import (
	"context"
	"net"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeAccountPoolXAIBaseURL(t *testing.T) {
	publicResolver := func(context.Context, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("203.0.113.10")}, nil
	}
	privateResolver := func(context.Context, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("10.0.0.8")}, nil
	}

	t.Run("trusted xai host does not require DNS", func(t *testing.T) {
		got, err := normalizeAccountPoolXAIBaseURL(context.Background(), " https://api.x.ai/v1/ ", false, nil)
		require.NoError(t, err)
		assert.Equal(t, "https://api.x.ai/v1", got)
	})

	t.Run("custom public https host is accepted", func(t *testing.T) {
		got, err := normalizeAccountPoolXAIBaseURL(context.Background(), "https://gateway.example.com/xai/", false, publicResolver)
		require.NoError(t, err)
		assert.Equal(t, "https://gateway.example.com/xai", got)
	})

	for _, test := range []struct {
		name     string
		raw      string
		resolver accountPoolXAIHostResolver
	}{
		{name: "http", raw: "http://api.x.ai/v1"},
		{name: "literal private address", raw: "https://127.0.0.1/v1"},
		{name: "resolved private address", raw: "https://internal.example.com/v1", resolver: privateResolver},
		{name: "credentials", raw: "https://user:pass@api.x.ai/v1"},
		{name: "query", raw: "https://api.x.ai/v1?token=secret"},
	} {
		t.Run("rejects "+test.name, func(t *testing.T) {
			_, err := normalizeAccountPoolXAIBaseURL(context.Background(), test.raw, false, test.resolver)
			require.Error(t, err)
		})
	}

	t.Run("unsafe mode is explicit", func(t *testing.T) {
		got, err := normalizeAccountPoolXAIBaseURL(context.Background(), "http://127.0.0.1:8080/v1/", true, nil)
		require.NoError(t, err)
		assert.Equal(t, "http://127.0.0.1:8080/v1", got)
	})
}

func TestAccountPoolServiceStoresAndClearsXAIOverrides(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool, err := service.CreatePool(AccountPoolCreateParams{Name: "xai-overrides", Platform: model.AccountPoolPlatformXAI})
	require.NoError(t, err)
	baseURL := " https://api.x.ai/v1/ "
	enabled := true
	account, err := service.CreateAccount(AccountPoolAccountCreateParams{
		PoolID: pool.Id,
		Name:   "xai-account",
		Credential: AccountPoolCredentialConfig{
			Type:                  AccountPoolCredentialTypeAPIKey,
			APIKey:                "secret-key",
			BaseURL:               &baseURL,
			HeaderOverrideEnabled: &enabled,
			HeaderOverrides: map[string]string{
				"x-trace-id": "trace",
			},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "https://api.x.ai/v1", account.BaseURL)
	assert.True(t, account.HeaderOverrideEnabled)
	assert.Equal(t, map[string]string{"X-Trace-Id": "trace"}, account.HeaderOverrides)

	clearBaseURL := ""
	disabled := false
	updated, err := service.UpdateAccount(pool.Id, account.Id, AccountPoolAccountCreateParams{
		Name: "xai-account",
		Credential: AccountPoolCredentialConfig{
			BaseURL:               &clearBaseURL,
			HeaderOverrideEnabled: &disabled,
			HeaderOverrides:       map[string]string{},
		},
	})
	require.NoError(t, err)
	assert.Empty(t, updated.BaseURL)
	assert.False(t, updated.HeaderOverrideEnabled)
	assert.Empty(t, updated.HeaderOverrides)

	stored, err := getAccountPoolAccountForPool(pool.Id, account.Id)
	require.NoError(t, err)
	credential, err := DecryptAccountPoolCredentialConfig(stored.CredentialConfig)
	require.NoError(t, err)
	assert.Equal(t, "secret-key", credential.APIKey, "clearing overrides must preserve the account secret")
}

func TestNormalizeAccountPoolXAIHeaderOverrides(t *testing.T) {
	got, err := normalizeAccountPoolXAIHeaderOverrides(map[string]string{
		" X-Trace-ID ": " trace-123 ",
		"X-Feature":    "enabled",
	})
	require.NoError(t, err)
	assert.Equal(t, map[string]string{
		"X-Feature":  "enabled",
		"X-Trace-Id": "trace-123",
	}, got)

	for _, header := range []string{
		"Host",
		"Content-Length",
		"Authorization",
		"Cookie",
		"Proxy-Authorization",
		"X-Api-Key",
		"Connection",
		"Transfer-Encoding",
	} {
		t.Run("rejects "+header, func(t *testing.T) {
			_, err := normalizeAccountPoolXAIHeaderOverrides(map[string]string{header: "unsafe"})
			require.Error(t, err)
		})
	}

	t.Run("rejects excessive entries", func(t *testing.T) {
		headers := make(map[string]string, accountPoolOutboundHeaderOverrideMaxEntries+1)
		for index := 0; index <= accountPoolOutboundHeaderOverrideMaxEntries; index++ {
			headers["X-Test-"+string(rune('A'+index))] = "value"
		}
		_, err := normalizeAccountPoolXAIHeaderOverrides(headers)
		require.Error(t, err)
	})
}
