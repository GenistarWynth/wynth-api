package model

import (
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaydto "github.com/QuantumNous/new-api/relaykit/dto"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type legacyUpstreamSourceBillingProbe struct {
	Id                       int
	SourceID                 int
	MappingID                int
	Enabled                  bool
	IntervalMinutes          int
	Status                   string
	FailureCount             int
	LeaseToken               string
	ClaimIdentityFingerprint string
}

type incompleteLegacyUpstreamSourceBillingProbe struct {
	Id       int
	SourceID int
}

type billingProbeNamedDialector struct {
	gorm.Dialector
	name string
}

func (legacyUpstreamSourceBillingProbe) TableName() string {
	return "upstream_source_billing_probes"
}

func (incompleteLegacyUpstreamSourceBillingProbe) TableName() string {
	return "upstream_source_billing_probes"
}

func (dialector billingProbeNamedDialector) Name() string {
	return dialector.name
}

func setupUpstreamSourceBillingProbeModelTest(t *testing.T) {
	t.Helper()
	setupUpstreamSourceTestDB(t)
	require.NoError(t, DB.AutoMigrate(&UpstreamSourceBillingProbe{}))
}

func createUpstreamSourceBillingProbeMapping(t *testing.T) (UpstreamSource, UpstreamSourceChannelMapping) {
	t.Helper()
	source := UpstreamSource{
		Name:         "billing-probe-source",
		Type:         UpstreamSourceTypeSub2API,
		Status:       UpstreamSourceStatusEnabled,
		BaseURL:      "https://admin.example.com",
		RelayBaseURL: "https://relay.example.com",
	}
	require.NoError(t, DB.Create(&source).Error)
	mapping := UpstreamSourceChannelMapping{
		SourceID:        source.Id,
		SyncEnabled:     true,
		UpstreamGroupID: "billing-probe-group",
		DiscoveryStatus: UpstreamMappingDiscoveryStatusActive,
		UpstreamKeyID:   "billing-probe-key-id",
	}
	require.NoError(t, DB.Create(&mapping).Error)
	baseURL := source.RelayBaseURL
	channel := Channel{
		Name:    "billing-probe-channel",
		Type:    constant.ChannelTypeOpenAI,
		Key:     "sk-billing-probe-test",
		Status:  common.ChannelStatusEnabled,
		BaseURL: &baseURL,
		Models:  "gpt-4o-mini",
		Group:   "default",
	}
	channel.SetOtherSettings(relaydto.ChannelOtherSettings{
		GeneratedByUpstreamSourceID:  source.Id,
		GeneratedByUpstreamMappingID: mapping.Id,
	})
	require.NoError(t, DB.Create(&channel).Error)
	require.NoError(t, DB.Model(&UpstreamSourceChannelMapping{}).Where("id = ?", mapping.Id).
		Update("local_channel_id", channel.Id).Error)
	mapping.LocalChannelID = channel.Id
	return source, mapping
}

func TestUpstreamSourceBillingProbeMigrationDefaultsDisabledAndUnique(t *testing.T) {
	setupUpstreamSourceBillingProbeModelTest(t)
	source, mapping := createUpstreamSourceBillingProbeMapping(t)

	probe := UpstreamSourceBillingProbe{SourceID: source.Id, MappingID: mapping.Id}
	require.NoError(t, DB.Create(&probe).Error)
	require.NotZero(t, probe.Id)

	var stored UpstreamSourceBillingProbe
	require.NoError(t, DB.First(&stored, probe.Id).Error)
	assert.False(t, stored.Enabled)
	assert.Equal(t, DefaultUpstreamSourceBillingProbeIntervalMinutes, stored.IntervalMinutes)
	assert.Equal(t, UpstreamSourceBillingProbeStatusIdle, stored.Status)
	assert.Empty(t, stored.LastGoodIdentityFingerprint)
	assert.Empty(t, stored.ClaimIdentityFingerprint)
	assert.Empty(t, stored.LeaseToken)
	assert.Zero(t, stored.LeaseStartedAt)

	duplicate := UpstreamSourceBillingProbe{SourceID: source.Id, MappingID: mapping.Id}
	assert.Error(t, DB.Create(&duplicate).Error)
	otherSource := UpstreamSource{
		Name:         "billing-probe-other-source",
		Type:         UpstreamSourceTypeSub2API,
		Status:       UpstreamSourceStatusEnabled,
		BaseURL:      "https://other-admin.example.com",
		RelayBaseURL: "https://other-relay.example.com",
	}
	require.NoError(t, DB.Create(&otherSource).Error)
	crossSourceDuplicate := UpstreamSourceBillingProbe{SourceID: otherSource.Id, MappingID: mapping.Id}
	assert.Error(t, DB.Create(&crossSourceDuplicate).Error, "mapping_id must be globally unique regardless of source_id")

	require.NoError(t, DB.Model(&UpstreamSourceBillingProbe{}).
		Where("mapping_id = ?", mapping.Id).
		Updates(map[string]any{"enabled": true, "next_probe_at": int64(0)}).Error)
	claimed, err := ClaimUpstreamSourceBillingProbe(mapping.Id, "unique-lease", 1, 60, true)
	require.NoError(t, err)
	require.NotNil(t, claimed)
	var leasedRows int64
	require.NoError(t, DB.Model(&UpstreamSourceBillingProbe{}).
		Where("mapping_id = ? AND lease_token = ?", mapping.Id, "unique-lease").
		Count(&leasedRows).Error)
	assert.Equal(t, int64(1), leasedRows, "a mapping-only claim must affect exactly one row")

	require.NoError(t, DB.Exec(
		"UPDATE upstream_source_billing_probes SET enabled = NULL, interval_minutes = NULL, status = NULL, lease_token = NULL, lease_started_at = NULL, last_good_identity_fingerprint = NULL, claim_identity_fingerprint = NULL WHERE id = ?",
		probe.Id,
	).Error)
	require.NoError(t, backfillUpstreamSourceBillingProbeDefaults())
	require.NoError(t, DB.First(&stored, probe.Id).Error)
	assert.False(t, stored.Enabled)
	assert.Equal(t, DefaultUpstreamSourceBillingProbeIntervalMinutes, stored.IntervalMinutes)
	assert.Equal(t, UpstreamSourceBillingProbeStatusIdle, stored.Status)
	assert.Empty(t, stored.LastGoodIdentityFingerprint)
	assert.Empty(t, stored.ClaimIdentityFingerprint)
	assert.Empty(t, stored.LeaseToken)
	assert.Zero(t, stored.LeaseStartedAt)
}

func TestUpstreamSourceBillingProbeDueClaimLeaseAndStaleRecovery(t *testing.T) {
	setupUpstreamSourceBillingProbeModelTest(t)
	source, mapping := createUpstreamSourceBillingProbeMapping(t)
	probe := UpstreamSourceBillingProbe{
		SourceID:        source.Id,
		MappingID:       mapping.Id,
		Enabled:         true,
		IntervalMinutes: 30,
		NextProbeAt:     1_000,
	}
	require.NoError(t, DB.Create(&probe).Error)

	due, err := ListDueUpstreamSourceBillingProbes(999, 20)
	require.NoError(t, err)
	assert.Empty(t, due)
	due, err = ListDueUpstreamSourceBillingProbes(1_000, 20)
	require.NoError(t, err)
	require.Len(t, due, 1)
	assert.Equal(t, mapping.Id, due[0].MappingID)

	claimed, err := ClaimUpstreamSourceBillingProbe(mapping.Id, "lease-a", 1_000, 60, true)
	require.NoError(t, err)
	require.NotNil(t, claimed)
	assert.Equal(t, "lease-a", claimed.LeaseToken)

	claimed, err = ClaimUpstreamSourceBillingProbe(mapping.Id, "lease-b", 1_010, 60, false)
	require.NoError(t, err)
	assert.Nil(t, claimed)

	claimed, err = ClaimUpstreamSourceBillingProbe(mapping.Id, "lease-c", 1_061, 60, false)
	require.NoError(t, err)
	require.NotNil(t, claimed)
	assert.Equal(t, "lease-c", claimed.LeaseToken)

	released, err := ReleaseUpstreamSourceBillingProbeLease(mapping.Id, "lease-a", 2_000, 1_070)
	require.NoError(t, err)
	assert.False(t, released)
	require.NoError(t, DB.First(&probe, probe.Id).Error)
	assert.Equal(t, "lease-c", probe.LeaseToken)

	released, err = ReleaseUpstreamSourceBillingProbeLease(mapping.Id, "lease-c", 2_000, 1_080)
	require.NoError(t, err)
	assert.True(t, released)
	require.NoError(t, DB.First(&probe, probe.Id).Error)
	assert.Empty(t, probe.LeaseToken)
	assert.Zero(t, probe.LeaseStartedAt)
	assert.Equal(t, int64(2_000), probe.NextProbeAt)
}

func TestUpstreamSourceBillingProbeClaimAllowsOnlyOneWorker(t *testing.T) {
	setupUpstreamSourceBillingProbeModelTest(t)
	source, mapping := createUpstreamSourceBillingProbeMapping(t)
	require.NoError(t, DB.Create(&UpstreamSourceBillingProbe{
		SourceID:        source.Id,
		MappingID:       mapping.Id,
		Enabled:         true,
		IntervalMinutes: 30,
		NextProbeAt:     1_000,
	}).Error)

	const workers = 12
	start := make(chan struct{})
	results := make(chan bool, workers)
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			<-start
			claimed, err := ClaimUpstreamSourceBillingProbe(mapping.Id, "worker-"+string(rune('a'+worker)), 1_000, 60, true)
			errs <- err
			results <- claimed != nil
		}(i)
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)

	for err := range errs {
		require.NoError(t, err)
	}
	claimedCount := 0
	for claimed := range results {
		if claimed {
			claimedCount++
		}
	}
	assert.Equal(t, 1, claimedCount)
}

func TestUpstreamSourceBillingProbeDueAndClaimExcludeIneligibleOwnership(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(t *testing.T, source UpstreamSource, mapping UpstreamSourceChannelMapping)
	}{
		{name: "source disabled", mutate: func(t *testing.T, source UpstreamSource, _ UpstreamSourceChannelMapping) {
			require.NoError(t, DB.Model(&UpstreamSource{}).Where("id = ?", source.Id).
				Update("status", UpstreamSourceStatusDisabled).Error)
		}},
		{name: "source deleted", mutate: func(t *testing.T, source UpstreamSource, _ UpstreamSourceChannelMapping) {
			require.NoError(t, DB.Model(&UpstreamSource{}).Where("id = ?", source.Id).
				Update("status", UpstreamSourceStatusDeleted).Error)
		}},
		{name: "mapping deleted", mutate: func(t *testing.T, _ UpstreamSource, mapping UpstreamSourceChannelMapping) {
			require.NoError(t, DB.Delete(&UpstreamSourceChannelMapping{}, mapping.Id).Error)
		}},
		{name: "mapping source rebound", mutate: func(t *testing.T, _ UpstreamSource, mapping UpstreamSourceChannelMapping) {
			other := UpstreamSource{Name: "other-source", Type: UpstreamSourceTypeSub2API, Status: UpstreamSourceStatusEnabled, BaseURL: "https://other.example", RelayBaseURL: "https://other.example"}
			require.NoError(t, DB.Create(&other).Error)
			require.NoError(t, DB.Model(&UpstreamSourceChannelMapping{}).Where("id = ?", mapping.Id).
				Update("source_id", other.Id).Error)
		}},
		{name: "mapping channel rebound", mutate: func(t *testing.T, _ UpstreamSource, mapping UpstreamSourceChannelMapping) {
			require.NoError(t, DB.Model(&UpstreamSourceChannelMapping{}).Where("id = ?", mapping.Id).
				Update("local_channel_id", 0).Error)
		}},
		{name: "mapping disabled", mutate: func(t *testing.T, _ UpstreamSource, mapping UpstreamSourceChannelMapping) {
			require.NoError(t, DB.Model(&UpstreamSourceChannelMapping{}).Where("id = ?", mapping.Id).
				Update("sync_enabled", false).Error)
		}},
		{name: "mapping stale", mutate: func(t *testing.T, _ UpstreamSource, mapping UpstreamSourceChannelMapping) {
			require.NoError(t, DB.Model(&UpstreamSourceChannelMapping{}).Where("id = ?", mapping.Id).
				Update("discovery_status", UpstreamMappingDiscoveryStatusStale).Error)
		}},
		{name: "channel disabled", mutate: func(t *testing.T, _ UpstreamSource, mapping UpstreamSourceChannelMapping) {
			require.NoError(t, DB.Model(&Channel{}).Where("id = ?", mapping.LocalChannelID).
				Update("status", common.ChannelStatusManuallyDisabled).Error)
		}},
		{name: "channel key empty", mutate: func(t *testing.T, _ UpstreamSource, mapping UpstreamSourceChannelMapping) {
			require.NoError(t, DB.Model(&Channel{}).Where("id = ?", mapping.LocalChannelID).
				Update("key", "").Error)
		}},
		{name: "channel type unsupported", mutate: func(t *testing.T, _ UpstreamSource, mapping UpstreamSourceChannelMapping) {
			require.NoError(t, DB.Model(&Channel{}).Where("id = ?", mapping.LocalChannelID).
				Update("type", constant.ChannelTypeDummy+1_000).Error)
		}},
		{name: "generated owner source rebound", mutate: func(t *testing.T, source UpstreamSource, mapping UpstreamSourceChannelMapping) {
			var channel Channel
			require.NoError(t, DB.First(&channel, mapping.LocalChannelID).Error)
			settings := channel.GetOtherSettings()
			settings.GeneratedByUpstreamSourceID = source.Id + 1
			channel.SetOtherSettings(settings)
			require.NoError(t, DB.Model(&Channel{}).Where("id = ?", channel.Id).
				Update("settings", channel.OtherSettings).Error)
		}},
		{name: "generated owner mapping rebound", mutate: func(t *testing.T, _ UpstreamSource, mapping UpstreamSourceChannelMapping) {
			var channel Channel
			require.NoError(t, DB.First(&channel, mapping.LocalChannelID).Error)
			settings := channel.GetOtherSettings()
			settings.GeneratedByUpstreamMappingID = mapping.Id + 1
			channel.SetOtherSettings(settings)
			require.NoError(t, DB.Model(&Channel{}).Where("id = ?", channel.Id).
				Update("settings", channel.OtherSettings).Error)
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setupUpstreamSourceBillingProbeModelTest(t)
			source, mapping := createUpstreamSourceBillingProbeMapping(t)
			require.NoError(t, DB.Create(&UpstreamSourceBillingProbe{
				SourceID: source.Id, MappingID: mapping.Id, Enabled: true, IntervalMinutes: 30, NextProbeAt: 1_000,
			}).Error)
			tt.mutate(t, source, mapping)

			due, err := ListDueUpstreamSourceBillingProbes(1_000, 20)
			require.NoError(t, err)
			assert.Empty(t, due)
			claimed, err := ClaimUpstreamSourceBillingProbe(mapping.Id, "ineligible-lease", 1_000, 60, true)
			require.NoError(t, err)
			assert.Nil(t, claimed)
			var stored UpstreamSourceBillingProbe
			require.NoError(t, DB.Where("mapping_id = ?", mapping.Id).First(&stored).Error)
			assert.Empty(t, stored.LeaseToken)
			assert.Zero(t, stored.LeaseStartedAt)
		})
	}
}

func TestUpstreamSourceBillingProbeEligibilityChangeBetweenDueAndClaimDoesNotLease(t *testing.T) {
	setupUpstreamSourceBillingProbeModelTest(t)
	source, mapping := createUpstreamSourceBillingProbeMapping(t)
	require.NoError(t, DB.Create(&UpstreamSourceBillingProbe{
		SourceID: source.Id, MappingID: mapping.Id, Enabled: true, IntervalMinutes: 30, NextProbeAt: 1_000,
	}).Error)

	due, err := ListDueUpstreamSourceBillingProbes(1_000, 20)
	require.NoError(t, err)
	require.Len(t, due, 1)

	var channel Channel
	require.NoError(t, DB.First(&channel, mapping.LocalChannelID).Error)
	settings := channel.GetOtherSettings()
	settings.GeneratedByUpstreamMappingID++
	channel.SetOtherSettings(settings)
	require.NoError(t, DB.Model(&Channel{}).Where("id = ?", channel.Id).
		Update("settings", channel.OtherSettings).Error)

	claimed, err := ClaimUpstreamSourceBillingProbe(mapping.Id, "stale-due-lease", 1_000, 60, true)
	require.NoError(t, err)
	assert.Nil(t, claimed)
	var stored UpstreamSourceBillingProbe
	require.NoError(t, DB.Where("mapping_id = ?", mapping.Id).First(&stored).Error)
	assert.Empty(t, stored.LeaseToken)
	assert.Zero(t, stored.LeaseStartedAt)
}

func TestUpstreamSourceBillingProbeMultiKeyChangeInsideClaimDoesNotAcquireLease(t *testing.T) {
	setupUpstreamSourceBillingProbeModelTest(t)
	source, mapping := createUpstreamSourceBillingProbeMapping(t)
	require.NoError(t, DB.Create(&UpstreamSourceBillingProbe{
		SourceID: source.Id, MappingID: mapping.Id, Enabled: true, IntervalMinutes: 30, NextProbeAt: 1_000,
	}).Error)

	var changed atomic.Bool
	var mutationErr error
	const callbackName = "test:billing_probe_multikey_claim_toctou"
	require.NoError(t, DB.Callback().Query().After("gorm:query").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Schema == nil || tx.Statement.Schema.Name != "Channel" || !changed.CompareAndSwap(false, true) {
			return
		}
		var channel Channel
		mutationErr = DB.Session(&gorm.Session{NewDB: true, SkipHooks: true}).First(&channel, mapping.LocalChannelID).Error
		if mutationErr != nil {
			return
		}
		channel.ChannelInfo.IsMultiKey = true
		mutationErr = DB.Session(&gorm.Session{NewDB: true, SkipHooks: true}).
			Model(&Channel{}).
			Where("id = ?", mapping.LocalChannelID).
			UpdateColumn("channel_info", channel.ChannelInfo).Error
	}))
	t.Cleanup(func() { _ = DB.Callback().Query().Remove(callbackName) })

	claimed, err := ClaimUpstreamSourceBillingProbe(mapping.Id, "multikey-race-lease", 1_000, 60, true)
	require.NoError(t, mutationErr)
	require.NoError(t, err)
	require.True(t, changed.Load())
	assert.Nil(t, claimed)

	var stored UpstreamSourceBillingProbe
	require.NoError(t, DB.Where("mapping_id = ?", mapping.Id).First(&stored).Error)
	assert.Empty(t, stored.LeaseToken)
	assert.Zero(t, stored.LeaseStartedAt)
}

func TestUpstreamSourceBillingProbeFinalClaimUsesDialectCorrectMultiKeyPredicate(t *testing.T) {
	setupUpstreamSourceBillingProbeModelTest(t)
	originalDatabaseType := common.MainDatabaseType()
	t.Cleanup(func() { common.SetMainDatabaseType(originalDatabaseType) })
	channel := &Channel{Id: 1, Type: constant.ChannelTypeOpenAI, Key: "test-key", OtherSettings: "{}"}

	tests := []struct {
		name         string
		databaseType common.DatabaseType
		wantSQL      string
	}{
		{name: "sqlite", databaseType: common.DatabaseTypeSQLite, wantSQL: "json_extract(billing_probe_channel.channel_info, '$.is_multi_key')"},
		{name: "mysql", databaseType: common.DatabaseTypeMySQL, wantSQL: "JSON_UNQUOTE(JSON_EXTRACT(billing_probe_channel.channel_info, '$.is_multi_key'))"},
		{name: "postgres", databaseType: common.DatabaseTypePostgreSQL, wantSQL: "billing_probe_channel.channel_info ->> 'is_multi_key'"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			common.SetMainDatabaseType(tt.databaseType)
			statement := eligibleUpstreamSourceBillingProbeQuery(DB.Session(&gorm.Session{DryRun: true}).Model(&UpstreamSourceBillingProbe{}), channel).
				Find(&[]UpstreamSourceBillingProbe{}).Statement
			require.NoError(t, statement.Error)
			assert.True(t, strings.Contains(statement.SQL.String(), tt.wantSQL), statement.SQL.String())
		})
	}
}

func TestUpstreamSourceBillingProbeLegacyDuplicateAutoMigrationPreservesOldestStateAndGlobalUniqueness(t *testing.T) {
	dsn := "file:billing-probe-legacy-" + common.GetUUID() + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	require.NoError(t, db.AutoMigrate(&legacyUpstreamSourceBillingProbe{}))
	oldest := legacyUpstreamSourceBillingProbe{
		SourceID:                 1,
		MappingID:                9,
		Enabled:                  true,
		IntervalMinutes:          17,
		Status:                   UpstreamSourceBillingProbeStatusOK,
		FailureCount:             3,
		LeaseToken:               "oldest-lease-state",
		ClaimIdentityFingerprint: "oldest-identity-state",
	}
	newer := legacyUpstreamSourceBillingProbe{
		SourceID:                 2,
		MappingID:                9,
		Enabled:                  false,
		IntervalMinutes:          29,
		Status:                   UpstreamSourceBillingProbeStatusFailed,
		FailureCount:             8,
		LeaseToken:               "newer-lease-state",
		ClaimIdentityFingerprint: "newer-identity-state",
	}
	require.NoError(t, db.Create(&oldest).Error)
	require.NoError(t, db.Create(&newer).Error)

	require.NoError(t, db.AutoMigrate(&UpstreamSourceBillingProbe{}))
	require.True(t, db.Migrator().HasIndex(&UpstreamSourceBillingProbe{}, "idx_upstream_source_billing_probe_mapping_id"))

	var survivors []UpstreamSourceBillingProbe
	require.NoError(t, db.Where("mapping_id = ?", 9).Find(&survivors).Error)
	require.Len(t, survivors, 1)
	assert.Equal(t, oldest.Id, int(survivors[0].Id))
	assert.Equal(t, oldest.SourceID, survivors[0].SourceID)
	assert.Equal(t, oldest.Enabled, survivors[0].Enabled)
	assert.Equal(t, oldest.IntervalMinutes, survivors[0].IntervalMinutes)
	assert.Equal(t, oldest.Status, survivors[0].Status)
	assert.Equal(t, oldest.FailureCount, survivors[0].FailureCount)
	assert.Equal(t, oldest.LeaseToken, survivors[0].LeaseToken)
	assert.Equal(t, oldest.ClaimIdentityFingerprint, survivors[0].ClaimIdentityFingerprint)
	assert.NotEqual(t, newer.SourceID, survivors[0].SourceID)

	duplicate := UpstreamSourceBillingProbe{SourceID: 3, MappingID: 9}
	assert.Error(t, db.Create(&duplicate).Error)

	require.NoError(t, db.AutoMigrate(&UpstreamSourceBillingProbe{}))
	var afterRerun []UpstreamSourceBillingProbe
	require.NoError(t, db.Where("mapping_id = ?", 9).Find(&afterRerun).Error)
	require.Len(t, afterRerun, 1)
	assert.Equal(t, survivors[0], afterRerun[0])
}

func TestUpstreamSourceBillingProbeLegacyEmptyAutoMigrationIsIdempotent(t *testing.T) {
	dsn := "file:billing-probe-empty-" + common.GetUUID() + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	require.NoError(t, db.AutoMigrate(&legacyUpstreamSourceBillingProbe{}))
	require.NoError(t, db.AutoMigrate(&UpstreamSourceBillingProbe{}))
	require.NoError(t, db.AutoMigrate(&UpstreamSourceBillingProbe{}))
	require.True(t, db.Migrator().HasIndex(&UpstreamSourceBillingProbe{}, "idx_upstream_source_billing_probe_mapping_id"))
	var count int64
	require.NoError(t, db.Model(&UpstreamSourceBillingProbe{}).Count(&count).Error)
	assert.Zero(t, count)
}

func TestUpstreamSourceBillingProbeLegacyMigrationFailsClosedWithoutIdentityColumns(t *testing.T) {
	dsn := "file:billing-probe-incomplete-" + common.GetUUID() + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(&incompleteLegacyUpstreamSourceBillingProbe{}))

	err = deduplicateLegacyUpstreamSourceBillingProbes(db)
	require.Error(t, err)
	assert.Contains(t, strings.ToLower(err.Error()), "mapping_id")
	assert.False(t, db.Migrator().HasIndex(&UpstreamSourceBillingProbe{}, "idx_upstream_source_billing_probe_mapping_id"))

	err = db.AutoMigrate(&UpstreamSourceBillingProbe{})
	require.Error(t, err)
	assert.False(t, db.Migrator().HasIndex(&UpstreamSourceBillingProbe{}, "idx_upstream_source_billing_probe_mapping_id"))
}

func TestUpstreamSourceBillingProbeMigrationIDKeepsDialectPrimaryKeyTypes(t *testing.T) {
	for _, dialectName := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialectName, func(t *testing.T) {
			dsn := "file:billing-probe-dialect-" + dialectName + "-" + common.GetUUID() + "?mode=memory&cache=shared"
			db, err := gorm.Open(billingProbeNamedDialector{Dialector: sqlite.Open(dsn), name: dialectName}, &gorm.Config{})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			t.Cleanup(func() { _ = sqlDB.Close() })
			statement := &gorm.Statement{DB: db}
			require.NoError(t, statement.Parse(&UpstreamSourceBillingProbe{}))
			field := statement.Schema.LookUpField("Id")
			require.NotNil(t, field)
			dataType := upstreamSourceBillingProbeID(0).GormDBDataType(db, field)
			assert.Empty(t, dataType, "the migration guard must defer primary-key typing to GORM")
			assert.NotContains(t, strings.ToLower(dataType), "auto_increment")
			assert.NotContains(t, strings.ToLower(dataType), "serial")
		})
	}
}
