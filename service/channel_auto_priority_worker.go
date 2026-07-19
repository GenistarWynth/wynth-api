package service

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"

	"github.com/bytedance/gopkg/util/gopool"
)

const channelAutoPriorityTickInterval = time.Minute

var (
	channelAutoPriorityOnce    sync.Once
	channelAutoPriorityRunning atomic.Bool
)

type channelAutoPriorityRunResult struct {
	ChannelID int
	Applied   bool
	Reason    string
}

func RunDueChannelAutoPriority(ctx context.Context, now int64) []channelAutoPriorityRunResult {
	if now == 0 {
		now = common.GetTimestamp()
	}

	var channels []model.Channel
	if err := model.DB.WithContext(ctx).
		Where("status = ?", common.ChannelStatusEnabled).
		Order("id").
		Find(&channels).Error; err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("channel auto-priority: list channels failed: %v", err))
		return nil
	}

	type manualAutoPriorityChannel struct {
		channel    model.Channel
		settings   dto.ChannelOtherSettings
		localGroup string
	}

	manualChannels := make([]manualAutoPriorityChannel, 0, len(channels))
	for _, channel := range channels {
		settings, ok := readChannelOtherSettingsForAutoPriorityDue(channel)
		if !ok {
			logger.LogWarn(ctx, fmt.Sprintf("channel auto-priority: skip channel_id=%d invalid settings", channel.Id))
			continue
		}
		if !settings.ChannelAutoPriorityEnabled {
			continue
		}
		if settings.GeneratedByUpstreamSourceID != 0 || settings.GeneratedByUpstreamMappingID != 0 {
			continue
		}
		localGroup := strings.TrimSpace(channel.Group)
		manualChannels = append(manualChannels, manualAutoPriorityChannel{
			channel:    channel,
			settings:   settings,
			localGroup: localGroup,
		})
	}
	availabilityGroups := make([]string, 0, len(manualChannels))
	availabilityGroupSet := make(map[string]struct{}, len(manualChannels))
	for _, configuredChannel := range manualChannels {
		if _, exists := availabilityGroupSet[configuredChannel.localGroup]; exists {
			continue
		}
		availabilityGroupSet[configuredChannel.localGroup] = struct{}{}
		availabilityGroups = append(availabilityGroups, configuredChannel.localGroup)
	}
	groupAvailabilityWindowHours, err := autoPriorityLocalGroupAvailabilityWindowHours(ctx, availabilityGroups)
	if err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("channel auto-priority: availability windows failed: %v", err))
		return nil
	}

	pending := make([]upstreamSourceAutoPriorityCandidate, 0, len(manualChannels))
	groupedPending := make(map[autoPriorityWindowKey][]int, len(manualChannels))
	for _, configuredChannel := range manualChannels {
		channel := configuredChannel.channel
		settings := configuredChannel.settings

		intervalMinutes := dto.NormalizeChannelAutoPriorityInterval(settings.ChannelAutoPriorityIntervalMinutes)
		if intervalMinutes > 0 && settings.ChannelAutoPriorityLastRunAt > 0 &&
			now-settings.ChannelAutoPriorityLastRunAt < int64(intervalMinutes)*60 {
			continue
		}

		windowHours := dto.NormalizeChannelAutoPriorityWindowHours(settings.ChannelAutoPriorityWindowHours)
		availabilityWindowHours := groupAvailabilityWindowHours[configuredChannel.localGroup]
		if availabilityWindowHours == 0 {
			availabilityWindowHours = dto.NormalizeChannelAutoPriorityWindowHours(
				settings.ChannelAutoPriorityAvailabilityWindowHours,
			)
		}

		rateMultiplier := settings.ChannelAutoPriorityRateMultiplier
		if !isValidAutoPriorityMultiplier(rateMultiplier) {
			rateMultiplier = 1
		}

		pending = append(pending, upstreamSourceAutoPriorityCandidate{
			channel:  channel,
			settings: settings,
			resolution: upstreamSourceRuleResolution{
				AutoPriorityEnabled:                 true,
				AutoPriorityIntervalMinutes:         intervalMinutes,
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
	}
	if len(pending) == 0 {
		return nil
	}

	distinctGroups := make(map[string]struct{}, len(pending))
	distinctTypes := make(map[int]struct{}, len(pending))
	groups := make([]string, 0, len(pending))
	types := make([]int, 0, len(pending))
	for i := range pending {
		group := pending[i].scoreInput.LocalGroup
		if _, ok := distinctGroups[group]; !ok {
			distinctGroups[group] = struct{}{}
			groups = append(groups, group)
		}
		channelType := pending[i].scoreInput.ChannelType
		if _, ok := distinctTypes[channelType]; !ok {
			distinctTypes[channelType] = struct{}{}
			types = append(types, channelType)
		}
	}
	if bounds, err := autoPriorityLocalGroupCostBounds(ctx, groups, types); err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("channel auto-priority: cost bounds failed: %v", err))
	} else {
		for i := range pending {
			cohort := autoPriorityCohortKey(pending[i].scoreInput.LocalGroup, pending[i].scoreInput.ChannelType)
			if bound, ok := bounds[cohort]; ok {
				pending[i].scoreInput.CohortCostFloor = bound[0]
				pending[i].scoreInput.CohortCostCeil = bound[1]
			}
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

	scoreResults := ScoreAutoPriorityCandidates(scoreInputs, 1000)
	results := make([]channelAutoPriorityRunResult, 0, len(scoreResults))
	appliedAny := false

	for i := range scoreResults {
		if reason, failed := failedReasons[i]; failed {
			results = append(results, channelAutoPriorityRunResult{
				ChannelID: pending[i].channel.Id,
				Applied:   false,
				Reason:    reason,
			})
			continue
		}
		score := scoreResults[i]
		candidate := pending[i]
		reason, err := persistAutoPriorityCandidate(ctx, candidate, score, now)
		if err != nil {
			logger.LogWarn(ctx, fmt.Sprintf("channel auto-priority: persist channel_id=%d failed: %v", candidate.channel.Id, err))
			results = append(results, channelAutoPriorityRunResult{
				ChannelID: candidate.channel.Id,
				Applied:   false,
				Reason:    "update_failed",
			})
			continue
		}
		if score.Applied && reason == "" {
			appliedAny = true
		}
		results = append(results, channelAutoPriorityRunResult{
			ChannelID: candidate.channel.Id,
			Applied:   score.Applied && reason == "",
			Reason:    reason,
		})
	}

	if appliedAny {
		initChannelCacheAfterAutoPriority(ctx)
	}

	return results
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
