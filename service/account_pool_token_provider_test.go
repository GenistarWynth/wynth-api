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

func TestAccountPoolTokenProviderSeparatesChannelTestRefreshFailureFromNormalRefresh(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)
	account := createAccountPoolSchedulerAccount(t, service, pool.Id, AccountPoolAccountCreateParams{
		Name: "concurrent-refresh-failure",
		Credential: AccountPoolCredentialConfig{
			Type: AccountPoolCredentialTypeOAuth,
		},
		TokenState: AccountPoolTokenState{
			RefreshToken: "refresh-concurrent-failure",
			ExpiresAt:    900,
			Version:      1,
		},
	})

	var calls atomic.Int32
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	secondStarted := make(chan struct{})
	setAccountPoolOAuthRefreshForTest(t, func(context.Context, string, string) (*CodexOAuthTokenResult, error) {
		switch calls.Add(1) {
		case 1:
			close(firstStarted)
			<-releaseFirst
			return nil, errors.New("channel test refresh failed")
		case 2:
			close(secondStarted)
			return nil, errors.New("normal refresh failed")
		default:
			return nil, errors.New("unexpected extra refresh")
		}
	})

	expiredState := AccountPoolTokenState{
		RefreshToken: "refresh-concurrent-failure",
		ExpiresAt:    900,
		Version:      1,
	}
	var wg sync.WaitGroup
	var channelTestErr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, channelTestErr = ResolveAccountPoolRuntimeCredential(context.Background(), AccountPoolRuntimeCredentialRequest{
			AccountID:         account.Id,
			Credential:        AccountPoolCredentialConfig{Type: AccountPoolCredentialTypeOAuth},
			TokenState:        expiredState,
			Now:               1000,
			SkipFailureRecord: true,
		})
	}()
	<-firstStarted

	normalErrCh := make(chan error, 1)
	go func() {
		_, err := ResolveAccountPoolRuntimeCredential(context.Background(), AccountPoolRuntimeCredentialRequest{
			AccountID:  account.Id,
			Credential: AccountPoolCredentialConfig{Type: AccountPoolCredentialTypeOAuth},
			TokenState: expiredState,
			Now:        1000,
		})
		normalErrCh <- err
	}()

	select {
	case <-secondStarted:
	case <-time.After(time.Second):
		close(releaseFirst)
		t.Fatal("normal refresh shared channel-test singleflight")
	}

	normalErr := <-normalErrCh
	close(releaseFirst)
	wg.Wait()

	require.ErrorContains(t, channelTestErr, "channel test refresh failed")
	require.ErrorContains(t, normalErr, "normal refresh failed")
	assert.Equal(t, int32(2), calls.Load())

	var stored model.AccountPoolAccount
	require.NoError(t, model.DB.First(&stored, account.Id).Error)
	assert.Contains(t, stored.LastError, "normal refresh failed")
	assert.Greater(t, stored.TempDisabledUntil, int64(0))
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

// TestAccountPoolTokenProviderGeminiOAuthDispatchesToGeminiSeam verifies that a
// Gemini-platform OAuth account dispatches to accountPoolGeminiOAuthRefresh (not
// the codex or claude seams) and resolves the token.
func TestAccountPoolTokenProviderGeminiOAuthDispatchesToGeminiSeam(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	svc := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, svc)
	require.NoError(t, model.DB.Model(&pool).Update("platform", model.AccountPoolPlatformGemini).Error)
	account := createAccountPoolSchedulerAccount(t, svc, pool.Id, AccountPoolAccountCreateParams{
		Name: "gemini-oauth-dispatch",
		Credential: AccountPoolCredentialConfig{
			Type:         AccountPoolCredentialTypeOAuth,
			RefreshToken: "gemini-refresh-token",
		},
		TokenState: AccountPoolTokenState{
			ExpiresAt: 900,
			Version:   1,
		},
	})

	var codexCalls, claudeCalls, geminiCalls int

	setAccountPoolOAuthRefreshForTest(t, func(_ context.Context, _ string, _ string) (*CodexOAuthTokenResult, error) {
		codexCalls++
		return nil, errors.New("codex seam must NOT be called for gemini")
	})

	oldClaude := accountPoolClaudeOAuthRefresh
	accountPoolClaudeOAuthRefresh = func(_ context.Context, _ string, _ string) (*CodexOAuthTokenResult, error) {
		claudeCalls++
		return nil, errors.New("claude seam must NOT be called for gemini")
	}
	t.Cleanup(func() { accountPoolClaudeOAuthRefresh = oldClaude })

	setAccountPoolGeminiOAuthRefreshForTest(t, func(_ context.Context, _ string, refreshToken string, proxyURL string) (*CodexOAuthTokenResult, error) {
		geminiCalls++
		assert.Equal(t, "gemini-refresh-token", refreshToken)
		return &CodexOAuthTokenResult{
			AccessToken:  "ya29.gemini-access-token",
			RefreshToken: "1//gemini-refresh-next",
			ExpiresAt:    time.Unix(2000, 0),
		}, nil
	})

	token, err := ResolveAccountPoolRuntimeCredential(context.Background(), AccountPoolRuntimeCredentialRequest{
		AccountID: account.Id,
		Credential: AccountPoolCredentialConfig{
			Type:         AccountPoolCredentialTypeOAuth,
			RefreshToken: "gemini-refresh-token",
		},
		TokenState: AccountPoolTokenState{
			ExpiresAt: 900,
			Version:   1,
		},
		Platform: model.AccountPoolPlatformGemini,
		Now:      1000,
	})

	require.NoError(t, err)
	assert.Equal(t, "ya29.gemini-access-token", token)
	assert.Equal(t, 1, geminiCalls, "gemini seam must be called exactly once")
	assert.Equal(t, 0, codexCalls, "codex seam must NOT be called for gemini OAuth")
	assert.Equal(t, 0, claudeCalls, "claude seam must NOT be called for gemini OAuth")
}

// TestAccountPoolTokenProviderGeminiAPIKeyResolvesDirectly verifies that a
// gemini-platform account with an API key resolves immediately without any
// OAuth dispatch.
func TestAccountPoolTokenProviderGeminiAPIKeyResolvesDirectly(t *testing.T) {
	setupAccountPoolServiceTestDB(t)

	var codexCalls int
	setAccountPoolOAuthRefreshForTest(t, func(_ context.Context, _ string, _ string) (*CodexOAuthTokenResult, error) {
		codexCalls++
		return nil, errors.New("codex refresh must not be called for gemini api-key account")
	})

	var claudeCalls int
	oldClaude := accountPoolClaudeOAuthRefresh
	accountPoolClaudeOAuthRefresh = func(_ context.Context, _ string, _ string) (*CodexOAuthTokenResult, error) {
		claudeCalls++
		return nil, errors.New("claude refresh must not be called for gemini api-key account")
	}
	t.Cleanup(func() { accountPoolClaudeOAuthRefresh = oldClaude })

	token, err := ResolveAccountPoolRuntimeCredential(context.Background(), AccountPoolRuntimeCredentialRequest{
		AccountID: 1,
		Credential: AccountPoolCredentialConfig{
			Type:   AccountPoolCredentialTypeAPIKey,
			APIKey: "AIza-gemini-key",
		},
		Platform: model.AccountPoolPlatformGemini,
		Now:      1000,
	})

	require.NoError(t, err)
	assert.Equal(t, "AIza-gemini-key", token)
	assert.Equal(t, 0, codexCalls, "codex refresh seam must NOT be called")
	assert.Equal(t, 0, claudeCalls, "claude refresh seam must NOT be called")
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

func setAccountPoolGeminiOAuthRefreshForTest(t *testing.T, refresh accountPoolGeminiOAuthRefreshFunc) {
	t.Helper()
	old := accountPoolGeminiOAuthRefresh
	accountPoolGeminiOAuthRefresh = refresh
	t.Cleanup(func() { accountPoolGeminiOAuthRefresh = old })
}

func setAccountPoolXAIOAuthRefreshForTest(t *testing.T, refresh accountPoolOAuthRefreshFunc) {
	t.Helper()
	old := accountPoolXAIOAuthRefresh
	accountPoolXAIOAuthRefresh = refresh
	t.Cleanup(func() { accountPoolXAIOAuthRefresh = old })
}

// TestAccountPoolTokenProviderXAIOAuthDispatchesToXAISeam verifies that an
// xAI-platform OAuth account dispatches to accountPoolXAIOAuthRefresh (not the
// codex/claude/gemini seams) and that the resolved access token is returned (this
// is the value that becomes info.ApiKey at runtime).
func TestAccountPoolTokenProviderXAIOAuthDispatchesToXAISeam(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	svc := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, svc)
	require.NoError(t, model.DB.Model(&pool).Update("platform", model.AccountPoolPlatformXAI).Error)
	account := createAccountPoolSchedulerAccount(t, svc, pool.Id, AccountPoolAccountCreateParams{
		Name: "xai-oauth-dispatch",
		Credential: AccountPoolCredentialConfig{
			Type:         AccountPoolCredentialTypeOAuth,
			RefreshToken: "xai-refresh-token",
		},
		TokenState: AccountPoolTokenState{
			ExpiresAt: 900,
			Version:   1,
		},
	})

	var codexCalls, claudeCalls, geminiCalls, xaiCalls int

	setAccountPoolOAuthRefreshForTest(t, func(_ context.Context, _ string, _ string) (*CodexOAuthTokenResult, error) {
		codexCalls++
		return nil, errors.New("codex seam must NOT be called for xai")
	})

	oldClaude := accountPoolClaudeOAuthRefresh
	accountPoolClaudeOAuthRefresh = func(_ context.Context, _ string, _ string) (*CodexOAuthTokenResult, error) {
		claudeCalls++
		return nil, errors.New("claude seam must NOT be called for xai")
	}
	t.Cleanup(func() { accountPoolClaudeOAuthRefresh = oldClaude })

	setAccountPoolGeminiOAuthRefreshForTest(t, func(_ context.Context, _ string, _ string, _ string) (*CodexOAuthTokenResult, error) {
		geminiCalls++
		return nil, errors.New("gemini seam must NOT be called for xai")
	})

	setAccountPoolXAIOAuthRefreshForTest(t, func(_ context.Context, refreshToken string, _ string) (*CodexOAuthTokenResult, error) {
		xaiCalls++
		assert.Equal(t, "xai-refresh-token", refreshToken)
		return &CodexOAuthTokenResult{
			AccessToken:  "xai-access-token",
			RefreshToken: "xai-refresh-next",
			ExpiresAt:    time.Unix(2000, 0),
		}, nil
	})

	token, err := ResolveAccountPoolRuntimeCredential(context.Background(), AccountPoolRuntimeCredentialRequest{
		AccountID: account.Id,
		Credential: AccountPoolCredentialConfig{
			Type:         AccountPoolCredentialTypeOAuth,
			RefreshToken: "xai-refresh-token",
		},
		TokenState: AccountPoolTokenState{
			ExpiresAt: 900,
			Version:   1,
		},
		Platform: model.AccountPoolPlatformXAI,
		Now:      1000,
	})

	require.NoError(t, err)
	assert.Equal(t, "xai-access-token", token, "resolved access token becomes info.ApiKey")
	assert.Equal(t, 1, xaiCalls, "xai seam must be called exactly once")
	assert.Equal(t, 0, codexCalls, "codex seam must NOT be called for xai OAuth")
	assert.Equal(t, 0, claudeCalls, "claude seam must NOT be called for xai OAuth")
	assert.Equal(t, 0, geminiCalls, "gemini seam must NOT be called for xai OAuth")
}
