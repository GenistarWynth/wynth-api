package service

import (
	"testing"

	"github.com/QuantumNous/new-api/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPersistUpstreamSourceBalanceAndCostSnapshots(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	source := createDiscoveryTestSource(t)
	scan := createUpstreamSourceCollectionTestScan(t, source.Id, 100)

	require.NoError(t, persistUpstreamSourceBalance(source.Id, scan.Id, UpstreamBalanceSnapshot{
		Available:   12.5,
		Currency:    "USD",
		CollectedAt: 101,
	}, 110))
	require.NoError(t, persistUpstreamSourceBalance(source.Id, scan.Id, UpstreamBalanceSnapshot{
		Available: 14.25,
		Currency:  "USD",
	}, 120))
	require.NoError(t, persistUpstreamSourceCost(source.Id, scan.Id, UpstreamCostSnapshot{
		Amount:      3.5,
		Currency:    "USD",
		PeriodStart: 10,
		PeriodEnd:   20,
		CollectedAt: 121,
	}, 130))
	require.NoError(t, persistUpstreamSourceCost(source.Id, scan.Id, UpstreamCostSnapshot{
		Amount:      4.5,
		Currency:    "USD",
		PeriodStart: 20,
		PeriodEnd:   30,
	}, 140))

	var balances []model.UpstreamSourceBalanceSnapshot
	require.NoError(t, model.DB.Where("source_id = ?", source.Id).Find(&balances).Error)
	require.Len(t, balances, 1)
	assert.Equal(t, 14.25, balances[0].Available)
	assert.Equal(t, int64(120), balances[0].CollectedAt)
	var costs []model.UpstreamSourceCostSnapshot
	require.NoError(t, model.DB.Where("source_id = ?", source.Id).Order("id").Find(&costs).Error)
	require.Len(t, costs, 2)
	assert.Equal(t, 3.5, costs[0].Amount)
	assert.Equal(t, int64(121), costs[0].CollectedAt)
	assert.Equal(t, 4.5, costs[1].Amount)
	assert.Equal(t, int64(140), costs[1].CollectedAt)
}

func TestPersistUpstreamSourceAnnouncementsBaselinesThenMarksNewItems(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	source := createDiscoveryTestSource(t)
	baselineScan := createUpstreamSourceCollectionTestScan(t, source.Id, 100)

	baseline, newCount, err := persistUpstreamSourceAnnouncements(source.Id, baselineScan.Id, UpstreamAnnouncementSnapshot{
		Items: []UpstreamAnnouncement{
			{ID: "remote-1", Title: "Existing remote", Content: "one", PublishedAt: 50},
			{Title: "Existing hash", Content: "two"},
		},
		CollectedAt: 101,
	}, 110)
	require.NoError(t, err)
	assert.True(t, baseline)
	assert.Zero(t, newCount)

	var initial []model.UpstreamSourceAnnouncement
	require.NoError(t, model.DB.Where("source_id = ?", source.Id).Order("id").Find(&initial).Error)
	require.Len(t, initial, 2)
	assert.False(t, initial[0].IsNew)
	assert.False(t, initial[1].IsNew)
	assert.Equal(t, "remote-1", initial[0].SourceKey)
	assert.Contains(t, initial[1].SourceKey, "sha256:")
	var state model.UpstreamSourceAnnouncementState
	require.NoError(t, model.DB.First(&state, "source_id = ?", source.Id).Error)
	assert.True(t, state.BaselineCompleted)
	assert.Equal(t, int64(101), state.CompletedAt)

	laterScan := createUpstreamSourceCollectionTestScan(t, source.Id, 200)
	baseline, newCount, err = persistUpstreamSourceAnnouncements(source.Id, laterScan.Id, UpstreamAnnouncementSnapshot{
		Items: []UpstreamAnnouncement{
			{ID: "remote-1", Title: "Existing remote edited", Content: "one", PublishedAt: 50},
			{Title: "Existing hash", Content: "two"},
			{ID: "remote-2", Title: "New remote", Content: "three", PublishedAt: 190},
		},
		CollectedAt: 201,
	}, 210)
	require.NoError(t, err)
	assert.False(t, baseline)
	assert.Equal(t, 1, newCount)

	var announcements []model.UpstreamSourceAnnouncement
	require.NoError(t, model.DB.Where("source_id = ?", source.Id).Order("source_key").Find(&announcements).Error)
	require.Len(t, announcements, 3)
	newRows := 0
	for _, announcement := range announcements {
		if announcement.IsNew {
			newRows++
			assert.Equal(t, "remote-2", announcement.SourceKey)
		}
	}
	assert.Equal(t, 1, newRows)
}

func TestPersistUpstreamSourceAnnouncementsDoesNotResurrectAfterRetention(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	source := createDiscoveryTestSource(t)
	baselineScan := createUpstreamSourceCollectionTestScan(t, source.Id, 100)
	baseline, newCount, err := persistUpstreamSourceAnnouncements(source.Id, baselineScan.Id, UpstreamAnnouncementSnapshot{
		Items:       []UpstreamAnnouncement{{ID: "still-current", Title: "Existing notice"}},
		CollectedAt: 100,
	}, 100)
	require.NoError(t, err)
	assert.True(t, baseline)
	assert.Zero(t, newCount)

	cleanup, err := model.CleanupUpstreamSourceMonitorHistory(1000, model.UpstreamSourceRetentionPolicy{AnnouncementSeconds: 500})
	require.NoError(t, err)
	assert.Equal(t, int64(1), cleanup.Announcements)

	resumeScan := createUpstreamSourceCollectionTestScan(t, source.Id, 1100)
	baseline, newCount, err = persistUpstreamSourceAnnouncements(source.Id, resumeScan.Id, UpstreamAnnouncementSnapshot{
		Items:       []UpstreamAnnouncement{{ID: "still-current", Title: "Existing notice"}},
		CollectedAt: 1100,
	}, 1100)
	require.NoError(t, err)
	assert.False(t, baseline)
	assert.Zero(t, newCount)

	var announcement model.UpstreamSourceAnnouncement
	require.NoError(t, model.DB.Where("source_id = ? AND source_key = ?", source.Id, "still-current").First(&announcement).Error)
	assert.False(t, announcement.IsNew)
	assert.Equal(t, int64(100), announcement.FirstSeenAt)
}

func TestPersistUpstreamSourceSubscriptionUsageStoresRawWindows(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	source := createDiscoveryTestSource(t)
	scan := createUpstreamSourceCollectionTestScan(t, source.Id, 100)
	dailyLimit, dailyRemaining, dailyRemainingPercent := 10.0, 8.0, 80.0
	weeklyLimit, weeklyRemaining, weeklyRemainingPercent := 100.0, 75.0, 75.0
	monthlyLimit, monthlyRemaining, monthlyRemainingPercent := 200.0, 150.0, 75.0

	count, err := persistUpstreamSourceSubscriptionUsage(source.Id, scan.Id, UpstreamSubscriptionUsageSnapshot{
		Subscriptions: []UpstreamSubscriptionUsage{{
			SourceKey: "subscription-1",
			Name:      "Pro",
			ExpiresAt: 999,
			Daily: &UpstreamSubscriptionUsageWindow{
				Used: 2, Limit: &dailyLimit, Remaining: &dailyRemaining, RemainingPercent: &dailyRemainingPercent,
				Unit: "USD", PeriodStart: 100, PeriodEnd: 200,
			},
			Weekly: &UpstreamSubscriptionUsageWindow{
				Used: 25, Limit: &weeklyLimit, Remaining: &weeklyRemaining, RemainingPercent: &weeklyRemainingPercent,
				Unit: "USD", PeriodStart: 100, PeriodEnd: 800,
			},
			Monthly: &UpstreamSubscriptionUsageWindow{
				Used: 50, Limit: &monthlyLimit, Remaining: &monthlyRemaining, RemainingPercent: &monthlyRemainingPercent,
				Unit: "USD", PeriodStart: 100, PeriodEnd: 3100,
			},
			RawData: `{"id":1,"daily":{"used_usd":2}}`,
		}},
		CollectedAt: 101,
	}, 110)
	require.NoError(t, err)
	assert.Equal(t, 3, count)

	var windows []model.UpstreamSourceSubscriptionUsageSnapshot
	require.NoError(t, model.DB.Where("source_id = ?", source.Id).Order("window").Find(&windows).Error)
	require.Len(t, windows, 3)
	assert.Equal(t, []string{"daily", "monthly", "weekly"}, []string{windows[0].Window, windows[1].Window, windows[2].Window})
	assert.Equal(t, `{"id":1,"daily":{"used_usd":2}}`, windows[0].RawData)
	require.NotNil(t, windows[0].RemainingPercent)
	assert.Equal(t, 80.0, *windows[0].RemainingPercent)
}

func TestMonitorRateCollectorUsesSharedDiscoveryLedgerRules(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	source := createDiscoveryTestSource(t)
	rateOne, rateTwo, rateThree := 1.0, 2.0, 3.0

	baselineScan := createUpstreamSourceCollectionTestScan(t, source.Id, 100)
	baseline, count, err := applyUpstreamSourceRateGroupSnapshot(&source, baselineScan.Id, UpstreamRateGroupSnapshot{
		Groups: []UpstreamGroup{
			{ID: "a", Name: "A", RateMultiplier: &rateOne, EffectiveRateMultiplier: &rateOne},
			{ID: "b", Name: "B", RateMultiplier: &rateOne, EffectiveRateMultiplier: &rateOne},
		},
	}, 101)
	require.NoError(t, err)
	assert.True(t, baseline)
	assert.Zero(t, count)
	require.NoError(t, model.FinishUpstreamSourceScanTx(model.DB, baselineScan.Id, model.UpstreamSourceScanStatusSuccess, false, 102, ""))
	require.NoError(t, model.DB.Create(&model.UpstreamSourceCapabilityOutcome{
		SourceID: source.Id, ScanID: baselineScan.Id, Capability: model.UpstreamSourceCapabilityRateGroup,
		Status: model.UpstreamSourceCapabilityStatusSuccess, Baseline: true, StartedAt: 100, FinishedAt: 102,
	}).Error)
	require.NoError(t, model.DB.Model(&model.UpstreamSourceChannelMapping{}).
		Where("source_id = ? AND upstream_group_id = ?", source.Id, "a").
		Updates(map[string]any{"sync_enabled": false, "upstream_key_id": "key-a", "local_channel_id": 123}).Error)

	changeScan := createUpstreamSourceCollectionTestScan(t, source.Id, 200)
	baseline, count, err = applyUpstreamSourceRateGroupSnapshot(&source, changeScan.Id, UpstreamRateGroupSnapshot{
		Groups: []UpstreamGroup{
			{ID: "a", Name: "A", RateMultiplier: &rateTwo, EffectiveRateMultiplier: &rateTwo},
			{ID: "c", Name: "C", RateMultiplier: &rateThree, EffectiveRateMultiplier: &rateThree},
		},
	}, 201)
	require.NoError(t, err)
	assert.False(t, baseline)
	assert.Equal(t, 3, count)
	require.NoError(t, model.FinishUpstreamSourceScanTx(model.DB, changeScan.Id, model.UpstreamSourceScanStatusSuccess, false, 202, ""))
	require.NoError(t, model.DB.Create(&model.UpstreamSourceCapabilityOutcome{
		SourceID: source.Id, ScanID: changeScan.Id, Capability: model.UpstreamSourceCapabilityRateGroup,
		Status: model.UpstreamSourceCapabilityStatusSuccess, StartedAt: 200, FinishedAt: 202,
	}).Error)

	restoreScan := createUpstreamSourceCollectionTestScan(t, source.Id, 300)
	baseline, count, err = applyUpstreamSourceRateGroupSnapshot(&source, restoreScan.Id, UpstreamRateGroupSnapshot{
		Groups: []UpstreamGroup{
			{ID: "a", Name: "A", RateMultiplier: &rateTwo, EffectiveRateMultiplier: &rateTwo},
			{ID: "b", Name: "B", RateMultiplier: &rateOne, EffectiveRateMultiplier: &rateOne},
			{ID: "c", Name: "C", RateMultiplier: &rateThree, EffectiveRateMultiplier: &rateThree},
		},
	}, 301)
	require.NoError(t, err)
	assert.False(t, baseline)
	assert.Equal(t, 1, count)

	var changes []model.UpstreamSourceGroupChange
	require.NoError(t, model.DB.Where("source_id = ?", source.Id).Order("id").Find(&changes).Error)
	changeTypes := make([]string, 0, len(changes))
	for _, change := range changes {
		changeTypes = append(changeTypes, change.ChangeType)
	}
	assert.ElementsMatch(t, []string{
		model.UpstreamSourceGroupChangeRateChanged,
		model.UpstreamSourceGroupChangeAdded,
		model.UpstreamSourceGroupChangeRemoved,
		model.UpstreamSourceGroupChangeRestored,
	}, changeTypes)

	var preserved model.UpstreamSourceChannelMapping
	require.NoError(t, model.DB.Where("source_id = ? AND upstream_group_id = ?", source.Id, "a").First(&preserved).Error)
	assert.False(t, preserved.SyncEnabled)
	assert.Equal(t, "key-a", preserved.UpstreamKeyID)
	assert.Equal(t, 123, preserved.LocalChannelID)
}

func createUpstreamSourceCollectionTestScan(t *testing.T, sourceID int, startedAt int64) model.UpstreamSourceScan {
	t.Helper()
	scan, err := model.CreateUpstreamSourceScan(sourceID, model.UpstreamSourceScanTypeMonitor, startedAt)
	require.NoError(t, err)
	return *scan
}
