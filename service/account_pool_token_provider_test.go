package service

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAccountPoolTokenProviderReturnsStaticAPIKey(t *testing.T) {
	setupAccountPoolServiceTestDB(t)

	token, err := ResolveAccountPoolRuntimeCredential(context.Background(), AccountPoolRuntimeCredentialRequest{
		AccountID: 1,
		Credential: AccountPoolCredentialConfig{
			Type:   AccountPoolCredentialTypeAPIKey,
			APIKey: "sk-static",
		},
		Now: 1000,
	})

	require.NoError(t, err)
	assert.Equal(t, "sk-static", token)
}

func TestAccountPoolTokenProviderNoopsWithoutAnyCredential(t *testing.T) {
	setupAccountPoolServiceTestDB(t)

	token, err := ResolveAccountPoolRuntimeCredential(context.Background(), AccountPoolRuntimeCredentialRequest{
		AccountID: 1,
		Now:       1000,
	})

	require.NoError(t, err)
	assert.Empty(t, token)
}

func TestAccountPoolTokenProviderReusesValidOAuthAccessToken(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	setAccountPoolOAuthRefreshForTest(t, func(context.Context, string, string) (*CodexOAuthTokenResult, error) {
		t.Fatal("refresh should not be called for a valid access token")
		return nil, nil
	})

	token, err := ResolveAccountPoolRuntimeCredential(context.Background(), AccountPoolRuntimeCredentialRequest{
		AccountID: 1,
		Credential: AccountPoolCredentialConfig{
			Type: AccountPoolCredentialTypeOAuth,
		},
		TokenState: AccountPoolTokenState{
			AccessToken:  "access-valid",
			RefreshToken: "refresh-valid",
			ExpiresAt:    2000,
			Version:      7,
		},
		Now: 1000,
	})

	require.NoError(t, err)
	assert.Equal(t, "access-valid", token)
}

func TestAccountPoolTokenProviderRefreshesExpiredOAuthToken(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)
	account := createAccountPoolSchedulerAccount(t, service, pool.Id, AccountPoolAccountCreateParams{
		Name: "expired-oauth",
		Credential: AccountPoolCredentialConfig{
			Type: AccountPoolCredentialTypeOAuth,
		},
		TokenState: AccountPoolTokenState{
			AccessToken:  "access-expired",
			RefreshToken: "refresh-old",
			ExpiresAt:    900,
			Version:      3,
		},
	})
	setAccountPoolOAuthRefreshForTest(t, func(ctx context.Context, refreshToken string, proxyURL string) (*CodexOAuthTokenResult, error) {
		assert.Equal(t, "refresh-old", refreshToken)
		assert.Equal(t, "socks5://proxy.local:1080", proxyURL)
		return &CodexOAuthTokenResult{
			AccessToken:  "access-new",
			RefreshToken: "refresh-new",
			ExpiresAt:    time.Unix(2000, 0),
		}, nil
	})

	token, err := ResolveAccountPoolRuntimeCredential(context.Background(), AccountPoolRuntimeCredentialRequest{
		AccountID: account.Id,
		Credential: AccountPoolCredentialConfig{
			Type: AccountPoolCredentialTypeOAuth,
		},
		TokenState: AccountPoolTokenState{
			AccessToken:  "access-expired",
			RefreshToken: "refresh-old",
			ExpiresAt:    900,
			Version:      3,
		},
		ProxyURL: "socks5://proxy.local:1080",
		Now:      1000,
	})

	require.NoError(t, err)
	assert.Equal(t, "access-new", token)
	var stored model.AccountPoolAccount
	require.NoError(t, model.DB.First(&stored, account.Id).Error)
	require.NotContains(t, stored.TokenState, "access-new")
	reloadedState, err := DecryptAccountPoolTokenState(stored.TokenState)
	require.NoError(t, err)
	assert.Equal(t, "access-new", reloadedState.AccessToken)
	assert.Equal(t, "refresh-new", reloadedState.RefreshToken)
	assert.Equal(t, int64(2000), reloadedState.ExpiresAt)
	assert.Equal(t, int64(4), reloadedState.Version)
}

func TestAccountPoolTokenProviderRefreshFailureTemporarilyDisablesAccount(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)
	account := createAccountPoolSchedulerAccount(t, service, pool.Id, AccountPoolAccountCreateParams{
		Name: "refresh-fails",
		Credential: AccountPoolCredentialConfig{
			Type:         AccountPoolCredentialTypeOAuth,
			RefreshToken: "refresh-secret-token",
		},
		TokenState: AccountPoolTokenState{
			AccessToken: "access-expired",
			ExpiresAt:   900,
			Version:     1,
		},
	})
	setAccountPoolOAuthRefreshForTest(t, func(context.Context, string, string) (*CodexOAuthTokenResult, error) {
		return nil, errors.New("oauth failed bearer sk-refresh-secret-token")
	})

	token, err := ResolveAccountPoolRuntimeCredential(context.Background(), AccountPoolRuntimeCredentialRequest{
		AccountID: account.Id,
		Credential: AccountPoolCredentialConfig{
			Type:         AccountPoolCredentialTypeOAuth,
			RefreshToken: "refresh-secret-token",
		},
		TokenState: AccountPoolTokenState{
			AccessToken: "access-expired",
			ExpiresAt:   900,
			Version:     1,
		},
		Now: 1000,
	})

	require.Error(t, err)
	assert.Empty(t, token)
	var stored model.AccountPoolAccount
	require.NoError(t, model.DB.First(&stored, account.Id).Error)
	assert.Equal(t, int64(1060), stored.TempDisabledUntil)
	assert.Contains(t, stored.LastError, "oauth failed")
	assert.NotContains(t, stored.LastError, "sk-refresh-secret-token")
	assert.NotContains(t, stored.TempDisabledReason, "sk-refresh-secret-token")
}

func TestAccountPoolTokenProviderSingleflightsConcurrentRefresh(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)
	account := createAccountPoolSchedulerAccount(t, service, pool.Id, AccountPoolAccountCreateParams{
		Name: "concurrent-refresh",
		Credential: AccountPoolCredentialConfig{
			Type: AccountPoolCredentialTypeOAuth,
		},
		TokenState: AccountPoolTokenState{
			RefreshToken: "refresh-concurrent",
			ExpiresAt:    900,
			Version:      1,
		},
	})
	var calls atomic.Int32
	setAccountPoolOAuthRefreshForTest(t, func(context.Context, string, string) (*CodexOAuthTokenResult, error) {
		calls.Add(1)
		time.Sleep(20 * time.Millisecond)
		return &CodexOAuthTokenResult{
			AccessToken:  "access-concurrent",
			RefreshToken: "refresh-concurrent-next",
			ExpiresAt:    time.Unix(2000, 0),
		}, nil
	})

	var wg sync.WaitGroup
	results := make([]string, 6)
	errs := make([]error, 6)
	for i := range results {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			results[index], errs[index] = ResolveAccountPoolRuntimeCredential(context.Background(), AccountPoolRuntimeCredentialRequest{
				AccountID: account.Id,
				Credential: AccountPoolCredentialConfig{
					Type: AccountPoolCredentialTypeOAuth,
				},
				TokenState: AccountPoolTokenState{
					RefreshToken: "refresh-concurrent",
					ExpiresAt:    900,
					Version:      1,
				},
				Now: 1000,
			})
		}(i)
	}
	wg.Wait()

	for i := range results {
		require.NoError(t, errs[i])
		assert.Equal(t, "access-concurrent", results[i])
	}
	assert.Equal(t, int32(1), calls.Load())
}

func TestAccountPoolTokenProviderReturnsLatestTokenWhenOptimisticWriteLoses(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)
	account := createAccountPoolSchedulerAccount(t, service, pool.Id, AccountPoolAccountCreateParams{
		Name: "optimistic-race",
		Credential: AccountPoolCredentialConfig{
			Type: AccountPoolCredentialTypeOAuth,
		},
		TokenState: AccountPoolTokenState{
			RefreshToken: "refresh-race",
			ExpiresAt:    900,
			Version:      1,
		},
	})
	setAccountPoolOAuthRefreshForTest(t, func(context.Context, string, string) (*CodexOAuthTokenResult, error) {
		return &CodexOAuthTokenResult{
			AccessToken:  "access-loser",
			RefreshToken: "refresh-loser",
			ExpiresAt:    time.Unix(2000, 0),
		}, nil
	})
	setAccountPoolTokenStateUpdateForTest(t, func(accountID int, oldTokenState string, newTokenState string) (int64, error) {
		winnerState, err := EncryptAccountPoolTokenState(AccountPoolTokenState{
			AccessToken:  "access-winner",
			RefreshToken: "refresh-winner",
			ExpiresAt:    2000,
			Version:      2,
		})
		require.NoError(t, err)
		require.NoError(t, model.DB.Model(&model.AccountPoolAccount{}).
			Where("id = ?", accountID).
			Update("token_state", winnerState).Error)
		return 0, nil
	})

	token, err := ResolveAccountPoolRuntimeCredential(context.Background(), AccountPoolRuntimeCredentialRequest{
		AccountID: account.Id,
		Credential: AccountPoolCredentialConfig{
			Type: AccountPoolCredentialTypeOAuth,
		},
		TokenState: AccountPoolTokenState{
			RefreshToken: "refresh-race",
			ExpiresAt:    900,
			Version:      1,
		},
		Now: 1000,
	})

	require.NoError(t, err)
	assert.Equal(t, "access-winner", token)
	var stored model.AccountPoolAccount
	require.NoError(t, model.DB.First(&stored, account.Id).Error)
	reloadedState, err := DecryptAccountPoolTokenState(stored.TokenState)
	require.NoError(t, err)
	assert.Equal(t, "access-winner", reloadedState.AccessToken)
}

func setAccountPoolOAuthRefreshForTest(t *testing.T, refresh accountPoolOAuthRefreshFunc) {
	t.Helper()
	oldRefresh := accountPoolOAuthRefresh
	accountPoolOAuthRefresh = refresh
	t.Cleanup(func() {
		accountPoolOAuthRefresh = oldRefresh
	})
}

func setAccountPoolTokenStateUpdateForTest(t *testing.T, update accountPoolTokenStateUpdateFunc) {
	t.Helper()
	oldUpdate := accountPoolTokenStateUpdate
	accountPoolTokenStateUpdate = update
	t.Cleanup(func() {
		accountPoolTokenStateUpdate = oldUpdate
	})
}
