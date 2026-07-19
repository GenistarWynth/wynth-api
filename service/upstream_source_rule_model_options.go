package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"

	"gorm.io/gorm"
)

func (s *UpstreamSourceService) ResolveRuleModelOptions(ctx context.Context, sourceID int, rules []dto.UpstreamSourceLocalGroupRule, ruleIndex int) (*dto.UpstreamSourceRuleModelOptionsResponse, error) {
	if sourceID == 0 {
		return nil, fmt.Errorf("source ID is required")
	}
	if ruleIndex < 0 || ruleIndex >= len(rules) {
		return nil, fmt.Errorf("rule index is out of range")
	}

	var source model.UpstreamSource
	if err := model.DB.WithContext(ctx).
		Where("id = ? AND status <> ?", sourceID, model.UpstreamSourceStatusDeleted).
		First(&source).Error; err != nil {
		return nil, err
	}

	config, err := parseUpstreamSourceSyncConfig(source.SyncConfig)
	if err != nil {
		return nil, err
	}
	config.LocalGroupRules = NormalizeUpstreamSourceLocalGroupRulesForConfig(rules)

	var mappings []model.UpstreamSourceChannelMapping
	if err := model.DB.WithContext(ctx).
		Where("source_id = ?", sourceID).
		Order("id").
		Find(&mappings).Error; err != nil {
		return nil, err
	}

	response := &dto.UpstreamSourceRuleModelOptionsResponse{
		Models:          []string{},
		MatchedMappings: []dto.UpstreamSourceRuleModelOptionsMatchedMapping{},
	}
	if len(mappings) == 0 {
		return response, nil
	}

	fetchModels := s.fetchModels(config)
	var adapter UpstreamSourceAdapter
	adapterLoaded := false
	intersectedModels := []string(nil)

	for i := range mappings {
		// The submitted rules are a pre-save preview, so derive eligibility from
		// active discovery instead of the mapping's eligibility under the last
		// persisted rules.
		previewMapping := mappings[i]
		previewMapping.SyncEnabled = true
		resolution, matchedRuleIndex := resolveUpstreamSourceRuleWithIndex(config, &previewMapping)
		if matchedRuleIndex != ruleIndex || !resolution.SyncEligible {
			continue
		}

		response.MatchedMappings = append(response.MatchedMappings, dto.UpstreamSourceRuleModelOptionsMatchedMapping{
			MappingID:         mappings[i].Id,
			UpstreamGroupID:   mappings[i].UpstreamGroupID,
			UpstreamGroupName: mappings[i].UpstreamGroupName,
			UpstreamPlatform:  mappings[i].UpstreamPlatform,
			LocalChannelID:    mappings[i].LocalChannelID,
		})

		channel, err := resolveRuleModelOptionsChannel(ctx, &source, &mappings[i], config, resolution, &adapter, &adapterLoaded, s.adapterFactory())
		if err != nil {
			return nil, err
		}

		models, err := fetchModels(channel)
		if err != nil {
			return nil, fmt.Errorf("fetch models for upstream group %q failed: %w", mappings[i].UpstreamGroupName, err)
		}
		models = normalizeFetchedModelNames(models)
		if intersectedModels == nil {
			intersectedModels = models
		} else {
			intersectedModels = intersectFetchedModelsWithFixedModels(intersectedModels, models)
		}
	}

	if intersectedModels != nil {
		response.Models = intersectedModels
	}
	return response, nil
}

func resolveRuleModelOptionsChannel(ctx context.Context, source *model.UpstreamSource, mapping *model.UpstreamSourceChannelMapping, config upstreamSourceSyncConfig, resolution upstreamSourceRuleResolution, adapter *UpstreamSourceAdapter, adapterLoaded *bool, adapterFactory func(sourceType string) (UpstreamSourceAdapter, error)) (*model.Channel, error) {
	if source == nil || mapping == nil {
		return nil, fmt.Errorf("source mapping context is required")
	}

	if mapping.LocalChannelID > 0 {
		channel, err := loadChannelByIDWithContext(ctx, mapping.LocalChannelID)
		if err == nil && isGeneratedChannelOwnedByMapping(channel, source, mapping) {
			channel.Type = resolution.ChannelType
			return channel, nil
		}
		if err != nil && !errorsIsRecordNotFound(err) {
			return nil, err
		}
	}

	if !*adapterLoaded {
		loadedAdapter, err := adapterFactory(source.Type)
		if err != nil {
			return nil, err
		}
		*adapter = loadedAdapter
		*adapterLoaded = true
	}
	if *adapter == nil {
		return nil, fmt.Errorf("upstream source adapter is unavailable")
	}

	key, found, err := findUpstreamSourceKeyWithValue(ctx, *adapter, source, mapping, UpstreamKey{})
	if err != nil {
		return nil, err
	}
	if !found {
		key, _, err = ensureUpstreamSourceMappingKey(ctx, *adapter, source, mapping)
		if err != nil {
			return nil, err
		}
	}
	if strings.TrimSpace(key.Key) == "" {
		recoveredKey, err := recoverUpstreamSourceKeyForChannelCreate(ctx, *adapter, source, mapping, key)
		if err != nil {
			return nil, err
		}
		key = recoveredKey
	}
	rawKey := strings.TrimSpace(key.Key)
	if rawKey == "" {
		return nil, fmt.Errorf("upstream key value is missing")
	}

	return buildGeneratedChannel(source, mapping, config, resolution, rawKey), nil
}

func errorsIsRecordNotFound(err error) bool {
	return errors.Is(err, gorm.ErrRecordNotFound)
}
