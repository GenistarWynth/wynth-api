package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
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

func TestRunDueChannelAutoPriorityEvaluatesCompleteCohortWhenOnlyEnabledSubsetIsDue(t *testing.T) {
	setupUpstreamSourceAutoPriorityTestDB(t)
	now := int64(10_025_000)

	type cohortMember struct {
		rate          float64
		status        int
		lastRunAt     int64
		expectedPrice float64
	}
	members := []cohortMember{
		{rate: 0.02, status: common.ChannelStatusAutoDisabled, lastRunAt: now - 60, expectedPrice: 100},
		{rate: 0.04, status: common.ChannelStatusAutoDisabled, lastRunAt: now - 60, expectedPrice: 50},
		{rate: 0.06, status: common.ChannelStatusAutoDisabled, lastRunAt: now - 60, expectedPrice: 100.0 / 3.0},
		{rate: 0.05, status: common.ChannelStatusEnabled, lastRunAt: now - 6*60, expectedPrice: 40},
		{rate: 0.08, status: common.ChannelStatusEnabled, lastRunAt: now - 6*60, expectedPrice: 25},
	}
	channels := make([]model.Channel, 0, len(members))
	for i, member := range members {
		channel := createAutoPriorityTestChannel(t, fmt.Sprintf("cohort member %d", i), int64(500+i), dto.ChannelOtherSettings{
			ChannelAutoPriorityEnabled:         true,
			ChannelAutoPriorityIntervalMinutes: 5,
			ChannelAutoPriorityWindowHours:     24,
			ChannelAutoPriorityRateMultiplier:  member.rate,
			ChannelAutoPriorityLastRunAt:       member.lastRunAt,
		})
		if member.status != common.ChannelStatusEnabled {
			require.NoError(t, model.DB.Model(&model.Channel{}).
				Where("id = ?", channel.Id).
				Update("status", member.status).Error)
		}
		channels = append(channels, channel)
	}

	results := RunDueChannelAutoPriority(context.Background(), now)

	require.Len(t, results, len(members))
	var cheapestPrice, dearerPrice float64
	var cohort string
	for i, channel := range channels {
		var reloaded model.Channel
		require.NoError(t, model.DB.First(&reloaded, channel.Id).Error)
		settings := reloaded.GetOtherSettings()
		assert.Equal(t, now, settings.ChannelAutoPriorityLastRunAt)
		require.NotNil(t, settings.ChannelAutoPriorityLastScore)
		snapshot := settings.ChannelAutoPriorityLastScore
		assert.Equal(t, "v4", snapshot.Version)
		assert.Equal(t, now, snapshot.ComputedAt)
		assert.InDelta(t, 0.02, snapshot.CohortFloor, 1e-12)
		assert.InDelta(t, 0.08, snapshot.CohortCeil, 1e-12)
		assert.Equal(t, len(members), snapshot.CohortMemberCount)
		assert.InDelta(t, members[i].expectedPrice, snapshot.NominalPriceScore, 1e-9)
		if cohort == "" {
			cohort = snapshot.Cohort
		} else {
			assert.Equal(t, cohort, snapshot.Cohort)
		}
		if members[i].rate == 0.02 {
			cheapestPrice = snapshot.NominalPriceScore
		}
		if members[i].rate == 0.06 {
			dearerPrice = snapshot.NominalPriceScore
		}
	}
	assert.GreaterOrEqual(t, cheapestPrice, dearerPrice)
}

func TestRunDueChannelAutoPriorityRefreshesStaleAutoDisabledSnapshot(t *testing.T) {
	setupUpstreamSourceAutoPriorityTestDB(t)
	now := int64(10_040_000)

	stale := createAutoPriorityTestChannel(t, "stale auto disabled", 321, dto.ChannelOtherSettings{
		ChannelAutoPriorityEnabled:         true,
		ChannelAutoPriorityIntervalMinutes: 5,
		ChannelAutoPriorityWindowHours:     24,
		ChannelAutoPriorityRateMultiplier:  0.02,
		ChannelAutoPriorityLastRunAt:       now - 5*3600,
		ChannelAutoPriorityLastScore: &dto.ChannelAutoPriorityScore{
			Version:                 "v2",
			ComputedAt:              now - 5*3600,
			EffectiveRateMultiplier: 0.02,
			EffectivePriceScore:     100,
			FinalScore:              99,
			NewPriority:             321,
			Applied:                 true,
		},
	})
	require.NoError(t, model.DB.Model(&model.Channel{}).
		Where("id = ?", stale.Id).
		Update("status", common.ChannelStatusAutoDisabled).Error)
	recent := createAutoPriorityTestChannel(t, "recent enabled peer", 222, dto.ChannelOtherSettings{
		ChannelAutoPriorityEnabled:         true,
		ChannelAutoPriorityIntervalMinutes: 5,
		ChannelAutoPriorityWindowHours:     24,
		ChannelAutoPriorityRateMultiplier:  0.06,
		ChannelAutoPriorityLastRunAt:       now - 60,
	})

	results := RunDueChannelAutoPriority(context.Background(), now)

	require.Len(t, results, 2)
	var reloadedStale, reloadedRecent model.Channel
	require.NoError(t, model.DB.First(&reloadedStale, stale.Id).Error)
	require.NoError(t, model.DB.First(&reloadedRecent, recent.Id).Error)
	assert.Equal(t, int64(321), reloadedStale.GetPriority())
	var staleAbility model.Ability
	require.NoError(t, model.DB.Where("channel_id = ?", stale.Id).First(&staleAbility).Error)
	require.NotNil(t, staleAbility.Priority)
	assert.Equal(t, int64(321), *staleAbility.Priority)

	staleSettings := reloadedStale.GetOtherSettings()
	recentSettings := reloadedRecent.GetOtherSettings()
	assert.Equal(t, now, staleSettings.ChannelAutoPriorityLastRunAt)
	assert.Equal(t, now, recentSettings.ChannelAutoPriorityLastRunAt)
	require.NotNil(t, staleSettings.ChannelAutoPriorityLastScore)
	require.NotNil(t, recentSettings.ChannelAutoPriorityLastScore)
	assert.Equal(t, "v4", staleSettings.ChannelAutoPriorityLastScore.Version)
	assert.Equal(t, recentSettings.ChannelAutoPriorityLastScore.Version, staleSettings.ChannelAutoPriorityLastScore.Version)
	assert.Equal(t, recentSettings.ChannelAutoPriorityLastScore.ComputedAt, staleSettings.ChannelAutoPriorityLastScore.ComputedAt)
	assert.Equal(t, "channel_auto_disabled", staleSettings.ChannelAutoPriorityLastScore.Reason)
	assert.Equal(t, 0.0, staleSettings.ChannelAutoPriorityLastScore.FinalScore)
	assert.Equal(t, int64(321), staleSettings.ChannelAutoPriorityLastScore.NewPriority)
	assert.False(t, staleSettings.ChannelAutoPriorityLastScore.Applied)
	assert.InDelta(t, 0.02, staleSettings.ChannelAutoPriorityLastScore.CohortFloor, 1e-12)
	assert.Equal(t, 2, staleSettings.ChannelAutoPriorityLastScore.CohortMemberCount)
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
	assert.Equal(t, int64(991), reloaded.GetPriority())
	assert.Equal(t, now, reloaded.GetOtherSettings().ChannelAutoPriorityLastRunAt)
}

func TestRunDueChannelAutoPriorityUsesMappingRateSourceNotGeneratedName(t *testing.T) {
	setupUpstreamSourceAutoPriorityTestDB(t)
	now := int64(10_325_000)

	source := createSyncTestSource(t, map[string]any{
		"auto_priority_enabled":          true,
		"auto_priority_interval_minutes": 0,
		"auto_priority_window_hours":     24,
	})
	generatedChannel, mapping := createGeneratedAutoPriorityTestChannel(t, source.Id, 0.05, "duck", 100)
	require.NoError(t, model.DB.Model(&model.Channel{}).
		Where("id = ?", generatedChannel.Id).
		Update("name", "可达鸭 / 0.040x").Error)
	createAutoPriorityTestUsageLog(t, generatedChannel.Id, now-60)
	createAutoPriorityTestMonitorLog(t, generatedChannel.Id, now-60)

	results := RunDueChannelAutoPriority(context.Background(), now)

	require.Len(t, results, 1)
	var reloaded model.Channel
	require.NoError(t, model.DB.First(&reloaded, generatedChannel.Id).Error)
	settings := reloaded.GetOtherSettings()
	require.NotNil(t, settings.ChannelAutoPriorityLastScore)
	assert.Equal(t, 0.05, settings.ChannelAutoPriorityLastScore.NominalRateMultiplier)
	assert.Equal(t, 0.05, settings.ChannelAutoPriorityLastScore.EffectiveRateMultiplier)
	var reloadedMapping model.UpstreamSourceChannelMapping
	require.NoError(t, model.DB.First(&reloadedMapping, mapping.Id).Error)
	require.NotNil(t, reloadedMapping.EffectiveRateMultiplier)
	assert.Equal(t, 0.05, *reloadedMapping.EffectiveRateMultiplier)
}

func TestRunDueChannelAutoPriorityUsesConfiguredRateSourceForManualChannel(t *testing.T) {
	setupUpstreamSourceAutoPriorityTestDB(t)
	now := int64(10_330_000)

	manualChannel := createAutoPriorityTestChannel(t, "manual / 0.040x", 100, dto.ChannelOtherSettings{
		ChannelAutoPriorityEnabled:         true,
		ChannelAutoPriorityIntervalMinutes: 0,
		ChannelAutoPriorityWindowHours:     24,
		ChannelAutoPriorityRateMultiplier:  0.07,
	})
	createAutoPriorityTestUsageLog(t, manualChannel.Id, now-60)
	createAutoPriorityTestMonitorLog(t, manualChannel.Id, now-60)

	results := RunDueChannelAutoPriority(context.Background(), now)

	require.Len(t, results, 1)
	var reloaded model.Channel
	require.NoError(t, model.DB.First(&reloaded, manualChannel.Id).Error)
	settings := reloaded.GetOtherSettings()
	require.NotNil(t, settings.ChannelAutoPriorityLastScore)
	assert.Equal(t, 0.07, settings.ChannelAutoPriorityLastScore.NominalRateMultiplier)
	assert.Equal(t, 0.07, settings.ChannelAutoPriorityLastScore.EffectiveRateMultiplier)
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
	assert.Equal(t, int64(991), *reloadedAbility.Priority)
}

func TestRunDueChannelAutoPrioritySinksManuallyDisabledMembers(t *testing.T) {
	setupUpstreamSourceAutoPriorityTestDB(t)
	now := int64(10_500_000)

	cheaperEnabled := createAutoPriorityTestChannel(t, "cheaper enabled", 100, dto.ChannelOtherSettings{
		ChannelAutoPriorityEnabled:         true,
		ChannelAutoPriorityIntervalMinutes: 0,
		ChannelAutoPriorityWindowHours:     24,
		ChannelAutoPriorityRateMultiplier:  1,
	})
	dearerEnabled := createAutoPriorityTestChannel(t, "dearer enabled", 100, dto.ChannelOtherSettings{
		ChannelAutoPriorityEnabled:         true,
		ChannelAutoPriorityIntervalMinutes: 0,
		ChannelAutoPriorityWindowHours:     24,
		ChannelAutoPriorityRateMultiplier:  2,
	})
	manuallyDisabled := createAutoPriorityTestChannel(t, "manually disabled", 5_000, dto.ChannelOtherSettings{
		ChannelAutoPriorityEnabled:         true,
		ChannelAutoPriorityIntervalMinutes: 0,
		ChannelAutoPriorityWindowHours:     24,
		ChannelAutoPriorityRateMultiplier:  0.01,
	})
	require.NoError(t, model.DB.Model(&model.Channel{}).
		Where("id = ?", manuallyDisabled.Id).
		Update("status", common.ChannelStatusManuallyDisabled).Error)
	for _, channel := range []model.Channel{cheaperEnabled, dearerEnabled} {
		createAutoPriorityTestUsageLog(t, channel.Id, now-60)
		createAutoPriorityTestMonitorLog(t, channel.Id, now-60)
	}

	results := RunDueChannelAutoPriority(context.Background(), now)

	require.Len(t, results, 3)
	var sinkResult *ChannelAutoPriorityRunResult
	for i := range results {
		if results[i].ChannelID == manuallyDisabled.Id {
			sinkResult = &results[i]
			break
		}
	}
	require.NotNil(t, sinkResult)
	assert.True(t, sinkResult.Applied)
	assert.Equal(t, "manually_disabled_sunk", sinkResult.Reason)

	var reloadedDisabled model.Channel
	require.NoError(t, model.DB.First(&reloadedDisabled, manuallyDisabled.Id).Error)
	assert.Equal(t, int64(-1), reloadedDisabled.GetPriority())
	disabledSettings := reloadedDisabled.GetOtherSettings()
	assert.Zero(t, disabledSettings.ChannelAutoPriorityLastRunAt)
	assert.Nil(t, disabledSettings.ChannelAutoPriorityLastScore)

	var disabledAbility model.Ability
	require.NoError(t, model.DB.Where("channel_id = ?", manuallyDisabled.Id).First(&disabledAbility).Error)
	require.NotNil(t, disabledAbility.Priority)
	assert.Equal(t, int64(-1), *disabledAbility.Priority)

	var reloadedCheaper, reloadedDearer model.Channel
	require.NoError(t, model.DB.First(&reloadedCheaper, cheaperEnabled.Id).Error)
	require.NoError(t, model.DB.First(&reloadedDearer, dearerEnabled.Id).Error)
	assert.Greater(t, reloadedCheaper.GetPriority(), reloadedDearer.GetPriority())
	assert.Greater(t, reloadedDearer.GetPriority(), reloadedDisabled.GetPriority())
}

func TestRunDueChannelAutoPriorityManualSinkIgnoresAutoDisabledPriority(t *testing.T) {
	setupUpstreamSourceAutoPriorityTestDB(t)
	now := int64(10_510_000)

	enabled := createAutoPriorityTestChannel(t, "enabled sink reference", 100, dto.ChannelOtherSettings{
		ChannelAutoPriorityEnabled:         true,
		ChannelAutoPriorityIntervalMinutes: 0,
		ChannelAutoPriorityWindowHours:     24,
		ChannelAutoPriorityRateMultiplier:  1,
	})
	autoDisabled := createAutoPriorityTestChannel(t, "auto disabled stale priority", -5_000, dto.ChannelOtherSettings{
		ChannelAutoPriorityEnabled:         true,
		ChannelAutoPriorityIntervalMinutes: 0,
		ChannelAutoPriorityWindowHours:     24,
		ChannelAutoPriorityRateMultiplier:  0.5,
	})
	manuallyDisabled := createAutoPriorityTestChannel(t, "manual disabled sink target", 5_000, dto.ChannelOtherSettings{
		ChannelAutoPriorityEnabled:         true,
		ChannelAutoPriorityIntervalMinutes: 0,
		ChannelAutoPriorityWindowHours:     24,
		ChannelAutoPriorityRateMultiplier:  2,
	})
	require.NoError(t, model.DB.Model(&model.Channel{}).
		Where("id = ?", autoDisabled.Id).
		Update("status", common.ChannelStatusAutoDisabled).Error)
	require.NoError(t, model.DB.Model(&model.Channel{}).
		Where("id = ?", manuallyDisabled.Id).
		Update("status", common.ChannelStatusManuallyDisabled).Error)
	createAutoPriorityTestUsageLog(t, enabled.Id, now-60)
	createAutoPriorityTestMonitorLog(t, enabled.Id, now-60)

	results := RunDueChannelAutoPriority(context.Background(), now)

	require.Len(t, results, 3)
	var reloadedAutoDisabled, reloadedManuallyDisabled model.Channel
	require.NoError(t, model.DB.First(&reloadedAutoDisabled, autoDisabled.Id).Error)
	require.NoError(t, model.DB.First(&reloadedManuallyDisabled, manuallyDisabled.Id).Error)
	assert.Equal(t, int64(-5_000), reloadedAutoDisabled.GetPriority())
	assert.Equal(t, int64(-1), reloadedManuallyDisabled.GetPriority())
}

func TestRunDueChannelAutoPrioritySinksManuallyDisabledWhenAutoPriorityIsOff(t *testing.T) {
	setupUpstreamSourceAutoPriorityTestDB(t)
	now := int64(10_525_000)
	manuallyDisabled := createAutoPriorityTestChannel(t, "manual disabled AP off", 813, dto.ChannelOtherSettings{
		ChannelAutoPriorityEnabled:       false,
		ChannelAutoPriorityLastRunAt:     now - 60,
		ChannelAutoPriorityLastAppliedAt: now - 120,
		ChannelAutoPriorityLastScore: &dto.ChannelAutoPriorityScore{
			ComputedAt:  now - 60,
			FinalScore:  81.3,
			NewPriority: 813,
		},
	})
	settingsObject := make(map[string]any)
	require.NoError(t, common.UnmarshalJsonStr(manuallyDisabled.OtherSettings, &settingsObject))
	settingsObject["custom_unknown_setting"] = "keep-me"
	settingsBytes, err := common.Marshal(settingsObject)
	require.NoError(t, err)
	require.NoError(t, model.DB.Model(&model.Channel{}).
		Where("id = ?", manuallyDisabled.Id).
		Updates(map[string]any{
			"status":   common.ChannelStatusManuallyDisabled,
			"settings": string(settingsBytes),
		}).Error)

	results := RunDueChannelAutoPriority(context.Background(), now)

	require.Len(t, results, 1)
	assert.Equal(t, manuallyDisabled.Id, results[0].ChannelID)
	assert.True(t, results[0].Applied)
	assert.Equal(t, "manually_disabled_sunk", results[0].Reason)
	var reloaded model.Channel
	require.NoError(t, model.DB.First(&reloaded, manuallyDisabled.Id).Error)
	assert.Equal(t, int64(-1), reloaded.GetPriority())
	reloadedSettings := reloaded.GetOtherSettings()
	assert.Zero(t, reloadedSettings.ChannelAutoPriorityLastRunAt)
	assert.Zero(t, reloadedSettings.ChannelAutoPriorityLastAppliedAt)
	assert.Nil(t, reloadedSettings.ChannelAutoPriorityLastScore)
	reloadedObject := make(map[string]any)
	require.NoError(t, common.UnmarshalJsonStr(reloaded.OtherSettings, &reloadedObject))
	assert.Equal(t, "keep-me", reloadedObject["custom_unknown_setting"])
	var ability model.Ability
	require.NoError(t, model.DB.Where("channel_id = ?", manuallyDisabled.Id).First(&ability).Error)
	require.NotNil(t, ability.Priority)
	assert.Equal(t, int64(-1), *ability.Priority)
}

func TestRunDueChannelAutoPrioritySinksManuallyDisabledWhenGroupIsNotDue(t *testing.T) {
	setupUpstreamSourceAutoPriorityTestDB(t)
	now := int64(10_540_000)
	enabled := createAutoPriorityTestChannel(t, "not due enabled", 700, dto.ChannelOtherSettings{
		ChannelAutoPriorityEnabled:         true,
		ChannelAutoPriorityIntervalMinutes: 30,
		ChannelAutoPriorityWindowHours:     24,
		ChannelAutoPriorityLastRunAt:       now - 60,
		ChannelAutoPriorityLastScore: &dto.ChannelAutoPriorityScore{
			ComputedAt:  now - 60,
			NewPriority: 700,
		},
	})
	manuallyDisabled := createAutoPriorityTestChannel(t, "not due disabled", 626, dto.ChannelOtherSettings{
		ChannelAutoPriorityEnabled:         true,
		ChannelAutoPriorityIntervalMinutes: 30,
		ChannelAutoPriorityWindowHours:     24,
		ChannelAutoPriorityLastRunAt:       now - 60,
		ChannelAutoPriorityLastScore: &dto.ChannelAutoPriorityScore{
			ComputedAt:  now - 60,
			NewPriority: 626,
		},
	})
	require.NoError(t, model.DB.Model(&model.Channel{}).
		Where("id = ?", manuallyDisabled.Id).
		Update("status", common.ChannelStatusManuallyDisabled).Error)

	results := RunDueChannelAutoPriority(context.Background(), now)

	require.Len(t, results, 1)
	assert.Equal(t, manuallyDisabled.Id, results[0].ChannelID)
	assert.True(t, results[0].Applied)
	var reloadedEnabled, reloadedDisabled model.Channel
	require.NoError(t, model.DB.First(&reloadedEnabled, enabled.Id).Error)
	require.NoError(t, model.DB.First(&reloadedDisabled, manuallyDisabled.Id).Error)
	assert.Equal(t, int64(700), reloadedEnabled.GetPriority())
	assert.Equal(t, now-60, reloadedEnabled.GetOtherSettings().ChannelAutoPriorityLastRunAt)
	assert.Equal(t, int64(-1), reloadedDisabled.GetPriority())
	assert.Zero(t, reloadedDisabled.GetOtherSettings().ChannelAutoPriorityLastRunAt)
}

func TestUpdateChannelStatusImmediatelySinksManualDisableOnly(t *testing.T) {
	setupUpstreamSourceAutoPriorityTestDB(t)
	manual := createAutoPriorityTestChannel(t, "immediate manual disable", 50, dto.ChannelOtherSettings{
		ChannelAutoPriorityEnabled:   true,
		ChannelAutoPriorityLastRunAt: 1234,
		ChannelAutoPriorityLastScore: &dto.ChannelAutoPriorityScore{NewPriority: 50},
	})
	auto := createAutoPriorityTestChannel(t, "immediate auto disable", 236, dto.ChannelOtherSettings{
		ChannelAutoPriorityEnabled:       true,
		ChannelAutoPriorityLastRunAt:     2345,
		ChannelAutoPriorityLastAppliedAt: 2300,
		ChannelAutoPriorityLastScore: &dto.ChannelAutoPriorityScore{
			ComputedAt:  2345,
			FinalScore:  62.4,
			NewPriority: 236,
		},
	})

	assert.True(t, model.UpdateChannelStatus(manual.Id, "", common.ChannelStatusManuallyDisabled, "manual operation"))
	assert.True(t, model.UpdateChannelStatus(auto.Id, "", common.ChannelStatusAutoDisabled, "automatic operation"))

	var reloadedManual, reloadedAuto model.Channel
	require.NoError(t, model.DB.First(&reloadedManual, manual.Id).Error)
	require.NoError(t, model.DB.First(&reloadedAuto, auto.Id).Error)
	assert.Equal(t, int64(-1), reloadedManual.GetPriority())
	assert.Equal(t, int64(236), reloadedAuto.GetPriority())
	assert.Nil(t, reloadedManual.GetOtherSettings().ChannelAutoPriorityLastScore)
	autoSettings := reloadedAuto.GetOtherSettings()
	assert.Equal(t, int64(2345), autoSettings.ChannelAutoPriorityLastRunAt)
	assert.Equal(t, int64(2300), autoSettings.ChannelAutoPriorityLastAppliedAt)
	require.NotNil(t, autoSettings.ChannelAutoPriorityLastScore)
	assert.Equal(t, 62.4, autoSettings.ChannelAutoPriorityLastScore.FinalScore)
	assert.Equal(t, int64(236), autoSettings.ChannelAutoPriorityLastScore.NewPriority)
	var manualAbility, autoAbility model.Ability
	require.NoError(t, model.DB.Where("channel_id = ?", manual.Id).First(&manualAbility).Error)
	require.NoError(t, model.DB.Where("channel_id = ?", auto.Id).First(&autoAbility).Error)
	require.NotNil(t, manualAbility.Priority)
	require.NotNil(t, autoAbility.Priority)
	assert.Equal(t, int64(-1), *manualAbility.Priority)
	assert.Equal(t, int64(236), *autoAbility.Priority)
}

func TestUpdateChannelStatusRefreshesManualDisableCleanupInMemoryCache(t *testing.T) {
	setupUpstreamSourceAutoPriorityTestDB(t)
	channel := createAutoPriorityTestChannel(t, "cached manual disable", 813, dto.ChannelOtherSettings{
		ChannelAutoPriorityEnabled:   true,
		ChannelAutoPriorityLastRunAt: 1234,
		ChannelAutoPriorityLastScore: &dto.ChannelAutoPriorityScore{
			ComputedAt:  1234,
			FinalScore:  81.3,
			NewPriority: 813,
		},
	})
	oldMemoryCacheEnabled := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = true
	model.InitChannelCache()
	t.Cleanup(func() {
		common.MemoryCacheEnabled = oldMemoryCacheEnabled
	})

	require.True(t, model.UpdateChannelStatus(channel.Id, "", common.ChannelStatusManuallyDisabled, "manual operation"))

	cached, err := model.CacheGetChannel(channel.Id)
	require.NoError(t, err)
	assert.Equal(t, common.ChannelStatusManuallyDisabled, cached.Status)
	assert.Equal(t, int64(-1), cached.GetPriority())
	assert.Nil(t, cached.GetOtherSettings().ChannelAutoPriorityLastScore)
}

func TestUpdateChannelStatusRollsBackManualDisableWhenCleanupFails(t *testing.T) {
	setupUpstreamSourceAutoPriorityTestDB(t)
	channel := createAutoPriorityTestChannel(t, "rollback manual disable", 813, dto.ChannelOtherSettings{
		ChannelAutoPriorityEnabled:   true,
		ChannelAutoPriorityLastRunAt: 1234,
		ChannelAutoPriorityLastScore: &dto.ChannelAutoPriorityScore{
			ComputedAt:  1234,
			FinalScore:  81.3,
			NewPriority: 813,
		},
	})
	callbackName := fmt.Sprintf("fail_manual_disable_cleanup_%s", t.Name())
	require.NoError(t, model.DB.Callback().Update().Before("gorm:update").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Name == "Ability" {
			tx.AddError(errors.New("forced ability cleanup failure"))
		}
	}))
	t.Cleanup(func() {
		require.NoError(t, model.DB.Callback().Update().Remove(callbackName))
	})

	changed := model.UpdateChannelStatus(channel.Id, "", common.ChannelStatusManuallyDisabled, "manual operation")

	assert.False(t, changed)
	var reloaded model.Channel
	require.NoError(t, model.DB.First(&reloaded, channel.Id).Error)
	assert.Equal(t, common.ChannelStatusEnabled, reloaded.Status)
	assert.Equal(t, int64(813), reloaded.GetPriority())
	require.NotNil(t, reloaded.GetOtherSettings().ChannelAutoPriorityLastScore)
	var ability model.Ability
	require.NoError(t, model.DB.Where("channel_id = ?", channel.Id).First(&ability).Error)
	assert.True(t, ability.Enabled)
	require.NotNil(t, ability.Priority)
	assert.Equal(t, int64(813), *ability.Priority)
}

func TestDisableChannelByTagImmediatelySinksAndClearsAutoPriorityState(t *testing.T) {
	setupUpstreamSourceAutoPriorityTestDB(t)
	channel := createAutoPriorityTestChannel(t, "tag disable", 813, dto.ChannelOtherSettings{
		ChannelAutoPriorityEnabled:       true,
		ChannelAutoPriorityLastRunAt:     3456,
		ChannelAutoPriorityLastAppliedAt: 3400,
		ChannelAutoPriorityLastScore: &dto.ChannelAutoPriorityScore{
			ComputedAt:  3456,
			FinalScore:  81.3,
			NewPriority: 813,
		},
	})
	tag := "manual-disable-tag"
	require.NoError(t, model.DB.Model(&model.Channel{}).
		Where("id = ?", channel.Id).
		Update("tag", tag).Error)
	require.NoError(t, model.DB.Model(&model.Ability{}).
		Where("channel_id = ?", channel.Id).
		Update("tag", tag).Error)

	require.NoError(t, model.DisableChannelByTag(tag))

	var reloaded model.Channel
	require.NoError(t, model.DB.First(&reloaded, channel.Id).Error)
	assert.Equal(t, common.ChannelStatusManuallyDisabled, reloaded.Status)
	assert.Equal(t, int64(-1), reloaded.GetPriority())
	settings := reloaded.GetOtherSettings()
	assert.Zero(t, settings.ChannelAutoPriorityLastRunAt)
	assert.Zero(t, settings.ChannelAutoPriorityLastAppliedAt)
	assert.Nil(t, settings.ChannelAutoPriorityLastScore)
	var ability model.Ability
	require.NoError(t, model.DB.Where("channel_id = ?", channel.Id).First(&ability).Error)
	assert.False(t, ability.Enabled)
	require.NotNil(t, ability.Priority)
	assert.Equal(t, int64(-1), *ability.Priority)
}

func TestDisableChannelByTagRollsBackWhenCleanupFails(t *testing.T) {
	setupUpstreamSourceAutoPriorityTestDB(t)
	channel := createAutoPriorityTestChannel(t, "rollback tag disable", 813, dto.ChannelOtherSettings{
		ChannelAutoPriorityEnabled:   true,
		ChannelAutoPriorityLastRunAt: 3456,
		ChannelAutoPriorityLastScore: &dto.ChannelAutoPriorityScore{
			ComputedAt:  3456,
			FinalScore:  81.3,
			NewPriority: 813,
		},
	})
	tag := "rollback-manual-disable-tag"
	require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", channel.Id).Update("tag", tag).Error)
	require.NoError(t, model.DB.Model(&model.Ability{}).Where("channel_id = ?", channel.Id).Update("tag", tag).Error)
	callbackName := fmt.Sprintf("fail_tag_disable_cleanup_%s", t.Name())
	require.NoError(t, model.DB.Callback().Update().Before("gorm:update").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Name == "Ability" {
			tx.AddError(errors.New("forced tag ability cleanup failure"))
		}
	}))
	t.Cleanup(func() {
		require.NoError(t, model.DB.Callback().Update().Remove(callbackName))
	})

	err := model.DisableChannelByTag(tag)

	require.Error(t, err)
	var reloaded model.Channel
	require.NoError(t, model.DB.First(&reloaded, channel.Id).Error)
	assert.Equal(t, common.ChannelStatusEnabled, reloaded.Status)
	assert.Equal(t, int64(813), reloaded.GetPriority())
	require.NotNil(t, reloaded.GetOtherSettings().ChannelAutoPriorityLastScore)
	var ability model.Ability
	require.NoError(t, model.DB.Where("channel_id = ?", channel.Id).First(&ability).Error)
	assert.True(t, ability.Enabled)
	require.NotNil(t, ability.Priority)
	assert.Equal(t, int64(813), *ability.Priority)
}

func TestReenabledChannelReentersAutoPriorityOnNextGroupRun(t *testing.T) {
	setupUpstreamSourceAutoPriorityTestDB(t)
	now := int64(10_545_000)
	channel := createAutoPriorityTestChannel(t, "reenabled auto priority", 813, dto.ChannelOtherSettings{
		ChannelAutoPriorityEnabled:       true,
		ChannelAutoPriorityLastRunAt:     now - 60,
		ChannelAutoPriorityLastAppliedAt: now - 120,
		ChannelAutoPriorityLastScore: &dto.ChannelAutoPriorityScore{
			ComputedAt:  now - 60,
			FinalScore:  81.3,
			NewPriority: 813,
		},
	})
	require.True(t, model.UpdateChannelStatus(channel.Id, "", common.ChannelStatusManuallyDisabled, "manual operation"))
	require.True(t, model.UpdateChannelStatus(channel.Id, "", common.ChannelStatusEnabled, "manual operation"))
	createAutoPriorityTestUsageLog(t, channel.Id, now-30)
	createAutoPriorityTestMonitorLog(t, channel.Id, now-30)

	results := RunDueChannelAutoPriority(context.Background(), now)

	require.Len(t, results, 1)
	assert.Equal(t, channel.Id, results[0].ChannelID)
	var reloaded model.Channel
	require.NoError(t, model.DB.First(&reloaded, channel.Id).Error)
	assert.Equal(t, common.ChannelStatusEnabled, reloaded.Status)
	assert.NotEqual(t, int64(-1), reloaded.GetPriority())
	settings := reloaded.GetOtherSettings()
	assert.Equal(t, now, settings.ChannelAutoPriorityLastRunAt)
	require.NotNil(t, settings.ChannelAutoPriorityLastScore)
	assert.Equal(t, reloaded.GetPriority(), settings.ChannelAutoPriorityLastScore.NewPriority)
}

func TestRunDueChannelAutoPriorityRefreshesAutoDisabledMembersWithoutSinking(t *testing.T) {
	setupUpstreamSourceAutoPriorityTestDB(t)
	now := int64(10_550_000)

	enabled := createAutoPriorityTestChannel(t, "enabled peer", 100, dto.ChannelOtherSettings{
		ChannelAutoPriorityEnabled:         true,
		ChannelAutoPriorityIntervalMinutes: 0,
		ChannelAutoPriorityWindowHours:     24,
		ChannelAutoPriorityRateMultiplier:  1,
	})
	autoDisabled := createAutoPriorityTestChannel(t, "auto disabled extreme cheap", 5_000, dto.ChannelOtherSettings{
		ChannelAutoPriorityEnabled:        true,
		ChannelAutoPriorityWindowHours:    24,
		ChannelAutoPriorityRateMultiplier: 0.01,
		ChannelAutoPriorityLastRunAt:      now - 120,
		ChannelAutoPriorityLastAppliedAt:  now - 180,
		ChannelAutoPriorityLastScore: &dto.ChannelAutoPriorityScore{
			ComputedAt:  now - 120,
			FinalScore:  99.9,
			NewPriority: 5_000,
		},
	})
	require.NoError(t, model.DB.Model(&model.Channel{}).
		Where("id = ?", autoDisabled.Id).
		Update("status", common.ChannelStatusAutoDisabled).Error)
	createAutoPriorityTestUsageLog(t, enabled.Id, now-60)
	createAutoPriorityTestMonitorLog(t, enabled.Id, now-60)

	results := RunDueChannelAutoPriority(context.Background(), now)

	require.Len(t, results, 2)
	resultCount := make(map[int]int, len(results))
	for _, result := range results {
		resultCount[result.ChannelID]++
	}
	assert.Equal(t, 1, resultCount[enabled.Id])
	assert.Equal(t, 1, resultCount[autoDisabled.Id])
	var reloaded model.Channel
	require.NoError(t, model.DB.First(&reloaded, autoDisabled.Id).Error)
	assert.Equal(t, int64(5_000), reloaded.GetPriority())
	var reloadedAbility model.Ability
	require.NoError(t, model.DB.Where("channel_id = ?", autoDisabled.Id).First(&reloadedAbility).Error)
	require.NotNil(t, reloadedAbility.Priority)
	assert.Equal(t, int64(5_000), *reloadedAbility.Priority)
	settings := reloaded.GetOtherSettings()
	assert.Equal(t, now, settings.ChannelAutoPriorityLastRunAt)
	assert.Equal(t, now-180, settings.ChannelAutoPriorityLastAppliedAt)
	require.NotNil(t, settings.ChannelAutoPriorityLastScore)
	assert.Equal(t, "v4", settings.ChannelAutoPriorityLastScore.Version)
	assert.Equal(t, 0.0, settings.ChannelAutoPriorityLastScore.FinalScore)
	assert.Equal(t, int64(5_000), settings.ChannelAutoPriorityLastScore.NewPriority)
	assert.False(t, settings.ChannelAutoPriorityLastScore.Applied)
	assert.Equal(t, "channel_auto_disabled", settings.ChannelAutoPriorityLastScore.Reason)
	assert.InDelta(t, 0.01, settings.ChannelAutoPriorityLastScore.CohortFloor, 1e-12)
	assert.Equal(t, 2, settings.ChannelAutoPriorityLastScore.CohortMemberCount)
}

func TestRunDueChannelAutoPrioritySinksBelowNegativeEnabledPriority(t *testing.T) {
	setupUpstreamSourceAutoPriorityTestDB(t)
	now := int64(10_575_000)

	enabled := createAutoPriorityTestChannel(t, "negative enabled peer", -5, dto.ChannelOtherSettings{
		ChannelAutoPriorityEnabled:         true,
		ChannelAutoPriorityIntervalMinutes: 0,
		ChannelAutoPriorityWindowHours:     24,
		ChannelAutoPriorityRateMultiplier:  1,
		ChannelAutoPriorityLastScore: &dto.ChannelAutoPriorityScore{
			EffectiveCostMultiplier: 1,
		},
	})
	manuallyDisabled := createAutoPriorityTestChannel(t, "disabled above negative peer", 5_000, dto.ChannelOtherSettings{
		ChannelAutoPriorityEnabled:         true,
		ChannelAutoPriorityIntervalMinutes: 0,
		ChannelAutoPriorityWindowHours:     24,
		ChannelAutoPriorityRateMultiplier:  1,
	})
	require.NoError(t, model.DB.Model(&model.Channel{}).
		Where("id = ?", manuallyDisabled.Id).
		Update("status", common.ChannelStatusManuallyDisabled).Error)
	for i := int64(1); i <= 3; i++ {
		checkedAt := now - i*60
		require.NoError(t, model.DB.Create(&model.ChannelMonitorLog{
			ChannelID: enabled.Id,
			Status:    model.ChannelMonitorStatusFailed,
			CheckedAt: checkedAt,
			CreatedAt: checkedAt,
		}).Error)
	}

	results := RunDueChannelAutoPriority(context.Background(), now)

	require.Len(t, results, 2)
	var reloadedEnabled, reloadedDisabled model.Channel
	require.NoError(t, model.DB.First(&reloadedEnabled, enabled.Id).Error)
	require.NoError(t, model.DB.First(&reloadedDisabled, manuallyDisabled.Id).Error)
	assert.Equal(t, int64(-5), reloadedEnabled.GetPriority())
	assert.Less(t, reloadedDisabled.GetPriority(), reloadedEnabled.GetPriority())
}

func TestRunDueChannelAutoPriorityKeepsManualBelowNonAutoPriorityNegativePeer(t *testing.T) {
	setupUpstreamSourceAutoPriorityTestDB(t)
	now := int64(10_580_000)
	autoPriorityEnabled := createAutoPriorityTestChannel(t, "competitive enabled", 100, dto.ChannelOtherSettings{
		ChannelAutoPriorityEnabled:        true,
		ChannelAutoPriorityWindowHours:    24,
		ChannelAutoPriorityRateMultiplier: 1,
	})
	nonAutoPriorityPeer := createAutoPriorityTestChannel(t, "non AP negative peer", -100, dto.ChannelOtherSettings{
		ChannelAutoPriorityEnabled: false,
	})
	manuallyDisabled := createAutoPriorityTestChannel(t, "manual below every enabled peer", 5_000, dto.ChannelOtherSettings{
		ChannelAutoPriorityEnabled:        true,
		ChannelAutoPriorityWindowHours:    24,
		ChannelAutoPriorityRateMultiplier: 1,
	})
	require.NoError(t, model.DB.Model(&model.Channel{}).
		Where("id = ?", manuallyDisabled.Id).
		Update("status", common.ChannelStatusManuallyDisabled).Error)
	createAutoPriorityTestUsageLog(t, autoPriorityEnabled.Id, now-60)
	createAutoPriorityTestMonitorLog(t, autoPriorityEnabled.Id, now-60)

	results := RunDueChannelAutoPriority(context.Background(), now)

	var reloadedDisabled, reloadedNonAP model.Channel
	require.NoError(t, model.DB.First(&reloadedDisabled, manuallyDisabled.Id).Error)
	require.NoError(t, model.DB.First(&reloadedNonAP, nonAutoPriorityPeer.Id).Error)
	assert.Equal(t, int64(-100), reloadedNonAP.GetPriority())
	assert.Less(t, reloadedDisabled.GetPriority(), reloadedNonAP.GetPriority())
	assert.Nil(t, reloadedDisabled.GetOtherSettings().ChannelAutoPriorityLastScore)
	var disabledResult *ChannelAutoPriorityRunResult
	for i := range results {
		if results[i].ChannelID == manuallyDisabled.Id {
			disabledResult = &results[i]
			break
		}
	}
	require.NotNil(t, disabledResult)
	assert.Equal(t, reloadedDisabled.GetPriority(), disabledResult.score.NewPriority)
}

func TestRunChannelAutoPriorityGroupSinksManuallyDisabledOnlyGroup(t *testing.T) {
	setupUpstreamSourceAutoPriorityTestDB(t)
	now := int64(10_590_000)

	manuallyDisabled := createAutoPriorityTestChannel(t, "disabled only group", 5_000, dto.ChannelOtherSettings{
		ChannelAutoPriorityEnabled:         true,
		ChannelAutoPriorityIntervalMinutes: 0,
		ChannelAutoPriorityWindowHours:     24,
		ChannelAutoPriorityRateMultiplier:  1,
	})
	require.NoError(t, model.DB.Model(&model.Channel{}).
		Where("id = ?", manuallyDisabled.Id).
		Update("status", common.ChannelStatusManuallyDisabled).Error)

	results, err := RunChannelAutoPriorityGroup(context.Background(), manuallyDisabled.Id, now)

	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, manuallyDisabled.Id, results[0].ChannelID)
	assert.True(t, results[0].Applied)
	assert.Equal(t, "manually_disabled_sunk", results[0].Reason)
	var reloaded model.Channel
	require.NoError(t, model.DB.First(&reloaded, manuallyDisabled.Id).Error)
	assert.Equal(t, int64(-1), reloaded.GetPriority())
	var reloadedAbility model.Ability
	require.NoError(t, model.DB.Where("channel_id = ?", manuallyDisabled.Id).First(&reloadedAbility).Error)
	require.NotNil(t, reloadedAbility.Priority)
	assert.Equal(t, int64(-1), *reloadedAbility.Priority)
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
	unrelatedDisabled := createAutoPriorityTestChannel(t, "force unrelated disabled", 813, dto.ChannelOtherSettings{
		ChannelAutoPriorityEnabled:   false,
		ChannelAutoPriorityLastRunAt: now - 60,
		ChannelAutoPriorityLastScore: &dto.ChannelAutoPriorityScore{
			ComputedAt:  now - 60,
			FinalScore:  81.3,
			NewPriority: 813,
		},
	})
	require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", unrelatedDisabled.Id).Updates(map[string]any{
		"group":  "other",
		"status": common.ChannelStatusManuallyDisabled,
	}).Error)
	require.NoError(t, model.DB.Model(&model.Ability{}).Where("channel_id = ?", unrelatedDisabled.Id).Update("group", "other").Error)

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
	var reloadedUnrelatedDisabled model.Channel
	require.NoError(t, model.DB.First(&reloadedUnrelatedDisabled, unrelatedDisabled.Id).Error)
	assert.Equal(t, int64(813), reloadedUnrelatedDisabled.GetPriority())
	require.NotNil(t, reloadedUnrelatedDisabled.GetOtherSettings().ChannelAutoPriorityLastScore)
}

func TestPersistChannelAutoPriorityGroupRollsBackEveryMemberOnConflict(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(t *testing.T, channel model.Channel)
	}{
		{
			name: "settings changed",
			mutate: func(t *testing.T, channel model.Channel) {
				settings := channel.GetOtherSettings()
				settings.ChannelAutoPriorityRateMultiplier = 2
				channel.SetOtherSettings(settings)
				require.NoError(t, model.DB.Model(&model.Channel{}).
					Where("id = ?", channel.Id).
					Update("settings", channel.OtherSettings).Error)
			},
		},
		{
			name: "status changed",
			mutate: func(t *testing.T, channel model.Channel) {
				require.NoError(t, model.DB.Model(&model.Channel{}).
					Where("id = ?", channel.Id).
					Update("status", common.ChannelStatusAutoDisabled).Error)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
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
			tt.mutate(t, second)

			reason, err := persistChannelAutoPriorityGroup(
				context.Background(),
				candidates,
				scores,
				[]int{0, 1},
				nil,
				channelAutoPriorityDefaultSinkPriority,
				now,
			)

			require.NoError(t, err)
			assert.Equal(t, "generated_channel_changed", reason)
			var reloadedFirst model.Channel
			require.NoError(t, model.DB.First(&reloadedFirst, first.Id).Error)
			assert.Equal(t, int64(100), reloadedFirst.GetPriority())
			assert.Zero(t, reloadedFirst.GetOtherSettings().ChannelAutoPriorityLastRunAt)
		})
	}
}

func TestRunDueChannelAutoPriorityReportsManualCleanupOnceWhenGroupPersistFails(t *testing.T) {
	setupUpstreamSourceAutoPriorityTestDB(t)
	now := int64(10_950_000)
	enabled := createAutoPriorityTestChannel(t, "persist failure enabled", 100, dto.ChannelOtherSettings{
		ChannelAutoPriorityEnabled:        true,
		ChannelAutoPriorityWindowHours:    24,
		ChannelAutoPriorityRateMultiplier: 1,
	})
	manuallyDisabled := createAutoPriorityTestChannel(t, "persist failure disabled", 813, dto.ChannelOtherSettings{
		ChannelAutoPriorityEnabled:   true,
		ChannelAutoPriorityLastRunAt: now - 60,
		ChannelAutoPriorityLastScore: &dto.ChannelAutoPriorityScore{
			ComputedAt:  now - 60,
			FinalScore:  81.3,
			NewPriority: 813,
		},
	})
	require.NoError(t, model.DB.Model(&model.Channel{}).
		Where("id = ?", manuallyDisabled.Id).
		Update("status", common.ChannelStatusManuallyDisabled).Error)
	createAutoPriorityTestUsageLog(t, enabled.Id, now-60)
	createAutoPriorityTestMonitorLog(t, enabled.Id, now-60)
	callbackName := fmt.Sprintf("fail_auto_priority_persist_%s", t.Name())
	require.NoError(t, model.DB.Callback().Update().Before("gorm:update").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Schema == nil || tx.Statement.Schema.Name != "Channel" {
			return
		}
		updates, ok := tx.Statement.Dest.(map[string]any)
		if !ok {
			return
		}
		settings, ok := updates["settings"].(string)
		if ok && strings.Contains(settings, "channel_auto_priority_last_score") {
			tx.AddError(errors.New("forced auto-priority persist failure"))
		}
	}))
	t.Cleanup(func() {
		require.NoError(t, model.DB.Callback().Update().Remove(callbackName))
	})

	results := RunDueChannelAutoPriority(context.Background(), now)

	require.Len(t, results, 2)
	resultCount := map[int]int{}
	for _, result := range results {
		resultCount[result.ChannelID]++
	}
	assert.Equal(t, 1, resultCount[enabled.Id])
	assert.Equal(t, 1, resultCount[manuallyDisabled.Id])
	for _, result := range results {
		if result.ChannelID == manuallyDisabled.Id {
			assert.True(t, result.Applied)
			assert.Equal(t, "manually_disabled_sunk", result.Reason)
		}
	}
}
