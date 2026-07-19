package service

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAccountPoolServiceRefreshXAIOAuthAccountPersistsRotatedTokens(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	accountPoolService := AccountPoolService{}
	pool, err := accountPoolService.CreatePool(AccountPoolCreateParams{
		Name:     "xai-pool",
		Platform: model.AccountPoolPlatformXAI,
	})
	require.NoError(t, err)
	account, err := accountPoolService.CreateAccount(AccountPoolAccountCreateParams{
		PoolID: pool.Id,
		Name:   "xai-account",
		Credential: AccountPoolCredentialConfig{
			Type:         AccountPoolCredentialTypeOAuth,
			RefreshToken: "old-refresh",
			ClientID:     "stored-client",
			Subject:      "subject-1",
		},
		TokenState: AccountPoolTokenState{
			AccessToken:  "old-access",
			RefreshToken: "runtime-rotated-refresh",
			ExpiresAt:    time.Now().Add(-time.Hour).Unix(),
			Version:      7,
		},
	})
	require.NoError(t, err)

	oldRefresh := accountPoolXAIOAuthInfoRefresh
	accountPoolXAIOAuthInfoRefresh = func(ctx context.Context, refreshToken string, proxyURL string, clientID string) (*XAIOAuthTokenInfo, error) {
		assert.Equal(t, "runtime-rotated-refresh", refreshToken)
		assert.Empty(t, proxyURL)
		assert.Equal(t, "stored-client", clientID)
		return &XAIOAuthTokenInfo{
			AccessToken:  "new-access",
			RefreshToken: "new-refresh",
			IDToken:      "new-id-token",
			TokenType:    "Bearer",
			ExpiresAt:    time.Now().Add(time.Hour).Unix(),
			ClientID:     clientID,
			Email:        "xai@example.com",
		}, nil
	}
	t.Cleanup(func() { accountPoolXAIOAuthInfoRefresh = oldRefresh })

	refreshed, err := accountPoolService.RefreshXAIOAuthAccount(context.Background(), pool.Id, account.Id)
	require.NoError(t, err)
	assert.Equal(t, account.Id, refreshed.Id)

	stored, err := getAccountPoolAccountForPool(pool.Id, account.Id)
	require.NoError(t, err)
	credential, err := DecryptAccountPoolCredentialConfig(stored.CredentialConfig)
	require.NoError(t, err)
	assert.Equal(t, "new-refresh", credential.RefreshToken)
	assert.Equal(t, "new-id-token", credential.IDToken)
	assert.Equal(t, "subject-1", credential.Subject)
	assert.Equal(t, "xai@example.com", credential.Email)
	tokenState, err := DecryptAccountPoolTokenState(stored.TokenState)
	require.NoError(t, err)
	assert.Equal(t, "new-access", tokenState.AccessToken)
	assert.Equal(t, "new-refresh", tokenState.RefreshToken)
	assert.Equal(t, int64(8), tokenState.Version)
}

func TestAccountPoolServiceRefreshXAIOAuthAccountDoesNotOverwriteConcurrentRotation(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	accountPoolService := AccountPoolService{}
	pool, err := accountPoolService.CreatePool(AccountPoolCreateParams{
		Name:     "xai-pool",
		Platform: model.AccountPoolPlatformXAI,
	})
	require.NoError(t, err)
	account, err := accountPoolService.CreateAccount(AccountPoolAccountCreateParams{
		PoolID: pool.Id,
		Name:   "xai-account-concurrent-refresh",
		Credential: AccountPoolCredentialConfig{
			Type:         AccountPoolCredentialTypeOAuth,
			RefreshToken: "initial-refresh",
			ClientID:     "stored-client",
		},
		TokenState: AccountPoolTokenState{
			RefreshToken: "initial-refresh",
			ExpiresAt:    time.Now().Add(-time.Hour).Unix(),
			Version:      3,
		},
	})
	require.NoError(t, err)

	oldRefresh := accountPoolXAIOAuthInfoRefresh
	accountPoolXAIOAuthInfoRefresh = func(context.Context, string, string, string) (*XAIOAuthTokenInfo, error) {
		winnerCredential, encryptErr := EncryptAccountPoolCredentialConfig(AccountPoolCredentialConfig{
			Type:         AccountPoolCredentialTypeOAuth,
			RefreshToken: "winner-refresh",
			ClientID:     "stored-client",
		})
		require.NoError(t, encryptErr)
		winnerState, encryptErr := EncryptAccountPoolTokenState(AccountPoolTokenState{
			AccessToken:  "winner-access",
			RefreshToken: "winner-refresh",
			ExpiresAt:    time.Now().Add(time.Hour).Unix(),
			Version:      4,
		})
		require.NoError(t, encryptErr)
		require.NoError(t, model.DB.Model(&model.AccountPoolAccount{}).
			Where("id = ?", account.Id).
			Updates(map[string]any{
				"credential_config": winnerCredential,
				"token_state":       winnerState,
			}).Error)
		return &XAIOAuthTokenInfo{
			AccessToken:  "loser-access",
			RefreshToken: "loser-refresh",
			ExpiresAt:    time.Now().Add(time.Hour).Unix(),
			ClientID:     "stored-client",
		}, nil
	}
	t.Cleanup(func() { accountPoolXAIOAuthInfoRefresh = oldRefresh })

	_, err = accountPoolService.RefreshXAIOAuthAccount(context.Background(), pool.Id, account.Id)
	require.NoError(t, err)
	stored, err := getAccountPoolAccountForPool(pool.Id, account.Id)
	require.NoError(t, err)
	credential, err := DecryptAccountPoolCredentialConfig(stored.CredentialConfig)
	require.NoError(t, err)
	assert.Equal(t, "winner-refresh", credential.RefreshToken)
	tokenState, err := DecryptAccountPoolTokenState(stored.TokenState)
	require.NoError(t, err)
	assert.Equal(t, "winner-access", tokenState.AccessToken)
	assert.Equal(t, "winner-refresh", tokenState.RefreshToken)
	assert.Equal(t, int64(4), tokenState.Version)
}

func TestAccountPoolServiceImportXAISSOAccountsReportsPartialFailure(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	accountPoolService := AccountPoolService{}
	pool, err := accountPoolService.CreatePool(AccountPoolCreateParams{
		Name:     "xai-pool",
		Platform: model.AccountPoolPlatformXAI,
	})
	require.NoError(t, err)

	oldConvert := accountPoolXAISSOConvert
	accountPoolXAISSOConvert = func(ctx context.Context, ssoToken string, proxyURL string) (*XAIOAuthTokenInfo, error) {
		assert.Empty(t, proxyURL)
		if ssoToken == "bad-sso" {
			return nil, errors.New("invalid sso token")
		}
		return &XAIOAuthTokenInfo{
			AccessToken:  "access-" + ssoToken,
			RefreshToken: "refresh-" + ssoToken,
			ClientID:     xaiOAuthDefaultClientID,
			Email:        ssoToken + "@example.com",
			Subject:      "subject-" + ssoToken,
			ExpiresAt:    time.Now().Add(time.Hour).Unix(),
		}, nil
	}
	t.Cleanup(func() { accountPoolXAISSOConvert = oldConvert })

	result, err := accountPoolService.ImportXAISSOAccounts(context.Background(), AccountPoolXAISSOImportParams{
		PoolID:            pool.Id,
		SSOTokens:         []string{"first", "bad-sso", "second"},
		Name:              "Imported Grok",
		MaxConcurrency:    0,
		MaxConcurrencySet: true,
		SupportedModels:   []string{"grok-4"},
	})
	require.NoError(t, err)
	require.Len(t, result.Created, 2)
	require.Len(t, result.Errors, 1)
	assert.Equal(t, 2, result.Errors[0].Index)
	assert.NotContains(t, result.Errors[0].Message, "bad-sso")
	assert.Equal(t, "first@example.com", result.Created[0].AccountIdentifier)
	assert.Equal(t, "Imported Grok - first@example.com", result.Created[0].Name)
	assert.Zero(t, result.Created[0].MaxConcurrency)
	assert.Equal(t, []string{"grok-4"}, result.Created[0].SupportedModels)

	stored, err := getAccountPoolAccountForPool(pool.Id, result.Created[0].Id)
	require.NoError(t, err)
	credential, err := DecryptAccountPoolCredentialConfig(stored.CredentialConfig)
	require.NoError(t, err)
	assert.Equal(t, "refresh-first", credential.RefreshToken)
	assert.NotContains(t, stored.CredentialConfig, "first")
}

func TestAccountPoolServiceImportXAISSOAccountsCarriesBatchContextIntoPersistence(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	accountPoolService := AccountPoolService{}
	pool, err := accountPoolService.CreatePool(AccountPoolCreateParams{
		Name:     "xai-context-import",
		Platform: model.AccountPoolPlatformXAI,
	})
	require.NoError(t, err)

	oldConvert := accountPoolXAISSOConvert
	accountPoolXAISSOConvert = func(context.Context, string, string) (*XAIOAuthTokenInfo, error) {
		return &XAIOAuthTokenInfo{
			AccessToken:  "access",
			RefreshToken: "refresh",
			Email:        "context@example.com",
			ExpiresAt:    time.Now().Add(time.Hour).Unix(),
		}, nil
	}
	t.Cleanup(func() { accountPoolXAISSOConvert = oldConvert })

	type contextMarkerKey struct{}
	marker := &struct{}{}
	type observedPersistenceContext struct {
		marker      any
		hasDeadline bool
	}
	observed := make(chan observedPersistenceContext, 1)
	callbackName := "account_pool_test_observe_sso_import_context"
	require.NoError(t, model.DB.Callback().Create().Before("gorm:create").Register(callbackName, func(db *gorm.DB) {
		_, hasDeadline := db.Statement.Context.Deadline()
		observed <- observedPersistenceContext{
			marker:      db.Statement.Context.Value(contextMarkerKey{}),
			hasDeadline: hasDeadline,
		}
		db.AddError(errors.New("stop after observing persistence context"))
	}))
	t.Cleanup(func() {
		require.NoError(t, model.DB.Callback().Create().Remove(callbackName))
	})

	ctx := context.WithValue(context.Background(), contextMarkerKey{}, marker)
	result, err := accountPoolService.ImportXAISSOAccounts(ctx, AccountPoolXAISSOImportParams{
		PoolID:    pool.Id,
		SSOTokens: []string{"sso"},
	})
	require.NoError(t, err)
	require.Len(t, result.Errors, 1)

	persistenceContext := <-observed
	assert.Equal(t, marker, persistenceContext.marker)
	assert.True(t, persistenceContext.hasDeadline, "account creation must inherit the bounded batch context")
}

func TestAccountPoolServiceImportXAISSOAccountsUsesBoundedParallelConversionAndStableAggregation(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	accountPoolService := AccountPoolService{}
	pool, err := accountPoolService.CreatePool(AccountPoolCreateParams{
		Name:     "xai-concurrent-import",
		Platform: model.AccountPoolPlatformXAI,
	})
	require.NoError(t, err)

	started := make(chan string, 6)
	release := make(chan struct{})
	var active atomic.Int32
	var maximum atomic.Int32
	oldConvert := accountPoolXAISSOConvert
	accountPoolXAISSOConvert = func(ctx context.Context, ssoToken string, proxyURL string) (*XAIOAuthTokenInfo, error) {
		assert.Empty(t, proxyURL)
		current := active.Add(1)
		defer active.Add(-1)
		for {
			observed := maximum.Load()
			if current <= observed || maximum.CompareAndSwap(observed, current) {
				break
			}
		}
		started <- ssoToken
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-release:
		}
		if ssoToken == "token-2" {
			return nil, errors.New("secret token-2 failed")
		}
		return &XAIOAuthTokenInfo{
			AccessToken:  "access-" + ssoToken,
			RefreshToken: "refresh-" + ssoToken,
			Email:        ssoToken + "@example.com",
			ExpiresAt:    time.Now().Add(time.Hour).Unix(),
		}, nil
	}
	t.Cleanup(func() { accountPoolXAISSOConvert = oldConvert })

	type importResult struct {
		result AccountPoolXAISSOImportResult
		err    error
	}
	done := make(chan importResult, 1)
	go func() {
		result, importErr := accountPoolService.ImportXAISSOAccounts(context.Background(), AccountPoolXAISSOImportParams{
			PoolID:    pool.Id,
			SSOTokens: []string{"token-1", "token-2", "token-3", "token-4", "token-5", "token-6"},
		})
		done <- importResult{result: result, err: importErr}
	}()

	for index := 0; index < 3; index++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			close(release)
			t.Fatal("timed out waiting for three concurrent SSO conversions")
		}
	}
	assert.Equal(t, int32(3), maximum.Load())
	close(release)

	var imported importResult
	select {
	case imported = <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for concurrent SSO import")
	}
	require.NoError(t, imported.err)
	require.Len(t, imported.result.Created, 5)
	require.Len(t, imported.result.Errors, 1)
	assert.Equal(t, 2, imported.result.Errors[0].Index)
	assert.NotContains(t, imported.result.Errors[0].Message, "token-2")
	identifiers := make([]string, 0, len(imported.result.Created))
	for _, created := range imported.result.Created {
		identifiers = append(identifiers, created.AccountIdentifier)
	}
	assert.Equal(t, []string{
		"token-1@example.com",
		"token-3@example.com",
		"token-4@example.com",
		"token-5@example.com",
		"token-6@example.com",
	}, identifiers)
}

func TestAccountPoolXAISSOImportConfigBoundsConcurrencyAndTimeout(t *testing.T) {
	t.Setenv(accountPoolXAISSOImportConcurrencyEnv, "5")
	assert.Equal(t, 5, loadAccountPoolXAISSOImportConcurrency())
	t.Setenv(accountPoolXAISSOImportConcurrencyEnv, "99")
	assert.Equal(t, accountPoolXAISSOImportMaxConcurrency, loadAccountPoolXAISSOImportConcurrency())
	t.Setenv(accountPoolXAISSOImportConcurrencyEnv, "0")
	assert.Equal(t, accountPoolXAISSOImportDefaultConcurrency, loadAccountPoolXAISSOImportConcurrency())

	t.Setenv(accountPoolXAISSOImportTimeoutEnv, "600")
	assert.Equal(t, 10*time.Minute, loadAccountPoolXAISSOImportTimeout())
	for _, value := range []string{"0", "29", "1801", "9223372036854775807"} {
		t.Setenv(accountPoolXAISSOImportTimeoutEnv, value)
		assert.Equal(t, accountPoolXAISSOImportDefaultTimeout, loadAccountPoolXAISSOImportTimeout())
	}
}

func TestAccountPoolServiceImportXAISSOAccountsPassesResolvedProxyToEveryConversion(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	accountPoolService := AccountPoolService{}
	pool, err := accountPoolService.CreatePool(AccountPoolCreateParams{
		Name:     "xai-proxy-import",
		Platform: model.AccountPoolPlatformXAI,
	})
	require.NoError(t, err)
	proxy, err := accountPoolService.CreateProxy(AccountPoolProxyCreateParams{
		Name:     "import-proxy",
		Protocol: "socks5",
		Host:     "proxy.example.com",
		Port:     1080,
		Username: "operator",
		Password: "secret",
	})
	require.NoError(t, err)

	oldConvert := accountPoolXAISSOConvert
	accountPoolXAISSOConvert = func(_ context.Context, ssoToken string, proxyURL string) (*XAIOAuthTokenInfo, error) {
		assert.Equal(t, "socks5://operator:secret@proxy.example.com:1080", proxyURL)
		return &XAIOAuthTokenInfo{
			AccessToken:  "access-" + ssoToken,
			RefreshToken: "refresh-" + ssoToken,
			Email:        ssoToken + "@example.com",
			ExpiresAt:    time.Now().Add(time.Hour).Unix(),
		}, nil
	}
	t.Cleanup(func() { accountPoolXAISSOConvert = oldConvert })

	result, err := accountPoolService.ImportXAISSOAccounts(context.Background(), AccountPoolXAISSOImportParams{
		PoolID:    pool.Id,
		ProxyID:   proxy.Id,
		SSOTokens: []string{"first", "second"},
	})
	require.NoError(t, err)
	assert.Len(t, result.Created, 2)
	assert.Empty(t, result.Errors)
}

func TestAccountPoolXAIQuotaImportProbeRunsForEachSuccessfulSSOItem(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	accountPoolService := AccountPoolService{}
	pool := createAccountPoolServiceTestPoolWithPlatform(t, accountPoolService, model.AccountPoolPlatformXAI)

	oldConvert := accountPoolXAISSOConvert
	accountPoolXAISSOConvert = func(_ context.Context, ssoToken string, _ string) (*XAIOAuthTokenInfo, error) {
		if ssoToken == "bad" {
			return nil, errors.New("conversion failed")
		}
		return &XAIOAuthTokenInfo{
			AccessToken:  "access-" + ssoToken,
			RefreshToken: "refresh-" + ssoToken,
			Email:        ssoToken + "@example.com",
			ExpiresAt:    time.Now().Add(time.Hour).Unix(),
		}, nil
	}
	t.Cleanup(func() { accountPoolXAISSOConvert = oldConvert })

	probed := make(chan int, 2)
	setAccountPoolXAIQuotaProbeRunnerForTest(t, func(_ context.Context, poolID int, accountID int) (AccountPoolXAIQuotaSnapshot, error) {
		assert.Equal(t, pool.Id, poolID)
		probed <- accountID
		return AccountPoolXAIQuotaSnapshot{}, nil
	})
	accountPoolXAIQuotaCreateProbeEnabled.Store(true)
	t.Cleanup(func() { accountPoolXAIQuotaCreateProbeEnabled.Store(false) })

	result, err := accountPoolService.ImportXAISSOAccounts(context.Background(), AccountPoolXAISSOImportParams{
		PoolID:    pool.Id,
		SSOTokens: []string{"first", "bad", "second"},
	})
	require.NoError(t, err)
	require.Len(t, result.Created, 2)
	require.Len(t, result.Errors, 1)

	seen := make(map[int]struct{}, 2)
	for range result.Created {
		select {
		case accountID := <-probed:
			seen[accountID] = struct{}{}
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for SSO import quota probe")
		}
	}
	assert.Contains(t, seen, result.Created[0].Id)
	assert.Contains(t, seen, result.Created[1].Id)
}
