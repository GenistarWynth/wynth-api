package relay

import (
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

// TestAccountPoolClaudeHelperPoolExhaustedBeforeUpstream verifies that a Claude
// channel with an account pool binding stops before reaching the upstream when
// the pool is exhausted (no schedulable account) and returns a retriable 503.
func TestAccountPoolClaudeHelperPoolExhaustedBeforeUpstream(t *testing.T) {
	setupAccountPoolRelayTestDB(t)
	ctx := newAccountPoolRelayTestContext("/v1/messages")
	pool := createAccountPoolRelayTestAnthropicPool(t)
	channel := createAccountPoolRelayTestAnthropicChannel(t)
	createAccountPoolRelayTestEnabledBinding(t, pool.Id, channel.Id)
	setAccountPoolRelayAnthropicChannelContext(ctx, channel.Id)
	common.SetContextKey(ctx, constant.ContextKeyOriginalModel, "claude-opus-4")
	request := &dto.ClaudeRequest{Model: "claude-opus-4"}
	info := relaycommon.GenRelayInfoClaude(ctx, request)

	newAPIError := ClaudeHelper(ctx, info)

	require.NotNil(t, newAPIError)
	require.ErrorIs(t, newAPIError, service.ErrAccountPoolNoSchedulableAccount)
	assert.Equal(t, http.StatusServiceUnavailable, newAPIError.StatusCode)
	assert.Equal(t, types.ErrorCodeGetChannelFailed, newAPIError.GetErrorCode())
	assert.False(t, types.IsSkipRetryError(newAPIError))
}

// TestAccountPoolClaudeAPIKeyAccountSetsOAuthFlagFalse verifies that when a Claude account pool
// attempt is made with an API-key account on Anthropic platform, info.RuntimeAnthropicOAuth
// is false and info.ApiKey carries the API key.
func TestAccountPoolClaudeAPIKeyAccountSetsOAuthFlagFalse(t *testing.T) {
	setupAccountPoolRelayTestDB(t)
	ctx := newAccountPoolRelayTestContext("/v1/messages")
	pool := createAccountPoolRelayTestAnthropicPool(t)
	channel := createAccountPoolRelayTestAnthropicChannel(t)
	createAccountPoolRelayTestEnabledBindingWithRetryTimes(t, pool.Id, channel.Id, 0)

	createAccountPoolRelayTestAccount(t, pool.Id, service.AccountPoolAccountCreateParams{
		Name: "claude-apikey-account",
		Credential: service.AccountPoolCredentialConfig{
			Type:   service.AccountPoolCredentialTypeAPIKey,
			APIKey: "sk-claude-direct",
		},
	})

	// Use runAccountPoolRuntimeAttempts directly to capture what ApplyAccountPoolRuntimeSelection
	// sets on info — ClaudeHelper would attempt an upstream request we cannot intercept.
	info := newAccountPoolRelayTestInfo(channel.Id, "claude-opus-4", "claude-opus-4")
	baseRequest := &dto.GeneralOpenAIRequest{Model: "claude-opus-4"}
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
	assert.False(t, capturedOAuthFlag, "API-key Anthropic account must NOT set RuntimeAnthropicOAuth")
	assert.Equal(t, "sk-claude-direct", capturedApiKey)
}

// TestAccountPoolClaudeOAuthSnapshotRestored verifies that RuntimeAnthropicOAuth
// is properly snapshotted and restored across pool retries.
func TestAccountPoolClaudeOAuthSnapshotRestored(t *testing.T) {
	info := &relaycommon.RelayInfo{
		RuntimeAnthropicOAuth: true,
		ChannelMeta: &relaycommon.ChannelMeta{
			ApiKey:            "sk-original",
			UpstreamModelName: "claude-opus-4",
		},
	}
	snapshot := snapshotAccountPoolRuntimeRelay(info)

	// Mutate the flag (simulating what a pool attempt might do).
	info.RuntimeAnthropicOAuth = false

	restoreAccountPoolRuntimeRelay(info, snapshot)

	assert.True(t, info.RuntimeAnthropicOAuth, "RuntimeAnthropicOAuth must be restored from snapshot")
}

// TestAccountPoolRelayClaudeHelperNoopForUnboundChannel verifies that a Claude channel
// WITHOUT a pool binding passes through to the attempt without 503 rejection.
// The rejectUnsupportedAccountPoolRuntime guard is gone; the pool loop handles unbound
// channels by calling the attempt once (pass-through mode).
func TestAccountPoolRelayClaudeHelperNoopForUnboundChannel(t *testing.T) {
	setupAccountPoolRelayTestDB(t)
	ctx := newAccountPoolRelayTestContext("/v1/messages")
	channel := createAccountPoolRelayTestAnthropicChannel(t)
	// No pool binding — channel is unbound.
	info := newAccountPoolRelayTestInfo(channel.Id, "claude-opus-4", "claude-opus-4")
	baseRequest := &dto.GeneralOpenAIRequest{Model: "claude-opus-4"}
	attemptCalled := false

	// runAccountPoolRuntimeAttempts should call the attempt once (no pool = pass-through).
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
	assert.True(t, attemptCalled, "attempt must be called for unbound (non-pool) channel")
}

// ─── helpers ──────────────────────────────────────────────────────────────────

func createAccountPoolRelayTestAnthropicPool(t *testing.T) model.AccountPool {
	t.Helper()
	pool := model.AccountPool{
		Name:     "relay-anthropic-pool",
		Platform: model.AccountPoolPlatformAnthropic,
		Status:   model.AccountPoolStatusEnabled,
	}
	require.NoError(t, model.DB.Create(&pool).Error)
	return pool
}

func createAccountPoolRelayTestAnthropicChannel(t *testing.T) model.Channel {
	t.Helper()
	channel := model.Channel{
		Type:   constant.ChannelTypeAnthropic,
		Key:    "sk-claude-channel",
		Name:   "relay-claude-channel",
		Status: common.ChannelStatusManuallyDisabled,
	}
	require.NoError(t, model.DB.Create(&channel).Error)
	return channel
}

func setAccountPoolRelayAnthropicChannelContext(ctx *gin.Context, channelID int) {
	common.SetContextKey(ctx, constant.ContextKeyChannelId, channelID)
	common.SetContextKey(ctx, constant.ContextKeyChannelType, constant.ChannelTypeAnthropic)
	common.SetContextKey(ctx, constant.ContextKeyChannelKey, "sk-claude-channel")
}
