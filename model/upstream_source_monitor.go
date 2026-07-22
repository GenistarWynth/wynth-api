package model

import (
	"errors"

	"gorm.io/gorm"
)

const (
	defaultUpstreamSourceMonitorDueLimit = 100
	maxUpstreamSourceMonitorDueLimit     = 1000
)

func ListDueUpstreamSourcesForMonitor(now int64, limit int) ([]UpstreamSource, error) {
	if limit <= 0 {
		limit = defaultUpstreamSourceMonitorDueLimit
	}
	if limit > maxUpstreamSourceMonitorDueLimit {
		limit = maxUpstreamSourceMonitorDueLimit
	}
	sources := make([]UpstreamSource, 0, limit)
	err := DB.Where(
		"status = ? AND monitor_enabled = ? AND next_monitor_at <= ?",
		UpstreamSourceStatusEnabled,
		true,
		now,
	).Order("next_monitor_at, id").Limit(limit).Find(&sources).Error
	return sources, err
}

func backfillUpstreamSourceMonitorDefaults() error {
	defaults := []struct {
		column string
		value  interface{}
	}{
		{column: "monitor_enabled", value: false},
		{column: "monitor_interval_minutes", value: 0},
		{column: "next_monitor_at", value: int64(0)},
		{column: "current_monitor_token", value: ""},
		{column: "monitor_started_at", value: int64(0)},
		{column: "last_monitor_time", value: int64(0)},
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		for _, item := range defaults {
			if err := tx.Model(&UpstreamSource{}).
				Where(item.column+" IS NULL").
				UpdateColumn(item.column, item.value).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func ClaimUpstreamSourceMonitor(sourceID int, token string, now int64, staleAfterSeconds int64) (bool, error) {
	if sourceID == 0 {
		return false, errors.New("source ID is required")
	}
	if token == "" {
		return false, errors.New("monitor token is required")
	}
	staleBefore := now - staleAfterSeconds
	result := DB.Model(&UpstreamSource{}).
		Where(
			"id = ? AND status = ? AND monitor_enabled = ? AND next_monitor_at <= ? AND (current_monitor_token = ? OR current_monitor_token IS NULL OR monitor_started_at <= ?)",
			sourceID,
			UpstreamSourceStatusEnabled,
			true,
			now,
			"",
			staleBefore,
		).
		Updates(map[string]interface{}{
			"current_monitor_token": token,
			"monitor_started_at":    now,
			"updated_time":          now,
		})
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected == 1, nil
}

func ReleaseUpstreamSourceMonitor(sourceID int, token string, finishedAt int64) error {
	if sourceID == 0 {
		return errors.New("source ID is required")
	}
	if token == "" {
		return errors.New("monitor token is required")
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		var source UpstreamSource
		result := lockForUpdate(tx).
			Select("id", "monitor_enabled", "monitor_interval_minutes").
			Where("id = ? AND current_monitor_token = ?", sourceID, token).
			Limit(1).
			Find(&source)
		if result.Error != nil || result.RowsAffected == 0 {
			return result.Error
		}
		intervalMinutes := source.MonitorIntervalMinutes
		if intervalMinutes < 1 {
			intervalMinutes = 1
		}
		nextMonitorAt := int64(0)
		if source.MonitorEnabled {
			nextMonitorAt = finishedAt + int64(intervalMinutes)*60
		}
		return tx.Model(&UpstreamSource{}).
			Where("id = ? AND current_monitor_token = ?", sourceID, token).
			Updates(map[string]interface{}{
				"current_monitor_token": "",
				"monitor_started_at":    0,
				"last_monitor_time":     finishedAt,
				"next_monitor_at":       nextMonitorAt,
				"updated_time":          finishedAt,
			}).Error
	})
}

func ReconcileStaleUpstreamSourceMonitorRuns(now int64, staleAfterSeconds int64) (int64, error) {
	staleBefore := now - staleAfterSeconds
	var reconciled int64
	err := DB.Transaction(func(tx *gorm.DB) error {
		scans := tx.Model(&UpstreamSourceScan{}).
			Where("scan_type = ? AND status = ? AND started_at <= ?", UpstreamSourceScanTypeMonitor, UpstreamSourceScanStatusRunning, staleBefore).
			Updates(map[string]interface{}{
				"status":        UpstreamSourceScanStatusFailed,
				"finished_at":   now,
				"error_summary": "monitor run interrupted before completion",
			})
		if scans.Error != nil {
			return scans.Error
		}
		reconciled = scans.RowsAffected
		if err := tx.Model(&UpstreamSource{}).
			Where("current_monitor_token <> ? AND monitor_started_at <= ?", "", staleBefore).
			Updates(map[string]interface{}{
				"current_monitor_token": "",
				"monitor_started_at":    0,
				"next_monitor_at":       now,
				"updated_time":          now,
			}).Error; err != nil {
			return err
		}
		return tx.Model(&UpstreamSource{}).
			Where("monitor_enabled = ? AND next_monitor_at <> ?", false, 0).
			UpdateColumn("next_monitor_at", 0).Error
	})
	return reconciled, err
}
