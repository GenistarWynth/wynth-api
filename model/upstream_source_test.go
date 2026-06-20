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

	require.NoError(t, DB.AutoMigrate(&UpstreamSource{}, &UpstreamSourceChannelMapping{}, &Channel{}, &Ability{}))
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
