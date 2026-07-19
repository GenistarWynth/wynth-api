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

// A grok.com web-cookie account stores its sso token in APIKey and an optional
// cf_clearance — both secret. A full export must emit them under the importer-read
// names ("sso"/"cf_clearance") so it round-trips, and a redacted export must mask
// both. This guards the seam where export historically only emitted "api_key" (which
// the grok_web importer ignores) and never emitted cf_clearance at all.
func TestAccountPoolServiceExportRoundTripsGrokWebCookie(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	svc := AccountPoolService{}

	// Full export round-trips both secrets into a fresh grok_web pool.
	pool := createAccountPoolServiceTestPoolWithPlatform(t, svc, model.AccountPoolPlatformGrokWeb)
	_, err := svc.CreateAccount(AccountPoolAccountCreateParams{
		PoolID: pool.Id,
		Name:   "grok-cookie",
		Credential: AccountPoolCredentialConfig{
			Type:        AccountPoolCredentialTypeGrokWebCookie,
			APIKey:      "sso-token-abcdefgh",
			CFClearance: "cf-clearance-12345678",
		},
	})
	require.NoError(t, err)

	payload, skipped, err := svc.ExportAccounts(pool.Id, true)
	require.NoError(t, err)
	assert.Equal(t, 0, skipped)
	require.Len(t, payload.Accounts, 1)
	assert.Equal(t, "sso-token-abcdefgh", payload.Accounts[0].Credentials["sso"], "sso emitted under importer-read name")
	assert.Equal(t, "cf-clearance-12345678", payload.Accounts[0].Credentials["cf_clearance"], "cf_clearance emitted")
	_, hasAPIKey := payload.Accounts[0].Credentials["api_key"]
	assert.False(t, hasAPIKey, "grok_web export must not use the api_key name the importer ignores")

	content, err := common.Marshal(payload)
	require.NoError(t, err)
	pool2 := createAccountPoolServiceTestPoolWithPlatform(t, svc, model.AccountPoolPlatformGrokWeb)
	result, err := svc.ImportAccounts(AccountPoolAccountImportParams{
		PoolID:  pool2.Id,
		Format:  "sub2api",
		Content: string(content),
	})
	require.NoError(t, err)
	assert.Equal(t, 1, result.Imported)
	reimported := accountByNameInPool(t, pool2.Id, "grok-cookie")
	cred, err := DecryptAccountPoolCredentialConfig(reimported.CredentialConfig)
	require.NoError(t, err)
	assert.Equal(t, AccountPoolCredentialTypeGrokWebCookie, cred.Type)
	assert.Equal(t, "sso-token-abcdefgh", cred.APIKey, "sso token survives the round-trip")
	assert.Equal(t, "cf-clearance-12345678", cred.CFClearance, "cf_clearance survives the round-trip")

	// Redacted export still emits both secret keys (so the shape round-trips for
	// inspection) but masks their values — a regression that dropped cf_clearance
	// entirely must fail here, not merely be "absent".
	redacted, _, err := svc.ExportAccounts(pool.Id, false)
	require.NoError(t, err)
	require.Len(t, redacted.Accounts, 1)
	redactedCreds := redacted.Accounts[0].Credentials
	require.Contains(t, redactedCreds, "sso", "redacted export still emits the sso key")
	require.Contains(t, redactedCreds, "cf_clearance", "redacted export still emits the cf_clearance key")
	assert.NotEqual(t, "sso-token-abcdefgh", redactedCreds["sso"], "sso value must be masked")
	assert.NotEqual(t, "cf-clearance-12345678", redactedCreds["cf_clearance"], "cf_clearance value must be masked")
}

func TestAccountPoolServiceExportRoundTripsXAIOAuthMetadata(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	svc := AccountPoolService{}
	pool := createAccountPoolServiceTestPoolWithPlatform(t, svc, model.AccountPoolPlatformXAI)
	_, err := svc.CreateAccount(AccountPoolAccountCreateParams{
		PoolID:            pool.Id,
		Name:              "grok-oauth",
		AccountIdentifier: "grok-subject",
		Credential: AccountPoolCredentialConfig{
			Type:              AccountPoolCredentialTypeOAuth,
			Email:             "grok@example.com",
			RefreshToken:      "grok-refresh-secret",
			IDToken:           "grok-id-secret",
			ClientID:          "grok-client",
			Scope:             "openid offline_access",
			TokenType:         "Bearer",
			Subject:           "grok-subject",
			TeamID:            "team-42",
			SubscriptionTier:  "SUPER_GROK",
			EntitlementStatus: "active",
		},
		TokenState: AccountPoolTokenState{
			AccessToken:  "grok-access-secret",
			RefreshToken: "grok-refresh-secret",
			ExpiresAt:    1784548800,
		},
	})
	require.NoError(t, err)

	payload, _, err := svc.ExportAccounts(pool.Id, true)
	require.NoError(t, err)
	require.Len(t, payload.Accounts, 1)
	assert.Equal(t, "grok-id-secret", payload.Accounts[0].Credentials["id_token"])
	assert.Equal(t, "grok-client", payload.Accounts[0].Credentials["client_id"])
	assert.Equal(t, "team-42", payload.Accounts[0].Credentials["team_id"])
	assert.Equal(t, "SUPER_GROK", payload.Accounts[0].Credentials["subscription_tier"])

	content, err := common.Marshal(payload)
	require.NoError(t, err)
	pool2 := createAccountPoolServiceTestPoolWithPlatform(t, svc, model.AccountPoolPlatformXAI)
	result, err := svc.ImportAccounts(AccountPoolAccountImportParams{PoolID: pool2.Id, Format: "sub2api", Content: string(content)})
	require.NoError(t, err)
	assert.Equal(t, 1, result.Imported)
	reimported := accountByNameInPool(t, pool2.Id, "grok-oauth")
	credential, err := DecryptAccountPoolCredentialConfig(reimported.CredentialConfig)
	require.NoError(t, err)
	assert.Equal(t, "grok-id-secret", credential.IDToken)
	assert.Equal(t, "grok-client", credential.ClientID)
	assert.Equal(t, "team-42", credential.TeamID)
	assert.Equal(t, "SUPER_GROK", credential.SubscriptionTier)

	redacted, _, err := svc.ExportAccounts(pool.Id, false)
	require.NoError(t, err)
	redactedBody, err := common.Marshal(redacted)
	require.NoError(t, err)
	assert.NotContains(t, string(redactedBody), "grok-id-secret")
	assert.NotContains(t, string(redactedBody), "grok-refresh-secret")
	assert.Contains(t, string(redactedBody), "grok-client")
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
