package service

import (
	"context"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunDueChannelAutoPriorityDefaultsUnsetRateMultiplierToOne(t *testing.T) {
	setupUpstreamSourceAutoPriorityTestDB(t)
	now := int64(10_000_000)

	defaultRateChannel := createAutoPriorityTestChannel(t, "manual default rate", 100, dto.ChannelOtherSettings{
		ChannelAutoPriorityEnabled:         true,
		ChannelAutoPriorityIntervalMinutes: 0,
		ChannelAutoPriorityWindowHours:     24,
	})
	cheapChannel := createAutoPriorityTestChannel(t, "manual cheap", 100, dto.ChannelOtherSettings{
		ChannelAutoPriorityEnabled:         true,
		ChannelAutoPriorityIntervalMinutes: 0,
		ChannelAutoPriorityWindowHours:     24,
		ChannelAutoPriorityRateMultiplier:  0.5,
	})
	createAutoPriorityTestUsageLog(t, defaultRateChannel.Id, now-60)
	createAutoPriorityTestMonitorLog(t, defaultRateChannel.Id, now-60)
	createAutoPriorityTestUsageLog(t, cheapChannel.Id, now-60)
	createAutoPriorityTestMonitorLog(t, cheapChannel.Id, now-60)

	results := RunDueChannelAutoPriority(context.Background(), now)

	require.Len(t, results, 2)

	var reloadedDefaultRate model.Channel
	require.NoError(t, model.DB.First(&reloadedDefaultRate, defaultRateChannel.Id).Error)
	defaultSettings := reloadedDefaultRate.GetOtherSettings()
	require.NotNil(t, defaultSettings.ChannelAutoPriorityLastScore)
	assert.Equal(t, 1.0, defaultSettings.ChannelAutoPriorityLastScore.EffectiveRateMultiplier)
	assert.Equal(t, now, defaultSettings.ChannelAutoPriorityLastRunAt)

	var reloadedCheap model.Channel
	require.NoError(t, model.DB.First(&reloadedCheap, cheapChannel.Id).Error)
	cheapSettings := reloadedCheap.GetOtherSettings()
	require.NotNil(t, cheapSettings.ChannelAutoPriorityLastScore)
	assert.Equal(t, 0.5, cheapSettings.ChannelAutoPriorityLastScore.EffectiveRateMultiplier)

	assert.Greater(t, reloadedCheap.GetPriority(), reloadedDefaultRate.GetPriority())
}

func TestRunDueChannelAutoPriorityPreservesUnrelatedSettings(t *testing.T) {
	setupUpstreamSourceAutoPriorityTestDB(t)
	now := int64(10_050_000)

	channel := createAutoPriorityTestChannel(t, "manual preserve settings", 100, dto.ChannelOtherSettings{
		ChannelAutoPriorityEnabled:         true,
		ChannelAutoPriorityIntervalMinutes: 0,
		ChannelAutoPriorityWindowHours:     24,
		ChannelAutoPriorityRateMultiplier:  1,
	})
	settingsObject := make(map[string]any)
	require.NoError(t, common.UnmarshalJsonStr(channel.OtherSettings, &settingsObject))
	settingsObject["custom_unknown_setting"] = "keep-me"
	settingsBytes, err := common.Marshal(settingsObject)
	require.NoError(t, err)
	channel.OtherSettings = string(settingsBytes)
	require.NoError(t, model.DB.Model(&model.Channel{}).
		Where("id = ?", channel.Id).
		Update("settings", channel.OtherSettings).Error)

	createAutoPriorityTestUsageLog(t, channel.Id, now-60)
	createAutoPriorityTestMonitorLog(t, channel.Id, now-60)

	results := RunDueChannelAutoPriority(context.Background(), now)

	require.Len(t, results, 1)
	var reloaded model.Channel
	require.NoError(t, model.DB.First(&reloaded, channel.Id).Error)
	reloadedObject := make(map[string]any)
	require.NoError(t, common.UnmarshalJsonStr(reloaded.OtherSettings, &reloadedObject))
	assert.Equal(t, "keep-me", reloadedObject["custom_unknown_setting"])
	assert.Equal(t, float64(now), reloadedObject["channel_auto_priority_last_run_at"])
}

func TestRunDueChannelAutoPriorityKeepsCostCohortsWithinLocalGroup(t *testing.T) {
	setupUpstreamSourceAutoPriorityTestDB(t)
	now := int64(10_100_000)

	defaultGroupChannel := createAutoPriorityTestChannel(t, "default group", 100, dto.ChannelOtherSettings{
		ChannelAutoPriorityEnabled:         true,
		ChannelAutoPriorityIntervalMinutes: 0,
		ChannelAutoPriorityWindowHours:     24,
		ChannelAutoPriorityRateMultiplier:  1,
	})
	vipChannel := createAutoPriorityTestChannel(t, "vip group", 100, dto.ChannelOtherSettings{
		ChannelAutoPriorityEnabled:         true,
		ChannelAutoPriorityIntervalMinutes: 0,
		ChannelAutoPriorityWindowHours:     24,
		ChannelAutoPriorityRateMultiplier:  0.01,
	})
	require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", vipChannel.Id).Update("group", "vip").Error)
	require.NoError(t, model.DB.Model(&model.Ability{}).Where("channel_id = ?", vipChannel.Id).Update("group", "vip").Error)
	createAutoPriorityTestUsageLog(t, defaultGroupChannel.Id, now-60)
	createAutoPriorityTestMonitorLog(t, defaultGroupChannel.Id, now-60)
	createAutoPriorityTestUsageLog(t, vipChannel.Id, now-60)
	createAutoPriorityTestMonitorLog(t, vipChannel.Id, now-60)

	results := RunDueChannelAutoPriority(context.Background(), now)

	require.Len(t, results, 2)

	var reloadedDefault model.Channel
	require.NoError(t, model.DB.First(&reloadedDefault, defaultGroupChannel.Id).Error)
	defaultSettings := reloadedDefault.GetOtherSettings()
	require.NotNil(t, defaultSettings.ChannelAutoPriorityLastScore)
	assert.Equal(t, 100.0, defaultSettings.ChannelAutoPriorityLastScore.EffectivePriceScore)

	var reloadedVIP model.Channel
	require.NoError(t, model.DB.First(&reloadedVIP, vipChannel.Id).Error)
	vipSettings := reloadedVIP.GetOtherSettings()
	require.NotNil(t, vipSettings.ChannelAutoPriorityLastScore)
	assert.Equal(t, 100.0, vipSettings.ChannelAutoPriorityLastScore.EffectivePriceScore)
}

func TestRunDueChannelAutoPriorityUsesDedicatedAvailabilityWindow(t *testing.T) {
	setupUpstreamSourceAutoPriorityTestDB(t)
	now := int64(10_200_000)

	channel := createAutoPriorityTestChannel(t, "manual split windows", 100, dto.ChannelOtherSettings{
		ChannelAutoPriorityEnabled:                 true,
		ChannelAutoPriorityIntervalMinutes:         0,
		ChannelAutoPriorityWindowHours:             24,
		ChannelAutoPriorityAvailabilityWindowHours: 1,
		ChannelAutoPriorityRateMultiplier:          1,
	})
	createAutoPriorityTestUsageLog(t, channel.Id, now-2*3600)
	createAutoPriorityTestMonitorLog(t, channel.Id, now-2*3600)

	results := RunDueChannelAutoPriority(context.Background(), now)

	require.Len(t, results, 1)

	var reloaded model.Channel
	require.NoError(t, model.DB.First(&reloaded, channel.Id).Error)
	settings := reloaded.GetOtherSettings()
	require.NotNil(t, settings.ChannelAutoPriorityLastScore)
	assert.Equal(t, now-24*3600, settings.ChannelAutoPriorityLastScore.WindowStart)
	assert.Equal(t, int64(1), settings.ChannelAutoPriorityLastScore.UsageLogCount)
	assert.Equal(t, int64(0), settings.ChannelAutoPriorityLastScore.MonitorCheckCount)
	assert.Equal(t, int64(1), settings.ChannelAutoPriorityLastScore.FirstTokenSampleCount)
	assert.Equal(t, now, settings.ChannelAutoPriorityLastRunAt)
}

func TestRunDueChannelAutoPriorityUsesOneAvailabilityWindowForLocalGroup(t *testing.T) {
	setupUpstreamSourceAutoPriorityTestDB(t)
	now := int64(10_250_000)

	firstDue := createAutoPriorityTestChannel(t, "manual group first due", 100, dto.ChannelOtherSettings{
		ChannelAutoPriorityEnabled:                 true,
		ChannelAutoPriorityIntervalMinutes:         0,
		ChannelAutoPriorityWindowHours:             24,
		ChannelAutoPriorityAvailabilityWindowHours: 1,
		ChannelAutoPriorityRateMultiplier:          1,
	})
	secondDue := createAutoPriorityTestChannel(t, "manual group second due", 100, dto.ChannelOtherSettings{
		ChannelAutoPriorityEnabled:                 true,
		ChannelAutoPriorityIntervalMinutes:         0,
		ChannelAutoPriorityWindowHours:             24,
		ChannelAutoPriorityAvailabilityWindowHours: 1,
		ChannelAutoPriorityRateMultiplier:          1,
	})
	createAutoPriorityTestChannel(t, "manual group not due", 100, dto.ChannelOtherSettings{
		ChannelAutoPriorityEnabled:                 true,
		ChannelAutoPriorityIntervalMinutes:         30,
		ChannelAutoPriorityWindowHours:             24,
		ChannelAutoPriorityAvailabilityWindowHours: 24,
		ChannelAutoPriorityRateMultiplier:          1,
		ChannelAutoPriorityLastRunAt:               now - 60,
	})

	for _, channel := range []model.Channel{firstDue, secondDue} {
		createAutoPriorityTestUsageLog(t, channel.Id, now-60)
		createAutoPriorityTestMonitorLog(t, channel.Id, now-2*3600)
	}

	results := RunDueChannelAutoPriority(context.Background(), now)

	require.Len(t, results, 2)
	for _, channel := range []model.Channel{firstDue, secondDue} {
		var reloaded model.Channel
		require.NoError(t, model.DB.First(&reloaded, channel.Id).Error)
		settings := reloaded.GetOtherSettings()
		require.NotNil(t, settings.ChannelAutoPriorityLastScore)
		assert.Equal(t, int64(1), settings.ChannelAutoPriorityLastScore.MonitorCheckCount)
	}
}

func TestRunDueChannelAutoPriorityIncludesGeneratedMemberInGroupAvailabilityWindow(t *testing.T) {
	setupUpstreamSourceAutoPriorityTestDB(t)
	now := int64(10_275_000)

	manualChannel := createAutoPriorityTestChannel(t, "manual group member", 100, dto.ChannelOtherSettings{
		ChannelAutoPriorityEnabled:                 true,
		ChannelAutoPriorityIntervalMinutes:         0,
		ChannelAutoPriorityWindowHours:             24,
		ChannelAutoPriorityAvailabilityWindowHours: 1,
		ChannelAutoPriorityRateMultiplier:          1,
	})
	source := createSyncTestSource(t, map[string]any{
		"auto_priority_enabled":                   true,
		"auto_priority_interval_minutes":          0,
		"auto_priority_window_hours":              24,
		"auto_priority_availability_window_hours": 24,
	})
	generatedChannel, _ := createGeneratedAutoPriorityTestChannel(t, source.Id, 0.5, "OpenAI", 100)
	generatedSettings := generatedChannel.GetOtherSettings()
	generatedSettings.ChannelAutoPriorityAvailabilityWindowHours = 24
	generatedChannel.SetOtherSettings(generatedSettings)
	require.NoError(t, model.DB.Model(&model.Channel{}).
		Where("id = ?", generatedChannel.Id).
		Update("settings", generatedChannel.OtherSettings).Error)

	createAutoPriorityTestUsageLog(t, manualChannel.Id, now-60)
	createAutoPriorityTestMonitorLog(t, manualChannel.Id, now-2*3600)

	results := RunDueChannelAutoPriority(context.Background(), now)

	require.Len(t, results, 1)
	var reloaded model.Channel
	require.NoError(t, model.DB.First(&reloaded, manualChannel.Id).Error)
	settings := reloaded.GetOtherSettings()
	require.NotNil(t, settings.ChannelAutoPriorityLastScore)
	assert.Equal(t, int64(1), settings.ChannelAutoPriorityLastScore.MonitorCheckCount)
	assert.Equal(t, 24, settings.ChannelAutoPriorityAvailabilityWindowHours)
}

func TestRunDueChannelAutoPrioritySkipsGeneratedChannels(t *testing.T) {
	setupUpstreamSourceAutoPriorityTestDB(t)
	now := int64(10_300_000)

	source := createSyncTestSource(t, map[string]any{
		"auto_priority_enabled":          true,
		"auto_priority_interval_minutes": 0,
		"auto_priority_window_hours":     24,
	})
	generatedChannel, _ := createGeneratedAutoPriorityTestChannel(t, source.Id, 0.5, "OpenAI", 100)
	createAutoPriorityTestUsageLog(t, generatedChannel.Id, now-60)
	createAutoPriorityTestMonitorLog(t, generatedChannel.Id, now-60)

	results := RunDueChannelAutoPriority(context.Background(), now)

	assert.Empty(t, results)

	var reloaded model.Channel
	require.NoError(t, model.DB.First(&reloaded, generatedChannel.Id).Error)
	assert.Equal(t, int64(100), reloaded.GetPriority())
	assert.Equal(t, int64(0), reloaded.GetOtherSettings().ChannelAutoPriorityLastRunAt)
}

func TestRunDueChannelAutoPriorityUpdatesAbilityPriority(t *testing.T) {
	setupUpstreamSourceAutoPriorityTestDB(t)
	now := int64(10_400_000)

	channel := createAutoPriorityTestChannel(t, "manual ability update", 100, dto.ChannelOtherSettings{
		ChannelAutoPriorityEnabled:         true,
		ChannelAutoPriorityIntervalMinutes: 0,
		ChannelAutoPriorityWindowHours:     24,
		ChannelAutoPriorityRateMultiplier:  0.5,
	})
	createAutoPriorityTestUsageLog(t, channel.Id, now-60)
	createAutoPriorityTestMonitorLog(t, channel.Id, now-60)

	results := RunDueChannelAutoPriority(context.Background(), now)

	require.Len(t, results, 1)

	var reloadedAbility model.Ability
	require.NoError(t, model.DB.Where("channel_id = ?", channel.Id).First(&reloadedAbility).Error)
	require.NotNil(t, reloadedAbility.Priority)
	assert.Equal(t, int64(1000), *reloadedAbility.Priority)
}

func TestRunDueChannelAutoPriorityTreatsDisabledChannelsAsNotDue(t *testing.T) {
	setupUpstreamSourceAutoPriorityTestDB(t)
	now := int64(10_500_000)

	channel := createAutoPriorityTestChannel(t, "manual disabled", 100, dto.ChannelOtherSettings{
		ChannelAutoPriorityEnabled:         true,
		ChannelAutoPriorityIntervalMinutes: 0,
		ChannelAutoPriorityWindowHours:     24,
		ChannelAutoPriorityRateMultiplier:  1,
	})
	require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", channel.Id).Update("status", common.ChannelStatusManuallyDisabled).Error)

	results := RunDueChannelAutoPriority(context.Background(), now)

	assert.Empty(t, results)
}
