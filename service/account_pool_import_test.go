package service

import (
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAccountPoolServiceImportSub2APIDataCreatesAccountsAndReferencedProxy(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)

	result, err := service.ImportAccounts(AccountPoolAccountImportParams{
		PoolID: pool.Id,
		Format: "sub2api",
		Content: `{
			"type": "sub2api-data",
			"version": 1,
			"exported_at": "2026-06-24T00:00:00Z",
			"proxies": [
				{
					"proxy_key": "proxy-a",
					"name": "Proxy A",
					"protocol": "http",
					"host": "127.0.0.1",
					"port": 8080,
					"username": "proxy-user",
					"password": "proxy-secret",
					"status": "active"
				}
			],
			"accounts": [
				{
					"name": "sub2api-key",
					"platform": "openai",
					"type": "api_key",
					"credentials": {
						"api_key": "sk-sub2api"
					},
					"proxy_key": "proxy-a",
					"concurrency": 3,
					"priority": 7
				},
				{
					"name": "sub2api-oauth",
					"platform": "openai",
					"type": "oauth",
					"credentials": {
						"email": "codex@example.com",
						"refresh_token": "refresh-sub",
						"access_token": "access-sub",
						"chatgpt_account_id": "acct-sub",
						"expires_at": 4102444800
					},
					"concurrency": 2,
					"priority": 9
				}
			]
		}`,
		Defaults: AccountPoolAccountImportDefaults{
			Weight:          11,
			SupportedModels: []string{"gpt-5"},
		},
	})

	require.NoError(t, err)
	assert.Equal(t, 2, result.Imported)
	assert.Equal(t, 0, result.Skipped)
	assert.Equal(t, 0, result.Failed)
	assert.Equal(t, 1, result.ProxyCreated)
	require.Len(t, result.Accounts, 2)

	keyAccount := requireAccountPoolAccountByName(t, "sub2api-key")
	keyCredential, err := DecryptAccountPoolCredentialConfig(keyAccount.CredentialConfig)
	require.NoError(t, err)
	assert.Equal(t, AccountPoolCredentialTypeAPIKey, keyCredential.Type)
	assert.Equal(t, "sk-sub2api", keyCredential.APIKey)
	assert.Equal(t, int64(7), keyAccount.Priority)
	assert.Equal(t, uint(11), keyAccount.Weight)
	assert.Equal(t, 3, keyAccount.MaxConcurrency)
	assert.NotZero(t, keyAccount.ProxyID)

	var storedProxy model.AccountPoolProxy
	require.NoError(t, model.DB.First(&storedProxy, keyAccount.ProxyID).Error)
	assert.Equal(t, "Proxy A", storedProxy.Name)
	assert.Equal(t, "http", storedProxy.Protocol)
	assert.Equal(t, "127.0.0.1", storedProxy.Host)
	assert.Equal(t, 8080, storedProxy.Port)
	assert.Equal(t, "proxy-user", storedProxy.Username)
	proxyAuth, err := DecryptAccountPoolProxyAuthConfig(storedProxy.Password)
	require.NoError(t, err)
	assert.Equal(t, "proxy-secret", proxyAuth.Password)

	oauthAccount := requireAccountPoolAccountByName(t, "sub2api-oauth")
	oauthCredential, err := DecryptAccountPoolCredentialConfig(oauthAccount.CredentialConfig)
	require.NoError(t, err)
	assert.Equal(t, AccountPoolCredentialTypeOAuth, oauthCredential.Type)
	assert.Equal(t, "codex@example.com", oauthCredential.Email)
	assert.Equal(t, "refresh-sub", oauthCredential.RefreshToken)
	assert.Equal(t, "acct-sub", oauthAccount.AccountIdentifier)
	tokenState, err := DecryptAccountPoolTokenState(oauthAccount.TokenState)
	require.NoError(t, err)
	assert.Equal(t, "access-sub", tokenState.AccessToken)
	assert.Equal(t, "refresh-sub", tokenState.RefreshToken)
	assert.Equal(t, int64(4102444800), tokenState.ExpiresAt)
}

func TestAccountPoolServiceImportRejectsMissingDefaultProxyInDryRun(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)

	_, err := service.ImportAccounts(AccountPoolAccountImportParams{
		PoolID: pool.Id,
		Format: "sub2api",
		DryRun: true,
		Content: `{
			"type": "sub2api-data",
			"accounts": [
				{
					"name": "sub2api-key",
					"type": "api_key",
					"credentials": {
						"api_key": "sk-sub2api"
					}
				}
			]
		}`,
		Defaults: AccountPoolAccountImportDefaults{
			ProxyID: 999,
		},
	})

	require.ErrorContains(t, err, "account pool proxy not found")
}

func TestAccountPoolServiceImportSub2APIPreservesExplicitZeroConcurrency(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)

	result, err := service.ImportAccounts(AccountPoolAccountImportParams{
		PoolID: pool.Id,
		Format: "sub2api",
		Content: `{
			"type": "sub2api-data",
			"accounts": [
				{
					"name": "unlimited-sub2api-key",
					"platform": "openai",
					"type": "api_key",
					"credentials": {
						"api_key": "sk-sub2api"
					},
					"concurrency": 0
				}
			]
		}`,
		Defaults: AccountPoolAccountImportDefaults{
			MaxConcurrency: 4,
		},
	})

	require.NoError(t, err)
	assert.Equal(t, 1, result.Imported)
	account := requireAccountPoolAccountByName(t, "unlimited-sub2api-key")
	assert.Zero(t, account.MaxConcurrency)
}

func TestAccountPoolServiceImportCPAConfigMapsCodexKeysAndModels(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)

	result, err := service.ImportAccounts(AccountPoolAccountImportParams{
		PoolID: pool.Id,
		Format: "cpa",
		Content: `
codex-api-key:
  - api-key: sk-cpa
    priority: 30
    prefix: team
    proxy-url: socks5://user:pass@proxy.example.com:1080
    models:
      - name: gpt-5-codex
        alias: codex-latest
      - name: gpt-5.1
`,
		Defaults: AccountPoolAccountImportDefaults{
			MaxConcurrency: 4,
		},
	})

	require.NoError(t, err)
	assert.Equal(t, 1, result.Imported)
	assert.Equal(t, 0, result.Skipped)
	assert.Equal(t, 0, result.Failed)
	assert.Equal(t, 1, result.ProxyCreated)
	require.Len(t, result.Accounts, 1)

	account := requireAccountPoolAccountByName(t, result.Accounts[0].Name)
	credential, err := DecryptAccountPoolCredentialConfig(account.CredentialConfig)
	require.NoError(t, err)
	assert.Equal(t, AccountPoolCredentialTypeAPIKey, credential.Type)
	assert.Equal(t, "sk-cpa", credential.APIKey)
	assert.Equal(t, int64(30), account.Priority)
	assert.Equal(t, 4, account.MaxConcurrency)
	assert.NotZero(t, account.ProxyID)

	view, err := buildAccountPoolAccountView(account)
	require.NoError(t, err)
	assert.Equal(t, []string{"team/codex-latest", "team/gpt-5.1"}, view.SupportedModels)
	assert.Equal(t, map[string]string{
		"team/codex-latest": "gpt-5-codex",
		"team/gpt-5.1":      "gpt-5.1",
	}, view.ModelMapping)
}

func TestAccountPoolServiceImportSub2APIRejectsMissingProxyReference(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)

	result, err := service.ImportAccounts(AccountPoolAccountImportParams{
		PoolID: pool.Id,
		Format: "sub2api",
		Content: `{
			"type": "sub2api-data",
			"accounts": [
				{
					"name": "missing-proxy-account",
					"platform": "openai",
					"type": "api_key",
					"credentials": {
						"api_key": "sk-missing-proxy"
					},
					"proxy_key": "missing-proxy"
				}
			]
		}`,
	})

	require.NoError(t, err)
	assert.Equal(t, 0, result.Imported)
	assert.Equal(t, 1, result.Skipped)
	require.Len(t, result.Errors, 1)
	assert.Contains(t, result.Errors[0].Message, "proxy_key")

	var count int64
	require.NoError(t, model.DB.Model(&model.AccountPoolAccount{}).Count(&count).Error)
	assert.Zero(t, count)
}

func TestAccountPoolServiceImportCPAAuthJSONImportsOAuthAndSkipsDuplicate(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)
	_, err := service.CreateAccount(AccountPoolAccountCreateParams{
		PoolID:            pool.Id,
		Name:              "existing-cpa",
		AccountIdentifier: "acct-cpa",
		Credential: AccountPoolCredentialConfig{
			Type:         AccountPoolCredentialTypeOAuth,
			Email:        "existing@example.com",
			RefreshToken: "refresh-existing",
		},
	})
	require.NoError(t, err)

	result, err := service.ImportAccounts(AccountPoolAccountImportParams{
		PoolID: pool.Id,
		Format: "cpa",
		Content: `[
			{
				"provider": "codex",
				"label": "Existing Codex",
				"metadata": {
					"type": "codex",
					"email": "existing@example.com",
					"account_id": "acct-cpa",
					"access_token": "access-existing",
					"refresh_token": "refresh-existing",
					"expired": 4102444800
				}
			},
			{
				"type": "codex",
				"email": "new@example.com",
				"account_id": "acct-new",
				"access_token": "access-new",
				"refresh_token": "refresh-new",
				"expired": 4102444801
			}
		]`,
	})

	require.NoError(t, err)
	assert.Equal(t, 1, result.Imported)
	assert.Equal(t, 1, result.Skipped)
	assert.Equal(t, 0, result.Failed)

	account := requireAccountPoolAccountByName(t, "new@example.com")
	assert.Equal(t, "acct-new", account.AccountIdentifier)
	credential, err := DecryptAccountPoolCredentialConfig(account.CredentialConfig)
	require.NoError(t, err)
	assert.Equal(t, AccountPoolCredentialTypeOAuth, credential.Type)
	assert.Equal(t, "new@example.com", credential.Email)
	assert.Equal(t, "refresh-new", credential.RefreshToken)
	tokenState, err := DecryptAccountPoolTokenState(account.TokenState)
	require.NoError(t, err)
	assert.Equal(t, "access-new", tokenState.AccessToken)
	assert.Equal(t, "refresh-new", tokenState.RefreshToken)
	assert.Equal(t, int64(4102444801), tokenState.ExpiresAt)
}

func TestAccountPoolServiceImportCPAAuthJSONImportsAccessTokenOnlyEntry(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)

	result, err := service.ImportAccounts(AccountPoolAccountImportParams{
		PoolID: pool.Id,
		Format: "cpa",
		Content: `[
			{
				"type": "codex",
				"label": "access-only-codex",
				"metadata": {
					"access_token": "access-only-token",
					"expired": 4102444800
				}
			}
		]`,
	})

	require.NoError(t, err)
	assert.Equal(t, 1, result.Imported)
	assert.Equal(t, 0, result.Skipped)
	assert.Equal(t, 0, result.Failed)

	account := requireAccountPoolAccountByName(t, "access-only-codex")
	credential, err := DecryptAccountPoolCredentialConfig(account.CredentialConfig)
	require.NoError(t, err)
	assert.Equal(t, AccountPoolCredentialTypeOAuth, credential.Type)
	assert.Empty(t, credential.RefreshToken)
	tokenState, err := DecryptAccountPoolTokenState(account.TokenState)
	require.NoError(t, err)
	assert.Equal(t, "access-only-token", tokenState.AccessToken)
	assert.Empty(t, tokenState.RefreshToken)
	assert.Equal(t, int64(4102444800), tokenState.ExpiresAt)
}

func requireAccountPoolAccountByName(t *testing.T, name string) model.AccountPoolAccount {
	t.Helper()

	var account model.AccountPoolAccount
	require.NoError(t, model.DB.Where("name = ?", name).First(&account).Error)
	return account
}

// import-1: CPA YAML with plural mis-keyed field returns actionable error, not raw JSON error.
func TestAccountPoolServiceImportCPAYAMLMisKeyedPluralReturnsActionableError(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)

	// "codex-api-keys:" (plural) is a common mis-keying; YAML parses fine but produces zero CodexAPIKeys entries.
	// parseCPAImport falls through to parseCPAAuthJSONImport which previously returned a raw JSON unmarshal error.
	_, err := service.ImportAccounts(AccountPoolAccountImportParams{
		PoolID: pool.Id,
		Format: "cpa",
		Content: `
codex-api-keys:
  - api-key: sk-wrong-plural
    base-url: https://api.example.com
    proxy-url: ""
`,
	})

	require.Error(t, err)
	// Must mention the correct singular key name as an actionable hint.
	assert.Contains(t, err.Error(), "codex-api-key")
	// Must NOT be a bare "invalid character" or "cannot unmarshal" JSON error with no hint.
	assert.NotEqual(t, "invalid character", err.Error()[:min(len(err.Error()), 15)])
}

// import-1 (positive): valid CPA config YAML with singular codex-api-key still imports successfully.
func TestAccountPoolServiceImportCPAYAMLSingularKeyImportsSuccessfully(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)

	result, err := service.ImportAccounts(AccountPoolAccountImportParams{
		PoolID: pool.Id,
		Format: "cpa",
		Content: `
codex-api-key:
  - api-key: sk-singular-ok
    base-url: https://api.example.com
    proxy-url: ""
`,
	})

	require.NoError(t, err)
	assert.Equal(t, 1, result.Imported)
}

// import-3: dry-run sub2api import with one pre-existing proxy and one new proxy reports accurate counts.
func TestAccountPoolServiceImportSub2APIDryRunReportsAccurateProxyCounts(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)

	// Pre-create a proxy that will match the first entry in the import.
	_, err := service.CreateProxy(AccountPoolProxyCreateParams{
		Name:     "existing-proxy",
		Protocol: "http",
		Host:     "127.0.0.1",
		Port:     8080,
		Username: "user1",
		Password: "pass1",
	})
	require.NoError(t, err)

	// Count proxy rows before dry-run.
	var proxyCountBefore int64
	require.NoError(t, model.DB.Model(&model.AccountPoolProxy{}).Count(&proxyCountBefore).Error)

	result, err := service.ImportAccounts(AccountPoolAccountImportParams{
		PoolID: pool.Id,
		Format: "sub2api",
		DryRun: true,
		Content: `{
			"type": "sub2api-data",
			"proxies": [
				{
					"proxy_key": "proxy-existing",
					"name": "Existing Proxy",
					"protocol": "http",
					"host": "127.0.0.1",
					"port": 8080,
					"username": "user1",
					"password": "pass1",
					"status": "active"
				},
				{
					"proxy_key": "proxy-new",
					"name": "New Proxy",
					"protocol": "socks5",
					"host": "10.0.0.1",
					"port": 1080,
					"username": "",
					"password": "",
					"status": "active"
				}
			],
			"accounts": [
				{
					"name": "account-a",
					"platform": "openai",
					"type": "api_key",
					"credentials": {
						"api_key": "sk-account-a"
					},
					"proxy_key": "proxy-existing"
				},
				{
					"name": "account-b",
					"platform": "openai",
					"type": "api_key",
					"credentials": {
						"api_key": "sk-account-b"
					},
					"proxy_key": "proxy-new"
				}
			]
		}`,
	})

	require.NoError(t, err)
	assert.Equal(t, 2, result.Imported)
	// Accurate: one proxy already existed, one is new.
	assert.Equal(t, 1, result.ProxyReused, "expected one proxy reused (pre-existing match)")
	assert.Equal(t, 1, result.ProxyCreated, "expected one proxy created (no match)")

	// No proxy rows should have been written in dry-run.
	var proxyCountAfter int64
	require.NoError(t, model.DB.Model(&model.AccountPoolProxy{}).Count(&proxyCountAfter).Error)
	assert.Equal(t, proxyCountBefore, proxyCountAfter, "dry-run must not write proxy rows")
}

// import-3 (CPA): dry-run CPA config import reports non-zero proxy count when a matching proxy exists.
func TestAccountPoolServiceImportCPADryRunReportsNonZeroProxyCount(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)

	// Pre-create a proxy matching the CPA entry's proxy-url.
	_, err := service.CreateProxy(AccountPoolProxyCreateParams{
		Name:     "cpa-existing-proxy",
		Protocol: "socks5",
		Host:     "proxy.example.com",
		Port:     1080,
		Username: "user",
		Password: "pass",
	})
	require.NoError(t, err)

	var proxyCountBefore int64
	require.NoError(t, model.DB.Model(&model.AccountPoolProxy{}).Count(&proxyCountBefore).Error)

	result, err := service.ImportAccounts(AccountPoolAccountImportParams{
		PoolID: pool.Id,
		Format: "cpa",
		DryRun: true,
		Content: `
codex-api-key:
  - api-key: sk-cpa-dry
    base-url: https://api.example.com
    proxy-url: socks5://user:pass@proxy.example.com:1080
`,
	})

	require.NoError(t, err)
	// The CPA dry-run path must report a non-zero proxy count (reused or created).
	assert.True(t, result.ProxyReused+result.ProxyCreated > 0, "CPA dry-run should report proxy reused=1 for existing proxy")
	assert.Equal(t, 1, result.ProxyReused, "pre-existing proxy should be counted as reused")

	// No proxy rows should have been written.
	var proxyCountAfter int64
	require.NoError(t, model.DB.Model(&model.AccountPoolProxy{}).Count(&proxyCountAfter).Error)
	assert.Equal(t, proxyCountBefore, proxyCountAfter, "dry-run must not write proxy rows")
}

// import-6: dedup pass with a corrupt CredentialConfig on an existing account does not abort the import.
func TestAccountPoolServiceImportDedupToleratesCorruptCredentialConfig(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)

	// Seed an existing account with an undecryptable CredentialConfig (corrupt ciphertext).
	corrupt := model.AccountPoolAccount{
		PoolID:           pool.Id,
		Name:             "corrupt-existing",
		Status:           "enabled",
		CredentialConfig: "not-valid-ciphertext",
	}
	require.NoError(t, model.DB.Create(&corrupt).Error)

	// Import a new, distinct account — the dedup pass must survive the corrupt row.
	result, err := service.ImportAccounts(AccountPoolAccountImportParams{
		PoolID: pool.Id,
		Format: "sub2api",
		Content: `{
			"type": "sub2api-data",
			"accounts": [
				{
					"name": "new-clean-account",
					"platform": "openai",
					"type": "api_key",
					"credentials": {
						"api_key": "sk-new-clean"
					}
				}
			]
		}`,
	})

	require.NoError(t, err, "import must not return error when dedup encounters corrupt credential")
	assert.Equal(t, 1, result.Imported)
	assert.Equal(t, 0, result.Failed)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
