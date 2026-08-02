package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"

	"github.com/glebarez/sqlite"
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

func TestPersistChannelAutoPriorityGroupPersistsRankReconciledRows(t *testing.T) {
	setupUpstreamSourceAutoPriorityTestDB(t)
	now := int64(10_005_000)
	windowStart := now - 24*3600

	channel175 := createAutoPriorityTestChannel(t, "production channel 175", 371, dto.ChannelOtherSettings{
		ChannelAutoPriorityEnabled:         true,
		ChannelAutoPriorityIntervalMinutes: 0,
		ChannelAutoPriorityWindowHours:     24,
		ChannelAutoPriorityRateMultiplier:  0.060,
		ChannelAutoPriorityLastScore:       &dto.ChannelAutoPriorityScore{NewPriority: 371},
	})
	channel150 := createAutoPriorityTestChannel(t, "production channel 150", 367, dto.ChannelOtherSettings{
		ChannelAutoPriorityEnabled:         true,
		ChannelAutoPriorityIntervalMinutes: 0,
		ChannelAutoPriorityWindowHours:     24,
		ChannelAutoPriorityRateMultiplier:  0.045,
		ChannelAutoPriorityLastScore:       &dto.ChannelAutoPriorityScore{NewPriority: 367},
	})
	scoreInputs := []AutoPriorityScoreInput{
		{
			ChannelID:               channel175.Id,
			LocalGroup:              channel175.Group,
			ChannelType:             channel175.Type,
			CurrentPriority:         371,
			EffectiveRateMultiplier: 0.060,
			CacheAdjustedCostFactor: 0.43688438383406902,
			CohortCostFloor:         0.01,
			Availability:            floatPtr(1),
			MonitorCheckCount:       3,
			UsageLogCount:           autoPriorityFullCacheSampleCount,
			FirstTokenLatencyMS:     autoPriorityFirstTokenFastMS,
			FirstTokenSampleCount:   1,
			ThroughputTps:           autoPriorityThroughputFastTps,
			ThroughputSampleCount:   1,
			HasPreviousSnapshot:     true,
		},
		{
			ChannelID:               channel150.Id,
			LocalGroup:              channel150.Group,
			ChannelType:             channel150.Type,
			CurrentPriority:         367,
			EffectiveRateMultiplier: 0.045,
			CacheAdjustedCostFactor: 0.63338533554855192,
			CohortCostFloor:         0.01,
			Availability:            floatPtr(1),
			MonitorCheckCount:       3,
			UsageLogCount:           autoPriorityFullCacheSampleCount,
			FirstTokenLatencyMS:     autoPriorityFirstTokenFastMS,
			FirstTokenSampleCount:   1,
			ThroughputTps:           autoPriorityThroughputFastTps,
			ThroughputSampleCount:   1,
			HasPreviousSnapshot:     true,
		},
	}
	scores := ScoreAutoPriorityCandidates(scoreInputs, 1000)
	require.Len(t, scores, 2)
	assert.Equal(t, int64(362), scores[0].ComputedPriority)
	assert.Equal(t, int64(373), scores[1].ComputedPriority)
	for _, score := range scores {
		assert.True(t, score.Applied)
		assert.Equal(t, score.ComputedPriority, score.NewPriority)
		assert.Empty(t, score.Reason)
	}

	resolution := upstreamSourceRuleResolution{
		AutoPriorityEnabled:                 true,
		AutoPriorityIntervalMinutes:         0,
		AutoPriorityWindowHours:             24,
		AutoPriorityAvailabilityWindowHours: 24,
	}
	candidates := []upstreamSourceAutoPriorityCandidate{
		{
			channel:     channel175,
			settings:    channel175.GetOtherSettings(),
			resolution:  resolution,
			scoreInput:  scoreInputs[0],
			windowStart: windowStart,
			windowEnd:   now,
		},
		{
			channel:     channel150,
			settings:    channel150.GetOtherSettings(),
			resolution:  resolution,
			scoreInput:  scoreInputs[1],
			windowStart: windowStart,
			windowEnd:   now,
		},
	}

	reason, _, err := persistChannelAutoPriorityGroup(
		context.Background(),
		candidates,
		scores,
		[]int{0, 1},
		nil,
		channelAutoPriorityDefaultSinkPriority,
		now,
	)
	require.NoError(t, err)
	assert.Empty(t, reason)

	for idx, channel := range []model.Channel{channel175, channel150} {
		var reloaded model.Channel
		require.NoError(t, model.DB.First(&reloaded, channel.Id).Error)
		assert.Equal(t, scores[idx].NewPriority, reloaded.GetPriority())

		var ability model.Ability
		require.NoError(t, model.DB.Where("channel_id = ?", channel.Id).First(&ability).Error)
		require.NotNil(t, ability.Priority)
		assert.Equal(t, reloaded.GetPriority(), *ability.Priority)

		settings := reloaded.GetOtherSettings()
		assert.Equal(t, now, settings.ChannelAutoPriorityLastAppliedAt)
		require.NotNil(t, settings.ChannelAutoPriorityLastScore)
		assert.Equal(t, reloaded.GetPriority(), settings.ChannelAutoPriorityLastScore.NewPriority)
		assert.True(t, settings.ChannelAutoPriorityLastScore.Applied)
		assert.Empty(t, settings.ChannelAutoPriorityLastScore.Reason)
	}
}

func TestRunDueChannelAutoPriorityPersistsFiniteSnapshotForExtremeEffectiveCost(t *testing.T) {
	setupUpstreamSourceAutoPriorityTestDB(t)
	now := int64(10_010_000)

	channel := createAutoPriorityTestChannel(t, "finite effective cost", 100, dto.ChannelOtherSettings{
		ChannelAutoPriorityEnabled:         true,
		ChannelAutoPriorityIntervalMinutes: 0,
		ChannelAutoPriorityWindowHours:     24,
		ChannelAutoPriorityRateMultiplier:  math.MaxFloat64,
	})
	other, err := common.Marshal(map[string]any{
		"input_tokens_total":    1,
		"cache_creation_tokens": 1,
		"cache_creation_ratio":  3,
		"completion_ratio":      1,
		"is_channel_test":       false,
	})
	require.NoError(t, err)
	logs := make([]model.Log, 0, autoPriorityFullCacheSampleCount)
	for i := int64(0); i < autoPriorityFullCacheSampleCount; i++ {
		logs = append(logs, model.Log{
			Type:         model.LogTypeConsume,
			CreatedAt:    now - 60 - i,
			ChannelId:    channel.Id,
			PromptTokens: 1,
			Other:        string(other),
		})
	}
	require.NoError(t, model.LOG_DB.Create(&logs).Error)

	results := RunDueChannelAutoPriority(context.Background(), now)

	require.Len(t, results, 1)
	assert.NotEqual(t, "update_failed", results[0].Reason)
	assert.NotEqual(t, "update_failed", results[0].score.Reason)
	assert.True(t, results[0].Applied)
	assert.GreaterOrEqual(t, results[0].score.NewPriority, int64(0))
	assert.LessOrEqual(t, results[0].score.NewPriority, int64(1000))

	var reloaded model.Channel
	require.NoError(t, model.DB.First(&reloaded, channel.Id).Error)
	settings := reloaded.GetOtherSettings()
	assert.Equal(t, now, settings.ChannelAutoPriorityLastRunAt)
	require.NotNil(t, settings.ChannelAutoPriorityLastScore)
	snapshot := settings.ChannelAutoPriorityLastScore
	assert.Equal(t, "v5", snapshot.Version)
	assert.Equal(t, 3.0, snapshot.CacheAdjustedCostFactor)
	assert.Equal(t, math.MaxFloat64, snapshot.EffectiveCostMultiplier)
	assert.False(t, math.IsNaN(snapshot.EffectiveCostMultiplier))
	assert.False(t, math.IsInf(snapshot.EffectiveCostMultiplier, 0))
	_, err = common.Marshal(snapshot)
	require.NoError(t, err)
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
		assert.Equal(t, "v5", snapshot.Version)
		assert.Equal(t, now, snapshot.ComputedAt)
		assert.InDelta(t, 0.02, snapshot.CohortFloor, 1e-12)
		assert.InDelta(t, 0.08, snapshot.CohortCeil, 1e-12)
		assert.Equal(t, len(members), snapshot.CohortMemberCount)
		assert.InDelta(t, 0.02, snapshot.OrdinaryPriceFloor, 1e-12)
		assert.Equal(t, "cohort_floor", snapshot.EffectivePriceFloorSource)
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
	assert.Equal(t, "v5", staleSettings.ChannelAutoPriorityLastScore.Version)
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

func createEmpiricalAutoPriorityProbe(
	t *testing.T,
	source model.UpstreamSource,
	mapping model.UpstreamSourceChannelMapping,
	channel model.Channel,
	now int64,
	resolvedRate float64,
	configure func(*model.UpstreamSourceBillingProbe),
) (model.UpstreamSourceChannelMapping, model.Channel, model.UpstreamSourceBillingProbe) {
	t.Helper()
	require.NoError(t, model.DB.Model(&model.UpstreamSourceChannelMapping{}).
		Where("id = ?", mapping.Id).
		Updates(map[string]any{
			"auto_priority_cost_source": model.UpstreamSourceAutoPriorityCostSourceEmpiricalProbe,
			"effective_rate_multiplier": nil,
			"upstream_key_id":           "empirical-key-id",
		}).Error)
	require.NoError(t, model.DB.Model(&model.Channel{}).
		Where("id = ?", channel.Id).
		Update("base_url", source.RelayBaseURL).Error)
	require.NoError(t, model.DB.First(&mapping, mapping.Id).Error)
	require.NoError(t, model.DB.First(&channel, channel.Id).Error)
	groupRate := resolvedRate
	effectiveDiagnostic := resolvedRate
	peakEnabled := false
	probe := model.UpstreamSourceBillingProbe{
		SourceID:                source.Id,
		MappingID:               mapping.Id,
		Enabled:                 true,
		IntervalMinutes:         5,
		Status:                  model.UpstreamSourceBillingProbeStatusOK,
		GroupRateMultiplier:     &groupRate,
		ResolvedRateMultiplier:  &resolvedRate,
		PeakRateEnabled:         &peakEnabled,
		EffectiveRateMultiplier: &effectiveDiagnostic,
		ObservedAt:              time.Unix(now-60, 0).UTC().Format(time.RFC3339Nano),
		ReceivedAt:              now - 60,
		FreshUntil:              now + 3600,
		LastGoodGeneration:      4,
	}
	if configure != nil {
		configure(&probe)
	}
	identity, identityErr := upstreamSourceBillingProbeIdentityFromRows(&probe, &source, &mapping, &channel)
	require.Nil(t, identityErr)
	probe.LastGoodIdentityFingerprint = identity.Fingerprint
	require.NoError(t, model.DB.Create(&probe).Error)
	return mapping, channel, probe
}

func TestRunDueChannelAutoPriorityUsesSelectedEmpiricalCostWithoutAdvertisedRate(t *testing.T) {
	setupUpstreamSourceAutoPriorityTestDB(t)
	now := int64(10_327_000)
	source := createSyncTestSource(t, map[string]any{
		"auto_priority_enabled":          true,
		"auto_priority_interval_minutes": 0,
		"auto_priority_window_hours":     24,
	})
	generated, mapping := createGeneratedAutoPriorityTestChannel(t, source.Id, 0.5, "empirical", 100)
	manual := createAutoPriorityTestChannel(t, "manual peer", 100, dto.ChannelOtherSettings{
		ChannelAutoPriorityEnabled:         true,
		ChannelAutoPriorityIntervalMinutes: 0,
		ChannelAutoPriorityWindowHours:     24,
		ChannelAutoPriorityRateMultiplier:  0.1,
	})
	resolvedRate := 0.05
	mapping, generated, _ = createEmpiricalAutoPriorityProbe(t, source, mapping, generated, now, resolvedRate, nil)
	for _, channel := range []model.Channel{generated, manual} {
		createAutoPriorityTestUsageLog(t, channel.Id, now-60)
		createAutoPriorityTestMonitorLog(t, channel.Id, now-60)
	}

	results := RunDueChannelAutoPriority(context.Background(), now)

	require.Len(t, results, 2)
	var reloaded model.Channel
	require.NoError(t, model.DB.First(&reloaded, generated.Id).Error)
	snapshot := reloaded.GetOtherSettings().ChannelAutoPriorityLastScore
	require.NotNil(t, snapshot)
	assert.Equal(t, resolvedRate, snapshot.NominalRateMultiplier)
	assert.Equal(t, resolvedRate, snapshot.EffectiveRateMultiplier)
	assert.Equal(t, resolvedRate, snapshot.CohortFloor)
	assert.Equal(t, 0.1, snapshot.CohortCeil)
	assert.Equal(t, time.Unix(now, 0).Unix(), snapshot.ComputedAt)
}

func TestRunChannelAutoPriorityGroupsForSourceResolvesSelectedEmpiricalCostBeforeAdvertisedRate(t *testing.T) {
	setupUpstreamSourceAutoPriorityTestDB(t)
	now := int64(10_328_000)
	source := createSyncTestSource(t, map[string]any{
		"auto_priority_enabled":          true,
		"auto_priority_interval_minutes": 60,
		"auto_priority_window_hours":     24,
	})
	generated, mapping := createGeneratedAutoPriorityTestChannel(t, source.Id, 0.5, "empirical-force", 100)
	manual := createAutoPriorityTestChannel(t, "manual force peer", 100, dto.ChannelOtherSettings{
		ChannelAutoPriorityEnabled:         true,
		ChannelAutoPriorityIntervalMinutes: 60,
		ChannelAutoPriorityWindowHours:     24,
		ChannelAutoPriorityRateMultiplier:  0.1,
	})
	resolvedRate := 0.05
	_, generated, _ = createEmpiricalAutoPriorityProbe(t, source, mapping, generated, now, resolvedRate, nil)
	for _, channel := range []model.Channel{generated, manual} {
		createAutoPriorityTestUsageLog(t, channel.Id, now-60)
		createAutoPriorityTestMonitorLog(t, channel.Id, now-60)
	}

	result, err := RunChannelAutoPriorityGroupsForSource(context.Background(), source.Id, now)

	require.NoError(t, err)
	require.Len(t, result.Results, 2)
	var reloaded model.Channel
	require.NoError(t, model.DB.First(&reloaded, generated.Id).Error)
	snapshot := reloaded.GetOtherSettings().ChannelAutoPriorityLastScore
	require.NotNil(t, snapshot)
	assert.Equal(t, resolvedRate, snapshot.NominalRateMultiplier)
	assert.Equal(t, resolvedRate, snapshot.CohortFloor)
	assert.Equal(t, 0.1, snapshot.CohortCeil)
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

func TestRunDueChannelAutoPriorityFailsClosedWhenMappingOwnerHasZeroGeneratedSettings(t *testing.T) {
	setupUpstreamSourceAutoPriorityTestDB(t)
	now := int64(10_352_000)
	source := createSyncTestSource(t, map[string]any{
		"auto_priority_enabled":          true,
		"auto_priority_interval_minutes": 0,
	})
	generated, _ := createGeneratedAutoPriorityTestChannel(t, source.Id, 0.5, "zero-generated-settings", 100)
	settings := generated.GetOtherSettings()
	settings.GeneratedByUpstreamSourceID = 0
	settings.GeneratedByUpstreamMappingID = 0
	generated.SetOtherSettings(settings)
	require.NoError(t, model.DB.Model(&model.Channel{}).
		Where("id = ?", generated.Id).
		Update("settings", generated.OtherSettings).Error)
	manual := createAutoPriorityTestChannel(t, "zero generated settings manual peer", 100, dto.ChannelOtherSettings{
		ChannelAutoPriorityEnabled:         true,
		ChannelAutoPriorityIntervalMinutes: 0,
		ChannelAutoPriorityWindowHours:     24,
		ChannelAutoPriorityRateMultiplier:  0.1,
	})

	results := RunDueChannelAutoPriority(context.Background(), now)

	require.Len(t, results, 2)
	for _, result := range results {
		assert.False(t, result.Applied)
		assert.Equal(t, "generated_channel_metadata_mismatch", result.Reason)
	}
	for _, channel := range []model.Channel{generated, manual} {
		var persisted model.Channel
		require.NoError(t, model.DB.First(&persisted, channel.Id).Error)
		assert.Equal(t, int64(100), persisted.GetPriority())
		assert.Zero(t, persisted.GetOtherSettings().ChannelAutoPriorityLastRunAt)
		assert.Nil(t, persisted.GetOtherSettings().ChannelAutoPriorityLastScore)
		var ability model.Ability
		require.NoError(t, model.DB.Where("channel_id = ?", channel.Id).First(&ability).Error)
		require.NotNil(t, ability.Priority)
		assert.Equal(t, int64(100), *ability.Priority)
	}
}

func TestRunDueChannelAutoPriorityRejectsUnsafeEmpiricalStateForWholeExactGroup(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(t *testing.T, now int64, channel model.Channel, probe model.UpstreamSourceBillingProbe)
		wantReason string
	}{
		{
			name: "disabled",
			mutate: func(t *testing.T, _ int64, _ model.Channel, probe model.UpstreamSourceBillingProbe) {
				require.NoError(t, model.DB.Model(&model.UpstreamSourceBillingProbe{}).
					Where("mapping_id = ?", probe.MappingID).Update("enabled", false).Error)
			},
			wantReason: "empirical_probe_disabled",
		},
		{
			name: "missing",
			mutate: func(t *testing.T, _ int64, _ model.Channel, probe model.UpstreamSourceBillingProbe) {
				require.NoError(t, model.DB.Where("mapping_id = ?", probe.MappingID).
					Delete(&model.UpstreamSourceBillingProbe{}).Error)
			},
			wantReason: "empirical_probe_missing",
		},
		{
			name: "stale",
			mutate: func(t *testing.T, now int64, _ model.Channel, probe model.UpstreamSourceBillingProbe) {
				require.NoError(t, model.DB.Model(&model.UpstreamSourceBillingProbe{}).
					Where("mapping_id = ?", probe.MappingID).Update("fresh_until", now).Error)
			},
			wantReason: "empirical_probe_stale",
		},
		{
			name: "unsupported",
			mutate: func(t *testing.T, _ int64, _ model.Channel, probe model.UpstreamSourceBillingProbe) {
				require.NoError(t, model.DB.Model(&model.UpstreamSourceBillingProbe{}).
					Where("mapping_id = ?", probe.MappingID).
					Update("status", model.UpstreamSourceBillingProbeStatusUnsupported).Error)
			},
			wantReason: "empirical_probe_unsupported",
		},
		{
			name: "malformed",
			mutate: func(t *testing.T, _ int64, _ model.Channel, probe model.UpstreamSourceBillingProbe) {
				require.NoError(t, model.DB.Model(&model.UpstreamSourceBillingProbe{}).
					Where("mapping_id = ?", probe.MappingID).Update("effective_rate_multiplier", nil).Error)
			},
			wantReason: "empirical_probe_malformed",
		},
		{
			name: "identity mismatch",
			mutate: func(t *testing.T, _ int64, channel model.Channel, _ model.UpstreamSourceBillingProbe) {
				require.NoError(t, model.DB.Model(&model.Channel{}).
					Where("id = ?", channel.Id).Update("key", "sk-identity-changed").Error)
			},
			wantReason: "empirical_probe_identity_mismatch",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setupUpstreamSourceAutoPriorityTestDB(t)
			now := int64(10_355_000)
			source := createSyncTestSource(t, map[string]any{
				"auto_priority_enabled":          true,
				"auto_priority_interval_minutes": 0,
			})
			generated, mapping := createGeneratedAutoPriorityTestChannel(t, source.Id, 0.5, "unsafe-empirical", 100)
			manual := createAutoPriorityTestChannel(t, "unsafe empirical manual peer", 100, dto.ChannelOtherSettings{
				ChannelAutoPriorityEnabled:         true,
				ChannelAutoPriorityIntervalMinutes: 0,
				ChannelAutoPriorityWindowHours:     24,
				ChannelAutoPriorityRateMultiplier:  0.1,
			})
			_, generated, probe := createEmpiricalAutoPriorityProbe(t, source, mapping, generated, now, 0.05, nil)
			tt.mutate(t, now, generated, probe)

			results := RunDueChannelAutoPriority(context.Background(), now)

			require.Len(t, results, 2)
			for _, result := range results {
				assert.False(t, result.Applied)
				assert.Equal(t, tt.wantReason, result.Reason)
			}
			for _, channel := range []model.Channel{generated, manual} {
				var reloaded model.Channel
				require.NoError(t, model.DB.First(&reloaded, channel.Id).Error)
				assert.Equal(t, int64(100), reloaded.GetPriority())
				assert.Zero(t, reloaded.GetOtherSettings().ChannelAutoPriorityLastRunAt)
				assert.Nil(t, reloaded.GetOtherSettings().ChannelAutoPriorityLastScore)
			}
		})
	}
}

func TestRunDueChannelAutoPriorityRejectsUnsafeEmpiricalStateBeforeManualDisabledSweep(t *testing.T) {
	setupUpstreamSourceAutoPriorityTestDB(t)
	now := int64(10_357_000)
	source := createSyncTestSource(t, map[string]any{
		"auto_priority_enabled":          true,
		"auto_priority_interval_minutes": 0,
	})
	generated, mapping := createGeneratedAutoPriorityTestChannel(t, source.Id, 0.5, "unsafe-before-manual-sweep", 100)
	_, _, probe := createEmpiricalAutoPriorityProbe(t, source, mapping, generated, now, 0.05, nil)
	require.NoError(t, model.DB.Model(&model.UpstreamSourceBillingProbe{}).
		Where("mapping_id = ?", probe.MappingID).
		Update("enabled", false).Error)

	manuallyDisabled := createAutoPriorityTestChannel(t, "unsafe empirical manual disabled peer", 813, dto.ChannelOtherSettings{
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

	results := RunDueChannelAutoPriority(context.Background(), now)

	require.Len(t, results, 2)
	for _, result := range results {
		assert.False(t, result.Applied)
		assert.Equal(t, "empirical_probe_disabled", result.Reason)
	}
	var persisted model.Channel
	require.NoError(t, model.DB.First(&persisted, manuallyDisabled.Id).Error)
	assert.Equal(t, int64(813), persisted.GetPriority())
	persistedSettings := persisted.GetOtherSettings()
	assert.Equal(t, now-60, persistedSettings.ChannelAutoPriorityLastRunAt)
	require.NotNil(t, persistedSettings.ChannelAutoPriorityLastScore)
	assert.Equal(t, int64(813), persistedSettings.ChannelAutoPriorityLastScore.NewPriority)
	var ability model.Ability
	require.NoError(t, model.DB.Where("channel_id = ?", manuallyDisabled.Id).First(&ability).Error)
	require.NotNil(t, ability.Priority)
	assert.Equal(t, int64(813), *ability.Priority)
}

func TestRunDueChannelAutoPriorityRecomputesEmpiricalPeakAtEachScorerClock(t *testing.T) {
	setupUpstreamSourceAutoPriorityTestDB(t)
	observationTime := time.Date(2026, time.August, 1, 12, 0, 0, 0, time.UTC)
	source := createSyncTestSource(t, map[string]any{
		"auto_priority_enabled":          true,
		"auto_priority_interval_minutes": 0,
		"auto_priority_window_hours":     24,
	})
	generated, mapping := createGeneratedAutoPriorityTestChannel(t, source.Id, 0.5, "peak-empirical", 100)
	manual := createAutoPriorityTestChannel(t, "peak empirical manual peer", 100, dto.ChannelOtherSettings{
		ChannelAutoPriorityEnabled:         true,
		ChannelAutoPriorityIntervalMinutes: 0,
		ChannelAutoPriorityWindowHours:     24,
		ChannelAutoPriorityRateMultiplier:  0.2,
	})
	_, generated, probe := createEmpiricalAutoPriorityProbe(
		t,
		source,
		mapping,
		generated,
		observationTime.Unix(),
		0.05,
		func(probe *model.UpstreamSourceBillingProbe) {
			peakEnabled := true
			peakFactor := 2.0
			storedAppliedDiagnostic := 1.0
			storedEffectiveDiagnostic := 0.05
			probe.PeakRateEnabled = &peakEnabled
			probe.PeakStart = "09:00"
			probe.PeakEnd = "17:00"
			probe.PeakRateMultiplier = &peakFactor
			probe.AppliedPeakMultiplier = &storedAppliedDiagnostic
			probe.EffectiveRateMultiplier = &storedEffectiveDiagnostic
			probe.Timezone = "America/New_York"
			probe.FreshUntil = observationTime.Add(24 * time.Hour).Unix()
		},
	)
	for _, channel := range []model.Channel{generated, manual} {
		createAutoPriorityTestUsageLog(t, channel.Id, observationTime.Add(-time.Minute).Unix())
		createAutoPriorityTestMonitorLog(t, channel.Id, observationTime.Add(-time.Minute).Unix())
	}

	tests := []struct {
		name string
		now  time.Time
		want float64
	}{
		{name: "pre peak", now: time.Date(2026, time.August, 1, 12, 59, 0, 0, time.UTC), want: 0.05},
		{name: "in peak", now: time.Date(2026, time.August, 1, 13, 0, 0, 0, time.UTC), want: 0.10},
		{name: "post peak", now: time.Date(2026, time.August, 1, 21, 0, 0, 0, time.UTC), want: 0.05},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results, err := RunChannelAutoPriorityGroup(context.Background(), generated.Id, tt.now.Unix())
			require.NoError(t, err)
			require.Len(t, results, 2)
			var reloaded model.Channel
			require.NoError(t, model.DB.First(&reloaded, generated.Id).Error)
			snapshot := reloaded.GetOtherSettings().ChannelAutoPriorityLastScore
			require.NotNil(t, snapshot)
			assert.InDelta(t, tt.want, snapshot.NominalRateMultiplier, 1e-12)
			assert.InDelta(t, tt.want, snapshot.CohortFloor, 1e-12)
			assert.InDelta(t, 0.2, snapshot.CohortCeil, 1e-12)
		})
	}

	var stored model.UpstreamSourceBillingProbe
	require.NoError(t, model.DB.First(&stored, probe.Id).Error)
	require.NotNil(t, stored.AppliedPeakMultiplier)
	require.NotNil(t, stored.EffectiveRateMultiplier)
	assert.Equal(t, 1.0, *stored.AppliedPeakMultiplier)
	assert.Equal(t, 0.05, *stored.EffectiveRateMultiplier)
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

	ordinaryExpensive := createAutoPriorityTestChannel(t, "enabled expensive peer", 100, dto.ChannelOtherSettings{
		ChannelAutoPriorityEnabled:         true,
		ChannelAutoPriorityIntervalMinutes: 0,
		ChannelAutoPriorityWindowHours:     24,
		ChannelAutoPriorityRateMultiplier:  0.07,
	})
	ordinaryCheap := createAutoPriorityTestChannel(t, "enabled cheap peer", 100, dto.ChannelOtherSettings{
		ChannelAutoPriorityEnabled:         true,
		ChannelAutoPriorityIntervalMinutes: 0,
		ChannelAutoPriorityWindowHours:     24,
		ChannelAutoPriorityRateMultiplier:  0.04,
	})
	autoDisabled := createAutoPriorityTestChannel(t, "auto disabled extreme cheap", 5_000, dto.ChannelOtherSettings{
		ChannelAutoPriorityEnabled:        true,
		ChannelAutoPriorityWindowHours:    24,
		ChannelAutoPriorityRateMultiplier: 0.001,
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
	createAutoPriorityTestUsageLog(t, ordinaryExpensive.Id, now-60)
	createAutoPriorityTestMonitorLog(t, ordinaryExpensive.Id, now-60)
	createAutoPriorityTestUsageLog(t, ordinaryCheap.Id, now-60)
	createAutoPriorityTestMonitorLog(t, ordinaryCheap.Id, now-60)

	results := RunDueChannelAutoPriority(context.Background(), now)

	require.Len(t, results, 3)
	resultCount := make(map[int]int, len(results))
	for _, result := range results {
		resultCount[result.ChannelID]++
	}
	assert.Equal(t, 1, resultCount[ordinaryExpensive.Id])
	assert.Equal(t, 1, resultCount[ordinaryCheap.Id])
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
	assert.Equal(t, "v5", settings.ChannelAutoPriorityLastScore.Version)
	assert.Equal(t, 0.0, settings.ChannelAutoPriorityLastScore.FinalScore)
	assert.Equal(t, int64(5_000), settings.ChannelAutoPriorityLastScore.NewPriority)
	assert.False(t, settings.ChannelAutoPriorityLastScore.Applied)
	assert.Equal(t, "channel_auto_disabled", settings.ChannelAutoPriorityLastScore.Reason)
	assert.InDelta(t, 0.001, settings.ChannelAutoPriorityLastScore.CohortFloor, 1e-12)
	assert.InDelta(t, 0.04, settings.ChannelAutoPriorityLastScore.OrdinaryPriceFloor, 1e-12)
	assert.Equal(t, "ordinary_band", settings.ChannelAutoPriorityLastScore.EffectivePriceFloorSource)
	assert.Equal(t, 3, settings.ChannelAutoPriorityLastScore.CohortMemberCount)

	var reloadedOrdinaryCheap, reloadedOrdinaryExpensive model.Channel
	require.NoError(t, model.DB.First(&reloadedOrdinaryCheap, ordinaryCheap.Id).Error)
	require.NoError(t, model.DB.First(&reloadedOrdinaryExpensive, ordinaryExpensive.Id).Error)
	ordinaryCheapScore := reloadedOrdinaryCheap.GetOtherSettings().ChannelAutoPriorityLastScore
	ordinaryExpensiveScore := reloadedOrdinaryExpensive.GetOtherSettings().ChannelAutoPriorityLastScore
	require.NotNil(t, ordinaryCheapScore)
	require.NotNil(t, ordinaryExpensiveScore)
	assert.InDelta(t, 100, ordinaryCheapScore.NominalPriceScore, 1e-9)
	assert.InDelta(t, 100*0.04/0.07, ordinaryExpensiveScore.NominalPriceScore, 1e-9)
	assert.Greater(t, reloadedOrdinaryCheap.GetPriority(), reloadedOrdinaryExpensive.GetPriority())
}

func TestRunDueChannelAutoPriorityPreservesAutoDisabledExpensiveSnapshotPriority(t *testing.T) {
	setupUpstreamSourceAutoPriorityTestDB(t)
	now := int64(10_560_000)

	autoDisabled := createAutoPriorityTestChannel(t, "auto disabled expensive peer", 5_000, dto.ChannelOtherSettings{
		ChannelAutoPriorityEnabled:         true,
		ChannelAutoPriorityIntervalMinutes: 0,
		ChannelAutoPriorityWindowHours:     24,
		ChannelAutoPriorityRateMultiplier:  0.08,
		ChannelAutoPriorityLastRunAt:       now - 120,
		ChannelAutoPriorityLastAppliedAt:   now - 180,
	})
	require.NoError(t, model.DB.Model(&model.Channel{}).
		Where("id = ?", autoDisabled.Id).
		Update("status", common.ChannelStatusAutoDisabled).Error)
	cheap := createAutoPriorityTestChannel(t, "enabled extreme cheap peer", 100, dto.ChannelOtherSettings{
		ChannelAutoPriorityEnabled:         true,
		ChannelAutoPriorityIntervalMinutes: 0,
		ChannelAutoPriorityWindowHours:     24,
		ChannelAutoPriorityRateMultiplier:  0.01,
	})
	usableExpensive := createAutoPriorityTestChannel(t, "enabled expensive comparison peer", 100, dto.ChannelOtherSettings{
		ChannelAutoPriorityEnabled:         true,
		ChannelAutoPriorityIntervalMinutes: 0,
		ChannelAutoPriorityWindowHours:     24,
		ChannelAutoPriorityRateMultiplier:  0.08,
	})
	createAutoPriorityTestUsageLog(t, cheap.Id, now-60)
	createAutoPriorityTestMonitorLog(t, cheap.Id, now-60)
	createAutoPriorityTestUsageLog(t, usableExpensive.Id, now-60)
	createAutoPriorityTestMonitorLog(t, usableExpensive.Id, now-60)

	results := RunDueChannelAutoPriority(context.Background(), now)

	require.Len(t, results, 3)
	var autoDisabledResult *ChannelAutoPriorityRunResult
	for i := range results {
		if results[i].ChannelID == autoDisabled.Id {
			autoDisabledResult = &results[i]
			break
		}
	}
	require.NotNil(t, autoDisabledResult)
	assert.False(t, autoDisabledResult.Applied)
	assert.Equal(t, "channel_auto_disabled", autoDisabledResult.score.Reason)
	assert.Equal(t, int64(0), autoDisabledResult.score.ComputedPriority)
	assert.Equal(t, int64(5_000), autoDisabledResult.score.NewPriority)

	var reloadedAutoDisabled, reloadedCheap, reloadedUsableExpensive model.Channel
	require.NoError(t, model.DB.First(&reloadedAutoDisabled, autoDisabled.Id).Error)
	require.NoError(t, model.DB.First(&reloadedCheap, cheap.Id).Error)
	require.NoError(t, model.DB.First(&reloadedUsableExpensive, usableExpensive.Id).Error)
	assert.Equal(t, int64(5_000), reloadedAutoDisabled.GetPriority())
	var ability model.Ability
	require.NoError(t, model.DB.Where("channel_id = ?", autoDisabled.Id).First(&ability).Error)
	require.NotNil(t, ability.Priority)
	assert.Equal(t, int64(5_000), *ability.Priority)
	snapshot := reloadedAutoDisabled.GetOtherSettings().ChannelAutoPriorityLastScore
	require.NotNil(t, snapshot)
	assert.Equal(t, "v5", snapshot.Version)
	assert.False(t, snapshot.Applied)
	assert.Equal(t, "channel_auto_disabled", snapshot.Reason)
	assert.Equal(t, int64(5_000), snapshot.NewPriority)
	assert.GreaterOrEqual(
		t,
		reloadedCheap.GetPriority()-reloadedUsableExpensive.GetPriority(),
		autoPriorityDominancePriorityMargin,
	)
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

			reason, _, err := persistChannelAutoPriorityGroup(
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

func TestPersistChannelAutoPriorityGroupRevalidatesEveryEmpiricalMemberBeforeAnyWrite(t *testing.T) {
	setupUpstreamSourceAutoPriorityTestDB(t)
	now := int64(10_910_000)
	source := createSyncTestSource(t, map[string]any{
		"auto_priority_enabled":      true,
		"auto_priority_window_hours": 24,
	})
	firstChannel, firstMapping := createGeneratedAutoPriorityTestChannel(t, source.Id, 0.5, "empirical-cas-first", 100)
	secondChannel, secondMapping := createGeneratedAutoPriorityTestChannel(t, source.Id, 0.5, "empirical-cas-second", 100)
	firstMapping, firstChannel, firstProbe := createEmpiricalAutoPriorityProbe(
		t,
		source,
		firstMapping,
		firstChannel,
		now,
		0.05,
		nil,
	)
	secondMapping, secondChannel, secondProbe := createEmpiricalAutoPriorityProbe(
		t,
		source,
		secondMapping,
		secondChannel,
		now,
		0.10,
		nil,
	)
	firstCost, firstReason := resolveUpstreamSourceAutoPriorityCost(
		time.Unix(now, 0),
		&source,
		&firstMapping,
		&firstChannel,
		&firstProbe,
	)
	require.Empty(t, firstReason)
	secondCost, secondReason := resolveUpstreamSourceAutoPriorityCost(
		time.Unix(now, 0),
		&source,
		&secondMapping,
		&secondChannel,
		&secondProbe,
	)
	require.Empty(t, secondReason)
	resolution := upstreamSourceRuleResolution{
		AutoPriorityEnabled:                 true,
		AutoPriorityWindowHours:             24,
		AutoPriorityAvailabilityWindowHours: 24,
	}
	candidates := []upstreamSourceAutoPriorityCandidate{
		{
			mapping:      firstMapping,
			channel:      firstChannel,
			settings:     firstChannel.GetOtherSettings(),
			resolution:   resolution,
			costEvidence: firstCost.evidence,
			windowStart:  now - 24*3600,
			windowEnd:    now,
		},
		{
			mapping:      secondMapping,
			channel:      secondChannel,
			settings:     secondChannel.GetOtherSettings(),
			resolution:   resolution,
			costEvidence: secondCost.evidence,
			windowStart:  now - 24*3600,
			windowEnd:    now,
		},
	}
	scores := []AutoPriorityScoreResult{
		{ChannelID: firstChannel.Id, OldPriority: 100, NewPriority: 900, ComputedPriority: 900, Applied: true},
		{ChannelID: secondChannel.Id, OldPriority: 100, NewPriority: 800, ComputedPriority: 800, Applied: true},
	}
	require.NoError(t, model.DB.Model(&model.UpstreamSourceBillingProbe{}).
		Where("mapping_id = ?", secondProbe.MappingID).
		Update("last_good_generation", secondProbe.LastGoodGeneration+1).Error)

	writes := 0
	callbackName := fmt.Sprintf("count_empirical_cas_writes_%s", t.Name())
	require.NoError(t, model.DB.Callback().Update().Before("gorm:update").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Schema == nil {
			return
		}
		if tx.Statement.Schema.Name == "Channel" || tx.Statement.Schema.Name == "Ability" {
			writes++
		}
	}))
	t.Cleanup(func() {
		require.NoError(t, model.DB.Callback().Update().Remove(callbackName))
	})

	reason, _, err := persistChannelAutoPriorityGroup(
		context.Background(),
		candidates,
		scores,
		[]int{0, 1},
		nil,
		channelAutoPriorityDefaultSinkPriority,
		now,
	)

	require.NoError(t, err)
	assert.Equal(t, "empirical_probe_changed", reason)
	assert.Zero(t, writes)
	for _, channel := range []model.Channel{firstChannel, secondChannel} {
		var reloaded model.Channel
		require.NoError(t, model.DB.First(&reloaded, channel.Id).Error)
		assert.Equal(t, int64(100), reloaded.GetPriority())
		assert.Zero(t, reloaded.GetOtherSettings().ChannelAutoPriorityLastRunAt)
		var ability model.Ability
		require.NoError(t, model.DB.Where("channel_id = ?", channel.Id).First(&ability).Error)
		require.NotNil(t, ability.Priority)
		assert.Equal(t, int64(100), *ability.Priority)
	}
}

func TestRunDueChannelAutoPriorityPersistenceFailureReturnsPersistedPriorities(t *testing.T) {
	setupUpstreamSourceAutoPriorityTestDB(t)
	now := int64(10_925_000)
	channels := []model.Channel{
		createAutoPriorityTestChannel(t, "failed cohort cheap", 100, dto.ChannelOtherSettings{
			ChannelAutoPriorityEnabled:        true,
			ChannelAutoPriorityWindowHours:    24,
			ChannelAutoPriorityRateMultiplier: 0.5,
		}),
		createAutoPriorityTestChannel(t, "failed cohort expensive", 100, dto.ChannelOtherSettings{
			ChannelAutoPriorityEnabled:        true,
			ChannelAutoPriorityWindowHours:    24,
			ChannelAutoPriorityRateMultiplier: 2,
		}),
	}
	for _, channel := range channels {
		createAutoPriorityTestUsageLog(t, channel.Id, now-60)
		createAutoPriorityTestMonitorLog(t, channel.Id, now-60)
	}

	updateCount := 0
	callbackName := fmt.Sprintf("fail_complete_auto_priority_cohort_%s", t.Name())
	require.NoError(t, model.DB.Callback().Update().Before("gorm:update").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Schema == nil || tx.Statement.Schema.Name != "Channel" {
			return
		}
		updates, ok := tx.Statement.Dest.(map[string]any)
		if !ok {
			return
		}
		settings, ok := updates["settings"].(string)
		if !ok || !strings.Contains(settings, "channel_auto_priority_last_score") {
			return
		}
		updateCount++
		if updateCount == 2 {
			tx.AddError(errors.New("forced complete-cohort persistence failure"))
		}
	}))
	t.Cleanup(func() {
		require.NoError(t, model.DB.Callback().Update().Remove(callbackName))
	})

	results := RunDueChannelAutoPriority(context.Background(), now)

	require.Len(t, results, len(channels))
	assert.Equal(t, len(channels), updateCount)
	for _, result := range results {
		assert.False(t, result.Applied)
		assert.Equal(t, "update_failed", result.Reason)
		assert.False(t, result.score.Applied)
		assert.Equal(t, "update_failed", result.score.Reason)
		assert.Equal(t, result.score.OldPriority, result.score.NewPriority)
		assert.NotEqual(t, result.score.OldPriority, result.score.ComputedPriority)

		var persisted model.Channel
		require.NoError(t, model.DB.First(&persisted, result.ChannelID).Error)
		assert.Equal(t, int64(100), persisted.GetPriority())
		assert.Equal(t, persisted.GetPriority(), result.score.OldPriority)
		assert.Equal(t, persisted.GetPriority(), result.score.NewPriority)
		persistedSettings := persisted.GetOtherSettings()
		assert.Zero(t, persistedSettings.ChannelAutoPriorityLastRunAt)
		assert.Nil(t, persistedSettings.ChannelAutoPriorityLastScore)

		var ability model.Ability
		require.NoError(t, model.DB.Where("channel_id = ?", result.ChannelID).First(&ability).Error)
		require.NotNil(t, ability.Priority)
		assert.Equal(t, int64(100), *ability.Priority)
	}
}

func TestRunDueChannelAutoPriorityConcurrentPersistenceFailureReturnsPostRollbackPriorities(t *testing.T) {
	tests := []struct {
		name                 string
		priority             int64
		status               int
		updateStatus         bool
		forceFailureAtUpdate int
	}{
		{
			name:         "manual disable",
			priority:     -1,
			status:       common.ChannelStatusManuallyDisabled,
			updateStatus: true,
		},
		{
			name:                 "priority only",
			priority:             333,
			status:               common.ChannelStatusEnabled,
			forceFailureAtUpdate: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldDB := model.DB
			oldLogDB := model.LOG_DB
			db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "auto-priority.db")), &gorm.Config{})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			sqlDB.SetMaxOpenConns(4)
			model.DB = db
			model.LOG_DB = db
			t.Cleanup(func() {
				model.DB = oldDB
				model.LOG_DB = oldLogDB
				require.NoError(t, sqlDB.Close())
			})
			require.NoError(t, model.DB.AutoMigrate(
				&model.UpstreamSource{},
				&model.UpstreamSourceChannelMapping{},
				&model.Channel{},
				&model.Ability{},
				&model.ChannelMonitorLog{},
				&model.Log{},
			))

			now := int64(10_935_000)
			first := createAutoPriorityTestChannel(t, "concurrent rollback first", 100, dto.ChannelOtherSettings{
				ChannelAutoPriorityEnabled:        true,
				ChannelAutoPriorityWindowHours:    24,
				ChannelAutoPriorityRateMultiplier: 0.5,
			})
			second := createAutoPriorityTestChannel(t, "concurrent rollback second", 100, dto.ChannelOtherSettings{
				ChannelAutoPriorityEnabled:        true,
				ChannelAutoPriorityWindowHours:    24,
				ChannelAutoPriorityRateMultiplier: 2,
			})
			channels := []model.Channel{first, second}
			if tt.forceFailureAtUpdate > len(channels) {
				channels = append(channels, createAutoPriorityTestChannel(t, "concurrent rollback third", 100, dto.ChannelOtherSettings{
					ChannelAutoPriorityEnabled:        true,
					ChannelAutoPriorityWindowHours:    24,
					ChannelAutoPriorityRateMultiplier: 4,
				}))
			}
			for _, channel := range channels {
				createAutoPriorityTestUsageLog(t, channel.Id, now-60)
				createAutoPriorityTestMonitorLog(t, channel.Id, now-60)
			}

			updateCount := 0
			var concurrentMutationErr error
			callbackName := fmt.Sprintf("concurrent_auto_priority_failure_%s", strings.ReplaceAll(t.Name(), "/", "_"))
			require.NoError(t, model.DB.Callback().Update().Before("gorm:update").Register(callbackName, func(tx *gorm.DB) {
				if tx.Statement.Schema == nil || tx.Statement.Schema.Name != "Channel" {
					return
				}
				updates, ok := tx.Statement.Dest.(map[string]any)
				if !ok {
					return
				}
				settings, ok := updates["settings"].(string)
				if !ok || !strings.Contains(settings, "channel_auto_priority_last_score") {
					return
				}
				updateCount++
				if updateCount == 1 {
					concurrentUpdates := map[string]any{"priority": tt.priority}
					if tt.updateStatus {
						concurrentUpdates["status"] = tt.status
					}
					if err := model.DB.Session(&gorm.Session{SkipHooks: true}).
						Model(&model.Channel{}).
						Where("id = ?", second.Id).
						Updates(concurrentUpdates).Error; err != nil {
						concurrentMutationErr = err
						return
					}
					if err := model.DB.Model(&model.Ability{}).
						Where("channel_id = ?", second.Id).
						Update("priority", tt.priority).Error; err != nil {
						concurrentMutationErr = err
					}
					return
				}
				if updateCount == tt.forceFailureAtUpdate {
					tx.AddError(errors.New("forced persistence failure after concurrent update"))
				}
			}))
			t.Cleanup(func() {
				require.NoError(t, model.DB.Callback().Update().Remove(callbackName))
			})

			results := RunDueChannelAutoPriority(context.Background(), now)

			require.NoError(t, concurrentMutationErr)
			require.Len(t, results, len(channels))
			assert.Equal(t, len(channels), updateCount)
			expectedPriorities := make(map[int]int64, len(channels))
			for _, channel := range channels {
				expectedPriorities[channel.Id] = 100
			}
			expectedPriorities[second.Id] = tt.priority
			resultsByChannelID := make(map[int]ChannelAutoPriorityRunResult, len(results))
			for _, result := range results {
				resultsByChannelID[result.ChannelID] = result
				assert.False(t, result.Applied)
				assert.Equal(t, "update_failed", result.Reason)
				assert.False(t, result.score.Applied)
				assert.Equal(t, "update_failed", result.score.Reason)
				assert.Equal(t, expectedPriorities[result.ChannelID], result.score.OldPriority)
				assert.Equal(t, expectedPriorities[result.ChannelID], result.score.NewPriority)

				var persisted model.Channel
				require.NoError(t, model.DB.First(&persisted, result.ChannelID).Error)
				assert.Equal(t, expectedPriorities[result.ChannelID], persisted.GetPriority())
				persistedSettings := persisted.GetOtherSettings()
				assert.Zero(t, persistedSettings.ChannelAutoPriorityLastRunAt)
				assert.Nil(t, persistedSettings.ChannelAutoPriorityLastScore)

				var ability model.Ability
				require.NoError(t, model.DB.Where("channel_id = ?", result.ChannelID).First(&ability).Error)
				require.NotNil(t, ability.Priority)
				assert.Equal(t, expectedPriorities[result.ChannelID], *ability.Priority)
			}
			mutatedResult, ok := resultsByChannelID[second.Id]
			require.True(t, ok)
			assert.NotEqual(t, tt.priority, mutatedResult.score.ComputedPriority)
			var persistedSecond model.Channel
			require.NoError(t, model.DB.First(&persistedSecond, second.Id).Error)
			assert.Equal(t, tt.status, persistedSecond.Status)
		})
	}
}

func TestRunDueChannelAutoPriorityRollsBackManualCleanupWhenGroupPersistFails(t *testing.T) {
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
		assert.False(t, result.Applied)
		assert.Equal(t, "update_failed", result.Reason)
	}
	var persistedEnabled model.Channel
	require.NoError(t, model.DB.First(&persistedEnabled, enabled.Id).Error)
	assert.Equal(t, int64(100), persistedEnabled.GetPriority())
	assert.Zero(t, persistedEnabled.GetOtherSettings().ChannelAutoPriorityLastRunAt)
	assert.Nil(t, persistedEnabled.GetOtherSettings().ChannelAutoPriorityLastScore)
	var persistedManual model.Channel
	require.NoError(t, model.DB.First(&persistedManual, manuallyDisabled.Id).Error)
	assert.Equal(t, int64(813), persistedManual.GetPriority())
	assert.Equal(t, now-60, persistedManual.GetOtherSettings().ChannelAutoPriorityLastRunAt)
	require.NotNil(t, persistedManual.GetOtherSettings().ChannelAutoPriorityLastScore)
	var persistedManualAbility model.Ability
	require.NoError(t, model.DB.Where("channel_id = ?", manuallyDisabled.Id).First(&persistedManualAbility).Error)
	require.NotNil(t, persistedManualAbility.Priority)
	assert.Equal(t, int64(813), *persistedManualAbility.Priority)
}

func TestRunDueChannelAutoPriorityRefreshesManualResultsAfterTransactionalGroupFailure(t *testing.T) {
	oldDB := model.DB
	oldLogDB := model.LOG_DB
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "auto-priority.db")), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(4)
	model.DB = db
	model.LOG_DB = db
	t.Cleanup(func() {
		model.DB = oldDB
		model.LOG_DB = oldLogDB
		require.NoError(t, sqlDB.Close())
	})
	require.NoError(t, model.DB.AutoMigrate(
		&model.UpstreamSource{},
		&model.UpstreamSourceChannelMapping{},
		&model.Channel{},
		&model.Ability{},
		&model.ChannelMonitorLog{},
		&model.Log{},
	))

	now := int64(10_975_000)
	enabled := createAutoPriorityTestChannel(t, "manual refresh enabled", 100, dto.ChannelOtherSettings{
		ChannelAutoPriorityEnabled:        true,
		ChannelAutoPriorityWindowHours:    24,
		ChannelAutoPriorityRateMultiplier: 1,
	})
	manuallyDisabled := createAutoPriorityTestChannel(t, "manual refresh disabled", 813, dto.ChannelOtherSettings{
		ChannelAutoPriorityEnabled:   true,
		ChannelAutoPriorityLastRunAt: now - 60,
		ChannelAutoPriorityLastScore: &dto.ChannelAutoPriorityScore{
			Version:     upstreamSourceAutoPriorityScoreVersion,
			ComputedAt:  now - 60,
			FinalScore:  81.3,
			NewPriority: 813,
		},
	})
	abilityOnlyUpdated := createAutoPriorityTestChannel(t, "manual refresh ability only", 814, dto.ChannelOtherSettings{
		ChannelAutoPriorityEnabled:   true,
		ChannelAutoPriorityLastRunAt: now - 60,
		ChannelAutoPriorityLastScore: &dto.ChannelAutoPriorityScore{
			Version:     upstreamSourceAutoPriorityScoreVersion,
			ComputedAt:  now - 60,
			FinalScore:  81.4,
			NewPriority: 814,
		},
	})
	require.NoError(t, model.DB.Model(&model.Channel{}).
		Where("id IN ?", []int{manuallyDisabled.Id, abilityOnlyUpdated.Id}).
		Update("status", common.ChannelStatusManuallyDisabled).Error)
	createAutoPriorityTestUsageLog(t, enabled.Id, now-60)
	createAutoPriorityTestMonitorLog(t, enabled.Id, now-60)

	concurrentSettings := manuallyDisabled.GetOtherSettings()
	concurrentSettings.ChannelAutoPriorityLastRunAt = now - 5
	concurrentSettings.ChannelAutoPriorityLastScore = &dto.ChannelAutoPriorityScore{
		Version:     upstreamSourceAutoPriorityScoreVersion,
		ComputedAt:  now - 5,
		FinalScore:  77.7,
		NewPriority: 777,
	}
	concurrentlyUpdated := manuallyDisabled
	concurrentlyUpdated.SetOtherSettings(concurrentSettings)

	const concurrentPriority = int64(777)
	const concurrentAbilityOnlyPriority = int64(555)
	updateCount := 0
	concurrentMutationStarted := false
	observedManualPriority := int64(0)
	observedManualSnapshotPresent := false
	var concurrentMutationErr error
	callbackName := fmt.Sprintf("refresh_manual_sweep_after_failure_%s", t.Name())
	require.NoError(t, model.DB.Callback().Update().Before("gorm:update").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Schema == nil || tx.Statement.Schema.Name != "Channel" {
			return
		}
		updates, ok := tx.Statement.Dest.(map[string]any)
		if !ok {
			return
		}
		settings, ok := updates["settings"].(string)
		if !ok || !strings.Contains(settings, "channel_auto_priority_last_score") {
			return
		}
		if concurrentMutationStarted {
			return
		}
		concurrentMutationStarted = true
		updateCount++
		var swept model.Channel
		if err := model.DB.Session(&gorm.Session{SkipHooks: true}).
			First(&swept, manuallyDisabled.Id).Error; err != nil {
			concurrentMutationErr = err
			tx.AddError(errors.New("manual sweep verification failed"))
			return
		}
		observedManualPriority = swept.GetPriority()
		observedManualSnapshotPresent = swept.GetOtherSettings().ChannelAutoPriorityLastScore != nil
		if err := model.DB.Session(&gorm.Session{SkipHooks: true}).
			Model(&model.Channel{}).
			Where("id = ?", manuallyDisabled.Id).
			Updates(map[string]any{
				"priority": concurrentPriority,
				"settings": concurrentlyUpdated.OtherSettings,
				"status":   common.ChannelStatusAutoDisabled,
			}).Error; err != nil {
			concurrentMutationErr = err
			tx.AddError(errors.New("concurrent manual channel update failed"))
			return
		}
		if err := model.DB.Session(&gorm.Session{SkipHooks: true}).
			Model(&model.Ability{}).
			Where("channel_id = ?", manuallyDisabled.Id).
			Update("priority", concurrentPriority).Error; err != nil {
			concurrentMutationErr = err
			tx.AddError(errors.New("concurrent manual ability update failed"))
			return
		}
		if err := model.DB.Session(&gorm.Session{SkipHooks: true}).
			Model(&model.Ability{}).
			Where("channel_id = ?", abilityOnlyUpdated.Id).
			Update("priority", concurrentAbilityOnlyPriority).Error; err != nil {
			concurrentMutationErr = err
			tx.AddError(errors.New("concurrent ability-only update failed"))
			return
		}
		tx.AddError(errors.New("forced transactional group persistence failure"))
	}))
	t.Cleanup(func() {
		require.NoError(t, model.DB.Callback().Update().Remove(callbackName))
	})

	results := RunDueChannelAutoPriority(context.Background(), now)

	require.NoError(t, concurrentMutationErr)
	assert.Equal(t, 1, updateCount)
	assert.Equal(t, int64(813), observedManualPriority)
	assert.True(t, observedManualSnapshotPresent)
	require.Len(t, results, 3)
	resultsByChannelID := make(map[int]ChannelAutoPriorityRunResult, len(results))
	for _, result := range results {
		resultsByChannelID[result.ChannelID] = result
	}

	manualResult, ok := resultsByChannelID[manuallyDisabled.Id]
	require.True(t, ok)
	assert.False(t, manualResult.Applied)
	assert.Equal(t, "update_failed", manualResult.Reason)
	assert.False(t, manualResult.score.Applied)
	assert.Equal(t, "update_failed", manualResult.score.Reason)
	assert.Equal(t, concurrentPriority, manualResult.score.OldPriority)
	assert.Equal(t, int64(-1), manualResult.score.ComputedPriority)
	assert.Equal(t, concurrentPriority, manualResult.score.NewPriority)

	abilityOnlyResult, ok := resultsByChannelID[abilityOnlyUpdated.Id]
	require.True(t, ok)
	assert.False(t, abilityOnlyResult.Applied)
	assert.Equal(t, "update_failed", abilityOnlyResult.Reason)
	assert.False(t, abilityOnlyResult.score.Applied)
	assert.Equal(t, "update_failed", abilityOnlyResult.score.Reason)
	assert.Equal(t, int64(814), abilityOnlyResult.score.OldPriority)
	assert.Equal(t, int64(-1), abilityOnlyResult.score.ComputedPriority)
	assert.Equal(t, int64(814), abilityOnlyResult.score.NewPriority)

	enabledResult, ok := resultsByChannelID[enabled.Id]
	require.True(t, ok)
	assert.False(t, enabledResult.Applied)
	assert.Equal(t, "update_failed", enabledResult.Reason)
	assert.False(t, enabledResult.score.Applied)
	assert.Equal(t, "update_failed", enabledResult.score.Reason)
	assert.Equal(t, int64(100), enabledResult.score.OldPriority)
	assert.Equal(t, int64(100), enabledResult.score.NewPriority)
	assert.NotEqual(t, enabledResult.score.OldPriority, enabledResult.score.ComputedPriority)

	var persistedManual model.Channel
	require.NoError(t, model.DB.First(&persistedManual, manuallyDisabled.Id).Error)
	assert.Equal(t, concurrentPriority, persistedManual.GetPriority())
	assert.Equal(t, common.ChannelStatusAutoDisabled, persistedManual.Status)
	persistedManualSnapshot := persistedManual.GetOtherSettings().ChannelAutoPriorityLastScore
	require.NotNil(t, persistedManualSnapshot)
	assert.Equal(t, upstreamSourceAutoPriorityScoreVersion, persistedManualSnapshot.Version)
	assert.Equal(t, now-5, persistedManualSnapshot.ComputedAt)
	assert.Equal(t, concurrentPriority, persistedManualSnapshot.NewPriority)

	var persistedManualAbility model.Ability
	require.NoError(t, model.DB.Where("channel_id = ?", manuallyDisabled.Id).First(&persistedManualAbility).Error)
	require.NotNil(t, persistedManualAbility.Priority)
	assert.Equal(t, concurrentPriority, *persistedManualAbility.Priority)

	var persistedAbilityOnlyChannel model.Channel
	require.NoError(t, model.DB.First(&persistedAbilityOnlyChannel, abilityOnlyUpdated.Id).Error)
	assert.Equal(t, int64(814), persistedAbilityOnlyChannel.GetPriority())
	assert.Equal(t, common.ChannelStatusManuallyDisabled, persistedAbilityOnlyChannel.Status)
	persistedAbilityOnlySnapshot := persistedAbilityOnlyChannel.GetOtherSettings().ChannelAutoPriorityLastScore
	require.NotNil(t, persistedAbilityOnlySnapshot)
	assert.Equal(t, now-60, persistedAbilityOnlySnapshot.ComputedAt)
	var persistedAbilityOnlyAbility model.Ability
	require.NoError(t, model.DB.Where("channel_id = ?", abilityOnlyUpdated.Id).First(&persistedAbilityOnlyAbility).Error)
	require.NotNil(t, persistedAbilityOnlyAbility.Priority)
	assert.Equal(t, concurrentAbilityOnlyPriority, *persistedAbilityOnlyAbility.Priority)

	var persistedEnabled model.Channel
	require.NoError(t, model.DB.First(&persistedEnabled, enabled.Id).Error)
	assert.Equal(t, int64(100), persistedEnabled.GetPriority())
	assert.Nil(t, persistedEnabled.GetOtherSettings().ChannelAutoPriorityLastScore)
	var persistedEnabledAbility model.Ability
	require.NoError(t, model.DB.Where("channel_id = ?", enabled.Id).First(&persistedEnabledAbility).Error)
	require.NotNil(t, persistedEnabledAbility.Priority)
	assert.Equal(t, int64(100), *persistedEnabledAbility.Priority)
}
