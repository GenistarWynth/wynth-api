package model

import (
	"context"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupChannelMonitorTestDB(t *testing.T) {
	t.Helper()

	oldDB := DB
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	DB = db
	t.Cleanup(func() {
		DB = oldDB
	})

	require.NoError(t, DB.AutoMigrate(&Channel{}, &ChannelMonitorLog{}))
}

func marshalChannelOtherSettings(t *testing.T, settings dto.ChannelOtherSettings) string {
	t.Helper()

	data, err := common.Marshal(settings)
	require.NoError(t, err)
	return string(data)
}

func marshalChannelSettingsMap(t *testing.T, settings map[string]any) string {
	t.Helper()

	data, err := common.Marshal(settings)
	require.NoError(t, err)
	return string(data)
}

func TestDeadChannelRecoveryDelaySecondsUsesConfiguredBounds(t *testing.T) {
	channel := &Channel{Id: 42, Status: common.ChannelStatusAutoDisabled}
	channel.SetOtherInfo(map[string]any{"status_time": int64(1_700_000_000)})
	settings := operation_setting.DeadChannelRecoverySettings{
		MinMinutes: 20,
		MaxMinutes: 25,
		MaxPerTick: 5,
	}

	delay := DeadChannelRecoveryDelaySeconds(channel, settings)

	assert.GreaterOrEqual(t, delay, int64(20*60))
	assert.LessOrEqual(t, delay, int64(25*60))
	assert.Equal(t, delay, DeadChannelRecoveryDelaySeconds(channel, settings))
	assert.Equal(t, int64(30*60), DeadChannelRecoveryDelaySeconds(channel, operation_setting.DeadChannelRecoverySettings{
		MinMinutes: 30,
		MaxMinutes: 30,
		MaxPerTick: 5,
	}))
}

func TestIsDeadChannelRecoveryEligible(t *testing.T) {
	makeChannel := func(status int, monitorEnabled bool) *Channel {
		channel := &Channel{Id: 1, Status: status}
		channel.SetOtherSettings(dto.ChannelOtherSettings{ChannelMonitorEnabled: monitorEnabled})
		return channel
	}

	assert.True(t, IsDeadChannelRecoveryEligible(makeChannel(common.ChannelStatusAutoDisabled, false)))
	assert.False(t, IsDeadChannelRecoveryEligible(makeChannel(common.ChannelStatusAutoDisabled, true)))
	assert.False(t, IsDeadChannelRecoveryEligible(makeChannel(common.ChannelStatusManuallyDisabled, false)))
	assert.False(t, IsDeadChannelRecoveryEligible(makeChannel(common.ChannelStatusEnabled, false)))
	assert.False(t, IsDeadChannelRecoveryEligible(nil))
}

func TestNormalizeChannelMonitorSettings(t *testing.T) {
	disabled := dto.ChannelOtherSettings{ChannelMonitorIntervalMinutes: -5}
	assert.Equal(t, disabled, NormalizeChannelMonitorSettings(disabled))

	withDefault := NormalizeChannelMonitorSettings(dto.ChannelOtherSettings{
		ChannelMonitorEnabled: true,
	})
	assert.Equal(t, DefaultChannelMonitorIntervalMinutes, withDefault.ChannelMonitorIntervalMinutes)

	withMinimum := NormalizeChannelMonitorSettings(dto.ChannelOtherSettings{
		ChannelMonitorEnabled:         true,
		ChannelMonitorIntervalMinutes: -3,
	})
	assert.Equal(t, MinimumChannelMonitorIntervalMinutes, withMinimum.ChannelMonitorIntervalMinutes)

	unchanged := NormalizeChannelMonitorSettings(dto.ChannelOtherSettings{
		ChannelMonitorEnabled:         true,
		ChannelMonitorIntervalMinutes: 15,
	})
	assert.Equal(t, 15, unchanged.ChannelMonitorIntervalMinutes)
}

func TestUpdateChannelMonitorSettingsPropagatesAvailabilityWindowWithinAutoPriorityGroup(t *testing.T) {
	setupChannelMonitorTestDB(t)

	channels := []Channel{
		{
			Name:  "selected",
			Group: "alpha",
			OtherSettings: marshalChannelSettingsMap(t, map[string]any{
				"channel_auto_priority_enabled":                   true,
				"channel_auto_priority_availability_window_hours": 1,
				"channel_auto_priority_last_run_at":               123,
				"selected_unrelated_setting":                      "keep-selected",
			}),
		},
		{
			Name:  "manual disabled auto priority",
			Group: "alpha",
			OtherSettings: marshalChannelSettingsMap(t, map[string]any{
				"channel_auto_priority_enabled":                   false,
				"channel_auto_priority_availability_window_hours": 2,
				"channel_auto_priority_last_run_at":               456,
				"member_unrelated_setting":                        "keep-member",
			}),
		},
		{
			Name:  "generated",
			Group: "alpha",
			OtherSettings: marshalChannelSettingsMap(t, map[string]any{
				"channel_auto_priority_enabled":                   true,
				"channel_auto_priority_availability_window_hours": 3,
				"generated_by_upstream_source_id":                 99,
			}),
		},
		{
			Name:  "different exact group",
			Group: "alpha,beta",
			OtherSettings: marshalChannelSettingsMap(t, map[string]any{
				"channel_auto_priority_enabled":                   true,
				"channel_auto_priority_availability_window_hours": 4,
			}),
		},
	}
	require.NoError(t, DB.Create(&channels).Error)

	requestedSettings := marshalChannelSettingsMap(t, map[string]any{
		"channel_monitor_enabled":                         true,
		"channel_monitor_interval_minutes":                5,
		"channel_monitor_model":                           "gpt-4o-mini",
		"channel_auto_priority_enabled":                   true,
		"channel_auto_priority_interval_minutes":          0,
		"channel_auto_priority_window_hours":              12,
		"channel_auto_priority_availability_window_hours": 48,
		"channel_auto_priority_rate_multiplier":           0.8,
		"channel_auto_priority_last_run_at":               99,
		"selected_unrelated_setting":                      "keep-selected",
	})

	updated, err := UpdateChannelMonitorSettings(
		context.Background(),
		channels[0].Id,
		requestedSettings,
		"",
	)
	require.NoError(t, err)
	require.NotNil(t, updated)

	selectedSettings := updated.GetOtherSettings()
	assert.True(t, selectedSettings.ChannelMonitorEnabled)
	assert.Equal(t, 5, selectedSettings.ChannelMonitorIntervalMinutes)
	assert.Equal(t, "gpt-4o-mini", selectedSettings.ChannelMonitorModel)
	assert.True(t, selectedSettings.ChannelAutoPriorityEnabled)
	assert.Equal(t, 0, selectedSettings.ChannelAutoPriorityIntervalMinutes)
	assert.Equal(t, 12, selectedSettings.ChannelAutoPriorityWindowHours)
	assert.Equal(t, 48, selectedSettings.ChannelAutoPriorityAvailabilityWindowHours)
	assert.Equal(t, 0.8, selectedSettings.ChannelAutoPriorityRateMultiplier)
	assert.Equal(t, int64(123), selectedSettings.ChannelAutoPriorityLastRunAt)
	var selectedSettingsMap map[string]any
	require.NoError(t, common.UnmarshalJsonStr(updated.OtherSettings, &selectedSettingsMap))
	assert.Equal(t, "keep-selected", selectedSettingsMap["selected_unrelated_setting"])

	var manualMember Channel
	require.NoError(t, DB.First(&manualMember, channels[1].Id).Error)
	manualMemberSettings := manualMember.GetOtherSettings()
	assert.False(t, manualMemberSettings.ChannelAutoPriorityEnabled)
	assert.Equal(t, 2, manualMemberSettings.ChannelAutoPriorityAvailabilityWindowHours)
	assert.Equal(t, int64(456), manualMemberSettings.ChannelAutoPriorityLastRunAt)
	var manualMemberSettingsMap map[string]any
	require.NoError(t, common.UnmarshalJsonStr(manualMember.OtherSettings, &manualMemberSettingsMap))
	assert.Equal(t, "keep-member", manualMemberSettingsMap["member_unrelated_setting"])

	var generated Channel
	require.NoError(t, DB.First(&generated, channels[2].Id).Error)
	assert.Equal(t, 48, generated.GetOtherSettings().ChannelAutoPriorityAvailabilityWindowHours)

	var differentGroup Channel
	require.NoError(t, DB.First(&differentGroup, channels[3].Id).Error)
	assert.Equal(t, 4, differentGroup.GetOtherSettings().ChannelAutoPriorityAvailabilityWindowHours)
}

func TestUpdateChannelMonitorSettingsPropagatesAvailabilityWindowFromGeneratedChannel(t *testing.T) {
	setupChannelMonitorTestDB(t)

	channels := []Channel{
		{
			Name:  "generated selected",
			Group: "alpha",
			OtherSettings: marshalChannelSettingsMap(t, map[string]any{
				"channel_auto_priority_enabled":                   true,
				"channel_auto_priority_interval_minutes":          15,
				"channel_auto_priority_window_hours":              24,
				"channel_auto_priority_availability_window_hours": 1,
				"channel_auto_priority_rate_multiplier":           0.5,
				"generated_by_upstream_source_id":                 99,
			}),
		},
		{
			Name:  "manual peer",
			Group: "alpha",
			OtherSettings: marshalChannelSettingsMap(t, map[string]any{
				"channel_auto_priority_enabled":                   true,
				"channel_auto_priority_interval_minutes":          10,
				"channel_auto_priority_availability_window_hours": 2,
			}),
		},
		{
			Name:  "other group",
			Group: "beta",
			OtherSettings: marshalChannelSettingsMap(t, map[string]any{
				"channel_auto_priority_enabled":                   true,
				"channel_auto_priority_interval_minutes":          20,
				"channel_auto_priority_availability_window_hours": 3,
			}),
		},
	}
	require.NoError(t, DB.Create(&channels).Error)

	requestedSettings := marshalChannelSettingsMap(t, map[string]any{
		"channel_monitor_enabled":                         true,
		"channel_monitor_interval_minutes":                5,
		"channel_auto_priority_enabled":                   false,
		"channel_auto_priority_interval_minutes":          60,
		"channel_auto_priority_window_hours":              12,
		"channel_auto_priority_availability_window_hours": 96,
		"channel_auto_priority_rate_multiplier":           2,
	})

	updated, err := UpdateChannelMonitorSettings(
		context.Background(),
		channels[0].Id,
		requestedSettings,
		ChannelSettingsUpdateScopeAutoPriority,
	)
	require.NoError(t, err)
	require.NotNil(t, updated)

	selectedSettings := updated.GetOtherSettings()
	assert.True(t, selectedSettings.ChannelAutoPriorityEnabled)
	assert.Equal(t, 60, selectedSettings.ChannelAutoPriorityIntervalMinutes)
	assert.Equal(t, 24, selectedSettings.ChannelAutoPriorityWindowHours)
	assert.Equal(t, 96, selectedSettings.ChannelAutoPriorityAvailabilityWindowHours)
	assert.Equal(t, 0.5, selectedSettings.ChannelAutoPriorityRateMultiplier)
	assert.Equal(t, 99, selectedSettings.GeneratedByUpstreamSourceID)

	var manualPeer Channel
	require.NoError(t, DB.First(&manualPeer, channels[1].Id).Error)
	assert.Equal(t, 60, manualPeer.GetOtherSettings().ChannelAutoPriorityIntervalMinutes)
	assert.Equal(t, 96, manualPeer.GetOtherSettings().ChannelAutoPriorityAvailabilityWindowHours)

	var otherGroup Channel
	require.NoError(t, DB.First(&otherGroup, channels[2].Id).Error)
	assert.Equal(t, 20, otherGroup.GetOtherSettings().ChannelAutoPriorityIntervalMinutes)
	assert.Equal(t, 3, otherGroup.GetOtherSettings().ChannelAutoPriorityAvailabilityWindowHours)
}

func TestUpdateChannelMonitorSettingsAllowsMonitorOnlySaveWithoutAvailabilityWindow(t *testing.T) {
	setupChannelMonitorTestDB(t)

	channels := []Channel{
		{
			Name:  "legacy selected",
			Group: "alpha",
			OtherSettings: marshalChannelSettingsMap(t, map[string]any{
				"selected_unrelated_setting": "keep-selected",
			}),
		},
		{
			Name:  "auto priority peer",
			Group: "alpha",
			OtherSettings: marshalChannelSettingsMap(t, map[string]any{
				"channel_auto_priority_enabled":                   true,
				"channel_auto_priority_availability_window_hours": 72,
			}),
		},
	}
	require.NoError(t, DB.Create(&channels).Error)

	requestedSettings := marshalChannelSettingsMap(t, map[string]any{
		"channel_monitor_enabled":          true,
		"channel_monitor_interval_minutes": 5,
		"channel_monitor_model":            "gpt-4o-mini",
		"selected_unrelated_setting":       "keep-selected",
	})

	updated, err := UpdateChannelMonitorSettings(
		context.Background(),
		channels[0].Id,
		requestedSettings,
		"",
	)
	require.NoError(t, err)
	require.NotNil(t, updated)

	selectedSettings := updated.GetOtherSettings()
	assert.True(t, selectedSettings.ChannelMonitorEnabled)
	assert.Equal(t, 5, selectedSettings.ChannelMonitorIntervalMinutes)
	assert.Equal(t, "gpt-4o-mini", selectedSettings.ChannelMonitorModel)
	var selectedSettingsMap map[string]any
	require.NoError(t, common.UnmarshalJsonStr(updated.OtherSettings, &selectedSettingsMap))
	assert.NotContains(t, selectedSettingsMap, "channel_auto_priority_availability_window_hours")

	var peer Channel
	require.NoError(t, DB.First(&peer, channels[1].Id).Error)
	assert.Equal(t, 72, peer.GetOtherSettings().ChannelAutoPriorityAvailabilityWindowHours)
}

func TestUpdateChannelMonitorSettingsRejectsNullSettingsObject(t *testing.T) {
	setupChannelMonitorTestDB(t)

	channel := Channel{Name: "null settings", Group: "default"}
	require.NoError(t, DB.Create(&channel).Error)

	updated, err := UpdateChannelMonitorSettings(context.Background(), channel.Id, "null", "")

	assert.Nil(t, updated)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid channel monitor settings")
}

func TestRecordChannelMonitorLogAndLatest(t *testing.T) {
	setupChannelMonitorTestDB(t)

	require.NoError(t, RecordChannelMonitorLog(ChannelMonitorLog{
		ChannelID:           1,
		Model:               "gpt-4o-mini",
		Status:              " success ",
		LatencyMS:           120,
		EndpointLatencyMS:   40,
		FirstTokenLatencyMS: 80,
		PromptTokens:        5,
		CompletionTokens:    7,
		Message:             "ok",
		CheckedAt:           100,
		CreatedAt:           90,
	}))
	require.NoError(t, RecordChannelMonitorLog(ChannelMonitorLog{
		ChannelID:           1,
		Model:               "gpt-4o",
		Status:              "unknown",
		LatencyMS:           250,
		EndpointLatencyMS:   70,
		FirstTokenLatencyMS: 150,
		PromptTokens:        11,
		CompletionTokens:    13,
		Message:             "fallback status",
		CheckedAt:           200,
	}))
	require.NoError(t, RecordChannelMonitorLog(ChannelMonitorLog{
		ChannelID: 2,
		Model:     "claude-sonnet-4",
		Status:    ChannelMonitorStatusFailed,
		LatencyMS: 500,
		CheckedAt: 150,
	}))

	latest, err := GetLatestChannelMonitorLogs([]int{1, 2, 3})
	require.NoError(t, err)
	require.Len(t, latest, 2)

	assert.Equal(t, "gpt-4o", latest[1].Model)
	assert.Equal(t, ChannelMonitorStatusError, latest[1].Status)
	assert.Equal(t, int64(70), latest[1].EndpointLatencyMS)
	assert.Equal(t, int64(150), latest[1].FirstTokenLatencyMS)
	assert.Equal(t, 11, latest[1].PromptTokens)
	assert.Equal(t, 13, latest[1].CompletionTokens)
	assert.NotZero(t, latest[1].CreatedAt)
	assert.Equal(t, ChannelMonitorStatusFailed, latest[2].Status)
}

func TestGetLatestChannelMonitorLogsTieBreaksAndFiltersRequestedChannels(t *testing.T) {
	setupChannelMonitorTestDB(t)

	logs := []ChannelMonitorLog{
		{ID: 10, ChannelID: 1, Status: ChannelMonitorStatusSuccess, Message: "same time lower id", CheckedAt: 500},
		{ID: 11, ChannelID: 1, Status: ChannelMonitorStatusFailed, Message: "same time higher id", CheckedAt: 500},
		{ID: 12, ChannelID: 1, Status: ChannelMonitorStatusError, Message: "older", CheckedAt: 400},
		{ID: 20, ChannelID: 2, Status: ChannelMonitorStatusDegraded, Message: "requested second channel", CheckedAt: 600},
		{ID: 30, ChannelID: 99, Status: ChannelMonitorStatusSuccess, Message: "unrelated newest", CheckedAt: 900},
	}
	for id := 100; id < 125; id++ {
		logs = append(logs, ChannelMonitorLog{
			ID:        id,
			ChannelID: 1,
			Status:    ChannelMonitorStatusSuccess,
			Message:   "older high id",
			CheckedAt: 300 + int64(id-100),
		})
	}
	for _, log := range logs {
		require.NoError(t, RecordChannelMonitorLog(log))
	}

	latest, err := GetLatestChannelMonitorLogs([]int{1, 2, 3})
	require.NoError(t, err)
	require.Len(t, latest, 2)

	require.Contains(t, latest, 1)
	assert.Equal(t, 11, latest[1].ID)
	assert.Equal(t, ChannelMonitorStatusFailed, latest[1].Status)
	assert.Equal(t, "same time higher id", latest[1].Message)

	require.Contains(t, latest, 2)
	assert.Equal(t, 20, latest[2].ID)
	assert.Equal(t, ChannelMonitorStatusDegraded, latest[2].Status)
	assert.NotContains(t, latest, 3)
	assert.NotContains(t, latest, 99)
}

func TestGetChannelMonitorStatsUsesRollingWindow(t *testing.T) {
	setupChannelMonitorTestDB(t)

	logs := []ChannelMonitorLog{
		{ChannelID: 1, Status: ChannelMonitorStatusSuccess, LatencyMS: 100, CheckedAt: 90},
		{ChannelID: 1, Status: ChannelMonitorStatusFailed, LatencyMS: 300, CheckedAt: 100},
		{ChannelID: 1, Status: ChannelMonitorStatusSuccess, LatencyMS: 500, CheckedAt: 110},
		{ChannelID: 1, Status: ChannelMonitorStatusSuccess, LatencyMS: 900, CheckedAt: 10},
		{ChannelID: 2, Status: ChannelMonitorStatusDegraded, LatencyMS: 200, CheckedAt: 120},
	}
	for _, log := range logs {
		require.NoError(t, RecordChannelMonitorLog(log))
	}

	stats, err := GetChannelMonitorStats([]int{1, 2, 3}, 90)
	require.NoError(t, err)
	require.Len(t, stats, 2)

	require.Contains(t, stats, 1)
	assert.Equal(t, int64(3), stats[1].TotalChecks)
	assert.Equal(t, int64(2), stats[1].SuccessChecks)
	require.NotNil(t, stats[1].Availability)
	assert.InDelta(t, 2.0/3.0, *stats[1].Availability, 0.0001)
	assert.InDelta(t, 300.0, stats[1].AverageLatencyMS, 0.0001)

	require.Contains(t, stats, 2)
	assert.Equal(t, int64(1), stats[2].TotalChecks)
	assert.Equal(t, int64(1), stats[2].SuccessChecks)
	require.NotNil(t, stats[2].Availability)
	assert.Equal(t, 1.0, *stats[2].Availability)
}

func TestGetChannelMonitorStatsIncludesTimingBreakdowns(t *testing.T) {
	setupChannelMonitorTestDB(t)

	logs := []ChannelMonitorLog{
		{ChannelID: 1, Status: ChannelMonitorStatusSuccess, LatencyMS: 100, EndpointLatencyMS: 20, FirstTokenLatencyMS: 60, CheckedAt: 100},
		{ChannelID: 1, Status: ChannelMonitorStatusDegraded, LatencyMS: 300, EndpointLatencyMS: 40, FirstTokenLatencyMS: 120, CheckedAt: 110},
		{ChannelID: 1, Status: ChannelMonitorStatusFailed, LatencyMS: 500, EndpointLatencyMS: 0, FirstTokenLatencyMS: 0, CheckedAt: 120},
		{ChannelID: 1, Status: ChannelMonitorStatusSuccess, LatencyMS: 0, EndpointLatencyMS: 0, FirstTokenLatencyMS: 0, CheckedAt: 130},
	}
	for _, log := range logs {
		require.NoError(t, RecordChannelMonitorLog(log))
	}

	stats, err := GetChannelMonitorStats([]int{1}, 90)
	require.NoError(t, err)

	require.Contains(t, stats, 1)
	assert.Equal(t, int64(4), stats[1].TotalChecks)
	assert.Equal(t, int64(3), stats[1].SuccessChecks)
	assert.InDelta(t, 300.0, stats[1].AverageLatencyMS, 0.0001)
	assert.InDelta(t, 30.0, stats[1].AverageEndpointLatencyMS, 0.0001)
	assert.InDelta(t, 90.0, stats[1].AverageFirstTokenLatencyMS, 0.0001)
}

func TestGetChannelMonitorDetailReturnsSummaryAndRecentRecords(t *testing.T) {
	setupChannelMonitorTestDB(t)

	channel := &Channel{
		Id:            1,
		OtherSettings: marshalChannelOtherSettings(t, dto.ChannelOtherSettings{ChannelMonitorEnabled: true, ChannelMonitorIntervalMinutes: 5}),
	}
	for i := 1; i <= 5; i++ {
		status := ChannelMonitorStatusSuccess
		if i == 5 {
			status = ChannelMonitorStatusFailed
		}
		require.NoError(t, RecordChannelMonitorLog(ChannelMonitorLog{
			ID:                  i,
			ChannelID:           channel.Id,
			Model:               "gpt-4o",
			Status:              status,
			LatencyMS:           int64(100 * i),
			EndpointLatencyMS:   int64(10 * i),
			FirstTokenLatencyMS: int64(50 * i),
			PromptTokens:        i,
			CompletionTokens:    i + 10,
			Message:             "point",
			CheckedAt:           int64(1000 + i),
		}))
	}

	detail, err := GetChannelMonitorDetail(channel, 2000, 3)

	require.NoError(t, err)
	assert.Equal(t, channel.Id, detail.ChannelID)
	assert.True(t, detail.Info.Enabled)
	assert.Equal(t, 5, detail.Info.IntervalMinutes)
	assert.Equal(t, ChannelMonitorStatusFailed, detail.Info.LatestStatus)
	assert.Equal(t, int64(500), detail.Info.LatestLatencyMS)
	assert.Equal(t, int64(50), detail.Info.LatestEndpointLatencyMS)
	assert.Equal(t, int64(250), detail.Info.LatestFirstTokenLatencyMS)
	assert.Equal(t, int64(1305), detail.Info.NextCheckAt)
	assert.Equal(t, int64(0), detail.Info.SecondsUntilNextCheck)
	assert.Equal(t, 5, detail.Info.LatestPromptTokens)
	assert.Equal(t, 15, detail.Info.LatestCompletionTokens)
	require.Len(t, detail.RecentRecords, 3)
	assert.Equal(t, []int{3, 4, 5}, []int{detail.RecentRecords[0].ID, detail.RecentRecords[1].ID, detail.RecentRecords[2].ID})
	assert.Equal(t, int64(1003), detail.RecentRecords[0].CheckedAt)
	assert.Equal(t, int64(250), detail.RecentRecords[2].FirstTokenLatencyMS)
}

func TestDeleteOldChannelMonitorLogs(t *testing.T) {
	setupChannelMonitorTestDB(t)

	for _, log := range []ChannelMonitorLog{
		{ChannelID: 1, Status: ChannelMonitorStatusSuccess, CheckedAt: 10},
		{ChannelID: 1, Status: ChannelMonitorStatusSuccess, CheckedAt: 20},
		{ChannelID: 1, Status: ChannelMonitorStatusSuccess, CheckedAt: 30},
		{ChannelID: 1, Status: ChannelMonitorStatusSuccess, CheckedAt: 40},
	} {
		require.NoError(t, RecordChannelMonitorLog(log))
	}

	deleted, err := DeleteOldChannelMonitorLogs(35, 2)
	require.NoError(t, err)
	assert.Equal(t, int64(3), deleted)

	var remaining []ChannelMonitorLog
	require.NoError(t, DB.Find(&remaining).Error)
	require.Len(t, remaining, 1)
	assert.Equal(t, int64(40), remaining[0].CheckedAt)
}

func TestAttachChannelMonitorInfo(t *testing.T) {
	setupChannelMonitorTestDB(t)

	now := int64(1_000_000)
	channels := []*Channel{
		{
			Id:            1,
			OtherSettings: marshalChannelOtherSettings(t, dto.ChannelOtherSettings{ChannelMonitorEnabled: true}),
		},
		{
			Id:            2,
			OtherSettings: marshalChannelOtherSettings(t, dto.ChannelOtherSettings{ChannelMonitorEnabled: true, ChannelMonitorIntervalMinutes: -5}),
		},
		{
			Id:            3,
			OtherSettings: marshalChannelOtherSettings(t, dto.ChannelOtherSettings{}),
		},
		{
			Id:            4,
			Status:        common.ChannelStatusAutoDisabled,
			OtherSettings: marshalChannelOtherSettings(t, dto.ChannelOtherSettings{}),
		},
	}
	channels[3].SetOtherInfo(map[string]any{"status_time": now - 60})

	require.NoError(t, RecordChannelMonitorLog(ChannelMonitorLog{
		ChannelID: 1,
		Model:     "gpt-4o-mini",
		Status:    ChannelMonitorStatusSuccess,
		LatencyMS: 101,
		Message:   "old",
		CheckedAt: now - ChannelMonitorRetentionSeconds - 1,
	}))
	require.NoError(t, RecordChannelMonitorLog(ChannelMonitorLog{
		ChannelID: 1,
		Model:     "gpt-4o",
		Status:    ChannelMonitorStatusSuccess,
		LatencyMS: 100,
		Message:   "ok",
		CheckedAt: now - 100,
	}))
	require.NoError(t, RecordChannelMonitorLog(ChannelMonitorLog{
		ChannelID:           1,
		Model:               "gpt-4o-realtime",
		Status:              ChannelMonitorStatusFailed,
		LatencyMS:           0,
		EndpointLatencyMS:   81,
		FirstTokenLatencyMS: 161,
		PromptTokens:        12,
		CompletionTokens:    4,
		Message:             "slow",
		CheckedAt:           now - 50,
	}))

	require.NoError(t, AttachChannelMonitorInfo(channels, now))

	require.NotNil(t, channels[0].MonitorInfo)
	monitorInfoJSON, err := common.Marshal(channels[0].MonitorInfo)
	require.NoError(t, err)
	var monitorInfoMap map[string]any
	require.NoError(t, common.Unmarshal(monitorInfoJSON, &monitorInfoMap))
	assert.True(t, channels[0].MonitorInfo.Enabled)
	assert.Equal(t, DefaultChannelMonitorIntervalMinutes, channels[0].MonitorInfo.IntervalMinutes)
	assert.Equal(t, ChannelMonitorStatusFailed, channels[0].MonitorInfo.LatestStatus)
	assert.Equal(t, "gpt-4o-realtime", monitorInfoMap["latest_model"])
	assert.Equal(t, now-50, channels[0].MonitorInfo.LatestCheckedAt)
	assert.Equal(t, int64(0), channels[0].MonitorInfo.LatestLatencyMS)
	assert.Equal(t, int64(81), channels[0].MonitorInfo.LatestEndpointLatencyMS)
	assert.Equal(t, int64(161), channels[0].MonitorInfo.LatestFirstTokenLatencyMS)
	assert.Equal(t, 12, channels[0].MonitorInfo.LatestPromptTokens)
	assert.Equal(t, 4, channels[0].MonitorInfo.LatestCompletionTokens)
	assert.Equal(t, "slow", channels[0].MonitorInfo.LatestMessage)
	assert.Equal(t, int64(2), channels[0].MonitorInfo.SevenDayChecks)
	assert.Equal(t, int64(1), channels[0].MonitorInfo.SevenDaySuccesses)
	require.NotNil(t, channels[0].MonitorInfo.SevenDayAvailability)
	assert.Equal(t, 0.5, *channels[0].MonitorInfo.SevenDayAvailability)
	assert.Equal(t, int64(100), channels[0].MonitorInfo.AverageLatencyMS)

	require.NotNil(t, channels[1].MonitorInfo)
	assert.True(t, channels[1].MonitorInfo.Enabled)
	assert.Equal(t, MinimumChannelMonitorIntervalMinutes, channels[1].MonitorInfo.IntervalMinutes)
	assert.Nil(t, channels[1].MonitorInfo.SevenDayAvailability)

	require.NotNil(t, channels[2].MonitorInfo)
	assert.False(t, channels[2].MonitorInfo.Enabled)
	assert.Equal(t, 0, channels[2].MonitorInfo.IntervalMinutes)
	assert.Nil(t, channels[2].MonitorInfo.SevenDayAvailability)
	assert.False(t, channels[2].MonitorInfo.DeadRecoveryEligible)

	recoverySettings := operation_setting.GetDeadChannelRecoverySettings()
	wantDeadRecoveryNextCheckAt := DeadChannelRecoveryNextCheckAt(channels[3], recoverySettings)
	require.NotNil(t, channels[3].MonitorInfo)
	assert.True(t, channels[3].MonitorInfo.DeadRecoveryEligible)
	assert.Equal(t, wantDeadRecoveryNextCheckAt, channels[3].MonitorInfo.DeadRecoveryNextCheckAt)
	assert.Equal(t, wantDeadRecoveryNextCheckAt-now, channels[3].MonitorInfo.DeadRecoverySecondsUntilNextCheck)

	invalidSettingsChannel := &Channel{
		Type:          1,
		Key:           "test-key",
		Name:          "invalid-settings",
		OtherSettings: "{bad-json",
	}
	require.NoError(t, DB.Create(invalidSettingsChannel).Error)

	require.NoError(t, AttachChannelMonitorInfo([]*Channel{invalidSettingsChannel}, now))

	assert.Equal(t, "{bad-json", invalidSettingsChannel.OtherSettings)
	require.NotNil(t, invalidSettingsChannel.MonitorInfo)
	assert.False(t, invalidSettingsChannel.MonitorInfo.Enabled)

	var reloaded Channel
	require.NoError(t, DB.First(&reloaded, invalidSettingsChannel.Id).Error)
	assert.Equal(t, "{bad-json", reloaded.OtherSettings)
}
