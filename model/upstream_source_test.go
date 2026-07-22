package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupUpstreamSourceTestDB(t *testing.T) {
	t.Helper()

	oldDB := DB
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	DB = db
	t.Cleanup(func() {
		DB = oldDB
	})

	require.NoError(t, DB.AutoMigrate(
		&UpstreamSource{},
		&UpstreamSourceSession{},
		&UpstreamSourceChannelMapping{},
		&UpstreamSourceScan{},
		&UpstreamSourceGroupChange{},
		&UpstreamSourceBalanceSnapshot{},
		&UpstreamSourceCostSnapshot{},
		&UpstreamSourceAnnouncement{},
		&UpstreamSourceAnnouncementIdentity{},
		&UpstreamSourceAnnouncementState{},
		&UpstreamSourceSubscriptionUsageSnapshot{},
		&UpstreamSourceCapabilityOutcome{},
		&UpstreamSourceNotificationSubscription{},
		&UpstreamSourceNotificationCooldown{},
		&UpstreamSourceNotificationDelivery{},
		&Channel{},
		&Ability{},
	))
}

func TestBackfillUpstreamSourceMonitorDefaultsKeepsLegacyRowsDisabled(t *testing.T) {
	setupUpstreamSourceTestDB(t)
	source := UpstreamSource{
		Name:    "legacy-source",
		Type:    UpstreamSourceTypeSub2API,
		Status:  UpstreamSourceStatusEnabled,
		BaseURL: "https://legacy.example.com",
	}
	require.NoError(t, DB.Create(&source).Error)
	require.NoError(t, DB.Exec(`UPDATE upstream_sources SET monitor_enabled = NULL, monitor_interval_minutes = NULL, next_monitor_at = NULL, current_monitor_token = NULL, monitor_started_at = NULL, last_monitor_time = NULL WHERE id = ?`, source.Id).Error)

	require.NoError(t, backfillUpstreamSourceMonitorDefaults())

	var reloaded UpstreamSource
	require.NoError(t, DB.First(&reloaded, source.Id).Error)
	assert.False(t, reloaded.MonitorEnabled)
	assert.Zero(t, reloaded.MonitorIntervalMinutes)
	assert.Zero(t, reloaded.NextMonitorAt)
	assert.Empty(t, reloaded.CurrentMonitorToken)
	assert.Zero(t, reloaded.MonitorStartedAt)
	assert.Zero(t, reloaded.LastMonitorTime)
}

func TestUpstreamSourceRedactedResponseOmitsAuthConfig(t *testing.T) {
	setupUpstreamSourceTestDB(t)

	source := UpstreamSource{
		Name:         "source-a",
		Type:         UpstreamSourceTypeSub2API,
		Status:       UpstreamSourceStatusEnabled,
		BaseURL:      "https://example.com",
		RelayBaseURL: "https://example.com",
		AuthConfig:   `{"email":"admin@example.com","password":"secret"}`,
	}
	payload, err := common.Marshal(source)
	require.NoError(t, err)
	assert.NotContains(t, string(payload), "secret")
	assert.NotContains(t, string(payload), "AuthConfig")
}

func TestUpstreamSourceMappingPreservesSyncFieldsOnDiscoveryUpsert(t *testing.T) {
	setupUpstreamSourceTestDB(t)

	source := UpstreamSource{
		Name:         "source-a",
		Type:         UpstreamSourceTypeSub2API,
		Status:       UpstreamSourceStatusEnabled,
		BaseURL:      "https://example.com",
		RelayBaseURL: "https://example.com",
	}
	require.NoError(t, DB.Create(&source).Error)

	mapping := UpstreamSourceChannelMapping{
		SourceID:        source.Id,
		SyncEnabled:     true,
		UpstreamGroupID: "10",
		UpstreamKeyID:   "99",
		LocalChannelID:  123,
		SyncStatus:      UpstreamMappingSyncStatusSynced,
		LastError:       "keep me",
		LastSyncedAt:    111,
	}
	require.NoError(t, DB.Create(&mapping).Error)

	now := int64(12345)
	rate := 0.8
	incoming := UpstreamSourceChannelMapping{
		SourceID:                source.Id,
		UpstreamGroupID:         "10",
		UpstreamGroupName:       "ChatGPT Cheap",
		UpstreamPlatform:        "openai",
		DiscoveryStatus:         UpstreamMappingDiscoveryStatusActive,
		UpstreamStatus:          "active",
		UpstreamRateMultiplier:  &rate,
		EffectiveRateMultiplier: &rate,
	}
	require.NoError(t, UpsertDiscoveredMappings(source.Id, []UpstreamSourceChannelMapping{incoming}, now))

	var reloaded UpstreamSourceChannelMapping
	require.NoError(t, DB.Where("source_id = ? AND upstream_group_id = ?", source.Id, "10").First(&reloaded).Error)
	assert.True(t, reloaded.SyncEnabled)
	assert.Equal(t, "99", reloaded.UpstreamKeyID)
	assert.Equal(t, 123, reloaded.LocalChannelID)
	assert.Equal(t, UpstreamMappingSyncStatusSynced, reloaded.SyncStatus)
	assert.Equal(t, "keep me", reloaded.LastError)
	assert.Equal(t, int64(111), reloaded.LastSyncedAt)
	assert.Equal(t, "ChatGPT Cheap", reloaded.UpstreamGroupName)
	assert.Equal(t, UpstreamMappingDiscoveryStatusActive, reloaded.DiscoveryStatus)
	assert.Equal(t, now, reloaded.LastDiscoveredAt)
	require.NotNil(t, reloaded.UpstreamRateMultiplier)
	assert.Equal(t, rate, *reloaded.UpstreamRateMultiplier)
}

func TestUpstreamSourceClaimSyncIsDatabaseGuarded(t *testing.T) {
	setupUpstreamSourceTestDB(t)

	source := UpstreamSource{
		Name:         "source-a",
		Type:         UpstreamSourceTypeSub2API,
		Status:       UpstreamSourceStatusEnabled,
		BaseURL:      "https://example.com",
		RelayBaseURL: "https://example.com",
	}
	require.NoError(t, DB.Create(&source).Error)

	claimed, err := ClaimUpstreamSourceSync(source.Id, "token-a", 1000, 60)
	require.NoError(t, err)
	require.True(t, claimed)

	claimed, err = ClaimUpstreamSourceSync(source.Id, "token-b", 1010, 60)
	require.NoError(t, err)
	assert.False(t, claimed)

	claimed, err = ClaimUpstreamSourceSync(source.Id, "token-c", 1061, 60)
	require.NoError(t, err)
	assert.True(t, claimed)

	require.NoError(t, ReleaseUpstreamSourceSync(source.Id, "token-a", UpstreamSyncStatusFailed, "wrong token", 1070))
	var reloaded UpstreamSource
	require.NoError(t, DB.First(&reloaded, source.Id).Error)
	assert.Equal(t, "token-c", reloaded.CurrentSyncToken)
	assert.NotEqual(t, UpstreamSyncStatusFailed, reloaded.LastSyncStatus)

	require.NoError(t, ReleaseUpstreamSourceSync(source.Id, "token-c", UpstreamSyncStatusSucceeded, "", 1080))
	require.NoError(t, DB.First(&reloaded, source.Id).Error)
	assert.Empty(t, reloaded.CurrentSyncToken)
	assert.Equal(t, UpstreamSyncStatusSucceeded, reloaded.LastSyncStatus)
	assert.Equal(t, int64(1080), reloaded.LastSyncTime)
}

func TestUpstreamSourceAutoMigrateCreatesUniqueMapping(t *testing.T) {
	setupUpstreamSourceTestDB(t)

	sourceA := UpstreamSource{
		Name:         "source-a",
		Type:         UpstreamSourceTypeSub2API,
		Status:       UpstreamSourceStatusEnabled,
		BaseURL:      "https://example.com",
		RelayBaseURL: "https://example.com",
	}
	sourceB := UpstreamSource{
		Name:         "source-b",
		Type:         UpstreamSourceTypeSub2API,
		Status:       UpstreamSourceStatusEnabled,
		BaseURL:      "https://example.org",
		RelayBaseURL: "https://example.org",
	}
	require.NoError(t, DB.Create(&sourceA).Error)
	require.NoError(t, DB.Create(&sourceB).Error)

	require.NoError(t, DB.Create(&UpstreamSourceChannelMapping{
		SourceID:        sourceA.Id,
		UpstreamGroupID: "10",
	}).Error)
	assert.Error(t, DB.Create(&UpstreamSourceChannelMapping{
		SourceID:        sourceA.Id,
		UpstreamGroupID: "10",
	}).Error)
	assert.NoError(t, DB.Create(&UpstreamSourceChannelMapping{
		SourceID:        sourceB.Id,
		UpstreamGroupID: "10",
	}).Error)
}

func TestUpstreamSourceRecentHistoryQueriesAreScopedAndBounded(t *testing.T) {
	setupUpstreamSourceTestDB(t)
	sourceA := UpstreamSource{Name: "source-a", Type: UpstreamSourceTypeSub2API, BaseURL: "https://a.example.com"}
	sourceB := UpstreamSource{Name: "source-b", Type: UpstreamSourceTypeSub2API, BaseURL: "https://b.example.com"}
	require.NoError(t, DB.Create(&sourceA).Error)
	require.NoError(t, DB.Create(&sourceB).Error)

	scans := []UpstreamSourceScan{
		{SourceID: sourceA.Id, ScanType: UpstreamSourceScanTypeDiscover, Status: UpstreamSourceScanStatusSuccess, StartedAt: 100, FinishedAt: 101},
		{SourceID: sourceA.Id, ScanType: UpstreamSourceScanTypeDiscover, Status: UpstreamSourceScanStatusSuccess, StartedAt: 200, FinishedAt: 201},
		{SourceID: sourceA.Id, ScanType: UpstreamSourceScanTypeDiscover, Status: UpstreamSourceScanStatusFailed, StartedAt: 300, FinishedAt: 301},
		{SourceID: sourceB.Id, ScanType: UpstreamSourceScanTypeDiscover, Status: UpstreamSourceScanStatusSuccess, StartedAt: 400, FinishedAt: 401},
	}
	require.NoError(t, DB.Create(&scans).Error)
	changes := []UpstreamSourceGroupChange{
		{SourceID: sourceA.Id, ScanID: scans[0].Id, ChangeType: UpstreamSourceGroupChangeAdded, UpstreamGroupID: "10", CreatedAt: 100},
		{SourceID: sourceA.Id, ScanID: scans[1].Id, ChangeType: UpstreamSourceGroupChangeRateChanged, UpstreamGroupID: "10", CreatedAt: 200},
		{SourceID: sourceB.Id, ScanID: scans[3].Id, ChangeType: UpstreamSourceGroupChangeAdded, UpstreamGroupID: "other", CreatedAt: 400},
	}
	require.NoError(t, DB.Create(&changes).Error)

	recentScans, err := ListRecentUpstreamSourceScans(sourceA.Id, 2)
	require.NoError(t, err)
	require.Len(t, recentScans, 2)
	assert.Equal(t, scans[2].Id, recentScans[0].Id)
	assert.Equal(t, scans[1].Id, recentScans[1].Id)
	for _, scan := range recentScans {
		assert.Equal(t, sourceA.Id, scan.SourceID)
	}

	recentChanges, err := ListRecentUpstreamSourceGroupChanges(sourceA.Id, 1)
	require.NoError(t, err)
	require.Len(t, recentChanges, 1)
	assert.Equal(t, changes[1].Id, recentChanges[0].Id)
	assert.Equal(t, sourceA.Id, recentChanges[0].SourceID)
}

func TestFinishUpstreamSourceScanRejectsNonRunningScan(t *testing.T) {
	setupUpstreamSourceTestDB(t)
	source := UpstreamSource{Name: "source-a", Type: UpstreamSourceTypeSub2API, BaseURL: "https://a.example.com"}
	require.NoError(t, DB.Create(&source).Error)
	scan := UpstreamSourceScan{
		SourceID:   source.Id,
		ScanType:   UpstreamSourceScanTypeDiscover,
		Status:     UpstreamSourceScanStatusSuccess,
		StartedAt:  100,
		FinishedAt: 101,
	}
	require.NoError(t, DB.Create(&scan).Error)

	err := FinishUpstreamSourceScanTx(DB, scan.Id, UpstreamSourceScanStatusFailed, false, 200, "late failure")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "not running")
}

func TestUpstreamSourceCollectionMigrationPreservesLegacyRows(t *testing.T) {
	oldDB := DB
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	DB = db
	t.Cleanup(func() { DB = oldDB })

	require.NoError(t, DB.AutoMigrate(
		&UpstreamSource{},
		&UpstreamSourceSession{},
		&UpstreamSourceChannelMapping{},
		&UpstreamSourceScan{},
		&UpstreamSourceGroupChange{},
	))
	source := UpstreamSource{
		Name:    "legacy-source",
		Type:    UpstreamSourceTypeSub2API,
		Status:  UpstreamSourceStatusEnabled,
		BaseURL: "https://legacy.example.com",
	}
	require.NoError(t, DB.Create(&source).Error)
	legacyScan := UpstreamSourceScan{
		SourceID:   source.Id,
		ScanType:   UpstreamSourceScanTypeMonitor,
		Status:     UpstreamSourceScanStatusSuccess,
		StartedAt:  100,
		FinishedAt: 101,
	}
	require.NoError(t, DB.Create(&legacyScan).Error)

	require.NoError(t, DB.AutoMigrate(
		&UpstreamSourceBalanceSnapshot{},
		&UpstreamSourceCostSnapshot{},
		&UpstreamSourceAnnouncement{},
		&UpstreamSourceAnnouncementState{},
		&UpstreamSourceSubscriptionUsageSnapshot{},
		&UpstreamSourceCapabilityOutcome{},
	))

	for _, table := range []any{
		&UpstreamSourceBalanceSnapshot{},
		&UpstreamSourceCostSnapshot{},
		&UpstreamSourceAnnouncement{},
		&UpstreamSourceAnnouncementState{},
		&UpstreamSourceSubscriptionUsageSnapshot{},
		&UpstreamSourceCapabilityOutcome{},
	} {
		assert.True(t, DB.Migrator().HasTable(table))
	}
	var reloaded UpstreamSource
	require.NoError(t, DB.First(&reloaded, source.Id).Error)
	assert.Equal(t, source.Name, reloaded.Name)
	var reloadedScan UpstreamSourceScan
	require.NoError(t, DB.First(&reloadedScan, legacyScan.Id).Error)
	assert.Equal(t, legacyScan.Status, reloadedScan.Status)
}

func TestUpstreamSourceCollectionUniqueKeysAreSourceScoped(t *testing.T) {
	setupUpstreamSourceTestDB(t)
	source := UpstreamSource{Name: "source-a", Type: UpstreamSourceTypeSub2API, BaseURL: "https://a.example.com"}
	require.NoError(t, DB.Create(&source).Error)
	scan := UpstreamSourceScan{SourceID: source.Id, ScanType: UpstreamSourceScanTypeMonitor, Status: UpstreamSourceScanStatusSuccess, StartedAt: 100, FinishedAt: 101}
	require.NoError(t, DB.Create(&scan).Error)

	announcement := UpstreamSourceAnnouncement{SourceID: source.Id, ScanID: scan.Id, SourceKey: "remote-1", Title: "first", FirstSeenAt: 100, LastSeenAt: 100}
	require.NoError(t, DB.Create(&announcement).Error)
	assert.Error(t, DB.Create(&UpstreamSourceAnnouncement{SourceID: source.Id, ScanID: scan.Id, SourceKey: "remote-1", Title: "duplicate", FirstSeenAt: 101, LastSeenAt: 101}).Error)

	outcome := UpstreamSourceCapabilityOutcome{SourceID: source.Id, ScanID: scan.Id, Capability: UpstreamSourceCapabilityBalance, Status: UpstreamSourceCapabilityStatusSuccess, StartedAt: 100, FinishedAt: 101}
	require.NoError(t, DB.Create(&outcome).Error)
	assert.Error(t, DB.Create(&UpstreamSourceCapabilityOutcome{SourceID: source.Id, ScanID: scan.Id, Capability: UpstreamSourceCapabilityBalance, Status: UpstreamSourceCapabilityStatusFailed, StartedAt: 100, FinishedAt: 101}).Error)
}
