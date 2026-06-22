package service

import (
	"testing"

	"github.com/QuantumNous/new-api/common"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAccountPoolCredentialConfigEncryptsAndDecrypts(t *testing.T) {
	setStableCryptoSecretForAccountPoolConfigTest(t, "account-pool-config-credential-secret")

	original := AccountPoolCredentialConfig{
		Type:         AccountPoolCredentialTypeOAuth,
		Email:        "owner@example.com",
		RefreshToken: "refresh-token-secret",
	}

	encrypted, err := EncryptAccountPoolCredentialConfig(original)
	require.NoError(t, err)
	require.NotEmpty(t, encrypted)
	assert.NotContains(t, encrypted, original.Email)
	assert.NotContains(t, encrypted, original.RefreshToken)

	decrypted, err := DecryptAccountPoolCredentialConfig(encrypted)
	require.NoError(t, err)
	assert.Equal(t, original, decrypted)
}

func TestAccountPoolTokenStateIncrementsVersion(t *testing.T) {
	original := AccountPoolTokenState{
		AccessToken:  "access-token",
		RefreshToken: "refresh-token",
		ExpiresAt:    1712345678,
		Version:      41,
	}

	next := original.NextVersion()

	assert.Equal(t, int64(42), next.Version)
	assert.Equal(t, original.AccessToken, next.AccessToken)
	assert.Equal(t, original.RefreshToken, next.RefreshToken)
	assert.Equal(t, original.ExpiresAt, next.ExpiresAt)
	assert.Equal(t, int64(41), original.Version)
}

func TestAccountPoolTokenStateConfigEncryptsAndDecrypts(t *testing.T) {
	setStableCryptoSecretForAccountPoolConfigTest(t, "account-pool-config-token-secret")

	original := AccountPoolTokenState{
		AccessToken:  "access-token-secret",
		RefreshToken: "refresh-token-secret",
		ExpiresAt:    1712345678,
		Version:      7,
	}

	encrypted, err := EncryptAccountPoolTokenState(original)
	require.NoError(t, err)
	require.NotEmpty(t, encrypted)
	assert.NotContains(t, encrypted, original.AccessToken)
	assert.NotContains(t, encrypted, original.RefreshToken)

	decrypted, err := DecryptAccountPoolTokenState(encrypted)
	require.NoError(t, err)
	assert.Equal(t, original, decrypted)
}

func TestAccountPoolProxyAuthConfigEncryptsAndDecrypts(t *testing.T) {
	setStableCryptoSecretForAccountPoolConfigTest(t, "account-pool-config-proxy-secret")

	original := AccountPoolProxyAuthConfig{
		Username: "proxy-user",
		Password: "proxy-password-secret",
	}

	encrypted, err := EncryptAccountPoolProxyAuthConfig(original)
	require.NoError(t, err)
	require.NotEmpty(t, encrypted)
	assert.NotContains(t, encrypted, original.Username)
	assert.NotContains(t, encrypted, original.Password)

	decrypted, err := DecryptAccountPoolProxyAuthConfig(encrypted)
	require.NoError(t, err)
	assert.Equal(t, original, decrypted)
}

func TestAccountPoolConfigDecryptsEmptyInput(t *testing.T) {
	credential, err := DecryptAccountPoolCredentialConfig("")
	require.NoError(t, err)
	assert.Equal(t, AccountPoolCredentialConfig{}, credential)

	tokenState, err := DecryptAccountPoolTokenState("")
	require.NoError(t, err)
	assert.Equal(t, AccountPoolTokenState{}, tokenState)

	proxyAuth, err := DecryptAccountPoolProxyAuthConfig("")
	require.NoError(t, err)
	assert.Equal(t, AccountPoolProxyAuthConfig{}, proxyAuth)
}

func TestMaskAccountPoolSecretValue(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{name: "empty", value: "", want: ""},
		{name: "whitespace", value: "   ", want: ""},
		{name: "short", value: "short", want: "***"},
		{name: "exactly eight", value: "12345678", want: "***"},
		{name: "long", value: "sk-1234567890", want: "sk-1...7890"},
		{name: "trims before masking", value: "  sk-1234567890  ", want: "sk-1...7890"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, MaskAccountPoolSecretValue(tt.value))
		})
	}
}

func setStableCryptoSecretForAccountPoolConfigTest(t *testing.T, secret string) {
	t.Helper()
	oldSecret := common.CryptoSecret
	oldStable := common.CryptoSecretStable
	common.CryptoSecret = secret
	common.CryptoSecretStable = true
	t.Cleanup(func() {
		common.CryptoSecret = oldSecret
		common.CryptoSecretStable = oldStable
	})
}
