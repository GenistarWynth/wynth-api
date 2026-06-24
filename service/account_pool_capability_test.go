package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type accountPoolCapabilityProbeRequestPayload struct {
	Model    string `json:"model"`
	Messages []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"messages"`
	MaxTokens int  `json:"max_tokens"`
	Stream    bool `json:"stream"`
}

func TestAccountPoolCapabilityProbeModelsAppliesOnlySupportedCandidates(t *testing.T) {
	withSub2APIFetchSetting(t, true)
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)

	account, err := service.CreateAccount(AccountPoolAccountCreateParams{
		PoolID: pool.Id,
		Name:   "probe-apply-account",
		Credential: AccountPoolCredentialConfig{
			Type:   AccountPoolCredentialTypeAPIKey,
			APIKey: "sk-probe-secret",
		},
		SupportedModels: []string{"existing"},
	})
	require.NoError(t, err)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/v1/chat/completions", r.URL.Path)
		assert.Equal(t, "Bearer sk-probe-secret", r.Header.Get("Authorization"))

		var payload accountPoolCapabilityProbeRequestPayload
		require.NoError(t, common.DecodeJson(r.Body, &payload))
		assert.Len(t, payload.Messages, 1)
		assert.Equal(t, "user", payload.Messages[0].Role)
		assert.Equal(t, "ping", payload.Messages[0].Content)
		assert.Equal(t, 1, payload.MaxTokens)
		assert.False(t, payload.Stream)

		w.Header().Set("Content-Type", "application/json")
		switch payload.Model {
		case "gpt-5":
			_, _ = w.Write([]byte(`{"id":"ok"}`))
		case "missing-model":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"message":"model missing-model not found"}}`))
		default:
			t.Fatalf("unexpected probed model %q", payload.Model)
		}
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
		Mode:            AccountPoolCapabilityModeProbeModels,
		CandidateModels: []string{"gpt-5", "missing-model"},
		Apply:           true,
		Merge:           false,
	})
	require.NoError(t, err)
	assert.Equal(t, AccountPoolCapabilityStatusPartial, result.Status)
	assert.Equal(t, []string{"gpt-5"}, result.DetectedModels)
	assert.Equal(t, []string{"gpt-5"}, result.AppliedModels)
	require.NotEmpty(t, result.Errors)
	assert.Contains(t, strings.Join(result.Errors, "\n"), "missing-model")
	assert.NotContains(t, strings.Join(result.Errors, "\n"), "sk-probe-secret")

	stored := loadAccountPoolCapabilityTestAccount(t, account.Id)
	assert.Equal(t, []string{"gpt-5"}, mustUnmarshalAccountPoolCapabilityModels(t, stored.SupportedModels))
	assert.Equal(t, AccountPoolCapabilityStatusPartial, stored.LastCapabilityCheckStatus)
	assert.Equal(t, []string{"gpt-5"}, mustUnmarshalAccountPoolCapabilityModels(t, stored.LastCapabilityCheckModels))
	assert.NotContains(t, stored.LastCapabilityCheckError, "sk-probe-secret")
}

func TestAccountPoolCapabilityProbeModelsTreatsStructuredModelCodeAsUnsupported(t *testing.T) {
	withSub2APIFetchSetting(t, true)
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)

	account, err := service.CreateAccount(AccountPoolAccountCreateParams{
		PoolID: pool.Id,
		Name:   "probe-structured-error-account",
		Credential: AccountPoolCredentialConfig{
			Type:   AccountPoolCredentialTypeAPIKey,
			APIKey: "sk-probe-structured-secret",
		},
		SupportedModels: []string{"existing"},
	})
	require.NoError(t, err)

	probedModels := make([]string, 0, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload accountPoolCapabilityProbeRequestPayload
		require.NoError(t, common.DecodeJson(r.Body, &payload))
		probedModels = append(probedModels, payload.Model)

		w.Header().Set("Content-Type", "application/json")
		switch payload.Model {
		case "missing-model":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":"model_not_found","message":"The requested identifier does not exist"}}`))
		case "gpt-5":
			_, _ = w.Write([]byte(`{"id":"ok"}`))
		default:
			t.Fatalf("unexpected probed model %q", payload.Model)
		}
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
		Mode:            AccountPoolCapabilityModeProbeModels,
		CandidateModels: []string{"missing-model", "gpt-5"},
		Apply:           true,
		Merge:           false,
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"missing-model", "gpt-5"}, probedModels)
	assert.Equal(t, AccountPoolCapabilityStatusPartial, result.Status)
	assert.Equal(t, []string{"gpt-5"}, result.DetectedModels)
	assert.Equal(t, []string{"gpt-5"}, result.AppliedModels)
	require.NotEmpty(t, result.Errors)
	assert.Contains(t, strings.Join(result.Errors, "\n"), "missing-model")
	assert.NotContains(t, strings.Join(result.Errors, "\n"), "sk-probe-structured-secret")

	stored := loadAccountPoolCapabilityTestAccount(t, account.Id)
	assert.Equal(t, []string{"gpt-5"}, mustUnmarshalAccountPoolCapabilityModels(t, stored.SupportedModels))
	assert.Equal(t, AccountPoolCapabilityStatusPartial, stored.LastCapabilityCheckStatus)
	assert.Equal(t, []string{"gpt-5"}, mustUnmarshalAccountPoolCapabilityModels(t, stored.LastCapabilityCheckModels))
	assert.NotContains(t, stored.LastCapabilityCheckError, "sk-probe-structured-secret")
}

func TestAccountPoolCapabilityProbeModelsTreatsCamelCaseDeploymentCodeAsUnsupported(t *testing.T) {
	withSub2APIFetchSetting(t, true)
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)

	account, err := service.CreateAccount(AccountPoolAccountCreateParams{
		PoolID: pool.Id,
		Name:   "probe-camelcase-error-account",
		Credential: AccountPoolCredentialConfig{
			Type:   AccountPoolCredentialTypeAPIKey,
			APIKey: "sk-probe-camelcase-secret",
		},
		SupportedModels: []string{"existing"},
	})
	require.NoError(t, err)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload accountPoolCapabilityProbeRequestPayload
		require.NoError(t, common.DecodeJson(r.Body, &payload))

		w.Header().Set("Content-Type", "application/json")
		switch payload.Model {
		case "missing-deployment":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":"DeploymentNotFound","message":"The deployment was not found"}}`))
		case "gpt-5":
			_, _ = w.Write([]byte(`{"id":"ok"}`))
		default:
			t.Fatalf("unexpected probed model %q", payload.Model)
		}
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
		Mode:            AccountPoolCapabilityModeProbeModels,
		CandidateModels: []string{"missing-deployment", "gpt-5"},
		Apply:           true,
		Merge:           false,
	})
	require.NoError(t, err)
	assert.Equal(t, AccountPoolCapabilityStatusPartial, result.Status)
	assert.Equal(t, []string{"gpt-5"}, result.DetectedModels)
	assert.Equal(t, []string{"gpt-5"}, result.AppliedModels)
	require.NotEmpty(t, result.Errors)
	assert.Contains(t, strings.Join(result.Errors, "\n"), "missing-deployment")
	assert.NotContains(t, strings.Join(result.Errors, "\n"), "sk-probe-camelcase-secret")

	stored := loadAccountPoolCapabilityTestAccount(t, account.Id)
	assert.Equal(t, []string{"gpt-5"}, mustUnmarshalAccountPoolCapabilityModels(t, stored.SupportedModels))
	assert.Equal(t, AccountPoolCapabilityStatusPartial, stored.LastCapabilityCheckStatus)
	assert.Equal(t, []string{"gpt-5"}, mustUnmarshalAccountPoolCapabilityModels(t, stored.LastCapabilityCheckModels))
	assert.NotContains(t, stored.LastCapabilityCheckError, "sk-probe-camelcase-secret")
}

func TestAccountPoolCapabilityProbeRequiresCandidateModels(t *testing.T) {
	withSub2APIFetchSetting(t, true)
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)

	account, err := service.CreateAccount(AccountPoolAccountCreateParams{
		PoolID: pool.Id,
		Name:   "probe-requires-candidates-account",
		Credential: AccountPoolCredentialConfig{
			Type:   AccountPoolCredentialTypeAPIKey,
			APIKey: "sk-probe-requires",
		},
		SupportedModels: []string{"existing"},
	})
	require.NoError(t, err)

	channel := createAccountPoolCapabilityTestChannel(t, "https://example.com")
	_, err = service.CreateBinding(AccountPoolBindingCreateParams{
		PoolID:    pool.Id,
		ChannelID: channel.Id,
	})
	require.NoError(t, err)

	result, err := service.DetectAccountCapability(context.Background(), AccountPoolCapabilityDetectRequest{
		PoolID:    pool.Id,
		AccountID: account.Id,
		ChannelID: channel.Id,
		Mode:      AccountPoolCapabilityModeProbeModels,
		Apply:     true,
	})
	require.NoError(t, err)
	assert.Equal(t, AccountPoolCapabilityStatusConfigError, result.Status)
	assert.False(t, accountPoolCapabilitySucceeded(result.Status))
	require.NotEmpty(t, result.Errors)
	assert.Contains(t, result.Errors[0], "candidate")
	assert.Empty(t, result.DetectedModels)
	assert.Empty(t, result.AppliedModels)

	stored := loadAccountPoolCapabilityTestAccount(t, account.Id)
	assert.Equal(t, `["existing"]`, stored.SupportedModels)
	assert.Equal(t, AccountPoolCapabilityStatusConfigError, stored.LastCapabilityCheckStatus)
	assert.NotEmpty(t, stored.LastCapabilityCheckError)
	assert.Equal(t, "[]", stored.LastCapabilityCheckModels)
}

func TestAccountPoolCapabilityProbeModelsStopsOnAuthErrorWithoutApply(t *testing.T) {
	withSub2APIFetchSetting(t, true)
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)

	account, err := service.CreateAccount(AccountPoolAccountCreateParams{
		PoolID: pool.Id,
		Name:   "probe-auth-stop-account",
		Credential: AccountPoolCredentialConfig{
			Type:   AccountPoolCredentialTypeAPIKey,
			APIKey: "sk-probe-auth-secret",
		},
		SupportedModels: []string{"existing"},
	})
	require.NoError(t, err)

	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		var payload accountPoolCapabilityProbeRequestPayload
		require.NoError(t, common.DecodeJson(r.Body, &payload))
		assert.Equal(t, "gpt-5", payload.Model)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"bearer sk-probe-auth-secret rejected"}}`))
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
		Mode:            AccountPoolCapabilityModeProbeModels,
		CandidateModels: []string{"gpt-5", "gpt-5-mini"},
		Apply:           true,
	})
	require.NoError(t, err)
	assert.Equal(t, AccountPoolCapabilityStatusAuthError, result.Status)
	assert.Equal(t, 1, requestCount)
	require.NotEmpty(t, result.Errors)
	assert.NotContains(t, result.Errors[0], "sk-probe-auth-secret")
	assert.Empty(t, result.DetectedModels)
	assert.Empty(t, result.AppliedModels)

	stored := loadAccountPoolCapabilityTestAccount(t, account.Id)
	assert.Equal(t, `["existing"]`, stored.SupportedModels)
	assert.Equal(t, AccountPoolCapabilityStatusAuthError, stored.LastCapabilityCheckStatus)
	assert.NotContains(t, stored.LastCapabilityCheckError, "sk-probe-auth-secret")
	assert.Equal(t, "[]", stored.LastCapabilityCheckModels)
}

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
	require.NotNil(t, result.Errors)
	assert.Empty(t, result.Errors)

	stored := loadAccountPoolCapabilityTestAccount(t, account.Id)
	assert.Equal(t, `["existing"]`, stored.SupportedModels)
	assert.Zero(t, stored.LastCapabilityCheckAt)
	assert.Empty(t, stored.LastCapabilityCheckStatus)
	assert.Empty(t, stored.LastCapabilityCheckError)
	assert.Empty(t, stored.LastCapabilityCheckModels)
}

func TestAccountPoolCapabilityDetectModelsEndpointApplyEmptyDetectedDoesNotOverwriteSupportedModels(t *testing.T) {
	withSub2APIFetchSetting(t, true)
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)

	account, err := service.CreateAccount(AccountPoolAccountCreateParams{
		PoolID: pool.Id,
		Name:   "empty-detected-account",
		Credential: AccountPoolCredentialConfig{
			Type:   AccountPoolCredentialTypeAPIKey,
			APIKey: "sk-empty-detected",
		},
		SupportedModels: []string{"existing"},
	})
	require.NoError(t, err)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/models", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[]}`))
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
	assert.Equal(t, AccountPoolCapabilityStatusUnsupported, result.Status)
	require.NotNil(t, result.Errors)
	assert.NotEmpty(t, result.Errors)
	assert.Empty(t, result.AppliedModels)
	assert.Empty(t, result.DetectedModels)

	stored := loadAccountPoolCapabilityTestAccount(t, account.Id)
	assert.Equal(t, `["existing"]`, stored.SupportedModels)
	assert.Equal(t, AccountPoolCapabilityStatusUnsupported, stored.LastCapabilityCheckStatus)
	assert.NotEmpty(t, stored.LastCapabilityCheckError)
	assert.Equal(t, "[]", stored.LastCapabilityCheckModels)
}

func TestAccountPoolCapabilityDetectModelsEndpointApplyCandidateMissDoesNotOverwriteSupportedModels(t *testing.T) {
	withSub2APIFetchSetting(t, true)
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)

	account, err := service.CreateAccount(AccountPoolAccountCreateParams{
		PoolID: pool.Id,
		Name:   "candidate-miss-account",
		Credential: AccountPoolCredentialConfig{
			Type:   AccountPoolCredentialTypeAPIKey,
			APIKey: "sk-candidate-miss",
		},
		SupportedModels: []string{"existing"},
	})
	require.NoError(t, err)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/models", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"account-gpt-5"}]}`))
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
		CandidateModels: []string{"channel-gpt-5"},
		Apply:           true,
	})
	require.NoError(t, err)
	assert.Equal(t, AccountPoolCapabilityStatusUnsupported, result.Status)
	require.NotNil(t, result.Errors)
	assert.NotEmpty(t, result.Errors)
	assert.Empty(t, result.AppliedModels)
	assert.Empty(t, result.DetectedModels)

	stored := loadAccountPoolCapabilityTestAccount(t, account.Id)
	assert.Equal(t, `["existing"]`, stored.SupportedModels)
	assert.Equal(t, AccountPoolCapabilityStatusUnsupported, stored.LastCapabilityCheckStatus)
	assert.NotEmpty(t, stored.LastCapabilityCheckError)
	assert.Equal(t, "[]", stored.LastCapabilityCheckModels)
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

func TestAccountPoolCapabilityDetectModelsEndpointApplyStoresMappingKeysAndRawDetectedModels(t *testing.T) {
	withSub2APIFetchSetting(t, true)
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)

	account, err := service.CreateAccount(AccountPoolAccountCreateParams{
		PoolID: pool.Id,
		Name:   "mapping-aware-account",
		Credential: AccountPoolCredentialConfig{
			Type:   AccountPoolCredentialTypeAPIKey,
			APIKey: "sk-mapping-aware",
		},
		SupportedModels: []string{"existing"},
	})
	require.NoError(t, err)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/models", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"account-gpt-5"}]}`))
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
		ModelMapping: map[string]string{
			"channel-gpt-5": "account-gpt-5",
		},
	})
	require.NoError(t, err)
	assert.Equal(t, AccountPoolCapabilityStatusSuccess, result.Status)
	assert.Equal(t, []string{"account-gpt-5"}, result.DetectedModels)
	assert.Equal(t, []string{"channel-gpt-5"}, result.AppliedModels)
	require.NotNil(t, result.Errors)
	assert.Empty(t, result.Errors)

	stored := loadAccountPoolCapabilityTestAccount(t, account.Id)
	assert.Equal(t, []string{"channel-gpt-5"}, mustUnmarshalAccountPoolCapabilityModels(t, stored.SupportedModels))
	assert.Equal(t, []string{"account-gpt-5"}, mustUnmarshalAccountPoolCapabilityModels(t, stored.LastCapabilityCheckModels))
	assert.Equal(t, map[string]string{"channel-gpt-5": "account-gpt-5"}, mustUnmarshalAccountPoolCapabilityMapping(t, stored.ModelMapping))
}

func TestAccountPoolCapabilityDetectModelsEndpointApplyRejectsUndetectedMappingValue(t *testing.T) {
	withSub2APIFetchSetting(t, true)
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)

	account, err := service.CreateAccount(AccountPoolAccountCreateParams{
		PoolID: pool.Id,
		Name:   "mapping-miss-account",
		Credential: AccountPoolCredentialConfig{
			Type:   AccountPoolCredentialTypeAPIKey,
			APIKey: "sk-mapping-miss",
		},
		SupportedModels: []string{"existing"},
	})
	require.NoError(t, err)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/models", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"account-gpt-5"}]}`))
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
		ModelMapping: map[string]string{
			"channel-gpt-5": "account-gpt-5-missing",
		},
	})
	require.NoError(t, err)
	assert.Equal(t, AccountPoolCapabilityStatusConfigError, result.Status)
	require.NotNil(t, result.Errors)
	assert.NotEmpty(t, result.Errors)
	assert.Empty(t, result.AppliedModels)

	stored := loadAccountPoolCapabilityTestAccount(t, account.Id)
	assert.Equal(t, `["existing"]`, stored.SupportedModels)
	assert.Equal(t, AccountPoolCapabilityStatusConfigError, stored.LastCapabilityCheckStatus)
	assert.NotEmpty(t, stored.LastCapabilityCheckError)
	assert.Equal(t, "[]", stored.LastCapabilityCheckModels)
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

func TestAccountPoolCapabilityDetectPoolCapabilitiesDeletedPoolReturnsError(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)

	createAccountPoolSchedulerAccount(t, service, pool.Id, AccountPoolAccountCreateParams{
		Name: "deleted-pool-account",
		Credential: AccountPoolCredentialConfig{
			Type:   AccountPoolCredentialTypeAPIKey,
			APIKey: "sk-deleted-pool",
		},
	})

	require.NoError(t, model.DB.Model(&model.AccountPool{}).
		Where("id = ?", pool.Id).
		Update("status", model.AccountPoolStatusDeleted).Error)

	result, err := service.DetectPoolCapabilities(context.Background(), AccountPoolCapabilityDetectRequest{
		PoolID: pool.Id,
	})
	require.Error(t, err)
	assert.Zero(t, result.Total)
	require.NotNil(t, result.Results)
	assert.Empty(t, result.Results)
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

func mustUnmarshalAccountPoolCapabilityMapping(t *testing.T, raw string) map[string]string {
	t.Helper()

	var modelMapping map[string]string
	require.NoError(t, common.UnmarshalJsonStr(raw, &modelMapping))
	return modelMapping
}
