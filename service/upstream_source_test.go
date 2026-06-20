package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type fakeUpstreamSourceAdapter struct {
	groups             []UpstreamGroup
	err                error
	createKeys         []UpstreamKey
	createErr          error
	updateKey          UpstreamKey
	updateErr          error
	listKeys           []UpstreamKey
	listErr            error
	keepEmptyUpdateKey bool
	createCalls        *[]fakeUpstreamSourceCreateKeyCall
	updateCalls        *[]fakeUpstreamSourceUpdateKeyCall
	listCalls          *[]string
}

type fakeUpstreamSourceCreateKeyCall struct {
	GroupID string
	Name    string
}

type fakeUpstreamSourceUpdateKeyCall struct {
	KeyID   string
	GroupID string
	Name    string
}

func (a fakeUpstreamSourceAdapter) DiscoverGroups(ctx context.Context, source *model.UpstreamSource) ([]UpstreamGroup, error) {
	if a.err != nil {
		return nil, a.err
	}
	return a.groups, nil
}

func (a fakeUpstreamSourceAdapter) CreateKey(ctx context.Context, source *model.UpstreamSource, groupID string, name string) (UpstreamKey, error) {
	callIndex := 0
	if a.createCalls != nil {
		callIndex = len(*a.createCalls)
		*a.createCalls = append(*a.createCalls, fakeUpstreamSourceCreateKeyCall{GroupID: groupID, Name: name})
	}
	if a.createErr != nil {
		return UpstreamKey{}, a.createErr
	}
	if len(a.createKeys) > 0 {
		if callIndex >= len(a.createKeys) {
			callIndex = len(a.createKeys) - 1
		}
		key := a.createKeys[callIndex]
		if key.GroupID == "" {
			key.GroupID = groupID
		}
		if key.Name == "" {
			key.Name = name
		}
		return key, nil
	}
	return UpstreamKey{ID: "key-" + groupID, Key: "sk-" + groupID, Name: name, GroupID: groupID}, nil
}

func (a fakeUpstreamSourceAdapter) UpdateKey(ctx context.Context, source *model.UpstreamSource, keyID string, groupID string, name string) (UpstreamKey, error) {
	if a.updateCalls != nil {
		*a.updateCalls = append(*a.updateCalls, fakeUpstreamSourceUpdateKeyCall{KeyID: keyID, GroupID: groupID, Name: name})
	}
	if a.updateErr != nil {
		return UpstreamKey{}, a.updateErr
	}
	key := a.updateKey
	if key.ID == "" {
		key.ID = keyID
	}
	if key.GroupID == "" {
		key.GroupID = groupID
	}
	if key.Name == "" {
		key.Name = name
	}
	if key.Key == "" && !a.keepEmptyUpdateKey {
		key.Key = "sk-updated-" + groupID
	}
	return key, nil
}

func (a fakeUpstreamSourceAdapter) ListKeys(ctx context.Context, source *model.UpstreamSource, groupID string) ([]UpstreamKey, error) {
	if a.listCalls != nil {
		*a.listCalls = append(*a.listCalls, groupID)
	}
	if a.listErr != nil {
		return nil, a.listErr
	}
	if a.listKeys != nil {
		return a.listKeys, nil
	}
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
	assert.True(t, result.Mappings[0].SyncEnabled)

	var mappings []model.UpstreamSourceChannelMapping
	require.NoError(t, model.DB.Order("upstream_group_id").Find(&mappings).Error)
	require.Len(t, mappings, 2)
	assert.Equal(t, model.UpstreamMappingDiscoveryStatusActive, mappings[0].DiscoveryStatus)
	assert.True(t, mappings[0].SyncEnabled)
	assert.Equal(t, int64(12345), mappings[0].LastDiscoveredAt)
	require.NotNil(t, mappings[0].EffectiveRateMultiplier)
	assert.Equal(t, rateA, *mappings[0].EffectiveRateMultiplier)

	var reloaded model.UpstreamSource
	require.NoError(t, model.DB.First(&reloaded, source.Id).Error)
	assert.Equal(t, model.UpstreamDiscoveryStatusSucceeded, reloaded.LastDiscoveryStatus)
	assert.Equal(t, int64(12345), reloaded.LastDiscoveryTime)
	assert.Empty(t, reloaded.LastDiscoveryError)
}

func TestDiscoverUpstreamSourceDeduplicatesTrimmedGroupIDs(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	source := createDiscoveryTestSource(t)
	rateA := 0.5
	rateB := 1.5
	groups := []UpstreamGroup{
		{ID: " 10 ", Name: "first invalid", Platform: "openai", Status: "enabled", RateMultiplier: &rateA},
		{ID: "10", Name: "last", Platform: "claude", Status: "disabled", RateMultiplier: &rateB, EffectiveRateMultiplier: &rateB},
		{ID: "   ", Name: "blank", Platform: "openai", Status: "enabled", RateMultiplier: &rateA, EffectiveRateMultiplier: &rateA},
	}
	mappings, discoveredIDs, invalidCount := discoveredGroupsToMappings(source.Id, groups, 12345)
	require.Len(t, mappings, 1)
	assert.Equal(t, []string{"10"}, discoveredIDs)
	assert.Equal(t, 2, invalidCount)
	assert.Equal(t, "10", mappings[0].UpstreamGroupID)
	assert.Equal(t, "last", mappings[0].UpstreamGroupName)
	assert.Equal(t, "claude", mappings[0].UpstreamPlatform)

	require.NoError(t, model.DB.Create(&model.UpstreamSourceChannelMapping{
		SourceID:        source.Id,
		UpstreamGroupID: "20",
		DiscoveryStatus: model.UpstreamMappingDiscoveryStatusActive,
	}).Error)
	service := UpstreamSourceService{
		AdapterFactory: func(sourceType string) (UpstreamSourceAdapter, error) {
			return fakeUpstreamSourceAdapter{groups: groups}, nil
		},
		Now: func() int64 { return 12345 },
	}

	result, err := service.Discover(context.Background(), source.Id)

	require.NoError(t, err)
	assert.Equal(t, 3, result.Discovered)
	assert.Equal(t, 1, result.Active)
	assert.Equal(t, 2, result.Invalid)
	assert.Equal(t, 1, result.Stale)
	require.Len(t, result.Mappings, 2)

	var mapping model.UpstreamSourceChannelMapping
	require.NoError(t, model.DB.Where("source_id = ? AND upstream_group_id = ?", source.Id, "10").First(&mapping).Error)
	assert.Equal(t, "last", mapping.UpstreamGroupName)
	assert.Equal(t, "claude", mapping.UpstreamPlatform)
	assert.Equal(t, "disabled", mapping.UpstreamStatus)
	require.NotNil(t, mapping.EffectiveRateMultiplier)
	assert.Equal(t, rateB, *mapping.EffectiveRateMultiplier)

	var duplicateCount int64
	require.NoError(t, model.DB.Model(&model.UpstreamSourceChannelMapping{}).Where("source_id = ? AND upstream_group_id = ?", source.Id, "10").Count(&duplicateCount).Error)
	assert.Equal(t, int64(1), duplicateCount)

	var blankCount int64
	require.NoError(t, model.DB.Model(&model.UpstreamSourceChannelMapping{}).Where("source_id = ? AND upstream_group_id = ?", source.Id, "").Count(&blankCount).Error)
	assert.Equal(t, int64(0), blankCount)

	var stale model.UpstreamSourceChannelMapping
	require.NoError(t, model.DB.Where("source_id = ? AND upstream_group_id = ?", source.Id, "20").First(&stale).Error)
	assert.Equal(t, model.UpstreamMappingDiscoveryStatusStale, stale.DiscoveryStatus)
	assert.Equal(t, int64(12345), stale.LastDiscoveredAt)
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

func TestDiscoverUpstreamSourceDoesNotReenableDeselectedMapping(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	source := createDiscoveryTestSource(t)
	rate := 0.75
	existing := model.UpstreamSourceChannelMapping{
		SourceID:        source.Id,
		SyncEnabled:     false,
		UpstreamGroupID: "10",
		DiscoveryStatus: model.UpstreamMappingDiscoveryStatusActive,
	}
	require.NoError(t, model.DB.Create(&existing).Error)
	service := UpstreamSourceService{
		AdapterFactory: func(sourceType string) (UpstreamSourceAdapter, error) {
			return fakeUpstreamSourceAdapter{groups: []UpstreamGroup{
				{ID: "10", Name: "renamed", Platform: "openai", Status: "enabled", RateMultiplier: &rate, EffectiveRateMultiplier: &rate},
			}}, nil
		},
		Now: func() int64 { return 223 },
	}

	_, err := service.Discover(context.Background(), source.Id)

	require.NoError(t, err)
	var reloaded model.UpstreamSourceChannelMapping
	require.NoError(t, model.DB.Where("source_id = ? AND upstream_group_id = ?", source.Id, "10").First(&reloaded).Error)
	assert.False(t, reloaded.SyncEnabled)
	assert.Equal(t, "renamed", reloaded.UpstreamGroupName)
	assert.Equal(t, int64(223), reloaded.LastDiscoveredAt)
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
	assert.False(t, stale.SyncEnabled)
	assert.Equal(t, "key-20", stale.UpstreamKeyID)
	assert.Equal(t, 88, stale.LocalChannelID)
	assert.Equal(t, model.UpstreamMappingSyncStatusSynced, stale.SyncStatus)

	var staleFromInvalid model.UpstreamSourceChannelMapping
	require.NoError(t, model.DB.Where("source_id = ? AND upstream_group_id = ?", source.Id, "30").First(&staleFromInvalid).Error)
	assert.Equal(t, model.UpstreamMappingDiscoveryStatusStale, staleFromInvalid.DiscoveryStatus)
	assert.Equal(t, int64(333), staleFromInvalid.LastDiscoveredAt)
	assert.False(t, staleFromInvalid.SyncEnabled)
	assert.Equal(t, "key-30", staleFromInvalid.UpstreamKeyID)
	assert.Equal(t, 99, staleFromInvalid.LocalChannelID)
	assert.Equal(t, model.UpstreamMappingSyncStatusFailed, staleFromInvalid.SyncStatus)
}

func TestDiscoverUpstreamSourceDisablesGeneratedChannelForMissingGroup(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	source := createDiscoveryTestSource(t)
	rate := 1.0
	channel := model.Channel{
		Name:   "source-a / 1.000x",
		Type:   constant.ChannelTypeOpenAI,
		Key:    "sk-local",
		Status: common.ChannelStatusEnabled,
		Group:  "default",
	}
	require.NoError(t, model.DB.Create(&channel).Error)
	mapping := model.UpstreamSourceChannelMapping{
		SourceID:                source.Id,
		SyncEnabled:             true,
		UpstreamGroupID:         "20",
		UpstreamGroupName:       "removed",
		DiscoveryStatus:         model.UpstreamMappingDiscoveryStatusActive,
		UpstreamKeyID:           "key-20",
		LocalChannelID:          channel.Id,
		SyncStatus:              model.UpstreamMappingSyncStatusSynced,
		EffectiveRateMultiplier: &rate,
	}
	require.NoError(t, model.DB.Create(&mapping).Error)
	settings := channel.GetOtherSettings()
	settings.GeneratedByUpstreamSourceID = source.Id
	settings.GeneratedByUpstreamMappingID = mapping.Id
	channel.SetOtherSettings(settings)
	require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", channel.Id).Update("settings", channel.OtherSettings).Error)
	service := UpstreamSourceService{
		AdapterFactory: func(sourceType string) (UpstreamSourceAdapter, error) {
			return fakeUpstreamSourceAdapter{groups: []UpstreamGroup{}}, nil
		},
		Now: func() int64 { return 334 },
	}

	result, err := service.Discover(context.Background(), source.Id)

	require.NoError(t, err)
	assert.Equal(t, 1, result.Stale)
	var reloadedMapping model.UpstreamSourceChannelMapping
	require.NoError(t, model.DB.First(&reloadedMapping, mapping.Id).Error)
	assert.Equal(t, model.UpstreamMappingDiscoveryStatusStale, reloadedMapping.DiscoveryStatus)
	assert.False(t, reloadedMapping.SyncEnabled)
	assert.Equal(t, channel.Id, reloadedMapping.LocalChannelID)
	var reloadedChannel model.Channel
	require.NoError(t, model.DB.First(&reloadedChannel, channel.Id).Error)
	assert.Equal(t, common.ChannelStatusManuallyDisabled, reloadedChannel.Status)
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

func TestDiscoverUpstreamSourceDisabledSourceFailsBeforeAdapterCall(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	source := createDiscoveryTestSource(t)
	require.NoError(t, model.DB.Model(&model.UpstreamSource{}).Where("id = ?", source.Id).Update("status", model.UpstreamSourceStatusDisabled).Error)
	var adapterFactoryCalled bool
	service := UpstreamSourceService{
		AdapterFactory: func(sourceType string) (UpstreamSourceAdapter, error) {
			adapterFactoryCalled = true
			return fakeUpstreamSourceAdapter{groups: []UpstreamGroup{{ID: "10", Name: "should not run"}}}, nil
		},
		Now: func() int64 { return 888 },
	}

	result, err := service.Discover(context.Background(), source.Id)

	require.Error(t, err)
	require.NotNil(t, result)
	assert.False(t, adapterFactoryCalled)
	assert.Contains(t, result.Error, "enabled")

	var reloaded model.UpstreamSource
	require.NoError(t, model.DB.First(&reloaded, source.Id).Error)
	assert.Equal(t, model.UpstreamDiscoveryStatusFailed, reloaded.LastDiscoveryStatus)
	assert.Equal(t, int64(888), reloaded.LastDiscoveryTime)
	assert.Contains(t, reloaded.LastDiscoveryError, "enabled")
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

func createSyncTestSource(t *testing.T, syncConfig map[string]any) model.UpstreamSource {
	t.Helper()

	source := createDiscoveryTestSource(t)
	if syncConfig != nil {
		data, err := common.Marshal(syncConfig)
		require.NoError(t, err)
		require.NoError(t, model.DB.Model(&model.UpstreamSource{}).Where("id = ?", source.Id).Update("sync_config", string(data)).Error)
		source.SyncConfig = string(data)
	}
	return source
}

func createSyncTestMapping(t *testing.T, sourceID int, groupID string, groupName string, multiplier *float64) model.UpstreamSourceChannelMapping {
	t.Helper()

	mapping := model.UpstreamSourceChannelMapping{
		SourceID:                sourceID,
		SyncEnabled:             true,
		UpstreamGroupID:         groupID,
		UpstreamGroupName:       groupName,
		UpstreamPlatform:        "openai",
		DiscoveryStatus:         model.UpstreamMappingDiscoveryStatusActive,
		UpstreamStatus:          model.UpstreamSourceStatusEnabled,
		EffectiveRateMultiplier: multiplier,
	}
	require.NoError(t, model.DB.Create(&mapping).Error)
	return mapping
}

func TestParseUpstreamSourceSyncConfigSupportsAutoSyncAndLocalGroupRules(t *testing.T) {
	raw, err := common.Marshal(map[string]any{
		"local_group":                "legacy",
		"auto_sync_enabled":          true,
		"auto_sync_interval_minutes": 3,
		"default_local_group":        "regular",
		"local_group_rules": []map[string]any{
			{
				"name":                 "Pro",
				"local_group":          "pro",
				"name_contains":        []string{" Pro ", "PLUS"},
				"description_contains": []string{"member"},
			},
		},
	})
	require.NoError(t, err)

	config, err := parseUpstreamSourceSyncConfig(string(raw))

	require.NoError(t, err)
	assert.True(t, config.AutoSyncEnabled)
	assert.Equal(t, 5, config.AutoSyncIntervalMinutes)
	assert.Equal(t, "regular", config.DefaultLocalGroup)
	require.Len(t, config.LocalGroupRules, 1)
	assert.Equal(t, "Pro", config.LocalGroupRules[0].Name)
	assert.Equal(t, "pro", config.LocalGroupRules[0].LocalGroup)
	assert.Equal(t, []string{"pro", "plus"}, config.LocalGroupRules[0].NameContains)
	assert.Equal(t, []string{"member"}, config.LocalGroupRules[0].DescriptionContains)
}

func TestSyncUpstreamSourceCreatesChannelPerSelectedGroup(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	source := createSyncTestSource(t, map[string]any{
		"local_group":              "paid",
		"default_priority":         7,
		"default_weight":           11,
		"enable_monitor":           true,
		"monitor_interval_minutes": 15,
	})
	rate := 1.0
	createSyncTestMapping(t, source.Id, "10", "primary", &rate)
	createSyncTestMapping(t, source.Id, "20", "backup", &rate)
	createCalls := make([]fakeUpstreamSourceCreateKeyCall, 0)
	service := UpstreamSourceService{
		AdapterFactory: func(sourceType string) (UpstreamSourceAdapter, error) {
			return fakeUpstreamSourceAdapter{
				createKeys: []UpstreamKey{
					{ID: "key-10", Key: "sk-secret-10"},
					{ID: "key-20", Key: "sk-secret-20"},
				},
				createCalls: &createCalls,
			}, nil
		},
		FetchModels: func(channel *model.Channel) ([]string, error) {
			require.NotNil(t, channel.BaseURL)
			assert.Equal(t, "https://relay.example.com", *channel.BaseURL)
			assert.NotEmpty(t, channel.Key)
			return []string{" gpt-4o ", "gpt-4o", "claude-3-haiku"}, nil
		},
		Now: func() int64 { return 1000 },
	}

	result, err := service.Sync(context.Background(), source.Id)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, source.Id, result.SourceID)
	assert.Equal(t, model.UpstreamSyncStatusSucceeded, result.Status)
	assert.Equal(t, 2, result.Created)
	assert.Equal(t, 0, result.Updated)
	assert.Equal(t, 0, result.Skipped)
	assert.Equal(t, 0, result.Failed)
	require.Len(t, result.Results, 2)
	assert.Equal(t, model.UpstreamMappingSyncStatusSynced, result.Results[0].Status)
	assert.True(t, result.Results[0].Created)
	assert.Empty(t, result.Results[0].Error)
	require.Len(t, createCalls, 2)
	assert.Equal(t, fakeUpstreamSourceCreateKeyCall{GroupID: "10", Name: "Wynth API / source-a / primary"}, createCalls[0])
	assert.Equal(t, fakeUpstreamSourceCreateKeyCall{GroupID: "20", Name: "Wynth API / source-a / backup"}, createCalls[1])

	var channels []model.Channel
	require.NoError(t, model.DB.Order("id").Find(&channels).Error)
	require.Len(t, channels, 2)
	assert.Equal(t, "source-a / 1.000x", channels[0].Name)
	assert.Equal(t, constant.ChannelTypeOpenAI, channels[0].Type)
	require.NotNil(t, channels[0].BaseURL)
	assert.Equal(t, "https://relay.example.com", *channels[0].BaseURL)
	assert.Equal(t, "sk-secret-10", channels[0].Key)
	assert.Equal(t, "paid", channels[0].Group)
	require.NotNil(t, channels[0].Priority)
	assert.Equal(t, int64(7), *channels[0].Priority)
	require.NotNil(t, channels[0].Weight)
	assert.Equal(t, uint(11), *channels[0].Weight)
	require.NotNil(t, channels[0].Tag)
	assert.Equal(t, "source-a", *channels[0].Tag)
	assert.Equal(t, "gpt-4o,claude-3-haiku", channels[0].Models)
	assert.Equal(t, common.ChannelStatusEnabled, channels[0].Status)
	settings := channels[0].GetOtherSettings()
	assert.True(t, settings.ChannelMonitorEnabled)
	assert.Equal(t, 15, settings.ChannelMonitorIntervalMinutes)

	var mappings []model.UpstreamSourceChannelMapping
	require.NoError(t, model.DB.Order("upstream_group_id").Find(&mappings).Error)
	require.Len(t, mappings, 2)
	assert.Equal(t, "key-10", mappings[0].UpstreamKeyID)
	assert.Equal(t, channels[0].Id, mappings[0].LocalChannelID)
	assert.Equal(t, model.UpstreamMappingSyncStatusSynced, mappings[0].SyncStatus)
	assert.Equal(t, int64(1000), mappings[0].LastSyncedAt)
	assert.Empty(t, mappings[0].LastError)

	var abilityCount int64
	require.NoError(t, model.DB.Model(&model.Ability{}).Count(&abilityCount).Error)
	assert.Equal(t, int64(4), abilityCount)
}

func TestSyncUpstreamSourceUsesShortRateNameAndRemark(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	source := createSyncTestSource(t, nil)
	rate := 0.75
	createSyncTestMapping(t, source.Id, "10", "primary", &rate)
	service := UpstreamSourceService{
		AdapterFactory: func(sourceType string) (UpstreamSourceAdapter, error) {
			return fakeUpstreamSourceAdapter{createKeys: []UpstreamKey{{ID: "key-10", Key: "sk-secret-10"}}}, nil
		},
		FetchModels: func(channel *model.Channel) ([]string, error) {
			return []string{"gpt-4o"}, nil
		},
		Now: func() int64 { return 1001 },
	}

	result, err := service.Sync(context.Background(), source.Id)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, 1, result.Created)
	var channel model.Channel
	require.NoError(t, model.DB.First(&channel).Error)
	assert.Equal(t, "source-a / 0.750x", channel.Name)
	require.NotNil(t, channel.Remark)
	assert.Contains(t, *channel.Remark, "primary")
	assert.Contains(t, *channel.Remark, "10")
	assert.Contains(t, *channel.Remark, "0.750x")
}

func TestSyncUpstreamSourceRoutesLocalGroupByRule(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	source := createSyncTestSource(t, map[string]any{
		"default_local_group": "regular",
		"local_group_rules": []map[string]any{
			{
				"name":          "Pro",
				"local_group":   "pro",
				"name_contains": []string{"pro"},
			},
		},
	})
	rate := 1.0
	createSyncTestMapping(t, source.Id, "10", "GPT Pro", &rate)
	createSyncTestMapping(t, source.Id, "20", "GPT Basic", &rate)
	service := UpstreamSourceService{
		AdapterFactory: func(sourceType string) (UpstreamSourceAdapter, error) {
			return fakeUpstreamSourceAdapter{
				createKeys: []UpstreamKey{
					{ID: "key-10", Key: "sk-secret-10"},
					{ID: "key-20", Key: "sk-secret-20"},
				},
			}, nil
		},
		FetchModels: func(channel *model.Channel) ([]string, error) {
			return []string{"gpt-4o"}, nil
		},
		Now: func() int64 { return 1002 },
	}

	result, err := service.Sync(context.Background(), source.Id)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, 2, result.Created)
	var channels []model.Channel
	require.NoError(t, model.DB.Order("key").Find(&channels).Error)
	require.Len(t, channels, 2)
	assert.Equal(t, "pro", channels[0].Group)
	assert.Equal(t, "regular", channels[1].Group)
}

func TestSyncUpstreamSourceFetchesModelsFromAllowedPrivateRelayBaseURL(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	withSub2APIFetchSetting(t, false)
	var requestCount int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/v1/models", r.URL.Path)
		assert.Equal(t, "Bearer sk-secret-10", r.Header.Get("Authorization"))
		writeUpstreamModelFetchTestJSON(t, w, map[string]any{
			"data": []map[string]string{{"id": "gpt-4o"}},
		})
	}))
	t.Cleanup(server.Close)
	source := createSyncTestSource(t, map[string]any{
		"allow_private_ip": true,
	})
	require.NoError(t, model.DB.Model(&model.UpstreamSource{}).Where("id = ?", source.Id).Updates(map[string]any{
		"base_url":       server.URL,
		"relay_base_url": server.URL,
	}).Error)
	rate := 1.0
	createSyncTestMapping(t, source.Id, "10", "primary", &rate)
	service := UpstreamSourceService{
		AdapterFactory: func(sourceType string) (UpstreamSourceAdapter, error) {
			return fakeUpstreamSourceAdapter{createKeys: []UpstreamKey{{ID: "key-10", Key: "sk-secret-10"}}}, nil
		},
		Now: func() int64 { return 1010 },
	}

	result, err := service.Sync(context.Background(), source.Id)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, model.UpstreamSyncStatusSucceeded, result.Status)
	assert.Equal(t, 1, requestCount)
	var channel model.Channel
	require.NoError(t, model.DB.First(&channel).Error)
	assert.Equal(t, "gpt-4o", channel.Models)
}

func TestSyncUpstreamSourceAcceptsNumericPrivateIPFlagFromExistingConfig(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	source := createSyncTestSource(t, map[string]any{
		"allow_private_ip": 1,
	})
	rate := 1.0
	createSyncTestMapping(t, source.Id, "10", "primary", &rate)
	service := UpstreamSourceService{
		AdapterFactory: func(sourceType string) (UpstreamSourceAdapter, error) {
			return fakeUpstreamSourceAdapter{createKeys: []UpstreamKey{{ID: "key-10", Key: "sk-secret-10"}}}, nil
		},
		FetchModels: func(channel *model.Channel) ([]string, error) {
			return []string{"gpt-4o"}, nil
		},
		Now: func() int64 { return 1009 },
	}

	result, err := service.Sync(context.Background(), source.Id)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, model.UpstreamSyncStatusSucceeded, result.Status)
	assert.Equal(t, 1, result.Created)
}

func TestSyncUpstreamSourceRequiresSelectedMappings(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	source := createSyncTestSource(t, nil)
	adapterFactoryCalled := false
	service := UpstreamSourceService{
		AdapterFactory: func(sourceType string) (UpstreamSourceAdapter, error) {
			adapterFactoryCalled = true
			return fakeUpstreamSourceAdapter{}, nil
		},
		Now: func() int64 { return 1001 },
	}

	result, err := service.Sync(context.Background(), source.Id)

	require.Error(t, err)
	require.NotNil(t, result)
	assert.Equal(t, model.UpstreamSyncStatusFailed, result.Status)
	assert.Contains(t, result.Error, "select")
	assert.False(t, adapterFactoryCalled)

	var reloaded model.UpstreamSource
	require.NoError(t, model.DB.First(&reloaded, source.Id).Error)
	assert.Empty(t, reloaded.CurrentSyncToken)
	assert.Equal(t, model.UpstreamSyncStatusFailed, reloaded.LastSyncStatus)
	assert.Contains(t, reloaded.LastSyncError, "select")
	assert.Equal(t, int64(1001), reloaded.LastSyncTime)
}

func TestSyncUpstreamSourceIsIdempotentByMappingID(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	source := createSyncTestSource(t, nil)
	rate := 1.0
	createSyncTestMapping(t, source.Id, "10", "primary", &rate)
	createCalls := make([]fakeUpstreamSourceCreateKeyCall, 0)
	updateCalls := make([]fakeUpstreamSourceUpdateKeyCall, 0)
	service := UpstreamSourceService{
		AdapterFactory: func(sourceType string) (UpstreamSourceAdapter, error) {
			return fakeUpstreamSourceAdapter{
				createKeys:  []UpstreamKey{{ID: "key-10", Key: "sk-created-10"}},
				updateKey:   UpstreamKey{ID: "key-10", Key: "sk-updated-10"},
				createCalls: &createCalls,
				updateCalls: &updateCalls,
			}, nil
		},
		FetchModels: func(channel *model.Channel) ([]string, error) {
			return []string{"gpt-4o"}, nil
		},
		Now: func() int64 { return 1001 },
	}

	first, err := service.Sync(context.Background(), source.Id)
	require.NoError(t, err)
	require.NotNil(t, first)
	assert.Equal(t, 1, first.Created)
	var firstMapping model.UpstreamSourceChannelMapping
	require.NoError(t, model.DB.Where("source_id = ? AND upstream_group_id = ?", source.Id, "10").First(&firstMapping).Error)
	firstChannelID := firstMapping.LocalChannelID

	second, err := service.Sync(context.Background(), source.Id)

	require.NoError(t, err)
	require.NotNil(t, second)
	assert.Equal(t, 0, second.Created)
	assert.Equal(t, 1, second.Updated)
	require.Len(t, createCalls, 1)
	require.Len(t, updateCalls, 1)
	assert.Equal(t, fakeUpstreamSourceUpdateKeyCall{KeyID: "key-10", GroupID: "10", Name: "Wynth API / source-a / primary"}, updateCalls[0])
	var channelCount int64
	require.NoError(t, model.DB.Model(&model.Channel{}).Count(&channelCount).Error)
	assert.Equal(t, int64(1), channelCount)
	var reloadedMapping model.UpstreamSourceChannelMapping
	require.NoError(t, model.DB.Where("source_id = ? AND upstream_group_id = ?", source.Id, "10").First(&reloadedMapping).Error)
	assert.Equal(t, firstChannelID, reloadedMapping.LocalChannelID)
	assert.Equal(t, "key-10", reloadedMapping.UpstreamKeyID)
	var channel model.Channel
	require.NoError(t, model.DB.First(&channel, firstChannelID).Error)
	assert.Equal(t, "sk-updated-10", channel.Key)
}

func TestSyncUpstreamSourceRecoversExistingKeyFromListWhenNoLocalChannel(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	source := createSyncTestSource(t, nil)
	rate := 1.0
	mapping := createSyncTestMapping(t, source.Id, "10", "primary", &rate)
	require.NoError(t, model.DB.Model(&model.UpstreamSourceChannelMapping{}).Where("id = ?", mapping.Id).Updates(map[string]any{
		"upstream_key_id": "key-10",
	}).Error)
	createCalls := make([]fakeUpstreamSourceCreateKeyCall, 0)
	listCalls := make([]string, 0)
	service := UpstreamSourceService{
		AdapterFactory: func(sourceType string) (UpstreamSourceAdapter, error) {
			return fakeUpstreamSourceAdapter{
				updateKey:          UpstreamKey{ID: "key-10"},
				keepEmptyUpdateKey: true,
				listKeys: []UpstreamKey{
					{ID: "other-key", Key: "sk-other", GroupID: "10"},
					{ID: "key-10", Key: "sk-listed-10", GroupID: "10"},
				},
				createCalls: &createCalls,
				listCalls:   &listCalls,
			}, nil
		},
		FetchModels: func(channel *model.Channel) ([]string, error) {
			return []string{"gpt-4o"}, nil
		},
		Now: func() int64 { return 1007 },
	}

	result, err := service.Sync(context.Background(), source.Id)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, model.UpstreamSyncStatusSucceeded, result.Status)
	assert.Equal(t, 1, result.Created)
	assert.Empty(t, createCalls)
	assert.Equal(t, []string{"10"}, listCalls)
	var reloadedMapping model.UpstreamSourceChannelMapping
	require.NoError(t, model.DB.First(&reloadedMapping, mapping.Id).Error)
	assert.Equal(t, "key-10", reloadedMapping.UpstreamKeyID)
	require.NotZero(t, reloadedMapping.LocalChannelID)
	var channel model.Channel
	require.NoError(t, model.DB.First(&channel, reloadedMapping.LocalChannelID).Error)
	assert.Equal(t, "sk-listed-10", channel.Key)
}

func TestSyncUpstreamSourceReplacesExistingKeyWhenListCannotRecover(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	source := createSyncTestSource(t, nil)
	rate := 1.0
	mapping := createSyncTestMapping(t, source.Id, "10", "primary", &rate)
	require.NoError(t, model.DB.Model(&model.UpstreamSourceChannelMapping{}).Where("id = ?", mapping.Id).Updates(map[string]any{
		"upstream_key_id": "key-10",
	}).Error)
	createCalls := make([]fakeUpstreamSourceCreateKeyCall, 0)
	listCalls := make([]string, 0)
	service := UpstreamSourceService{
		AdapterFactory: func(sourceType string) (UpstreamSourceAdapter, error) {
			return fakeUpstreamSourceAdapter{
				updateKey:          UpstreamKey{ID: "key-10"},
				keepEmptyUpdateKey: true,
				listKeys:           []UpstreamKey{{ID: "other-key", Key: "sk-other", GroupID: "10"}},
				createKeys:         []UpstreamKey{{ID: "key-replacement", Key: "sk-replacement-10"}},
				createCalls:        &createCalls,
				listCalls:          &listCalls,
			}, nil
		},
		FetchModels: func(channel *model.Channel) ([]string, error) {
			return []string{"gpt-4o"}, nil
		},
		Now: func() int64 { return 1008 },
	}

	result, err := service.Sync(context.Background(), source.Id)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, model.UpstreamSyncStatusSucceeded, result.Status)
	assert.Equal(t, 1, result.Created)
	require.Len(t, createCalls, 1)
	assert.Equal(t, fakeUpstreamSourceCreateKeyCall{GroupID: "10", Name: "Wynth API / source-a / primary"}, createCalls[0])
	assert.Equal(t, []string{"10"}, listCalls)
	var reloadedMapping model.UpstreamSourceChannelMapping
	require.NoError(t, model.DB.First(&reloadedMapping, mapping.Id).Error)
	assert.Equal(t, "key-replacement", reloadedMapping.UpstreamKeyID)
	require.NotZero(t, reloadedMapping.LocalChannelID)
	var channel model.Channel
	require.NoError(t, model.DB.First(&channel, reloadedMapping.LocalChannelID).Error)
	assert.Equal(t, "sk-replacement-10", channel.Key)
}

func TestSyncUpstreamSourcePreservesUnownedChannelFields(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	source := createSyncTestSource(t, map[string]any{
		"local_group":              "synced",
		"default_priority":         3,
		"default_weight":           4,
		"enable_monitor":           true,
		"monitor_interval_minutes": 25,
	})
	oldBaseURL := "https://old.example.com"
	remark := "operator remark"
	headerOverride := `{"X-Operator":"keep"}`
	paramOverride := `{"temperature":0}`
	oldPriority := int64(9)
	oldWeight := uint(99)
	channel := model.Channel{
		Type:           constant.ChannelTypeAnthropic,
		Key:            "sk-old",
		Status:         common.ChannelStatusManuallyDisabled,
		Name:           "operator name",
		BaseURL:        &oldBaseURL,
		Models:         "old-model",
		Group:          "old",
		Priority:       &oldPriority,
		Weight:         &oldWeight,
		Remark:         &remark,
		HeaderOverride: &headerOverride,
		ParamOverride:  &paramOverride,
		Balance:        12.5,
		TestTime:       77,
		ResponseTime:   88,
	}
	openRouterEnterprise := true
	channel.SetOtherSettings(dto.ChannelOtherSettings{
		OpenRouterEnterprise:                  &openRouterEnterprise,
		AllowServiceTier:                      true,
		UpstreamModelUpdateCheckEnabled:       true,
		UpstreamModelUpdateIgnoredModels:      []string{"keep-ignored"},
		ChannelMonitorEnabled:                 false,
		ChannelMonitorIntervalMinutes:         5,
		UpstreamModelUpdateLastDetectedModels: []string{"keep-detected"},
	})
	require.NoError(t, model.DB.Create(&channel).Error)
	rate := 1.0
	mapping := createSyncTestMapping(t, source.Id, "10", "primary", &rate)
	settings := channel.GetOtherSettings()
	settings.GeneratedByUpstreamSourceID = source.Id
	settings.GeneratedByUpstreamMappingID = mapping.Id
	channel.SetOtherSettings(settings)
	require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", channel.Id).Update("settings", channel.OtherSettings).Error)
	require.NoError(t, model.DB.Model(&model.UpstreamSourceChannelMapping{}).Where("id = ?", mapping.Id).Updates(map[string]any{
		"upstream_key_id":  "key-10",
		"local_channel_id": channel.Id,
	}).Error)
	service := UpstreamSourceService{
		AdapterFactory: func(sourceType string) (UpstreamSourceAdapter, error) {
			return fakeUpstreamSourceAdapter{updateKey: UpstreamKey{ID: "key-10", Key: "sk-new"}}, nil
		},
		FetchModels: func(channel *model.Channel) ([]string, error) {
			return []string{"gpt-4o"}, nil
		},
		Now: func() int64 { return 1002 },
	}

	result, err := service.Sync(context.Background(), source.Id)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, 0, result.Created)
	assert.Equal(t, 1, result.Updated)
	var reloaded model.Channel
	require.NoError(t, model.DB.First(&reloaded, channel.Id).Error)
	assert.Equal(t, "source-a / 1.000x", reloaded.Name)
	assert.Equal(t, constant.ChannelTypeOpenAI, reloaded.Type)
	require.NotNil(t, reloaded.BaseURL)
	assert.Equal(t, "https://relay.example.com", *reloaded.BaseURL)
	assert.Equal(t, "sk-new", reloaded.Key)
	assert.Equal(t, "synced", reloaded.Group)
	assert.Equal(t, "gpt-4o", reloaded.Models)
	assert.Equal(t, common.ChannelStatusEnabled, reloaded.Status)
	assert.Equal(t, remark, *reloaded.Remark)
	assert.Equal(t, headerOverride, *reloaded.HeaderOverride)
	assert.Equal(t, paramOverride, *reloaded.ParamOverride)
	assert.Equal(t, 12.5, reloaded.Balance)
	assert.Equal(t, int64(77), reloaded.TestTime)
	assert.Equal(t, 88, reloaded.ResponseTime)
	reloadedSettings := reloaded.GetOtherSettings()
	require.NotNil(t, reloadedSettings.OpenRouterEnterprise)
	assert.True(t, *reloadedSettings.OpenRouterEnterprise)
	assert.True(t, reloadedSettings.AllowServiceTier)
	assert.True(t, reloadedSettings.UpstreamModelUpdateCheckEnabled)
	assert.Equal(t, []string{"keep-ignored"}, reloadedSettings.UpstreamModelUpdateIgnoredModels)
	assert.Equal(t, []string{"keep-detected"}, reloadedSettings.UpstreamModelUpdateLastDetectedModels)
	assert.True(t, reloadedSettings.ChannelMonitorEnabled)
	assert.Equal(t, 25, reloadedSettings.ChannelMonitorIntervalMinutes)
}

func TestSyncUpstreamSourceWritesOwnedZeroValues(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	source := createSyncTestSource(t, map[string]any{
		"default_priority": 0,
		"default_weight":   0,
	})
	priority := int64(9)
	weight := uint(44)
	baseURL := "https://old.example.com"
	channel := model.Channel{
		Type:     constant.ChannelTypeOpenAI,
		Key:      "sk-old",
		Status:   common.ChannelStatusEnabled,
		Name:     "old",
		BaseURL:  &baseURL,
		Models:   "old-model",
		Group:    "old",
		Priority: &priority,
		Weight:   &weight,
	}
	require.NoError(t, model.DB.Create(&channel).Error)
	rate := 1.0
	mapping := createSyncTestMapping(t, source.Id, "10", "primary", &rate)
	channel.SetOtherSettings(dto.ChannelOtherSettings{
		GeneratedByUpstreamSourceID:  source.Id,
		GeneratedByUpstreamMappingID: mapping.Id,
	})
	require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", channel.Id).Update("settings", channel.OtherSettings).Error)
	require.NoError(t, model.DB.Model(&model.UpstreamSourceChannelMapping{}).Where("id = ?", mapping.Id).Updates(map[string]any{
		"upstream_key_id":  "key-10",
		"local_channel_id": channel.Id,
	}).Error)
	service := UpstreamSourceService{
		AdapterFactory: func(sourceType string) (UpstreamSourceAdapter, error) {
			return fakeUpstreamSourceAdapter{updateKey: UpstreamKey{ID: "key-10", Key: "sk-new"}}, nil
		},
		FetchModels: func(channel *model.Channel) ([]string, error) {
			return []string{"gpt-4o"}, nil
		},
		Now: func() int64 { return 1003 },
	}

	_, err := service.Sync(context.Background(), source.Id)

	require.NoError(t, err)
	var reloaded model.Channel
	require.NoError(t, model.DB.First(&reloaded, channel.Id).Error)
	require.NotNil(t, reloaded.Priority)
	assert.Equal(t, int64(0), *reloaded.Priority)
	require.NotNil(t, reloaded.Weight)
	assert.Equal(t, uint(0), *reloaded.Weight)
}

func TestSyncUpstreamSourceRecreatesMissingLocalChannel(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	source := createSyncTestSource(t, nil)
	rate := 1.0
	mapping := createSyncTestMapping(t, source.Id, "10", "primary", &rate)
	require.NoError(t, model.DB.Model(&model.UpstreamSourceChannelMapping{}).Where("id = ?", mapping.Id).Updates(map[string]any{
		"upstream_key_id":  "key-10",
		"local_channel_id": 999,
	}).Error)
	createCalls := make([]fakeUpstreamSourceCreateKeyCall, 0)
	updateCalls := make([]fakeUpstreamSourceUpdateKeyCall, 0)
	service := UpstreamSourceService{
		AdapterFactory: func(sourceType string) (UpstreamSourceAdapter, error) {
			return fakeUpstreamSourceAdapter{createCalls: &createCalls, updateCalls: &updateCalls}, nil
		},
		FetchModels: func(channel *model.Channel) ([]string, error) {
			return []string{"gpt-4o"}, nil
		},
		Now: func() int64 { return 1004 },
	}

	result, err := service.Sync(context.Background(), source.Id)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, model.UpstreamSyncStatusSucceeded, result.Status)
	assert.Equal(t, 1, result.Created)
	assert.Equal(t, 0, result.Failed)
	require.Len(t, result.Results, 1)
	assert.Equal(t, model.UpstreamMappingSyncStatusSynced, result.Results[0].Status)
	assert.Empty(t, createCalls)
	require.Len(t, updateCalls, 1)
	assert.Equal(t, fakeUpstreamSourceUpdateKeyCall{KeyID: "key-10", GroupID: "10", Name: "Wynth API / source-a / primary"}, updateCalls[0])
	var channelCount int64
	require.NoError(t, model.DB.Model(&model.Channel{}).Count(&channelCount).Error)
	assert.Equal(t, int64(1), channelCount)
	var reloaded model.UpstreamSourceChannelMapping
	require.NoError(t, model.DB.First(&reloaded, mapping.Id).Error)
	assert.Equal(t, model.UpstreamMappingSyncStatusSynced, reloaded.SyncStatus)
	assert.Empty(t, reloaded.LastError)
	assert.NotEqual(t, 999, reloaded.LocalChannelID)
	assert.NotZero(t, reloaded.LocalChannelID)
	assert.Equal(t, int64(1004), reloaded.LastSyncedAt)
}

func TestSyncUpstreamSourceDoesNotOverwriteChannelOwnedByDifferentMapping(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	source := createSyncTestSource(t, nil)
	rate := 1.0
	firstMapping := createSyncTestMapping(t, source.Id, "10", "primary", &rate)
	secondMapping := createSyncTestMapping(t, source.Id, "20", "backup", &rate)
	baseURL := "https://relay.example.com"
	priority := int64(0)
	weight := uint(0)
	channel := model.Channel{
		Type:     constant.ChannelTypeOpenAI,
		Key:      "sk-old-primary",
		Status:   common.ChannelStatusEnabled,
		Name:     "source-a / primary",
		BaseURL:  &baseURL,
		Models:   "old-model",
		Group:    "default",
		Priority: &priority,
		Weight:   &weight,
		Tag:      common.GetPointer("source-a"),
	}
	require.NoError(t, model.DB.Create(&channel).Error)
	require.NoError(t, model.DB.Model(&model.UpstreamSourceChannelMapping{}).Where("id IN ?", []int{firstMapping.Id, secondMapping.Id}).Updates(map[string]any{
		"upstream_key_id":  "key-shared",
		"local_channel_id": channel.Id,
	}).Error)
	service := UpstreamSourceService{
		AdapterFactory: func(sourceType string) (UpstreamSourceAdapter, error) {
			return fakeUpstreamSourceAdapter{}, nil
		},
		FetchModels: func(channel *model.Channel) ([]string, error) {
			return []string{"gpt-4o"}, nil
		},
		Now: func() int64 { return 1011 },
	}

	result, err := service.Sync(context.Background(), source.Id)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, model.UpstreamSyncStatusSucceeded, result.Status)
	assert.Equal(t, 1, result.Created)
	assert.Equal(t, 1, result.Updated)
	var channels []model.Channel
	require.NoError(t, model.DB.Order("id").Find(&channels).Error)
	require.Len(t, channels, 2)
	assert.Equal(t, "source-a / 1.000x", channels[0].Name)
	assert.Equal(t, "source-a / 1.000x", channels[1].Name)
	var reloadedFirst, reloadedSecond model.UpstreamSourceChannelMapping
	require.NoError(t, model.DB.First(&reloadedFirst, firstMapping.Id).Error)
	require.NoError(t, model.DB.First(&reloadedSecond, secondMapping.Id).Error)
	assert.Equal(t, channel.Id, reloadedFirst.LocalChannelID)
	assert.NotEqual(t, channel.Id, reloadedSecond.LocalChannelID)
	assert.NotZero(t, reloadedSecond.LocalChannelID)
}

func TestSyncUpstreamSourceDoesNotClaimShortNameChannelWithoutMetadata(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	source := createSyncTestSource(t, nil)
	rate := 1.0
	mapping := createSyncTestMapping(t, source.Id, "10", "primary", &rate)
	baseURL := "https://relay.example.com"
	priority := int64(0)
	weight := uint(0)
	channel := model.Channel{
		Type:     constant.ChannelTypeOpenAI,
		Key:      "sk-other",
		Status:   common.ChannelStatusEnabled,
		Name:     "source-a / 1.000x",
		BaseURL:  &baseURL,
		Models:   "old-model",
		Group:    "default",
		Priority: &priority,
		Weight:   &weight,
		Tag:      common.GetPointer("source-a"),
	}
	require.NoError(t, model.DB.Create(&channel).Error)
	require.NoError(t, model.DB.Model(&model.UpstreamSourceChannelMapping{}).Where("id = ?", mapping.Id).Updates(map[string]any{
		"upstream_key_id":  "key-10",
		"local_channel_id": channel.Id,
	}).Error)
	service := UpstreamSourceService{
		AdapterFactory: func(sourceType string) (UpstreamSourceAdapter, error) {
			return fakeUpstreamSourceAdapter{updateKey: UpstreamKey{ID: "key-10", Key: "sk-new"}}, nil
		},
		FetchModels: func(channel *model.Channel) ([]string, error) {
			return []string{"gpt-4o"}, nil
		},
		Now: func() int64 { return 1012 },
	}

	result, err := service.Sync(context.Background(), source.Id)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, 1, result.Created)
	assert.Equal(t, 0, result.Updated)
	var channels []model.Channel
	require.NoError(t, model.DB.Order("id").Find(&channels).Error)
	require.Len(t, channels, 2)
	assert.Equal(t, "sk-other", channels[0].Key)
	assert.Equal(t, "sk-new", channels[1].Key)
	var reloadedMapping model.UpstreamSourceChannelMapping
	require.NoError(t, model.DB.First(&reloadedMapping, mapping.Id).Error)
	assert.NotEqual(t, channel.Id, reloadedMapping.LocalChannelID)
}

func TestUpstreamSourceGeneratedChannelRemarkTruncatesUTF8Safely(t *testing.T) {
	rate := 1.0
	mapping := &model.UpstreamSourceChannelMapping{
		UpstreamGroupID:         "10",
		UpstreamGroupName:       strings.Repeat("中文", 200),
		UpstreamPlatform:        "openai",
		EffectiveRateMultiplier: &rate,
	}

	remark := upstreamSourceGeneratedChannelRemark(mapping)

	require.NotNil(t, remark)
	assert.LessOrEqual(t, len(*remark), 255)
	assert.True(t, utf8.ValidString(*remark))
}

func TestSyncUpstreamSourceDoesNotEnableChannelWithoutModels(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	source := createSyncTestSource(t, nil)
	rate := 1.0
	createSyncTestMapping(t, source.Id, "10", "primary", &rate)
	service := UpstreamSourceService{
		AdapterFactory: func(sourceType string) (UpstreamSourceAdapter, error) {
			return fakeUpstreamSourceAdapter{createKeys: []UpstreamKey{{ID: "key-10", Key: "sk-secret-10"}}}, nil
		},
		FetchModels: func(channel *model.Channel) ([]string, error) {
			return nil, nil
		},
		Now: func() int64 { return 1005 },
	}

	result, err := service.Sync(context.Background(), source.Id)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, model.UpstreamSyncStatusFailed, result.Status)
	assert.Equal(t, 1, result.Failed)
	require.Len(t, result.Results, 1)
	assert.Equal(t, model.UpstreamMappingSyncStatusFailed, result.Results[0].Status)
	assert.Contains(t, result.Results[0].Error, "models")
	var channel model.Channel
	require.NoError(t, model.DB.First(&channel).Error)
	assert.Equal(t, common.ChannelStatusManuallyDisabled, channel.Status)
	assert.Empty(t, channel.Models)
	var abilityCount int64
	require.NoError(t, model.DB.Model(&model.Ability{}).Count(&abilityCount).Error)
	assert.Equal(t, int64(0), abilityCount)
}

func TestSyncUpstreamSourceClaimsSourceBeforeSync(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	source := createSyncTestSource(t, nil)
	rate := 1.0
	mapping := createSyncTestMapping(t, source.Id, "10", "primary", &rate)
	require.NoError(t, model.DB.Model(&model.UpstreamSource{}).Where("id = ?", source.Id).Updates(map[string]any{
		"current_sync_token": "held-token",
		"sync_started_at":    int64(999),
		"last_sync_status":   model.UpstreamSyncStatusRunning,
	}).Error)
	adapterFactoryCalled := false
	service := UpstreamSourceService{
		AdapterFactory: func(sourceType string) (UpstreamSourceAdapter, error) {
			adapterFactoryCalled = true
			return fakeUpstreamSourceAdapter{}, nil
		},
		FetchModels: func(channel *model.Channel) ([]string, error) {
			return []string{"gpt-4o"}, nil
		},
		Now: func() int64 { return 1000 },
	}

	result, err := service.Sync(context.Background(), source.Id)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, model.UpstreamSyncStatusRunning, result.Status)
	assert.Contains(t, result.Error, "already")
	assert.False(t, adapterFactoryCalled)
	var channelCount int64
	require.NoError(t, model.DB.Model(&model.Channel{}).Count(&channelCount).Error)
	assert.Equal(t, int64(0), channelCount)
	var reloadedMapping model.UpstreamSourceChannelMapping
	require.NoError(t, model.DB.First(&reloadedMapping, mapping.Id).Error)
	assert.Equal(t, model.UpstreamMappingSyncStatusNeverSynced, reloadedMapping.SyncStatus)
	var reloadedSource model.UpstreamSource
	require.NoError(t, model.DB.First(&reloadedSource, source.Id).Error)
	assert.Equal(t, "held-token", reloadedSource.CurrentSyncToken)
}

func TestSyncUpstreamSourceStoresSanitizedCappedMappingError(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	source := createSyncTestSource(t, nil)
	rate := 1.0
	mapping := createSyncTestMapping(t, source.Id, "10", "primary", &rate)
	rawKey := "sk-" + strings.Repeat("a", 32)
	service := UpstreamSourceService{
		AdapterFactory: func(sourceType string) (UpstreamSourceAdapter, error) {
			return fakeUpstreamSourceAdapter{
				createErr: errors.New("failed with Authorization: Bearer bearer-secret password=body-password token=query-token " + rawKey + " " + strings.Repeat("x", 2000)),
			}, nil
		},
		FetchModels: func(channel *model.Channel) ([]string, error) {
			return []string{"gpt-4o"}, nil
		},
		Now: func() int64 { return 1006 },
	}

	result, err := service.Sync(context.Background(), source.Id)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, model.UpstreamSyncStatusFailed, result.Status)
	assert.Equal(t, 1, result.Failed)
	require.Len(t, result.Results, 1)
	assert.LessOrEqual(t, len(result.Results[0].Error), 1024)
	assert.NotContains(t, result.Results[0].Error, "bearer-secret")
	assert.NotContains(t, result.Results[0].Error, "body-password")
	assert.NotContains(t, result.Results[0].Error, "query-token")
	assert.NotContains(t, result.Results[0].Error, rawKey)

	var reloaded model.UpstreamSourceChannelMapping
	require.NoError(t, model.DB.First(&reloaded, mapping.Id).Error)
	assert.Equal(t, model.UpstreamMappingSyncStatusFailed, reloaded.SyncStatus)
	assert.LessOrEqual(t, len(reloaded.LastError), 1024)
	assert.NotContains(t, reloaded.LastError, "bearer-secret")
	assert.NotContains(t, reloaded.LastError, "body-password")
	assert.NotContains(t, reloaded.LastError, "query-token")
	assert.NotContains(t, reloaded.LastError, rawKey)

	var reloadedSource model.UpstreamSource
	require.NoError(t, model.DB.First(&reloadedSource, source.Id).Error)
	assert.Equal(t, model.UpstreamSyncStatusFailed, reloadedSource.LastSyncStatus)
	assert.NotContains(t, reloadedSource.LastSyncError, "bearer-secret")
	assert.NotContains(t, reloadedSource.LastSyncError, rawKey)
}

func TestListDueUpstreamSourcesForAutoSyncFiltersByScheduleAndStatus(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	due := createAutoSyncTestSource(t, "due", model.UpstreamSourceStatusEnabled, map[string]any{
		"auto_sync_enabled":          true,
		"auto_sync_interval_minutes": 30,
	}, 1000, "", 0)
	createAutoSyncTestSource(t, "disabled", model.UpstreamSourceStatusDisabled, map[string]any{
		"auto_sync_enabled":          true,
		"auto_sync_interval_minutes": 30,
	}, 1000, "", 0)
	createAutoSyncTestSource(t, "not-due", model.UpstreamSourceStatusEnabled, map[string]any{
		"auto_sync_enabled":          true,
		"auto_sync_interval_minutes": 30,
	}, 2900, "", 0)
	staleRunning := createAutoSyncTestSource(t, "stale-running", model.UpstreamSourceStatusEnabled, map[string]any{
		"auto_sync_enabled":          true,
		"auto_sync_interval_minutes": 30,
	}, 1000, "held-token", -1000)
	createAutoSyncTestSource(t, "fresh-running", model.UpstreamSourceStatusEnabled, map[string]any{
		"auto_sync_enabled":          true,
		"auto_sync_interval_minutes": 30,
	}, 1000, "fresh-token", 2990)
	createAutoSyncTestSource(t, "manual", model.UpstreamSourceStatusEnabled, map[string]any{
		"auto_sync_enabled":          false,
		"auto_sync_interval_minutes": 30,
	}, 1000, "", 0)

	sources, err := ListDueUpstreamSourcesForAutoSync(3000)

	require.NoError(t, err)
	require.Len(t, sources, 2)
	assert.Equal(t, []int{due.Id, staleRunning.Id}, []int{sources[0].Id, sources[1].Id})
}

func TestRunDueUpstreamSourceAutoSyncDiscoversThenSyncsDueSources(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	source := createAutoSyncTestSource(t, "due", model.UpstreamSourceStatusEnabled, map[string]any{
		"auto_sync_enabled":          true,
		"auto_sync_interval_minutes": 30,
		"default_local_group":        "regular",
	}, 1000, "", 0)
	createAutoSyncTestSource(t, "not-due", model.UpstreamSourceStatusEnabled, map[string]any{
		"auto_sync_enabled":          true,
		"auto_sync_interval_minutes": 30,
	}, 2900, "", 0)
	rate := 0.5
	createCalls := make([]fakeUpstreamSourceCreateKeyCall, 0)
	service := UpstreamSourceService{
		AdapterFactory: func(sourceType string) (UpstreamSourceAdapter, error) {
			return fakeUpstreamSourceAdapter{
				groups: []UpstreamGroup{
					{ID: "10", Name: "GPT Basic", Platform: "openai", Status: "enabled", RateMultiplier: &rate, EffectiveRateMultiplier: &rate},
				},
				createCalls: &createCalls,
			}, nil
		},
		FetchModels: func(channel *model.Channel) ([]string, error) {
			return []string{"gpt-4o"}, nil
		},
		Now: func() int64 { return 3000 },
	}

	results := service.RunDueUpstreamSourceAutoSync(context.Background(), 3000)

	require.Len(t, results, 1)
	assert.Equal(t, source.Id, results[0].SourceID)
	assert.Equal(t, model.UpstreamSyncStatusSucceeded, results[0].Status)
	assert.Equal(t, 1, results[0].Created)
	require.Len(t, createCalls, 1)
	assert.Equal(t, "10", createCalls[0].GroupID)
	var mappings []model.UpstreamSourceChannelMapping
	require.NoError(t, model.DB.Order("source_id").Find(&mappings).Error)
	require.Len(t, mappings, 1)
	assert.Equal(t, source.Id, mappings[0].SourceID)
	assert.True(t, mappings[0].SyncEnabled)
	var channel model.Channel
	require.NoError(t, model.DB.First(&channel).Error)
	assert.Equal(t, "due / 0.500x", channel.Name)
	assert.Equal(t, "regular", channel.Group)
	var reloadedSource model.UpstreamSource
	require.NoError(t, model.DB.First(&reloadedSource, source.Id).Error)
	assert.Equal(t, int64(3000), reloadedSource.LastDiscoveryTime)
	assert.Equal(t, int64(3000), reloadedSource.LastSyncTime)
}

func createAutoSyncTestSource(t *testing.T, name string, status string, syncConfig map[string]any, lastSyncTime int64, currentToken string, syncStartedAt int64) model.UpstreamSource {
	t.Helper()

	data, err := common.Marshal(syncConfig)
	require.NoError(t, err)
	source := model.UpstreamSource{
		Name:             name,
		Type:             model.UpstreamSourceTypeSub2API,
		Status:           status,
		BaseURL:          "https://admin.example.com",
		RelayBaseURL:     "https://relay.example.com",
		SyncConfig:       string(data),
		LastSyncTime:     lastSyncTime,
		CurrentSyncToken: currentToken,
		SyncStartedAt:    syncStartedAt,
	}
	require.NoError(t, model.DB.Create(&source).Error)
	return source
}
