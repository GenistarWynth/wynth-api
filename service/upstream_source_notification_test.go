package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func createUpstreamSourceNotificationAdmin(t *testing.T) model.User {
	t.Helper()
	user := model.User{
		Username: "monitor-admin",
		Password: "password",
		Email:    "admin@example.com",
		Role:     common.RoleAdminUser,
		Status:   common.UserStatusEnabled,
		Group:    "default",
	}
	require.NoError(t, model.DB.Create(&user).Error)
	return user
}

func TestUpstreamSourceNotificationBatchesSameScanRateChanges(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	createUpstreamSourceNotificationAdmin(t)
	source := createDiscoveryTestSource(t)
	scan, err := model.CreateUpstreamSourceScan(source.Id, model.UpstreamSourceScanTypeMonitor, 100)
	require.NoError(t, err)
	require.NoError(t, model.DB.Create(&[]model.UpstreamSourceGroupChange{
		{SourceID: source.Id, ScanID: scan.Id, ChangeType: model.UpstreamSourceGroupChangeAdded, UpstreamGroupID: "standard", UpstreamGroupName: "Standard", CreatedAt: 101},
		{SourceID: source.Id, ScanID: scan.Id, ChangeType: model.UpstreamSourceGroupChangeRateChanged, UpstreamGroupID: "premium", UpstreamGroupName: "Premium", CreatedAt: 102},
	}).Error)

	deliveries := make([]dto.Notify, 0)
	notifier := UpstreamSourceMonitorNotifier{
		Now: func() int64 { return 200 },
		Send: func(userID int, userEmail string, setting dto.UserSetting, notification dto.Notify) error {
			deliveries = append(deliveries, notification)
			return nil
		},
	}

	require.NoError(t, notifier.NotifyScan(context.Background(), &source, scan.Id))
	require.Len(t, deliveries, 1)
	assert.Contains(t, deliveries[0].Content, "Standard")
	assert.Contains(t, deliveries[0].Content, "Premium")

	var audit []model.UpstreamSourceNotificationDelivery
	require.NoError(t, model.DB.Find(&audit).Error)
	require.Len(t, audit, 1)
	assert.Equal(t, model.UpstreamSourceNotificationDeliverySuccess, audit[0].Status)
	assert.Equal(t, model.UpstreamSourceNotificationEventRateGroupBatch, audit[0].EventType)
}

func TestUpstreamSourceNotificationKeepsImplicitDefaultsWhenRulesTargetAnotherSource(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	admin := createUpstreamSourceNotificationAdmin(t)
	source := createDiscoveryTestSource(t)
	require.NoError(t, model.DB.Create(&model.UpstreamSourceNotificationSubscription{
		UserID: admin.Id, SourceID: source.Id + 100, EventType: model.UpstreamSourceNotificationEventAll, Enabled: true,
	}).Error)
	scan, err := model.CreateUpstreamSourceScan(source.Id, model.UpstreamSourceScanTypeMonitor, 100)
	require.NoError(t, err)
	require.NoError(t, model.DB.Create(&model.UpstreamSourceGroupChange{
		SourceID: source.Id, ScanID: scan.Id, ChangeType: model.UpstreamSourceGroupChangeAdded, UpstreamGroupID: "standard", CreatedAt: 101,
	}).Error)

	deliveryCount := 0
	notifier := UpstreamSourceMonitorNotifier{
		Now: func() int64 { return 200 },
		Send: func(userID int, userEmail string, setting dto.UserSetting, notification dto.Notify) error {
			deliveryCount++
			return nil
		},
	}

	require.NoError(t, notifier.NotifyScan(context.Background(), &source, scan.Id))
	assert.Equal(t, 1, deliveryCount)
}

func TestUpstreamSourceNotificationCooldownSuppressesRepeatedSubscriptionAlert(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	createUpstreamSourceNotificationAdmin(t)
	source := createDiscoveryTestSource(t)
	remainingPercent := 10.0
	now := int64(1000)
	deliveryCount := 0
	notifier := UpstreamSourceMonitorNotifier{
		Now: func() int64 { return now },
		Send: func(userID int, userEmail string, setting dto.UserSetting, notification dto.Notify) error {
			deliveryCount++
			return nil
		},
	}

	for _, scanStart := range []int64{900, 1100} {
		scan, err := model.CreateUpstreamSourceScan(source.Id, model.UpstreamSourceScanTypeMonitor, scanStart)
		require.NoError(t, err)
		require.NoError(t, model.DB.Create(&model.UpstreamSourceSubscriptionUsageSnapshot{
			SourceID:         source.Id,
			ScanID:           scan.Id,
			SubscriptionKey:  "main",
			Name:             "Main plan",
			Window:           "monthly",
			RemainingPercent: &remainingPercent,
			CollectedAt:      scanStart,
		}).Error)
		require.NoError(t, notifier.NotifyScan(context.Background(), &source, scan.Id))
		now += 60
	}

	assert.Equal(t, 1, deliveryCount)
	var cooldown model.UpstreamSourceNotificationCooldown
	require.NoError(t, model.DB.Where("event_type = ?", model.UpstreamSourceNotificationEventSubscriptionRemainingLow).First(&cooldown).Error)
	assert.Equal(t, int64(1000), cooldown.LastDeliveredAt)
}

func TestUpstreamSourceNotificationRetriesThenAuditsSanitizedFailure(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	createUpstreamSourceNotificationAdmin(t)
	source := createDiscoveryTestSource(t)
	scan, err := model.CreateUpstreamSourceScan(source.Id, model.UpstreamSourceScanTypeMonitor, 100)
	require.NoError(t, err)
	require.NoError(t, model.DB.Create(&model.UpstreamSourceGroupChange{
		SourceID: source.Id, ScanID: scan.Id, ChangeType: model.UpstreamSourceGroupChangeRemoved, UpstreamGroupID: "legacy", CreatedAt: 101,
	}).Error)

	attempts := 0
	backoffs := make([]time.Duration, 0)
	notifier := UpstreamSourceMonitorNotifier{
		Now: func() int64 { return 200 },
		Sleep: func(delay time.Duration) {
			backoffs = append(backoffs, delay)
		},
		Send: func(userID int, userEmail string, setting dto.UserSetting, notification dto.Notify) error {
			attempts++
			return errors.New("delivery failed password=super-secret")
		},
	}

	require.NoError(t, notifier.NotifyScan(context.Background(), &source, scan.Id))
	assert.Equal(t, 3, attempts)
	assert.Equal(t, []time.Duration{100 * time.Millisecond, 200 * time.Millisecond}, backoffs)

	var audit model.UpstreamSourceNotificationDelivery
	require.NoError(t, model.DB.First(&audit).Error)
	assert.Equal(t, model.UpstreamSourceNotificationDeliveryFailed, audit.Status)
	assert.Equal(t, 3, audit.Attempts)
	assert.Contains(t, audit.ErrorSummary, "[redacted]")
	assert.NotContains(t, audit.ErrorSummary, "super-secret")
}

func TestUpstreamSourceNotificationAuditsMissingTransportAsFailure(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	admin := createUpstreamSourceNotificationAdmin(t)
	settingJSON, err := common.Marshal(dto.UserSetting{NotifyType: dto.NotifyTypeWebhook})
	require.NoError(t, err)
	require.NoError(t, model.DB.Model(&admin).Updates(map[string]interface{}{
		"email":   "",
		"setting": string(settingJSON),
	}).Error)
	source := createDiscoveryTestSource(t)
	scan, err := model.CreateUpstreamSourceScan(source.Id, model.UpstreamSourceScanTypeMonitor, 100)
	require.NoError(t, err)
	remainingPercent := 5.0
	require.NoError(t, model.DB.Create(&model.UpstreamSourceSubscriptionUsageSnapshot{
		SourceID: source.Id, ScanID: scan.Id, SubscriptionKey: "main", Window: "monthly",
		RemainingPercent: &remainingPercent, CollectedAt: 100,
	}).Error)

	notifier := UpstreamSourceMonitorNotifier{
		Now:   func() int64 { return 200 },
		Sleep: func(time.Duration) {},
	}
	require.NoError(t, notifier.NotifyScan(context.Background(), &source, scan.Id))

	var audit model.UpstreamSourceNotificationDelivery
	require.NoError(t, model.DB.First(&audit).Error)
	assert.Equal(t, model.UpstreamSourceNotificationDeliveryFailed, audit.Status)
	assert.Zero(t, audit.Attempts)
	assert.Contains(t, audit.ErrorSummary, "webhook URL")
	var cooldownCount int64
	require.NoError(t, model.DB.Model(&model.UpstreamSourceNotificationCooldown{}).Count(&cooldownCount).Error)
	assert.Zero(t, cooldownCount)
}

func TestUpstreamSourceNewAnnouncementAfterBaselineNotifiesOnce(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	createUpstreamSourceNotificationAdmin(t)
	source := createDiscoveryTestSource(t)
	deliveryCount := 0
	notifier := UpstreamSourceMonitorNotifier{
		Now: func() int64 { return 300 },
		Send: func(userID int, userEmail string, setting dto.UserSetting, notification dto.Notify) error {
			deliveryCount++
			return nil
		},
	}

	baselineScan, err := model.CreateUpstreamSourceScan(source.Id, model.UpstreamSourceScanTypeMonitor, 100)
	require.NoError(t, err)
	baseline, _, err := persistUpstreamSourceAnnouncements(source.Id, baselineScan.Id, UpstreamAnnouncementSnapshot{
		Items:       []UpstreamAnnouncement{{ID: "old", Title: "Old notice"}},
		CollectedAt: 100,
	}, 100)
	require.NoError(t, err)
	require.True(t, baseline)
	require.NoError(t, notifier.NotifyScan(context.Background(), &source, baselineScan.Id))
	assert.Zero(t, deliveryCount)

	newScan, err := model.CreateUpstreamSourceScan(source.Id, model.UpstreamSourceScanTypeMonitor, 200)
	require.NoError(t, err)
	baseline, newCount, err := persistUpstreamSourceAnnouncements(source.Id, newScan.Id, UpstreamAnnouncementSnapshot{
		Items: []UpstreamAnnouncement{
			{ID: "old", Title: "Old notice"},
			{ID: "new", Title: "New notice", Content: "Maintenance window"},
		},
		CollectedAt: 200,
	}, 200)
	require.NoError(t, err)
	assert.False(t, baseline)
	assert.Equal(t, 1, newCount)
	require.NoError(t, notifier.NotifyScan(context.Background(), &source, newScan.Id))
	require.NoError(t, notifier.NotifyScan(context.Background(), &source, newScan.Id))
	assert.Equal(t, 1, deliveryCount)

	var persistedAnnouncement model.UpstreamSourceAnnouncement
	require.NoError(t, model.DB.Where("source_id = ? AND source_key = ?", source.Id, "new").First(&persistedAnnouncement).Error)
	assert.False(t, persistedAnnouncement.IsNew)

	// Delivery audits are retained for operations visibility, not announcement idempotency.
	require.NoError(t, model.DB.Where("source_id = ?", source.Id).Delete(&model.UpstreamSourceNotificationDelivery{}).Error)
	laterScan, err := model.CreateUpstreamSourceScan(source.Id, model.UpstreamSourceScanTypeMonitor, 400)
	require.NoError(t, err)
	_, _, err = persistUpstreamSourceAnnouncements(source.Id, laterScan.Id, UpstreamAnnouncementSnapshot{
		Items: []UpstreamAnnouncement{
			{ID: "old", Title: "Old notice"},
			{ID: "new", Title: "New notice", Content: "Maintenance window"},
		},
		CollectedAt: 400,
	}, 400)
	require.NoError(t, err)
	require.NoError(t, notifier.NotifyScan(context.Background(), &source, laterScan.Id))
	assert.Equal(t, 1, deliveryCount)
}
