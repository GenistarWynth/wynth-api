package service

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"

	"gorm.io/gorm"
)

const upstreamSourceSyncStaleAfterSeconds int64 = 3600

type UpstreamSourceService struct {
	AdapterFactory func(sourceType string) (UpstreamSourceAdapter, error)
	FetchModels    func(channel *model.Channel) ([]string, error)
	Now            func() int64
}

func (s *UpstreamSourceService) Discover(ctx context.Context, sourceID int) (*dto.UpstreamSourceDiscoveryResult, error) {
	if sourceID == 0 {
		return nil, errors.New("source ID is required")
	}

	var source model.UpstreamSource
	if err := model.DB.Where("id = ? AND status <> ?", sourceID, model.UpstreamSourceStatusDeleted).First(&source).Error; err != nil {
		return nil, err
	}

	now := s.now()
	if err := validateUpstreamSourceDiscoveryConfig(&source); err != nil {
		return s.recordDiscoveryFailure(source.Id, now, err), err
	}

	adapter, err := s.adapterFactory()(source.Type)
	if err != nil {
		return s.recordDiscoveryFailure(source.Id, now, err), err
	}
	if adapter == nil {
		err := errors.New("upstream source adapter is unavailable")
		return s.recordDiscoveryFailure(source.Id, now, err), err
	}

	groups, err := adapter.DiscoverGroups(ctx, &source)
	if err != nil {
		return s.recordDiscoveryFailure(source.Id, now, err), err
	}

	mappings, discoveredIDs, invalidCount := discoveredGroupsToMappings(source.Id, groups, now)
	var result dto.UpstreamSourceDiscoveryResult
	if err := model.DB.Transaction(func(tx *gorm.DB) error {
		if err := model.UpsertDiscoveredMappingsTx(tx, source.Id, mappings, now); err != nil {
			return err
		}
		staleCount, err := markMissingDiscoveredMappingsStaleTx(tx, source.Id, discoveredIDs, now)
		if err != nil {
			return err
		}
		if err := updateUpstreamSourceDiscoveryStatusTx(tx, source.Id, model.UpstreamDiscoveryStatusSucceeded, "", now); err != nil {
			return err
		}

		built, err := buildDiscoveryResultTx(tx, source.Id, len(groups), staleCount, invalidCount)
		if err != nil {
			return err
		}
		result = built
		return nil
	}); err != nil {
		return nil, err
	}
	return &result, nil
}

func (s *UpstreamSourceService) now() int64 {
	if s != nil && s.Now != nil {
		return s.Now()
	}
	return common.GetTimestamp()
}

func (s *UpstreamSourceService) adapterFactory() func(sourceType string) (UpstreamSourceAdapter, error) {
	if s != nil && s.AdapterFactory != nil {
		return s.AdapterFactory
	}
	return DefaultUpstreamSourceAdapterFactory
}

func (s *UpstreamSourceService) fetchModels(config upstreamSourceSyncConfig) func(channel *model.Channel) ([]string, error) {
	if s != nil && s.FetchModels != nil {
		return s.FetchModels
	}
	return func(channel *model.Channel) ([]string, error) {
		return FetchChannelUpstreamModelIDsWithOptions(channel, FetchChannelUpstreamModelIDsOptions{
			AllowPrivateIP: bool(config.AllowPrivateIP),
		})
	}
}

func (s *UpstreamSourceService) Sync(ctx context.Context, sourceID int) (*dto.UpstreamSourceSyncResult, error) {
	if sourceID == 0 {
		return nil, errors.New("source ID is required")
	}

	var source model.UpstreamSource
	if err := model.DB.Where("id = ?", sourceID).First(&source).Error; err != nil {
		return nil, err
	}

	now := s.now()
	result := &dto.UpstreamSourceSyncResult{
		SourceID: sourceID,
		Status:   model.UpstreamSyncStatusSucceeded,
		Results:  make([]dto.UpstreamSourceMappingSyncResult, 0),
	}
	if err := validateUpstreamSourceSyncConfig(&source); err != nil {
		return s.recordSyncFailure(source.Id, now, err), err
	}

	token := common.GetUUID()
	claimed, err := model.ClaimUpstreamSourceSync(sourceID, token, now, upstreamSourceSyncStaleAfterSeconds)
	if err != nil {
		return nil, err
	}
	if !claimed {
		result.Status = model.UpstreamSyncStatusRunning
		result.Error = "sync already running"
		return result, nil
	}

	finalStatus := model.UpstreamSyncStatusSucceeded
	finalError := ""
	defer func() {
		_ = model.ReleaseUpstreamSourceSync(sourceID, token, finalStatus, finalError, s.now())
	}()

	config, err := parseUpstreamSourceSyncConfig(source.SyncConfig)
	if err != nil {
		sanitized := SanitizeUpstreamSourceError(err)
		result.Status = model.UpstreamSyncStatusFailed
		result.Error = sanitized
		finalStatus = model.UpstreamSyncStatusFailed
		finalError = sanitized
		return result, err
	}

	var mappings []model.UpstreamSourceChannelMapping
	if err := model.DB.Where("source_id = ? AND sync_enabled = ?", sourceID, true).Order("id").Find(&mappings).Error; err != nil {
		result.Status = model.UpstreamSyncStatusFailed
		result.Error = SanitizeUpstreamSourceError(err)
		finalStatus = model.UpstreamSyncStatusFailed
		finalError = result.Error
		return result, err
	}
	if len(mappings) == 0 {
		err := errors.New("no upstream groups selected for sync; discover and select at least one group before syncing")
		sanitized := SanitizeUpstreamSourceError(err)
		result.Status = model.UpstreamSyncStatusFailed
		result.Error = sanitized
		finalStatus = model.UpstreamSyncStatusFailed
		finalError = sanitized
		return result, err
	}

	adapter, err := s.adapterFactory()(source.Type)
	if err != nil {
		sanitized := SanitizeUpstreamSourceError(err)
		result.Status = model.UpstreamSyncStatusFailed
		result.Error = sanitized
		finalStatus = model.UpstreamSyncStatusFailed
		finalError = sanitized
		return result, err
	}
	if adapter == nil {
		err := errors.New("upstream source adapter is unavailable")
		sanitized := SanitizeUpstreamSourceError(err)
		result.Status = model.UpstreamSyncStatusFailed
		result.Error = sanitized
		finalStatus = model.UpstreamSyncStatusFailed
		finalError = sanitized
		return result, err
	}

	changedChannels := make([]*model.Channel, 0, len(mappings))
	for i := range mappings {
		mappingResult, changedChannel := s.syncUpstreamSourceMapping(ctx, &source, &mappings[i], config, adapter, now)
		result.Results = append(result.Results, mappingResult)
		switch mappingResult.Status {
		case model.UpstreamMappingSyncStatusSynced:
			if mappingResult.Created {
				result.Created++
			} else if mappingResult.Updated {
				result.Updated++
			}
		case model.UpstreamMappingSyncStatusSkipped:
			result.Skipped++
		default:
			result.Failed++
			if result.Error == "" {
				result.Error = mappingResult.Error
			}
		}
		if changedChannel != nil {
			changedChannels = append(changedChannels, changedChannel)
		}
	}

	for _, channel := range changedChannels {
		model.CacheUpdateChannel(channel)
	}
	if len(changedChannels) > 0 {
		model.InitChannelCache()
	}

	if result.Failed > 0 {
		result.Status = model.UpstreamSyncStatusFailed
		finalStatus = model.UpstreamSyncStatusFailed
		finalError = result.Error
	} else {
		finalStatus = model.UpstreamSyncStatusSucceeded
	}
	return result, nil
}

// Keep this JSON shape in lockstep with controller.upstreamSourceControllerSyncConfig.
// AutoSyncModels stays pointer-based here so absent keys can preserve the
// historical default while explicit false remains distinguishable.
type upstreamSourceSyncConfig struct {
	LocalGroup             string              `json:"local_group"`
	ChannelType            int                 `json:"channel_type"`
	DefaultPriority        int64               `json:"default_priority"`
	DefaultWeight          uint                `json:"default_weight"`
	EnableMonitor          bool                `json:"enable_monitor"`
	MonitorIntervalMinutes int                 `json:"monitor_interval_minutes"`
	AutoSyncModels         *bool               `json:"auto_sync_models"`
	AllowPrivateIP         common.FlexibleBool `json:"allow_private_ip"`
}

func parseUpstreamSourceSyncConfig(raw string) (upstreamSourceSyncConfig, error) {
	config := upstreamSourceSyncConfig{
		LocalGroup:      "default",
		ChannelType:     constant.ChannelTypeOpenAI,
		AutoSyncModels:  common.GetPointer(true),
		DefaultPriority: 0,
		DefaultWeight:   0,
	}
	if strings.TrimSpace(raw) == "" {
		return config, nil
	}
	if err := common.Unmarshal([]byte(raw), &config); err != nil {
		return config, err
	}
	if strings.TrimSpace(config.LocalGroup) == "" {
		config.LocalGroup = "default"
	} else {
		config.LocalGroup = strings.TrimSpace(config.LocalGroup)
	}
	if config.ChannelType == 0 {
		config.ChannelType = constant.ChannelTypeOpenAI
	}
	if config.AutoSyncModels == nil {
		config.AutoSyncModels = common.GetPointer(true)
	}
	return config, nil
}

func validateUpstreamSourceSyncConfig(source *model.UpstreamSource) error {
	if source == nil {
		return errors.New("upstream source is required")
	}
	if source.Status != model.UpstreamSourceStatusEnabled {
		return fmt.Errorf("upstream source must be enabled for sync")
	}
	if strings.TrimSpace(source.Type) == "" {
		return errors.New("upstream source type is required")
	}
	if strings.TrimSpace(source.Type) != model.UpstreamSourceTypeSub2API {
		return fmt.Errorf("unsupported upstream source type %q", source.Type)
	}
	if strings.TrimSpace(source.BaseURL) == "" && strings.TrimSpace(source.RelayBaseURL) == "" {
		return errors.New("upstream source base URL is required")
	}
	baseURL := upstreamSourceGeneratedBaseURL(source)
	if err := validateAbsoluteHTTPURL("generated channel base URL", baseURL); err != nil {
		return err
	}
	return nil
}

func upstreamSourceGeneratedBaseURL(source *model.UpstreamSource) string {
	if source == nil {
		return ""
	}
	if trimmed := strings.TrimSpace(source.RelayBaseURL); trimmed != "" {
		return trimmed
	}
	return strings.TrimSpace(source.BaseURL)
}

func (s *UpstreamSourceService) syncUpstreamSourceMapping(ctx context.Context, source *model.UpstreamSource, mapping *model.UpstreamSourceChannelMapping, config upstreamSourceSyncConfig, adapter UpstreamSourceAdapter, now int64) (dto.UpstreamSourceMappingSyncResult, *model.Channel) {
	result := dto.UpstreamSourceMappingSyncResult{
		MappingID:       mapping.Id,
		UpstreamGroupID: mapping.UpstreamGroupID,
		LocalChannelID:  mapping.LocalChannelID,
	}

	if mapping.EffectiveRateMultiplier == nil {
		errText := SanitizeUpstreamSourceError(errors.New("effective rate multiplier is missing"))
		_ = updateUpstreamSourceMappingSync(mapping.Id, mapping.UpstreamKeyID, mapping.LocalChannelID, model.UpstreamMappingSyncStatusSkipped, errText, now)
		result.Status = model.UpstreamMappingSyncStatusSkipped
		result.Error = errText
		return result, nil
	}

	var existingChannel *model.Channel
	if mapping.LocalChannelID != 0 {
		channel, err := loadChannelByID(mapping.LocalChannelID)
		if err != nil {
			status := model.UpstreamMappingSyncStatusFailed
			if errors.Is(err, gorm.ErrRecordNotFound) {
				status = model.UpstreamMappingSyncStatusNeedsAttention
				err = fmt.Errorf("local channel %d is missing", mapping.LocalChannelID)
			}
			errText := SanitizeUpstreamSourceError(err)
			_ = updateUpstreamSourceMappingSync(mapping.Id, mapping.UpstreamKeyID, mapping.LocalChannelID, status, errText, now)
			result.Status = status
			result.Error = errText
			return result, nil
		}
		existingChannel = channel
	}

	key, err := ensureUpstreamSourceMappingKey(ctx, adapter, source, mapping)
	if err != nil {
		errText := SanitizeUpstreamSourceError(err)
		_ = updateUpstreamSourceMappingSync(mapping.Id, mapping.UpstreamKeyID, mapping.LocalChannelID, model.UpstreamMappingSyncStatusFailed, errText, now)
		result.Status = model.UpstreamMappingSyncStatusFailed
		result.Error = errText
		return result, nil
	}
	if key.ID == "" {
		err := errors.New("upstream key ID is missing")
		errText := SanitizeUpstreamSourceError(err)
		_ = updateUpstreamSourceMappingSync(mapping.Id, mapping.UpstreamKeyID, mapping.LocalChannelID, model.UpstreamMappingSyncStatusFailed, errText, now)
		result.Status = model.UpstreamMappingSyncStatusFailed
		result.Error = errText
		return result, nil
	}

	rawKey := strings.TrimSpace(key.Key)
	if rawKey == "" && mapping.UpstreamKeyID != "" && mapping.LocalChannelID == 0 {
		recoveredKey, err := recoverUpstreamSourceKeyForChannelCreate(ctx, adapter, source, mapping, key)
		if err != nil {
			errText := SanitizeUpstreamSourceError(err)
			_ = updateUpstreamSourceMappingSync(mapping.Id, key.ID, mapping.LocalChannelID, model.UpstreamMappingSyncStatusFailed, errText, now)
			result.Status = model.UpstreamMappingSyncStatusFailed
			result.Error = errText
			return result, nil
		}
		key = recoveredKey
		rawKey = strings.TrimSpace(key.Key)
	}
	if rawKey == "" && existingChannel != nil {
		rawKey = existingChannel.Key
	}
	if rawKey == "" {
		err := errors.New("upstream key value is missing")
		errText := SanitizeUpstreamSourceError(err)
		_ = updateUpstreamSourceMappingSync(mapping.Id, key.ID, mapping.LocalChannelID, model.UpstreamMappingSyncStatusFailed, errText, now)
		result.Status = model.UpstreamMappingSyncStatusFailed
		result.Error = errText
		return result, nil
	}

	channel := buildGeneratedChannel(source, mapping, config, rawKey)
	if existingChannel != nil {
		channel.Id = existingChannel.Id
		mergeGeneratedChannelOtherSettings(channel, existingChannel, config)
	}

	models, modelErr := fetchGeneratedChannelModels(s.fetchModels(config), channel, config)
	if modelErr != nil {
		result.Error = SanitizeUpstreamSourceError(modelErr)
		channel.Models = ""
		channel.Status = common.ChannelStatusManuallyDisabled
	} else {
		channel.Models = strings.Join(models, ",")
		channel.Status = common.ChannelStatusEnabled
	}

	created := mapping.LocalChannelID == 0
	savedChannel, err := saveGeneratedChannel(channel, created)
	if err != nil {
		errText := SanitizeUpstreamSourceError(err)
		_ = updateUpstreamSourceMappingSync(mapping.Id, key.ID, mapping.LocalChannelID, model.UpstreamMappingSyncStatusFailed, errText, now)
		result.Status = model.UpstreamMappingSyncStatusFailed
		result.Error = errText
		return result, nil
	}

	if created {
		err = savedChannel.AddAbilities(nil)
	} else {
		err = savedChannel.UpdateAbilities(nil)
	}
	if err != nil {
		errText := SanitizeUpstreamSourceError(err)
		_ = updateUpstreamSourceMappingSync(mapping.Id, key.ID, savedChannel.Id, model.UpstreamMappingSyncStatusFailed, errText, now)
		result.Status = model.UpstreamMappingSyncStatusFailed
		result.Error = errText
		result.LocalChannelID = savedChannel.Id
		return result, savedChannel
	}

	status := model.UpstreamMappingSyncStatusSynced
	lastError := ""
	if modelErr != nil {
		status = model.UpstreamMappingSyncStatusFailed
		lastError = result.Error
	}
	if err := updateUpstreamSourceMappingSync(mapping.Id, key.ID, savedChannel.Id, status, lastError, now); err != nil {
		errText := SanitizeUpstreamSourceError(err)
		result.Status = model.UpstreamMappingSyncStatusFailed
		result.Error = errText
		result.LocalChannelID = savedChannel.Id
		return result, savedChannel
	}

	result.LocalChannelID = savedChannel.Id
	result.Status = status
	result.Created = created
	result.Updated = !created
	if status == model.UpstreamMappingSyncStatusSynced {
		result.Error = ""
	}
	return result, savedChannel
}

func loadChannelByID(channelID int) (*model.Channel, error) {
	var channel model.Channel
	if err := model.DB.First(&channel, channelID).Error; err != nil {
		return nil, err
	}
	return &channel, nil
}

func ensureUpstreamSourceMappingKey(ctx context.Context, adapter UpstreamSourceAdapter, source *model.UpstreamSource, mapping *model.UpstreamSourceChannelMapping) (UpstreamKey, error) {
	name := upstreamSourceKeyName(source, mapping)
	if strings.TrimSpace(mapping.UpstreamKeyID) == "" {
		return adapter.CreateKey(ctx, source, mapping.UpstreamGroupID, name)
	}
	key, err := adapter.UpdateKey(ctx, source, mapping.UpstreamKeyID, mapping.UpstreamGroupID, name)
	if err == nil {
		return key, nil
	}
	if !isUpstreamKeyNotFoundError(err) {
		return UpstreamKey{}, err
	}
	return adapter.CreateKey(ctx, source, mapping.UpstreamGroupID, name)
}

func recoverUpstreamSourceKeyForChannelCreate(ctx context.Context, adapter UpstreamSourceAdapter, source *model.UpstreamSource, mapping *model.UpstreamSourceChannelMapping, updatedKey UpstreamKey) (UpstreamKey, error) {
	keyID := strings.TrimSpace(updatedKey.ID)
	if keyID == "" {
		keyID = strings.TrimSpace(mapping.UpstreamKeyID)
	}
	if keyID != "" {
		keys, err := adapter.ListKeys(ctx, source, mapping.UpstreamGroupID)
		if err == nil {
			for _, listedKey := range keys {
				if strings.TrimSpace(listedKey.ID) == keyID && strings.TrimSpace(listedKey.Key) != "" {
					if listedKey.GroupID == "" {
						listedKey.GroupID = mapping.UpstreamGroupID
					}
					if listedKey.Name == "" {
						listedKey.Name = upstreamSourceKeyName(source, mapping)
					}
					return listedKey, nil
				}
			}
		}
	}
	return adapter.CreateKey(ctx, source, mapping.UpstreamGroupID, upstreamSourceKeyName(source, mapping))
}

func isUpstreamKeyNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "not found") ||
		strings.Contains(text, "404") ||
		strings.Contains(text, "不存在")
}

func upstreamSourceKeyName(source *model.UpstreamSource, mapping *model.UpstreamSourceChannelMapping) string {
	return fmt.Sprintf("Wynth API / %s / %s", strings.TrimSpace(source.Name), upstreamSourceGroupDisplayName(mapping))
}

func upstreamSourceGeneratedChannelName(source *model.UpstreamSource, mapping *model.UpstreamSourceChannelMapping) string {
	return fmt.Sprintf("%s / %s", strings.TrimSpace(source.Name), upstreamSourceGroupDisplayName(mapping))
}

func upstreamSourceGroupDisplayName(mapping *model.UpstreamSourceChannelMapping) string {
	if trimmed := strings.TrimSpace(mapping.UpstreamGroupName); trimmed != "" {
		return trimmed
	}
	return strings.TrimSpace(mapping.UpstreamGroupID)
}

func buildGeneratedChannel(source *model.UpstreamSource, mapping *model.UpstreamSourceChannelMapping, config upstreamSourceSyncConfig, rawKey string) *model.Channel {
	channel := &model.Channel{
		Name:     upstreamSourceGeneratedChannelName(source, mapping),
		Type:     config.ChannelType,
		Key:      rawKey,
		BaseURL:  common.GetPointer(upstreamSourceGeneratedBaseURL(source)),
		Group:    config.LocalGroup,
		Priority: common.GetPointer(config.DefaultPriority),
		Weight:   common.GetPointer(config.DefaultWeight),
		Tag:      common.GetPointer(strings.TrimSpace(source.Name)),
	}
	channel.SetOtherSettings(dto.ChannelOtherSettings{
		ChannelMonitorEnabled:         config.EnableMonitor,
		ChannelMonitorIntervalMinutes: config.MonitorIntervalMinutes,
	})
	return channel
}

func mergeGeneratedChannelOtherSettings(channel *model.Channel, existingChannel *model.Channel, config upstreamSourceSyncConfig) {
	if channel == nil || existingChannel == nil {
		return
	}
	settings := existingChannel.GetOtherSettings()
	settings.ChannelMonitorEnabled = config.EnableMonitor
	settings.ChannelMonitorIntervalMinutes = config.MonitorIntervalMinutes
	channel.SetOtherSettings(settings)
}

func fetchGeneratedChannelModels(fetchModels func(channel *model.Channel) ([]string, error), channel *model.Channel, config upstreamSourceSyncConfig) ([]string, error) {
	if config.AutoSyncModels == nil || !*config.AutoSyncModels {
		return nil, errors.New("models are required before enabling generated channel")
	}
	models, err := fetchModels(channel)
	if err != nil {
		return nil, err
	}
	models = normalizeFetchedModelNames(models)
	if len(models) == 0 {
		return nil, errors.New("models are required before enabling generated channel")
	}
	return models, nil
}

func saveGeneratedChannel(channel *model.Channel, create bool) (*model.Channel, error) {
	if create {
		if err := model.DB.Create(channel).Error; err != nil {
			return nil, err
		}
		return channel, nil
	}
	updates := generatedChannelUpdateMap(channel)
	if err := model.DB.Model(&model.Channel{}).Where("id = ?", channel.Id).Updates(updates).Error; err != nil {
		return nil, err
	}
	var reloaded model.Channel
	if err := model.DB.First(&reloaded, channel.Id).Error; err != nil {
		return nil, err
	}
	return &reloaded, nil
}

func generatedChannelUpdateMap(channel *model.Channel) map[string]any {
	return map[string]any{
		"name":     channel.Name,
		"type":     channel.Type,
		"base_url": channel.BaseURL,
		"key":      channel.Key,
		"group":    channel.Group,
		"priority": channel.Priority,
		"weight":   channel.Weight,
		"tag":      channel.Tag,
		"models":   channel.Models,
		"settings": channel.OtherSettings,
		"status":   channel.Status,
	}
}

func updateUpstreamSourceMappingSync(mappingID int, upstreamKeyID string, localChannelID int, status string, errText string, now int64) error {
	return model.DB.Model(&model.UpstreamSourceChannelMapping{}).
		Where("id = ?", mappingID).
		Updates(map[string]any{
			"upstream_key_id":  upstreamKeyID,
			"local_channel_id": localChannelID,
			"sync_status":      status,
			"last_error":       errText,
			"last_synced_at":   now,
			"updated_time":     now,
		}).Error
}

func DefaultUpstreamSourceAdapterFactory(sourceType string) (UpstreamSourceAdapter, error) {
	switch strings.TrimSpace(sourceType) {
	case model.UpstreamSourceTypeSub2API:
		return Sub2APIAdapter{}, nil
	default:
		return nil, fmt.Errorf("unsupported upstream source type %q", sourceType)
	}
}

func validateUpstreamSourceDiscoveryConfig(source *model.UpstreamSource) error {
	if source == nil {
		return errors.New("upstream source is required")
	}
	if source.Status != model.UpstreamSourceStatusEnabled {
		return fmt.Errorf("upstream source must be enabled for discovery")
	}
	if strings.TrimSpace(source.Type) == "" {
		return errors.New("upstream source type is required")
	}
	if strings.TrimSpace(source.Type) != model.UpstreamSourceTypeSub2API {
		return fmt.Errorf("unsupported upstream source type %q", source.Type)
	}
	if err := validateAbsoluteHTTPURL("base URL", source.BaseURL); err != nil {
		return err
	}
	if err := validateAbsoluteHTTPURL("relay base URL", source.RelayBaseURL); err != nil {
		return err
	}
	return nil
}

func validateAbsoluteHTTPURL(name string, value string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fmt.Errorf("upstream source %s is required", name)
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return fmt.Errorf("invalid upstream source %s: %w", name, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("upstream source %s must use http or https", name)
	}
	if parsed.Host == "" {
		return fmt.Errorf("upstream source %s must include a host", name)
	}
	return nil
}

func discoveredGroupsToMappings(sourceID int, groups []UpstreamGroup, now int64) ([]model.UpstreamSourceChannelMapping, []string, int) {
	mappingByID := make(map[string]model.UpstreamSourceChannelMapping, len(groups))
	discoveredIDs := make([]string, 0, len(groups))
	invalidCount := 0

	for _, group := range groups {
		groupID := strings.TrimSpace(group.ID)
		if groupID == "" {
			invalidCount++
			continue
		}
		if _, exists := mappingByID[groupID]; !exists {
			discoveredIDs = append(discoveredIDs, groupID)
		}
		discoveryStatus := model.UpstreamMappingDiscoveryStatusActive
		if group.EffectiveRateMultiplier == nil {
			discoveryStatus = model.UpstreamMappingDiscoveryStatusInvalid
			invalidCount++
		}
		mappingByID[groupID] = model.UpstreamSourceChannelMapping{
			SourceID:                sourceID,
			SyncEnabled:             discoveryStatus == model.UpstreamMappingDiscoveryStatusActive,
			UpstreamGroupID:         groupID,
			UpstreamGroupName:       strings.TrimSpace(group.Name),
			UpstreamPlatform:        strings.TrimSpace(group.Platform),
			DiscoveryStatus:         discoveryStatus,
			UpstreamStatus:          strings.TrimSpace(group.Status),
			UpstreamRateMultiplier:  group.RateMultiplier,
			EffectiveRateMultiplier: group.EffectiveRateMultiplier,
			LastDiscoveredAt:        now,
		}
	}

	mappings := make([]model.UpstreamSourceChannelMapping, 0, len(mappingByID))
	for _, groupID := range discoveredIDs {
		mappings = append(mappings, mappingByID[groupID])
	}

	return mappings, discoveredIDs, invalidCount
}

func markMissingDiscoveredMappingsStale(sourceID int, discoveredIDs []string, now int64) (int, error) {
	return markMissingDiscoveredMappingsStaleTx(model.DB, sourceID, discoveredIDs, now)
}

func markMissingDiscoveredMappingsStaleTx(tx *gorm.DB, sourceID int, discoveredIDs []string, now int64) (int, error) {
	query := tx.Model(&model.UpstreamSourceChannelMapping{}).
		Where("source_id = ?", sourceID)
	if len(discoveredIDs) > 0 {
		query = query.Where("upstream_group_id NOT IN ?", discoveredIDs)
	}
	result := query.Updates(map[string]interface{}{
		"discovery_status":   model.UpstreamMappingDiscoveryStatusStale,
		"last_discovered_at": now,
		"updated_time":       now,
	})
	if result.Error != nil {
		return 0, result.Error
	}
	return int(result.RowsAffected), nil
}

func updateUpstreamSourceDiscoveryStatus(sourceID int, status string, errText string, now int64) error {
	return updateUpstreamSourceDiscoveryStatusTx(model.DB, sourceID, status, errText, now)
}

func updateUpstreamSourceDiscoveryStatusTx(tx *gorm.DB, sourceID int, status string, errText string, now int64) error {
	return tx.Model(&model.UpstreamSource{}).
		Where("id = ?", sourceID).
		Updates(map[string]interface{}{
			"last_discovery_time":   now,
			"last_discovery_status": status,
			"last_discovery_error":  errText,
			"updated_time":          now,
		}).Error
}

func (s *UpstreamSourceService) recordDiscoveryFailure(sourceID int, now int64, err error) *dto.UpstreamSourceDiscoveryResult {
	sanitized := SanitizeUpstreamSourceError(err)
	_ = updateUpstreamSourceDiscoveryStatus(sourceID, model.UpstreamDiscoveryStatusFailed, sanitized, now)
	return &dto.UpstreamSourceDiscoveryResult{
		SourceID: sourceID,
		Error:    sanitized,
	}
}

func (s *UpstreamSourceService) recordSyncFailure(sourceID int, now int64, err error) *dto.UpstreamSourceSyncResult {
	sanitized := SanitizeUpstreamSourceError(err)
	_ = updateUpstreamSourceSyncStatus(sourceID, model.UpstreamSyncStatusFailed, sanitized, now)
	return &dto.UpstreamSourceSyncResult{
		SourceID: sourceID,
		Status:   model.UpstreamSyncStatusFailed,
		Error:    sanitized,
	}
}

func updateUpstreamSourceSyncStatus(sourceID int, status string, errText string, now int64) error {
	return model.DB.Model(&model.UpstreamSource{}).
		Where("id = ?", sourceID).
		Updates(map[string]interface{}{
			"last_sync_time":   now,
			"last_sync_status": status,
			"last_sync_error":  errText,
			"updated_time":     now,
		}).Error
}

func buildDiscoveryResult(sourceID int, discovered int, staleCount int, invalidCount int) (dto.UpstreamSourceDiscoveryResult, error) {
	return buildDiscoveryResultTx(model.DB, sourceID, discovered, staleCount, invalidCount)
}

func buildDiscoveryResultTx(tx *gorm.DB, sourceID int, discovered int, staleCount int, invalidCount int) (dto.UpstreamSourceDiscoveryResult, error) {
	var mappings []model.UpstreamSourceChannelMapping
	if err := tx.Where("source_id = ?", sourceID).Order("upstream_group_id").Find(&mappings).Error; err != nil {
		return dto.UpstreamSourceDiscoveryResult{}, err
	}

	result := dto.UpstreamSourceDiscoveryResult{
		SourceID:   sourceID,
		Discovered: discovered,
		Stale:      staleCount,
		Invalid:    invalidCount,
		Mappings:   make([]dto.UpstreamSourceMappingResponse, 0, len(mappings)),
	}
	for _, mapping := range mappings {
		if mapping.DiscoveryStatus == model.UpstreamMappingDiscoveryStatusActive {
			result.Active++
		}
		result.Mappings = append(result.Mappings, upstreamSourceMappingResponse(mapping))
	}
	sort.SliceStable(result.Mappings, func(i, j int) bool {
		return result.Mappings[i].UpstreamGroupID < result.Mappings[j].UpstreamGroupID
	})
	return result, nil
}

func upstreamSourceMappingResponse(mapping model.UpstreamSourceChannelMapping) dto.UpstreamSourceMappingResponse {
	return dto.UpstreamSourceMappingResponse{
		Id:                      mapping.Id,
		SourceID:                mapping.SourceID,
		SyncEnabled:             mapping.SyncEnabled,
		UpstreamGroupID:         mapping.UpstreamGroupID,
		UpstreamGroupName:       mapping.UpstreamGroupName,
		UpstreamPlatform:        mapping.UpstreamPlatform,
		DiscoveryStatus:         mapping.DiscoveryStatus,
		UpstreamStatus:          mapping.UpstreamStatus,
		UpstreamRateMultiplier:  mapping.UpstreamRateMultiplier,
		EffectiveRateMultiplier: mapping.EffectiveRateMultiplier,
		HasUpstreamKey:          mapping.UpstreamKeyID != "",
		LocalChannelID:          mapping.LocalChannelID,
		SyncStatus:              mapping.SyncStatus,
		LastError:               sanitizeUpstreamSourceStoredError(mapping.LastError),
		LastDiscoveredAt:        mapping.LastDiscoveredAt,
		LastSyncedAt:            mapping.LastSyncedAt,
	}
}

func sanitizeUpstreamSourceStoredError(text string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	return SanitizeUpstreamSourceError(errors.New(text))
}
