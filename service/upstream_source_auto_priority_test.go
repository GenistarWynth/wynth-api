package service

import (
	"context"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupUpstreamSourceAutoPriorityTestDB(t *testing.T) {
	t.Helper()

	setupUpstreamSourceServiceTestDB(t)

	oldLOGDB := model.LOG_DB
	model.LOG_DB = model.DB
	t.Cleanup(func() {
		model.LOG_DB = oldLOGDB
	})

	require.NoError(t, model.DB.AutoMigrate(&model.ChannelMonitorLog{}, &model.Log{}))
}

func createAutoPriorityTestChannel(t *testing.T, name string, priority int64, settings dto.ChannelOtherSettings) model.Channel {
	t.Helper()

	channel := model.Channel{
		Type:          constant.ChannelTypeOpenAI,
		Key:           "sk-test",
		Status:        common.ChannelStatusEnabled,
		Name:          name,
		Weight:        common.GetPointer(uint(0)),
		Models:        "gpt-4o",
		Group:         "default",
		Priority:      common.GetPointer(priority),
		Other:         "{}",
		OtherInfo:     "{}",
		OtherSettings: "{}",
	}
	require.NoError(t, model.DB.Create(&channel).Error)

	channel.SetOtherSettings(settings)
	require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", channel.Id).Update("settings", channel.OtherSettings).Error)

	ability := model.Ability{
		Group:     channel.Group,
		Model:     "gpt-4o",
		ChannelId: channel.Id,
		Enabled:   true,
		Priority:  common.GetPointer(priority),
		Weight:    0,
	}
	require.NoError(t, model.DB.Create(&ability).Error)

	return channel
}

func createAutoPriorityTestUsageLog(t *testing.T, channelID int, createdAt int64) {
	t.Helper()

	other, err := common.Marshal(map[string]any{
		"input_tokens_total":       100,
		"cache_tokens":             20,
		"cache_ratio":              0.5,
		"completion_ratio":         1,
		"frt":                      80,
		"is_channel_test":          false,
		"cache_creation_tokens":    0,
		"cache_creation_ratio":     0,
		"cache_creation_tokens_5m": 0,
		"cache_creation_ratio_5m":  0,
		"cache_creation_tokens_1h": 0,
		"cache_creation_ratio_1h":  0,
		"cache_write_tokens":       0,
	})
	require.NoError(t, err)

	require.NoError(t, model.LOG_DB.Create(&model.Log{
		Type:             model.LogTypeConsume,
		CreatedAt:        createdAt,
		ChannelId:        channelID,
		PromptTokens:     100,
		CompletionTokens: 50,
		Other:            string(other),
	}).Error)
}

func createAutoPriorityTestMonitorLog(t *testing.T, channelID int, checkedAt int64) {
	t.Helper()

	require.NoError(t, model.DB.Create(&model.ChannelMonitorLog{
		ChannelID:           channelID,
		Status:              model.ChannelMonitorStatusSuccess,
		LatencyMS:           120,
		EndpointLatencyMS:   40,
		FirstTokenLatencyMS: 80,
		CheckedAt:           checkedAt,
		CreatedAt:           checkedAt,
	}).Error)
}

func TestRunUpstreamSourceAutoPriorityUsesPerCandidateWindows(t *testing.T) {
	setupUpstreamSourceAutoPriorityTestDB(t)
	now := int64(5_000_000)
	source := createSyncTestSource(t, map[string]any{
		"auto_priority_enabled":          true,
		"auto_priority_interval_minutes": 15,
		"auto_priority_window_hours":     24,
		"local_group_rules": []map[string]any{
			{
				"name":          "Long window",
				"local_group":   "long",
				"name_contains": []string{"long"},
				"auto_priority": map[string]any{
					"enabled":      true,
					"window_hours": 24,
				},
			},
			{
				"name":          "Short window",
				"local_group":   "short",
				"name_contains": []string{"short"},
				"auto_priority": map[string]any{
					"enabled":      true,
					"window_hours": 1,
				},
			},
		},
	})
	longRate := 0.5
	shortRate := 0.5
	longMapping := createSyncTestMapping(t, source.Id, "10", "long upstream", &longRate)
	shortMapping := createSyncTestMapping(t, source.Id, "20", "short upstream", &shortRate)
	longChannel := createAutoPriorityTestChannel(t, "source-a / long", 100, dto.ChannelOtherSettings{
		GeneratedByUpstreamSourceID:        source.Id,
		GeneratedByUpstreamMappingID:       longMapping.Id,
		ChannelAutoPriorityEnabled:         true,
		ChannelAutoPriorityIntervalMinutes: 15,
		ChannelAutoPriorityWindowHours:     24,
	})
	shortChannel := createAutoPriorityTestChannel(t, "source-a / short", 200, dto.ChannelOtherSettings{
		GeneratedByUpstreamSourceID:        source.Id,
		GeneratedByUpstreamMappingID:       shortMapping.Id,
		ChannelAutoPriorityEnabled:         true,
		ChannelAutoPriorityIntervalMinutes: 15,
		ChannelAutoPriorityWindowHours:     1,
	})
	require.NoError(t, model.DB.Model(&model.UpstreamSourceChannelMapping{}).Where("id = ?", longMapping.Id).Update("local_channel_id", longChannel.Id).Error)
	require.NoError(t, model.DB.Model(&model.UpstreamSourceChannelMapping{}).Where("id = ?", shortMapping.Id).Update("local_channel_id", shortChannel.Id).Error)

	// Shared record age: inside the 24h window, outside the 1h window.
	createAutoPriorityTestUsageLog(t, longChannel.Id, now-2*3600)
	createAutoPriorityTestMonitorLog(t, longChannel.Id, now-2*3600)
	createAutoPriorityTestUsageLog(t, shortChannel.Id, now-2*3600)
	createAutoPriorityTestMonitorLog(t, shortChannel.Id, now-2*3600)

	service := UpstreamSourceService{Now: func() int64 { return now }}

	result, err := service.RunAutoPriority(context.Background(), source.Id, now)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, result.Results, 2)

	var longResult, shortResult *dto.UpstreamSourceAutoPriorityChannelResult
	for i := range result.Results {
		switch result.Results[i].MappingID {
		case longMapping.Id:
			longResult = &result.Results[i]
		case shortMapping.Id:
			shortResult = &result.Results[i]
		}
	}
	require.NotNil(t, longResult)
	require.NotNil(t, shortResult)

	assert.Equal(t, now-24*3600, resultSnapshotWindowStart(t, source.Id, longChannel.Id))
	assert.Equal(t, now-3600, resultSnapshotWindowStart(t, source.Id, shortChannel.Id))

	var longChannelReloaded, shortChannelReloaded model.Channel
	require.NoError(t, model.DB.First(&longChannelReloaded, longChannel.Id).Error)
	require.NoError(t, model.DB.First(&shortChannelReloaded, shortChannel.Id).Error)
	longSettings := longChannelReloaded.GetOtherSettings()
	shortSettings := shortChannelReloaded.GetOtherSettings()
	require.NotNil(t, longSettings.ChannelAutoPriorityLastScore)
	require.NotNil(t, shortSettings.ChannelAutoPriorityLastScore)
	assert.Equal(t, now-24*3600, longSettings.ChannelAutoPriorityLastScore.WindowStart)
	assert.Equal(t, now-3600, shortSettings.ChannelAutoPriorityLastScore.WindowStart)
	assert.Equal(t, int64(1), longSettings.ChannelAutoPriorityLastScore.UsageLogCount)
	assert.Equal(t, int64(0), shortSettings.ChannelAutoPriorityLastScore.UsageLogCount)
	assert.Equal(t, int64(1), longSettings.ChannelAutoPriorityLastScore.MonitorCheckCount)
	assert.Equal(t, int64(0), shortSettings.ChannelAutoPriorityLastScore.MonitorCheckCount)
	assert.Equal(t, int64(1), longSettings.ChannelAutoPriorityLastScore.FirstTokenSampleCount)
	assert.Equal(t, int64(0), shortSettings.ChannelAutoPriorityLastScore.FirstTokenSampleCount)
}

func resultSnapshotWindowStart(t *testing.T, sourceID int, channelID int) int64 {
	t.Helper()

	var channel model.Channel
	require.NoError(t, model.DB.First(&channel, channelID).Error)
	settings := channel.GetOtherSettings()
	require.NotNil(t, settings.ChannelAutoPriorityLastScore)
	assert.Equal(t, sourceID, settings.GeneratedByUpstreamSourceID)
	return settings.ChannelAutoPriorityLastScore.WindowStart
}

func TestRunUpstreamSourceAutoPriorityAppliesGeneratedChannelPriority(t *testing.T) {
	setupUpstreamSourceAutoPriorityTestDB(t)
	now := int64(1_000_000)
	source := createSyncTestSource(t, map[string]any{
		"auto_priority_enabled":          true,
		"auto_priority_interval_minutes": 15,
		"auto_priority_window_hours":     24,
	})
	rate := 0.5
	mapping := createSyncTestMapping(t, source.Id, "10", "openai", &rate)
	channel := createAutoPriorityTestChannel(t, "source-a / openai", 100, dto.ChannelOtherSettings{
		GeneratedByUpstreamSourceID:        source.Id,
		GeneratedByUpstreamMappingID:       mapping.Id,
		ChannelAutoPriorityEnabled:         true,
		ChannelAutoPriorityIntervalMinutes: 15,
		ChannelAutoPriorityWindowHours:     24,
	})
	require.NoError(t, model.DB.Model(&model.UpstreamSourceChannelMapping{}).Where("id = ?", mapping.Id).Update("local_channel_id", channel.Id).Error)
	createAutoPriorityTestUsageLog(t, channel.Id, now-60)
	createAutoPriorityTestMonitorLog(t, channel.Id, now-60)

	service := UpstreamSourceService{Now: func() int64 { return now }}

	result, err := service.RunAutoPriority(context.Background(), source.Id, now)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, source.Id, result.SourceID)
	assert.Equal(t, 1, result.Updated)
	assert.Equal(t, 0, result.Skipped)
	assert.Equal(t, 0, result.Failed)
	require.Len(t, result.Results, 1)
	r := result.Results[0]
	assert.Equal(t, mapping.Id, r.MappingID)
	assert.Equal(t, channel.Id, r.LocalChannelID)
	assert.True(t, r.Applied)
	assert.Empty(t, r.Reason)
	assert.Equal(t, int64(100), r.OldPriority)
	assert.Equal(t, int64(1000), r.ComputedPriority)
	assert.Equal(t, int64(1000), r.NewPriority)
	assert.Equal(t, 0.5, r.EffectiveRateMultiplier)
	assert.Greater(t, r.CacheAdjustedCostFactor, 0.0)
	assert.Less(t, r.CacheAdjustedCostFactor, 1.0)
	assert.InDelta(t, r.EffectiveRateMultiplier*r.CacheAdjustedCostFactor, r.EffectiveCostMultiplier, 0.0000001)
	assert.Equal(t, 100.0, r.EffectivePriceScore)
	assert.Equal(t, 100.0, r.AvailabilityScore)
	assert.Equal(t, 100.0, r.FirstTokenScore)
	assert.Equal(t, 100.0, r.FinalScore)

	var reloadedChannel model.Channel
	require.NoError(t, model.DB.First(&reloadedChannel, channel.Id).Error)
	assert.Equal(t, int64(1000), reloadedChannel.GetPriority())
	reloadedSettings := reloadedChannel.GetOtherSettings()
	assert.True(t, reloadedSettings.ChannelAutoPriorityEnabled)
	assert.Equal(t, 15, reloadedSettings.ChannelAutoPriorityIntervalMinutes)
	assert.Equal(t, 24, reloadedSettings.ChannelAutoPriorityWindowHours)
	assert.Equal(t, now, reloadedSettings.ChannelAutoPriorityLastRunAt)
	assert.Equal(t, now, reloadedSettings.ChannelAutoPriorityLastAppliedAt)
	require.NotNil(t, reloadedSettings.ChannelAutoPriorityLastScore)
	assert.Equal(t, "v1", reloadedSettings.ChannelAutoPriorityLastScore.Version)
	assert.Equal(t, now, reloadedSettings.ChannelAutoPriorityLastScore.ComputedAt)
	assert.Equal(t, now-24*3600, reloadedSettings.ChannelAutoPriorityLastScore.WindowStart)
	assert.Equal(t, now, reloadedSettings.ChannelAutoPriorityLastScore.WindowEnd)
	assert.Equal(t, int64(1000), reloadedSettings.ChannelAutoPriorityLastScore.NewPriority)
	assert.True(t, reloadedSettings.ChannelAutoPriorityLastScore.Applied)
	assert.Equal(t, int64(1), reloadedSettings.ChannelAutoPriorityLastScore.UsageLogCount)
	assert.Equal(t, int64(1), reloadedSettings.ChannelAutoPriorityLastScore.MonitorCheckCount)
	assert.Equal(t, int64(1), reloadedSettings.ChannelAutoPriorityLastScore.FirstTokenSampleCount)

	var reloadedAbility model.Ability
	require.NoError(t, model.DB.Where("channel_id = ?", channel.Id).First(&reloadedAbility).Error)
	require.NotNil(t, reloadedAbility.Priority)
	assert.Equal(t, int64(1000), *reloadedAbility.Priority)
}

func TestRunUpstreamSourceAutoPriorityHysteresisUpdatesSnapshotOnly(t *testing.T) {
	setupUpstreamSourceAutoPriorityTestDB(t)
	now := int64(2_000_000)
	source := createSyncTestSource(t, map[string]any{
		"auto_priority_enabled":          true,
		"auto_priority_interval_minutes": 15,
		"auto_priority_window_hours":     24,
	})
	rate := 0.5
	mapping := createSyncTestMapping(t, source.Id, "20", "openai", &rate)
	channel := createAutoPriorityTestChannel(t, "source-a / openai-2", 995, dto.ChannelOtherSettings{
		GeneratedByUpstreamSourceID:        source.Id,
		GeneratedByUpstreamMappingID:       mapping.Id,
		ChannelAutoPriorityEnabled:         true,
		ChannelAutoPriorityIntervalMinutes: 15,
		ChannelAutoPriorityWindowHours:     24,
		ChannelAutoPriorityLastAppliedAt:   now - 1000,
		ChannelAutoPriorityLastScore: &dto.ChannelAutoPriorityScore{
			Version:     "v1",
			ComputedAt:  now - 1000,
			WindowStart: now - 24*3600 - 1000,
			WindowEnd:   now - 1000,
			NewPriority: 995,
			Applied:     true,
		},
	})
	require.NoError(t, model.DB.Model(&model.UpstreamSourceChannelMapping{}).Where("id = ?", mapping.Id).Update("local_channel_id", channel.Id).Error)
	createAutoPriorityTestUsageLog(t, channel.Id, now-60)
	createAutoPriorityTestMonitorLog(t, channel.Id, now-60)

	service := UpstreamSourceService{Now: func() int64 { return now }}

	result, err := service.RunAutoPriority(context.Background(), source.Id, now)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, 0, result.Updated)
	assert.Equal(t, 1, result.Skipped)
	assert.Equal(t, 0, result.Failed)
	require.Len(t, result.Results, 1)
	r := result.Results[0]
	assert.False(t, r.Applied)
	assert.Equal(t, "hysteresis_delta_below_threshold", r.Reason)
	assert.Equal(t, int64(995), r.OldPriority)
	assert.Equal(t, int64(1000), r.ComputedPriority)
	assert.Equal(t, int64(995), r.NewPriority)

	var reloadedChannel model.Channel
	require.NoError(t, model.DB.First(&reloadedChannel, channel.Id).Error)
	assert.Equal(t, int64(995), reloadedChannel.GetPriority())
	reloadedSettings := reloadedChannel.GetOtherSettings()
	assert.Equal(t, now, reloadedSettings.ChannelAutoPriorityLastRunAt)
	assert.Equal(t, now-1000, reloadedSettings.ChannelAutoPriorityLastAppliedAt)
	require.NotNil(t, reloadedSettings.ChannelAutoPriorityLastScore)
	assert.Equal(t, int64(995), reloadedSettings.ChannelAutoPriorityLastScore.NewPriority)
	assert.False(t, reloadedSettings.ChannelAutoPriorityLastScore.Applied)
	assert.Equal(t, "hysteresis_delta_below_threshold", reloadedSettings.ChannelAutoPriorityLastScore.Reason)

	var reloadedAbility model.Ability
	require.NoError(t, model.DB.Where("channel_id = ?", channel.Id).First(&reloadedAbility).Error)
	require.NotNil(t, reloadedAbility.Priority)
	assert.Equal(t, int64(995), *reloadedAbility.Priority)
}

func TestRunUpstreamSourceAutoPrioritySkipsMetadataMismatch(t *testing.T) {
	setupUpstreamSourceAutoPriorityTestDB(t)
	now := int64(3_000_000)
	source := createSyncTestSource(t, map[string]any{
		"auto_priority_enabled":          true,
		"auto_priority_interval_minutes": 15,
		"auto_priority_window_hours":     24,
	})
	rate := 0.5
	mapping := createSyncTestMapping(t, source.Id, "30", "openai", &rate)
	channel := createAutoPriorityTestChannel(t, "manual channel", 250, dto.ChannelOtherSettings{
		GeneratedByUpstreamSourceID:        source.Id + 1,
		GeneratedByUpstreamMappingID:       mapping.Id,
		ChannelAutoPriorityEnabled:         true,
		ChannelAutoPriorityIntervalMinutes: 15,
		ChannelAutoPriorityWindowHours:     24,
	})
	require.NoError(t, model.DB.Model(&model.UpstreamSourceChannelMapping{}).Where("id = ?", mapping.Id).Update("local_channel_id", channel.Id).Error)

	service := UpstreamSourceService{Now: func() int64 { return now }}

	result, err := service.RunAutoPriority(context.Background(), source.Id, now)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, 0, result.Updated)
	assert.Equal(t, 1, result.Skipped)
	assert.Equal(t, 0, result.Failed)
	require.Len(t, result.Results, 1)
	r := result.Results[0]
	assert.False(t, r.Applied)
	assert.Equal(t, "generated_channel_metadata_mismatch", r.Reason)
	assert.Equal(t, int64(250), r.OldPriority)
	assert.Equal(t, int64(250), r.NewPriority)

	var reloadedChannel model.Channel
	require.NoError(t, model.DB.First(&reloadedChannel, channel.Id).Error)
	assert.Equal(t, int64(250), reloadedChannel.GetPriority())
	reloadedSettings := reloadedChannel.GetOtherSettings()
	assert.Equal(t, int64(0), reloadedSettings.ChannelAutoPriorityLastRunAt)
	assert.Nil(t, reloadedSettings.ChannelAutoPriorityLastScore)
}

func TestRunUpstreamSourceAutoPrioritySkipsDisabledAndMissingRate(t *testing.T) {
	setupUpstreamSourceAutoPriorityTestDB(t)
	now := int64(4_000_000)
	source := createSyncTestSource(t, map[string]any{
		"auto_priority_enabled":          true,
		"auto_priority_interval_minutes": 15,
		"auto_priority_window_hours":     24,
		"local_group_rules": []map[string]any{
			{
				"name":          "Disabled rule",
				"local_group":   "disabled",
				"name_contains": []string{"disabled"},
				"auto_priority": map[string]any{
					"enabled": false,
				},
			},
			{
				"name":          "Good rule",
				"local_group":   "good",
				"name_contains": []string{"good"},
			},
		},
	})
	rate := 0.5
	createSyncTestMapping(t, source.Id, "40", "disabled group", &rate)
	missingRateMapping := createSyncTestMapping(t, source.Id, "50", "good group", nil)
	channel := createAutoPriorityTestChannel(t, "source-a / good group", 300, dto.ChannelOtherSettings{
		GeneratedByUpstreamSourceID:        source.Id,
		GeneratedByUpstreamMappingID:       missingRateMapping.Id,
		ChannelAutoPriorityEnabled:         true,
		ChannelAutoPriorityIntervalMinutes: 15,
		ChannelAutoPriorityWindowHours:     24,
	})
	require.NoError(t, model.DB.Model(&model.UpstreamSourceChannelMapping{}).Where("id = ?", missingRateMapping.Id).Update("local_channel_id", channel.Id).Error)

	service := UpstreamSourceService{Now: func() int64 { return now }}

	result, err := service.RunAutoPriority(context.Background(), source.Id, now)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, 0, result.Updated)
	assert.Equal(t, 2, result.Skipped)
	assert.Equal(t, 0, result.Failed)
	require.Len(t, result.Results, 1)
	r := result.Results[0]
	assert.Equal(t, missingRateMapping.Id, r.MappingID)
	assert.False(t, r.Applied)
	assert.Equal(t, "missing_effective_rate_multiplier", r.Reason)
	assert.Equal(t, int64(0), r.OldPriority)
	assert.Equal(t, int64(0), r.NewPriority)

	var reloadedChannel model.Channel
	require.NoError(t, model.DB.First(&reloadedChannel, channel.Id).Error)
	assert.Equal(t, int64(300), reloadedChannel.GetPriority())
}

func TestRunUpstreamSourceAutoPriorityPreservesResultOrder(t *testing.T) {
	setupUpstreamSourceAutoPriorityTestDB(t)
	now := int64(6_000_000)
	source := createSyncTestSource(t, map[string]any{
		"auto_priority_enabled":          true,
		"auto_priority_interval_minutes": 15,
		"auto_priority_window_hours":     24,
		"local_group_rules": []map[string]any{
			{
				"name":          "Long rule",
				"local_group":   "long",
				"name_contains": []string{"long"},
				"auto_priority": map[string]any{
					"enabled":      true,
					"window_hours": 24,
				},
			},
			{
				"name":          "Disabled rule",
				"local_group":   "disabled",
				"name_contains": []string{"disabled"},
				"auto_priority": map[string]any{
					"enabled": false,
				},
			},
			{
				"name":          "Missing rule",
				"local_group":   "missing",
				"name_contains": []string{"missing"},
				"auto_priority": map[string]any{
					"enabled":      true,
					"window_hours": 24,
				},
			},
			{
				"name":          "Short rule",
				"local_group":   "short",
				"name_contains": []string{"short"},
				"auto_priority": map[string]any{
					"enabled":      true,
					"window_hours": 24,
				},
			},
		},
	})
	rate := 0.5
	longMapping := createSyncTestMapping(t, source.Id, "10", "long upstream", &rate)
	_ = createSyncTestMapping(t, source.Id, "20", "disabled upstream", &rate)
	missingRateMapping := createSyncTestMapping(t, source.Id, "30", "missing upstream", nil)
	shortMapping := createSyncTestMapping(t, source.Id, "40", "short upstream", &rate)

	longChannel := createAutoPriorityTestChannel(t, "source-a / long", 100, dto.ChannelOtherSettings{
		GeneratedByUpstreamSourceID:        source.Id,
		GeneratedByUpstreamMappingID:       longMapping.Id,
		ChannelAutoPriorityEnabled:         true,
		ChannelAutoPriorityIntervalMinutes: 15,
		ChannelAutoPriorityWindowHours:     24,
	})
	shortChannel := createAutoPriorityTestChannel(t, "source-a / short", 200, dto.ChannelOtherSettings{
		GeneratedByUpstreamSourceID:        source.Id,
		GeneratedByUpstreamMappingID:       shortMapping.Id,
		ChannelAutoPriorityEnabled:         true,
		ChannelAutoPriorityIntervalMinutes: 15,
		ChannelAutoPriorityWindowHours:     24,
	})
	require.NoError(t, model.DB.Model(&model.UpstreamSourceChannelMapping{}).Where("id = ?", longMapping.Id).Update("local_channel_id", longChannel.Id).Error)
	require.NoError(t, model.DB.Model(&model.UpstreamSourceChannelMapping{}).Where("id = ?", shortMapping.Id).Update("local_channel_id", shortChannel.Id).Error)

	createAutoPriorityTestUsageLog(t, longChannel.Id, now-60)
	createAutoPriorityTestMonitorLog(t, longChannel.Id, now-60)
	createAutoPriorityTestUsageLog(t, shortChannel.Id, now-60)
	createAutoPriorityTestMonitorLog(t, shortChannel.Id, now-60)

	service := UpstreamSourceService{Now: func() int64 { return now }}

	result, err := service.RunAutoPriority(context.Background(), source.Id, now)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, 2, result.Updated)
	assert.Equal(t, 2, result.Skipped)
	assert.Equal(t, 0, result.Failed)
	require.Len(t, result.Results, 3)
	assert.Equal(t, longMapping.Id, result.Results[0].MappingID)
	assert.Equal(t, missingRateMapping.Id, result.Results[1].MappingID)
	assert.Equal(t, shortMapping.Id, result.Results[2].MappingID)
	assert.Equal(t, "missing_effective_rate_multiplier", result.Results[1].Reason)
}
