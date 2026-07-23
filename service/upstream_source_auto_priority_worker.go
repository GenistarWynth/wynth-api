package service

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
)

type upstreamSourceAutoPriorityMappingLoader func(ctx context.Context, source model.UpstreamSource) ([]model.UpstreamSourceChannelMapping, error)
type upstreamSourceAutoPriorityChannelLoader func(ctx context.Context, mappings []model.UpstreamSourceChannelMapping) (map[int]model.Channel, error)

type upstreamSourceAutoPriorityJob struct {
	source     model.UpstreamSource
	mappingIDs []int
}

func ListDueUpstreamSourcesForAutoPriority(now int64) ([]model.UpstreamSource, error) {
	jobs, err := listDueUpstreamSourceAutoPriorityJobs(context.Background(), now)
	if err != nil {
		return nil, err
	}
	sources := make([]model.UpstreamSource, 0, len(jobs))
	for _, job := range jobs {
		sources = append(sources, job.source)
	}
	return sources, nil
}

func listDueUpstreamSourceAutoPriorityJobs(ctx context.Context, now int64) ([]upstreamSourceAutoPriorityJob, error) {
	var sources []model.UpstreamSource
	if err := model.DB.WithContext(ctx).
		Select("id", "sync_config").
		Where("status = ?", model.UpstreamSourceStatusEnabled).
		Order("id").
		Find(&sources).Error; err != nil {
		return nil, err
	}

	return listDueUpstreamSourceAutoPriorityJobsFromSources(
		ctx,
		sources,
		now,
		listUpstreamSourceAutoPriorityMappings,
		loadUpstreamSourceAutoPriorityChannels,
	), nil
}

func listDueUpstreamSourcesForAutoPriorityFromSources(sources []model.UpstreamSource, now int64, loadMappings upstreamSourceAutoPriorityMappingLoader, loadChannels upstreamSourceAutoPriorityChannelLoader) []model.UpstreamSource {
	jobs := listDueUpstreamSourceAutoPriorityJobsFromSources(context.Background(), sources, now, loadMappings, loadChannels)
	due := make([]model.UpstreamSource, 0, len(jobs))
	for _, job := range jobs {
		due = append(due, job.source)
	}
	return due
}

func listDueUpstreamSourceAutoPriorityJobsFromSources(ctx context.Context, sources []model.UpstreamSource, now int64, loadMappings upstreamSourceAutoPriorityMappingLoader, loadChannels upstreamSourceAutoPriorityChannelLoader) []upstreamSourceAutoPriorityJob {
	jobs := make([]upstreamSourceAutoPriorityJob, 0, len(sources))
	for _, source := range sources {
		config, err := parseUpstreamSourceSyncConfig(source.SyncConfig)
		if err != nil {
			logger.LogWarn(ctx, fmt.Sprintf("upstream source auto-priority: skip source_id=%d invalid sync config: %v", source.Id, err))
			continue
		}

		mappings, err := loadMappings(ctx, source)
		if err != nil {
			logger.LogWarn(ctx, fmt.Sprintf("upstream source auto-priority: skip source_id=%d mapping query failed: %v", source.Id, err))
			continue
		}
		channels, err := loadChannels(ctx, mappings)
		if err != nil {
			logger.LogWarn(ctx, fmt.Sprintf("upstream source auto-priority: skip source_id=%d channel query failed: %v", source.Id, err))
			continue
		}
		dueMappingIDs := make([]int, 0, len(mappings))
		for i := range mappings {
			if upstreamSourceMappingAutoPriorityDue(source, config, &mappings[i], channels, now) {
				dueMappingIDs = append(dueMappingIDs, mappings[i].Id)
			}
		}
		if len(dueMappingIDs) == 0 {
			continue
		}
		jobs = append(jobs, upstreamSourceAutoPriorityJob{
			source:     source,
			mappingIDs: dueMappingIDs,
		})
	}
	return jobs
}

func listUpstreamSourceAutoPriorityMappings(ctx context.Context, source model.UpstreamSource) ([]model.UpstreamSourceChannelMapping, error) {
	var mappings []model.UpstreamSourceChannelMapping
	if err := model.DB.WithContext(ctx).
		Select("id", "source_id", "sync_enabled", "local_channel_id", "upstream_group_name", "upstream_group_description", "upstream_platform", "discovery_status").
		Where("source_id = ?", source.Id).
		Order("id").
		Find(&mappings).Error; err != nil {
		return nil, err
	}
	return mappings, nil
}

func loadUpstreamSourceAutoPriorityChannels(ctx context.Context, mappings []model.UpstreamSourceChannelMapping) (map[int]model.Channel, error) {
	channelIDs := make([]int, 0, len(mappings))
	seen := make(map[int]struct{}, len(mappings))
	for _, mapping := range mappings {
		if mapping.LocalChannelID == 0 {
			continue
		}
		if _, ok := seen[mapping.LocalChannelID]; ok {
			continue
		}
		seen[mapping.LocalChannelID] = struct{}{}
		channelIDs = append(channelIDs, mapping.LocalChannelID)
	}

	channelsByID := make(map[int]model.Channel, len(channelIDs))
	if len(channelIDs) == 0 {
		return channelsByID, nil
	}

	var channels []model.Channel
	if err := model.DB.WithContext(ctx).
		Select("id", "settings").
		Where("id IN ?", channelIDs).
		Find(&channels).Error; err != nil {
		return nil, err
	}
	for _, channel := range channels {
		channelsByID[channel.Id] = channel
	}
	return channelsByID, nil
}

func (s *UpstreamSourceService) RunDueUpstreamSourceAutoPriority(ctx context.Context, now int64) []dto.UpstreamSourceAutoPriorityResult {
	jobs, err := listDueUpstreamSourceAutoPriorityJobs(ctx, now)
	if err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("upstream source auto-priority: list due jobs failed: %v", err))
		return nil
	}
	mappingIDs := make([]int, 0)
	for _, job := range jobs {
		mappingIDs = append(mappingIDs, job.mappingIDs...)
	}
	if len(mappingIDs) == 0 {
		return nil
	}

	var mappings []model.UpstreamSourceChannelMapping
	if err := model.DB.WithContext(ctx).
		Select("local_channel_id").
		Where("id IN ?", mappingIDs).
		Find(&mappings).Error; err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("upstream source auto-priority: load due mappings failed: %v", err))
		return nil
	}
	channelIDs := make([]int, 0, len(mappings))
	for _, mapping := range mappings {
		if mapping.LocalChannelID != 0 {
			channelIDs = append(channelIDs, mapping.LocalChannelID)
		}
	}
	if len(channelIDs) == 0 {
		return nil
	}

	var triggerChannels []model.Channel
	if err := model.DB.WithContext(ctx).
		Select("id", "group").
		Where("id IN ? AND status IN ?", channelIDs, []int{
			common.ChannelStatusEnabled,
			common.ChannelStatusAutoDisabled,
		}).
		Find(&triggerChannels).Error; err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("upstream source auto-priority: load due channels failed: %v", err))
		return nil
	}
	localGroups := make(map[string]struct{}, len(triggerChannels))
	for _, channel := range triggerChannels {
		localGroups[strings.TrimSpace(channel.Group)] = struct{}{}
	}
	if len(localGroups) == 0 {
		return nil
	}

	runResults, err := runChannelAutoPriority(ctx, now, localGroups, true)
	if err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("upstream source auto-priority: run due cohorts failed: %v", err))
		return nil
	}
	if len(runResults) == 0 {
		return nil
	}

	channelIDs = make([]int, 0, len(runResults))
	for _, runResult := range runResults {
		channelIDs = append(channelIDs, runResult.ChannelID)
	}
	var channels []model.Channel
	if err := model.DB.WithContext(ctx).
		Select("id", "settings").
		Where("id IN ?", channelIDs).
		Find(&channels).Error; err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("upstream source auto-priority: load completed channels failed: %v", err))
		return nil
	}
	settingsByChannelID := make(map[int]dto.ChannelOtherSettings, len(channels))
	for _, channel := range channels {
		settings, ok := readChannelOtherSettingsForAutoPriorityDue(channel)
		if !ok {
			continue
		}
		settingsByChannelID[channel.Id] = settings
	}

	resultsBySourceID := make(map[int]*dto.UpstreamSourceAutoPriorityResult)
	for _, runResult := range runResults {
		settings, ok := settingsByChannelID[runResult.ChannelID]
		if !ok || settings.GeneratedByUpstreamSourceID == 0 {
			continue
		}
		result := resultsBySourceID[settings.GeneratedByUpstreamSourceID]
		if result == nil {
			result = &dto.UpstreamSourceAutoPriorityResult{
				SourceID: settings.GeneratedByUpstreamSourceID,
				Results:  make([]dto.UpstreamSourceAutoPriorityChannelResult, 0),
			}
			resultsBySourceID[settings.GeneratedByUpstreamSourceID] = result
		}
		reason := runResult.Reason
		if reason == "" {
			reason = runResult.score.Reason
		}
		result.Results = append(result.Results, dto.UpstreamSourceAutoPriorityChannelResult{
			MappingID:               settings.GeneratedByUpstreamMappingID,
			LocalChannelID:          runResult.ChannelID,
			OldPriority:             runResult.score.OldPriority,
			NewPriority:             runResult.score.NewPriority,
			ComputedPriority:        runResult.score.ComputedPriority,
			Applied:                 runResult.Applied,
			Reason:                  reason,
			EffectiveRateMultiplier: runResult.score.EffectiveRateMultiplier,
			NominalRateMultiplier:   runResult.score.NominalRateMultiplier,
			CacheAdjustedCostFactor: runResult.score.CacheAdjustedCostFactor,
			EffectiveCostMultiplier: runResult.score.EffectiveCostMultiplier,
			EffectivePriceScore:     runResult.score.EffectivePriceScore,
			NominalPriceScore:       runResult.score.NominalPriceScore,
			CacheScore:              runResult.score.CacheScore,
			AvailabilityScore:       runResult.score.AvailabilityScore,
			FirstTokenScore:         runResult.score.FirstTokenScore,
			ThroughputScore:         runResult.score.ThroughputScore,
			FinalScore:              runResult.score.FinalScore,
		})
		switch {
		case runResult.Applied:
			result.Updated++
		case reason == "update_failed" || strings.HasSuffix(reason, "_failed"):
			result.Failed++
		default:
			result.Skipped++
		}
	}

	sourceIDs := make([]int, 0, len(resultsBySourceID))
	for sourceID := range resultsBySourceID {
		sourceIDs = append(sourceIDs, sourceID)
	}
	sort.Ints(sourceIDs)
	results := make([]dto.UpstreamSourceAutoPriorityResult, 0, len(sourceIDs))
	for _, sourceID := range sourceIDs {
		results = append(results, *resultsBySourceID[sourceID])
	}
	return results
}

func StartUpstreamSourceAutoPriorityWorker() {
	StartChannelAutoPriorityWorker()
}

func runDueUpstreamSourceAutoPriorityOnceRecovering() {
	runDueChannelAutoPriorityOnceRecovering()
}

func runDueUpstreamSourceAutoPriorityOnce() {
	runDueChannelAutoPriorityOnce()
}

func upstreamSourceMappingAutoPriorityDue(source model.UpstreamSource, config upstreamSourceSyncConfig, mapping *model.UpstreamSourceChannelMapping, channels map[int]model.Channel, now int64) bool {
	resolution := resolveUpstreamSourceRule(config, mapping)
	if !resolution.SyncEligible || !resolution.AutoPriorityEnabled {
		return false
	}
	if mapping == nil || mapping.LocalChannelID == 0 {
		return false
	}

	channel, ok := channels[mapping.LocalChannelID]
	if !ok {
		return false
	}
	settings, ok := readChannelOtherSettingsForAutoPriorityDue(channel)
	if !ok {
		logger.LogWarn(context.Background(), fmt.Sprintf("upstream source auto-priority: skip source_id=%d channel_id=%d invalid settings", source.Id, channel.Id))
		return false
	}
	if !isGeneratedChannelMetadataMatching(&settings, source.Id, mapping.Id) {
		return false
	}

	intervalMinutes := resolution.AutoPriorityIntervalMinutes
	if intervalMinutes == 0 {
		return true
	}
	lastRunAt := settings.ChannelAutoPriorityLastRunAt
	if lastRunAt == 0 {
		return true
	}
	return now-lastRunAt >= int64(intervalMinutes)*60
}

func readChannelOtherSettingsForAutoPriorityDue(channel model.Channel) (dto.ChannelOtherSettings, bool) {
	settings := dto.ChannelOtherSettings{}
	if strings.TrimSpace(channel.OtherSettings) == "" {
		return settings, true
	}
	if err := common.UnmarshalJsonStr(channel.OtherSettings, &settings); err != nil {
		return dto.ChannelOtherSettings{}, false
	}
	return settings, true
}
