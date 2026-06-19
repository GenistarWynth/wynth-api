package model

import (
	"fmt"
	"math/rand"
	"slices"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
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
