package service

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func accountByNameInPool(t *testing.T, poolID int, name string) model.AccountPoolAccount {
	t.Helper()
	var account model.AccountPoolAccount
	require.NoError(t, model.DB.Where("pool_id = ? AND name = ?", poolID, name).First(&account).Error)
	return account
}

// A full (secret-bearing) export must re-import into a fresh pool as identical
// usable accounts — the round-trip contract that makes export symmetric with import.
func TestAccountPoolServiceExportRoundTripsThroughImport(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	svc := AccountPoolService{}
	pool := createAccountPoolServiceTestPoolWithPlatform(t, svc, model.AccountPoolPlatformGemini)

	proxy, err := svc.CreateProxy(AccountPoolProxyCreateParams{
		Name: "exp-proxy", Protocol: "http", Host: "127.0.0.1", Port: 8080,
		Username: "puser", Password: "psecret", Status: "enabled",
	})
	require.NoError(t, err)

	_, err = svc.CreateAccount(AccountPoolAccountCreateParams{
		PoolID:         pool.Id,
		Name:           "exp-key",
		Credential:     AccountPoolCredentialConfig{Type: AccountPoolCredentialTypeAPIKey, APIKey: "sk-export-key"},
		ProxyID:        proxy.Id,
		Priority:       5,
		MaxConcurrency: 3,
	})
	require.NoError(t, err)
	_, err = svc.CreateAccount(AccountPoolAccountCreateParams{
		PoolID:     pool.Id,
		Name:       "exp-oauth",
		Credential: AccountPoolCredentialConfig{Type: AccountPoolCredentialTypeOAuth, Email: "ca@example.com", RefreshToken: "rt-export"},
		TokenState: AccountPoolTokenState{AccessToken: "at-export", RefreshToken: "rt-export", ProjectID: "projects/exp-1"},
		OAuthType:  AccountPoolGeminiOAuthTypeCodeAssist,
	})
	require.NoError(t, err)

	payload, skipped, err := svc.ExportAccounts(pool.Id, true)
	require.NoError(t, err)
	assert.Equal(t, 0, skipped)
	require.Len(t, payload.Accounts, 2)
	require.Len(t, payload.Proxies, 1)
	assert.Equal(t, "sub2api-data", payload.Type)

	content, err := common.Marshal(payload)
	require.NoError(t, err)

	// Import into a fresh pool of the same platform.
	pool2 := createAccountPoolServiceTestPoolWithPlatform(t, svc, model.AccountPoolPlatformGemini)
	result, err := svc.ImportAccounts(AccountPoolAccountImportParams{
		PoolID:  pool2.Id,
		Format:  "sub2api",
		Content: string(content),
	})
	require.NoError(t, err)
	assert.Equal(t, 2, result.Imported)

	// OAuth account round-trips with secrets + oauth_type column + project_id.
	oauth := accountByNameInPool(t, pool2.Id, "exp-oauth")
	assert.Equal(t, AccountPoolGeminiOAuthTypeCodeAssist, oauth.OAuthType)
	oauthCred, err := DecryptAccountPoolCredentialConfig(oauth.CredentialConfig)
	require.NoError(t, err)
	assert.Equal(t, AccountPoolCredentialTypeOAuth, oauthCred.Type)
	assert.Equal(t, "rt-export", oauthCred.RefreshToken)
	assert.Equal(t, "ca@example.com", oauthCred.Email)
	oauthToken, err := DecryptAccountPoolTokenState(oauth.TokenState)
	require.NoError(t, err)
	assert.Equal(t, "at-export", oauthToken.AccessToken)
	assert.Equal(t, "projects/exp-1", oauthToken.ProjectID)

	// API-key account round-trips its key, priority, and proxy (recreated + linked).
	keyAcct := accountByNameInPool(t, pool2.Id, "exp-key")
	keyCred, err := DecryptAccountPoolCredentialConfig(keyAcct.CredentialConfig)
	require.NoError(t, err)
	assert.Equal(t, "sk-export-key", keyCred.APIKey)
	assert.Equal(t, int64(5), keyAcct.Priority, "priority survives the round-trip")
	require.NotZero(t, keyAcct.ProxyID, "proxy should be recreated and linked on import")
	var reimportedProxy model.AccountPoolProxy
	require.NoError(t, model.DB.First(&reimportedProxy, keyAcct.ProxyID).Error)
	assert.Equal(t, "127.0.0.1", reimportedProxy.Host)
	assert.Equal(t, 8080, reimportedProxy.Port)
	proxyAuth, err := DecryptAccountPoolProxyAuthConfig(reimportedProxy.Password)
	require.NoError(t, err)
	assert.Equal(t, "psecret", proxyAuth.Password)
}

// A single undecryptable credential blob must be skipped (logged + counted), not
// fail the whole export — mirroring the importer's skip-and-continue.
func TestAccountPoolServiceExportSkipsUndecryptableAccount(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	svc := AccountPoolService{}
	pool := createAccountPoolServiceTestPoolWithPlatform(t, svc, model.AccountPoolPlatformGemini)

	_, err := svc.CreateAccount(AccountPoolAccountCreateParams{
		PoolID:     pool.Id,
		Name:       "exp-healthy",
		Credential: AccountPoolCredentialConfig{Type: AccountPoolCredentialTypeAPIKey, APIKey: "sk-healthy"},
	})
	require.NoError(t, err)
	corrupt, err := svc.CreateAccount(AccountPoolAccountCreateParams{
		PoolID:     pool.Id,
		Name:       "exp-corrupt",
		Credential: AccountPoolCredentialConfig{Type: AccountPoolCredentialTypeAPIKey, APIKey: "sk-corrupt"},
	})
	require.NoError(t, err)
	// Corrupt the stored credential so decryption fails on export.
	require.NoError(t, model.DB.Model(&model.AccountPoolAccount{}).
		Where("id = ?", corrupt.Id).Update("credential_config", "not-a-valid-encrypted-blob").Error)

	payload, skipped, err := svc.ExportAccounts(pool.Id, true)
	require.NoError(t, err, "a corrupt account must not fail the whole export")
	assert.Equal(t, 1, skipped)
	require.Len(t, payload.Accounts, 1)
	assert.Equal(t, "exp-healthy", payload.Accounts[0].Name)
}

// The default (redacted) export must not leak usable secrets, while still emitting
// non-secret fields (email, oauth_type, platform).
func TestAccountPoolServiceExportRedactsSecretsByDefault(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	svc := AccountPoolService{}
	pool := createAccountPoolServiceTestPoolWithPlatform(t, svc, model.AccountPoolPlatformGemini)

	_, err := svc.CreateAccount(AccountPoolAccountCreateParams{
		PoolID:     pool.Id,
		Name:       "redact-me",
		Credential: AccountPoolCredentialConfig{Type: AccountPoolCredentialTypeOAuth, Email: "ca@example.com", RefreshToken: "rt-secret-xyz"},
		TokenState: AccountPoolTokenState{AccessToken: "at-secret-xyz", RefreshToken: "rt-secret-xyz"},
		OAuthType:  AccountPoolGeminiOAuthTypeCodeAssist,
	})
	require.NoError(t, err)

	payload, _, err := svc.ExportAccounts(pool.Id, false)
	require.NoError(t, err)
	raw, err := common.Marshal(payload)
	require.NoError(t, err)
	body := string(raw)

	assert.NotContains(t, body, "rt-secret-xyz", "refresh token must be redacted")
	assert.NotContains(t, body, "at-secret-xyz", "access token must be redacted")
	assert.Contains(t, body, "ca@example.com", "non-secret email is exported")
	assert.Contains(t, body, AccountPoolGeminiOAuthTypeCodeAssist, "non-secret oauth_type is exported")
	assert.True(t, strings.Contains(body, model.AccountPoolPlatformGemini), "platform is exported")
}
