package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChannelAddAbilitiesSkipsEmptyModels(t *testing.T) {
	truncateTables(t)
	priority := int64(0)

	channel := &Channel{
		Id:       101,
		Type:     constant.ChannelTypeOpenAI,
		Status:   common.ChannelStatusManuallyDisabled,
		Name:     "empty-model-channel",
		Key:      "sk-test",
		Models:   "",
		Group:    "default",
		Priority: &priority,
	}
	require.NoError(t, DB.Create(channel).Error)

	require.NoError(t, channel.AddAbilities(nil))

	var count int64
	require.NoError(t, DB.Model(&Ability{}).Where("channel_id = ?", channel.Id).Count(&count).Error)
	assert.Equal(t, int64(0), count)
}

func TestChannelUpdateAbilitiesSkipsEmptyModels(t *testing.T) {
	truncateTables(t)
	priority := int64(0)

	channel := &Channel{
		Id:       102,
		Type:     constant.ChannelTypeOpenAI,
		Status:   common.ChannelStatusEnabled,
		Name:     "empty-model-update-channel",
		Key:      "sk-test",
		Models:   "gpt-4o",
		Group:    "default",
		Priority: &priority,
	}
	require.NoError(t, DB.Create(channel).Error)
	require.NoError(t, channel.AddAbilities(nil))

	channel.Models = ""
	require.NoError(t, channel.UpdateAbilities(nil))

	var count int64
	require.NoError(t, DB.Model(&Ability{}).Where("channel_id = ?", channel.Id).Count(&count).Error)
	assert.Equal(t, int64(0), count)
}

func TestChannelMemoryCacheNormalizesMessyAbilityTokens(t *testing.T) {
	clearStrictPriorityTables(t)
	withMemoryCacheForStrictPriority(t, true)
	priority := int64(0)

	channel := &Channel{
		Id:       103,
		Type:     constant.ChannelTypeOpenAI,
		Status:   common.ChannelStatusEnabled,
		Name:     "messy-cache-channel",
		Key:      "sk-test",
		Models:   ",gpt-4o, gpt-4o-mini,",
		Group:    ", default, premium ",
		Priority: &priority,
	}
	require.NoError(t, DB.Create(channel).Error)
	require.NoError(t, channel.AddAbilities(nil))

	require.NotPanics(t, InitChannelCache)

	cached, err := GetRandomSatisfiedChannel("default", "gpt-4o", 0, "")
	require.NoError(t, err)
	require.NotNil(t, cached)
	assert.Equal(t, channel.Id, cached.Id)
}
