package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"

	"gorm.io/gorm"
)

const upstreamSourceAutoPriorityScoreVersion = "v1"

var errAutoPriorityGeneratedChannelChanged = errors.New("generated channel changed")

type autoPriorityMonitorStatsCollector func(ctx context.Context, channelIDs []int, windowStart int64) (map[int]model.ChannelMonitorStats, error)
type autoPriorityUsageStatsCollector func(ctx context.Context, channelIDs []int, windowStart int64) (map[int]AutoPriorityUsageStats, error)

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
	return s.runAutoPriority(ctx, sourceID, now, nil)
}

func (s *UpstreamSourceService) RunAutoPriorityForMappings(ctx context.Context, sourceID int, now int64, mappingIDs []int) (*dto.UpstreamSourceAutoPriorityResult, error) {
	filter := make(map[int]struct{}, len(mappingIDs))
	for _, mappingID := range mappingIDs {
		if mappingID == 0 {
			continue
		}
		filter[mappingID] = struct{}{}
	}
	return s.runAutoPriority(ctx, sourceID, now, filter)
}

func (s *UpstreamSourceService) runAutoPriority(ctx context.Context, sourceID int, now int64, mappingFilter map[int]struct{}) (*dto.UpstreamSourceAutoPriorityResult, error) {
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
		if mappingFilter != nil {
			if _, ok := mappingFilter[mapping.Id]; !ok {
				continue
			}
		}
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

		channel, err := loadChannelByIDWithContext(ctx, mapping.LocalChannelID)
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
				ChannelID:                       channel.Id,
				LocalGroup:                      localGroup,
				ChannelType:                     channel.Type,
				CurrentPriority:                 priority,
				EffectiveRateMultiplier:         *mapping.EffectiveRateMultiplier,
				CacheAdjustedCostFactor:         1,
				PreviousEffectiveCostMultiplier: previousAutoPriorityEffectiveCostMultiplier(settings),
				Availability:                    nil,
				FirstTokenLatencyMS:             0,
				UsageLogCount:                   0,
				MonitorCheckCount:               0,
				FirstTokenSampleCount:           0,
				HasPreviousSnapshot:             settings.ChannelAutoPriorityLastScore != nil,
			},
			windowStart: windowStart,
			windowEnd:   now,
		})
		groupedPending[windowStart] = append(groupedPending[windowStart], len(pending)-1)
	}

	// Widen each candidate's price-cohort normalization range with cost bounds
	// observed across ALL upstream sources in its local group, not just this
	// source's run. Each source only ever contributes one channel per cohort, so
	// without this the run-local cohort always has a single member and cost
	// stops differentiating priority (see ScoreAutoPriorityCandidates).
	distinctGroups := make(map[string]struct{}, len(pending))
	distinctTypes := make(map[int]struct{}, len(pending))
	groups := make([]string, 0, len(pending))
	types := make([]int, 0, len(pending))
	for i := range pending {
		g := pending[i].scoreInput.LocalGroup
		if _, ok := distinctGroups[g]; !ok {
			distinctGroups[g] = struct{}{}
			groups = append(groups, g)
		}
		t := pending[i].scoreInput.ChannelType
		if _, ok := distinctTypes[t]; !ok {
			distinctTypes[t] = struct{}{}
			types = append(types, t)
		}
	}
	if bounds, err := autoPriorityLocalGroupCostBounds(ctx, groups, types); err != nil {
		common.SysError(fmt.Sprintf("upstream source auto-priority: failed to compute cross-source cost bounds: %v", err))
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
	for windowStart, indexes := range groupedPending {
		if err := fillAutoPriorityScoreInputsForWindow(ctx, pending, indexes, windowStart, scoreInputs, result, resultSlots, model.GetChannelMonitorStatsWithContext, CollectAutoPriorityUsageStatsWithContext); err != nil {
			appendAutoPriorityResultSlots(result, resultSlots)
			return result, err
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
			ThroughputScore:         score.ThroughputScore,
			FinalScore:              score.FinalScore,
		}

		candidate.settings.ChannelAutoPriorityEnabled = candidate.resolution.AutoPriorityEnabled
		candidate.settings.ChannelAutoPriorityIntervalMinutes = candidate.resolution.AutoPriorityIntervalMinutes
		candidate.settings.ChannelAutoPriorityWindowHours = candidate.resolution.AutoPriorityWindowHours
		candidate.settings.ChannelAutoPriorityLastRunAt = now
		candidate.settings.ChannelAutoPriorityLastScore = buildChannelAutoPriorityScoreSnapshot(score, candidate.windowStart, candidate.windowEnd)

		if score.Applied {
			candidate.settings.ChannelAutoPriorityLastAppliedAt = now
		}
		reason, txErr := persistAutoPriorityCandidate(ctx, candidate, score, now)
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
		if reason != "" {
			candidateResult.Applied = false
			candidateResult.Reason = reason
			candidateResult.NewPriority = candidateResult.OldPriority
		}
		if score.Applied && reason == "" {
			result.Updated++
			appliedAny = true
		} else {
			result.Skipped++
		}
		resultSlots[candidate.resultIndex] = &candidateResult
	}

	if appliedAny {
		initChannelCacheAfterAutoPriority(ctx)
	}

	for _, slot := range resultSlots {
		if slot != nil {
			result.Results = append(result.Results, *slot)
		}
	}

	return result, nil
}

// autoPriorityMappingCostRow projects the columns needed to accumulate
// per-cohort cost bounds from upstream-source channel mappings.
type autoPriorityMappingCostRow struct {
	LocalChannelID          int
	EffectiveRateMultiplier float64
}

// autoPriorityLocalGroupCostBounds returns, keyed by autoPriorityCohortKey
// (localGroup + "#" + channelType), the [min, max] effective rate multiplier
// across all ENABLED channels in that exact local group and channel type, taken
// from their upstream-source mappings across ALL sources. A single auto-priority
// run only ever sees the channels generated by one source, so without this,
// every price cohort has exactly one member and cost cannot differentiate
// priority; this widens the normalization range to the whole local group.
//
// Bounds are recomputed each run from the current enabled channels, so newly
// added/removed channels only shift a peer's normalization at its next run
// (self-healing; still strictly better than the pre-fix "cost ignored" state).
// It assumes one generating mapping per enabled channel; a stale/duplicate
// mapping pointing at an enabled channel would only widen the range and compress
// scores toward 100, never invert cheaper-vs-dearer ordering.
func autoPriorityLocalGroupCostBounds(ctx context.Context, groups []string, types []int) (map[string][2]float64, error) {
	groupSet := make(map[string]struct{}, len(groups))
	dedupedGroups := make([]string, 0, len(groups))
	for _, g := range groups {
		trimmed := strings.TrimSpace(g)
		if trimmed == "" {
			continue
		}
		if _, ok := groupSet[trimmed]; ok {
			continue
		}
		groupSet[trimmed] = struct{}{}
		dedupedGroups = append(dedupedGroups, trimmed)
	}
	if len(dedupedGroups) == 0 {
		return map[string][2]float64{}, nil
	}

	typeSet := make(map[int]struct{}, len(types))
	for _, t := range types {
		typeSet[t] = struct{}{}
	}

	channelRows, err := model.GetEnabledChannelGroupTypesInGroups(ctx, dedupedGroups)
	if err != nil {
		return nil, err
	}

	channelCohort := make(map[int]string, len(channelRows))
	channelIDs := make([]int, 0, len(channelRows))
	for _, row := range channelRows {
		trimmedGroup := strings.TrimSpace(row.LocalGroup)
		if _, ok := groupSet[trimmedGroup]; !ok {
			continue
		}
		if _, ok := typeSet[row.Type]; !ok {
			continue
		}
		cohort := autoPriorityCohortKey(trimmedGroup, row.Type)
		channelCohort[row.Id] = cohort
		channelIDs = append(channelIDs, row.Id)
	}
	if len(channelIDs) == 0 {
		return map[string][2]float64{}, nil
	}

	var mappingRows []autoPriorityMappingCostRow
	if err := model.DB.WithContext(ctx).Model(&model.UpstreamSourceChannelMapping{}).
		Where("local_channel_id IN ?", channelIDs).
		Where("effective_rate_multiplier > ?", 0).
		Select("local_channel_id", "effective_rate_multiplier").
		Find(&mappingRows).Error; err != nil {
		return nil, err
	}

	bounds := make(map[string][2]float64, len(channelCohort))
	for _, row := range mappingRows {
		cohort, ok := channelCohort[row.LocalChannelID]
		if !ok {
			continue
		}
		current, exists := bounds[cohort]
		if !exists {
			bounds[cohort] = [2]float64{row.EffectiveRateMultiplier, row.EffectiveRateMultiplier}
			continue
		}
		if row.EffectiveRateMultiplier < current[0] {
			current[0] = row.EffectiveRateMultiplier
		}
		if row.EffectiveRateMultiplier > current[1] {
			current[1] = row.EffectiveRateMultiplier
		}
		bounds[cohort] = current
	}
	return bounds, nil
}

func previousAutoPriorityEffectiveCostMultiplier(settings dto.ChannelOtherSettings) float64 {
	if settings.ChannelAutoPriorityLastScore == nil {
		return 0
	}
	return settings.ChannelAutoPriorityLastScore.EffectiveCostMultiplier
}

func initChannelCacheAfterAutoPriority(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			logger.LogWarn(ctx, fmt.Sprintf("upstream source auto-priority: InitChannelCache panic: %v", r))
		}
	}()
	model.InitChannelCache()
}

func fillAutoPriorityScoreInputsForWindow(ctx context.Context, pending []upstreamSourceAutoPriorityCandidate, indexes []int, windowStart int64, scoreInputs []AutoPriorityScoreInput, result *dto.UpstreamSourceAutoPriorityResult, resultSlots []*dto.UpstreamSourceAutoPriorityChannelResult, monitorCollector autoPriorityMonitorStatsCollector, usageCollector autoPriorityUsageStatsCollector) error {
	channelIDList := make([]int, 0, len(indexes))
	for _, idx := range indexes {
		channelIDList = append(channelIDList, pending[idx].channel.Id)
	}

	monitorStats, err := monitorCollector(ctx, channelIDList, windowStart)
	if err != nil {
		markAutoPriorityGroupFailure(result, resultSlots, pending, indexes, "monitor_stats_failed", err)
		return err
	}
	usageStats, err := usageCollector(ctx, channelIDList, windowStart)
	if err != nil {
		markAutoPriorityGroupFailure(result, resultSlots, pending, indexes, "usage_stats_failed", err)
		return err
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
	return nil
}

func persistAutoPriorityCandidate(ctx context.Context, candidate upstreamSourceAutoPriorityCandidate, score AutoPriorityScoreResult, now int64) (string, error) {
	settings := candidate.settings
	settings.ChannelAutoPriorityEnabled = candidate.resolution.AutoPriorityEnabled
	settings.ChannelAutoPriorityIntervalMinutes = candidate.resolution.AutoPriorityIntervalMinutes
	settings.ChannelAutoPriorityWindowHours = candidate.resolution.AutoPriorityWindowHours
	settings.ChannelAutoPriorityLastRunAt = now
	settings.ChannelAutoPriorityLastScore = buildChannelAutoPriorityScoreSnapshot(score, candidate.windowStart, candidate.windowEnd)
	if score.Applied {
		settings.ChannelAutoPriorityLastAppliedAt = now
	}

	channelSettings := candidate.channel
	channelSettings.SetOtherSettings(settings)

	err := model.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		updates := map[string]any{
			"settings": channelSettings.OtherSettings,
		}
		if score.Applied {
			updates["priority"] = score.NewPriority
		}
		res := tx.Model(&model.Channel{}).
			Where("id = ? AND settings = ?", candidate.channel.Id, candidate.channel.OtherSettings).
			Updates(updates)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return errAutoPriorityGeneratedChannelChanged
		}
		if score.Applied {
			abilityRes := tx.Model(&model.Ability{}).Where("channel_id = ?", candidate.channel.Id).Update("priority", score.NewPriority)
			if abilityRes.Error != nil {
				return abilityRes.Error
			}
			if err := resolveAutoPriorityAbilityUpdateResult(candidate.channel.Id, abilityRes.RowsAffected, func(channelID int) (bool, error) {
				return autoPriorityAbilityExists(tx, channelID)
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, errAutoPriorityGeneratedChannelChanged) {
			return "generated_channel_changed", nil
		}
		return "", err
	}
	return "", nil
}

func markAutoPriorityGroupFailure(result *dto.UpstreamSourceAutoPriorityResult, resultSlots []*dto.UpstreamSourceAutoPriorityChannelResult, pending []upstreamSourceAutoPriorityCandidate, indexes []int, reason string, err error) {
	for _, idx := range indexes {
		candidate := pending[idx]
		slot := dto.UpstreamSourceAutoPriorityChannelResult{
			MappingID:               candidate.mapping.Id,
			LocalChannelID:          candidate.channel.Id,
			OldPriority:             candidate.scoreInput.CurrentPriority,
			NewPriority:             candidate.scoreInput.CurrentPriority,
			ComputedPriority:        candidate.scoreInput.CurrentPriority,
			Applied:                 false,
			Reason:                  reason,
			EffectiveRateMultiplier: candidate.scoreInput.EffectiveRateMultiplier,
			CacheAdjustedCostFactor: candidate.scoreInput.CacheAdjustedCostFactor,
			EffectiveCostMultiplier: candidate.scoreInput.EffectiveRateMultiplier * candidate.scoreInput.CacheAdjustedCostFactor,
			EffectivePriceScore:     0,
			AvailabilityScore:       0,
			FirstTokenScore:         0,
			ThroughputScore:         0,
			FinalScore:              0,
		}
		resultSlots[candidate.resultIndex] = &slot
	}
	result.Failed += len(indexes)
	if result.Error == "" {
		result.Error = SanitizeUpstreamSourceError(err)
	}
}

func appendAutoPriorityResultSlots(result *dto.UpstreamSourceAutoPriorityResult, resultSlots []*dto.UpstreamSourceAutoPriorityChannelResult) {
	for _, slot := range resultSlots {
		if slot != nil {
			result.Results = append(result.Results, *slot)
		}
	}
}

func resolveAutoPriorityAbilityUpdateResult(channelID int, rowsAffected int64, abilityExists func(channelID int) (bool, error)) error {
	if rowsAffected != 0 {
		return nil
	}
	exists, err := abilityExists(channelID)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	return errors.New("ability priority update affected no rows")
}

func autoPriorityAbilityExists(tx *gorm.DB, channelID int) (bool, error) {
	var count int64
	if err := tx.Model(&model.Ability{}).Where("channel_id = ?", channelID).Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

func loadChannelByIDWithContext(ctx context.Context, channelID int) (*model.Channel, error) {
	var channel model.Channel
	if err := model.DB.WithContext(ctx).First(&channel, channelID).Error; err != nil {
		return nil, err
	}
	return &channel, nil
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
		ThroughputScore:         score.ThroughputScore,
		FinalScore:              score.FinalScore,
		OldPriority:             score.OldPriority,
		NewPriority:             score.NewPriority,
		Applied:                 score.Applied,
		Reason:                  score.Reason,
		UsageLogCount:           score.UsageLogCount,
		MonitorCheckCount:       score.MonitorCheckCount,
		FirstTokenSampleCount:   score.FirstTokenSampleCount,
		ThroughputSampleCount:   score.ThroughputSampleCount,
	}
}
