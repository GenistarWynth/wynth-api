package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"

	"github.com/bytedance/gopkg/util/gopool"
	"gorm.io/gorm"
)

const channelAutoPriorityTickInterval = time.Minute

var (
	channelAutoPriorityOnce    sync.Once
	channelAutoPriorityRunning atomic.Bool
)

type ChannelAutoPriorityRunResult struct {
	ChannelID int    `json:"channel_id"`
	Applied   bool   `json:"applied"`
	Reason    string `json:"reason,omitempty"`
	score     AutoPriorityScoreResult
}

func RunDueChannelAutoPriority(ctx context.Context, now int64) []ChannelAutoPriorityRunResult {
	results, err := runChannelAutoPriority(ctx, now, nil, false)
	if err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("channel auto-priority: run due groups failed: %v", err))
		return nil
	}
	return results
}

func RunChannelAutoPriorityGroup(ctx context.Context, channelID int, now int64) ([]ChannelAutoPriorityRunResult, error) {
	var channel model.Channel
	if err := model.DB.WithContext(ctx).Select("id", "group").First(&channel, channelID).Error; err != nil {
		return nil, err
	}
	localGroup := strings.TrimSpace(channel.Group)
	results, err := runChannelAutoPriority(ctx, now, map[string]struct{}{localGroup: {}}, true)
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("local group %q has no enabled auto-priority channels", localGroup)
	}
	return results, nil
}

func RunChannelAutoPriorityGroupsForSource(ctx context.Context, sourceID int, now int64) (*dto.UpstreamSourceAutoPriorityResult, error) {
	if sourceID == 0 {
		return nil, fmt.Errorf("source ID is required")
	}
	var source model.UpstreamSource
	if err := model.DB.WithContext(ctx).
		Where("id = ? AND status = ?", sourceID, model.UpstreamSourceStatusEnabled).
		First(&source).Error; err != nil {
		return nil, err
	}
	config, err := parseUpstreamSourceSyncConfig(source.SyncConfig)
	if err != nil {
		return nil, err
	}

	var mappings []model.UpstreamSourceChannelMapping
	if err := model.DB.WithContext(ctx).
		Where("source_id = ?", sourceID).
		Order("id").
		Find(&mappings).Error; err != nil {
		return nil, err
	}
	result := &dto.UpstreamSourceAutoPriorityResult{
		SourceID: sourceID,
		Results:  make([]dto.UpstreamSourceAutoPriorityChannelResult, 0, len(mappings)),
	}
	if len(mappings) == 0 {
		return result, nil
	}

	channelIDs := make([]int, 0, len(mappings))
	mappingIDByChannelID := make(map[int]int, len(mappings))
	for _, mapping := range mappings {
		resolution := resolveUpstreamSourceRule(config, &mapping)
		if mapping.LocalChannelID == 0 || !resolution.SyncEligible || !resolution.AutoPriorityEnabled {
			result.Skipped++
			continue
		}
		channelIDs = append(channelIDs, mapping.LocalChannelID)
		mappingIDByChannelID[mapping.LocalChannelID] = mapping.Id
	}
	if len(channelIDs) == 0 {
		return result, nil
	}

	var channels []model.Channel
	if err := model.DB.WithContext(ctx).
		Select("id", "group", "settings").
		Where("id IN ? AND status = ?", channelIDs, common.ChannelStatusEnabled).
		Order("id").
		Find(&channels).Error; err != nil {
		return nil, err
	}
	localGroups := make(map[string]struct{}, len(channels))
	for _, channel := range channels {
		localGroups[strings.TrimSpace(channel.Group)] = struct{}{}
	}
	result.Skipped += len(channelIDs) - len(channels)
	if len(localGroups) == 0 {
		return result, nil
	}

	runResults, err := runChannelAutoPriority(ctx, now, localGroups, true)
	if err != nil {
		return nil, err
	}
	for _, runResult := range runResults {
		reason := runResult.Reason
		if reason == "" {
			reason = runResult.score.Reason
		}
		channelResult := dto.UpstreamSourceAutoPriorityChannelResult{
			MappingID:               mappingIDByChannelID[runResult.ChannelID],
			LocalChannelID:          runResult.ChannelID,
			OldPriority:             runResult.score.OldPriority,
			NewPriority:             runResult.score.NewPriority,
			ComputedPriority:        runResult.score.ComputedPriority,
			Applied:                 runResult.Applied,
			Reason:                  reason,
			EffectiveRateMultiplier: runResult.score.EffectiveRateMultiplier,
			CacheAdjustedCostFactor: runResult.score.CacheAdjustedCostFactor,
			EffectiveCostMultiplier: runResult.score.EffectiveCostMultiplier,
			EffectivePriceScore:     runResult.score.EffectivePriceScore,
			AvailabilityScore:       runResult.score.AvailabilityScore,
			FirstTokenScore:         runResult.score.FirstTokenScore,
			ThroughputScore:         runResult.score.ThroughputScore,
			FinalScore:              runResult.score.FinalScore,
		}
		switch {
		case runResult.Applied:
			result.Updated++
		case runResult.Reason == "update_failed" || strings.HasSuffix(runResult.Reason, "_failed"):
			result.Failed++
		default:
			result.Skipped++
		}
		result.Results = append(result.Results, channelResult)
	}
	return result, nil
}

type configuredAutoPriorityChannel struct {
	channel        model.Channel
	settings       dto.ChannelOtherSettings
	localGroup     string
	rateMultiplier float64
	invalidReason  string
}

type generatedAutoPriorityState struct {
	sourceID            int
	localChannelID      int
	ruleResolved        bool
	autoPriorityEnabled bool
	rateMultiplier      float64
	rateValid           bool
}

func loadGeneratedAutoPriorityStates(ctx context.Context, mappingIDs []int) (map[int]generatedAutoPriorityState, error) {
	states := make(map[int]generatedAutoPriorityState, len(mappingIDs))
	if len(mappingIDs) == 0 {
		return states, nil
	}

	var mappings []model.UpstreamSourceChannelMapping
	if err := model.DB.WithContext(ctx).
		Where("id IN ?", mappingIDs).
		Find(&mappings).Error; err != nil {
		return nil, err
	}
	sourceIDs := make([]int, 0, len(mappings))
	sourceIDSet := make(map[int]struct{}, len(mappings))
	for _, mapping := range mappings {
		if _, exists := sourceIDSet[mapping.SourceID]; exists {
			continue
		}
		sourceIDSet[mapping.SourceID] = struct{}{}
		sourceIDs = append(sourceIDs, mapping.SourceID)
	}

	var sources []model.UpstreamSource
	if len(sourceIDs) > 0 {
		if err := model.DB.WithContext(ctx).
			Select("id", "status", "sync_config").
			Where("id IN ?", sourceIDs).
			Find(&sources).Error; err != nil {
			return nil, err
		}
	}
	sourcesByID := make(map[int]model.UpstreamSource, len(sources))
	configsBySourceID := make(map[int]upstreamSourceSyncConfig, len(sources))
	for _, source := range sources {
		sourcesByID[source.Id] = source
		if source.Status != model.UpstreamSourceStatusEnabled {
			continue
		}
		config, err := parseUpstreamSourceSyncConfig(source.SyncConfig)
		if err != nil {
			logger.LogWarn(ctx, fmt.Sprintf("channel auto-priority: parse source_id=%d rules failed: %v", source.Id, err))
			continue
		}
		configsBySourceID[source.Id] = config
	}

	for i := range mappings {
		mapping := mappings[i]
		state := generatedAutoPriorityState{
			sourceID:       mapping.SourceID,
			localChannelID: mapping.LocalChannelID,
		}
		source, sourceExists := sourcesByID[mapping.SourceID]
		if sourceExists && source.Status != model.UpstreamSourceStatusEnabled {
			state.ruleResolved = true
			states[mapping.Id] = state
			continue
		}
		config, configExists := configsBySourceID[mapping.SourceID]
		if !configExists {
			states[mapping.Id] = state
			continue
		}
		resolution := resolveUpstreamSourceRule(config, &mapping)
		state.ruleResolved = true
		state.autoPriorityEnabled = resolution.SyncEligible && resolution.AutoPriorityEnabled
		if mapping.EffectiveRateMultiplier != nil && isValidAutoPriorityMultiplier(*mapping.EffectiveRateMultiplier) {
			state.rateMultiplier = *mapping.EffectiveRateMultiplier
			state.rateValid = true
		}
		states[mapping.Id] = state
	}
	return states, nil
}

type autoPriorityGroupSchedule struct {
	intervalMinutes int
	lastRunAt       int64
	due             bool
	members         []configuredAutoPriorityChannel
}

func runChannelAutoPriority(ctx context.Context, now int64, localGroupFilter map[string]struct{}, force bool) ([]ChannelAutoPriorityRunResult, error) {
	if now == 0 {
		now = common.GetTimestamp()
	}

	var channels []model.Channel
	if err := model.DB.WithContext(ctx).
		Where("status = ?", common.ChannelStatusEnabled).
		Order("id").
		Find(&channels).Error; err != nil {
		return nil, err
	}

	type channelWithSettings struct {
		channel    model.Channel
		settings   dto.ChannelOtherSettings
		localGroup string
	}
	channelsWithSettings := make([]channelWithSettings, 0, len(channels))
	mappingIDs := make([]int, 0, len(channels))
	for _, channel := range channels {
		settings, ok := readChannelOtherSettingsForAutoPriorityDue(channel)
		if !ok {
			logger.LogWarn(ctx, fmt.Sprintf("channel auto-priority: skip channel_id=%d invalid settings", channel.Id))
			continue
		}
		localGroup := strings.TrimSpace(channel.Group)
		if localGroupFilter != nil {
			if _, ok := localGroupFilter[localGroup]; !ok {
				continue
			}
		}
		channelsWithSettings = append(channelsWithSettings, channelWithSettings{
			channel:    channel,
			settings:   settings,
			localGroup: localGroup,
		})
		if settings.GeneratedByUpstreamMappingID != 0 {
			mappingIDs = append(mappingIDs, settings.GeneratedByUpstreamMappingID)
		}
	}
	if len(channelsWithSettings) == 0 {
		return nil, nil
	}

	generatedStates, err := loadGeneratedAutoPriorityStates(ctx, mappingIDs)
	if err != nil {
		return nil, err
	}
	configuredChannels := make([]configuredAutoPriorityChannel, 0, len(channelsWithSettings))
	for _, configured := range channelsWithSettings {
		settings := configured.settings
		generated := settings.GeneratedByUpstreamSourceID != 0 || settings.GeneratedByUpstreamMappingID != 0
		if !generated {
			if !settings.ChannelAutoPriorityEnabled {
				continue
			}
			rateMultiplier := settings.ChannelAutoPriorityRateMultiplier
			if !isValidAutoPriorityMultiplier(rateMultiplier) {
				rateMultiplier = 1
			}
			configuredChannels = append(configuredChannels, configuredAutoPriorityChannel{
				channel:        configured.channel,
				settings:       settings,
				localGroup:     configured.localGroup,
				rateMultiplier: rateMultiplier,
			})
			continue
		}

		state, stateExists := generatedStates[settings.GeneratedByUpstreamMappingID]
		if stateExists && state.ruleResolved && !state.autoPriorityEnabled {
			continue
		}
		if (!stateExists || !state.ruleResolved) && !settings.ChannelAutoPriorityEnabled {
			continue
		}
		settings.ChannelAutoPriorityEnabled = true
		invalidReason := ""
		if !stateExists || !state.ruleResolved {
			invalidReason = "upstream_rule_resolution_failed"
		} else if state.localChannelID != configured.channel.Id ||
			(settings.GeneratedByUpstreamSourceID != 0 && state.sourceID != settings.GeneratedByUpstreamSourceID) ||
			!state.rateValid {
			invalidReason = "missing_effective_rate_multiplier"
		}
		configuredChannels = append(configuredChannels, configuredAutoPriorityChannel{
			channel:        configured.channel,
			settings:       settings,
			localGroup:     configured.localGroup,
			rateMultiplier: state.rateMultiplier,
			invalidReason:  invalidReason,
		})
	}
	if len(configuredChannels) == 0 {
		return nil, nil
	}

	groupSchedules := make(map[string]*autoPriorityGroupSchedule)
	for _, configuredChannel := range configuredChannels {
		schedule := groupSchedules[configuredChannel.localGroup]
		if schedule == nil {
			schedule = &autoPriorityGroupSchedule{lastRunAt: configuredChannel.settings.ChannelAutoPriorityLastRunAt}
			groupSchedules[configuredChannel.localGroup] = schedule
		} else if schedule.lastRunAt != 0 && (configuredChannel.settings.ChannelAutoPriorityLastRunAt == 0 || configuredChannel.settings.ChannelAutoPriorityLastRunAt < schedule.lastRunAt) {
			schedule.lastRunAt = configuredChannel.settings.ChannelAutoPriorityLastRunAt
		}
		intervalMinutes := dto.NormalizeChannelAutoPriorityInterval(configuredChannel.settings.ChannelAutoPriorityIntervalMinutes)
		if intervalMinutes > schedule.intervalMinutes {
			schedule.intervalMinutes = intervalMinutes
		}
		lastRunAt := configuredChannel.settings.ChannelAutoPriorityLastRunAt
		if intervalMinutes == 0 || lastRunAt == 0 || now-lastRunAt >= int64(intervalMinutes)*60 {
			schedule.due = true
		}
		schedule.members = append(schedule.members, configuredChannel)
	}

	selectedGroups := make([]string, 0, len(groupSchedules))
	for localGroup, schedule := range groupSchedules {
		// The group keeps the earliest legacy timestamp; a never-run member
		// therefore makes the whole group due even if every peer is recent.
		if schedule.lastRunAt == 0 {
			schedule.due = true
		}
		if !force && !schedule.due {
			continue
		}
		selectedGroups = append(selectedGroups, localGroup)
	}
	if len(selectedGroups) == 0 {
		return nil, nil
	}
	sort.Strings(selectedGroups)

	groupAvailabilityWindowHours := make(map[string]int, len(selectedGroups))
	for _, configuredChannel := range configuredChannels {
		windowHours := dto.NormalizeChannelAutoPriorityWindowHours(
			configuredChannel.settings.ChannelAutoPriorityAvailabilityWindowHours,
		)
		if windowHours > groupAvailabilityWindowHours[configuredChannel.localGroup] {
			groupAvailabilityWindowHours[configuredChannel.localGroup] = windowHours
		}
	}

	pending := make([]upstreamSourceAutoPriorityCandidate, 0, len(configuredChannels))
	groupedPending := make(map[autoPriorityWindowKey][]int, len(configuredChannels))
	pendingIndexesByGroup := make(map[string][]int, len(selectedGroups))
	invalidGroups := make(map[string]string)
	for _, localGroup := range selectedGroups {
		schedule := groupSchedules[localGroup]
		for _, configuredChannel := range schedule.members {
			channel := configuredChannel.channel
			settings := configuredChannel.settings
			windowHours := dto.NormalizeChannelAutoPriorityWindowHours(settings.ChannelAutoPriorityWindowHours)
			availabilityWindowHours := groupAvailabilityWindowHours[configuredChannel.localGroup]
			if availabilityWindowHours == 0 {
				availabilityWindowHours = dto.NormalizeChannelAutoPriorityWindowHours(
					settings.ChannelAutoPriorityAvailabilityWindowHours,
				)
			}

			rateMultiplier := configuredChannel.rateMultiplier
			if configuredChannel.invalidReason != "" {
				rateMultiplier = 1
				invalidGroups[localGroup] = configuredChannel.invalidReason
			}

			pending = append(pending, upstreamSourceAutoPriorityCandidate{
				channel:  channel,
				settings: settings,
				resolution: upstreamSourceRuleResolution{
					AutoPriorityEnabled:                 true,
					AutoPriorityIntervalMinutes:         schedule.intervalMinutes,
					AutoPriorityWindowHours:             windowHours,
					AutoPriorityAvailabilityWindowHours: availabilityWindowHours,
				},
				scoreInput: AutoPriorityScoreInput{
					ChannelID:                       channel.Id,
					LocalGroup:                      configuredChannel.localGroup,
					ChannelType:                     channel.Type,
					CurrentPriority:                 channel.GetPriority(),
					EffectiveRateMultiplier:         rateMultiplier,
					CacheAdjustedCostFactor:         1,
					PreviousEffectiveCostMultiplier: previousAutoPriorityEffectiveCostMultiplier(settings),
					HasPreviousSnapshot:             settings.ChannelAutoPriorityLastScore != nil,
				},
				windowStart:             now - int64(windowHours)*3600,
				availabilityWindowStart: now - int64(availabilityWindowHours)*3600,
				windowEnd:               now,
			})
			windowKey := autoPriorityWindowKey{
				usageWindowStart:        now - int64(windowHours)*3600,
				availabilityWindowStart: now - int64(availabilityWindowHours)*3600,
			}
			groupedPending[windowKey] = append(groupedPending[windowKey], len(pending)-1)
			pendingIndexesByGroup[localGroup] = append(pendingIndexesByGroup[localGroup], len(pending)-1)
		}
	}

	scoreInputs := make([]AutoPriorityScoreInput, len(pending))
	failedReasons := make(map[int]string)
	for windowKey, indexes := range groupedPending {
		channelIDs := make([]int, 0, len(indexes))
		for _, idx := range indexes {
			channelIDs = append(channelIDs, pending[idx].channel.Id)
		}
		monitorStats, err := model.GetChannelMonitorStatsWithContext(ctx, channelIDs, windowKey.availabilityWindowStart)
		if err != nil {
			logger.LogWarn(ctx, fmt.Sprintf("channel auto-priority: monitor stats failed: %v", err))
			for _, idx := range indexes {
				failedReasons[idx] = "monitor_stats_failed"
			}
			continue
		}
		usageStats, err := CollectAutoPriorityUsageStatsWithContext(ctx, channelIDs, windowKey.usageWindowStart)
		if err != nil {
			logger.LogWarn(ctx, fmt.Sprintf("channel auto-priority: usage stats failed: %v", err))
			for _, idx := range indexes {
				failedReasons[idx] = "usage_stats_failed"
			}
			continue
		}
		for _, idx := range indexes {
			channelID := pending[idx].channel.Id
			scoreInput := pending[idx].scoreInput
			if stat, ok := monitorStats[channelID]; ok {
				scoreInput.Availability = stat.Availability
				scoreInput.MonitorCheckCount = stat.TotalChecks
			}
			if stat, ok := usageStats[channelID]; ok {
				scoreInput.CacheAdjustedCostFactor = stat.CacheAdjustedCostFactor
				scoreInput.UsageLogCount = stat.UsageLogCount
				scoreInput.FirstTokenSampleCount = stat.FirstTokenSampleCount
				if stat.FirstTokenSampleCount > 0 {
					scoreInput.FirstTokenLatencyMS = float64(stat.AverageFirstTokenLatencyMS)
				}
				scoreInput.ThroughputSampleCount = stat.ThroughputSampleCount
				if stat.ThroughputSampleCount > 0 {
					scoreInput.ThroughputTps = stat.AverageThroughputTps
				}
			}
			scoreInputs[idx] = scoreInput
		}
	}
	for i := range pending {
		if reason := invalidGroups[pending[i].scoreInput.LocalGroup]; reason != "" {
			failedReasons[i] = reason
		}
	}
	failedGroups := make(map[string]string)
	for idx, reason := range failedReasons {
		failedGroups[pending[idx].scoreInput.LocalGroup] = reason
	}
	for i := range pending {
		if reason := failedGroups[pending[i].scoreInput.LocalGroup]; reason != "" {
			failedReasons[i] = reason
		}
	}

	scoreResults := ScoreAutoPriorityCandidates(scoreInputs, 1000)
	results := make([]ChannelAutoPriorityRunResult, 0, len(scoreResults))
	appliedAny := false

	for _, localGroup := range selectedGroups {
		indexes := pendingIndexesByGroup[localGroup]
		if len(indexes) == 0 {
			continue
		}
		if reason, failed := failedReasons[indexes[0]]; failed {
			for _, idx := range indexes {
				results = append(results, ChannelAutoPriorityRunResult{
					ChannelID: pending[idx].channel.Id,
					Applied:   false,
					Reason:    reason,
				})
			}
			continue
		}

		reason, err := persistChannelAutoPriorityGroup(ctx, pending, scoreResults, indexes, now)
		if err != nil {
			logger.LogWarn(ctx, fmt.Sprintf("channel auto-priority: persist group=%q failed: %v", localGroup, err))
			reason = "update_failed"
		}
		if reason != "" {
			for _, idx := range indexes {
				results = append(results, ChannelAutoPriorityRunResult{
					ChannelID: pending[idx].channel.Id,
					Applied:   false,
					Reason:    reason,
					score:     scoreResults[idx],
				})
			}
			continue
		}
		for _, idx := range indexes {
			score := scoreResults[idx]
			if score.Applied {
				appliedAny = true
			}
			results = append(results, ChannelAutoPriorityRunResult{
				ChannelID: pending[idx].channel.Id,
				Applied:   score.Applied,
				score:     score,
			})
		}
	}

	if appliedAny {
		initChannelCacheAfterAutoPriority(ctx)
	}

	return results, nil
}

func persistChannelAutoPriorityGroup(
	ctx context.Context,
	candidates []upstreamSourceAutoPriorityCandidate,
	scores []AutoPriorityScoreResult,
	indexes []int,
	now int64,
) (string, error) {
	err := model.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, idx := range indexes {
			if err := updateAutoPriorityCandidate(tx, candidates[idx], scores[idx], now); err != nil {
				return err
			}
		}
		return nil
	})
	if err == nil {
		return "", nil
	}
	if errors.Is(err, errAutoPriorityGeneratedChannelChanged) {
		return "generated_channel_changed", nil
	}
	return "", err
}

func StartChannelAutoPriorityWorker() {
	channelAutoPriorityOnce.Do(func() {
		if !common.IsMasterNode {
			return
		}
		gopool.Go(func() {
			logger.LogInfo(context.Background(), fmt.Sprintf("channel auto-priority worker started: tick=%s", channelAutoPriorityTickInterval))
			ticker := time.NewTicker(channelAutoPriorityTickInterval)
			defer ticker.Stop()

			runDueChannelAutoPriorityOnceRecovering()
			for range ticker.C {
				runDueChannelAutoPriorityOnceRecovering()
			}
		})
	})
}

func runDueChannelAutoPriorityOnceRecovering() {
	defer func() {
		if r := recover(); r != nil {
			logger.LogWarn(context.Background(), fmt.Sprintf("channel auto-priority: worker tick panic: %v", r))
		}
	}()
	runDueChannelAutoPriorityOnce()
}

func runDueChannelAutoPriorityOnce() {
	if !channelAutoPriorityRunning.CompareAndSwap(false, true) {
		return
	}
	defer channelAutoPriorityRunning.Store(false)

	RunDueChannelAutoPriority(context.Background(), common.GetTimestamp())
}
