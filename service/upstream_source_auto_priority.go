package service

import (
	"context"
	"errors"
	"strings"

	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"

	"gorm.io/gorm"
)

const upstreamSourceAutoPriorityScoreVersion = "v1"

type upstreamSourceAutoPriorityCandidate struct {
	mapping     model.UpstreamSourceChannelMapping
	channel     model.Channel
	settings    dto.ChannelOtherSettings
	resolution  upstreamSourceRuleResolution
	scoreInput  AutoPriorityScoreInput
	windowStart int64
	windowEnd   int64
	resultIndex int
}

func (s *UpstreamSourceService) RunAutoPriority(ctx context.Context, sourceID int, now int64) (*dto.UpstreamSourceAutoPriorityResult, error) {
	if sourceID == 0 {
		return nil, errors.New("source ID is required")
	}
	if now == 0 {
		now = s.now()
	}

	var source model.UpstreamSource
	if err := model.DB.WithContext(ctx).
		Where("id = ? AND status = ? AND status <> ?", sourceID, model.UpstreamSourceStatusEnabled, model.UpstreamSourceStatusDeleted).
		First(&source).Error; err != nil {
		return nil, err
	}

	config, err := parseUpstreamSourceSyncConfig(source.SyncConfig)
	if err != nil {
		return nil, err
	}

	var mappings []model.UpstreamSourceChannelMapping
	if err := model.DB.WithContext(ctx).Where("source_id = ?", sourceID).Order("id").Find(&mappings).Error; err != nil {
		return nil, err
	}

	result := &dto.UpstreamSourceAutoPriorityResult{
		SourceID: sourceID,
		Results:  make([]dto.UpstreamSourceAutoPriorityChannelResult, 0, len(mappings)),
	}
	if len(mappings) == 0 {
		return result, nil
	}

	pending := make([]upstreamSourceAutoPriorityCandidate, 0, len(mappings))
	groupedPending := make(map[int64][]int, len(mappings))
	resultSlots := make([]*dto.UpstreamSourceAutoPriorityChannelResult, len(mappings))

	for i := range mappings {
		mapping := mappings[i]
		resolution := resolveUpstreamSourceRule(config, &mapping)
		if !resolution.SyncEligible || !resolution.AutoPriorityEnabled {
			result.Skipped++
			continue
		}

		if mapping.EffectiveRateMultiplier == nil || *mapping.EffectiveRateMultiplier <= 0 {
			slot := dto.UpstreamSourceAutoPriorityChannelResult{
				MappingID:      mapping.Id,
				LocalChannelID: mapping.LocalChannelID,
				OldPriority:    0,
				NewPriority:    0,
				Applied:        false,
				Reason:         "missing_effective_rate_multiplier",
			}
			resultSlots[i] = &slot
			result.Skipped++
			continue
		}

		if mapping.LocalChannelID == 0 {
			slot := dto.UpstreamSourceAutoPriorityChannelResult{
				MappingID:      mapping.Id,
				LocalChannelID: 0,
				OldPriority:    0,
				NewPriority:    0,
				Applied:        false,
				Reason:         "local_channel_missing",
			}
			resultSlots[i] = &slot
			result.Skipped++
			continue
		}

		channel, err := loadChannelByID(mapping.LocalChannelID)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				slot := dto.UpstreamSourceAutoPriorityChannelResult{
					MappingID:      mapping.Id,
					LocalChannelID: mapping.LocalChannelID,
					OldPriority:    0,
					NewPriority:    0,
					Applied:        false,
					Reason:         "local_channel_missing",
				}
				resultSlots[i] = &slot
				result.Skipped++
				continue
			}
			result.Failed++
			if result.Error == "" {
				result.Error = SanitizeUpstreamSourceError(err)
			}
			return result, err
		}

		settings := channel.GetOtherSettings()
		if !isGeneratedChannelMetadataMatching(&settings, source.Id, mapping.Id) {
			slot := dto.UpstreamSourceAutoPriorityChannelResult{
				MappingID:      mapping.Id,
				LocalChannelID: channel.Id,
				OldPriority:    channel.GetPriority(),
				NewPriority:    channel.GetPriority(),
				Applied:        false,
				Reason:         "generated_channel_metadata_mismatch",
			}
			resultSlots[i] = &slot
			result.Skipped++
			continue
		}

		windowHours := resolution.AutoPriorityWindowHours
		if windowHours <= 0 {
			windowHours = 1
		}
		windowStart := now - int64(windowHours)*3600
		priority := channel.GetPriority()
		localGroup := strings.TrimSpace(channel.Group)
		if localGroup == "" {
			localGroup = strings.TrimSpace(resolution.LocalGroup)
		}

		pending = append(pending, upstreamSourceAutoPriorityCandidate{
			mapping:     mapping,
			channel:     *channel,
			settings:    settings,
			resolution:  resolution,
			resultIndex: i,
			scoreInput: AutoPriorityScoreInput{
				ChannelID:               channel.Id,
				LocalGroup:              localGroup,
				ChannelType:             channel.Type,
				CurrentPriority:         priority,
				EffectiveRateMultiplier: *mapping.EffectiveRateMultiplier,
				CacheAdjustedCostFactor: 1,
				Availability:            nil,
				FirstTokenLatencyMS:     0,
				UsageLogCount:           0,
				MonitorCheckCount:       0,
				FirstTokenSampleCount:   0,
				HasPreviousSnapshot:     settings.ChannelAutoPriorityLastScore != nil,
			},
			windowStart: windowStart,
			windowEnd:   now,
		})
		groupedPending[windowStart] = append(groupedPending[windowStart], len(pending)-1)
	}

	scoreInputs := make([]AutoPriorityScoreInput, len(pending))
	for windowStart, indexes := range groupedPending {
		channelIDList := make([]int, 0, len(indexes))
		for _, idx := range indexes {
			channelIDList = append(channelIDList, pending[idx].channel.Id)
		}

		monitorStats, err := model.GetChannelMonitorStats(channelIDList, windowStart)
		if err != nil {
			result.Error = SanitizeUpstreamSourceError(err)
			return result, err
		}
		usageStats, err := CollectAutoPriorityUsageStats(channelIDList, windowStart)
		if err != nil {
			result.Error = SanitizeUpstreamSourceError(err)
			return result, err
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
			}
			scoreInputs[idx] = scoreInput
		}
	}

	scoreResults := ScoreAutoPriorityCandidates(scoreInputs, 1000)
	appliedAny := false

	for i := range scoreResults {
		score := scoreResults[i]
		candidate := pending[i]
		candidateResult := dto.UpstreamSourceAutoPriorityChannelResult{
			MappingID:               candidate.mapping.Id,
			LocalChannelID:          candidate.channel.Id,
			OldPriority:             score.OldPriority,
			NewPriority:             score.NewPriority,
			ComputedPriority:        score.ComputedPriority,
			Applied:                 score.Applied,
			Reason:                  score.Reason,
			EffectiveRateMultiplier: score.EffectiveRateMultiplier,
			CacheAdjustedCostFactor: score.CacheAdjustedCostFactor,
			EffectiveCostMultiplier: score.EffectiveCostMultiplier,
			EffectivePriceScore:     score.EffectivePriceScore,
			AvailabilityScore:       score.AvailabilityScore,
			FirstTokenScore:         score.FirstTokenScore,
			FinalScore:              score.FinalScore,
		}

		candidate.settings.ChannelAutoPriorityEnabled = candidate.resolution.AutoPriorityEnabled
		candidate.settings.ChannelAutoPriorityIntervalMinutes = candidate.resolution.AutoPriorityIntervalMinutes
		candidate.settings.ChannelAutoPriorityWindowHours = candidate.resolution.AutoPriorityWindowHours
		candidate.settings.ChannelAutoPriorityLastRunAt = now
		candidate.settings.ChannelAutoPriorityLastScore = buildChannelAutoPriorityScoreSnapshot(score, candidate.windowStart, candidate.windowEnd)

		if score.Applied {
			candidate.settings.ChannelAutoPriorityLastAppliedAt = now
			channelSettings := candidate.channel
			channelSettings.SetOtherSettings(candidate.settings)
			txErr := model.DB.Transaction(func(tx *gorm.DB) error {
				if err := tx.Model(&model.Channel{}).Where("id = ?", candidate.channel.Id).Updates(map[string]any{
					"priority": score.NewPriority,
					"settings": channelSettings.OtherSettings,
				}).Error; err != nil {
					return err
				}
				if err := tx.Model(&model.Ability{}).Where("channel_id = ?", candidate.channel.Id).Update("priority", score.NewPriority).Error; err != nil {
					return err
				}
				return nil
			})
			if txErr != nil {
				result.Failed++
				if result.Error == "" {
					result.Error = SanitizeUpstreamSourceError(txErr)
				}
				candidateResult.Applied = false
				candidateResult.Reason = "update_failed"
				resultSlots[candidate.resultIndex] = &candidateResult
				continue
			}
			result.Updated++
			appliedAny = true
		} else {
			channelSettings := candidate.channel
			channelSettings.SetOtherSettings(candidate.settings)
			txErr := model.DB.Transaction(func(tx *gorm.DB) error {
				return tx.Model(&model.Channel{}).Where("id = ?", candidate.channel.Id).Update("settings", channelSettings.OtherSettings).Error
			})
			if txErr != nil {
				result.Failed++
				if result.Error == "" {
					result.Error = SanitizeUpstreamSourceError(txErr)
				}
				candidateResult.Applied = false
				candidateResult.Reason = "update_failed"
				resultSlots[candidate.resultIndex] = &candidateResult
				continue
			}
			result.Skipped++
		}
		resultSlots[candidate.resultIndex] = &candidateResult
	}

	if appliedAny {
		model.InitChannelCache()
	}

	for _, slot := range resultSlots {
		if slot != nil {
			result.Results = append(result.Results, *slot)
		}
	}

	return result, nil
}

func isGeneratedChannelMetadataMatching(settings *dto.ChannelOtherSettings, sourceID int, mappingID int) bool {
	if settings == nil {
		return false
	}
	return settings.GeneratedByUpstreamSourceID == sourceID &&
		settings.GeneratedByUpstreamMappingID == mappingID
}

func buildChannelAutoPriorityScoreSnapshot(score AutoPriorityScoreResult, windowStart int64, windowEnd int64) *dto.ChannelAutoPriorityScore {
	return &dto.ChannelAutoPriorityScore{
		Version:                 upstreamSourceAutoPriorityScoreVersion,
		ComputedAt:              windowEnd,
		WindowStart:             windowStart,
		WindowEnd:               windowEnd,
		Cohort:                  score.Cohort,
		EffectiveRateMultiplier: score.EffectiveRateMultiplier,
		CacheAdjustedCostFactor: score.CacheAdjustedCostFactor,
		EffectiveCostMultiplier: score.EffectiveCostMultiplier,
		EffectivePriceScore:     score.EffectivePriceScore,
		AvailabilityScore:       score.AvailabilityScore,
		FirstTokenScore:         score.FirstTokenScore,
		FinalScore:              score.FinalScore,
		OldPriority:             score.OldPriority,
		NewPriority:             score.NewPriority,
		Applied:                 score.Applied,
		Reason:                  score.Reason,
		UsageLogCount:           score.UsageLogCount,
		MonitorCheckCount:       score.MonitorCheckCount,
		FirstTokenSampleCount:   score.FirstTokenSampleCount,
	}
}
