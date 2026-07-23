package service

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type UpstreamSourceLatestMonitorSnapshots struct {
	Balance           *model.UpstreamSourceBalanceSnapshot            `json:"balance"`
	Cost              *model.UpstreamSourceCostSnapshot               `json:"cost"`
	SubscriptionUsage []model.UpstreamSourceSubscriptionUsageSnapshot `json:"subscription_usage"`
}

func GetUpstreamSourceLatestMonitorSnapshots(sourceID int) (UpstreamSourceLatestMonitorSnapshots, error) {
	balance, err := model.GetLatestUpstreamSourceBalance(sourceID)
	if err != nil {
		return UpstreamSourceLatestMonitorSnapshots{}, err
	}
	cost, err := model.GetLatestUpstreamSourceCost(sourceID)
	if err != nil {
		return UpstreamSourceLatestMonitorSnapshots{}, err
	}
	usage, err := model.ListLatestUpstreamSourceSubscriptionUsage(sourceID)
	if err != nil {
		return UpstreamSourceLatestMonitorSnapshots{}, err
	}
	return UpstreamSourceLatestMonitorSnapshots{
		Balance:           balance,
		Cost:              cost,
		SubscriptionUsage: usage,
	}, nil
}

func persistUpstreamSourceBalance(sourceID int, scanID int, snapshot UpstreamBalanceSnapshot, now int64) error {
	if sourceID == 0 || scanID == 0 {
		return errors.New("source ID and scan ID are required")
	}
	if err := validateUpstreamSourceSnapshotNumber("balance", snapshot.Available); err != nil {
		return err
	}
	collectedAt := snapshot.CollectedAt
	if collectedAt == 0 {
		collectedAt = now
	}
	record := model.UpstreamSourceBalanceSnapshot{
		SourceID:    sourceID,
		ScanID:      scanID,
		Available:   snapshot.Available,
		Currency:    strings.TrimSpace(snapshot.Currency),
		CollectedAt: collectedAt,
	}
	if record.Currency == "" {
		record.Currency = "unknown"
	}
	return model.DB.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "source_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"scan_id",
			"available",
			"currency",
			"collected_at",
		}),
	}).Create(&record).Error
}

func persistUpstreamSourceCost(sourceID int, scanID int, snapshot UpstreamCostSnapshot, now int64) error {
	if sourceID == 0 || scanID == 0 {
		return errors.New("source ID and scan ID are required")
	}
	if err := validateUpstreamSourceSnapshotNumber("cost", snapshot.Amount); err != nil {
		return err
	}
	if snapshot.Amount < 0 {
		return errors.New("cost cannot be negative")
	}
	collectedAt := snapshot.CollectedAt
	if collectedAt == 0 {
		collectedAt = now
	}
	record := model.UpstreamSourceCostSnapshot{
		SourceID:    sourceID,
		ScanID:      scanID,
		Amount:      snapshot.Amount,
		Currency:    strings.TrimSpace(snapshot.Currency),
		PeriodStart: snapshot.PeriodStart,
		PeriodEnd:   snapshot.PeriodEnd,
		CollectedAt: collectedAt,
	}
	if record.Currency == "" {
		record.Currency = "unknown"
	}
	if record.PeriodStart > 0 && record.PeriodEnd > 0 && record.PeriodEnd < record.PeriodStart {
		return errors.New("cost period end precedes period start")
	}
	return model.DB.Create(&record).Error
}

func persistUpstreamSourceAnnouncements(sourceID int, scanID int, snapshot UpstreamAnnouncementSnapshot, now int64) (bool, int, error) {
	if sourceID == 0 || scanID == 0 {
		return false, 0, errors.New("source ID and scan ID are required")
	}
	collectedAt := snapshot.CollectedAt
	if collectedAt == 0 {
		collectedAt = now
	}
	baseline := false
	newCount := 0
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		if err := model.LockUpstreamSourceForScanTx(tx, sourceID); err != nil {
			return err
		}
		var state model.UpstreamSourceAnnouncementState
		err := tx.Where("source_id = ?", sourceID).First(&state).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		baseline = errors.Is(err, gorm.ErrRecordNotFound) || !state.BaselineCompleted

		records := make([]model.UpstreamSourceAnnouncement, 0, len(snapshot.Items))
		keys := make([]string, 0, len(snapshot.Items))
		seenKeys := make(map[string]struct{}, len(snapshot.Items))
		for _, item := range snapshot.Items {
			title := strings.TrimSpace(item.Title)
			content := strings.TrimSpace(item.Content)
			itemURL := strings.TrimSpace(item.URL)
			sourceKey := strings.TrimSpace(item.ID)
			if sourceKey == "" {
				stablePayload, err := common.Marshal(struct {
					Title       string `json:"title"`
					Content     string `json:"content"`
					URL         string `json:"url"`
					PublishedAt int64  `json:"published_at"`
				}{Title: title, Content: content, URL: itemURL, PublishedAt: item.PublishedAt})
				if err != nil {
					return err
				}
				hash := sha256.Sum256(stablePayload)
				sourceKey = fmt.Sprintf("sha256:%x", hash)
			}
			if _, duplicate := seenKeys[sourceKey]; duplicate {
				continue
			}
			seenKeys[sourceKey] = struct{}{}
			keys = append(keys, sourceKey)
			records = append(records, model.UpstreamSourceAnnouncement{
				SourceID:    sourceID,
				ScanID:      scanID,
				SourceKey:   sourceKey,
				Title:       title,
				Content:     content,
				URL:         itemURL,
				PublishedAt: item.PublishedAt,
				FirstSeenAt: collectedAt,
				LastSeenAt:  collectedAt,
			})
		}

		existingKeys := make(map[string]int64, len(keys))
		if len(keys) > 0 {
			var identities []model.UpstreamSourceAnnouncementIdentity
			if err := tx.Select("source_key", "first_seen_at").Where("source_id = ? AND source_key IN ?", sourceID, keys).Find(&identities).Error; err != nil {
				return err
			}
			for _, identity := range identities {
				existingKeys[identity.SourceKey] = identity.FirstSeenAt
			}
			// Existing display rows seed the identity ledger during upgrades from
			// Batch B, before every source has completed a post-migration scan.
			var existing []model.UpstreamSourceAnnouncement
			if err := tx.Select("source_key", "first_seen_at").Where("source_id = ? AND source_key IN ?", sourceID, keys).Find(&existing).Error; err != nil {
				return err
			}
			for _, item := range existing {
				if _, tracked := existingKeys[item.SourceKey]; !tracked {
					existingKeys[item.SourceKey] = item.FirstSeenAt
				}
			}
			for index := range records {
				firstSeenAt, exists := existingKeys[records[index].SourceKey]
				records[index].IsNew = !baseline && !exists
				if exists && firstSeenAt > 0 {
					records[index].FirstSeenAt = firstSeenAt
				}
				if records[index].IsNew {
					newCount++
				}
			}
			if err := tx.Clauses(clause.OnConflict{
				Columns: []clause.Column{{Name: "source_id"}, {Name: "source_key"}},
				DoUpdates: clause.AssignmentColumns([]string{
					"scan_id",
					"title",
					"content",
					"url",
					"published_at",
					"last_seen_at",
				}),
			}).Create(&records).Error; err != nil {
				return err
			}
			identityRecords := make([]model.UpstreamSourceAnnouncementIdentity, 0, len(records))
			for _, record := range records {
				identityRecords = append(identityRecords, model.UpstreamSourceAnnouncementIdentity{
					SourceID: sourceID, SourceKey: record.SourceKey,
					FirstSeenAt: record.FirstSeenAt, LastSeenAt: record.LastSeenAt,
				})
			}
			if err := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "source_id"}, {Name: "source_key"}},
				DoUpdates: clause.AssignmentColumns([]string{"last_seen_at"}),
			}).Create(&identityRecords).Error; err != nil {
				return err
			}
		}

		completedAt := state.CompletedAt
		if completedAt == 0 {
			completedAt = collectedAt
		}
		state = model.UpstreamSourceAnnouncementState{
			SourceID:          sourceID,
			BaselineCompleted: true,
			CompletedAt:       completedAt,
			UpdatedAt:         collectedAt,
		}
		return tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "source_id"}},
			DoUpdates: clause.AssignmentColumns([]string{"baseline_completed", "updated_at"}),
		}).Create(&state).Error
	})
	return baseline, newCount, err
}

func persistUpstreamSourceSubscriptionUsage(sourceID int, scanID int, snapshot UpstreamSubscriptionUsageSnapshot, now int64) (int, error) {
	if sourceID == 0 || scanID == 0 {
		return 0, errors.New("source ID and scan ID are required")
	}
	collectedAt := snapshot.CollectedAt
	if collectedAt == 0 {
		collectedAt = now
	}
	records := make([]model.UpstreamSourceSubscriptionUsageSnapshot, 0, len(snapshot.Subscriptions)*3)
	for _, subscription := range snapshot.Subscriptions {
		sourceKey := strings.TrimSpace(subscription.SourceKey)
		if sourceKey == "" {
			stablePayload, err := common.Marshal(struct {
				Name      string `json:"name"`
				ExpiresAt int64  `json:"expires_at"`
			}{Name: strings.TrimSpace(subscription.Name), ExpiresAt: subscription.ExpiresAt})
			if err != nil {
				return 0, err
			}
			hash := sha256.Sum256(stablePayload)
			sourceKey = fmt.Sprintf("sha256:%x", hash)
		}
		windows := []struct {
			name   string
			window *UpstreamSubscriptionUsageWindow
		}{
			{name: "daily", window: subscription.Daily},
			{name: "weekly", window: subscription.Weekly},
			{name: "monthly", window: subscription.Monthly},
		}
		hasWindow := false
		for _, item := range windows {
			if item.window == nil {
				continue
			}
			hasWindow = true
			if err := validateUpstreamSourceUsageWindow(item.name, item.window); err != nil {
				return 0, err
			}
			remainingPercent := item.window.RemainingPercent
			if remainingPercent == nil && item.window.Limit != nil && *item.window.Limit > 0 {
				computed := float64(0)
				if item.window.Remaining != nil {
					computed = *item.window.Remaining / *item.window.Limit * 100
				} else {
					computed = (*item.window.Limit - item.window.Used) / *item.window.Limit * 100
				}
				if computed < 0 {
					computed = 0
				} else if computed > 100 {
					computed = 100
				}
				remainingPercent = &computed
			}
			records = append(records, model.UpstreamSourceSubscriptionUsageSnapshot{
				SourceID:         sourceID,
				ScanID:           scanID,
				SubscriptionKey:  sourceKey,
				Name:             strings.TrimSpace(subscription.Name),
				Window:           item.name,
				Unit:             strings.TrimSpace(item.window.Unit),
				Used:             item.window.Used,
				Limit:            item.window.Limit,
				Remaining:        item.window.Remaining,
				RemainingPercent: remainingPercent,
				PeriodStart:      item.window.PeriodStart,
				PeriodEnd:        item.window.PeriodEnd,
				ExpiresAt:        subscription.ExpiresAt,
				CollectedAt:      collectedAt,
				RawData:          subscription.RawData,
			})
		}
		if !hasWindow {
			records = append(records, model.UpstreamSourceSubscriptionUsageSnapshot{
				SourceID:        sourceID,
				ScanID:          scanID,
				SubscriptionKey: sourceKey,
				Name:            strings.TrimSpace(subscription.Name),
				Window:          "summary",
				ExpiresAt:       subscription.ExpiresAt,
				CollectedAt:     collectedAt,
				RawData:         subscription.RawData,
			})
		}
	}
	if len(records) == 0 {
		return 0, nil
	}
	if err := model.DB.Create(&records).Error; err != nil {
		return 0, err
	}
	return len(records), nil
}

func applyUpstreamSourceRateGroupSnapshot(source *model.UpstreamSource, scanID int, snapshot UpstreamRateGroupSnapshot, now int64) (bool, int, error) {
	if source == nil || source.Id == 0 || scanID == 0 {
		return false, 0, errors.New("persisted source and scan ID are required")
	}
	collectedAt := snapshot.CollectedAt
	if collectedAt == 0 {
		collectedAt = now
	}
	var application upstreamSourceGroupApplication
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		var err error
		application, err = applyUpstreamSourceGroupsTx(tx, source, scanID, snapshot.Groups, collectedAt)
		return err
	})
	if err != nil {
		return false, 0, err
	}
	if application.StaleCount > 0 || application.ChannelLabelsChanged {
		model.InitChannelCache()
	}
	return application.Baseline, application.ChangeCount, nil
}

func validateUpstreamSourceUsageWindow(name string, window *UpstreamSubscriptionUsageWindow) error {
	if window == nil {
		return nil
	}
	values := []struct {
		name  string
		value *float64
	}{
		{name: "used", value: &window.Used},
		{name: "limit", value: window.Limit},
		{name: "remaining", value: window.Remaining},
		{name: "remaining percent", value: window.RemainingPercent},
	}
	for _, item := range values {
		if item.value == nil {
			continue
		}
		if err := validateUpstreamSourceSnapshotNumber(name+" "+item.name, *item.value); err != nil {
			return err
		}
		if *item.value < 0 {
			return fmt.Errorf("%s %s cannot be negative", name, item.name)
		}
	}
	if window.PeriodStart > 0 && window.PeriodEnd > 0 && window.PeriodEnd < window.PeriodStart {
		return fmt.Errorf("%s period end precedes period start", name)
	}
	return nil
}

func validateUpstreamSourceSnapshotNumber(name string, value float64) error {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return fmt.Errorf("%s is not finite", name)
	}
	return nil
}
