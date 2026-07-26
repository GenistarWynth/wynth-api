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
	if DB.Migrator().HasTable(&AccountPoolChannelBinding{}) {
		require.NoError(t, DB.Exec("DELETE FROM account_pool_channel_bindings").Error)
	}
	if DB.Migrator().HasTable(&AccountPool{}) {
		require.NoError(t, DB.Exec("DELETE FROM account_pools").Error)
	}
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

func TestGetRandomSatisfiedChannelCacheParityExcludesStaleDisabledAbilityAndNormalizesModel(t *testing.T) {
	for _, memoryCacheEnabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("memory_cache_%t", memoryCacheEnabled), func(t *testing.T) {
			clearStrictPriorityTables(t)
			withMemoryCacheForStrictPriority(t, memoryCacheEnabled)

			priority := int64(200)
			weight := uint(100)
			require.NoError(t, DB.Create(&Channel{
				Id:       1,
				Type:     constant.ChannelTypeOpenAI,
				Key:      "disabled-key",
				Status:   common.ChannelStatusManuallyDisabled,
				Name:     "manually-disabled",
				Group:    "default",
				Models:   "gpt-4o-gizmo-*",
				Priority: &priority,
				Weight:   &weight,
			}).Error)
			require.NoError(t, DB.Create(&Ability{
				Group:     "default",
				Model:     "gpt-4o-gizmo-*",
				ChannelId: 1,
				Enabled:   true,
				Priority:  &priority,
				Weight:    weight,
			}).Error)
			insertStrictPriorityCandidate(t, 2, "default", "gpt-4o-gizmo-*", 100, 25)
			insertStrictPriorityCandidate(t, 3, "other", "gpt-4o-gizmo-*", 1_000, 100)
			insertStrictPriorityCandidate(t, 4, "default", "different-model", 900, 100)
			InitChannelCache()

			channel, err := GetRandomSatisfiedChannel(
				"default",
				"gpt-4o-gizmo-review",
				0,
				"",
				map[int]struct{}{3: {}, 4: {}},
			)

			require.NoError(t, err)
			require.NotNil(t, channel)
			assert.Equal(t, 2, channel.Id)

			channel, err = GetRandomSatisfiedChannel(
				"default",
				"gpt-4o-gizmo-review",
				1,
				"",
				map[int]struct{}{2: {}},
			)
			require.NoError(t, err)
			assert.Nil(t, channel)
		})
	}
}

func TestGetRandomSatisfiedChannelCacheParityExhaustsUnequalWeightsWithoutReplacement(t *testing.T) {
	var sequences [][]int
	for _, memoryCacheEnabled := range []bool{false, true} {
		clearStrictPriorityTables(t)
		withMemoryCacheForStrictPriority(t, memoryCacheEnabled)
		insertStrictPriorityCandidate(t, 1, "default", "gpt-weighted", 100, 1)
		insertStrictPriorityCandidate(t, 2, "default", "gpt-weighted", 100, 99)
		insertStrictPriorityCandidate(t, 3, "default", "gpt-weighted", 50, 50)
		InitChannelCache()
		rand.Seed(7)

		attempted := make(map[int]struct{})
		sequence := make([]int, 0, 3)
		for range 3 {
			channel, err := GetRandomSatisfiedChannel("default", "gpt-weighted", 0, "", attempted)
			require.NoError(t, err)
			require.NotNil(t, channel)
			sequence = append(sequence, channel.Id)
			attempted[channel.Id] = struct{}{}
		}
		channel, err := GetRandomSatisfiedChannel("default", "gpt-weighted", 0, "", attempted)
		require.NoError(t, err)
		assert.Nil(t, channel)

		assert.ElementsMatch(t, []int{1, 2}, sequence[:2])
		assert.Equal(t, 3, sequence[2])
		sequences = append(sequences, sequence)
	}

	assert.Equal(t, sequences[0], sequences[1])
}

func TestSelectHighestPriorityWeightedChannelHandlesFullWidthWeights(t *testing.T) {
	priority := int64(100)
	maxWeight := ^uint(0)
	cases := []struct {
		name    string
		weights []uint
	}{
		{name: "two equal 1<<62 weights", weights: []uint{uint(uint64(1) << 62), uint(uint64(1) << 62)}},
		{name: "near maximum stored weights", weights: []uint{maxWeight, maxWeight - 1}},
		{name: "mixed maximum small and zero", weights: []uint{maxWeight, 1, 0}},
		{name: "all zero remains uniform", weights: []uint{0, 0, 0}},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			channels := make([]*Channel, 0, len(test.weights))
			for index, weight := range test.weights {
				weight := weight
				channels = append(channels, &Channel{
					Id:       index + 1,
					Priority: &priority,
					Weight:   &weight,
				})
			}

			for range 100 {
				selected, err := selectHighestPriorityWeightedChannel(channels)
				require.NoError(t, err)
				require.NotNil(t, selected)
				assert.Contains(t, channels, selected)
			}
		})
	}
}

func TestGetRandomSatisfiedChannelCacheParityExhaustsHugeWeightsWithoutReplacement(t *testing.T) {
	for _, memoryCacheEnabled := range []bool{false, true} {
		clearStrictPriorityTables(t)
		withMemoryCacheForStrictPriority(t, memoryCacheEnabled)
		insertStrictPriorityCandidate(t, 1, "default", "gpt-huge-weight", 100, uint(uint64(1)<<62))
		insertStrictPriorityCandidate(t, 2, "default", "gpt-huge-weight", 100, uint((uint64(1)<<62)-1))
		insertStrictPriorityCandidate(t, 3, "default", "gpt-huge-weight", 100, 0)
		insertStrictPriorityCandidate(t, 4, "default", "gpt-huge-weight", 50, 1)
		InitChannelCache()

		attempted := make(map[int]struct{})
		sequence := make([]int, 0, 4)
		for range 4 {
			channel, err := GetRandomSatisfiedChannel("default", "gpt-huge-weight", 0, "", attempted)
			require.NoError(t, err)
			require.NotNil(t, channel)
			sequence = append(sequence, channel.Id)
			attempted[channel.Id] = struct{}{}
		}
		channel, err := GetRandomSatisfiedChannel("default", "gpt-huge-weight", 0, "", attempted)
		require.NoError(t, err)
		assert.Nil(t, channel)

		assert.ElementsMatch(t, []int{1, 2}, sequence[:2])
		assert.Equal(t, 3, sequence[2])
		assert.Equal(t, 4, sequence[3])
	}
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

func TestGetRandomSatisfiedChannelCacheParityKeepsEnabledAccountPoolRuntimeChannelRoutable(t *testing.T) {
	for _, memoryCacheEnabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("memory_cache_%t", memoryCacheEnabled), func(t *testing.T) {
			clearStrictPriorityTables(t)
			withMemoryCacheForStrictPriority(t, memoryCacheEnabled)
			require.NoError(t, DB.AutoMigrate(&AccountPool{}, &AccountPoolChannelBinding{}))

			priority := int64(100)
			weight := uint(100)
			channel := Channel{
				Id:       43,
				Type:     constant.ChannelTypeOpenAI,
				Key:      "account-pool-parity-key",
				Status:   common.ChannelStatusManuallyDisabled,
				Name:     "account-pool-parity-channel",
				Group:    "default",
				Models:   "gpt-runtime-parity",
				Priority: &priority,
				Weight:   &weight,
			}
			require.NoError(t, DB.Create(&channel).Error)
			require.NoError(t, DB.Create(&Ability{
				Group:     "default",
				Model:     "gpt-runtime-parity",
				ChannelId: channel.Id,
				Enabled:   true,
				Priority:  &priority,
				Weight:    weight,
			}).Error)
			pool := AccountPool{Name: "runtime-parity-pool", Platform: AccountPoolPlatformOpenAI}
			require.NoError(t, DB.Create(&pool).Error)
			require.NoError(t, DB.Create(&AccountPoolChannelBinding{
				PoolID:    pool.Id,
				ChannelID: channel.Id,
				Status:    AccountPoolBindingStatusEnabled,
			}).Error)
			InitChannelCache()

			selected, err := GetRandomSatisfiedChannel("default", "gpt-runtime-parity", 0, "/v1/chat/completions")

			require.NoError(t, err)
			require.NotNil(t, selected)
			assert.Equal(t, channel.Id, selected.Id)
		})
	}
}

func TestGetRandomSatisfiedChannelCacheParityTracksAccountPoolStatusTransitions(t *testing.T) {
	for _, memoryCacheEnabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("memory_cache_%t", memoryCacheEnabled), func(t *testing.T) {
			clearStrictPriorityTables(t)
			withMemoryCacheForStrictPriority(t, memoryCacheEnabled)
			require.NoError(t, DB.AutoMigrate(&AccountPool{}, &AccountPoolChannelBinding{}))

			disabledPool := AccountPool{
				Name:     "disabled-runtime-pool",
				Platform: AccountPoolPlatformOpenAI,
				Status:   AccountPoolStatusDisabled,
			}
			enabledPool := AccountPool{
				Name:     "enabled-runtime-pool",
				Platform: AccountPoolPlatformOpenAI,
				Status:   AccountPoolStatusEnabled,
			}
			require.NoError(t, DB.Create(&disabledPool).Error)
			require.NoError(t, DB.Create(&enabledPool).Error)

			for _, candidate := range []struct {
				id       int
				priority int64
				poolID   int
			}{
				{id: 51, priority: 200, poolID: disabledPool.Id},
				{id: 52, priority: 100, poolID: enabledPool.Id},
			} {
				weight := uint(100)
				require.NoError(t, DB.Create(&Channel{
					Id:       candidate.id,
					Type:     constant.ChannelTypeOpenAI,
					Key:      fmt.Sprintf("pool-key-%d", candidate.id),
					Status:   common.ChannelStatusManuallyDisabled,
					Name:     fmt.Sprintf("pool-channel-%d", candidate.id),
					Group:    "default",
					Models:   "gpt-pool-transition",
					Priority: &candidate.priority,
					Weight:   &weight,
				}).Error)
				require.NoError(t, DB.Create(&Ability{
					Group:     "default",
					Model:     "gpt-pool-transition",
					ChannelId: candidate.id,
					Enabled:   true,
					Priority:  &candidate.priority,
					Weight:    weight,
				}).Error)
				require.NoError(t, DB.Create(&AccountPoolChannelBinding{
					PoolID:    candidate.poolID,
					ChannelID: candidate.id,
					Status:    AccountPoolBindingStatusEnabled,
				}).Error)
			}
			InitChannelCache()

			runtimeChannelIDs, err := EnabledAccountPoolRuntimeChannelIDs()
			require.NoError(t, err)
			assert.Equal(t, map[int]struct{}{52: {}}, runtimeChannelIDs)

			selected, err := GetRandomSatisfiedChannel("default", "gpt-pool-transition", 0, "/v1/chat/completions")
			require.NoError(t, err)
			require.NotNil(t, selected)
			assert.Equal(t, 52, selected.Id)

			require.NoError(t, DB.Model(&AccountPool{}).
				Where("id = ?", disabledPool.Id).
				Update("status", AccountPoolStatusEnabled).Error)
			require.NoError(t, DB.Model(&AccountPool{}).
				Where("id = ?", enabledPool.Id).
				Update("status", AccountPoolStatusDisabled).Error)
			if memoryCacheEnabled {
				InitChannelCache()
			}

			runtimeChannelIDs, err = EnabledAccountPoolRuntimeChannelIDs()
			require.NoError(t, err)
			assert.Equal(t, map[int]struct{}{51: {}}, runtimeChannelIDs)

			selected, err = GetRandomSatisfiedChannel("default", "gpt-pool-transition", 0, "/v1/chat/completions")
			require.NoError(t, err)
			require.NotNil(t, selected)
			assert.Equal(t, 51, selected.Id)
		})
	}
}
