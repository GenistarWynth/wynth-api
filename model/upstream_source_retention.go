package model

import (
	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const upstreamSourceRetentionDaySeconds int64 = 24 * 60 * 60

type UpstreamSourceRetentionPolicy struct {
	ScanSeconds                 int64 `json:"scan_seconds"`
	CapabilityOutcomeSeconds    int64 `json:"capability_outcome_seconds"`
	CostSnapshotSeconds         int64 `json:"cost_snapshot_seconds"`
	AnnouncementSeconds         int64 `json:"announcement_seconds"`
	SubscriptionSnapshotSeconds int64 `json:"subscription_snapshot_seconds"`
	NotificationDeliverySeconds int64 `json:"notification_delivery_seconds"`
}

type UpstreamSourceRetentionCleanupResult struct {
	Scans                  int64 `json:"scans"`
	CapabilityOutcomes     int64 `json:"capability_outcomes"`
	CostSnapshots          int64 `json:"cost_snapshots"`
	Announcements          int64 `json:"announcements"`
	SubscriptionSnapshots  int64 `json:"subscription_snapshots"`
	NotificationDeliveries int64 `json:"notification_deliveries"`
}

func DefaultUpstreamSourceRetentionPolicy() UpstreamSourceRetentionPolicy {
	return UpstreamSourceRetentionPolicy{
		ScanSeconds:                 upstreamSourceRetentionSeconds("UPSTREAM_SOURCE_SCAN_RETENTION_DAYS", 30),
		CapabilityOutcomeSeconds:    upstreamSourceRetentionSeconds("UPSTREAM_SOURCE_CAPABILITY_RETENTION_DAYS", 30),
		CostSnapshotSeconds:         upstreamSourceRetentionSeconds("UPSTREAM_SOURCE_COST_RETENTION_DAYS", 90),
		AnnouncementSeconds:         upstreamSourceRetentionSeconds("UPSTREAM_SOURCE_ANNOUNCEMENT_RETENTION_DAYS", 90),
		SubscriptionSnapshotSeconds: upstreamSourceRetentionSeconds("UPSTREAM_SOURCE_SUBSCRIPTION_RETENTION_DAYS", 90),
		NotificationDeliverySeconds: upstreamSourceRetentionSeconds("UPSTREAM_SOURCE_NOTIFICATION_RETENTION_DAYS", 90),
	}
}

func upstreamSourceRetentionSeconds(environmentName string, defaultDays int) int64 {
	days := common.GetEnvOrDefault(environmentName, defaultDays)
	if days <= 0 {
		return 0
	}
	return int64(days) * upstreamSourceRetentionDaySeconds
}

func CleanupUpstreamSourceMonitorHistory(now int64, policy UpstreamSourceRetentionPolicy) (UpstreamSourceRetentionCleanupResult, error) {
	result := UpstreamSourceRetentionCleanupResult{}
	err := DB.Transaction(func(tx *gorm.DB) error {
		if policy.CapabilityOutcomeSeconds > 0 {
			deleted := tx.Where("finished_at > 0 AND finished_at < ?", now-policy.CapabilityOutcomeSeconds).Delete(&UpstreamSourceCapabilityOutcome{})
			if deleted.Error != nil {
				return deleted.Error
			}
			result.CapabilityOutcomes = deleted.RowsAffected
		}
		if policy.CostSnapshotSeconds > 0 {
			deleted := tx.Where("collected_at < ?", now-policy.CostSnapshotSeconds).Delete(&UpstreamSourceCostSnapshot{})
			if deleted.Error != nil {
				return deleted.Error
			}
			result.CostSnapshots = deleted.RowsAffected
		}
		if policy.AnnouncementSeconds > 0 {
			cutoff := now - policy.AnnouncementSeconds
			lastID := 0
			for {
				var announcements []UpstreamSourceAnnouncement
				if err := tx.Select("id", "source_id", "source_key", "first_seen_at", "last_seen_at").
					Where("id > ? AND last_seen_at < ?", lastID, cutoff).
					Order("id").Limit(500).Find(&announcements).Error; err != nil {
					return err
				}
				if len(announcements) == 0 {
					break
				}
				identities := make([]UpstreamSourceAnnouncementIdentity, 0, len(announcements))
				for _, announcement := range announcements {
					identities = append(identities, UpstreamSourceAnnouncementIdentity{
						SourceID: announcement.SourceID, SourceKey: announcement.SourceKey,
						FirstSeenAt: announcement.FirstSeenAt, LastSeenAt: announcement.LastSeenAt,
					})
				}
				if err := tx.Clauses(clause.OnConflict{
					Columns:   []clause.Column{{Name: "source_id"}, {Name: "source_key"}},
					DoUpdates: clause.AssignmentColumns([]string{"last_seen_at"}),
				}).Create(&identities).Error; err != nil {
					return err
				}
				lastID = announcements[len(announcements)-1].Id
			}
			deleted := tx.Where("last_seen_at < ?", cutoff).Delete(&UpstreamSourceAnnouncement{})
			if deleted.Error != nil {
				return deleted.Error
			}
			result.Announcements = deleted.RowsAffected
		}
		if policy.SubscriptionSnapshotSeconds > 0 {
			type latestSubscriptionScan struct {
				SourceID int
				ScanID   int
			}
			var latestScans []latestSubscriptionScan
			if err := tx.Model(&UpstreamSourceSubscriptionUsageSnapshot{}).
				Select("source_id, MAX(scan_id) AS scan_id").Group("source_id").Scan(&latestScans).Error; err != nil {
				return err
			}
			latestScanIDs := make([]int, 0, len(latestScans))
			for _, latest := range latestScans {
				latestScanIDs = append(latestScanIDs, latest.ScanID)
			}
			query := tx.Where("collected_at < ?", now-policy.SubscriptionSnapshotSeconds)
			if len(latestScanIDs) > 0 {
				query = query.Where("scan_id NOT IN ?", latestScanIDs)
			}
			deleted := query.Delete(&UpstreamSourceSubscriptionUsageSnapshot{})
			if deleted.Error != nil {
				return deleted.Error
			}
			result.SubscriptionSnapshots = deleted.RowsAffected
		}
		if policy.NotificationDeliverySeconds > 0 {
			deleted := tx.Where("created_at < ?", now-policy.NotificationDeliverySeconds).Delete(&UpstreamSourceNotificationDelivery{})
			if deleted.Error != nil {
				return deleted.Error
			}
			result.NotificationDeliveries = deleted.RowsAffected
		}
		if policy.ScanSeconds <= 0 {
			return nil
		}

		query := tx.Where("finished_at > 0 AND finished_at < ?", now-policy.ScanSeconds)
		for _, table := range []any{
			&UpstreamSourceGroupChange{},
			&UpstreamSourceBalanceSnapshot{},
			&UpstreamSourceSubscriptionUsageSnapshot{},
			&UpstreamSourceAnnouncement{},
			&UpstreamSourceCostSnapshot{},
			&UpstreamSourceCapabilityOutcome{},
			&UpstreamSourceNotificationDelivery{},
		} {
			query = query.Where("id NOT IN (?)", tx.Model(table).Select("scan_id"))
		}
		deleted := query.Delete(&UpstreamSourceScan{})
		if deleted.Error != nil {
			return deleted.Error
		}
		result.Scans = deleted.RowsAffected
		return nil
	})
	return result, err
}
