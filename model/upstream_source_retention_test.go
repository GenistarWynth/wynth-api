package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCleanupUpstreamSourceMonitorHistoryRemovesExpiredRowsAndPreservesCurrentState(t *testing.T) {
	setupUpstreamSourceTestDB(t)
	source := UpstreamSource{Name: "retention", Type: UpstreamSourceTypeSub2API, BaseURL: "https://retention.example.com"}
	require.NoError(t, DB.Create(&source).Error)

	oldScan := UpstreamSourceScan{SourceID: source.Id, ScanType: UpstreamSourceScanTypeMonitor, Status: UpstreamSourceScanStatusSuccess, StartedAt: 100, FinishedAt: 110}
	balanceScan := UpstreamSourceScan{SourceID: source.Id, ScanType: UpstreamSourceScanTypeMonitor, Status: UpstreamSourceScanStatusSuccess, StartedAt: 150, FinishedAt: 160}
	latestSubscriptionScan := UpstreamSourceScan{SourceID: source.Id, ScanType: UpstreamSourceScanTypeMonitor, Status: UpstreamSourceScanStatusSuccess, StartedAt: 200, FinishedAt: 210}
	ledgerScan := UpstreamSourceScan{SourceID: source.Id, ScanType: UpstreamSourceScanTypeMonitor, Status: UpstreamSourceScanStatusSuccess, StartedAt: 300, FinishedAt: 310}
	require.NoError(t, DB.Create(&[]*UpstreamSourceScan{&oldScan, &balanceScan, &latestSubscriptionScan, &ledgerScan}).Error)
	require.NoError(t, DB.Create(&UpstreamSourceCapabilityOutcome{SourceID: source.Id, ScanID: oldScan.Id, Capability: UpstreamSourceCapabilityBalance, Status: UpstreamSourceCapabilityStatusSuccess, StartedAt: 100, FinishedAt: 110}).Error)
	require.NoError(t, DB.Create(&UpstreamSourceCostSnapshot{SourceID: source.Id, ScanID: oldScan.Id, Amount: 1, Currency: "USD", CollectedAt: 110}).Error)
	require.NoError(t, DB.Create(&UpstreamSourceAnnouncement{SourceID: source.Id, ScanID: oldScan.Id, SourceKey: "old", Title: "old", FirstSeenAt: 100, LastSeenAt: 110}).Error)
	require.NoError(t, DB.Create(&UpstreamSourceAnnouncementState{SourceID: source.Id, BaselineCompleted: true, CompletedAt: 100, UpdatedAt: 110}).Error)
	require.NoError(t, DB.Create(&UpstreamSourceNotificationDelivery{UserID: 1, SourceID: source.Id, ScanID: oldScan.Id, EventType: UpstreamSourceNotificationEventAnnouncementNew, EventKey: "old", Status: UpstreamSourceNotificationDeliverySuccess, Attempts: 1, CreatedAt: 110}).Error)
	require.NoError(t, DB.Create(&[]UpstreamSourceSubscriptionUsageSnapshot{
		{SourceID: source.Id, ScanID: oldScan.Id, SubscriptionKey: "plan", Window: "monthly", CollectedAt: 110},
		{SourceID: source.Id, ScanID: latestSubscriptionScan.Id, SubscriptionKey: "plan", Window: "monthly", CollectedAt: 210},
	}).Error)
	require.NoError(t, DB.Create(&UpstreamSourceBalanceSnapshot{SourceID: source.Id, ScanID: balanceScan.Id, Available: 5, Currency: "USD", CollectedAt: 160}).Error)
	require.NoError(t, DB.Create(&UpstreamSourceGroupChange{SourceID: source.Id, ScanID: ledgerScan.Id, ChangeType: UpstreamSourceGroupChangeAdded, UpstreamGroupID: "durable", CreatedAt: 310}).Error)

	result, err := CleanupUpstreamSourceMonitorHistory(1000, UpstreamSourceRetentionPolicy{
		ScanSeconds:                 500,
		CapabilityOutcomeSeconds:    500,
		CostSnapshotSeconds:         500,
		AnnouncementSeconds:         500,
		SubscriptionSnapshotSeconds: 500,
		NotificationDeliverySeconds: 500,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), result.CapabilityOutcomes)
	assert.Equal(t, int64(1), result.CostSnapshots)
	assert.Equal(t, int64(1), result.Announcements)
	assert.Equal(t, int64(1), result.SubscriptionSnapshots)
	assert.Equal(t, int64(1), result.NotificationDeliveries)

	var oldScanCount int64
	require.NoError(t, DB.Model(&UpstreamSourceScan{}).Where("id = ?", oldScan.Id).Count(&oldScanCount).Error)
	assert.Zero(t, oldScanCount)
	var latestScanCount int64
	require.NoError(t, DB.Model(&UpstreamSourceScan{}).Where("id IN ?", []int{balanceScan.Id, latestSubscriptionScan.Id, ledgerScan.Id}).Count(&latestScanCount).Error)
	assert.Equal(t, int64(3), latestScanCount)
	var subscriptionCount int64
	require.NoError(t, DB.Model(&UpstreamSourceSubscriptionUsageSnapshot{}).Count(&subscriptionCount).Error)
	assert.Equal(t, int64(1), subscriptionCount)
	var balanceCount int64
	require.NoError(t, DB.Model(&UpstreamSourceBalanceSnapshot{}).Count(&balanceCount).Error)
	assert.Equal(t, int64(1), balanceCount)
	var announcementStateCount int64
	require.NoError(t, DB.Model(&UpstreamSourceAnnouncementState{}).Count(&announcementStateCount).Error)
	assert.Equal(t, int64(1), announcementStateCount)
	var announcementIdentity UpstreamSourceAnnouncementIdentity
	require.NoError(t, DB.Where("source_id = ? AND source_key = ?", source.Id, "old").First(&announcementIdentity).Error)
	assert.Equal(t, int64(100), announcementIdentity.FirstSeenAt)
	var ledgerCount int64
	require.NoError(t, DB.Model(&UpstreamSourceGroupChange{}).Count(&ledgerCount).Error)
	assert.Equal(t, int64(1), ledgerCount)
}
