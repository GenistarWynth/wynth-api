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

// TestCreateBindingAllowsGeminiChannelOnGeminiPool verifies gemini pool + gemini channel = OK.
func TestCreateBindingAllowsGeminiChannelOnGeminiPool(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	svc := AccountPoolService{}
	pool := createAccountPoolServiceTestPoolWithPlatform(t, svc, model.AccountPoolPlatformGemini)
	channel := createAccountPoolServiceTestChannelWithType(t, constant.ChannelTypeGemini, common.ChannelStatusManuallyDisabled)

	_, err := svc.CreateBinding(AccountPoolBindingCreateParams{
		PoolID:    pool.Id,
		ChannelID: channel.Id,
	})

	require.NoError(t, err)
}

// TestCreateBindingRejectsOpenAIChannelOnGeminiPool verifies gemini pool + openai channel = error.
func TestCreateBindingRejectsOpenAIChannelOnGeminiPool(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	svc := AccountPoolService{}
	pool := createAccountPoolServiceTestPoolWithPlatform(t, svc, model.AccountPoolPlatformGemini)
	channel := createAccountPoolServiceTestChannelWithType(t, constant.ChannelTypeOpenAI, common.ChannelStatusManuallyDisabled)

	_, err := svc.CreateBinding(AccountPoolBindingCreateParams{
		PoolID:    pool.Id,
		ChannelID: channel.Id,
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "not compatible")
}

// TestCreateBindingRejectsGeminiChannelOnOpenAIPool verifies openai pool + gemini channel = error.
func TestCreateBindingRejectsGeminiChannelOnOpenAIPool(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	svc := AccountPoolService{}
	pool := createAccountPoolServiceTestPoolWithPlatform(t, svc, model.AccountPoolPlatformOpenAI)
	channel := createAccountPoolServiceTestChannelWithType(t, constant.ChannelTypeGemini, common.ChannelStatusManuallyDisabled)

	_, err := svc.CreateBinding(AccountPoolBindingCreateParams{
		PoolID:    pool.Id,
		ChannelID: channel.Id,
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "not compatible")
}

// TestCreateBindingRejectsGeminiChannelOnAnthropicPool verifies anthropic pool + gemini channel = error.
func TestCreateBindingRejectsGeminiChannelOnAnthropicPool(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	svc := AccountPoolService{}
	pool := createAccountPoolServiceTestPoolWithPlatform(t, svc, model.AccountPoolPlatformAnthropic)
	channel := createAccountPoolServiceTestChannelWithType(t, constant.ChannelTypeGemini, common.ChannelStatusManuallyDisabled)

	_, err := svc.CreateBinding(AccountPoolBindingCreateParams{
		PoolID:    pool.Id,
		ChannelID: channel.Id,
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "not compatible")
}

// TestCreateBoundChannelDefaultsToGeminiTypeForGeminiPool verifies that when no
// ChannelType is specified, CreateBoundChannel uses ChannelTypeGemini for gemini pools.
func TestCreateBoundChannelDefaultsToGeminiTypeForGeminiPool(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	svc := AccountPoolService{}
	pool := createAccountPoolServiceTestPoolWithPlatform(t, svc, model.AccountPoolPlatformGemini)

	view, err := svc.CreateBoundChannel(AccountPoolBoundChannelCreateParams{
		PoolID: pool.Id,
		Name:   "gemini-auto-channel",
	})

	require.NoError(t, err)
	var channel model.Channel
	require.NoError(t, model.DB.First(&channel, view.ChannelID).Error)
	assert.Equal(t, constant.ChannelTypeGemini, channel.Type)
}

// TestCreateBindingAllowsXAIChannelOnXAIPool verifies xai pool + xai channel = OK.
func TestCreateBindingAllowsXAIChannelOnXAIPool(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	svc := AccountPoolService{}
	pool := createAccountPoolServiceTestPoolWithPlatform(t, svc, model.AccountPoolPlatformXAI)
	channel := createAccountPoolServiceTestChannelWithType(t, constant.ChannelTypeXai, common.ChannelStatusManuallyDisabled)

	_, err := svc.CreateBinding(AccountPoolBindingCreateParams{
		PoolID:    pool.Id,
		ChannelID: channel.Id,
	})

	require.NoError(t, err)
}

// TestCreateBindingRejectsOpenAIChannelOnXAIPool verifies xai pool + non-xai channel = error.
func TestCreateBindingRejectsOpenAIChannelOnXAIPool(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	svc := AccountPoolService{}
	pool := createAccountPoolServiceTestPoolWithPlatform(t, svc, model.AccountPoolPlatformXAI)
	channel := createAccountPoolServiceTestChannelWithType(t, constant.ChannelTypeOpenAI, common.ChannelStatusManuallyDisabled)

	_, err := svc.CreateBinding(AccountPoolBindingCreateParams{
		PoolID:    pool.Id,
		ChannelID: channel.Id,
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "not compatible")
}

// TestCreateBindingRejectsXAIChannelOnOpenAIPool verifies openai pool + xai channel = error.
func TestCreateBindingRejectsXAIChannelOnOpenAIPool(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	svc := AccountPoolService{}
	pool := createAccountPoolServiceTestPoolWithPlatform(t, svc, model.AccountPoolPlatformOpenAI)
	channel := createAccountPoolServiceTestChannelWithType(t, constant.ChannelTypeXai, common.ChannelStatusManuallyDisabled)

	_, err := svc.CreateBinding(AccountPoolBindingCreateParams{
		PoolID:    pool.Id,
		ChannelID: channel.Id,
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "not compatible")
}

// TestPoolBoundChannelTypeChangeRejected verifies that the generic channel editor cannot
// change the provider type of a pool-bound channel (which would bypass pool-platform
// compatibility validation), while a non-type edit of the same channel still succeeds.
func TestPoolBoundChannelTypeChangeRejected(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	svc := AccountPoolService{}
	pool := createAccountPoolServiceTestPoolWithPlatform(t, svc, model.AccountPoolPlatformOpenAI)
	channel := createAccountPoolServiceTestChannelWithType(t, constant.ChannelTypeOpenAI, common.ChannelStatusManuallyDisabled)
	_, err := svc.CreateBinding(AccountPoolBindingCreateParams{PoolID: pool.Id, ChannelID: channel.Id})
	require.NoError(t, err)

	var stored model.Channel
	require.NoError(t, model.DB.First(&stored, channel.Id).Error)
	stored.Type = constant.ChannelTypeAnthropic
	err = stored.Update()
	require.Error(t, err)
	assert.ErrorIs(t, err, model.ErrAccountPoolBoundChannelTypeChange)

	// The type is unchanged in storage after the rejected edit.
	var afterReject model.Channel
	require.NoError(t, model.DB.First(&afterReject, channel.Id).Error)
	assert.Equal(t, constant.ChannelTypeOpenAI, afterReject.Type)

	// A non-type edit of the same bound channel still succeeds.
	afterReject.Name = "renamed-bound-channel"
	require.NoError(t, afterReject.Update())
}

// TestSaveGeneratedChannelRejectsBoundChannelTypeChange verifies the upstream-source sync
// path (a direct map update that bypasses model.Channel.Update/Save) still honors the
// pool-bound channel-type guard, so a re-sync cannot repoint a bound channel's provider type.
func TestSaveGeneratedChannelRejectsBoundChannelTypeChange(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	svc := AccountPoolService{}
	pool := createAccountPoolServiceTestPoolWithPlatform(t, svc, model.AccountPoolPlatformOpenAI)
	channel := createAccountPoolServiceTestChannelWithType(t, constant.ChannelTypeOpenAI, common.ChannelStatusManuallyDisabled)
	_, err := svc.CreateBinding(AccountPoolBindingCreateParams{PoolID: pool.Id, ChannelID: channel.Id})
	require.NoError(t, err)

	channel.Type = constant.ChannelTypeAnthropic
	_, err = saveGeneratedChannel(&channel, false, false, common.GetTimestamp())
	require.Error(t, err)
	assert.ErrorIs(t, err, model.ErrAccountPoolBoundChannelTypeChange)

	// A re-sync that preserves the bound channel's type still succeeds.
	channel.Type = constant.ChannelTypeOpenAI
	_, err = saveGeneratedChannel(&channel, false, false, common.GetTimestamp())
	require.NoError(t, err)
}

// TestUpdatePoolRejectsPlatformChangeWithIncompatibleBinding verifies that changing a
// pool's platform is rejected while a channel of the old platform is still bound. The
// create/bind paths validate platform↔channel compatibility, but the platform can only
// be revisited via UpdatePool — without this guard an openai pool with an openai channel
// could be switched to grok_web, leaving an invalid pair the relay can't route.
func TestUpdatePoolRejectsPlatformChangeWithIncompatibleBinding(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	svc := AccountPoolService{}
	pool := createAccountPoolServiceTestPoolWithPlatform(t, svc, model.AccountPoolPlatformOpenAI)
	channel := createAccountPoolServiceTestChannelWithType(t, constant.ChannelTypeOpenAI, common.ChannelStatusManuallyDisabled)
	_, err := svc.CreateBinding(AccountPoolBindingCreateParams{PoolID: pool.Id, ChannelID: channel.Id})
	require.NoError(t, err)

	_, err = svc.UpdatePool(pool.Id, AccountPoolCreateParams{
		Name:     pool.Name,
		Platform: model.AccountPoolPlatformGrokWeb,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot change account pool platform")

	// The platform must be unchanged in storage after the rejected update.
	var stored model.AccountPool
	require.NoError(t, model.DB.First(&stored, pool.Id).Error)
	assert.Equal(t, model.AccountPoolPlatformOpenAI, stored.Platform)

	// Re-saving the same platform (no change) still succeeds.
	_, err = svc.UpdatePool(pool.Id, AccountPoolCreateParams{
		Name:     pool.Name,
		Platform: model.AccountPoolPlatformOpenAI,
	})
	require.NoError(t, err)
}

// TestUpdatePoolAllowsPlatformChangeWithNoBindings verifies that an empty pool can still
// have its platform changed freely (the guard only blocks incompatible existing bindings).
func TestUpdatePoolAllowsPlatformChangeWithNoBindings(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	svc := AccountPoolService{}
	pool := createAccountPoolServiceTestPoolWithPlatform(t, svc, model.AccountPoolPlatformOpenAI)

	updated, err := svc.UpdatePool(pool.Id, AccountPoolCreateParams{
		Name:     pool.Name,
		Platform: model.AccountPoolPlatformGrokWeb,
	})
	require.NoError(t, err)
	assert.Equal(t, model.AccountPoolPlatformGrokWeb, updated.Platform)
}

// TestCreateBoundChannelDefaultsToXAITypeForXAIPool verifies that when no
// ChannelType is specified, CreateBoundChannel uses ChannelTypeXai for xai pools.
func TestCreateBoundChannelDefaultsToXAITypeForXAIPool(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	svc := AccountPoolService{}
	pool := createAccountPoolServiceTestPoolWithPlatform(t, svc, model.AccountPoolPlatformXAI)

	view, err := svc.CreateBoundChannel(AccountPoolBoundChannelCreateParams{
		PoolID: pool.Id,
		Name:   "xai-auto-channel",
	})

	require.NoError(t, err)
	var channel model.Channel
	require.NoError(t, model.DB.First(&channel, view.ChannelID).Error)
	assert.Equal(t, constant.ChannelTypeXai, channel.Type)
}
