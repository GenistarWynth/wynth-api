package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
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

func TestRecordChannelMonitorLogAndLatest(t *testing.T) {
	setupChannelMonitorTestDB(t)

	require.NoError(t, RecordChannelMonitorLog(ChannelMonitorLog{
		ChannelID: 1,
		Model:     "gpt-4o-mini",
		Status:    " success ",
		LatencyMS: 120,
		Message:   "ok",
		CheckedAt: 100,
		CreatedAt: 90,
	}))
	require.NoError(t, RecordChannelMonitorLog(ChannelMonitorLog{
		ChannelID: 1,
		Model:     "gpt-4o",
		Status:    "unknown",
		LatencyMS: 250,
		Message:   "fallback status",
		CheckedAt: 200,
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
	assert.NotZero(t, latest[1].CreatedAt)
	assert.Equal(t, ChannelMonitorStatusFailed, latest[2].Status)
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
	assert.Equal(t, int64(0), stats[2].SuccessChecks)
	require.NotNil(t, stats[2].Availability)
	assert.Equal(t, 0.0, *stats[2].Availability)
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
	}

	require.NoError(t, RecordChannelMonitorLog(ChannelMonitorLog{
		ChannelID: 1,
		Status:    ChannelMonitorStatusSuccess,
		LatencyMS: 101,
		Message:   "old",
		CheckedAt: now - ChannelMonitorRetentionSeconds - 1,
	}))
	require.NoError(t, RecordChannelMonitorLog(ChannelMonitorLog{
		ChannelID: 1,
		Status:    ChannelMonitorStatusSuccess,
		LatencyMS: 100,
		Message:   "ok",
		CheckedAt: now - 100,
	}))
	require.NoError(t, RecordChannelMonitorLog(ChannelMonitorLog{
		ChannelID: 1,
		Status:    ChannelMonitorStatusFailed,
		LatencyMS: 201,
		Message:   "slow",
		CheckedAt: now - 50,
	}))

	require.NoError(t, AttachChannelMonitorInfo(channels, now))

	require.NotNil(t, channels[0].MonitorInfo)
	assert.True(t, channels[0].MonitorInfo.Enabled)
	assert.Equal(t, DefaultChannelMonitorIntervalMinutes, channels[0].MonitorInfo.IntervalMinutes)
	assert.Equal(t, ChannelMonitorStatusFailed, channels[0].MonitorInfo.LatestStatus)
	assert.Equal(t, now-50, channels[0].MonitorInfo.LatestCheckedAt)
	assert.Equal(t, int64(201), channels[0].MonitorInfo.LatestLatencyMS)
	assert.Equal(t, "slow", channels[0].MonitorInfo.LatestMessage)
	assert.Equal(t, int64(2), channels[0].MonitorInfo.SevenDayChecks)
	assert.Equal(t, int64(1), channels[0].MonitorInfo.SevenDaySuccesses)
	require.NotNil(t, channels[0].MonitorInfo.SevenDayAvailability)
	assert.Equal(t, 0.5, *channels[0].MonitorInfo.SevenDayAvailability)
	assert.Equal(t, int64(151), channels[0].MonitorInfo.AverageLatencyMS)

	require.NotNil(t, channels[1].MonitorInfo)
	assert.True(t, channels[1].MonitorInfo.Enabled)
	assert.Equal(t, MinimumChannelMonitorIntervalMinutes, channels[1].MonitorInfo.IntervalMinutes)
	assert.Nil(t, channels[1].MonitorInfo.SevenDayAvailability)

	require.NotNil(t, channels[2].MonitorInfo)
	assert.False(t, channels[2].MonitorInfo.Enabled)
	assert.Equal(t, 0, channels[2].MonitorInfo.IntervalMinutes)
	assert.Nil(t, channels[2].MonitorInfo.SevenDayAvailability)

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
