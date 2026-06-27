package service

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// createAccountPoolServiceTestPoolWithPlatform creates a pool with the given platform.
func createAccountPoolServiceTestPoolWithPlatform(t *testing.T, svc AccountPoolService, platform string) model.AccountPool {
	t.Helper()
	pool, err := svc.CreatePool(AccountPoolCreateParams{
		Name:     "pool-" + platform,
		Platform: platform,
	})
	require.NoError(t, err)
	return pool
}

// TestCreateBindingRejectsAnthropicChannelOnOpenAIPool verifies that binding an Anthropic
// channel to an openai pool is rejected with a clear compatibility error.
func TestCreateBindingRejectsAnthropicChannelOnOpenAIPool(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	svc := AccountPoolService{}
	pool := createAccountPoolServiceTestPoolWithPlatform(t, svc, model.AccountPoolPlatformOpenAI)
	channel := createAccountPoolServiceTestChannelWithType(t, constant.ChannelTypeAnthropic, common.ChannelStatusManuallyDisabled)

	_, err := svc.CreateBinding(AccountPoolBindingCreateParams{
		PoolID:    pool.Id,
		ChannelID: channel.Id,
	})

	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "not compatible"), "expected compatibility error, got: %s", err.Error())
}

// TestCreateBindingAllowsOpenAIChannelOnOpenAIPool verifies openai pool + openai channel = OK.
func TestCreateBindingAllowsOpenAIChannelOnOpenAIPool(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	svc := AccountPoolService{}
	pool := createAccountPoolServiceTestPoolWithPlatform(t, svc, model.AccountPoolPlatformOpenAI)
	channel := createAccountPoolServiceTestChannelWithType(t, constant.ChannelTypeOpenAI, common.ChannelStatusManuallyDisabled)

	_, err := svc.CreateBinding(AccountPoolBindingCreateParams{
		PoolID:    pool.Id,
		ChannelID: channel.Id,
	})

	require.NoError(t, err)
}

// TestCreateBindingAllowsCodexChannelOnOpenAIPool verifies openai pool + codex channel = OK.
func TestCreateBindingAllowsCodexChannelOnOpenAIPool(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	svc := AccountPoolService{}
	pool := createAccountPoolServiceTestPoolWithPlatform(t, svc, model.AccountPoolPlatformOpenAI)
	channel := createAccountPoolServiceTestChannelWithType(t, constant.ChannelTypeCodex, common.ChannelStatusManuallyDisabled)

	_, err := svc.CreateBinding(AccountPoolBindingCreateParams{
		PoolID:    pool.Id,
		ChannelID: channel.Id,
	})

	require.NoError(t, err)
}

// TestCreateBindingAllowsAnthropicChannelOnAnthropicPool verifies anthropic pool + anthropic channel = OK.
func TestCreateBindingAllowsAnthropicChannelOnAnthropicPool(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	svc := AccountPoolService{}
	pool := createAccountPoolServiceTestPoolWithPlatform(t, svc, model.AccountPoolPlatformAnthropic)
	channel := createAccountPoolServiceTestChannelWithType(t, constant.ChannelTypeAnthropic, common.ChannelStatusManuallyDisabled)

	_, err := svc.CreateBinding(AccountPoolBindingCreateParams{
		PoolID:    pool.Id,
		ChannelID: channel.Id,
	})

	require.NoError(t, err)
}

// TestCreateBindingRejectsOpenAIChannelOnAnthropicPool verifies that binding an OpenAI
// channel to an anthropic pool is rejected with a clear compatibility error.
func TestCreateBindingRejectsOpenAIChannelOnAnthropicPool(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	svc := AccountPoolService{}
	pool := createAccountPoolServiceTestPoolWithPlatform(t, svc, model.AccountPoolPlatformAnthropic)
	channel := createAccountPoolServiceTestChannelWithType(t, constant.ChannelTypeOpenAI, common.ChannelStatusManuallyDisabled)

	_, err := svc.CreateBinding(AccountPoolBindingCreateParams{
		PoolID:    pool.Id,
		ChannelID: channel.Id,
	})

	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "not compatible"), "expected compatibility error, got: %s", err.Error())
}

// TestCreateBindingRejectsCodexChannelOnAnthropicPool verifies that binding a Codex
// channel to an anthropic pool is rejected with a clear compatibility error.
func TestCreateBindingRejectsCodexChannelOnAnthropicPool(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	svc := AccountPoolService{}
	pool := createAccountPoolServiceTestPoolWithPlatform(t, svc, model.AccountPoolPlatformAnthropic)
	channel := createAccountPoolServiceTestChannelWithType(t, constant.ChannelTypeCodex, common.ChannelStatusManuallyDisabled)

	_, err := svc.CreateBinding(AccountPoolBindingCreateParams{
		PoolID:    pool.Id,
		ChannelID: channel.Id,
	})

	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "not compatible"), "expected compatibility error, got: %s", err.Error())
}

// TestCreateBoundChannelDefaultsToAnthropicTypeForAnthropicPool verifies that when no
// ChannelType is specified, CreateBoundChannel uses ChannelTypeAnthropic for anthropic pools.
func TestCreateBoundChannelDefaultsToAnthropicTypeForAnthropicPool(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	svc := AccountPoolService{}
	pool := createAccountPoolServiceTestPoolWithPlatform(t, svc, model.AccountPoolPlatformAnthropic)

	view, err := svc.CreateBoundChannel(AccountPoolBoundChannelCreateParams{
		PoolID: pool.Id,
		Name:   "anthropic-auto-channel",
	})

	require.NoError(t, err)
	var channel model.Channel
	require.NoError(t, model.DB.First(&channel, view.ChannelID).Error)
	assert.Equal(t, constant.ChannelTypeAnthropic, channel.Type)
}

// TestCreateBoundChannelDefaultsToOpenAITypeForOpenAIPool verifies that the existing
// behavior for openai pools (default to ChannelTypeOpenAI) is preserved.
func TestCreateBoundChannelDefaultsToOpenAITypeForOpenAIPool(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	svc := AccountPoolService{}
	pool := createAccountPoolServiceTestPoolWithPlatform(t, svc, model.AccountPoolPlatformOpenAI)

	view, err := svc.CreateBoundChannel(AccountPoolBoundChannelCreateParams{
		PoolID: pool.Id,
		Name:   "openai-auto-channel",
	})

	require.NoError(t, err)
	var channel model.Channel
	require.NoError(t, model.DB.First(&channel, view.ChannelID).Error)
	assert.Equal(t, constant.ChannelTypeOpenAI, channel.Type)
}

// TestCreateBoundChannelRejectsOpenAIChannelTypeOnAnthropicPool verifies that explicitly
// specifying a mismatched channel type is rejected, even when ChannelType is explicit.
func TestCreateBoundChannelRejectsOpenAIChannelTypeOnAnthropicPool(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	svc := AccountPoolService{}
	pool := createAccountPoolServiceTestPoolWithPlatform(t, svc, model.AccountPoolPlatformAnthropic)

	_, err := svc.CreateBoundChannel(AccountPoolBoundChannelCreateParams{
		PoolID:      pool.Id,
		Name:        "wrong-type-channel",
		ChannelType: constant.ChannelTypeOpenAI,
	})

	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "not compatible"), "expected compatibility error, got: %s", err.Error())
}

// TestCreateBoundChannelRejectsAnthropicChannelTypeOnOpenAIPool verifies that explicitly
// specifying Anthropic type on an openai pool is rejected.
func TestCreateBoundChannelRejectsAnthropicChannelTypeOnOpenAIPool(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	svc := AccountPoolService{}
	pool := createAccountPoolServiceTestPoolWithPlatform(t, svc, model.AccountPoolPlatformOpenAI)

	_, err := svc.CreateBoundChannel(AccountPoolBoundChannelCreateParams{
		PoolID:      pool.Id,
		Name:        "wrong-type-channel",
		ChannelType: constant.ChannelTypeAnthropic,
	})

	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "not compatible"), "expected compatibility error, got: %s", err.Error())
}
