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

type UpstreamSourceService struct {
	AdapterFactory func(sourceType string) (UpstreamSourceAdapter, error)
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
	mappings := make([]model.UpstreamSourceChannelMapping, 0, len(groups))
	discoveredIDs := make([]string, 0, len(groups))
	invalidCount := 0

	for _, group := range groups {
		groupID := strings.TrimSpace(group.ID)
		if groupID == "" {
			invalidCount++
			continue
		}
		discoveryStatus := model.UpstreamMappingDiscoveryStatusActive
		if group.EffectiveRateMultiplier == nil {
			discoveryStatus = model.UpstreamMappingDiscoveryStatusInvalid
			invalidCount++
		}
		discoveredIDs = append(discoveredIDs, groupID)
		mappings = append(mappings, model.UpstreamSourceChannelMapping{
			SourceID:                sourceID,
			UpstreamGroupID:         groupID,
			UpstreamGroupName:       strings.TrimSpace(group.Name),
			UpstreamPlatform:        strings.TrimSpace(group.Platform),
			DiscoveryStatus:         discoveryStatus,
			UpstreamStatus:          strings.TrimSpace(group.Status),
			UpstreamRateMultiplier:  group.RateMultiplier,
			EffectiveRateMultiplier: group.EffectiveRateMultiplier,
			LastDiscoveredAt:        now,
		})
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
		LastError:               mapping.LastError,
		LastDiscoveredAt:        mapping.LastDiscoveredAt,
		LastSyncedAt:            mapping.LastSyncedAt,
	}
}
