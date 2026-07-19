package service

import (
	"context"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveRuleModelOptionsIntersectsMatchedGroupModels(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)

	source := createSyncTestSource(t, nil)
	rate := 0.5
	mappingA := createSyncTestMapping(t, source.Id, "g-1", "OpenAI Alpha", &rate)
	mappingB := createSyncTestMapping(t, source.Id, "g-2", "OpenAI Beta", &rate)

	channelA := createAutoPriorityTestChannel(t, "source-a / alpha", 100, dto.ChannelOtherSettings{
		GeneratedByUpstreamSourceID:  source.Id,
		GeneratedByUpstreamMappingID: mappingA.Id,
	})
	channelB := createAutoPriorityTestChannel(t, "source-a / beta", 100, dto.ChannelOtherSettings{
		GeneratedByUpstreamSourceID:  source.Id,
		GeneratedByUpstreamMappingID: mappingB.Id,
	})
	require.NoError(t, model.DB.Model(&model.UpstreamSourceChannelMapping{}).Where("id = ?", mappingA.Id).Update("local_channel_id", channelA.Id).Error)
	require.NoError(t, model.DB.Model(&model.UpstreamSourceChannelMapping{}).Where("id = ?", mappingB.Id).Update("local_channel_id", channelB.Id).Error)

	service := UpstreamSourceService{
		FetchModels: func(channel *model.Channel) ([]string, error) {
			switch {
			case strings.Contains(channel.Name, "alpha"):
				return []string{"gpt-4o", "gpt-5"}, nil
			case strings.Contains(channel.Name, "beta"):
				return []string{"gpt-5", "claude-3"}, nil
			default:
				return []string{"unexpected"}, nil
			}
		},
	}

	result, err := service.ResolveRuleModelOptions(context.Background(), source.Id, []dto.UpstreamSourceLocalGroupRule{
		{
			Name:                "OpenAI",
			LocalGroup:          "default",
			Platforms:           []string{"openai"},
			NameContains:        []string{"openai"},
			ModelStrategy:       upstreamSourceModelStrategyAllUpstream,
			FixedModels:         []string{},
			ExcludeKeywords:     []string{},
			DescriptionContains: []string{},
		},
	}, 0)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, []string{"gpt-5"}, result.Models)
	require.Len(t, result.MatchedMappings, 2)
	assert.Equal(t, mappingA.Id, result.MatchedMappings[0].MappingID)
	assert.Equal(t, mappingB.Id, result.MatchedMappings[1].MappingID)
	assert.Equal(t, "OpenAI Alpha", result.MatchedMappings[0].UpstreamGroupName)
	assert.Equal(t, "OpenAI Beta", result.MatchedMappings[1].UpstreamGroupName)
}

func TestResolveRuleModelOptionsReadsExistingMappingKeyWithoutMutation(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)

	source := createSyncTestSource(t, nil)
	rate := 0.5
	mapping := createSyncTestMapping(t, source.Id, "g-1", "OpenAI", &rate)
	require.NoError(t, model.DB.Model(&model.UpstreamSourceChannelMapping{}).
		Where("id = ?", mapping.Id).
		Update("upstream_key_id", "key-existing").Error)

	createCalls := make([]fakeUpstreamSourceCreateKeyCall, 0)
	updateCalls := make([]fakeUpstreamSourceUpdateKeyCall, 0)
	listCalls := make([]string, 0)
	adapter := fakeUpstreamSourceAdapter{
		listKeys: []UpstreamKey{{
			ID:      "key-existing",
			Key:     "sk-existing",
			GroupID: mapping.UpstreamGroupID,
		}},
		createCalls: &createCalls,
		updateCalls: &updateCalls,
		listCalls:   &listCalls,
	}
	service := UpstreamSourceService{
		AdapterFactory: func(string) (UpstreamSourceAdapter, error) {
			return adapter, nil
		},
		FetchModels: func(channel *model.Channel) ([]string, error) {
			assert.Equal(t, "sk-existing", channel.Key)
			return []string{"gpt-5"}, nil
		},
	}

	result, err := service.ResolveRuleModelOptions(context.Background(), source.Id, []dto.UpstreamSourceLocalGroupRule{
		{
			LocalGroup:      "default",
			ModelStrategy:   upstreamSourceModelStrategyAllUpstream,
			FixedModels:     []string{},
			Platforms:       []string{},
			NameContains:    []string{},
			ExcludeKeywords: []string{},
		},
	}, 0)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, []string{"gpt-5"}, result.Models)
	assert.Equal(t, []string{mapping.UpstreamGroupID}, listCalls)
	assert.Empty(t, updateCalls)
	assert.Empty(t, createCalls)
}

func TestResolveRuleModelOptionsDoesNotMutateKeyWhenReadOnlyLookupFails(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)

	source := createSyncTestSource(t, nil)
	rate := 0.5
	mapping := createSyncTestMapping(t, source.Id, "g-1", "OpenAI", &rate)
	require.NoError(t, model.DB.Model(&model.UpstreamSourceChannelMapping{}).
		Where("id = ?", mapping.Id).
		Update("upstream_key_id", "key-existing").Error)

	createCalls := make([]fakeUpstreamSourceCreateKeyCall, 0)
	updateCalls := make([]fakeUpstreamSourceUpdateKeyCall, 0)
	adapter := fakeUpstreamSourceAdapter{
		listErr:     assert.AnError,
		createCalls: &createCalls,
		updateCalls: &updateCalls,
	}
	service := UpstreamSourceService{
		AdapterFactory: func(string) (UpstreamSourceAdapter, error) {
			return adapter, nil
		},
		FetchModels: func(*model.Channel) ([]string, error) {
			t.Fatal("model fetch must not run after key lookup fails")
			return nil, nil
		},
	}

	_, err := service.ResolveRuleModelOptions(context.Background(), source.Id, []dto.UpstreamSourceLocalGroupRule{
		{
			LocalGroup:      "default",
			ModelStrategy:   upstreamSourceModelStrategyAllUpstream,
			FixedModels:     []string{},
			Platforms:       []string{},
			NameContains:    []string{},
			ExcludeKeywords: []string{},
		},
	}, 0)

	require.ErrorIs(t, err, assert.AnError)
	assert.Empty(t, updateCalls)
	assert.Empty(t, createCalls)
}

func TestResolveRuleModelOptionsUsesCurrentRuleChannelType(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)

	source := createSyncTestSource(t, nil)
	rate := 0.5
	mapping := createSyncTestMapping(t, source.Id, "g-1", "Gemini", &rate)
	channel := createAutoPriorityTestChannel(t, "source-a / gemini", 100, dto.ChannelOtherSettings{
		GeneratedByUpstreamSourceID:  source.Id,
		GeneratedByUpstreamMappingID: mapping.Id,
	})
	require.NoError(t, model.DB.Model(&model.UpstreamSourceChannelMapping{}).
		Where("id = ?", mapping.Id).
		Update("local_channel_id", channel.Id).Error)

	service := UpstreamSourceService{
		FetchModels: func(channel *model.Channel) ([]string, error) {
			assert.Equal(t, constant.ChannelTypeGemini, channel.Type)
			return []string{"gemini-2.5-pro"}, nil
		},
	}

	result, err := service.ResolveRuleModelOptions(context.Background(), source.Id, []dto.UpstreamSourceLocalGroupRule{
		{
			LocalGroup:      "default",
			ChannelType:     constant.ChannelTypeGemini,
			ModelStrategy:   upstreamSourceModelStrategyAllUpstream,
			FixedModels:     []string{},
			Platforms:       []string{},
			NameContains:    []string{},
			ExcludeKeywords: []string{},
		},
	}, 0)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, []string{"gemini-2.5-pro"}, result.Models)
}

func TestResolveRuleModelOptionsMatchesSubmittedRulesBeforeEligibilityIsPersisted(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)

	source := createSyncTestSource(t, nil)
	rate := 0.5
	mapping := createSyncTestMapping(t, source.Id, "g-1", "Newly Matched OpenAI", &rate)
	channel := createAutoPriorityTestChannel(t, "source-a / newly matched", 100, dto.ChannelOtherSettings{
		GeneratedByUpstreamSourceID:  source.Id,
		GeneratedByUpstreamMappingID: mapping.Id,
	})
	require.NoError(t, model.DB.Model(&model.UpstreamSourceChannelMapping{}).
		Where("id = ?", mapping.Id).
		Updates(map[string]any{
			"local_channel_id": channel.Id,
			"sync_enabled":     false,
		}).Error)

	service := UpstreamSourceService{
		FetchModels: func(*model.Channel) ([]string, error) {
			return []string{"gpt-5"}, nil
		},
	}

	result, err := service.ResolveRuleModelOptions(context.Background(), source.Id, []dto.UpstreamSourceLocalGroupRule{
		{
			LocalGroup:      "default",
			NameContains:    []string{"newly matched"},
			ModelStrategy:   upstreamSourceModelStrategyAllUpstream,
			FixedModels:     []string{},
			Platforms:       []string{},
			ExcludeKeywords: []string{},
		},
	}, 0)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, []string{"gpt-5"}, result.Models)
	require.Len(t, result.MatchedMappings, 1)
	assert.Equal(t, mapping.Id, result.MatchedMappings[0].MappingID)
}
