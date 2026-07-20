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

	require.Len(t, results, 3)
	for _, channel := range []model.Channel{firstDue, secondDue} {
		var reloaded model.Channel
		require.NoError(t, model.DB.First(&reloaded, channel.Id).Error)
		settings := reloaded.GetOtherSettings()
		require.NotNil(t, settings.ChannelAutoPriorityLastScore)
		assert.Equal(t, int64(1), settings.ChannelAutoPriorityLastScore.MonitorCheckCount)
	}
	var reloadedNotDue model.Channel
	require.NoError(t, model.DB.Where("name = ?", "manual group not due").First(&reloadedNotDue).Error)
	assert.Equal(t, now, reloadedNotDue.GetOtherSettings().ChannelAutoPriorityLastRunAt)
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

	require.Len(t, results, 2)
	var reloaded model.Channel
	require.NoError(t, model.DB.First(&reloaded, manualChannel.Id).Error)
	settings := reloaded.GetOtherSettings()
	require.NotNil(t, settings.ChannelAutoPriorityLastScore)
	assert.Equal(t, int64(1), settings.ChannelAutoPriorityLastScore.MonitorCheckCount)
	assert.Equal(t, 24, settings.ChannelAutoPriorityAvailabilityWindowHours)
	var reloadedGenerated model.Channel
	require.NoError(t, model.DB.First(&reloadedGenerated, generatedChannel.Id).Error)
	assert.Equal(t, now, reloadedGenerated.GetOtherSettings().ChannelAutoPriorityLastRunAt)
}

func TestRunDueChannelAutoPriorityProcessesGeneratedChannels(t *testing.T) {
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

	require.Len(t, results, 1)
	assert.Equal(t, generatedChannel.Id, results[0].ChannelID)

	var reloaded model.Channel
	require.NoError(t, model.DB.First(&reloaded, generatedChannel.Id).Error)
	assert.Equal(t, int64(1000), reloaded.GetPriority())
	assert.Equal(t, now, reloaded.GetOtherSettings().ChannelAutoPriorityLastRunAt)
}

func TestRunDueChannelAutoPriorityRejectsInvalidGeneratedRateForWholeGroup(t *testing.T) {
	setupUpstreamSourceAutoPriorityTestDB(t)
	now := int64(10_350_000)
	source := createSyncTestSource(t, map[string]any{
		"auto_priority_enabled": true,
	})
	generated, mapping := createGeneratedAutoPriorityTestChannel(t, source.Id, 0.5, "generated", 100)
	manual := createAutoPriorityTestChannel(t, "manual peer", 100, dto.ChannelOtherSettings{
		ChannelAutoPriorityEnabled:         true,
		ChannelAutoPriorityIntervalMinutes: 30,
		ChannelAutoPriorityWindowHours:     24,
		ChannelAutoPriorityRateMultiplier:  1,
	})
	require.NoError(t, model.DB.Model(&model.UpstreamSourceChannelMapping{}).
		Where("id = ?", mapping.Id).
		Update("effective_rate_multiplier", nil).Error)

	results := RunDueChannelAutoPriority(context.Background(), now)

	require.Len(t, results, 2)
	for _, result := range results {
		assert.False(t, result.Applied)
		assert.Equal(t, "missing_effective_rate_multiplier", result.Reason)
	}
	for _, channel := range []model.Channel{generated, manual} {
		var reloaded model.Channel
		require.NoError(t, model.DB.First(&reloaded, channel.Id).Error)
		assert.Equal(t, int64(100), reloaded.GetPriority())
		assert.Zero(t, reloaded.GetOtherSettings().ChannelAutoPriorityLastRunAt)
	}
}

func TestRunDueChannelAutoPriorityUsesCurrentUpstreamRuleSwitch(t *testing.T) {
	setupUpstreamSourceAutoPriorityTestDB(t)
	now := int64(10_375_000)
	source := createSyncTestSource(t, map[string]any{
		"auto_priority_enabled": true,
	})
	generated, _ := createGeneratedAutoPriorityTestChannel(t, source.Id, 0.5, "generated", 100)
	generatedSettings := generated.GetOtherSettings()
	generatedSettings.ChannelAutoPriorityEnabled = false
	generated.SetOtherSettings(generatedSettings)
	require.NoError(t, model.DB.Model(&model.Channel{}).
		Where("id = ?", generated.Id).
		Update("settings", generated.OtherSettings).Error)

	results := RunDueChannelAutoPriority(context.Background(), now)

	require.Len(t, results, 1)
	var reloaded model.Channel
	require.NoError(t, model.DB.First(&reloaded, generated.Id).Error)
	assert.True(t, reloaded.GetOtherSettings().ChannelAutoPriorityEnabled)
	assert.Equal(t, now, reloaded.GetOtherSettings().ChannelAutoPriorityLastRunAt)

	disabledConfig, err := common.Marshal(map[string]any{
		"auto_priority_enabled": false,
	})
	require.NoError(t, err)
	require.NoError(t, model.DB.Model(&model.UpstreamSource{}).
		Where("id = ?", source.Id).
		Update("sync_config", string(disabledConfig)).Error)

	results = RunDueChannelAutoPriority(context.Background(), now+3600)

	assert.Empty(t, results)
	require.NoError(t, model.DB.First(&reloaded, generated.Id).Error)
	assert.Equal(t, now, reloaded.GetOtherSettings().ChannelAutoPriorityLastRunAt)
}

func TestRunDueChannelAutoPriorityUsesRuleEnabledGeneratedMemberForGroupWindow(t *testing.T) {
	setupUpstreamSourceAutoPriorityTestDB(t)
	now := int64(10_385_000)
	source := createSyncTestSource(t, map[string]any{
		"auto_priority_enabled": true,
	})
	generated, _ := createGeneratedAutoPriorityTestChannel(t, source.Id, 0.5, "generated", 100)
	generatedSettings := generated.GetOtherSettings()
	generatedSettings.ChannelAutoPriorityEnabled = false
	generatedSettings.ChannelAutoPriorityAvailabilityWindowHours = 96
	generated.SetOtherSettings(generatedSettings)
	require.NoError(t, model.DB.Model(&model.Channel{}).
		Where("id = ?", generated.Id).
		Update("settings", generated.OtherSettings).Error)
	manual := createAutoPriorityTestChannel(t, "manual window peer", 100, dto.ChannelOtherSettings{
		ChannelAutoPriorityEnabled:                 true,
		ChannelAutoPriorityIntervalMinutes:         30,
		ChannelAutoPriorityWindowHours:             24,
		ChannelAutoPriorityAvailabilityWindowHours: 1,
		ChannelAutoPriorityRateMultiplier:          1,
	})

	results := RunDueChannelAutoPriority(context.Background(), now)

	require.Len(t, results, 2)
	for _, channel := range []model.Channel{generated, manual} {
		var reloaded model.Channel
		require.NoError(t, model.DB.First(&reloaded, channel.Id).Error)
		assert.Equal(t, 96, reloaded.GetOtherSettings().ChannelAutoPriorityAvailabilityWindowHours)
	}
}

func TestRunChannelAutoPriorityGroupsForSourceRejectsDisabledSource(t *testing.T) {
	setupUpstreamSourceAutoPriorityTestDB(t)
	source := createSyncTestSource(t, map[string]any{"auto_priority_enabled": true})
	require.NoError(t, model.DB.Model(&model.UpstreamSource{}).
		Where("id = ?", source.Id).
		Update("status", model.UpstreamSourceStatusDisabled).Error)

	_, err := RunChannelAutoPriorityGroupsForSource(context.Background(), source.Id, 10_390_000)

	require.Error(t, err)
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

func TestRunDueChannelAutoPriorityGroupEarliestLastRunIncludesManualAndGeneratedMembers(t *testing.T) {
	setupUpstreamSourceAutoPriorityTestDB(t)
	now := int64(10_600_000)

	recentManual := createAutoPriorityTestChannel(t, "manual recent", 100, dto.ChannelOtherSettings{
		ChannelAutoPriorityEnabled:                 true,
		ChannelAutoPriorityIntervalMinutes:         15,
		ChannelAutoPriorityWindowHours:             24,
		ChannelAutoPriorityAvailabilityWindowHours: 24,
		ChannelAutoPriorityRateMultiplier:          1,
		ChannelAutoPriorityLastRunAt:               now - 60,
	})
	neverRunManual := createAutoPriorityTestChannel(t, "manual never run", 100, dto.ChannelOtherSettings{
		ChannelAutoPriorityEnabled:                 true,
		ChannelAutoPriorityIntervalMinutes:         30,
		ChannelAutoPriorityWindowHours:             24,
		ChannelAutoPriorityAvailabilityWindowHours: 24,
		ChannelAutoPriorityRateMultiplier:          1,
	})
	source := createSyncTestSource(t, map[string]any{
		"auto_priority_enabled":      true,
		"auto_priority_window_hours": 24,
	})
	generated, _ := createGeneratedAutoPriorityTestChannel(t, source.Id, 8, "generated", 100)
	generatedSettings := generated.GetOtherSettings()
	generatedSettings.ChannelAutoPriorityIntervalMinutes = 5
	generatedSettings.ChannelAutoPriorityLastRunAt = now - 60
	generated.SetOtherSettings(generatedSettings)
	require.NoError(t, model.DB.Model(&model.Channel{}).
		Where("id = ?", generated.Id).
		Update("settings", generated.OtherSettings).Error)

	for _, channel := range []model.Channel{recentManual, neverRunManual, generated} {
		createAutoPriorityTestUsageLog(t, channel.Id, now-60)
		createAutoPriorityTestMonitorLog(t, channel.Id, now-60)
	}

	results := RunDueChannelAutoPriority(context.Background(), now)

	require.Len(t, results, 3)
	for _, channel := range []model.Channel{recentManual, neverRunManual, generated} {
		var reloaded model.Channel
		require.NoError(t, model.DB.First(&reloaded, channel.Id).Error)
		settings := reloaded.GetOtherSettings()
		assert.Equal(t, 30, settings.ChannelAutoPriorityIntervalMinutes)
		assert.Equal(t, now, settings.ChannelAutoPriorityLastRunAt)
		require.NotNil(t, settings.ChannelAutoPriorityLastScore)
	}

	var reloadedRecent, reloadedGenerated model.Channel
	require.NoError(t, model.DB.First(&reloadedRecent, recentManual.Id).Error)
	require.NoError(t, model.DB.First(&reloadedGenerated, generated.Id).Error)
	assert.Greater(t, reloadedRecent.GetPriority(), reloadedGenerated.GetPriority())
}

func TestRunDueChannelAutoPrioritySkipsGroupWhenEveryMemberIsRecent(t *testing.T) {
	setupUpstreamSourceAutoPriorityTestDB(t)
	now := int64(10_700_000)

	for _, name := range []string{"recent one", "recent two"} {
		channel := createAutoPriorityTestChannel(t, name, 100, dto.ChannelOtherSettings{
			ChannelAutoPriorityEnabled:         true,
			ChannelAutoPriorityIntervalMinutes: 30,
			ChannelAutoPriorityWindowHours:     24,
			ChannelAutoPriorityLastRunAt:       now - 60,
		})
		createAutoPriorityTestUsageLog(t, channel.Id, now-60)
		createAutoPriorityTestMonitorLog(t, channel.Id, now-60)
	}

	results := RunDueChannelAutoPriority(context.Background(), now)

	assert.Empty(t, results)
}

func TestRunDueChannelAutoPriorityRunsGroupWhenAnyMemberIsOverdue(t *testing.T) {
	setupUpstreamSourceAutoPriorityTestDB(t)
	now := int64(10_750_000)

	overdue := createAutoPriorityTestChannel(t, "short interval overdue", 100, dto.ChannelOtherSettings{
		ChannelAutoPriorityEnabled:         true,
		ChannelAutoPriorityIntervalMinutes: 15,
		ChannelAutoPriorityWindowHours:     24,
		ChannelAutoPriorityLastRunAt:       now - 16*60,
	})
	recent := createAutoPriorityTestChannel(t, "long interval recent", 100, dto.ChannelOtherSettings{
		ChannelAutoPriorityEnabled:         true,
		ChannelAutoPriorityIntervalMinutes: 30,
		ChannelAutoPriorityWindowHours:     24,
		ChannelAutoPriorityLastRunAt:       now - 10*60,
	})
	for _, channel := range []model.Channel{overdue, recent} {
		createAutoPriorityTestUsageLog(t, channel.Id, now-60)
		createAutoPriorityTestMonitorLog(t, channel.Id, now-60)
	}

	results := RunDueChannelAutoPriority(context.Background(), now)

	require.Len(t, results, 2)
	for _, channel := range []model.Channel{overdue, recent} {
		var reloaded model.Channel
		require.NoError(t, model.DB.First(&reloaded, channel.Id).Error)
		settings := reloaded.GetOtherSettings()
		assert.Equal(t, 30, settings.ChannelAutoPriorityIntervalMinutes)
		assert.Equal(t, now, settings.ChannelAutoPriorityLastRunAt)
	}
}

func TestRunChannelAutoPriorityGroupForcesRecentGroupOnly(t *testing.T) {
	setupUpstreamSourceAutoPriorityTestDB(t)
	now := int64(10_800_000)

	selected := createAutoPriorityTestChannel(t, "force selected", 100, dto.ChannelOtherSettings{
		ChannelAutoPriorityEnabled:         true,
		ChannelAutoPriorityIntervalMinutes: 30,
		ChannelAutoPriorityWindowHours:     24,
		ChannelAutoPriorityLastRunAt:       now - 60,
		ChannelAutoPriorityRateMultiplier:  1,
	})
	peer := createAutoPriorityTestChannel(t, "force peer", 100, dto.ChannelOtherSettings{
		ChannelAutoPriorityEnabled:         true,
		ChannelAutoPriorityIntervalMinutes: 30,
		ChannelAutoPriorityWindowHours:     24,
		ChannelAutoPriorityLastRunAt:       now - 60,
		ChannelAutoPriorityRateMultiplier:  2,
	})
	otherGroup := createAutoPriorityTestChannel(t, "force other group", 100, dto.ChannelOtherSettings{
		ChannelAutoPriorityEnabled:         true,
		ChannelAutoPriorityIntervalMinutes: 30,
		ChannelAutoPriorityWindowHours:     24,
		ChannelAutoPriorityLastRunAt:       now - 60,
		ChannelAutoPriorityRateMultiplier:  1,
	})
	require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", otherGroup.Id).Update("group", "other").Error)
	require.NoError(t, model.DB.Model(&model.Ability{}).Where("channel_id = ?", otherGroup.Id).Update("group", "other").Error)

	for _, channel := range []model.Channel{selected, peer, otherGroup} {
		createAutoPriorityTestUsageLog(t, channel.Id, now-60)
		createAutoPriorityTestMonitorLog(t, channel.Id, now-60)
	}

	results, err := RunChannelAutoPriorityGroup(context.Background(), selected.Id, now)

	require.NoError(t, err)
	require.Len(t, results, 2)
	for _, channel := range []model.Channel{selected, peer} {
		var reloaded model.Channel
		require.NoError(t, model.DB.First(&reloaded, channel.Id).Error)
		assert.Equal(t, now, reloaded.GetOtherSettings().ChannelAutoPriorityLastRunAt)
	}
	var reloadedOther model.Channel
	require.NoError(t, model.DB.First(&reloadedOther, otherGroup.Id).Error)
	assert.Equal(t, now-60, reloadedOther.GetOtherSettings().ChannelAutoPriorityLastRunAt)
}

func TestPersistChannelAutoPriorityGroupRollsBackEveryMemberOnConflict(t *testing.T) {
	setupUpstreamSourceAutoPriorityTestDB(t)
	now := int64(10_900_000)
	first := createAutoPriorityTestChannel(t, "atomic first", 100, dto.ChannelOtherSettings{
		ChannelAutoPriorityEnabled:         true,
		ChannelAutoPriorityIntervalMinutes: 30,
		ChannelAutoPriorityWindowHours:     24,
	})
	second := createAutoPriorityTestChannel(t, "atomic second", 100, dto.ChannelOtherSettings{
		ChannelAutoPriorityEnabled:         true,
		ChannelAutoPriorityIntervalMinutes: 30,
		ChannelAutoPriorityWindowHours:     24,
	})
	candidates := []upstreamSourceAutoPriorityCandidate{
		{
			channel:  first,
			settings: first.GetOtherSettings(),
			resolution: upstreamSourceRuleResolution{
				AutoPriorityEnabled:         true,
				AutoPriorityIntervalMinutes: 30,
				AutoPriorityWindowHours:     24,
			},
			windowStart: now - 24*3600,
			windowEnd:   now,
		},
		{
			channel:  second,
			settings: second.GetOtherSettings(),
			resolution: upstreamSourceRuleResolution{
				AutoPriorityEnabled:         true,
				AutoPriorityIntervalMinutes: 30,
				AutoPriorityWindowHours:     24,
			},
			windowStart: now - 24*3600,
			windowEnd:   now,
		},
	}
	scores := []AutoPriorityScoreResult{
		{ChannelID: first.Id, OldPriority: 100, NewPriority: 900, ComputedPriority: 900, Applied: true},
		{ChannelID: second.Id, OldPriority: 100, NewPriority: 800, ComputedPriority: 800, Applied: true},
	}
	changedSettings := second.GetOtherSettings()
	changedSettings.ChannelAutoPriorityRateMultiplier = 2
	second.SetOtherSettings(changedSettings)
	require.NoError(t, model.DB.Model(&model.Channel{}).
		Where("id = ?", second.Id).
		Update("settings", second.OtherSettings).Error)

	reason, err := persistChannelAutoPriorityGroup(
		context.Background(),
		candidates,
		scores,
		[]int{0, 1},
		now,
	)

	require.NoError(t, err)
	assert.Equal(t, "generated_channel_changed", reason)
	var reloadedFirst model.Channel
	require.NoError(t, model.DB.First(&reloadedFirst, first.Id).Error)
	assert.Equal(t, int64(100), reloadedFirst.GetPriority())
	assert.Zero(t, reloadedFirst.GetOtherSettings().ChannelAutoPriorityLastRunAt)
}
