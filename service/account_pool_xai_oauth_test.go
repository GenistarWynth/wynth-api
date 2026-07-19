package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
