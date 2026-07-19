package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAccountPoolServiceReconcileXAIOAuthAccountsDryRunClassifiesCandidates(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	svc := AccountPoolService{}
	pool := createAccountPoolServiceTestPoolWithPlatform(t, svc, model.AccountPoolPlatformXAI)
	missingRefresh := createAccountPoolSchedulerAccount(t, svc, pool.Id, AccountPoolAccountCreateParams{
		Name:       "missing-refresh",
		Credential: AccountPoolCredentialConfig{Type: AccountPoolCredentialTypeOAuth},
		TokenState: AccountPoolTokenState{AccessToken: "still-valid", ExpiresAt: 5000, Version: 1},
	})
	expiredAccess := createAccountPoolSchedulerAccount(t, svc, pool.Id, AccountPoolAccountCreateParams{
		Name:       "expired-access",
		Credential: AccountPoolCredentialConfig{Type: AccountPoolCredentialTypeOAuth, RefreshToken: "refresh-expired"},
		TokenState: AccountPoolTokenState{AccessToken: "expired", ExpiresAt: 900, Version: 1},
	})
	nearExpiry := createAccountPoolSchedulerAccount(t, svc, pool.Id, AccountPoolAccountCreateParams{
		Name:       "near-expiry",
		Credential: AccountPoolCredentialConfig{Type: AccountPoolCredentialTypeOAuth, RefreshToken: "refresh-near"},
		TokenState: AccountPoolTokenState{AccessToken: "near", ExpiresAt: 1200, Version: 1},
	})
	_ = createAccountPoolSchedulerAccount(t, svc, pool.Id, AccountPoolAccountCreateParams{
		Name:       "healthy",
		Credential: AccountPoolCredentialConfig{Type: AccountPoolCredentialTypeOAuth, RefreshToken: "refresh-healthy"},
		TokenState: AccountPoolTokenState{AccessToken: "healthy", ExpiresAt: 5000, Version: 1},
	})
	_ = createAccountPoolSchedulerAccount(t, svc, pool.Id, AccountPoolAccountCreateParams{
		Name:       "api-key",
		Credential: AccountPoolCredentialConfig{Type: AccountPoolCredentialTypeAPIKey, APIKey: "xai-key"},
	})

	result, err := svc.ReconcileXAIOAuthAccounts(context.Background(), AccountPoolXAIOAuthReconcileParams{
		PoolID:                  pool.Id,
		DryRun:                  true,
		Now:                     1000,
		NearExpiryWindowSeconds: 300,
	})

	require.NoError(t, err)
	assert.True(t, result.DryRun)
	assert.Equal(t, 4, result.Scanned)
	assert.Equal(t, 3, result.Candidates)
	assert.Zero(t, result.Applied)
	items := accountPoolXAIOAuthReconcileItemsByID(result.Items)
	assert.Equal(t, AccountPoolXAIOAuthReconcileActionExpire, items[missingRefresh.Id].Action)
	assert.Equal(t, AccountPoolXAIOAuthReconcileReasonMissingRefreshToken, items[missingRefresh.Id].Reason)
	assert.Equal(t, AccountPoolXAIOAuthReconcileActionRefresh, items[expiredAccess.Id].Action)
	assert.Equal(t, AccountPoolXAIOAuthReconcileReasonAccessExpired, items[expiredAccess.Id].Reason)
	assert.Equal(t, AccountPoolXAIOAuthReconcileReasonAccessNearExpiry, items[nearExpiry.Id].Reason)

	for _, accountID := range []int{missingRefresh.Id, expiredAccess.Id, nearExpiry.Id} {
		var stored model.AccountPoolAccount
		require.NoError(t, model.DB.First(&stored, accountID).Error)
		assert.Equal(t, model.AccountPoolAccountStatusEnabled, stored.Status)
	}
}

func TestAccountPoolServiceReconcileXAIOAuthAccountsApplyRefreshesOrExpiresWithMetadataOnly(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	svc := AccountPoolService{}
	pool := createAccountPoolServiceTestPoolWithPlatform(t, svc, model.AccountPoolPlatformXAI)
	missingRefresh := createAccountPoolSchedulerAccount(t, svc, pool.Id, AccountPoolAccountCreateParams{
		Name:       "missing-refresh",
		Credential: AccountPoolCredentialConfig{Type: AccountPoolCredentialTypeOAuth},
		TokenState: AccountPoolTokenState{AccessToken: "old-access-secret", ExpiresAt: 900, Version: 1},
	})
	refreshable := createAccountPoolSchedulerAccount(t, svc, pool.Id, AccountPoolAccountCreateParams{
		Name:       "refreshable",
		Credential: AccountPoolCredentialConfig{Type: AccountPoolCredentialTypeOAuth, RefreshToken: "old-refresh-secret"},
		TokenState: AccountPoolTokenState{AccessToken: "old-access-secret", ExpiresAt: 900, Version: 4},
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"new-access-secret","refresh_token":"new-refresh-secret","expires_in":3600}`))
	}))
	defer srv.Close()
	overrideXAIOAuthTokenURLForTest(t, srv.URL)

	result, err := svc.ReconcileXAIOAuthAccounts(context.Background(), AccountPoolXAIOAuthReconcileParams{
		PoolID: pool.Id,
		DryRun: false,
		Now:    1000,
	})

	require.NoError(t, err)
	assert.False(t, result.DryRun)
	assert.Equal(t, 2, result.Candidates)
	assert.Equal(t, 2, result.Applied)
	items := accountPoolXAIOAuthReconcileItemsByID(result.Items)
	assert.True(t, items[missingRefresh.Id].Applied)
	assert.True(t, items[refreshable.Id].Applied)

	var expiredStored model.AccountPoolAccount
	require.NoError(t, model.DB.First(&expiredStored, missingRefresh.Id).Error)
	assert.Equal(t, model.AccountPoolAccountStatusExpired, expiredStored.Status)
	var refreshedStored model.AccountPoolAccount
	require.NoError(t, model.DB.First(&refreshedStored, refreshable.Id).Error)
	refreshedState, err := DecryptAccountPoolTokenState(refreshedStored.TokenState)
	require.NoError(t, err)
	assert.Equal(t, "new-access-secret", refreshedState.AccessToken)
	assert.Equal(t, int64(5), refreshedState.Version)

	serializedBytes, err := common.Marshal(result)
	require.NoError(t, err)
	serialized := string(serializedBytes)
	assert.NotContains(t, serialized, "old-access-secret")
	assert.NotContains(t, serialized, "old-refresh-secret")
	assert.NotContains(t, serialized, "new-access-secret")
	assert.NotContains(t, serialized, "new-refresh-secret")
}

func TestAccountPoolServiceReconcileXAIOAuthAccountsCASLetsConcurrentRefreshWin(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	svc := AccountPoolService{}
	pool := createAccountPoolServiceTestPoolWithPlatform(t, svc, model.AccountPoolPlatformXAI)
	account := createAccountPoolSchedulerAccount(t, svc, pool.Id, AccountPoolAccountCreateParams{
		Name:       "concurrent-winner",
		Credential: AccountPoolCredentialConfig{Type: AccountPoolCredentialTypeOAuth, RefreshToken: "old-refresh"},
		TokenState: AccountPoolTokenState{AccessToken: "expired", ExpiresAt: 900, Version: 1},
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		winnerCredential, err := EncryptAccountPoolCredentialConfig(AccountPoolCredentialConfig{Type: AccountPoolCredentialTypeOAuth, RefreshToken: "winner-refresh"})
		require.NoError(t, err)
		winnerState, err := EncryptAccountPoolTokenState(AccountPoolTokenState{AccessToken: "winner-access", RefreshToken: "winner-refresh", ExpiresAt: time.Now().Add(time.Hour).Unix(), Version: 2})
		require.NoError(t, err)
		require.NoError(t, model.DB.Model(&model.AccountPoolAccount{}).Where("id = ?", account.Id).Updates(map[string]any{
			"credential_config": winnerCredential,
			"token_state":       winnerState,
		}).Error)
		_, _ = w.Write([]byte(`{"access_token":"loser-access","refresh_token":"loser-refresh","expires_in":3600}`))
	}))
	defer srv.Close()
	overrideXAIOAuthTokenURLForTest(t, srv.URL)

	result, err := svc.ReconcileXAIOAuthAccounts(context.Background(), AccountPoolXAIOAuthReconcileParams{
		PoolID: pool.Id,
		DryRun: false,
		Now:    1000,
	})

	require.NoError(t, err)
	require.Len(t, result.Items, 1)
	assert.False(t, result.Items[0].Applied)
	assert.Equal(t, AccountPoolXAIOAuthReconcileOutcomeConcurrentUpdate, result.Items[0].Outcome)
	var stored model.AccountPoolAccount
	require.NoError(t, model.DB.First(&stored, account.Id).Error)
	state, err := DecryptAccountPoolTokenState(stored.TokenState)
	require.NoError(t, err)
	assert.Equal(t, "winner-access", state.AccessToken)
	assert.Equal(t, "winner-refresh", state.RefreshToken)
}

func TestAccountPoolServiceReconcileXAIOAuthAccountsExpiresRejectedCredential(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	svc := AccountPoolService{}
	pool := createAccountPoolServiceTestPoolWithPlatform(t, svc, model.AccountPoolPlatformXAI)
	account := createAccountPoolSchedulerAccount(t, svc, pool.Id, AccountPoolAccountCreateParams{
		Name:       "rejected-during-reconcile",
		Credential: AccountPoolCredentialConfig{Type: AccountPoolCredentialTypeOAuth, RefreshToken: "rejected-refresh"},
		TokenState: AccountPoolTokenState{AccessToken: "expired", ExpiresAt: 900, Version: 1},
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"rejected-refresh is invalid"}`))
	}))
	defer srv.Close()
	overrideXAIOAuthTokenURLForTest(t, srv.URL)

	result, err := svc.ReconcileXAIOAuthAccounts(context.Background(), AccountPoolXAIOAuthReconcileParams{
		PoolID: pool.Id,
		DryRun: false,
		Now:    1000,
	})

	require.NoError(t, err)
	require.Len(t, result.Items, 1)
	assert.Equal(t, 1, result.Applied)
	assert.True(t, result.Items[0].Applied)
	assert.Equal(t, AccountPoolXAIOAuthReconcileOutcomeCredentialRejected, result.Items[0].Outcome)
	var stored model.AccountPoolAccount
	require.NoError(t, model.DB.First(&stored, account.Id).Error)
	assert.Equal(t, model.AccountPoolAccountStatusExpired, stored.Status)
	assert.Contains(t, stored.LastError, "invalid_grant")
	assert.NotContains(t, stored.LastError, "rejected-refresh")
}

func TestAccountPoolServiceReconcileXAIOAuthAccountsCoolsDownTransientRefreshFailure(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	svc := AccountPoolService{}
	pool := createAccountPoolServiceTestPoolWithPlatform(t, svc, model.AccountPoolPlatformXAI)
	account := createAccountPoolSchedulerAccount(t, svc, pool.Id, AccountPoolAccountCreateParams{
		Name:       "transient-during-reconcile",
		Credential: AccountPoolCredentialConfig{Type: AccountPoolCredentialTypeOAuth, RefreshToken: "refresh"},
		TokenState: AccountPoolTokenState{AccessToken: "expired", ExpiresAt: 900, Version: 1},
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"temporarily_unavailable"}`))
	}))
	defer srv.Close()
	overrideXAIOAuthTokenURLForTest(t, srv.URL)

	result, err := svc.ReconcileXAIOAuthAccounts(context.Background(), AccountPoolXAIOAuthReconcileParams{
		PoolID: pool.Id,
		DryRun: false,
		Now:    1000,
	})

	require.NoError(t, err)
	require.Len(t, result.Items, 1)
	assert.Zero(t, result.Applied)
	assert.Equal(t, 1, result.Skipped)
	assert.Equal(t, AccountPoolXAIOAuthReconcileOutcomeRefreshFailed, result.Items[0].Outcome)
	var stored model.AccountPoolAccount
	require.NoError(t, model.DB.First(&stored, account.Id).Error)
	assert.Equal(t, model.AccountPoolAccountStatusEnabled, stored.Status)
	assert.Equal(t, int64(1060), stored.TempDisabledUntil)
}

func accountPoolXAIOAuthReconcileItemsByID(items []AccountPoolXAIOAuthReconcileItem) map[int]AccountPoolXAIOAuthReconcileItem {
	result := make(map[int]AccountPoolXAIOAuthReconcileItem, len(items))
	for _, item := range items {
		result[item.AccountID] = item
	}
	return result
}
