package model

import (
	"fmt"
	"math/rand"
	"slices"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func clearStrictPriorityTables(t *testing.T) {
	t.Helper()
	require.NoError(t, DB.Exec("DELETE FROM abilities").Error)
	require.NoError(t, DB.Exec("DELETE FROM channels").Error)
	InitChannelCache()
}

func withMemoryCacheForStrictPriority(t *testing.T, enabled bool) {
	t.Helper()
	previous := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = enabled
	InitChannelCache()
	t.Cleanup(func() {
		common.MemoryCacheEnabled = previous
		InitChannelCache()
	})
}

func insertStrictPriorityCandidate(t *testing.T, id int, group, modelName string, priority int64, weight uint) {
	t.Helper()
	require.NoError(t, DB.Create(&Channel{
		Id:       id,
		Type:     constant.ChannelTypeOpenAI,
		Key:      fmt.Sprintf("key-%d", id),
		Status:   common.ChannelStatusEnabled,
		Name:     fmt.Sprintf("channel-%d", id),
		Group:    group,
		Models:   modelName,
		Priority: &priority,
		Weight:   &weight,
	}).Error)
	require.NoError(t, DB.Create(&Ability{
		Group:     group,
		Model:     modelName,
		ChannelId: id,
		Enabled:   true,
		Priority:  &priority,
		Weight:    weight,
	}).Error)
}

func insertStrictPriorityAdvancedCustomCandidate(t *testing.T, id int, group, modelName string, priority int64, weight uint, incomingPath string) {
	t.Helper()
	channel := &Channel{
		Id:       id,
		Type:     constant.ChannelTypeAdvancedCustom,
		Key:      fmt.Sprintf("key-%d", id),
		Status:   common.ChannelStatusEnabled,
		Name:     fmt.Sprintf("channel-%d", id),
		Group:    group,
		Models:   modelName,
		Priority: &priority,
		Weight:   &weight,
	}
	channel.SetOtherSettings(dto.ChannelOtherSettings{
		AdvancedCustom: &dto.AdvancedCustomConfig{
			Routes: []dto.AdvancedCustomRoute{
				{
					IncomingPath: incomingPath,
					UpstreamPath: "https://example.com/v1/chat/completions",
					Converter:    dto.AdvancedCustomConverterNone,
				},
			},
		},
	})
	require.NoError(t, DB.Create(channel).Error)
	require.NoError(t, DB.Create(&Ability{
		Group:     group,
		Model:     modelName,
		ChannelId: id,
		Enabled:   true,
		Priority:  &priority,
		Weight:    weight,
	}).Error)
}

func setupStrictPriorityCandidates(t *testing.T) {
	t.Helper()
	insertStrictPriorityCandidate(t, 1, "default", "gpt-strict", 100, 100)
	insertStrictPriorityCandidate(t, 2, "default", "gpt-strict", 100, 100)
	insertStrictPriorityCandidate(t, 3, "default", "gpt-strict", 50, 100)
	InitChannelCache()
}

func TestGetRandomSatisfiedChannelStrictPriorityKeepsHighestRemainingTier(t *testing.T) {
	clearStrictPriorityTables(t)
	withMemoryCacheForStrictPriority(t, true)
	setupStrictPriorityCandidates(t)

	channel, err := GetRandomSatisfiedChannel("default", "gpt-strict", 1, "", map[int]struct{}{1: {}})

	require.NoError(t, err)
	require.NotNil(t, channel)
	assert.Equal(t, 2, channel.Id)
}

func TestGetRandomSatisfiedChannelNormalizedFallbackFiltersAttemptedChannels(t *testing.T) {
	clearStrictPriorityTables(t)
	withMemoryCacheForStrictPriority(t, true)
	insertStrictPriorityCandidate(t, 1, "default", "gpt-4o-gizmo-*", 100, 100)
	insertStrictPriorityCandidate(t, 2, "default", "gpt-4o-gizmo-*", 100, 100)
	InitChannelCache()

	channel, err := GetRandomSatisfiedChannel("default", "gpt-4o-gizmo-test", 1, "", map[int]struct{}{1: {}})

	require.NoError(t, err)
	require.NotNil(t, channel)
	assert.Equal(t, 2, channel.Id)
}

func TestGetRandomSatisfiedChannelPathFilterCombinesWithAttemptedChannels(t *testing.T) {
	clearStrictPriorityTables(t)
	withMemoryCacheForStrictPriority(t, true)
	insertStrictPriorityAdvancedCustomCandidate(t, 1, "default", "gpt-strict", 100, 100, "/v1/chat/completions")
	insertStrictPriorityAdvancedCustomCandidate(t, 2, "default", "gpt-strict", 100, 100, "/v1/chat/completions")
	insertStrictPriorityAdvancedCustomCandidate(t, 3, "default", "gpt-strict", 100, 100, "/v1/responses")
	InitChannelCache()

	channel, err := GetRandomSatisfiedChannel("default", "gpt-strict", 1, "/v1/chat/completions", map[int]struct{}{1: {}})

	require.NoError(t, err)
	require.NotNil(t, channel)
	assert.Equal(t, 2, channel.Id)
}

func TestGetChannelDatabasePathFilterCombinesWithAttemptedChannels(t *testing.T) {
	clearStrictPriorityTables(t)
	withMemoryCacheForStrictPriority(t, false)
	insertStrictPriorityAdvancedCustomCandidate(t, 1, "default", "gpt-strict", 100, 100, "/v1/chat/completions")
	insertStrictPriorityAdvancedCustomCandidate(t, 2, "default", "gpt-strict", 100, 100, "/v1/chat/completions")
	insertStrictPriorityAdvancedCustomCandidate(t, 3, "default", "gpt-strict", 100, 100, "/v1/responses")

	channel, err := GetChannel("default", "gpt-strict", 1, "/v1/chat/completions", map[int]struct{}{1: {}})

	require.NoError(t, err)
	require.NotNil(t, channel)
	assert.Equal(t, 2, channel.Id)
}

func TestGetRandomSatisfiedChannelNoAttemptedSetIgnoresRetryTier(t *testing.T) {
	clearStrictPriorityTables(t)
	withMemoryCacheForStrictPriority(t, true)
	insertStrictPriorityCandidate(t, 1, "default", "gpt-test", 100, 100)
	insertStrictPriorityCandidate(t, 2, "default", "gpt-test", 50, 100)
	InitChannelCache()

	channel, err := GetRandomSatisfiedChannel("default", "gpt-test", 1, "")

	require.NoError(t, err)
	require.NotNil(t, channel)
	assert.Equal(t, 1, channel.Id)
}

func TestGetRandomSatisfiedChannelStrictPriorityFallsBackAfterTierExhaustion(t *testing.T) {
	clearStrictPriorityTables(t)
	withMemoryCacheForStrictPriority(t, true)
	setupStrictPriorityCandidates(t)

	channel, err := GetRandomSatisfiedChannel("default", "gpt-strict", 2, "", map[int]struct{}{1: {}, 2: {}})

	require.NoError(t, err)
	require.NotNil(t, channel)
	assert.Equal(t, 3, channel.Id)
}

func TestGetRandomSatisfiedChannelStrictPriorityReturnsNilWhenAllCandidatesAttempted(t *testing.T) {
	clearStrictPriorityTables(t)
	withMemoryCacheForStrictPriority(t, true)
	setupStrictPriorityCandidates(t)

	channel, err := GetRandomSatisfiedChannel("default", "gpt-strict", 3, "", map[int]struct{}{1: {}, 2: {}, 3: {}})

	require.NoError(t, err)
	assert.Nil(t, channel)
}

func TestGetRandomSatisfiedChannelStrictPriorityReturnsNilWhenSingleCandidateAttempted(t *testing.T) {
	clearStrictPriorityTables(t)
	withMemoryCacheForStrictPriority(t, true)
	insertStrictPriorityCandidate(t, 1, "default", "gpt-strict", 100, 100)
	InitChannelCache()

	channel, err := GetRandomSatisfiedChannel("default", "gpt-strict", 1, "", map[int]struct{}{1: {}})

	require.NoError(t, err)
	assert.Nil(t, channel)
}

func TestGetRandomSatisfiedChannelAttemptedFilteringDoesNotMutateCachedSlice(t *testing.T) {
	clearStrictPriorityTables(t)
	withMemoryCacheForStrictPriority(t, true)
	setupStrictPriorityCandidates(t)

	before := slices.Clone(group2model2channels["default"]["gpt-strict"])
	_, err := GetRandomSatisfiedChannel("default", "gpt-strict", 1, "", map[int]struct{}{1: {}})
	require.NoError(t, err)

	assert.Equal(t, before, group2model2channels["default"]["gpt-strict"])
}

func TestGetRandomSatisfiedChannelZeroWeightSamePriorityTierRemainsSelectable(t *testing.T) {
	clearStrictPriorityTables(t)
	withMemoryCacheForStrictPriority(t, true)
	insertStrictPriorityCandidate(t, 1, "default", "gpt-strict", 100, 0)
	insertStrictPriorityCandidate(t, 2, "default", "gpt-strict", 100, 0)
	InitChannelCache()
	rand.Seed(1)

	channel, err := GetRandomSatisfiedChannel("default", "gpt-strict", 0, "", map[int]struct{}{})

	require.NoError(t, err)
	require.NotNil(t, channel)
	assert.Contains(t, []int{1, 2}, channel.Id)
}

func TestGetChannelDatabasePathKeepsSamePriorityBeforeLowerTier(t *testing.T) {
	clearStrictPriorityTables(t)
	withMemoryCacheForStrictPriority(t, false)
	setupStrictPriorityCandidates(t)

	channel, err := GetChannel("default", "gpt-strict", 1, "", map[int]struct{}{1: {}})

	require.NoError(t, err)
	require.NotNil(t, channel)
	assert.Equal(t, 2, channel.Id)
}

func TestGetChannelDatabasePathReturnsNilWhenAllCandidatesAttempted(t *testing.T) {
	clearStrictPriorityTables(t)
	withMemoryCacheForStrictPriority(t, false)
	setupStrictPriorityCandidates(t)

	channel, err := GetChannel("default", "gpt-strict", 3, "", map[int]struct{}{1: {}, 2: {}, 3: {}})

	require.NoError(t, err)
	assert.Nil(t, channel)
}

func TestGetRandomSatisfiedChannelDatabasePathFiltersAttemptedChannels(t *testing.T) {
	clearStrictPriorityTables(t)
	withMemoryCacheForStrictPriority(t, false)
	setupStrictPriorityCandidates(t)
	rand.Seed(1)

	channel, err := GetRandomSatisfiedChannel("default", "gpt-strict", 1, "", map[int]struct{}{1: {}})

	require.NoError(t, err)
	require.NotNil(t, channel)
	assert.Equal(t, 2, channel.Id)
}

func TestCacheUpdateChannelStatusKeepsEnabledAccountPoolRuntimeChannelRoutable(t *testing.T) {
	clearStrictPriorityTables(t)
	withMemoryCacheForStrictPriority(t, true)
	require.NoError(t, DB.AutoMigrate(&AccountPool{}, &AccountPoolChannelBinding{}))
	require.NoError(t, DB.Exec("DELETE FROM account_pool_channel_bindings").Error)
	require.NoError(t, DB.Exec("DELETE FROM account_pools").Error)

	priority := int64(100)
	weight := uint(100)
	channel := Channel{
		Id:       42,
		Type:     constant.ChannelTypeOpenAI,
		Key:      "account-pool-cache-key",
		Status:   common.ChannelStatusManuallyDisabled,
		Name:     "account-pool-cache-channel",
		Group:    "default",
		Models:   "gpt-runtime",
		Priority: &priority,
		Weight:   &weight,
	}
	require.NoError(t, DB.Create(&channel).Error)
	require.NoError(t, DB.Create(&Ability{
		Group:     "default",
		Model:     "gpt-runtime",
		ChannelId: channel.Id,
		Enabled:   true,
		Priority:  &priority,
		Weight:    weight,
	}).Error)
	pool := AccountPool{Name: "runtime-pool", Platform: AccountPoolPlatformOpenAI}
	require.NoError(t, DB.Create(&pool).Error)
	require.NoError(t, DB.Create(&AccountPoolChannelBinding{
		PoolID:    pool.Id,
		ChannelID: channel.Id,
		Status:    AccountPoolBindingStatusEnabled,
	}).Error)
	InitChannelCache()

	selected, err := GetRandomSatisfiedChannel("default", "gpt-runtime", 0, "/v1/chat/completions")
	require.NoError(t, err)
	require.NotNil(t, selected)
	require.Equal(t, channel.Id, selected.Id)

	CacheUpdateChannelStatus(channel.Id, common.ChannelStatusAutoDisabled)
	selected, err = GetRandomSatisfiedChannel("default", "gpt-runtime", 0, "/v1/chat/completions")
	require.NoError(t, err)
	require.NotNil(t, selected)
	assert.Equal(t, channel.Id, selected.Id)

	oldDB := DB
	DB = nil
	t.Cleanup(func() {
		DB = oldDB
	})

	CacheUpdateChannelStatus(channel.Id, common.ChannelStatusAutoDisabled)
	selected, err = GetRandomSatisfiedChannel("default", "gpt-runtime", 0, "/v1/chat/completions")
	require.NoError(t, err)
	require.NotNil(t, selected)
	assert.Equal(t, channel.Id, selected.Id)
}
