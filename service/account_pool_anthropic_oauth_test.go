package service

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestApplyAccountPoolRuntimeSelectionSetsOAuthFlagForAnthropicOAuth verifies that when an
// Anthropic OAuth account (with a live access token) is selected, info.RuntimeAnthropicOAuth
// is set to true.
func TestApplyAccountPoolRuntimeSelectionSetsOAuthFlagForAnthropicOAuth(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	svc := AccountPoolService{}
	pool := createAccountPoolServiceTestPoolWithPlatform(t, svc, model.AccountPoolPlatformAnthropic)
	channel := createAccountPoolServiceTestChannelWithType(t, constant.ChannelTypeAnthropic, common.ChannelStatusManuallyDisabled)
	createEnabledAccountPoolSchedulerBinding(t, pool.Id, channel.Id, AccountPoolAccountFilterConfig{}, AccountPoolModelPolicy{})
	createAccountPoolSchedulerAccount(t, svc, pool.Id, AccountPoolAccountCreateParams{
		Name: "anthropic-oauth",
		Credential: AccountPoolCredentialConfig{
			Type: AccountPoolCredentialTypeOAuth,
		},
		TokenState: AccountPoolTokenState{
			AccessToken: "at-anthropic-live",
			ExpiresAt:   9999999999,
		},
	})

	ctx := newAccountPoolRuntimeTestContext()
	info := &relaycommon.RelayInfo{
		OriginModelName: "claude-opus-4",
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelId:         channel.Id,
			ApiKey:            "sk-channel",
			UpstreamModelName: "claude-opus-4",
		},
	}

	err := ApplyAccountPoolRuntimeSelection(ctx, info, nil)

	require.NoError(t, err)
	defer ReleaseAccountPoolRuntimeSelection(ctx)
	assert.True(t, info.RuntimeAnthropicOAuth, "Anthropic OAuth account must set RuntimeAnthropicOAuth=true")
	assert.Equal(t, "at-anthropic-live", info.ApiKey)
}

// TestApplyAccountPoolRuntimeSelectionFalseForAnthropicAPIKey verifies that an Anthropic
// API-key account does NOT set RuntimeAnthropicOAuth.
func TestApplyAccountPoolRuntimeSelectionFalseForAnthropicAPIKey(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	svc := AccountPoolService{}
	pool := createAccountPoolServiceTestPoolWithPlatform(t, svc, model.AccountPoolPlatformAnthropic)
	channel := createAccountPoolServiceTestChannelWithType(t, constant.ChannelTypeAnthropic, common.ChannelStatusManuallyDisabled)
	createEnabledAccountPoolSchedulerBinding(t, pool.Id, channel.Id, AccountPoolAccountFilterConfig{}, AccountPoolModelPolicy{})
	createAccountPoolSchedulerAccount(t, svc, pool.Id, AccountPoolAccountCreateParams{
		Name: "anthropic-apikey",
		Credential: AccountPoolCredentialConfig{
			Type:   AccountPoolCredentialTypeAPIKey,
			APIKey: "sk-anthropic-direct",
		},
	})

	ctx := newAccountPoolRuntimeTestContext()
	info := &relaycommon.RelayInfo{
		OriginModelName: "claude-opus-4",
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelId:         channel.Id,
			ApiKey:            "sk-channel",
			UpstreamModelName: "claude-opus-4",
		},
	}

	err := ApplyAccountPoolRuntimeSelection(ctx, info, nil)

	require.NoError(t, err)
	defer ReleaseAccountPoolRuntimeSelection(ctx)
	assert.False(t, info.RuntimeAnthropicOAuth, "Anthropic API-key account must NOT set RuntimeAnthropicOAuth")
	assert.Equal(t, "sk-anthropic-direct", info.ApiKey)
}

// TestApplyAccountPoolRuntimeSelectionFalseForOpenAIAccount verifies that an OpenAI
// account does NOT set RuntimeAnthropicOAuth regardless of credential type.
func TestApplyAccountPoolRuntimeSelectionFalseForOpenAIAccount(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	svc := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, svc) // platform = OpenAI
	channel := createAccountPoolServiceTestChannel(t, common.ChannelStatusManuallyDisabled)
	createEnabledAccountPoolSchedulerBinding(t, pool.Id, channel.Id, AccountPoolAccountFilterConfig{}, AccountPoolModelPolicy{})
	createAccountPoolSchedulerAccount(t, svc, pool.Id, AccountPoolAccountCreateParams{
		Name: "openai-oauth-account",
		Credential: AccountPoolCredentialConfig{
			Type: AccountPoolCredentialTypeOAuth,
		},
		TokenState: AccountPoolTokenState{
			AccessToken: "at-openai-live",
			ExpiresAt:   9999999999,
		},
	})

	ctx := newAccountPoolRuntimeTestContext()
	info := &relaycommon.RelayInfo{
		OriginModelName: "gpt-5",
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelId:         channel.Id,
			ApiKey:            "sk-channel",
			UpstreamModelName: "gpt-5",
		},
	}

	err := ApplyAccountPoolRuntimeSelection(ctx, info, nil)

	require.NoError(t, err)
	defer ReleaseAccountPoolRuntimeSelection(ctx)
	assert.False(t, info.RuntimeAnthropicOAuth, "OpenAI account must NOT set RuntimeAnthropicOAuth")
}

// TestApplyAccountPoolRuntimeSelectionResetsOAuthFlagOnRelease verifies that
// RuntimeAnthropicOAuth is reset to false at the start of a new selection call,
// even when it was previously set to true.
func TestApplyAccountPoolRuntimeSelectionResetsOAuthFlagOnRelease(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	channel := createAccountPoolServiceTestChannelWithType(t, constant.ChannelTypeOpenAI, common.ChannelStatusManuallyDisabled)

	ctx := newAccountPoolRuntimeTestContext()
	info := &relaycommon.RelayInfo{
		RuntimeAnthropicOAuth: true, // pre-set to true to verify it gets cleared
		OriginModelName:       "gpt-5",
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelId:         channel.Id,
			ApiKey:            "sk-channel",
			UpstreamModelName: "gpt-5",
		},
	}

	// No pool binding: selection is a no-op but must still reset the flag.
	err := ApplyAccountPoolRuntimeSelection(ctx, info, nil)

	require.NoError(t, err)
	assert.False(t, info.RuntimeAnthropicOAuth, "RuntimeAnthropicOAuth must be reset to false when no selection applies")
}
