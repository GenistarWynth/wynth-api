package model

import (
	"fmt"
	"math"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
)

const (
	ChannelMonitorStatusSuccess  = "success"
	ChannelMonitorStatusFailed   = "failed"
	ChannelMonitorStatusDegraded = "degraded"
	ChannelMonitorStatusError    = "error"

	DefaultChannelMonitorIntervalMinutes = 10
	MinimumChannelMonitorIntervalMinutes = 1
	ChannelMonitorRetentionSeconds       = int64(7 * 24 * 60 * 60)
)

type ChannelMonitorLog struct {
	ID        int    `json:"id" gorm:"primaryKey"`
	ChannelID int    `json:"channel_id" gorm:"index:idx_channel_monitor_channel_checked,priority:1"`
	Model     string `json:"model" gorm:"type:varchar(191)"`
	Status    string `json:"status" gorm:"type:varchar(32)"`
	LatencyMS int64  `json:"latency_ms" gorm:"bigint"`
	Message   string `json:"message" gorm:"type:text"`
	CheckedAt int64  `json:"checked_at" gorm:"bigint;index;index:idx_channel_monitor_channel_checked,priority:2"`
	CreatedAt int64  `json:"created_at" gorm:"bigint"`
}

type ChannelMonitorStats struct {
	ChannelID        int      `json:"channel_id"`
	TotalChecks      int64    `json:"total_checks"`
	SuccessChecks    int64    `json:"success_checks"`
	Availability     *float64 `json:"availability,omitempty"`
	AverageLatencyMS float64  `json:"average_latency_ms"`
}

type ChannelMonitorInfo struct {
	Enabled              bool     `json:"enabled"`
	IntervalMinutes      int      `json:"interval_minutes"`
	LatestStatus         string   `json:"latest_status,omitempty"`
	LatestCheckedAt      int64    `json:"latest_checked_at,omitempty"`
	LatestLatencyMS      int64    `json:"latest_latency_ms,omitempty"`
	LatestMessage        string   `json:"latest_message,omitempty"`
	SevenDayChecks       int64    `json:"seven_day_checks"`
	SevenDaySuccesses    int64    `json:"seven_day_successes"`
	SevenDayAvailability *float64 `json:"seven_day_availability,omitempty"`
	AverageLatencyMS     int64    `json:"average_latency_ms,omitempty"`
}

func NormalizeChannelMonitorSettings(settings dto.ChannelOtherSettings) dto.ChannelOtherSettings {
	if !settings.ChannelMonitorEnabled {
		return settings
	}
	if settings.ChannelMonitorIntervalMinutes == 0 {
		settings.ChannelMonitorIntervalMinutes = DefaultChannelMonitorIntervalMinutes
	} else if settings.ChannelMonitorIntervalMinutes < MinimumChannelMonitorIntervalMinutes {
		settings.ChannelMonitorIntervalMinutes = MinimumChannelMonitorIntervalMinutes
	}
	return settings
}

func NormalizeChannelMonitorStatus(status string) string {
	switch strings.TrimSpace(status) {
	case ChannelMonitorStatusSuccess:
		return ChannelMonitorStatusSuccess
	case ChannelMonitorStatusFailed:
		return ChannelMonitorStatusFailed
	case ChannelMonitorStatusDegraded:
		return ChannelMonitorStatusDegraded
	case ChannelMonitorStatusError:
		return ChannelMonitorStatusError
	default:
		return ChannelMonitorStatusError
	}
}

func RecordChannelMonitorLog(log ChannelMonitorLog) error {
	now := common.GetTimestamp()
	log.Status = NormalizeChannelMonitorStatus(log.Status)
	if log.CheckedAt == 0 {
		log.CheckedAt = now
	}
	if log.CreatedAt == 0 {
		log.CreatedAt = now
	}
	return DB.Create(&log).Error
}

func GetLatestChannelMonitorLogs(channelIDs []int) (map[int]ChannelMonitorLog, error) {
	latest := make(map[int]ChannelMonitorLog, len(channelIDs))
	if len(channelIDs) == 0 {
		return latest, nil
	}

	latestCheckedAt := DB.Model(&ChannelMonitorLog{}).
		Select("channel_id, MAX(checked_at) AS checked_at").
		Where("channel_id IN ?", channelIDs).
		Group("channel_id")

	var logs []ChannelMonitorLog
	if err := DB.Model(&ChannelMonitorLog{}).
		Joins("JOIN (?) AS latest ON channel_monitor_logs.channel_id = latest.channel_id AND channel_monitor_logs.checked_at = latest.checked_at", latestCheckedAt).
		Where("channel_monitor_logs.channel_id IN ?", channelIDs).
		Order("channel_monitor_logs.channel_id ASC").
		Order("channel_monitor_logs.id DESC").
		Find(&logs).Error; err != nil {
		return nil, err
	}

	for _, log := range logs {
		if _, ok := latest[log.ChannelID]; ok {
			continue
		}
		latest[log.ChannelID] = log
	}
	return latest, nil
}

func GetChannelMonitorStats(channelIDs []int, windowStart int64) (map[int]ChannelMonitorStats, error) {
	stats := make(map[int]ChannelMonitorStats, len(channelIDs))
	if len(channelIDs) == 0 {
		return stats, nil
	}

	type statsRow struct {
		ChannelID        int      `gorm:"column:channel_id"`
		TotalChecks      int64    `gorm:"column:total_checks"`
		SuccessChecks    int64    `gorm:"column:success_checks"`
		AverageLatencyMS *float64 `gorm:"column:average_latency_ms"`
	}
	var rows []statsRow
	err := DB.Model(&ChannelMonitorLog{}).
		Select(
			"channel_id, COUNT(*) AS total_checks, SUM(CASE WHEN status = ? THEN 1 ELSE 0 END) AS success_checks, AVG(latency_ms) AS average_latency_ms",
			ChannelMonitorStatusSuccess,
		).
		Where("channel_id IN ? AND checked_at >= ?", channelIDs, windowStart).
		Group("channel_id").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}

	for _, row := range rows {
		stat := ChannelMonitorStats{
			ChannelID:     row.ChannelID,
			TotalChecks:   row.TotalChecks,
			SuccessChecks: row.SuccessChecks,
		}
		if row.AverageLatencyMS != nil {
			stat.AverageLatencyMS = *row.AverageLatencyMS
		}
		if row.TotalChecks > 0 {
			availability := float64(row.SuccessChecks) / float64(row.TotalChecks)
			stat.Availability = &availability
		}
		stats[row.ChannelID] = stat
	}
	return stats, nil
}

func DeleteOldChannelMonitorLogs(cutoff int64, batchSize int) (int64, error) {
	if batchSize <= 0 {
		batchSize = 100
	}

	var totalDeleted int64
	for {
		var ids []int
		if err := DB.Model(&ChannelMonitorLog{}).
			Where("checked_at < ?", cutoff).
			Order("checked_at ASC").
			Limit(batchSize).
			Pluck("id", &ids).Error; err != nil {
			return totalDeleted, err
		}
		if len(ids) == 0 {
			return totalDeleted, nil
		}

		result := DB.Where("id IN ?", ids).Delete(&ChannelMonitorLog{})
		if result.Error != nil {
			return totalDeleted, result.Error
		}
		totalDeleted += result.RowsAffected
		if len(ids) < batchSize {
			return totalDeleted, nil
		}
	}
}

func GetChannelMonitorSettingsReadOnly(channel *Channel) dto.ChannelOtherSettings {
	if channel == nil || strings.TrimSpace(channel.OtherSettings) == "" {
		return dto.ChannelOtherSettings{}
	}

	var settings dto.ChannelOtherSettings
	if err := common.UnmarshalJsonStr(channel.OtherSettings, &settings); err != nil {
		common.SysLog(fmt.Sprintf("failed to unmarshal channel monitor settings: channel_id=%d, error=%v", channel.Id, err))
		return dto.ChannelOtherSettings{}
	}
	return settings
}

func AttachChannelMonitorInfo(channels []*Channel, now int64) error {
	channelIDs := make([]int, 0, len(channels))
	for _, channel := range channels {
		if channel != nil {
			channelIDs = append(channelIDs, channel.Id)
		}
	}

	latest, err := GetLatestChannelMonitorLogs(channelIDs)
	if err != nil {
		return err
	}
	stats, err := GetChannelMonitorStats(channelIDs, now-ChannelMonitorRetentionSeconds)
	if err != nil {
		return err
	}

	for _, channel := range channels {
		if channel == nil {
			continue
		}
		settings := NormalizeChannelMonitorSettings(GetChannelMonitorSettingsReadOnly(channel))
		info := &ChannelMonitorInfo{
			Enabled:         settings.ChannelMonitorEnabled,
			IntervalMinutes: settings.ChannelMonitorIntervalMinutes,
		}
		if log, ok := latest[channel.Id]; ok {
			info.LatestStatus = log.Status
			info.LatestCheckedAt = log.CheckedAt
			info.LatestLatencyMS = log.LatencyMS
			info.LatestMessage = log.Message
		}
		if stat, ok := stats[channel.Id]; ok {
			info.SevenDayChecks = stat.TotalChecks
			info.SevenDaySuccesses = stat.SuccessChecks
			info.SevenDayAvailability = stat.Availability
			info.AverageLatencyMS = int64(math.Round(stat.AverageLatencyMS))
		}
		channel.MonitorInfo = info
	}
	return nil
}
