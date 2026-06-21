package service

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"

	"gorm.io/gorm"
)

const upstreamSourceSyncStaleAfterSeconds int64 = 3600

type upstreamSourceSyncMode string

const (
	upstreamSourceSyncModeManual upstreamSourceSyncMode = "manual"
	upstreamSourceSyncModeAuto   upstreamSourceSyncMode = "auto"
)

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

	config, err := parseUpstreamSourceSyncConfig(source.SyncConfig)
	if err != nil {
		return s.recordDiscoveryFailure(source.Id, now, err), err
	}

	mappings, discoveredIDs, invalidCount := discoveredGroupsToMappings(source.Id, groups, now, config)
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

		built, err := buildDiscoveryResultTx(tx, source.Id, source.SyncConfig, len(groups), staleCount, invalidCount)
		if err != nil {
			return err
		}
		result = built
		return nil
	}); err != nil {
		return nil, err
	}
	if result.Stale > 0 {
		model.InitChannelCache()
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
	return s.sync(ctx, sourceID, upstreamSourceSyncModeManual)
}

func (s *UpstreamSourceService) SyncDueAuto(ctx context.Context, sourceID int) (*dto.UpstreamSourceSyncResult, error) {
	return s.sync(ctx, sourceID, upstreamSourceSyncModeAuto)
}

func (s *UpstreamSourceService) sync(ctx context.Context, sourceID int, mode upstreamSourceSyncMode) (*dto.UpstreamSourceSyncResult, error) {
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
	if err := model.DB.Where("source_id = ?", sourceID).Order("id").Find(&mappings).Error; err != nil {
		result.Status = model.UpstreamSyncStatusFailed
		result.Error = SanitizeUpstreamSourceError(err)
		finalStatus = model.UpstreamSyncStatusFailed
		finalError = result.Error
		return result, err
	}

	resolutions := make([]upstreamSourceRuleResolution, len(mappings))
	eligibleCount := 0
	syncEnabledCount := 0
	for i := range mappings {
		if mappings[i].SyncEnabled {
			syncEnabledCount++
		}
		resolutions[i] = resolveUpstreamSourceRuleForManualSync(config, &mappings[i])
		if mode == upstreamSourceSyncModeAuto && resolutions[i].SyncEligible && !upstreamSourceMappingAutoSyncDue(config, &mappings[i], now) {
			resolutions[i].SyncEligible = false
			resolutions[i].Reason = upstreamSourceMatchReasonAutoSyncNotDue
		}
		if resolutions[i].SyncEligible {
			eligibleCount++
		}
	}

	if len(mappings) == 0 && len(config.LocalGroupRules) == 0 {
		err := errors.New("no upstream groups selected for sync; discover and select at least one group before syncing")
		sanitized := SanitizeUpstreamSourceError(err)
		result.Status = model.UpstreamSyncStatusFailed
		result.Error = sanitized
		finalStatus = model.UpstreamSyncStatusFailed
		finalError = sanitized
		return result, err
	}
	if eligibleCount == 0 {
		for i := range mappings {
			mappingResult := skippedUpstreamSourceMappingResult(&mappings[i], resolutions[i], now, shouldPersistSkippedUpstreamSourceMapping(mode, resolutions[i]))
			result.Results = append(result.Results, mappingResult)
			result.Skipped++
		}
		if mode == upstreamSourceSyncModeAuto {
			return result, nil
		}
		if len(config.LocalGroupRules) > 0 {
			err := errors.New("no upstream groups matched sync rules")
			sanitized := SanitizeUpstreamSourceError(err)
			result.Status = model.UpstreamSyncStatusFailed
			result.Error = sanitized
			finalStatus = model.UpstreamSyncStatusFailed
			finalError = sanitized
			return result, err
		}
		if syncEnabledCount == 0 {
			err := errors.New("no upstream groups selected for sync; discover and select at least one group before syncing")
			sanitized := SanitizeUpstreamSourceError(err)
			result.Status = model.UpstreamSyncStatusFailed
			result.Error = sanitized
			finalStatus = model.UpstreamSyncStatusFailed
			finalError = sanitized
			return result, err
		}
		return result, nil
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
		var mappingResult dto.UpstreamSourceMappingSyncResult
		var changedChannel *model.Channel
		if !resolutions[i].SyncEligible {
			mappingResult = skippedUpstreamSourceMappingResult(&mappings[i], resolutions[i], now, shouldPersistSkippedUpstreamSourceMapping(mode, resolutions[i]))
		} else {
			mappingResult, changedChannel = s.syncUpstreamSourceMapping(ctx, &source, &mappings[i], config, resolutions[i], adapter, now)
		}
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

func resolveUpstreamSourceRuleForManualSync(config upstreamSourceSyncConfig, mapping *model.UpstreamSourceChannelMapping) upstreamSourceRuleResolution {
	resolution := resolveUpstreamSourceRule(config, mapping)
	if mapping == nil || len(config.LocalGroupRules) == 0 || mapping.SyncEnabled {
		return resolution
	}
	eligibilityMapping := *mapping
	eligibilityMapping.SyncEnabled = true
	ruleResolution := resolveUpstreamSourceRule(config, &eligibilityMapping)
	if !ruleResolution.SyncEligible {
		return ruleResolution
	}
	return resolution
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
	if !isSupportedUpstreamSourceType(source.Type) {
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

func shouldPersistSkippedUpstreamSourceMapping(mode upstreamSourceSyncMode, resolution upstreamSourceRuleResolution) bool {
	return mode != upstreamSourceSyncModeAuto || resolution.Reason != upstreamSourceMatchReasonAutoSyncNotDue
}

func skippedUpstreamSourceMappingResult(mapping *model.UpstreamSourceChannelMapping, resolution upstreamSourceRuleResolution, now int64, persist bool) dto.UpstreamSourceMappingSyncResult {
	errText := SanitizeUpstreamSourceError(errors.New(resolution.Reason))
	result := dto.UpstreamSourceMappingSyncResult{
		Status: model.UpstreamMappingSyncStatusSkipped,
		Error:  errText,
	}
	if mapping == nil {
		return result
	}
	result.MappingID = mapping.Id
	result.UpstreamGroupID = mapping.UpstreamGroupID
	result.LocalChannelID = mapping.LocalChannelID
	if persist {
		_ = updateUpstreamSourceMappingSync(mapping.Id, mapping.UpstreamKeyID, mapping.LocalChannelID, model.UpstreamMappingSyncStatusSkipped, errText, now)
	}
	return result
}

func (s *UpstreamSourceService) syncUpstreamSourceMapping(ctx context.Context, source *model.UpstreamSource, mapping *model.UpstreamSourceChannelMapping, config upstreamSourceSyncConfig, resolution upstreamSourceRuleResolution, adapter UpstreamSourceAdapter, now int64) (dto.UpstreamSourceMappingSyncResult, *model.Channel) {
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
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				errText := SanitizeUpstreamSourceError(err)
				_ = updateUpstreamSourceMappingSync(mapping.Id, mapping.UpstreamKeyID, mapping.LocalChannelID, model.UpstreamMappingSyncStatusFailed, errText, now)
				result.Status = model.UpstreamMappingSyncStatusFailed
				result.Error = errText
				return result, nil
			}
			mapping.LocalChannelID = 0
			result.LocalChannelID = 0
		} else if isGeneratedChannelOwnedByMapping(channel, source, mapping) {
			existingChannel = channel
		} else {
			mapping.LocalChannelID = 0
			result.LocalChannelID = 0
		}
	}

	upstreamKeyID := strings.TrimSpace(mapping.UpstreamKeyID)
	rawKey := ""
	if existingChannel != nil {
		rawKey = strings.TrimSpace(existingChannel.Key)
	}
	var ensuredKey UpstreamKey
	ensuredKeyCreated := false
	ensuredKeyLoaded := false
	if upstreamKeyID != "" {
		key, created, err := ensureUpstreamSourceMappingKey(ctx, adapter, source, mapping)
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

		ensuredKey = key
		ensuredKeyCreated = created
		ensuredKeyLoaded = true
		upstreamKeyID = strings.TrimSpace(key.ID)
		rawKey = strings.TrimSpace(key.Key)
	}
	if rawKey == "" {
		if !ensuredKeyLoaded {
			key, created, err := ensureUpstreamSourceMappingKey(ctx, adapter, source, mapping)
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

			ensuredKey = key
			ensuredKeyCreated = created
			ensuredKeyLoaded = true
			upstreamKeyID = strings.TrimSpace(key.ID)
			rawKey = strings.TrimSpace(key.Key)
		}
		if rawKey == "" && mapping.UpstreamKeyID != "" && !ensuredKeyCreated {
			recoveredKey, created, err := recoverOrReplaceUpstreamSourceKey(ctx, adapter, source, mapping, ensuredKey)
			if err != nil {
				errText := SanitizeUpstreamSourceError(err)
				_ = updateUpstreamSourceMappingSync(mapping.Id, upstreamKeyID, mapping.LocalChannelID, model.UpstreamMappingSyncStatusFailed, errText, now)
				result.Status = model.UpstreamMappingSyncStatusFailed
				result.Error = errText
				return result, nil
			}
			upstreamKeyID = strings.TrimSpace(recoveredKey.ID)
			rawKey = strings.TrimSpace(recoveredKey.Key)
			ensuredKeyCreated = created
		}
	}
	if rawKey == "" {
		err := errors.New("upstream key value is missing")
		errText := SanitizeUpstreamSourceError(err)
		_ = updateUpstreamSourceMappingSync(mapping.Id, upstreamKeyID, mapping.LocalChannelID, model.UpstreamMappingSyncStatusFailed, errText, now)
		result.Status = model.UpstreamMappingSyncStatusFailed
		result.Error = errText
		return result, nil
	}

	channel := buildGeneratedChannel(source, mapping, config, resolution, rawKey)
	if existingChannel != nil {
		channel.Id = existingChannel.Id
		mergeGeneratedChannelOtherSettings(channel, existingChannel, resolution, source, mapping)
	}

	models, modelErr := fetchGeneratedChannelModels(s.fetchModels(config), channel, resolution)
	if modelErr != nil {
		result.Error = SanitizeUpstreamSourceError(modelErr)
		channel.Models = ""
		channel.Status = common.ChannelStatusManuallyDisabled
	} else {
		channel.Models = strings.Join(models, ",")
		channel.Status = common.ChannelStatusEnabled
	}

	created := mapping.LocalChannelID == 0
	savedChannel, err := saveGeneratedChannel(channel, created, now)
	if err != nil {
		errText := SanitizeUpstreamSourceError(err)
		_ = updateUpstreamSourceMappingSync(mapping.Id, upstreamKeyID, mapping.LocalChannelID, model.UpstreamMappingSyncStatusFailed, errText, now)
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
		_ = updateUpstreamSourceMappingSync(mapping.Id, upstreamKeyID, savedChannel.Id, model.UpstreamMappingSyncStatusFailed, errText, now)
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
	if err := updateUpstreamSourceMappingSync(mapping.Id, upstreamKeyID, savedChannel.Id, status, lastError, now); err != nil {
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

func isGeneratedChannelOwnedByMapping(channel *model.Channel, source *model.UpstreamSource, mapping *model.UpstreamSourceChannelMapping) bool {
	if channel == nil || source == nil || mapping == nil {
		return false
	}
	settings := channel.GetOtherSettings()
	if settings.GeneratedByUpstreamSourceID != 0 || settings.GeneratedByUpstreamMappingID != 0 {
		return settings.GeneratedByUpstreamSourceID == source.Id &&
			settings.GeneratedByUpstreamMappingID == mapping.Id
	}
	expectedTag := strings.TrimSpace(source.Name)
	actualTag := ""
	if channel.Tag != nil {
		actualTag = strings.TrimSpace(*channel.Tag)
	}
	channelName := strings.TrimSpace(channel.Name)
	return channelName == legacyUpstreamSourceGeneratedChannelName(source, mapping) && actualTag == expectedTag
}

func ensureUpstreamSourceMappingKey(ctx context.Context, adapter UpstreamSourceAdapter, source *model.UpstreamSource, mapping *model.UpstreamSourceChannelMapping) (UpstreamKey, bool, error) {
	name := upstreamSourceKeyName(source, mapping)
	if strings.TrimSpace(mapping.UpstreamKeyID) == "" {
		if key, found := findReusableUpstreamSourceKey(ctx, adapter, source, mapping, name); found {
			return key, false, nil
		}
		key, err := adapter.CreateKey(ctx, source, mapping.UpstreamGroupID, name)
		return key, true, err
	}
	key, err := adapter.UpdateKey(ctx, source, mapping.UpstreamKeyID, mapping.UpstreamGroupID, name)
	if err == nil {
		return key, false, nil
	}
	if !isUpstreamKeyNotFoundError(err) {
		return UpstreamKey{}, false, err
	}
	key, err = adapter.CreateKey(ctx, source, mapping.UpstreamGroupID, name)
	return key, true, err
}

func findReusableUpstreamSourceKey(ctx context.Context, adapter UpstreamSourceAdapter, source *model.UpstreamSource, mapping *model.UpstreamSourceChannelMapping, name string) (UpstreamKey, bool) {
	keys, err := adapter.ListKeys(ctx, source, mapping.UpstreamGroupID)
	if err != nil {
		return UpstreamKey{}, false
	}
	for _, key := range keys {
		if strings.TrimSpace(key.ID) == "" {
			continue
		}
		if strings.TrimSpace(key.Name) != name {
			continue
		}
		if key.GroupID != "" && strings.TrimSpace(key.GroupID) != strings.TrimSpace(mapping.UpstreamGroupID) {
			continue
		}
		if key.GroupID == "" {
			key.GroupID = mapping.UpstreamGroupID
		}
		return key, true
	}
	return UpstreamKey{}, false
}

func recoverUpstreamSourceKeyForChannelCreate(ctx context.Context, adapter UpstreamSourceAdapter, source *model.UpstreamSource, mapping *model.UpstreamSourceChannelMapping, updatedKey UpstreamKey) (UpstreamKey, error) {
	key, found, err := findUpstreamSourceKeyWithValue(ctx, adapter, source, mapping, updatedKey)
	if err != nil {
		return UpstreamKey{}, err
	}
	if found {
		return key, nil
	}
	keyID := strings.TrimSpace(updatedKey.ID)
	if keyID == "" {
		keyID = strings.TrimSpace(mapping.UpstreamKeyID)
	}
	if keyID == "" {
		return UpstreamKey{}, errors.New("existing upstream key value is unavailable")
	}
	return UpstreamKey{}, fmt.Errorf("existing upstream key value is unavailable for key %s", keyID)
}

func recoverOrReplaceUpstreamSourceKey(ctx context.Context, adapter UpstreamSourceAdapter, source *model.UpstreamSource, mapping *model.UpstreamSourceChannelMapping, updatedKey UpstreamKey) (UpstreamKey, bool, error) {
	key, found, err := findUpstreamSourceKeyWithValue(ctx, adapter, source, mapping, updatedKey)
	if err != nil {
		return UpstreamKey{}, false, err
	}
	if found {
		return key, false, nil
	}
	name := upstreamSourceKeyName(source, mapping)
	key, err = adapter.CreateKey(ctx, source, mapping.UpstreamGroupID, name)
	return key, true, err
}

func findUpstreamSourceKeyWithValue(ctx context.Context, adapter UpstreamSourceAdapter, source *model.UpstreamSource, mapping *model.UpstreamSourceChannelMapping, updatedKey UpstreamKey) (UpstreamKey, bool, error) {
	if strings.TrimSpace(updatedKey.Key) != "" {
		return updatedKey, true, nil
	}
	keyID := strings.TrimSpace(updatedKey.ID)
	if keyID == "" {
		keyID = strings.TrimSpace(mapping.UpstreamKeyID)
	}
	if keyID != "" {
		keys, err := adapter.ListKeys(ctx, source, mapping.UpstreamGroupID)
		if err != nil {
			return UpstreamKey{}, false, err
		}
		for _, listedKey := range keys {
			if strings.TrimSpace(listedKey.ID) == keyID && strings.TrimSpace(listedKey.Key) != "" {
				if listedKey.GroupID == "" {
					listedKey.GroupID = mapping.UpstreamGroupID
				}
				if listedKey.Name == "" {
					listedKey.Name = upstreamSourceKeyName(source, mapping)
				}
				return listedKey, true, nil
			}
		}
	}
	return UpstreamKey{}, false, nil
}

func isUpstreamKeyNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "not found") ||
		strings.Contains(text, "404") ||
		strings.Contains(text, "401") ||
		strings.Contains(text, "unauthorized") ||
		strings.Contains(text, "不存在")
}

func upstreamSourceKeyName(source *model.UpstreamSource, mapping *model.UpstreamSourceChannelMapping) string {
	return fmt.Sprintf("Wynth API / %s / %s", strings.TrimSpace(source.Name), upstreamSourceGroupDisplayName(mapping))
}

func upstreamSourceGeneratedChannelName(source *model.UpstreamSource, mapping *model.UpstreamSourceChannelMapping) string {
	sourceName := strings.TrimSpace(source.Name)
	if mapping != nil && mapping.EffectiveRateMultiplier != nil {
		return fmt.Sprintf("%s / %s", sourceName, formatUpstreamSourceRateMultiplier(*mapping.EffectiveRateMultiplier))
	}
	return fmt.Sprintf("%s / %s", sourceName, upstreamSourceGroupDisplayName(mapping))
}

func legacyUpstreamSourceGeneratedChannelName(source *model.UpstreamSource, mapping *model.UpstreamSourceChannelMapping) string {
	return fmt.Sprintf("%s / %s", strings.TrimSpace(source.Name), upstreamSourceGroupDisplayName(mapping))
}

func upstreamSourceGroupDisplayName(mapping *model.UpstreamSourceChannelMapping) string {
	if trimmed := strings.TrimSpace(mapping.UpstreamGroupName); trimmed != "" {
		return trimmed
	}
	return strings.TrimSpace(mapping.UpstreamGroupID)
}

func formatUpstreamSourceRateMultiplier(value float64) string {
	return fmt.Sprintf("%.3fx", value)
}

func upstreamSourceKeywordsMatch(text string, keywords []string) bool {
	if text == "" || len(keywords) == 0 {
		return false
	}
	for _, keyword := range keywords {
		if keyword != "" && strings.Contains(text, keyword) {
			return true
		}
	}
	return false
}

func upstreamSourceGeneratedChannelRemark(mapping *model.UpstreamSourceChannelMapping) *string {
	if mapping == nil {
		return nil
	}
	parts := []string{
		"Upstream group: " + upstreamSourceGroupDisplayName(mapping),
	}
	if groupID := strings.TrimSpace(mapping.UpstreamGroupID); groupID != "" {
		parts = append(parts, "ID: "+groupID)
	}
	if platform := strings.TrimSpace(mapping.UpstreamPlatform); platform != "" {
		parts = append(parts, "Platform: "+platform)
	}
	if mapping.EffectiveRateMultiplier != nil {
		parts = append(parts, "Rate: "+formatUpstreamSourceRateMultiplier(*mapping.EffectiveRateMultiplier))
	}
	remark := strings.Join(parts, "; ")
	remark = truncateUpstreamSourceString(remark, 255)
	return common.GetPointer(remark)
}

func truncateUpstreamSourceString(value string, maxBytes int) string {
	if maxBytes <= 0 || len(value) <= maxBytes {
		return value
	}
	truncated := make([]rune, 0, len(value))
	byteCount := 0
	for _, r := range value {
		runeSize := len(string(r))
		if byteCount+runeSize > maxBytes {
			break
		}
		truncated = append(truncated, r)
		byteCount += runeSize
	}
	return string(truncated)
}

func buildGeneratedChannel(source *model.UpstreamSource, mapping *model.UpstreamSourceChannelMapping, config upstreamSourceSyncConfig, resolution upstreamSourceRuleResolution, rawKey string) *model.Channel {
	channel := &model.Channel{
		Name:     upstreamSourceGeneratedChannelName(source, mapping),
		Type:     config.ChannelType,
		Key:      rawKey,
		BaseURL:  common.GetPointer(upstreamSourceGeneratedBaseURL(source)),
		Group:    resolution.LocalGroup,
		Priority: common.GetPointer(config.DefaultPriority),
		Weight:   common.GetPointer(config.DefaultWeight),
		Tag:      common.GetPointer(strings.TrimSpace(source.Name)),
		Remark:   upstreamSourceGeneratedChannelRemark(mapping),
	}
	channel.SetOtherSettings(dto.ChannelOtherSettings{
		ChannelMonitorEnabled:         resolution.MonitorEnabled,
		ChannelMonitorIntervalMinutes: resolution.MonitorIntervalMinutes,
		GeneratedByUpstreamSourceID:   source.Id,
		GeneratedByUpstreamMappingID:  mapping.Id,
	})
	return channel
}

func mergeGeneratedChannelOtherSettings(channel *model.Channel, existingChannel *model.Channel, resolution upstreamSourceRuleResolution, source *model.UpstreamSource, mapping *model.UpstreamSourceChannelMapping) {
	if channel == nil || existingChannel == nil {
		return
	}
	settings := existingChannel.GetOtherSettings()
	settings.ChannelMonitorEnabled = resolution.MonitorEnabled
	settings.ChannelMonitorIntervalMinutes = resolution.MonitorIntervalMinutes
	if source != nil {
		settings.GeneratedByUpstreamSourceID = source.Id
	}
	if mapping != nil {
		settings.GeneratedByUpstreamMappingID = mapping.Id
	}
	channel.SetOtherSettings(settings)
}

func fetchGeneratedChannelModels(fetchModels func(channel *model.Channel) ([]string, error), channel *model.Channel, resolution upstreamSourceRuleResolution) ([]string, error) {
	models, err := fetchModels(channel)
	if err != nil {
		return nil, err
	}
	models = normalizeFetchedModelNames(models)
	if len(models) == 0 {
		return nil, errors.New("models are required before enabling generated channel")
	}
	if resolution.ModelStrategy == upstreamSourceModelStrategyFixed {
		models = intersectFetchedModelsWithFixedModels(models, resolution.FixedModels)
		if len(models) == 0 {
			return nil, errors.New("no configured models are available upstream")
		}
	}
	return models, nil
}

func intersectFetchedModelsWithFixedModels(fetchedModels []string, fixedModels []string) []string {
	allowed := make(map[string]struct{}, len(fixedModels))
	for _, modelName := range normalizeUpstreamSourceFixedModels(fixedModels) {
		allowed[modelName] = struct{}{}
	}
	intersected := make([]string, 0, len(fetchedModels))
	for _, modelName := range fetchedModels {
		if _, ok := allowed[modelName]; ok {
			intersected = append(intersected, modelName)
		}
	}
	return intersected
}

func saveGeneratedChannel(channel *model.Channel, create bool, now int64) (*model.Channel, error) {
	channel.LastSyncTime = now
	channel.UpdatedTime = now
	if create {
		channel.CreatedTime = now
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
		"name":           channel.Name,
		"type":           channel.Type,
		"base_url":       channel.BaseURL,
		"key":            channel.Key,
		"group":          channel.Group,
		"priority":       channel.Priority,
		"weight":         channel.Weight,
		"tag":            channel.Tag,
		"models":         channel.Models,
		"settings":       channel.OtherSettings,
		"status":         channel.Status,
		"last_sync_time": channel.LastSyncTime,
		"updated_time":   channel.UpdatedTime,
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
	case model.UpstreamSourceTypeNewAPI:
		return NewAPIAdapter{}, nil
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
	if !isSupportedUpstreamSourceType(source.Type) {
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

func isSupportedUpstreamSourceType(sourceType string) bool {
	switch strings.TrimSpace(sourceType) {
	case model.UpstreamSourceTypeSub2API, model.UpstreamSourceTypeNewAPI:
		return true
	default:
		return false
	}
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

func discoveredGroupsToMappings(sourceID int, groups []UpstreamGroup, now int64, config upstreamSourceSyncConfig) ([]model.UpstreamSourceChannelMapping, []string, int) {
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
		mapping := model.UpstreamSourceChannelMapping{
			SourceID:                 sourceID,
			SyncEnabled:              true,
			UpstreamGroupID:          groupID,
			UpstreamGroupName:        strings.TrimSpace(group.Name),
			UpstreamGroupDescription: strings.TrimSpace(group.Description),
			UpstreamPlatform:         strings.TrimSpace(group.Platform),
			DiscoveryStatus:          discoveryStatus,
			UpstreamStatus:           strings.TrimSpace(group.Status),
			UpstreamRateMultiplier:   group.RateMultiplier,
			EffectiveRateMultiplier:  group.EffectiveRateMultiplier,
			LastDiscoveredAt:         now,
		}
		resolution := resolveUpstreamSourceRule(config, &mapping)
		mapping.SyncEnabled = discoveryStatus == model.UpstreamMappingDiscoveryStatusActive && resolution.SyncEligible
		mappingByID[groupID] = mapping
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
	query := tx.Where("source_id = ?", sourceID)
	if len(discoveredIDs) > 0 {
		query = query.Where("upstream_group_id NOT IN ?", discoveredIDs)
	}
	var staleMappings []model.UpstreamSourceChannelMapping
	if err := query.Find(&staleMappings).Error; err != nil {
		return 0, err
	}
	if len(staleMappings) == 0 {
		return 0, nil
	}
	staleIDs := make([]int, 0, len(staleMappings))
	for _, mapping := range staleMappings {
		staleIDs = append(staleIDs, mapping.Id)
	}
	result := tx.Model(&model.UpstreamSourceChannelMapping{}).Where("id IN ?", staleIDs).Updates(map[string]interface{}{
		"discovery_status":   model.UpstreamMappingDiscoveryStatusStale,
		"sync_enabled":       false,
		"last_discovered_at": now,
		"updated_time":       now,
	})
	if result.Error != nil {
		return 0, result.Error
	}
	source := model.UpstreamSource{Id: sourceID}
	if err := tx.Select("id", "name").First(&source, sourceID).Error; err != nil {
		return 0, err
	}
	disableChannelIDs := make([]int, 0, len(staleMappings))
	for _, mapping := range staleMappings {
		if mapping.LocalChannelID == 0 {
			continue
		}
		var channel model.Channel
		if err := tx.First(&channel, mapping.LocalChannelID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				continue
			}
			return 0, err
		}
		if !isGeneratedChannelOwnedByMapping(&channel, &source, &mapping) {
			continue
		}
		disableChannelIDs = append(disableChannelIDs, channel.Id)
	}
	if len(disableChannelIDs) > 0 {
		if err := tx.Model(&model.Channel{}).Where("id IN ?", disableChannelIDs).Updates(map[string]interface{}{
			"status":       common.ChannelStatusManuallyDisabled,
			"updated_time": now,
		}).Error; err != nil {
			return 0, err
		}
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

func buildDiscoveryResultTx(tx *gorm.DB, sourceID int, rawConfig string, discovered int, staleCount int, invalidCount int) (dto.UpstreamSourceDiscoveryResult, error) {
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
		result.Mappings = append(result.Mappings, BuildUpstreamSourceMappingResponse(mapping, rawConfig))
	}
	sort.SliceStable(result.Mappings, func(i, j int) bool {
		return result.Mappings[i].UpstreamGroupID < result.Mappings[j].UpstreamGroupID
	})
	return result, nil
}

func BuildUpstreamSourceMappingResponse(mapping model.UpstreamSourceChannelMapping, rawConfig string) dto.UpstreamSourceMappingResponse {
	config, err := parseUpstreamSourceSyncConfig(rawConfig)
	if err != nil {
		config, _ = parseUpstreamSourceSyncConfig("")
	}
	return buildUpstreamSourceMappingResponse(mapping, config)
}

func buildUpstreamSourceMappingResponse(mapping model.UpstreamSourceChannelMapping, config upstreamSourceSyncConfig) dto.UpstreamSourceMappingResponse {
	eligibilityMapping := mapping
	eligibilityMapping.SyncEnabled = true
	resolution := resolveUpstreamSourceRule(config, &eligibilityMapping)
	return dto.UpstreamSourceMappingResponse{
		Id:                              mapping.Id,
		SourceID:                        mapping.SourceID,
		SyncEnabled:                     mapping.SyncEnabled,
		UpstreamGroupID:                 mapping.UpstreamGroupID,
		UpstreamGroupName:               mapping.UpstreamGroupName,
		UpstreamGroupDescription:        mapping.UpstreamGroupDescription,
		UpstreamPlatform:                mapping.UpstreamPlatform,
		DiscoveryStatus:                 mapping.DiscoveryStatus,
		UpstreamStatus:                  mapping.UpstreamStatus,
		UpstreamRateMultiplier:          mapping.UpstreamRateMultiplier,
		EffectiveRateMultiplier:         mapping.EffectiveRateMultiplier,
		HasUpstreamKey:                  mapping.UpstreamKeyID != "",
		LocalChannelID:                  mapping.LocalChannelID,
		SyncStatus:                      mapping.SyncStatus,
		LastError:                       sanitizeUpstreamSourceStoredError(mapping.LastError),
		LastDiscoveredAt:                mapping.LastDiscoveredAt,
		LastSyncedAt:                    mapping.LastSyncedAt,
		SyncEligible:                    resolution.SyncEligible,
		MatchedRuleName:                 resolution.RuleName,
		MatchReason:                     resolution.Reason,
		ResolvedLocalGroup:              resolution.LocalGroup,
		ResolvedMonitorEnabled:          resolution.MonitorEnabled,
		ResolvedMonitorIntervalMinutes:  resolution.MonitorIntervalMinutes,
		ResolvedAutoSyncEnabled:         resolution.AutoSyncEnabled,
		ResolvedAutoSyncIntervalMinutes: resolution.AutoSyncIntervalMinutes,
		ResolvedModelStrategy:           resolution.ModelStrategy,
		ResolvedFixedModels:             resolution.FixedModels,
	}
}

func sanitizeUpstreamSourceStoredError(text string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	return SanitizeUpstreamSourceError(errors.New(text))
}
