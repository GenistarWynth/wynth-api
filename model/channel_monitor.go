package model

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"gorm.io/gorm"
)

const (
	ChannelMonitorStatusSuccess  = "success"
	ChannelMonitorStatusFailed   = "failed"
	ChannelMonitorStatusDegraded = "degraded"
	ChannelMonitorStatusError    = "error"

	DefaultChannelMonitorIntervalMinutes   = 10
	MinimumChannelMonitorIntervalMinutes   = 1
	ChannelMonitorRetentionSeconds         = int64(7 * 24 * 60 * 60)
	ChannelSettingsUpdateScopeMonitor      = "monitor"
	ChannelSettingsUpdateScopeAutoPriority = "auto-priority"
	ChannelSettingsUpdateScopeDeadRecovery = "dead-recovery"
	DefaultChannelDeadRecoveryMinMinutes   = 15
	DefaultChannelDeadRecoveryMaxMinutes   = 120

	channelAutoPriorityIntervalKey           = "channel_auto_priority_interval_minutes"
	channelAutoPriorityAvailabilityWindowKey = "channel_auto_priority_availability_window_hours"
	channelDeadRecoveryMinMinutesKey         = "channel_dead_recovery_min_minutes"
	channelDeadRecoveryMaxMinutesKey         = "channel_dead_recovery_max_minutes"
)

type ChannelMonitorLog struct {
	ID                  int    `json:"id" gorm:"primaryKey"`
	ChannelID           int    `json:"channel_id" gorm:"index:idx_channel_monitor_channel_checked,priority:1"`
	Model               string `json:"model" gorm:"type:varchar(191)"`
	Status              string `json:"status" gorm:"type:varchar(32)"`
	LatencyMS           int64  `json:"latency_ms" gorm:"bigint"`
	EndpointLatencyMS   int64  `json:"endpoint_latency_ms" gorm:"bigint"`
	FirstTokenLatencyMS int64  `json:"first_token_latency_ms" gorm:"bigint"`
	PromptTokens        int    `json:"prompt_tokens"`
	CompletionTokens    int    `json:"completion_tokens"`
	Message             string `json:"message" gorm:"type:text"`
	CheckedAt           int64  `json:"checked_at" gorm:"bigint;index;index:idx_channel_monitor_channel_checked,priority:2"`
	CreatedAt           int64  `json:"created_at" gorm:"bigint"`
}

type ChannelMonitorStats struct {
	ChannelID                  int      `json:"channel_id"`
	TotalChecks                int64    `json:"total_checks"`
	SuccessChecks              int64    `json:"success_checks"`
	Availability               *float64 `json:"availability,omitempty"`
	AverageLatencyMS           float64  `json:"average_latency_ms"`
	AverageEndpointLatencyMS   float64  `json:"average_endpoint_latency_ms"`
	AverageFirstTokenLatencyMS float64  `json:"average_first_token_latency_ms"`
}

type ChannelMonitorInfo struct {
	Enabled                           bool     `json:"enabled"`
	IntervalMinutes                   int      `json:"interval_minutes"`
	LatestStatus                      string   `json:"latest_status,omitempty"`
	LatestModel                       string   `json:"latest_model,omitempty"`
	LatestCheckedAt                   int64    `json:"latest_checked_at,omitempty"`
	LatestLatencyMS                   int64    `json:"latest_latency_ms,omitempty"`
	LatestEndpointLatencyMS           int64    `json:"latest_endpoint_latency_ms,omitempty"`
	LatestFirstTokenLatencyMS         int64    `json:"latest_first_token_latency_ms,omitempty"`
	LatestPromptTokens                int      `json:"latest_prompt_tokens,omitempty"`
	LatestCompletionTokens            int      `json:"latest_completion_tokens,omitempty"`
	LatestMessage                     string   `json:"latest_message,omitempty"`
	NextCheckAt                       int64    `json:"next_check_at,omitempty"`
	SecondsUntilNextCheck             int64    `json:"seconds_until_next_check,omitempty"`
	DeadRecoveryEligible              bool     `json:"dead_recovery_eligible"`
	DeadRecoveryNextCheckAt           int64    `json:"dead_recovery_next_check_at,omitempty"`
	DeadRecoverySecondsUntilNextCheck int64    `json:"dead_recovery_seconds_until_next_check,omitempty"`
	SevenDayChecks                    int64    `json:"seven_day_checks"`
	SevenDaySuccesses                 int64    `json:"seven_day_successes"`
	SevenDayAvailability              *float64 `json:"seven_day_availability,omitempty"`
	AverageLatencyMS                  int64    `json:"average_latency_ms,omitempty"`
	AverageEndpointLatencyMS          int64    `json:"average_endpoint_latency_ms,omitempty"`
	AverageFirstTokenLatencyMS        int64    `json:"average_first_token_latency_ms,omitempty"`
}

type ChannelMonitorDetail struct {
	ChannelID     int                 `json:"channel_id"`
	Info          ChannelMonitorInfo  `json:"info"`
	RecentRecords []ChannelMonitorLog `json:"recent_records"`
}

var channelMonitorEditableSettingKeys = []string{
	"channel_monitor_enabled",
	"channel_monitor_interval_minutes",
	"channel_monitor_model",
}

var channelAutoPriorityEditableSettingKeys = []string{
	"channel_auto_priority_enabled",
	"channel_auto_priority_interval_minutes",
	"channel_auto_priority_window_hours",
	"channel_auto_priority_availability_window_hours",
	"channel_auto_priority_rate_multiplier",
}

var channelDeadRecoveryEditableSettingKeys = []string{
	"channel_dead_recovery_enabled",
	channelDeadRecoveryMinMinutesKey,
	channelDeadRecoveryMaxMinutesKey,
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

func NormalizeChannelDeadRecoverySettings(settings dto.ChannelOtherSettings) dto.ChannelOtherSettings {
	if settings.ChannelDeadRecoveryMinMinutes < 1 {
		settings.ChannelDeadRecoveryMinMinutes = DefaultChannelDeadRecoveryMinMinutes
	}
	if settings.ChannelDeadRecoveryMaxMinutes < 1 {
		settings.ChannelDeadRecoveryMaxMinutes = DefaultChannelDeadRecoveryMaxMinutes
	}
	if settings.ChannelDeadRecoveryMaxMinutes < settings.ChannelDeadRecoveryMinMinutes {
		settings.ChannelDeadRecoveryMaxMinutes = settings.ChannelDeadRecoveryMinMinutes
	}
	return settings
}

func parseChannelSettingsObject(raw string) (map[string]any, dto.ChannelOtherSettings, error) {
	settingsObject := make(map[string]any)
	settings := dto.ChannelOtherSettings{}
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return settingsObject, settings, nil
	}
	if trimmed == "null" {
		return nil, settings, fmt.Errorf("settings must be a JSON object")
	}
	if err := common.UnmarshalJsonStr(raw, &settingsObject); err != nil {
		return nil, settings, err
	}
	if err := common.UnmarshalJsonStr(raw, &settings); err != nil {
		return nil, settings, err
	}
	return settingsObject, settings, nil
}

// UpdateChannelMonitorSettings persists one channel-settings domain without
// overwriting the others, worker-owned snapshots, or unrelated JSON keys. An
// empty scope retains the legacy combined monitor and auto-priority behavior.
// Generated channels retain rule ownership of their per-channel auto-priority
// settings, but the schedule interval and availability window are group-scoped
// and remain editable from any channel in the group.
func UpdateChannelMonitorSettings(ctx context.Context, channelID int, requestedRaw, scope string) (*Channel, error) {
	requestedObject, requestedSettings, err := parseChannelSettingsObject(requestedRaw)
	if err != nil {
		return nil, fmt.Errorf("invalid channel monitor settings: %w", err)
	}
	updateMonitorSettings := scope == "" || scope == ChannelSettingsUpdateScopeMonitor
	updateAutoPrioritySettings := scope == "" || scope == ChannelSettingsUpdateScopeAutoPriority
	updateDeadRecoverySettings := scope == ChannelSettingsUpdateScopeDeadRecovery
	if !updateMonitorSettings && !updateAutoPrioritySettings && !updateDeadRecoverySettings {
		return nil, fmt.Errorf("invalid channel settings update scope %q", scope)
	}
	_, availabilityWindowRequested := requestedObject[channelAutoPriorityAvailabilityWindowKey]
	availabilityWindowRequested = updateAutoPrioritySettings && availabilityWindowRequested
	_, intervalRequested := requestedObject[channelAutoPriorityIntervalKey]
	intervalRequested = updateAutoPrioritySettings && intervalRequested
	_, deadRecoveryMinRequested := requestedObject[channelDeadRecoveryMinMinutesKey]
	deadRecoveryMinRequested = updateDeadRecoverySettings && deadRecoveryMinRequested
	_, deadRecoveryMaxRequested := requestedObject[channelDeadRecoveryMaxMinutesKey]
	deadRecoveryMaxRequested = updateDeadRecoverySettings && deadRecoveryMaxRequested

	var updatedChannel Channel
	err = DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var located Channel
		if err := tx.Select("group").First(&located, channelID).Error; err != nil {
			return err
		}

		// Lock the entire group in channel ID order so concurrent monitor saves
		// cannot deadlock by first locking different selected channels.
		var groupChannels []Channel
		if err := lockForUpdate(tx).
			Where(map[string]any{"group": located.Group}).
			Order("id ASC").
			Find(&groupChannels).Error; err != nil {
			return err
		}
		currentIndex := -1
		for i := range groupChannels {
			if groupChannels[i].Id == channelID {
				currentIndex = i
				break
			}
		}
		if currentIndex == -1 {
			return gorm.ErrRecordNotFound
		}
		current := groupChannels[currentIndex]

		currentObject, currentSettings, err := parseChannelSettingsObject(current.OtherSettings)
		if err != nil {
			return fmt.Errorf("invalid stored settings for channel %d: %w", current.Id, err)
		}
		if updateMonitorSettings {
			for _, key := range channelMonitorEditableSettingKeys {
				if value, exists := requestedObject[key]; exists {
					currentObject[key] = value
				} else {
					delete(currentObject, key)
				}
			}
		}

		if updateAutoPrioritySettings {
			generated := currentSettings.GeneratedByUpstreamSourceID != 0 ||
				currentSettings.GeneratedByUpstreamMappingID != 0
			if !generated {
				for _, key := range channelAutoPriorityEditableSettingKeys {
					if value, exists := requestedObject[key]; exists {
						currentObject[key] = value
					} else {
						delete(currentObject, key)
					}
				}
			} else {
				for _, key := range []string{channelAutoPriorityIntervalKey, channelAutoPriorityAvailabilityWindowKey} {
					if value, exists := requestedObject[key]; exists {
						currentObject[key] = value
					}
				}
			}
		}

		if updateDeadRecoverySettings {
			for _, key := range channelDeadRecoveryEditableSettingKeys {
				if value, exists := requestedObject[key]; exists {
					currentObject[key] = value
				} else {
					delete(currentObject, key)
				}
			}
			if deadRecoveryMinRequested && requestedSettings.ChannelDeadRecoveryMinMinutes < 1 {
				return fmt.Errorf("settings.%s must be >= 1", channelDeadRecoveryMinMinutesKey)
			}
			if deadRecoveryMaxRequested && requestedSettings.ChannelDeadRecoveryMaxMinutes < 1 {
				return fmt.Errorf("settings.%s must be >= 1", channelDeadRecoveryMaxMinutesKey)
			}
			effective := NormalizeChannelDeadRecoverySettings(requestedSettings)
			if deadRecoveryMaxRequested && requestedSettings.ChannelDeadRecoveryMaxMinutes < effective.ChannelDeadRecoveryMinMinutes {
				return fmt.Errorf("settings.%s must be >= settings.%s", channelDeadRecoveryMaxMinutesKey, channelDeadRecoveryMinMinutesKey)
			}
		}

		mergedBytes, err := common.Marshal(currentObject)
		if err != nil {
			return err
		}
		current.OtherSettings = string(mergedBytes)
		mergedSettings := current.GetOtherSettings()
		if mergedSettings.ChannelMonitorEnabled &&
			mergedSettings.ChannelMonitorIntervalMinutes < MinimumChannelMonitorIntervalMinutes {
			return fmt.Errorf("settings.channel_monitor_interval_minutes must be >= %d", MinimumChannelMonitorIntervalMinutes)
		}
		if availabilityWindowRequested &&
			requestedSettings.ChannelAutoPriorityAvailabilityWindowHours !=
				dto.NormalizeChannelAutoPriorityWindowHours(requestedSettings.ChannelAutoPriorityAvailabilityWindowHours) {
			return fmt.Errorf(
				"settings.channel_auto_priority_availability_window_hours must be between 1 and %d",
				dto.ChannelAutoPriorityMaxWindowHours,
			)
		}
		if intervalRequested && requestedSettings.ChannelAutoPriorityIntervalMinutes < 0 {
			return fmt.Errorf("settings.channel_auto_priority_interval_minutes must be >= 0")
		}
		if err := current.ValidateSettings(); err != nil {
			return err
		}

		now := common.GetTimestamp()
		if err := tx.Model(&Channel{}).
			Where("id = ?", current.Id).
			Updates(map[string]any{
				"settings":     current.OtherSettings,
				"updated_time": now,
			}).Error; err != nil {
			return err
		}
		current.UpdatedTime = now
		updatedChannel = current

		if !availabilityWindowRequested && !intervalRequested {
			return nil
		}
		availabilityWindowHours := requestedSettings.ChannelAutoPriorityAvailabilityWindowHours
		intervalMinutes := requestedSettings.ChannelAutoPriorityIntervalMinutes
		for i := range groupChannels {
			if groupChannels[i].Id == current.Id {
				continue
			}
			memberObject, memberSettings, err := parseChannelSettingsObject(groupChannels[i].OtherSettings)
			if err != nil {
				return fmt.Errorf("invalid stored settings for channel %d: %w", groupChannels[i].Id, err)
			}
			if !memberSettings.ChannelAutoPriorityEnabled {
				continue
			}
			if availabilityWindowRequested {
				memberObject[channelAutoPriorityAvailabilityWindowKey] = availabilityWindowHours
			}
			if intervalRequested {
				memberObject[channelAutoPriorityIntervalKey] = intervalMinutes
			}
			memberBytes, err := common.Marshal(memberObject)
			if err != nil {
				return err
			}
			if err := tx.Model(&Channel{}).
				Where("id = ?", groupChannels[i].Id).
				Updates(map[string]any{
					"settings":     string(memberBytes),
					"updated_time": now,
				}).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &updatedChannel, nil
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

func GetRecentChannelMonitorLogs(channelID int, limit int) ([]ChannelMonitorLog, error) {
	if channelID == 0 {
		return []ChannelMonitorLog{}, nil
	}
	if limit <= 0 {
		limit = 60
	}
	if limit > 500 {
		limit = 500
	}

	var newest []ChannelMonitorLog
	if err := DB.Model(&ChannelMonitorLog{}).
		Where("channel_id = ?", channelID).
		Order("checked_at DESC").
		Order("id DESC").
		Limit(limit).
		Find(&newest).Error; err != nil {
		return nil, err
	}

	for i, j := 0, len(newest)-1; i < j; i, j = i+1, j-1 {
		newest[i], newest[j] = newest[j], newest[i]
	}
	return newest, nil
}

func GetChannelMonitorStats(channelIDs []int, windowStart int64) (map[int]ChannelMonitorStats, error) {
	return GetChannelMonitorStatsWithContext(context.Background(), channelIDs, windowStart)
}

func GetChannelMonitorStatsWithContext(ctx context.Context, channelIDs []int, windowStart int64) (map[int]ChannelMonitorStats, error) {
	stats := make(map[int]ChannelMonitorStats, len(channelIDs))
	if len(channelIDs) == 0 {
		return stats, nil
	}

	type statsRow struct {
		ChannelID                  int      `gorm:"column:channel_id"`
		TotalChecks                int64    `gorm:"column:total_checks"`
		SuccessChecks              int64    `gorm:"column:success_checks"`
		AverageLatencyMS           *float64 `gorm:"column:average_latency_ms"`
		AverageEndpointLatencyMS   *float64 `gorm:"column:average_endpoint_latency_ms"`
		AverageFirstTokenLatencyMS *float64 `gorm:"column:average_first_token_latency_ms"`
	}
	var rows []statsRow
	err := DB.WithContext(ctx).Model(&ChannelMonitorLog{}).
		Select(
			"channel_id, COUNT(*) AS total_checks, SUM(CASE WHEN status = ? OR status = ? THEN 1 ELSE 0 END) AS success_checks, AVG(CASE WHEN latency_ms > 0 THEN latency_ms ELSE NULL END) AS average_latency_ms, AVG(CASE WHEN endpoint_latency_ms > 0 THEN endpoint_latency_ms ELSE NULL END) AS average_endpoint_latency_ms, AVG(CASE WHEN first_token_latency_ms > 0 THEN first_token_latency_ms ELSE NULL END) AS average_first_token_latency_ms",
			ChannelMonitorStatusSuccess,
			ChannelMonitorStatusDegraded,
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
		if row.AverageEndpointLatencyMS != nil {
			stat.AverageEndpointLatencyMS = *row.AverageEndpointLatencyMS
		}
		if row.AverageFirstTokenLatencyMS != nil {
			stat.AverageFirstTokenLatencyMS = *row.AverageFirstTokenLatencyMS
		}
		if row.TotalChecks > 0 {
			availability := float64(row.SuccessChecks) / float64(row.TotalChecks)
			stat.Availability = &availability
		}
		stats[row.ChannelID] = stat
	}
	return stats, nil
}

func GetChannelMonitorDetail(channel *Channel, now int64, limit int) (*ChannelMonitorDetail, error) {
	if channel == nil {
		return nil, fmt.Errorf("channel is required")
	}
	if err := AttachChannelMonitorInfo([]*Channel{channel}, now); err != nil {
		return nil, err
	}
	info := ChannelMonitorInfo{}
	if channel.MonitorInfo != nil {
		info = *channel.MonitorInfo
	}
	recent, err := GetRecentChannelMonitorLogs(channel.Id, limit)
	if err != nil {
		return nil, err
	}
	return &ChannelMonitorDetail{
		ChannelID:     channel.Id,
		Info:          info,
		RecentRecords: recent,
	}, nil
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

func channelStatusTime(channel *Channel) int64 {
	if channel == nil {
		return 0
	}
	raw, ok := channel.GetOtherInfo()["status_time"]
	if !ok || raw == nil {
		return 0
	}
	switch value := raw.(type) {
	case int64:
		return value
	case int:
		return int64(value)
	case float64:
		return int64(value)
	case float32:
		return int64(value)
	case json.Number:
		parsed, _ := value.Int64()
		return parsed
	default:
		trimmed := strings.TrimSpace(fmt.Sprint(value))
		if trimmed == "" {
			return 0
		}
		parsed, err := strconv.ParseInt(trimmed, 10, 64)
		if err != nil {
			return 0
		}
		return parsed
	}
}

func DeadChannelRecoveryDelaySeconds(channel *Channel) int64 {
	settings := NormalizeChannelDeadRecoverySettings(GetChannelMonitorSettingsReadOnly(channel))
	minSeconds := int64(settings.ChannelDeadRecoveryMinMinutes) * 60
	maxSeconds := int64(settings.ChannelDeadRecoveryMaxMinutes) * 60
	span := maxSeconds - minSeconds
	if span <= 0 {
		return minSeconds
	}
	channelID := 0
	if channel != nil {
		channelID = channel.Id
	}
	statusTime := channelStatusTime(channel)
	seed := uint64(channelID)*2654435761 ^ uint64(statusTime)*1597334677
	seed ^= seed << 13
	seed ^= seed >> 7
	seed ^= seed << 17
	return minSeconds + int64(seed%uint64(span+1))
}

func DeadChannelRecoveryNextCheckAt(channel *Channel) int64 {
	return channelStatusTime(channel) + DeadChannelRecoveryDelaySeconds(channel)
}

func IsDeadChannelRecoveryEligible(channel *Channel) bool {
	if channel == nil || channel.Status != common.ChannelStatusAutoDisabled {
		return false
	}
	settings := NormalizeChannelDeadRecoverySettings(GetChannelMonitorSettingsReadOnly(channel))
	return !settings.ChannelMonitorEnabled && settings.ChannelDeadRecoveryEnabled
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
		if IsDeadChannelRecoveryEligible(channel) {
			info.DeadRecoveryEligible = true
			info.DeadRecoveryNextCheckAt = DeadChannelRecoveryNextCheckAt(channel)
			if info.DeadRecoveryNextCheckAt > now {
				info.DeadRecoverySecondsUntilNextCheck = info.DeadRecoveryNextCheckAt - now
			}
		}
		if log, ok := latest[channel.Id]; ok {
			info.LatestStatus = log.Status
			info.LatestModel = log.Model
			info.LatestCheckedAt = log.CheckedAt
			info.LatestLatencyMS = log.LatencyMS
			info.LatestEndpointLatencyMS = log.EndpointLatencyMS
			info.LatestFirstTokenLatencyMS = log.FirstTokenLatencyMS
			info.LatestPromptTokens = log.PromptTokens
			info.LatestCompletionTokens = log.CompletionTokens
			info.LatestMessage = log.Message
			if info.Enabled && info.IntervalMinutes > 0 && log.CheckedAt > 0 {
				info.NextCheckAt = log.CheckedAt + int64(info.IntervalMinutes)*60
				if info.NextCheckAt > now {
					info.SecondsUntilNextCheck = info.NextCheckAt - now
				}
			}
		}
		if stat, ok := stats[channel.Id]; ok {
			info.SevenDayChecks = stat.TotalChecks
			info.SevenDaySuccesses = stat.SuccessChecks
			info.SevenDayAvailability = stat.Availability
			info.AverageLatencyMS = int64(math.Round(stat.AverageLatencyMS))
			info.AverageEndpointLatencyMS = int64(math.Round(stat.AverageEndpointLatencyMS))
			info.AverageFirstTokenLatencyMS = int64(math.Round(stat.AverageFirstTokenLatencyMS))
		}
		channel.MonitorInfo = info
	}
	return nil
}
