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

func TestAccountPoolCapabilityDetectModelsEndpointDryRunDoesNotWrite(t *testing.T) {
	withSub2APIFetchSetting(t, true)
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)

	account, err := service.CreateAccount(AccountPoolAccountCreateParams{
		PoolID: pool.Id,
		Name:   "dry-run-account",
		Credential: AccountPoolCredentialConfig{
			Type:   AccountPoolCredentialTypeAPIKey,
			APIKey: "sk-dry-run",
		},
		SupportedModels: []string{"existing"},
	})
	require.NoError(t, err)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/models", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"gpt-5"},{"id":"gpt-5-mini"},{"id":""},{"id":"gpt-5"}]}`))
	}))
	defer server.Close()

	channel := createAccountPoolCapabilityTestChannel(t, server.URL)
	_, err = service.CreateBinding(AccountPoolBindingCreateParams{
		PoolID:    pool.Id,
		ChannelID: channel.Id,
	})
	require.NoError(t, err)

	result, err := service.DetectAccountCapability(context.Background(), AccountPoolCapabilityDetectRequest{
		PoolID:          pool.Id,
		AccountID:       account.Id,
		ChannelID:       channel.Id,
		Mode:            AccountPoolCapabilityModeModelsEndpoint,
		CandidateModels: []string{"gpt-5-mini", "missing"},
		Apply:           false,
	})
	require.NoError(t, err)
	assert.Equal(t, AccountPoolCapabilityStatusSuccess, result.Status)
	assert.Equal(t, []string{"gpt-5-mini"}, result.DetectedModels)

	stored := loadAccountPoolCapabilityTestAccount(t, account.Id)
	assert.Equal(t, `["existing"]`, stored.SupportedModels)
	assert.Zero(t, stored.LastCapabilityCheckAt)
	assert.Empty(t, stored.LastCapabilityCheckStatus)
	assert.Empty(t, stored.LastCapabilityCheckError)
	assert.Empty(t, stored.LastCapabilityCheckModels)
}

func TestAccountPoolCapabilityDetectDryRunOAuthRefreshDoesNotPersistTokenState(t *testing.T) {
	withSub2APIFetchSetting(t, true)
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)

	account, err := service.CreateAccount(AccountPoolAccountCreateParams{
		PoolID: pool.Id,
		Name:   "dry-run-oauth-account",
		Credential: AccountPoolCredentialConfig{
			Type: AccountPoolCredentialTypeOAuth,
		},
		TokenState: AccountPoolTokenState{
			AccessToken:  "access-expired",
			RefreshToken: "refresh-old",
			ExpiresAt:    900,
			Version:      3,
		},
		SupportedModels: []string{"existing"},
	})
	require.NoError(t, err)

	storedBefore := loadAccountPoolCapabilityTestAccount(t, account.Id)
	originalTokenState := storedBefore.TokenState

	setAccountPoolOAuthRefreshForTest(t, func(ctx context.Context, refreshToken string, proxyURL string) (*CodexOAuthTokenResult, error) {
		assert.Equal(t, "refresh-old", refreshToken)
		assert.Empty(t, proxyURL)
		return &CodexOAuthTokenResult{
			AccessToken:  "access-new",
			RefreshToken: "refresh-next",
			ExpiresAt:    time.Unix(2000, 0),
		}, nil
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer access-new", r.Header.Get("Authorization"))
		assert.Equal(t, "/v1/models", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"gpt-5"}]}`))
	}))
	defer server.Close()

	channel := createAccountPoolCapabilityTestChannel(t, server.URL)
	_, err = service.CreateBinding(AccountPoolBindingCreateParams{
		PoolID:    pool.Id,
		ChannelID: channel.Id,
	})
	require.NoError(t, err)

	result, err := service.DetectAccountCapability(context.Background(), AccountPoolCapabilityDetectRequest{
		PoolID:    pool.Id,
		AccountID: account.Id,
		ChannelID: channel.Id,
		Mode:      AccountPoolCapabilityModeModelsEndpoint,
		Apply:     false,
	})
	require.NoError(t, err)
	assert.Equal(t, AccountPoolCapabilityStatusSuccess, result.Status)
	assert.Equal(t, []string{"gpt-5"}, result.DetectedModels)

	storedAfter := loadAccountPoolCapabilityTestAccount(t, account.Id)
	assert.Equal(t, originalTokenState, storedAfter.TokenState)
	assert.Equal(t, `["existing"]`, storedAfter.SupportedModels)
	assert.Zero(t, storedAfter.LastCapabilityCheckAt)
	assert.Empty(t, storedAfter.LastCapabilityCheckStatus)
	assert.Empty(t, storedAfter.LastCapabilityCheckError)
	assert.Empty(t, storedAfter.LastCapabilityCheckModels)
}

func TestAccountPoolCapabilityDetectModelsEndpointApplyMergeAndReplace(t *testing.T) {
	withSub2APIFetchSetting(t, true)
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)

	account, err := service.CreateAccount(AccountPoolAccountCreateParams{
		PoolID: pool.Id,
		Name:   "apply-account",
		Credential: AccountPoolCredentialConfig{
			Type:   AccountPoolCredentialTypeAPIKey,
			APIKey: "sk-apply",
		},
		SupportedModels: []string{"existing"},
	})
	require.NoError(t, err)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/models", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"gpt-5"},{"id":"gpt-5-mini"},{"id":""},{"id":"gpt-5"}]}`))
	}))
	defer server.Close()

	channel := createAccountPoolCapabilityTestChannel(t, server.URL)
	_, err = service.CreateBinding(AccountPoolBindingCreateParams{
		PoolID:    pool.Id,
		ChannelID: channel.Id,
	})
	require.NoError(t, err)

	mergeResult, err := service.DetectAccountCapability(context.Background(), AccountPoolCapabilityDetectRequest{
		PoolID:    pool.Id,
		AccountID: account.Id,
		ChannelID: channel.Id,
		Mode:      AccountPoolCapabilityModeModelsEndpoint,
		Apply:     true,
		Merge:     true,
	})
	require.NoError(t, err)
	assert.Equal(t, AccountPoolCapabilityStatusSuccess, mergeResult.Status)
	assert.Equal(t, []string{"existing", "gpt-5", "gpt-5-mini"}, mergeResult.AppliedModels)

	stored := loadAccountPoolCapabilityTestAccount(t, account.Id)
	assert.Equal(t, []string{"existing", "gpt-5", "gpt-5-mini"}, mustUnmarshalAccountPoolCapabilityModels(t, stored.SupportedModels))
	assert.Equal(t, AccountPoolCapabilityStatusSuccess, stored.LastCapabilityCheckStatus)
	assert.Equal(t, []string{"gpt-5", "gpt-5-mini"}, mustUnmarshalAccountPoolCapabilityModels(t, stored.LastCapabilityCheckModels))
	assert.Empty(t, stored.LastCapabilityCheckError)

	replaceResult, err := service.DetectAccountCapability(context.Background(), AccountPoolCapabilityDetectRequest{
		PoolID:    pool.Id,
		AccountID: account.Id,
		ChannelID: channel.Id,
		Mode:      AccountPoolCapabilityModeModelsEndpoint,
		Apply:     true,
		Merge:     false,
	})
	require.NoError(t, err)
	assert.Equal(t, AccountPoolCapabilityStatusSuccess, replaceResult.Status)
	assert.Equal(t, []string{"gpt-5", "gpt-5-mini"}, replaceResult.AppliedModels)

	stored = loadAccountPoolCapabilityTestAccount(t, account.Id)
	assert.Equal(t, []string{"gpt-5", "gpt-5-mini"}, mustUnmarshalAccountPoolCapabilityModels(t, stored.SupportedModels))
	assert.Equal(t, []string{"gpt-5", "gpt-5-mini"}, mustUnmarshalAccountPoolCapabilityModels(t, stored.LastCapabilityCheckModels))
}

func TestAccountPoolCapabilityDetectRequiresChannelWhenPoolHasMultipleBindings(t *testing.T) {
	withSub2APIFetchSetting(t, true)
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)

	account, err := service.CreateAccount(AccountPoolAccountCreateParams{
		PoolID: pool.Id,
		Name:   "ambiguous-account",
		Credential: AccountPoolCredentialConfig{
			Type:   AccountPoolCredentialTypeAPIKey,
			APIKey: "sk-ambiguous",
		},
		SupportedModels: []string{"existing"},
	})
	require.NoError(t, err)

	firstChannel := createAccountPoolCapabilityTestChannel(t, "https://example.com")
	secondChannel := createAccountPoolCapabilityTestChannel(t, "https://example.net")
	_, err = service.CreateBinding(AccountPoolBindingCreateParams{
		PoolID:    pool.Id,
		ChannelID: firstChannel.Id,
	})
	require.NoError(t, err)
	_, err = service.CreateBinding(AccountPoolBindingCreateParams{
		PoolID:    pool.Id,
		ChannelID: secondChannel.Id,
	})
	require.NoError(t, err)

	result, err := service.DetectAccountCapability(context.Background(), AccountPoolCapabilityDetectRequest{
		PoolID:    pool.Id,
		AccountID: account.Id,
		Mode:      AccountPoolCapabilityModeModelsEndpoint,
		Apply:     true,
	})
	require.NoError(t, err)
	assert.Equal(t, AccountPoolCapabilityStatusConfigError, result.Status)
	require.NotEmpty(t, result.Errors)
	assert.Contains(t, result.Errors[0], "ambiguous")

	stored := loadAccountPoolCapabilityTestAccount(t, account.Id)
	assert.Equal(t, `["existing"]`, stored.SupportedModels)
	assert.Zero(t, stored.LastCapabilityCheckAt)
	assert.Empty(t, stored.LastCapabilityCheckStatus)
	assert.Empty(t, stored.LastCapabilityCheckError)
	assert.Empty(t, stored.LastCapabilityCheckModels)
}

func TestAccountPoolCapabilityDetectSanitizesAuthErrorsAndDoesNotDisableAccount(t *testing.T) {
	withSub2APIFetchSetting(t, true)
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)

	account, err := service.CreateAccount(AccountPoolAccountCreateParams{
		PoolID: pool.Id,
		Name:   "auth-error-account",
		Credential: AccountPoolCredentialConfig{
			Type:   AccountPoolCredentialTypeAPIKey,
			APIKey: "sk-auth",
		},
		LastError: "keep me",
	})
	require.NoError(t, err)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"bearer sk-secret-token rejected"}}`))
	}))
	defer server.Close()

	channel := createAccountPoolCapabilityTestChannel(t, server.URL)
	_, err = service.CreateBinding(AccountPoolBindingCreateParams{
		PoolID:    pool.Id,
		ChannelID: channel.Id,
	})
	require.NoError(t, err)

	result, err := service.DetectAccountCapability(context.Background(), AccountPoolCapabilityDetectRequest{
		PoolID:    pool.Id,
		AccountID: account.Id,
		ChannelID: channel.Id,
		Mode:      AccountPoolCapabilityModeModelsEndpoint,
		Apply:     true,
	})
	require.NoError(t, err)
	assert.Equal(t, AccountPoolCapabilityStatusAuthError, result.Status)
	require.NotEmpty(t, result.Errors)
	assert.NotContains(t, result.Errors[0], "sk-secret-token")

	stored := loadAccountPoolCapabilityTestAccount(t, account.Id)
	assert.Equal(t, model.AccountPoolAccountStatusEnabled, stored.Status)
	assert.Zero(t, stored.TempDisabledUntil)
	assert.Zero(t, stored.RateLimitedUntil)
	assert.Equal(t, AccountPoolCapabilityStatusAuthError, stored.LastCapabilityCheckStatus)
	assert.NotEmpty(t, stored.LastCapabilityCheckError)
	assert.NotContains(t, stored.LastCapabilityCheckError, "sk-secret-token")
	assert.Equal(t, "keep me", stored.LastError)
	assert.Equal(t, "[]", stored.LastCapabilityCheckModels)
}

func createAccountPoolCapabilityTestChannel(t *testing.T, baseURL string) model.Channel {
	t.Helper()

	channel := createAccountPoolServiceTestChannel(t, common.ChannelStatusManuallyDisabled)
	channel.BaseURL = common.GetPointer[string](baseURL)
	require.NoError(t, model.DB.Model(&model.Channel{}).
		Where("id = ?", channel.Id).
		Update("base_url", baseURL).Error)
	return channel
}

func loadAccountPoolCapabilityTestAccount(t *testing.T, accountID int) model.AccountPoolAccount {
	t.Helper()

	var account model.AccountPoolAccount
	require.NoError(t, model.DB.First(&account, accountID).Error)
	return account
}

func mustUnmarshalAccountPoolCapabilityModels(t *testing.T, raw string) []string {
	t.Helper()

	var models []string
	require.NoError(t, common.UnmarshalJsonStr(raw, &models))
	return models
}
