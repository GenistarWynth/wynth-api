package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type fakeUpstreamSourceAdapter struct {
	groups []UpstreamGroup
	err    error
}

func (a fakeUpstreamSourceAdapter) DiscoverGroups(ctx context.Context, source *model.UpstreamSource) ([]UpstreamGroup, error) {
	if a.err != nil {
		return nil, a.err
	}
	return a.groups, nil
}

func (a fakeUpstreamSourceAdapter) CreateKey(ctx context.Context, source *model.UpstreamSource, groupID string, name string) (UpstreamKey, error) {
	return UpstreamKey{}, errors.New("unexpected CreateKey call")
}

func (a fakeUpstreamSourceAdapter) UpdateKey(ctx context.Context, source *model.UpstreamSource, keyID string, groupID string, name string) (UpstreamKey, error) {
	return UpstreamKey{}, errors.New("unexpected UpdateKey call")
}

func (a fakeUpstreamSourceAdapter) ListKeys(ctx context.Context, source *model.UpstreamSource, groupID string) ([]UpstreamKey, error) {
	return nil, errors.New("unexpected ListKeys call")
}

func setupUpstreamSourceServiceTestDB(t *testing.T) {
	t.Helper()

	oldDB := model.DB
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	model.DB = db
	t.Cleanup(func() {
		model.DB = oldDB
	})

	require.NoError(t, model.DB.AutoMigrate(&model.UpstreamSource{}, &model.UpstreamSourceChannelMapping{}, &model.Channel{}, &model.Ability{}))
}

func createDiscoveryTestSource(t *testing.T) model.UpstreamSource {
	t.Helper()

	source := model.UpstreamSource{
		Name:         "source-a",
		Type:         model.UpstreamSourceTypeSub2API,
		Status:       model.UpstreamSourceStatusEnabled,
		BaseURL:      "https://admin.example.com",
		RelayBaseURL: "https://relay.example.com",
	}
	require.NoError(t, model.DB.Create(&source).Error)
	return source
}

func TestDiscoverUpstreamSourceUpsertsMappings(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	source := createDiscoveryTestSource(t)
	rateA := 0.5
	rateB := 1.25
	service := UpstreamSourceService{
		AdapterFactory: func(sourceType string) (UpstreamSourceAdapter, error) {
			require.Equal(t, model.UpstreamSourceTypeSub2API, sourceType)
			return fakeUpstreamSourceAdapter{groups: []UpstreamGroup{
				{ID: "10", Name: "cheap", Platform: "openai", Status: "enabled", RateMultiplier: &rateA, EffectiveRateMultiplier: &rateA},
				{ID: "20", Name: "premium", Platform: "claude", Status: "enabled", RateMultiplier: &rateB, EffectiveRateMultiplier: &rateB},
			}}, nil
		},
		Now: func() int64 { return 12345 },
	}

	result, err := service.Discover(context.Background(), source.Id)

	require.NoError(t, err)
	assert.Equal(t, source.Id, result.SourceID)
	assert.Equal(t, 2, result.Discovered)
	assert.Equal(t, 2, result.Active)
	assert.Equal(t, 0, result.Invalid)
	assert.Equal(t, 0, result.Stale)
	require.Len(t, result.Mappings, 2)
	assert.Equal(t, "10", result.Mappings[0].UpstreamGroupID)
	assert.Equal(t, "cheap", result.Mappings[0].UpstreamGroupName)
	assert.False(t, result.Mappings[0].SyncEnabled)

	var mappings []model.UpstreamSourceChannelMapping
	require.NoError(t, model.DB.Order("upstream_group_id").Find(&mappings).Error)
	require.Len(t, mappings, 2)
	assert.Equal(t, model.UpstreamMappingDiscoveryStatusActive, mappings[0].DiscoveryStatus)
	assert.Equal(t, int64(12345), mappings[0].LastDiscoveredAt)
	require.NotNil(t, mappings[0].EffectiveRateMultiplier)
	assert.Equal(t, rateA, *mappings[0].EffectiveRateMultiplier)

	var reloaded model.UpstreamSource
	require.NoError(t, model.DB.First(&reloaded, source.Id).Error)
	assert.Equal(t, model.UpstreamDiscoveryStatusSucceeded, reloaded.LastDiscoveryStatus)
	assert.Equal(t, int64(12345), reloaded.LastDiscoveryTime)
	assert.Empty(t, reloaded.LastDiscoveryError)
}

func TestDiscoverUpstreamSourcePreservesSyncOwnedMappingFields(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	source := createDiscoveryTestSource(t)
	rate := 0.75
	existing := model.UpstreamSourceChannelMapping{
		SourceID:        source.Id,
		SyncEnabled:     true,
		UpstreamGroupID: "10",
		UpstreamKeyID:   "99",
		LocalChannelID:  123,
		SyncStatus:      model.UpstreamMappingSyncStatusSynced,
		LastError:       "keep me",
		LastSyncedAt:    111,
	}
	require.NoError(t, model.DB.Create(&existing).Error)
	service := UpstreamSourceService{
		AdapterFactory: func(sourceType string) (UpstreamSourceAdapter, error) {
			return fakeUpstreamSourceAdapter{groups: []UpstreamGroup{
				{ID: "10", Name: "renamed", Platform: "openai", Status: "enabled", RateMultiplier: &rate, EffectiveRateMultiplier: &rate},
			}}, nil
		},
		Now: func() int64 { return 222 },
	}

	_, err := service.Discover(context.Background(), source.Id)

	require.NoError(t, err)
	var reloaded model.UpstreamSourceChannelMapping
	require.NoError(t, model.DB.Where("source_id = ? AND upstream_group_id = ?", source.Id, "10").First(&reloaded).Error)
	assert.True(t, reloaded.SyncEnabled)
	assert.Equal(t, "99", reloaded.UpstreamKeyID)
	assert.Equal(t, 123, reloaded.LocalChannelID)
	assert.Equal(t, model.UpstreamMappingSyncStatusSynced, reloaded.SyncStatus)
	assert.Equal(t, "keep me", reloaded.LastError)
	assert.Equal(t, int64(111), reloaded.LastSyncedAt)
	assert.Equal(t, "renamed", reloaded.UpstreamGroupName)
	assert.Equal(t, int64(222), reloaded.LastDiscoveredAt)
}

func TestDiscoverUpstreamSourceMarksMissingMappingsStale(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	source := createDiscoveryTestSource(t)
	rate := 1.0
	require.NoError(t, model.DB.Create(&model.UpstreamSourceChannelMapping{
		SourceID:        source.Id,
		SyncEnabled:     true,
		UpstreamGroupID: "10",
		DiscoveryStatus: model.UpstreamMappingDiscoveryStatusActive,
		UpstreamKeyID:   "key-10",
		LocalChannelID:  77,
		SyncStatus:      model.UpstreamMappingSyncStatusSynced,
	}).Error)
	require.NoError(t, model.DB.Create(&model.UpstreamSourceChannelMapping{
		SourceID:         source.Id,
		SyncEnabled:      true,
		UpstreamGroupID:  "20",
		DiscoveryStatus:  model.UpstreamMappingDiscoveryStatusActive,
		UpstreamKeyID:    "key-20",
		LocalChannelID:   88,
		SyncStatus:       model.UpstreamMappingSyncStatusSynced,
		LastDiscoveredAt: 100,
	}).Error)
	require.NoError(t, model.DB.Create(&model.UpstreamSourceChannelMapping{
		SourceID:         source.Id,
		SyncEnabled:      true,
		UpstreamGroupID:  "30",
		DiscoveryStatus:  model.UpstreamMappingDiscoveryStatusInvalid,
		UpstreamKeyID:    "key-30",
		LocalChannelID:   99,
		SyncStatus:       model.UpstreamMappingSyncStatusFailed,
		LastDiscoveredAt: 200,
	}).Error)
	service := UpstreamSourceService{
		AdapterFactory: func(sourceType string) (UpstreamSourceAdapter, error) {
			return fakeUpstreamSourceAdapter{groups: []UpstreamGroup{
				{ID: "10", Name: "still here", Platform: "openai", Status: "enabled", RateMultiplier: &rate, EffectiveRateMultiplier: &rate},
			}}, nil
		},
		Now: func() int64 { return 333 },
	}

	result, err := service.Discover(context.Background(), source.Id)

	require.NoError(t, err)
	assert.Equal(t, 2, result.Stale)

	var stale model.UpstreamSourceChannelMapping
	require.NoError(t, model.DB.Where("source_id = ? AND upstream_group_id = ?", source.Id, "20").First(&stale).Error)
	assert.Equal(t, model.UpstreamMappingDiscoveryStatusStale, stale.DiscoveryStatus)
	assert.Equal(t, int64(333), stale.LastDiscoveredAt)
	assert.True(t, stale.SyncEnabled)
	assert.Equal(t, "key-20", stale.UpstreamKeyID)
	assert.Equal(t, 88, stale.LocalChannelID)
	assert.Equal(t, model.UpstreamMappingSyncStatusSynced, stale.SyncStatus)

	var staleFromInvalid model.UpstreamSourceChannelMapping
	require.NoError(t, model.DB.Where("source_id = ? AND upstream_group_id = ?", source.Id, "30").First(&staleFromInvalid).Error)
	assert.Equal(t, model.UpstreamMappingDiscoveryStatusStale, staleFromInvalid.DiscoveryStatus)
	assert.Equal(t, int64(333), staleFromInvalid.LastDiscoveredAt)
	assert.True(t, staleFromInvalid.SyncEnabled)
	assert.Equal(t, "key-30", staleFromInvalid.UpstreamKeyID)
	assert.Equal(t, 99, staleFromInvalid.LocalChannelID)
	assert.Equal(t, model.UpstreamMappingSyncStatusFailed, staleFromInvalid.SyncStatus)
}

func TestDiscoverUpstreamSourceRollsBackMappingsWhenStatusUpdateFails(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	source := createDiscoveryTestSource(t)
	rate := 1.0
	require.NoError(t, model.DB.Create(&model.UpstreamSourceChannelMapping{
		SourceID:         source.Id,
		SyncEnabled:      true,
		UpstreamGroupID:  "20",
		DiscoveryStatus:  model.UpstreamMappingDiscoveryStatusActive,
		LastDiscoveredAt: 111,
	}).Error)
	callbackName := fmt.Sprintf("fail_upstream_source_update_%s", t.Name())
	require.NoError(t, model.DB.Callback().Update().Before("gorm:update").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Name == "UpstreamSource" {
			tx.AddError(errors.New("forced source update failure"))
		}
	}))
	service := UpstreamSourceService{
		AdapterFactory: func(sourceType string) (UpstreamSourceAdapter, error) {
			return fakeUpstreamSourceAdapter{groups: []UpstreamGroup{
				{ID: "10", Name: "new", Platform: "openai", Status: "enabled", RateMultiplier: &rate, EffectiveRateMultiplier: &rate},
			}}, nil
		},
		Now: func() int64 { return 333 },
	}

	_, err := service.Discover(context.Background(), source.Id)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "forced source update failure")

	var mappingCount int64
	require.NoError(t, model.DB.Model(&model.UpstreamSourceChannelMapping{}).Where("source_id = ?", source.Id).Count(&mappingCount).Error)
	assert.Equal(t, int64(1), mappingCount)

	var existing model.UpstreamSourceChannelMapping
	require.NoError(t, model.DB.Where("source_id = ? AND upstream_group_id = ?", source.Id, "20").First(&existing).Error)
	assert.Equal(t, model.UpstreamMappingDiscoveryStatusActive, existing.DiscoveryStatus)
	assert.Equal(t, int64(111), existing.LastDiscoveredAt)

	var created model.UpstreamSourceChannelMapping
	assert.Error(t, model.DB.Where("source_id = ? AND upstream_group_id = ?", source.Id, "10").First(&created).Error)

	var reloaded model.UpstreamSource
	require.NoError(t, model.DB.First(&reloaded, source.Id).Error)
	assert.NotEqual(t, model.UpstreamDiscoveryStatusSucceeded, reloaded.LastDiscoveryStatus)
	assert.Zero(t, reloaded.LastDiscoveryTime)
}

func TestDiscoverUpstreamSourceFailureDoesNotMutateChannels(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	source := createDiscoveryTestSource(t)
	baseURL := "https://channel.example.com"
	priority := int64(5)
	weight := uint(10)
	channel := model.Channel{
		Type:     constant.ChannelTypeOpenAI,
		Key:      "sk-local-channel",
		Status:   common.ChannelStatusEnabled,
		Name:     "local channel",
		BaseURL:  &baseURL,
		Models:   "gpt-4o",
		Group:    "default",
		Priority: &priority,
		Weight:   &weight,
	}
	require.NoError(t, model.DB.Create(&channel).Error)
	service := UpstreamSourceService{
		AdapterFactory: func(sourceType string) (UpstreamSourceAdapter, error) {
			return fakeUpstreamSourceAdapter{err: errors.New("upstream failed")}, nil
		},
		Now: func() int64 { return 444 },
	}

	_, err := service.Discover(context.Background(), source.Id)

	require.Error(t, err)
	var channels []model.Channel
	require.NoError(t, model.DB.Find(&channels).Error)
	require.Len(t, channels, 1)
	assert.Equal(t, channel.Id, channels[0].Id)
	assert.Equal(t, "local channel", channels[0].Name)
	assert.Equal(t, "sk-local-channel", channels[0].Key)
	assert.Equal(t, common.ChannelStatusEnabled, channels[0].Status)

	var mappingCount int64
	require.NoError(t, model.DB.Model(&model.UpstreamSourceChannelMapping{}).Count(&mappingCount).Error)
	assert.Equal(t, int64(0), mappingCount)

	var reloaded model.UpstreamSource
	require.NoError(t, model.DB.First(&reloaded, source.Id).Error)
	assert.Equal(t, model.UpstreamDiscoveryStatusFailed, reloaded.LastDiscoveryStatus)
	assert.Equal(t, int64(444), reloaded.LastDiscoveryTime)
	assert.Contains(t, reloaded.LastDiscoveryError, "upstream failed")
}

func TestDiscoverUpstreamSourceNilAdapterFactoryFailsWithoutPanic(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	source := createDiscoveryTestSource(t)
	service := UpstreamSourceService{
		AdapterFactory: func(sourceType string) (UpstreamSourceAdapter, error) {
			return nil, nil
		},
		Now: func() int64 { return 777 },
	}

	result, err := service.Discover(context.Background(), source.Id)

	require.Error(t, err)
	require.NotNil(t, result)
	assert.Equal(t, source.Id, result.SourceID)
	assert.Contains(t, result.Error, "adapter")

	var reloaded model.UpstreamSource
	require.NoError(t, model.DB.First(&reloaded, source.Id).Error)
	assert.Equal(t, model.UpstreamDiscoveryStatusFailed, reloaded.LastDiscoveryStatus)
	assert.Equal(t, int64(777), reloaded.LastDiscoveryTime)
	assert.Contains(t, reloaded.LastDiscoveryError, "adapter")
}

func TestDiscoverUpstreamSourceUnknownMultiplierIsInvalidForSync(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	source := createDiscoveryTestSource(t)
	service := UpstreamSourceService{
		AdapterFactory: func(sourceType string) (UpstreamSourceAdapter, error) {
			return fakeUpstreamSourceAdapter{groups: []UpstreamGroup{
				{ID: "10", Name: "unknown price", Platform: "openai", Status: "enabled"},
			}}, nil
		},
		Now: func() int64 { return 555 },
	}

	result, err := service.Discover(context.Background(), source.Id)

	require.NoError(t, err)
	assert.Equal(t, 1, result.Discovered)
	assert.Equal(t, 0, result.Active)
	assert.Equal(t, 1, result.Invalid)
	require.Len(t, result.Mappings, 1)
	assert.Equal(t, model.UpstreamMappingDiscoveryStatusInvalid, result.Mappings[0].DiscoveryStatus)
	assert.False(t, result.Mappings[0].SyncEnabled)
	assert.Nil(t, result.Mappings[0].EffectiveRateMultiplier)

	var mapping model.UpstreamSourceChannelMapping
	require.NoError(t, model.DB.Where("source_id = ? AND upstream_group_id = ?", source.Id, "10").First(&mapping).Error)
	assert.Equal(t, model.UpstreamMappingDiscoveryStatusInvalid, mapping.DiscoveryStatus)
	assert.False(t, mapping.SyncEnabled)
	assert.Nil(t, mapping.EffectiveRateMultiplier)
}

func TestDiscoverUpstreamSourceStoresSanitizedCappedError(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	source := createDiscoveryTestSource(t)
	rawKey := "sk-" + strings.Repeat("a", 32)
	service := UpstreamSourceService{
		AdapterFactory: func(sourceType string) (UpstreamSourceAdapter, error) {
			return fakeUpstreamSourceAdapter{err: errors.New("failed with Authorization: Bearer bearer-secret password=body-password token=query-token " + rawKey + " " + strings.Repeat("x", 2000))}, nil
		},
		Now: func() int64 { return 666 },
	}

	_, err := service.Discover(context.Background(), source.Id)

	require.Error(t, err)
	var reloaded model.UpstreamSource
	require.NoError(t, model.DB.First(&reloaded, source.Id).Error)
	assert.Equal(t, model.UpstreamDiscoveryStatusFailed, reloaded.LastDiscoveryStatus)
	assert.Equal(t, int64(666), reloaded.LastDiscoveryTime)
	assert.LessOrEqual(t, len(reloaded.LastDiscoveryError), 1024)
	assert.NotContains(t, reloaded.LastDiscoveryError, "bearer-secret")
	assert.NotContains(t, reloaded.LastDiscoveryError, "body-password")
	assert.NotContains(t, reloaded.LastDiscoveryError, "query-token")
	assert.NotContains(t, reloaded.LastDiscoveryError, rawKey)
}
