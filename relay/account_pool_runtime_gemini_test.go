package relay

import (
	"errors"
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAccountPoolGeminiHelperPoolExhaustedBeforeUpstream verifies that a Gemini
// channel with an account pool binding stops before reaching the upstream when
// the pool is exhausted (no schedulable account) and returns a retriable 503.
func TestAccountPoolGeminiHelperPoolExhaustedBeforeUpstream(t *testing.T) {
	setupAccountPoolRelayTestDB(t)
	ctx := newAccountPoolRelayTestContext("/v1/chat/completions")
	pool := createAccountPoolRelayTestGeminiPool(t)
	channel := createAccountPoolRelayTestGeminiChannel(t)
	createAccountPoolRelayTestEnabledBinding(t, pool.Id, channel.Id)
	setAccountPoolRelayGeminiChannelContext(ctx, channel.Id)
	common.SetContextKey(ctx, constant.ContextKeyOriginalModel, "gemini-2.5-pro")
	request := &dto.GeminiChatRequest{Contents: []dto.GeminiChatContent{{Role: "user", Parts: []dto.GeminiPart{{Text: "hello"}}}}}
	info := newAccountPoolRelayTestGeminiInfo(channel.Id, "gemini-2.5-pro", "gemini-2.5-pro")
	info.Request = request

	newAPIError := GeminiHelper(ctx, info)

	require.NotNil(t, newAPIError)
	require.ErrorIs(t, newAPIError, service.ErrAccountPoolNoSchedulableAccount)
	assert.Equal(t, http.StatusServiceUnavailable, newAPIError.StatusCode)
	assert.Equal(t, types.ErrorCodeGetChannelFailed, newAPIError.GetErrorCode())
	assert.False(t, types.IsSkipRetryError(newAPIError))
}

// TestAccountPoolGeminiAPIKeyAccountSetsOAuthFlagFalse verifies that a Gemini API-key account
// sets info.RuntimeAnthropicOAuth = false and info.ApiKey to the account's api_key.
func TestAccountPoolGeminiAPIKeyAccountSetsOAuthFlagFalse(t *testing.T) {
	setupAccountPoolRelayTestDB(t)
	ctx := newAccountPoolRelayTestContext("/v1/chat/completions")
	pool := createAccountPoolRelayTestGeminiPool(t)
	channel := createAccountPoolRelayTestGeminiChannel(t)
	createAccountPoolRelayTestEnabledBindingWithRetryTimes(t, pool.Id, channel.Id, 0)

	createAccountPoolRelayTestAccount(t, pool.Id, service.AccountPoolAccountCreateParams{
		Name: "gemini-apikey-account",
		Credential: service.AccountPoolCredentialConfig{
			Type:   service.AccountPoolCredentialTypeAPIKey,
			APIKey: "AIzaSy-gemini-test-key",
		},
	})

	// Use runAccountPoolRuntimeAttempts directly to capture what ApplyAccountPoolRuntimeSelection
	// sets on info — GeminiHelper would attempt an upstream request we cannot intercept.
	info := newAccountPoolRelayTestGeminiInfo(channel.Id, "gemini-2.5-pro", "gemini-2.5-pro")
	baseRequest := &dto.GeminiChatRequest{Contents: []dto.GeminiChatContent{{Role: "user", Parts: []dto.GeminiPart{{Text: "hello"}}}}}
	var capturedOAuthFlag bool
	var capturedApiKey string

	newAPIError := runAccountPoolRuntimeAttempts(ctx, info, func() (dto.Request, *types.NewAPIError) {
		req, err := common.DeepCopy(baseRequest)
		if err != nil {
			return nil, types.NewError(err, types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
		}
		return req, nil
	}, func(r dto.Request) *types.NewAPIError {
		capturedOAuthFlag = info.RuntimeAnthropicOAuth
		capturedApiKey = info.ApiKey
		return nil
	})

	require.Nil(t, newAPIError)
	assert.False(t, capturedOAuthFlag, "API-key Gemini account must NOT set RuntimeAnthropicOAuth")
	assert.Equal(t, "AIzaSy-gemini-test-key", capturedApiKey)
}

// TestAccountPoolGeminiHelperNoopForUnboundChannel verifies that a Gemini channel
// WITHOUT a pool binding passes through to the attempt without 503 rejection.
// The rejectUnsupportedAccountPoolRuntime guard is removed; the pool loop handles
// unbound channels by calling the attempt once (pass-through mode).
func TestAccountPoolGeminiHelperNoopForUnboundChannel(t *testing.T) {
	setupAccountPoolRelayTestDB(t)
	ctx := newAccountPoolRelayTestContext("/v1/chat/completions")
	channel := createAccountPoolRelayTestGeminiChannel(t)
	// No pool binding — channel is unbound.
	info := newAccountPoolRelayTestGeminiInfo(channel.Id, "gemini-2.5-pro", "gemini-2.5-pro")
	baseRequest := &dto.GeminiChatRequest{Contents: []dto.GeminiChatContent{{Role: "user", Parts: []dto.GeminiPart{{Text: "hello"}}}}}
	attemptCalled := false

	newAPIError := runAccountPoolRuntimeAttempts(ctx, info, func() (dto.Request, *types.NewAPIError) {
		req, err := common.DeepCopy(baseRequest)
		if err != nil {
			return nil, types.NewError(err, types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
		}
		return req, nil
	}, func(r dto.Request) *types.NewAPIError {
		attemptCalled = true
		return nil
	})

	require.Nil(t, newAPIError)
	assert.True(t, attemptCalled, "attempt must be called for unbound (non-pool) Gemini channel")
}

// TestAccountPoolGeminiFailureRecordingUsesPlatformGemini verifies that when a Gemini
// pool attempt fails with a server error, the failure is recorded and observable in the
// DB row (TempDisabledUntil > 0, LastError set). A 500 is used because a 401 on a
// non-OAuth API-key account expires the account immediately (TempDisabledUntil stays 0).
func TestAccountPoolGeminiFailureRecordingUsesPlatformGemini(t *testing.T) {
	setupAccountPoolRelayTestDB(t)
	ctx := newAccountPoolRelayTestContext("/v1/chat/completions")
	pool := createAccountPoolRelayTestGeminiPool(t)
	channel := createAccountPoolRelayTestGeminiChannel(t)
	createAccountPoolRelayTestEnabledBindingWithRetryTimes(t, pool.Id, channel.Id, 0)

	account := createAccountPoolRelayTestAccount(t, pool.Id, service.AccountPoolAccountCreateParams{
		Name: "gemini-server-error-account",
		Credential: service.AccountPoolCredentialConfig{
			Type:   service.AccountPoolCredentialTypeAPIKey,
			APIKey: "AIzaSy-gemini-fail-key",
		},
	})

	info := newAccountPoolRelayTestGeminiInfo(channel.Id, "gemini-2.5-pro", "gemini-2.5-pro")
	baseRequest := &dto.GeminiChatRequest{Contents: []dto.GeminiChatContent{{Role: "user", Parts: []dto.GeminiPart{{Text: "hello"}}}}}

	newAPIError := runAccountPoolRuntimeAttempts(ctx, info, func() (dto.Request, *types.NewAPIError) {
		req, err := common.DeepCopy(baseRequest)
		if err != nil {
			return nil, types.NewError(err, types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
		}
		return req, nil
	}, func(r dto.Request) *types.NewAPIError {
		return types.NewErrorWithStatusCode(
			errors.New("gemini upstream 500"),
			types.ErrorCodeBadResponseStatusCode,
			http.StatusInternalServerError,
		)
	})

	require.NotNil(t, newAPIError)
	assert.Equal(t, http.StatusInternalServerError, newAPIError.StatusCode)

	var reloaded model.AccountPoolAccount
	require.NoError(t, model.DB.First(&reloaded, account.Id).Error)
	assert.Greater(t, reloaded.TempDisabledUntil, int64(0), "account should be temp-disabled after failure")
	assert.Contains(t, reloaded.LastError, "gemini upstream 500")
}

// ─── helpers ──────────────────────────────────────────────────────────────────

func createAccountPoolRelayTestGeminiPool(t *testing.T) model.AccountPool {
	t.Helper()
	pool := model.AccountPool{
		Name:     "relay-gemini-pool",
		Platform: model.AccountPoolPlatformGemini,
		Status:   model.AccountPoolStatusEnabled,
	}
	require.NoError(t, model.DB.Create(&pool).Error)
	return pool
}

func createAccountPoolRelayTestGeminiChannel(t *testing.T) model.Channel {
	t.Helper()
	channel := model.Channel{
		Type:   constant.ChannelTypeGemini,
		Key:    "AIzaSy-channel-key",
		Name:   "relay-gemini-channel",
		Status: common.ChannelStatusManuallyDisabled,
	}
	require.NoError(t, model.DB.Create(&channel).Error)
	return channel
}

func setAccountPoolRelayGeminiChannelContext(ctx *gin.Context, channelID int) {
	common.SetContextKey(ctx, constant.ContextKeyChannelId, channelID)
	common.SetContextKey(ctx, constant.ContextKeyChannelType, constant.ChannelTypeGemini)
	common.SetContextKey(ctx, constant.ContextKeyChannelKey, "AIzaSy-channel-key")
}

func newAccountPoolRelayTestGeminiInfo(channelID int, originModel string, upstreamModel string) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		OriginModelName: originModel,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelId:         channelID,
			ChannelType:       constant.ChannelTypeGemini,
			ApiType:           constant.APITypeGemini,
			ApiKey:            "AIzaSy-channel-key",
			UpstreamModelName: upstreamModel,
		},
	}
}
