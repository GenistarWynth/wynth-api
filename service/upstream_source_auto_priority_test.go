package service

import (
	"context"
	"errors"
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

func createGeneratedAutoPriorityTestChannel(t *testing.T, sourceID int, rate float64, groupName string, priority int64) (model.Channel, model.UpstreamSourceChannelMapping) {
	t.Helper()

	mapping := createSyncTestMapping(t, sourceID, groupName, groupName, &rate)
	channel := createAutoPriorityTestChannel(t, "source-a / "+groupName, priority, dto.ChannelOtherSettings{
		GeneratedByUpstreamSourceID:        sourceID,
		GeneratedByUpstreamMappingID:       mapping.Id,
		ChannelAutoPriorityEnabled:         true,
		ChannelAutoPriorityIntervalMinutes: 30,
		ChannelAutoPriorityWindowHours:     24,
	})
	require.NoError(t, model.DB.Model(&model.UpstreamSourceChannelMapping{}).Where("id = ?", mapping.Id).Update("local_channel_id", channel.Id).Error)
	mapping.LocalChannelID = channel.Id
	return channel, mapping
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
		// 50 tokens / 2s = 25 t/s, above the throughput fast anchor -> score 100, so a
		// fully-healthy generated channel still reaches the max final score.
		UseTime: 2,
		Other:   string(other),
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

func TestListDueUpstreamSourcesForAutoPriorityHonorsIntervalZero(t *testing.T) {
	setupUpstreamSourceAutoPriorityTestDB(t)
	source := createSyncTestSource(t, map[string]any{
		"auto_priority_enabled":          true,
		"auto_priority_interval_minutes": 0,
		"auto_priority_window_hours":     24,
	})
	_, mapping := createGeneratedAutoPriorityTestChannel(t, source.Id, 0.01, "OpenAI", 1000)
	require.NoError(t, model.DB.Model(&model.UpstreamSourceChannelMapping{}).Where("id = ?", mapping.Id).Update("last_synced_at", 1999).Error)

	due, err := ListDueUpstreamSourcesForAutoPriority(2000)

	require.NoError(t, err)
	require.Len(t, due, 1)
	assert.Equal(t, source.Id, due[0].Id)
}

func TestListDueUpstreamSourcesForAutoPrioritySkipsNotDue(t *testing.T) {
	setupUpstreamSourceAutoPriorityTestDB(t)
	source := createSyncTestSource(t, map[string]any{
		"auto_priority_enabled":          true,
		"auto_priority_interval_minutes": 30,
		"auto_priority_window_hours":     24,
	})
	channel, _ := createGeneratedAutoPriorityTestChannel(t, source.Id, 0.01, "OpenAI", 1000)
	settings := channel.GetOtherSettings()
	settings.ChannelAutoPriorityLastRunAt = 1900
	channel.SetOtherSettings(settings)
	require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", channel.Id).Update("settings", channel.OtherSettings).Error)

	due, err := ListDueUpstreamSourcesForAutoPriority(2000)

	require.NoError(t, err)
	assert.Empty(t, due)
}

func TestListDueUpstreamSourcesForAutoPrioritySkipsMetadataMismatch(t *testing.T) {
	setupUpstreamSourceAutoPriorityTestDB(t)
	source := createSyncTestSource(t, map[string]any{
		"auto_priority_enabled":          true,
		"auto_priority_interval_minutes": 30,
		"auto_priority_window_hours":     24,
	})
	channel, mapping := createGeneratedAutoPriorityTestChannel(t, source.Id, 0.01, "OpenAI", 1000)
	settings := channel.GetOtherSettings()
	settings.GeneratedByUpstreamSourceID = source.Id + 1
	settings.GeneratedByUpstreamMappingID = mapping.Id
	channel.SetOtherSettings(settings)
	require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", channel.Id).Update("settings", channel.OtherSettings).Error)

	due, err := ListDueUpstreamSourcesForAutoPriority(2000)

	require.NoError(t, err)
	assert.Empty(t, due)
}

func TestListDueUpstreamSourcesForAutoPrioritySkipsMissingLocalChannel(t *testing.T) {
	setupUpstreamSourceAutoPriorityTestDB(t)
	source := createSyncTestSource(t, map[string]any{
		"auto_priority_enabled":          true,
		"auto_priority_interval_minutes": 0,
		"auto_priority_window_hours":     24,
	})
	rate := 0.01
	mapping := createSyncTestMapping(t, source.Id, "OpenAI", "OpenAI", &rate)
	require.NoError(t, model.DB.Model(&model.UpstreamSourceChannelMapping{}).Where("id = ?", mapping.Id).Update("local_channel_id", 999999).Error)

	due, err := ListDueUpstreamSourcesForAutoPriority(2000)

	require.NoError(t, err)
	assert.Empty(t, due)
}

func TestListDueUpstreamSourcesForAutoPriorityDoesNotMutateInvalidChannelSettings(t *testing.T) {
	setupUpstreamSourceAutoPriorityTestDB(t)
	source := createSyncTestSource(t, map[string]any{
		"auto_priority_enabled":          true,
		"auto_priority_interval_minutes": 0,
		"auto_priority_window_hours":     24,
	})
	rate := 0.01
	mapping := createSyncTestMapping(t, source.Id, "OpenAI", "OpenAI", &rate)
	channel := createAutoPriorityTestChannel(t, "source-a / invalid settings", 1000, dto.ChannelOtherSettings{
		GeneratedByUpstreamSourceID:  source.Id,
		GeneratedByUpstreamMappingID: mapping.Id,
	})
	require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", channel.Id).Update("settings", "{invalid-json").Error)
	require.NoError(t, model.DB.Model(&model.UpstreamSourceChannelMapping{}).Where("id = ?", mapping.Id).Update("local_channel_id", channel.Id).Error)

	due, err := ListDueUpstreamSourcesForAutoPriority(2000)

	require.NoError(t, err)
	assert.Empty(t, due)
	var reloaded model.Channel
	require.NoError(t, model.DB.First(&reloaded, channel.Id).Error)
	assert.Equal(t, "{invalid-json", reloaded.OtherSettings)
}

func TestListDueUpstreamSourcesForAutoPrioritySkipsMappingLoadFailure(t *testing.T) {
	failingSource := model.UpstreamSource{
		Id:         1,
		Status:     model.UpstreamSourceStatusEnabled,
		SyncConfig: `{"auto_priority_enabled":true,"auto_priority_interval_minutes":0,"auto_priority_window_hours":24}`,
	}
	dueSource := model.UpstreamSource{
		Id:         2,
		Status:     model.UpstreamSourceStatusEnabled,
		SyncConfig: `{"auto_priority_enabled":true,"auto_priority_interval_minutes":0,"auto_priority_window_hours":24}`,
	}
	mapping := model.UpstreamSourceChannelMapping{
		Id:                22,
		SourceID:          dueSource.Id,
		SyncEnabled:       true,
		LocalChannelID:    33,
		UpstreamGroupID:   "OpenAI",
		UpstreamGroupName: "OpenAI",
		UpstreamPlatform:  "openai",
		DiscoveryStatus:   model.UpstreamMappingDiscoveryStatusActive,
	}
	channel := model.Channel{Id: mapping.LocalChannelID, OtherSettings: "{}"}
	channel.SetOtherSettings(dto.ChannelOtherSettings{
		GeneratedByUpstreamSourceID:  dueSource.Id,
		GeneratedByUpstreamMappingID: mapping.Id,
	})

	due := listDueUpstreamSourcesForAutoPriorityFromSources(
		[]model.UpstreamSource{failingSource, dueSource},
		2000,
		func(_ context.Context, source model.UpstreamSource) ([]model.UpstreamSourceChannelMapping, error) {
			if source.Id == failingSource.Id {
				return nil, errors.New("mapping table locked")
			}
			return []model.UpstreamSourceChannelMapping{mapping}, nil
		},
		func(context.Context, []model.UpstreamSourceChannelMapping) (map[int]model.Channel, error) {
			return map[int]model.Channel{channel.Id: channel}, nil
		},
	)

	require.Len(t, due, 1)
	assert.Equal(t, dueSource.Id, due[0].Id)
}

func TestListDueUpstreamSourcesForAutoPriorityBatchLoadsChannels(t *testing.T) {
	source := model.UpstreamSource{
		Id:         1,
		Status:     model.UpstreamSourceStatusEnabled,
		SyncConfig: `{"auto_priority_enabled":true,"auto_priority_interval_minutes":0,"auto_priority_window_hours":24}`,
	}
	mappings := []model.UpstreamSourceChannelMapping{
		{
			Id:                11,
			SourceID:          source.Id,
			SyncEnabled:       true,
			LocalChannelID:    101,
			UpstreamGroupID:   "OpenAI",
			UpstreamGroupName: "OpenAI",
			UpstreamPlatform:  "openai",
			DiscoveryStatus:   model.UpstreamMappingDiscoveryStatusActive,
		},
		{
			Id:                12,
			SourceID:          source.Id,
			SyncEnabled:       true,
			LocalChannelID:    102,
			UpstreamGroupID:   "Claude",
			UpstreamGroupName: "Claude",
			UpstreamPlatform:  "openai",
			DiscoveryStatus:   model.UpstreamMappingDiscoveryStatusActive,
		},
	}
	channels := make(map[int]model.Channel)
	for _, mapping := range mappings {
		channel := model.Channel{Id: mapping.LocalChannelID, OtherSettings: "{}"}
		channel.SetOtherSettings(dto.ChannelOtherSettings{
			GeneratedByUpstreamSourceID:  source.Id,
			GeneratedByUpstreamMappingID: mapping.Id,
		})
		channels[channel.Id] = channel
	}
	loadCalls := 0

	due := listDueUpstreamSourcesForAutoPriorityFromSources(
		[]model.UpstreamSource{source},
		2000,
		func(context.Context, model.UpstreamSource) ([]model.UpstreamSourceChannelMapping, error) {
			return mappings, nil
		},
		func(_ context.Context, loadedMappings []model.UpstreamSourceChannelMapping) (map[int]model.Channel, error) {
			loadCalls++
			assert.ElementsMatch(t, []int{101, 102}, []int{loadedMappings[0].LocalChannelID, loadedMappings[1].LocalChannelID})
			return channels, nil
		},
	)

	require.Len(t, due, 1)
	assert.Equal(t, source.Id, due[0].Id)
	assert.Equal(t, 1, loadCalls)
}

func TestRunDueUpstreamSourceAutoPriorityOnlyProcessesDueMappings(t *testing.T) {
	setupUpstreamSourceAutoPriorityTestDB(t)
	now := int64(8_000_000)
	source := createSyncTestSource(t, map[string]any{
		"auto_priority_enabled":          true,
		"auto_priority_interval_minutes": 30,
		"auto_priority_window_hours":     24,
	})
	dueChannel, dueMapping := createGeneratedAutoPriorityTestChannel(t, source.Id, 0.5, "due", 100)
	notDueChannel, _ := createGeneratedAutoPriorityTestChannel(t, source.Id, 0.5, "not-due", 200)
	dueSettings := dueChannel.GetOtherSettings()
	dueSettings.ChannelAutoPriorityLastRunAt = now - 3600
	dueChannel.SetOtherSettings(dueSettings)
	require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", dueChannel.Id).Update("settings", dueChannel.OtherSettings).Error)
	notDueLastRunAt := now - 60
	notDueSettings := notDueChannel.GetOtherSettings()
	notDueSettings.ChannelAutoPriorityLastRunAt = notDueLastRunAt
	notDueChannel.SetOtherSettings(notDueSettings)
	require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", notDueChannel.Id).Update("settings", notDueChannel.OtherSettings).Error)
	createAutoPriorityTestUsageLog(t, dueChannel.Id, now-60)
	createAutoPriorityTestMonitorLog(t, dueChannel.Id, now-60)
	createAutoPriorityTestUsageLog(t, notDueChannel.Id, now-60)
	createAutoPriorityTestMonitorLog(t, notDueChannel.Id, now-60)

	results := (&UpstreamSourceService{}).RunDueUpstreamSourceAutoPriority(context.Background(), now)

	require.Len(t, results, 1)
	assert.Equal(t, source.Id, results[0].SourceID)
	require.Len(t, results[0].Results, 1)
	assert.Equal(t, dueMapping.Id, results[0].Results[0].MappingID)

	var reloadedDue, reloadedNotDue model.Channel
	require.NoError(t, model.DB.First(&reloadedDue, dueChannel.Id).Error)
	require.NoError(t, model.DB.First(&reloadedNotDue, notDueChannel.Id).Error)
	assert.Equal(t, now, reloadedDue.GetOtherSettings().ChannelAutoPriorityLastRunAt)
	assert.Equal(t, notDueLastRunAt, reloadedNotDue.GetOtherSettings().ChannelAutoPriorityLastRunAt)
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
	// Availability defaults to its own 24-hour window, independently of the
	// rule's one-hour usage/cost window.
	assert.Equal(t, int64(1), shortSettings.ChannelAutoPriorityLastScore.MonitorCheckCount)
	assert.Equal(t, int64(1), longSettings.ChannelAutoPriorityLastScore.FirstTokenSampleCount)
	assert.Equal(t, int64(0), shortSettings.ChannelAutoPriorityLastScore.FirstTokenSampleCount)
}

func TestRunUpstreamSourceAutoPriorityUsesGroupAvailabilityWindowIncludingManualPeer(t *testing.T) {
	setupUpstreamSourceAutoPriorityTestDB(t)
	now := int64(5_100_000)
	source := createSyncTestSource(t, map[string]any{
		"auto_priority_enabled":          true,
		"auto_priority_interval_minutes": 15,
		"auto_priority_window_hours":     24,
		"local_group_rules": []map[string]any{
			{
				"name":          "Split window",
				"local_group":   "default",
				"name_contains": []string{"split"},
				"auto_priority": map[string]any{
					"enabled":                   true,
					"window_hours":              24,
					"availability_window_hours": 1,
				},
			},
		},
	})
	rate := 0.5
	mapping := createSyncTestMapping(t, source.Id, "15", "split upstream", &rate)
	channel := createAutoPriorityTestChannel(t, "source-a / split", 100, dto.ChannelOtherSettings{
		GeneratedByUpstreamSourceID:                source.Id,
		GeneratedByUpstreamMappingID:               mapping.Id,
		ChannelAutoPriorityEnabled:                 true,
		ChannelAutoPriorityIntervalMinutes:         15,
		ChannelAutoPriorityWindowHours:             24,
		ChannelAutoPriorityAvailabilityWindowHours: 1,
	})
	require.NoError(t, model.DB.Model(&model.UpstreamSourceChannelMapping{}).Where("id = ?", mapping.Id).Update("local_channel_id", channel.Id).Error)
	createAutoPriorityTestChannel(t, "manual group peer", 100, dto.ChannelOtherSettings{
		ChannelAutoPriorityEnabled:                 true,
		ChannelAutoPriorityIntervalMinutes:         0,
		ChannelAutoPriorityWindowHours:             24,
		ChannelAutoPriorityAvailabilityWindowHours: 24,
		ChannelAutoPriorityRateMultiplier:          1,
	})

	createAutoPriorityTestUsageLog(t, channel.Id, now-2*3600)
	createAutoPriorityTestMonitorLog(t, channel.Id, now-2*3600)

	service := UpstreamSourceService{Now: func() int64 { return now }}

	result, err := service.RunAutoPriority(context.Background(), source.Id, now)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, result.Results, 1)

	var reloaded model.Channel
	require.NoError(t, model.DB.First(&reloaded, channel.Id).Error)
	settings := reloaded.GetOtherSettings()
	require.NotNil(t, settings.ChannelAutoPriorityLastScore)
	assert.Equal(t, now-24*3600, settings.ChannelAutoPriorityLastScore.WindowStart)
	assert.Equal(t, int64(1), settings.ChannelAutoPriorityLastScore.UsageLogCount)
	assert.Equal(t, int64(1), settings.ChannelAutoPriorityLastScore.MonitorCheckCount)
	assert.Equal(t, int64(1), settings.ChannelAutoPriorityLastScore.FirstTokenSampleCount)
	assert.Equal(t, 24, settings.ChannelAutoPriorityAvailabilityWindowHours)
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
	assert.Equal(t, int64(991), r.ComputedPriority)
	assert.Equal(t, int64(991), r.NewPriority)
	assert.Equal(t, 0.5, r.EffectiveRateMultiplier)
	assert.Equal(t, 0.5, r.NominalRateMultiplier)
	assert.InDelta(t, 0.4100416667, r.CacheAdjustedCostFactor, 0.0000001)
	assert.InDelta(t, r.EffectiveRateMultiplier*r.CacheAdjustedCostFactor, r.EffectiveCostMultiplier, 0.0000001)
	assert.Equal(t, 100.0, r.EffectivePriceScore)
	assert.Equal(t, 100.0, r.NominalPriceScore)
	assert.InDelta(t, 90.7628205, r.CacheScore, 0.0000001)
	assert.Equal(t, 100.0, r.AvailabilityScore)
	assert.Equal(t, 100.0, r.FirstTokenScore)
	assert.Equal(t, 100.0, r.ThroughputScore)
	assert.InDelta(t, 99.0762821, r.FinalScore, 0.0000001)

	var reloadedChannel model.Channel
	require.NoError(t, model.DB.First(&reloadedChannel, channel.Id).Error)
	assert.Equal(t, int64(991), reloadedChannel.GetPriority())
	reloadedSettings := reloadedChannel.GetOtherSettings()
	assert.True(t, reloadedSettings.ChannelAutoPriorityEnabled)
	assert.Equal(t, upstreamSourceAutoPriorityDefaultIntervalMinutes, reloadedSettings.ChannelAutoPriorityIntervalMinutes)
	assert.Equal(t, 24, reloadedSettings.ChannelAutoPriorityWindowHours)
	assert.Equal(t, now, reloadedSettings.ChannelAutoPriorityLastRunAt)
	assert.Equal(t, now, reloadedSettings.ChannelAutoPriorityLastAppliedAt)
	require.NotNil(t, reloadedSettings.ChannelAutoPriorityLastScore)
	assert.Equal(t, "v3", reloadedSettings.ChannelAutoPriorityLastScore.Version)
	assert.Equal(t, now, reloadedSettings.ChannelAutoPriorityLastScore.ComputedAt)
	assert.Equal(t, now-24*3600, reloadedSettings.ChannelAutoPriorityLastScore.WindowStart)
	assert.Equal(t, now, reloadedSettings.ChannelAutoPriorityLastScore.WindowEnd)
	assert.Equal(t, int64(991), reloadedSettings.ChannelAutoPriorityLastScore.NewPriority)
	assert.True(t, reloadedSettings.ChannelAutoPriorityLastScore.Applied)
	assert.Equal(t, int64(1), reloadedSettings.ChannelAutoPriorityLastScore.UsageLogCount)
	assert.Equal(t, int64(1), reloadedSettings.ChannelAutoPriorityLastScore.MonitorCheckCount)
	assert.Equal(t, int64(1), reloadedSettings.ChannelAutoPriorityLastScore.FirstTokenSampleCount)

	var reloadedAbility model.Ability
	require.NoError(t, model.DB.Where("channel_id = ?", channel.Id).First(&reloadedAbility).Error)
	require.NotNil(t, reloadedAbility.Priority)
	assert.Equal(t, int64(991), *reloadedAbility.Priority)
}

func TestRunUpstreamSourceAutoPrioritySmoothsWithPreviousCostSnapshot(t *testing.T) {
	setupUpstreamSourceAutoPriorityTestDB(t)
	now := int64(1_500_000)
	source := createSyncTestSource(t, map[string]any{
		"auto_priority_enabled":          true,
		"auto_priority_interval_minutes": 15,
		"auto_priority_window_hours":     24,
	})
	rate := 1.0
	mapping := createSyncTestMapping(t, source.Id, "15", "openai", &rate)
	channel := createAutoPriorityTestChannel(t, "source-a / smoothed", 100, dto.ChannelOtherSettings{
		GeneratedByUpstreamSourceID:        source.Id,
		GeneratedByUpstreamMappingID:       mapping.Id,
		ChannelAutoPriorityEnabled:         true,
		ChannelAutoPriorityIntervalMinutes: 15,
		ChannelAutoPriorityWindowHours:     24,
		ChannelAutoPriorityLastScore: &dto.ChannelAutoPriorityScore{
			Version:                 "v1",
			ComputedAt:              now - 1000,
			WindowStart:             now - 24*3600 - 1000,
			WindowEnd:               now - 1000,
			EffectiveCostMultiplier: 3.0,
			NewPriority:             100,
			Applied:                 true,
		},
	})
	require.NoError(t, model.DB.Model(&model.UpstreamSourceChannelMapping{}).Where("id = ?", mapping.Id).Update("local_channel_id", channel.Id).Error)
	for i := 0; i < 20; i++ {
		createAutoPriorityTestUsageLog(t, channel.Id, now-60-int64(i))
	}
	createAutoPriorityTestMonitorLog(t, channel.Id, now-60)

	service := UpstreamSourceService{Now: func() int64 { return now }}

	result, err := service.RunAutoPriority(context.Background(), source.Id, now)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, result.Results, 1)
	r := result.Results[0]
	assert.InDelta(t, 1.6566666667, r.EffectiveCostMultiplier, 0.0001)
	assert.InDelta(t, 0.9333333333, r.CacheAdjustedCostFactor, 0.0001)

	var reloadedChannel model.Channel
	require.NoError(t, model.DB.First(&reloadedChannel, channel.Id).Error)
	reloadedSettings := reloadedChannel.GetOtherSettings()
	require.NotNil(t, reloadedSettings.ChannelAutoPriorityLastScore)
	assert.InDelta(t, 1.6566666667, reloadedSettings.ChannelAutoPriorityLastScore.EffectiveCostMultiplier, 0.0001)
	assert.InDelta(t, 0.9333333333, reloadedSettings.ChannelAutoPriorityLastScore.CacheAdjustedCostFactor, 0.0001)
}

func TestRunUpstreamSourceAutoPriorityRateChangeKeepsFixedCacheDefaultIndependent(t *testing.T) {
	setupUpstreamSourceAutoPriorityTestDB(t)
	now := int64(1_750_000)
	source := createSyncTestSource(t, map[string]any{
		"auto_priority_enabled":          true,
		"auto_priority_interval_minutes": 15,
		"auto_priority_window_hours":     24,
	})
	rate := 2.0
	mapping := createSyncTestMapping(t, source.Id, "17", "openai", &rate)
	channel := createAutoPriorityTestChannel(t, "source-a / rate changed", 100, dto.ChannelOtherSettings{
		GeneratedByUpstreamSourceID:        source.Id,
		GeneratedByUpstreamMappingID:       mapping.Id,
		ChannelAutoPriorityEnabled:         true,
		ChannelAutoPriorityIntervalMinutes: 15,
		ChannelAutoPriorityWindowHours:     24,
		ChannelAutoPriorityLastScore: &dto.ChannelAutoPriorityScore{
			Version:                 "v2",
			ComputedAt:              now - 1000,
			WindowStart:             now - 24*3600 - 1000,
			WindowEnd:               now - 1000,
			EffectiveRateMultiplier: 1,
			CacheAdjustedCostFactor: 1,
			EffectiveCostMultiplier: 1,
			NewPriority:             100,
			Applied:                 true,
		},
	})
	require.NoError(t, model.DB.Model(&model.UpstreamSourceChannelMapping{}).Where("id = ?", mapping.Id).Update("local_channel_id", channel.Id).Error)

	service := UpstreamSourceService{Now: func() int64 { return now }}
	result, err := service.RunAutoPriority(context.Background(), source.Id, now)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, result.Results, 1)
	score := result.Results[0]
	assert.Equal(t, 2.0, score.NominalRateMultiplier)
	assert.InDelta(t, 0.3825, score.CacheAdjustedCostFactor, 1e-12)
	assert.Equal(t, 95.0, score.CacheScore)
	assert.InDelta(t, 1.19725, score.EffectiveCostMultiplier, 1e-12)
	assert.Equal(t, 100.0, score.NominalPriceScore)
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
	channel := createAutoPriorityTestChannel(t, "source-a / openai-2", 985, dto.ChannelOtherSettings{
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
			NewPriority: 985,
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
	assert.Equal(t, int64(985), r.OldPriority)
	assert.Equal(t, int64(991), r.ComputedPriority)
	assert.Equal(t, int64(985), r.NewPriority)

	var reloadedChannel model.Channel
	require.NoError(t, model.DB.First(&reloadedChannel, channel.Id).Error)
	assert.Equal(t, int64(985), reloadedChannel.GetPriority())
	reloadedSettings := reloadedChannel.GetOtherSettings()
	assert.Equal(t, now, reloadedSettings.ChannelAutoPriorityLastRunAt)
	assert.Equal(t, now-1000, reloadedSettings.ChannelAutoPriorityLastAppliedAt)
	require.NotNil(t, reloadedSettings.ChannelAutoPriorityLastScore)
	assert.Equal(t, int64(985), reloadedSettings.ChannelAutoPriorityLastScore.NewPriority)
	assert.False(t, reloadedSettings.ChannelAutoPriorityLastScore.Applied)
	assert.Equal(t, "hysteresis_delta_below_threshold", reloadedSettings.ChannelAutoPriorityLastScore.Reason)

	var reloadedAbility model.Ability
	require.NoError(t, model.DB.Where("channel_id = ?", channel.Id).First(&reloadedAbility).Error)
	require.NotNil(t, reloadedAbility.Priority)
	assert.Equal(t, int64(985), *reloadedAbility.Priority)
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

func TestPersistAutoPriorityCandidateRejectsChangedChannel(t *testing.T) {
	setupUpstreamSourceAutoPriorityTestDB(t)
	now := int64(7_000_000)
	source := createSyncTestSource(t, map[string]any{
		"auto_priority_enabled":          true,
		"auto_priority_interval_minutes": 15,
		"auto_priority_window_hours":     24,
	})
	rate := 0.5
	mapping := createSyncTestMapping(t, source.Id, "10", "long upstream", &rate)
	channel := createAutoPriorityTestChannel(t, "source-a / long", 100, dto.ChannelOtherSettings{
		GeneratedByUpstreamSourceID:        source.Id,
		GeneratedByUpstreamMappingID:       mapping.Id,
		ChannelAutoPriorityEnabled:         true,
		ChannelAutoPriorityIntervalMinutes: 15,
		ChannelAutoPriorityWindowHours:     24,
	})
	candidate := upstreamSourceAutoPriorityCandidate{
		mapping:  mapping,
		channel:  channel,
		settings: channel.GetOtherSettings(),
		resolution: upstreamSourceRuleResolution{
			AutoPriorityEnabled:         true,
			AutoPriorityIntervalMinutes: 15,
			AutoPriorityWindowHours:     24,
		},
		windowStart: now - 24*3600,
		windowEnd:   now,
	}
	require.NoError(t, model.DB.Model(&model.UpstreamSourceChannelMapping{}).Where("id = ?", mapping.Id).Update("local_channel_id", channel.Id).Error)

	channel.SetOtherSettings(dto.ChannelOtherSettings{
		GeneratedByUpstreamSourceID:  source.Id,
		GeneratedByUpstreamMappingID: mapping.Id,
		ChannelMonitorEnabled:        true,
	})
	require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", channel.Id).Update("settings", channel.OtherSettings).Error)

	reason, err := persistAutoPriorityCandidate(context.Background(), candidate, AutoPriorityScoreResult{
		ChannelID:        channel.Id,
		OldPriority:      100,
		ComputedPriority: 200,
		NewPriority:      200,
		Applied:          true,
	}, now)

	require.NoError(t, err)
	assert.Equal(t, "generated_channel_changed", reason)

	var reloadedChannel model.Channel
	require.NoError(t, model.DB.First(&reloadedChannel, channel.Id).Error)
	assert.Equal(t, int64(100), reloadedChannel.GetPriority())
	assert.Equal(t, channel.OtherSettings, reloadedChannel.OtherSettings)

	var reloadedAbility model.Ability
	require.NoError(t, model.DB.Where("channel_id = ?", channel.Id).First(&reloadedAbility).Error)
	require.NotNil(t, reloadedAbility.Priority)
	assert.Equal(t, int64(100), *reloadedAbility.Priority)
}

func TestPersistAutoPriorityCandidateRejectsManuallyDisabledChannel(t *testing.T) {
	setupUpstreamSourceAutoPriorityTestDB(t)
	now := int64(7_100_000)
	channel := createAutoPriorityTestChannel(t, "manual disable race", 100, dto.ChannelOtherSettings{
		ChannelAutoPriorityEnabled:         true,
		ChannelAutoPriorityIntervalMinutes: 15,
		ChannelAutoPriorityWindowHours:     24,
	})
	candidate := upstreamSourceAutoPriorityCandidate{
		channel:  channel,
		settings: channel.GetOtherSettings(),
		resolution: upstreamSourceRuleResolution{
			AutoPriorityEnabled:         true,
			AutoPriorityIntervalMinutes: 15,
			AutoPriorityWindowHours:     24,
		},
		windowStart: now - 24*3600,
		windowEnd:   now,
	}
	require.NoError(t, model.DB.Model(&model.Channel{}).
		Where("id = ?", channel.Id).
		Update("status", common.ChannelStatusManuallyDisabled).Error)

	reason, err := persistAutoPriorityCandidate(context.Background(), candidate, AutoPriorityScoreResult{
		ChannelID:        channel.Id,
		OldPriority:      100,
		ComputedPriority: 200,
		NewPriority:      200,
		Applied:          true,
	}, now)

	require.NoError(t, err)
	assert.Equal(t, "generated_channel_changed", reason)
	var reloadedChannel model.Channel
	require.NoError(t, model.DB.First(&reloadedChannel, channel.Id).Error)
	assert.Equal(t, common.ChannelStatusManuallyDisabled, reloadedChannel.Status)
	assert.Equal(t, int64(100), reloadedChannel.GetPriority())
	assert.Equal(t, channel.OtherSettings, reloadedChannel.OtherSettings)
	var reloadedAbility model.Ability
	require.NoError(t, model.DB.Where("channel_id = ?", channel.Id).First(&reloadedAbility).Error)
	require.NotNil(t, reloadedAbility.Priority)
	assert.Equal(t, int64(100), *reloadedAbility.Priority)
}

func TestFillAutoPriorityScoreInputsForWindowMarksStatsFailure(t *testing.T) {
	setupUpstreamSourceAutoPriorityTestDB(t)
	candidate := upstreamSourceAutoPriorityCandidate{
		mapping: model.UpstreamSourceChannelMapping{Id: 11},
		channel: model.Channel{Id: 22, Priority: common.GetPointer(int64(30))},
		scoreInput: AutoPriorityScoreInput{
			ChannelID:               22,
			CurrentPriority:         30,
			EffectiveRateMultiplier: 0.5,
		},
		resultIndex: 0,
	}
	pending := []upstreamSourceAutoPriorityCandidate{candidate}
	resultSlots := make([]*dto.UpstreamSourceAutoPriorityChannelResult, 1)
	result := &dto.UpstreamSourceAutoPriorityResult{}
	scoreInputs := make([]AutoPriorityScoreInput, 1)

	err := fillAutoPriorityScoreInputsForWindow(
		context.Background(),
		pending,
		[]int{0},
		1234,
		scoreInputs,
		result,
		resultSlots,
		func(context.Context, []int, int64) (map[int]model.ChannelMonitorStats, error) {
			return nil, errors.New("monitor db down")
		},
		func(context.Context, []int, int64) (map[int]AutoPriorityUsageStats, error) {
			t.Fatal("usage collector should not be called after monitor failure")
			return nil, nil
		},
	)

	require.Error(t, err)
	assert.Equal(t, 1, result.Failed)
	require.Len(t, resultSlots, 1)
	require.NotNil(t, resultSlots[0])
	assert.Equal(t, "monitor_stats_failed", resultSlots[0].Reason)
	assert.Equal(t, int64(30), resultSlots[0].OldPriority)
	assert.Equal(t, int64(30), resultSlots[0].NewPriority)
	assert.Contains(t, result.Error, "monitor db down")
}

func TestFillAutoPriorityScoreInputsForWindowMarksUsageStatsFailure(t *testing.T) {
	setupUpstreamSourceAutoPriorityTestDB(t)
	candidate := upstreamSourceAutoPriorityCandidate{
		mapping: model.UpstreamSourceChannelMapping{Id: 12},
		channel: model.Channel{Id: 23, Priority: common.GetPointer(int64(40))},
		scoreInput: AutoPriorityScoreInput{
			ChannelID:               23,
			CurrentPriority:         40,
			EffectiveRateMultiplier: 0.5,
		},
		resultIndex: 0,
	}
	pending := []upstreamSourceAutoPriorityCandidate{candidate}
	resultSlots := make([]*dto.UpstreamSourceAutoPriorityChannelResult, 1)
	result := &dto.UpstreamSourceAutoPriorityResult{}
	scoreInputs := make([]AutoPriorityScoreInput, 1)

	err := fillAutoPriorityScoreInputsForWindow(
		context.Background(),
		pending,
		[]int{0},
		1234,
		scoreInputs,
		result,
		resultSlots,
		func(context.Context, []int, int64) (map[int]model.ChannelMonitorStats, error) {
			return map[int]model.ChannelMonitorStats{
				23: {
					ChannelID:     23,
					TotalChecks:   2,
					SuccessChecks: 1,
				},
			}, nil
		},
		func(context.Context, []int, int64) (map[int]AutoPriorityUsageStats, error) {
			return nil, errors.New("usage db down")
		},
	)

	require.Error(t, err)
	assert.Equal(t, 1, result.Failed)
	require.Len(t, resultSlots, 1)
	require.NotNil(t, resultSlots[0])
	assert.Equal(t, "usage_stats_failed", resultSlots[0].Reason)
	assert.Equal(t, int64(40), resultSlots[0].OldPriority)
	assert.Equal(t, int64(40), resultSlots[0].NewPriority)
	assert.Contains(t, result.Error, "usage db down")
}

func TestResolveAutoPriorityAbilityUpdateResultAllowsExistingAbilityOnZeroRows(t *testing.T) {
	err := resolveAutoPriorityAbilityUpdateResult(123, 0, func(channelID int) (bool, error) {
		assert.Equal(t, 123, channelID)
		return true, nil
	})
	require.NoError(t, err)
}

func TestResolveAutoPriorityAbilityUpdateResultFailsWhenAbilityMissing(t *testing.T) {
	err := resolveAutoPriorityAbilityUpdateResult(123, 0, func(channelID int) (bool, error) {
		assert.Equal(t, 123, channelID)
		return false, nil
	})
	require.Error(t, err)
	assert.Equal(t, "ability priority update affected no rows", err.Error())
}

func TestAutoPriorityLocalGroupCostBoundsAggregatesAcrossSources(t *testing.T) {
	setupUpstreamSourceAutoPriorityTestDB(t)

	sourceA := createSyncTestSource(t, nil)
	sourceB := createSyncTestSource(t, nil)
	_, _ = createGeneratedAutoPriorityTestChannel(t, sourceA.Id, 0.05, "cheap", 100)
	_, _ = createGeneratedAutoPriorityTestChannel(t, sourceB.Id, 0.20, "expensive", 100)

	bounds, err := autoPriorityLocalGroupCostBounds(context.Background(), []string{"default"}, []int{constant.ChannelTypeOpenAI})

	require.NoError(t, err)
	cohort := autoPriorityCohortKey("default", constant.ChannelTypeOpenAI)
	require.Contains(t, bounds, cohort)
	assert.InDelta(t, 0.05, bounds[cohort][0], 0.0001)
	assert.InDelta(t, 0.20, bounds[cohort][1], 0.0001)
}

func TestAutoPriorityLocalGroupCostBoundsIncludesManualAndGeneratedChannels(t *testing.T) {
	setupUpstreamSourceAutoPriorityTestDB(t)

	createAutoPriorityTestChannel(t, "manual cheap", 100, dto.ChannelOtherSettings{
		ChannelAutoPriorityEnabled:        true,
		ChannelAutoPriorityRateMultiplier: 0.001,
	})
	source := createSyncTestSource(t, nil)
	_, _ = createGeneratedAutoPriorityTestChannel(t, source.Id, 0.05, "generated expensive", 100)

	bounds, err := autoPriorityLocalGroupCostBounds(context.Background(), []string{"default"}, []int{constant.ChannelTypeOpenAI})

	require.NoError(t, err)
	cohort := autoPriorityCohortKey("default", constant.ChannelTypeOpenAI)
	require.Contains(t, bounds, cohort)
	assert.InDelta(t, 0.001, bounds[cohort][0], 0.0001)
	assert.InDelta(t, 0.05, bounds[cohort][1], 0.0001)
}

func TestAutoPriorityLocalGroupCostBoundsReturnsEmptyForNoGroups(t *testing.T) {
	setupUpstreamSourceAutoPriorityTestDB(t)

	bounds, err := autoPriorityLocalGroupCostBounds(context.Background(), nil, []int{constant.ChannelTypeOpenAI})

	require.NoError(t, err)
	assert.Empty(t, bounds)
}

func TestRunUpstreamSourceAutoPriorityUsesCrossSourceCostBoundsWithinLocalGroup(t *testing.T) {
	setupUpstreamSourceAutoPriorityTestDB(t)
	now := int64(9_000_000)

	sourceA := createSyncTestSource(t, map[string]any{
		"auto_priority_enabled":          true,
		"auto_priority_interval_minutes": 15,
		"auto_priority_window_hours":     24,
	})
	sourceB := createSyncTestSource(t, map[string]any{
		"auto_priority_enabled":          true,
		"auto_priority_interval_minutes": 15,
		"auto_priority_window_hours":     24,
	})

	// Source A's generated channel is the pricier one in the shared "default"
	// local group (createAutoPriorityTestChannel always uses that local group).
	expensiveChannel, _ := createGeneratedAutoPriorityTestChannel(t, sourceA.Id, 0.20, "expensive", 100)
	// Source B's generated channel is cheaper but lands in the SAME local group
	// and channel type, so it should widen source A's price cohort.
	_, _ = createGeneratedAutoPriorityTestChannel(t, sourceB.Id, 0.05, "cheap", 100)

	service := UpstreamSourceService{Now: func() int64 { return now }}

	result, err := service.RunAutoPriority(context.Background(), sourceA.Id, now)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, result.Results, 1)
	r := result.Results[0]
	assert.Equal(t, expensiveChannel.Id, r.LocalChannelID)

	// Without the cross-source cost bounds fix, source A's run only ever sees
	// its own single channel, so EffectivePriceScore is forced to 100 regardless
	// of actual cost. With group-wide bounds, the inverse-cost score preserves the
	// real 4x gap: 0.05 / 0.20 * 100 = 25.
	assert.InDelta(t, 25, r.EffectivePriceScore, 0.0001)
}
