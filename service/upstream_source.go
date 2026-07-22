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

var errGeneratedChannelModelsRequired = errors.New("models are required before enabling generated channel")

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
	if _, err := loadUpstreamSourceRuntimeAuth(&source); err != nil {
		return nil, err
	}
	originalAuth := source.AuthConfig

	now := s.now()
	scan, err := model.CreateUpstreamSourceScan(source.Id, model.UpstreamSourceScanTypeDiscover, now)
	if err != nil {
		return nil, err
	}
	if err := validateUpstreamSourceDiscoveryConfig(&source); err != nil {
		return s.recordDiscoveryFailure(source.Id, scan.Id, s.now(), err)
	}

	adapter, err := s.adapterFactory()(source.Type)
	if err != nil {
		return s.recordDiscoveryFailure(source.Id, scan.Id, s.now(), err)
	}
	if adapter == nil {
		err := errors.New("upstream source adapter is unavailable")
		return s.recordDiscoveryFailure(source.Id, scan.Id, s.now(), err)
	}

	groups, err := adapter.DiscoverGroups(ctx, &source)
	if err != nil {
		recordUpstreamSourceAuthFailure(&source, err, s.now())
		return s.recordDiscoveryFailure(source.Id, scan.Id, s.now(), err)
	}
	persistUpstreamSourceAuthState(&source, originalAuth, s.now(), true)

	config, err := parseUpstreamSourceSyncConfig(source.SyncConfig)
	if err != nil {
		return s.recordDiscoveryFailure(source.Id, scan.Id, s.now(), err)
	}

	mappings, discoveredIDs, invalidCount := discoveredGroupsToMappings(source.Id, groups, now, config)
	finishedAt := s.now()
	var result dto.UpstreamSourceDiscoveryResult
	if err := model.DB.Transaction(func(tx *gorm.DB) error {
		if err := model.LockUpstreamSourceForScanTx(tx, source.Id); err != nil {
			return err
		}
		var previousMappings []model.UpstreamSourceChannelMapping
		if err := tx.Where("source_id = ?", source.Id).Find(&previousMappings).Error; err != nil {
			return err
		}
		hasSuccessfulScan, err := model.HasSuccessfulUpstreamSourceScanTx(tx, source.Id, model.UpstreamSourceScanTypeDiscover)
		if err != nil {
			return err
		}
		baseline := !hasSuccessfulScan
		var changes []model.UpstreamSourceGroupChange
		if !baseline {
			changes = buildUpstreamSourceGroupChanges(source.Id, scan.Id, previousMappings, mappings, now)
		}
		if err := model.UpsertDiscoveredMappingsTx(tx, source.Id, mappings, now); err != nil {
			return err
		}
		staleCount, err := markMissingDiscoveredMappingsStaleTx(tx, source.Id, discoveredIDs, now)
		if err != nil {
			return err
		}
		if len(changes) > 0 {
			if err := tx.Create(&changes).Error; err != nil {
				return err
			}
		}
		if err := updateUpstreamSourceDiscoveryStatusTx(tx, source.Id, model.UpstreamDiscoveryStatusSucceeded, "", now); err != nil {
			return err
		}
		if err := model.FinishUpstreamSourceScanTx(tx, scan.Id, model.UpstreamSourceScanStatusSuccess, baseline, finishedAt, ""); err != nil {
			return err
		}

		built, err := buildDiscoveryResultTx(tx, source.Id, source.SyncConfig, len(groups), staleCount, invalidCount)
		if err != nil {
			return err
		}
		result = built
		return nil
	}); err != nil {
		return s.recordDiscoveryFailure(source.Id, scan.Id, s.now(), err)
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
	if _, err := loadUpstreamSourceRuntimeAuth(&source); err != nil {
		return nil, err
	}
	originalAuth := source.AuthConfig

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
	for i := range mappings {
		resolutions[i] = resolveUpstreamSourceRuleForManualSync(config, &mappings[i])
		if mode == upstreamSourceSyncModeAuto && resolutions[i].SyncEligible && !upstreamSourceMappingAutoSyncDue(config, &mappings[i], now) {
			resolutions[i].SyncEligible = false
			resolutions[i].Reason = upstreamSourceMatchReasonAutoSyncNotDue
		}
		if resolutions[i].SyncEligible {
			eligibleCount++
		}
	}

	// A source with genuinely no discovered/selected mappings can never sync
	// regardless of its rules, so this hard error is scoped to that case
	// alone (not conflated with "rules exist but nothing matched").
	if len(mappings) == 0 {
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
		// No rules configured at all: "no rules" means sync nothing, which
		// is a deliberate, successful no-op rather than a failure.
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
	persistUpstreamSourceAuthState(&source, originalAuth, s.now(), false)

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
		recordUpstreamSourceAuthFailure(&source, errors.New(result.Error), s.now())
	} else {
		finalStatus = model.UpstreamSyncStatusSucceeded
	}
	return result, nil
}

func resolveUpstreamSourceRuleForManualSync(config upstreamSourceSyncConfig, mapping *model.UpstreamSourceChannelMapping) upstreamSourceRuleResolution {
	resolution := resolveUpstreamSourceRule(config, mapping)
	if mapping == nil || mapping.SyncEnabled {
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

// RecomputeUpstreamSourceMappingSyncEligibility re-derives every mapping's
// sync_enabled flag from the source's CURRENT sync rules, so adding or editing a
// rule takes effect immediately without a manual per-mapping toggle — rules are
// the source of truth for what syncs. A mapping is enabled iff its discovery is
// active and a rule matches it; everything else is disabled. Call it after the
// sync config changes (it is also applied on discovery via the upsert).
func RecomputeUpstreamSourceMappingSyncEligibility(ctx context.Context, sourceID int) error {
	if sourceID == 0 {
		return errors.New("source ID is required")
	}
	var source model.UpstreamSource
	if err := model.DB.WithContext(ctx).First(&source, sourceID).Error; err != nil {
		return err
	}
	config, err := parseUpstreamSourceSyncConfig(source.SyncConfig)
	if err != nil {
		return err
	}
	var mappings []model.UpstreamSourceChannelMapping
	if err := model.DB.WithContext(ctx).Where("source_id = ?", sourceID).Find(&mappings).Error; err != nil {
		return err
	}
	for i := range mappings {
		mapping := mappings[i]
		// Probe rule-eligibility independent of the stored flag (mirrors
		// resolveUpstreamSourceRuleForManualSync): a rule match with active
		// discovery => enabled.
		probe := mapping
		probe.SyncEnabled = true
		desired := resolveUpstreamSourceRule(config, &probe).SyncEligible
		if desired == mapping.SyncEnabled {
			continue
		}
		if err := model.DB.WithContext(ctx).Model(&model.UpstreamSourceChannelMapping{}).
			Where("id = ?", mapping.Id).Update("sync_enabled", desired).Error; err != nil {
			return err
		}
	}
	return nil
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

// upstreamSourceResponseSnippet condenses an upstream response body into one
// short diagnostic line for error messages: an empty-body marker, the HTML
// <title> (Cloudflare/nginx error pages put the verdict there), or the
// whitespace-collapsed head of the body (capped). Used so an undecodable or
// status-only upstream reply (e.g. an expired session returning an empty 401)
// surfaces its real cause instead of an opaque "unexpected end of JSON input".
func upstreamSourceResponseSnippet(body []byte) string {
	text := strings.TrimSpace(string(body))
	if text == "" {
		return "(empty response body)"
	}
	lower := strings.ToLower(text)
	if start := strings.Index(lower, "<title>"); start >= 0 {
		rest := text[start+len("<title>"):]
		if end := strings.Index(strings.ToLower(rest), "</title>"); end >= 0 {
			if title := strings.Join(strings.Fields(rest[:end]), " "); title != "" {
				return "page title: " + title
			}
		}
	}
	joined := strings.Join(strings.Fields(text), " ")
	const maxSnippetRunes = 160
	if runes := []rune(joined); len(runes) > maxSnippetRunes {
		return string(runes[:maxSnippetRunes]) + "…"
	}
	return joined
}

func upstreamSourceGeneratedBaseURL(source *model.UpstreamSource) string {
	if source == nil {
		return ""
	}
	// TrimRight the trailing slash defensively: sources persisted before URL
	// normalization (or via other paths) may still carry one, which would make
	// the generated channel's base URL join to "host//v1/..." and hit the
	// gateway's web fallback instead of the API.
	if trimmed := strings.TrimRight(strings.TrimSpace(source.RelayBaseURL), "/"); trimmed != "" {
		return trimmed
	}
	return strings.TrimRight(strings.TrimSpace(source.BaseURL), "/")
}

// RefreshUpstreamSourceGeneratedChannelConnectionsTx updates the source-owned
// connection snapshot copied into generated channels. Channel type, key,
// routing group, models, priority, weight, status, and rule settings remain
// channel/mapping-owned and are refreshed by the normal source sync path.
func RefreshUpstreamSourceGeneratedChannelConnectionsTx(tx *gorm.DB, source *model.UpstreamSource) (bool, error) {
	if tx == nil {
		return false, errors.New("database transaction is required")
	}
	if source == nil || source.Id == 0 {
		return false, errors.New("upstream source is required")
	}

	var mappings []model.UpstreamSourceChannelMapping
	if err := tx.Where("source_id = ? AND local_channel_id <> 0", source.Id).Find(&mappings).Error; err != nil {
		return false, err
	}
	changed := false
	baseURL := upstreamSourceGeneratedBaseURL(source)
	for i := range mappings {
		var channel model.Channel
		if err := tx.First(&channel, mappings[i].LocalChannelID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				continue
			}
			return false, err
		}
		if !isGeneratedChannelOwnedByMapping(&channel, source, &mappings[i]) {
			continue
		}
		if err := tx.Model(&model.Channel{}).Where("id = ?", channel.Id).Updates(map[string]any{
			"base_url":     baseURL,
			"name":         upstreamSourceGeneratedChannelName(source, &mappings[i]),
			"tag":          strings.TrimSpace(source.Name),
			"updated_time": common.GetTimestamp(),
		}).Error; err != nil {
			return false, err
		}
		changed = true
	}
	return changed, nil
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
			errText := upstreamSourceFailureMessage(err)
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
		if ensuredRawKey := strings.TrimSpace(key.Key); ensuredRawKey != "" {
			rawKey = ensuredRawKey
		}
	}
	if rawKey == "" {
		if !ensuredKeyLoaded {
			key, created, err := ensureUpstreamSourceMappingKey(ctx, adapter, source, mapping)
			if err != nil {
				errText := upstreamSourceFailureMessage(err)
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
				errText := upstreamSourceFailureMessage(err)
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
	if shouldSkipNewAPIEmptyModelMapping(source, modelErr) {
		errText := SanitizeUpstreamSourceError(modelErr)
		if existingChannel != nil {
			if err := disableGeneratedChannelForSkippedMapping(ctx, existingChannel, now); err != nil {
				localErrText := SanitizeUpstreamSourceError(err)
				_ = updateUpstreamSourceMappingSync(mapping.Id, upstreamKeyID, mapping.LocalChannelID, model.UpstreamMappingSyncStatusFailed, localErrText, now)
				result.Status = model.UpstreamMappingSyncStatusFailed
				result.Error = localErrText
				return result, nil
			}
		}
		_ = updateUpstreamSourceMappingSync(mapping.Id, upstreamKeyID, mapping.LocalChannelID, model.UpstreamMappingSyncStatusSkipped, errText, now)
		result.Status = model.UpstreamMappingSyncStatusSkipped
		result.Error = errText
		return result, existingChannel
	}
	if modelErr != nil {
		result.Error = SanitizeUpstreamSourceError(modelErr)
		channel.Models = ""
		channel.Status = common.ChannelStatusManuallyDisabled
	} else {
		channel.Models = strings.Join(models, ",")
		channel.Status = common.ChannelStatusEnabled
	}

	created := mapping.LocalChannelID == 0
	if !created {
		if err := model.RejectAccountPoolBoundChannelTypeChange(channel.Id, channel.Type); err != nil {
			errText := SanitizeUpstreamSourceError(err)
			_ = updateUpstreamSourceMappingSync(mapping.Id, upstreamKeyID, mapping.LocalChannelID, model.UpstreamMappingSyncStatusFailed, errText, now)
			result.Status = model.UpstreamMappingSyncStatusFailed
			result.Error = errText
			return result, nil
		}
	}
	var savedChannel *model.Channel
	err := model.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		savedChannel, err = saveGeneratedChannelTx(tx, channel, created, autoPriorityOwnsGeneratedChannelPriority(existingChannel, resolution), now)
		if err != nil {
			return err
		}
		if savedChannel.Status == common.ChannelStatusManuallyDisabled {
			if _, err = sinkManuallyDisabledChannels(ctx, tx, []int{savedChannel.Id}, nil); err != nil {
				return err
			}
			if err = tx.First(savedChannel, savedChannel.Id).Error; err != nil {
				return err
			}
		}
		if created {
			return savedChannel.AddAbilities(tx)
		}
		return savedChannel.UpdateAbilities(tx)
	})
	if err != nil {
		errText := SanitizeUpstreamSourceError(err)
		_ = updateUpstreamSourceMappingSync(mapping.Id, upstreamKeyID, mapping.LocalChannelID, model.UpstreamMappingSyncStatusFailed, errText, now)
		result.Status = model.UpstreamMappingSyncStatusFailed
		result.Error = errText
		result.LocalChannelID = mapping.LocalChannelID
		return result, nil
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

func disableGeneratedChannelForSkippedMapping(ctx context.Context, channel *model.Channel, now int64) error {
	if channel == nil {
		return nil
	}
	err := model.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.Channel{}).Where("id = ?", channel.Id).Updates(map[string]any{
			"status":       common.ChannelStatusManuallyDisabled,
			"updated_time": now,
		}).Error; err != nil {
			return err
		}
		if _, err := sinkManuallyDisabledChannels(ctx, tx, []int{channel.Id}, nil); err != nil {
			return err
		}
		return tx.Model(&model.Ability{}).
			Where("channel_id = ?", channel.Id).
			Select("enabled").
			Update("enabled", false).Error
	})
	if err != nil {
		return err
	}
	return model.DB.WithContext(ctx).First(channel, channel.Id).Error
}

func shouldSkipNewAPIEmptyModelMapping(source *model.UpstreamSource, modelErr error) bool {
	if source == nil || modelErr == nil {
		return false
	}
	return strings.TrimSpace(source.Type) == model.UpstreamSourceTypeNewAPI &&
		errors.Is(modelErr, errGeneratedChannelModelsRequired)
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
	keys, listErr := adapter.ListKeys(ctx, source, mapping.UpstreamGroupID)
	if listErr == nil {
		keyID := strings.TrimSpace(mapping.UpstreamKeyID)
		groupID := strings.TrimSpace(mapping.UpstreamGroupID)
		for _, key := range keys {
			if strings.TrimSpace(key.ID) != keyID {
				continue
			}
			listedName := strings.TrimSpace(key.Name)
			listedGroupID := strings.TrimSpace(key.GroupID)
			if (listedName == "" || listedName == name) && (listedGroupID == "" || listedGroupID == groupID) {
				return key, false, nil
			}
			break
		}
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
		Type:     resolution.ChannelType,
		Key:      rawKey,
		BaseURL:  common.GetPointer(upstreamSourceGeneratedBaseURL(source)),
		Group:    resolution.LocalGroup,
		Priority: common.GetPointer(resolution.Priority),
		Weight:   common.GetPointer(resolution.Weight),
		Tag:      common.GetPointer(strings.TrimSpace(source.Name)),
		Remark:   upstreamSourceGeneratedChannelRemark(mapping),
	}
	channel.SetOtherSettings(dto.ChannelOtherSettings{
		ChannelMonitorEnabled:                      resolution.MonitorEnabled,
		ChannelMonitorIntervalMinutes:              resolution.MonitorIntervalMinutes,
		ChannelMonitorModel:                        resolution.MonitorModel,
		ChannelAutoPriorityEnabled:                 resolution.AutoPriorityEnabled,
		ChannelAutoPriorityIntervalMinutes:         resolution.AutoPriorityIntervalMinutes,
		ChannelAutoPriorityWindowHours:             resolution.AutoPriorityWindowHours,
		ChannelAutoPriorityAvailabilityWindowHours: dto.ChannelAutoPriorityDefaultWindowHours,
		CodexImageGenerationBridgePolicy:           upstreamSourceGeneratedCodexImageGenerationBridgePolicy(resolution),
		GeneratedByUpstreamSourceID:                source.Id,
		GeneratedByUpstreamMappingID:               mapping.Id,
	})
	return channel
}

// autoPriorityOwnsGeneratedChannelPriority reports whether the auto-priority
// worker owns this generated channel's priority column. When it does, a re-sync
// must NOT write the priority field at all — not even the existing value it read
// earlier, which can be stale (sync blocks on an upstream model-list call while
// the 1-minute auto-priority worker commits a newer priority). Omitting the
// column leaves whatever the worker last wrote, so sync never clobbers it. Only
// true when re-syncing an existing channel with auto-priority enabled; a brand
// new channel still gets the rule's static priority as its baseline.
func autoPriorityOwnsGeneratedChannelPriority(existingChannel *model.Channel, resolution upstreamSourceRuleResolution) bool {
	return existingChannel != nil && resolution.AutoPriorityEnabled
}

func mergeGeneratedChannelOtherSettings(channel *model.Channel, existingChannel *model.Channel, resolution upstreamSourceRuleResolution, source *model.UpstreamSource, mapping *model.UpstreamSourceChannelMapping) {
	if channel == nil || existingChannel == nil {
		return
	}
	settings := existingChannel.GetOtherSettings()
	settings.ChannelMonitorEnabled = resolution.MonitorEnabled
	settings.ChannelMonitorIntervalMinutes = resolution.MonitorIntervalMinutes
	settings.ChannelMonitorModel = resolution.MonitorModel
	settings.ChannelAutoPriorityEnabled = resolution.AutoPriorityEnabled
	settings.ChannelAutoPriorityWindowHours = resolution.AutoPriorityWindowHours
	localGroupChanged := strings.TrimSpace(existingChannel.Group) != strings.TrimSpace(channel.Group)
	if localGroupChanged {
		settings.ChannelAutoPriorityIntervalMinutes = resolution.AutoPriorityIntervalMinutes
	}
	if settings.ChannelAutoPriorityAvailabilityWindowHours == 0 {
		settings.ChannelAutoPriorityAvailabilityWindowHours = dto.ChannelAutoPriorityDefaultWindowHours
	}
	settings.CodexImageGenerationBridgePolicy = upstreamSourceGeneratedCodexImageGenerationBridgePolicy(resolution)
	if source != nil {
		settings.GeneratedByUpstreamSourceID = source.Id
	}
	if mapping != nil {
		settings.GeneratedByUpstreamMappingID = mapping.Id
	}
	channel.SetOtherSettings(settings)
}

func upstreamSourceGeneratedCodexImageGenerationBridgePolicy(resolution upstreamSourceRuleResolution) string {
	policy := dto.NormalizeCodexImageGenerationBridgePolicy(resolution.CodexImageGenerationBridgePolicy)
	if policy == dto.CodexImageGenerationBridgePolicyFollow {
		return ""
	}
	return policy
}

func fetchGeneratedChannelModels(fetchModels func(channel *model.Channel) ([]string, error), channel *model.Channel, resolution upstreamSourceRuleResolution) ([]string, error) {
	models, err := fetchModels(channel)
	if err != nil {
		return nil, err
	}
	models = normalizeFetchedModelNames(models)
	if len(models) == 0 {
		return nil, errGeneratedChannelModelsRequired
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

func saveGeneratedChannel(channel *model.Channel, create bool, autoPriorityManaged bool, now int64) (*model.Channel, error) {
	if !create {
		if err := model.RejectAccountPoolBoundChannelTypeChange(channel.Id, channel.Type); err != nil {
			return nil, err
		}
	}
	return saveGeneratedChannelTx(model.DB, channel, create, autoPriorityManaged, now)
}

// saveGeneratedChannelTx assumes account-pool type compatibility was checked
// before opening tx, because that guard uses the primary DB connection.
func saveGeneratedChannelTx(db *gorm.DB, channel *model.Channel, create bool, autoPriorityManaged bool, now int64) (*model.Channel, error) {
	channel.LastSyncTime = now
	channel.UpdatedTime = now
	if create {
		channel.CreatedTime = now
		if err := db.Create(channel).Error; err != nil {
			return nil, err
		}
		return channel, nil
	}
	updates := generatedChannelUpdateMap(channel, autoPriorityManaged)
	if err := db.Model(&model.Channel{}).Where("id = ?", channel.Id).Updates(updates).Error; err != nil {
		return nil, err
	}
	var reloaded model.Channel
	if err := db.First(&reloaded, channel.Id).Error; err != nil {
		return nil, err
	}
	return &reloaded, nil
}

func generatedChannelUpdateMap(channel *model.Channel, autoPriorityManaged bool) map[string]any {
	updates := map[string]any{
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
	if autoPriorityManaged {
		// Auto-priority owns the priority column; omit it so this sync never
		// writes a (possibly stale) value over a concurrent worker commit.
		delete(updates, "priority")
	}
	return updates
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
	query := tx.Where("source_id = ? AND discovery_status <> ?", sourceID, model.UpstreamMappingDiscoveryStatusStale)
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
		if _, err := sinkManuallyDisabledChannels(tx.Statement.Context, tx, disableChannelIDs, nil); err != nil {
			return 0, err
		}
		if err := tx.Model(&model.Ability{}).
			Where("channel_id IN ?", disableChannelIDs).
			Select("enabled").
			Update("enabled", false).Error; err != nil {
			return 0, err
		}
	}
	return int(result.RowsAffected), nil
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

func (s *UpstreamSourceService) recordDiscoveryFailure(sourceID int, scanID int, now int64, discoveryErr error) (*dto.UpstreamSourceDiscoveryResult, error) {
	sanitized := upstreamSourceFailureMessage(discoveryErr)
	result := &dto.UpstreamSourceDiscoveryResult{
		SourceID: sourceID,
		Error:    sanitized,
	}
	persistErr := model.DB.Transaction(func(tx *gorm.DB) error {
		if err := model.FinishUpstreamSourceScanTx(tx, scanID, model.UpstreamSourceScanStatusFailed, false, now, sanitized); err != nil {
			return fmt.Errorf("finish failed upstream source scan: %w", err)
		}
		if err := updateUpstreamSourceDiscoveryStatusTx(tx, sourceID, model.UpstreamDiscoveryStatusFailed, sanitized, now); err != nil {
			return fmt.Errorf("update failed upstream source discovery status: %w", err)
		}
		return nil
	})
	if persistErr != nil {
		return result, errors.Join(discoveryErr, fmt.Errorf("record discovery failure: %w", persistErr))
	}
	return result, discoveryErr
}

func (s *UpstreamSourceService) recordSyncFailure(sourceID int, now int64, err error) *dto.UpstreamSourceSyncResult {
	sanitized := upstreamSourceFailureMessage(err)
	_ = updateUpstreamSourceSyncStatus(sourceID, model.UpstreamSyncStatusFailed, sanitized, now)
	return &dto.UpstreamSourceSyncResult{
		SourceID: sourceID,
		Status:   model.UpstreamSyncStatusFailed,
		Error:    sanitized,
	}
}

// upstreamSourceFailureMessage maps a Cloudflare Turnstile block to its canonical
// sentinel text so callers (and the frontend) can reliably detect it, while all
// other errors continue to go through sanitization.
func upstreamSourceFailureMessage(err error) string {
	if errors.Is(err, ErrUpstreamSourceTurnstileRequired) {
		return ErrUpstreamSourceTurnstileRequired.Error()
	}
	return SanitizeUpstreamSourceError(err)
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
		ResolvedAutoPriorityEnabled:     resolution.AutoPriorityEnabled,
		ResolvedAutoPriorityWindowHours: resolution.AutoPriorityWindowHours,
		ResolvedAutoPriorityAvailabilityWindowHours: resolution.AutoPriorityAvailabilityWindowHours,
		ResolvedCodexImageGenerationBridgePolicy:    resolution.CodexImageGenerationBridgePolicy,
		ResolvedModelStrategy:                       resolution.ModelStrategy,
		ResolvedFixedModels:                         resolution.FixedModels,
		ResolvedChannelType:                         resolution.ChannelType,
		ResolvedPriority:                            resolution.Priority,
		ResolvedWeight:                              resolution.Weight,
		ResolvedMonitorModel:                        resolution.MonitorModel,
	}
}

func sanitizeUpstreamSourceStoredError(text string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	return SanitizeUpstreamSourceError(errors.New(text))
}
