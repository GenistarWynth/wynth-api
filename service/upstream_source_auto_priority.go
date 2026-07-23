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

const upstreamSourceAutoPriorityScoreVersion = "v4"

var errAutoPriorityGeneratedChannelChanged = errors.New("generated channel changed")

type autoPriorityMonitorStatsCollector func(ctx context.Context, channelIDs []int, windowStart int64) (map[int]model.ChannelMonitorStats, error)
type autoPriorityUsageStatsCollector func(ctx context.Context, channelIDs []int, windowStart int64) (map[int]AutoPriorityUsageStats, error)

type upstreamSourceAutoPriorityCandidate struct {
	mapping                 model.UpstreamSourceChannelMapping
	channel                 model.Channel
	settings                dto.ChannelOtherSettings
	resolution              upstreamSourceRuleResolution
	scoreInput              AutoPriorityScoreInput
	windowStart             int64
	availabilityWindowStart int64
	windowEnd               int64
	resultIndex             int
}

type autoPriorityWindowKey struct {
	usageWindowStart        int64
	availabilityWindowStart int64
}

func (s *UpstreamSourceService) RunAutoPriority(ctx context.Context, sourceID int, now int64) (*dto.UpstreamSourceAutoPriorityResult, error) {
	if now == 0 {
		now = s.now()
	}
	return runChannelAutoPriorityGroupsForSourceMappings(ctx, sourceID, now, nil, false)
}

func (s *UpstreamSourceService) RunAutoPriorityForMappings(ctx context.Context, sourceID int, now int64, mappingIDs []int) (*dto.UpstreamSourceAutoPriorityResult, error) {
	filter := make(map[int]struct{}, len(mappingIDs))
	for _, mappingID := range mappingIDs {
		if mappingID == 0 {
			continue
		}
		filter[mappingID] = struct{}{}
	}
	if now == 0 {
		now = s.now()
	}
	return runChannelAutoPriorityGroupsForSourceMappings(ctx, sourceID, now, filter, false)
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
	groupedPending := make(map[autoPriorityWindowKey][]int, len(mappings))
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
				PreviousCacheAdjustedCostFactor: previousAutoPriorityCacheAdjustedCostFactor(settings),
				PreviousEffectiveCostMultiplier: previousAutoPriorityEffectiveCostMultiplier(settings),
				Availability:                    nil,
				FirstTokenLatencyMS:             0,
				UsageLogCount:                   0,
				MonitorCheckCount:               0,
				FirstTokenSampleCount:           0,
				HasPreviousSnapshot:             settings.ChannelAutoPriorityLastScore != nil,
				HardUnavailable:                 channel.Status == common.ChannelStatusAutoDisabled,
			},
			windowStart:             windowStart,
			availabilityWindowStart: 0,
			windowEnd:               now,
		})
	}

	// Widen each candidate's price-cohort normalization range with nominal
	// rate bounds observed across ALL upstream sources in its local group, not
	// just this source's run. Cache factors never enter these bounds.
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
	groupAvailabilityWindowHours, err := autoPriorityLocalGroupAvailabilityWindowHours(ctx, groups)
	if err != nil {
		result.Failed += len(pending)
		result.Error = SanitizeUpstreamSourceError(err)
		return result, err
	}
	for i := range pending {
		availabilityWindowHours := groupAvailabilityWindowHours[pending[i].scoreInput.LocalGroup]
		if availabilityWindowHours == 0 {
			availabilityWindowHours = dto.NormalizeChannelAutoPriorityWindowHours(
				pending[i].resolution.AutoPriorityAvailabilityWindowHours,
			)
		}
		pending[i].resolution.AutoPriorityAvailabilityWindowHours = availabilityWindowHours
		pending[i].availabilityWindowStart = now - int64(availabilityWindowHours)*3600
		windowKey := autoPriorityWindowKey{
			usageWindowStart:        pending[i].windowStart,
			availabilityWindowStart: pending[i].availabilityWindowStart,
		}
		groupedPending[windowKey] = append(groupedPending[windowKey], i)
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
	for windowKey, indexes := range groupedPending {
		if err := fillAutoPriorityScoreInputsForWindows(ctx, pending, indexes, windowKey.availabilityWindowStart, windowKey.usageWindowStart, scoreInputs, result, resultSlots, model.GetChannelMonitorStatsWithContext, CollectAutoPriorityUsageStatsWithContext); err != nil {
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
			NominalRateMultiplier:   score.NominalRateMultiplier,
			CacheAdjustedCostFactor: score.CacheAdjustedCostFactor,
			EffectiveCostMultiplier: score.EffectiveCostMultiplier,
			EffectivePriceScore:     score.EffectivePriceScore,
			NominalPriceScore:       score.NominalPriceScore,
			CacheScore:              score.CacheScore,
			AvailabilityScore:       score.AvailabilityScore,
			FirstTokenScore:         score.FirstTokenScore,
			ThroughputScore:         score.ThroughputScore,
			FinalScore:              score.FinalScore,
		}

		candidate.settings.ChannelAutoPriorityEnabled = candidate.resolution.AutoPriorityEnabled
		candidate.settings.ChannelAutoPriorityIntervalMinutes = candidate.resolution.AutoPriorityIntervalMinutes
		candidate.settings.ChannelAutoPriorityWindowHours = candidate.resolution.AutoPriorityWindowHours
		candidate.settings.ChannelAutoPriorityAvailabilityWindowHours = candidate.resolution.AutoPriorityAvailabilityWindowHours
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
// per-cohort nominal rate bounds from upstream-source channel mappings.
type autoPriorityMappingCostRow struct {
	LocalChannelID          int
	EffectiveRateMultiplier float64
}

type autoPriorityChannelSettingsRow struct {
	ID            int
	OtherSettings string `gorm:"column:settings"`
}

// autoPriorityLocalGroupAvailabilityWindowHours resolves one availability
// window per exact local group across every enabled or temporarily auto-disabled
// channel participating in auto-priority scoring. Channel origin is intentionally
// irrelevant: manual and upstream-generated members contribute equally. Taking
// the maximum preserves the existing deterministic conflict policy until the
// next monitor save makes every member's persisted value identical.
func autoPriorityLocalGroupAvailabilityWindowHours(ctx context.Context, groups []string) (map[string]int, error) {
	groupSet := make(map[string]struct{}, len(groups))
	dedupedGroups := make([]string, 0, len(groups))
	for _, group := range groups {
		trimmed := strings.TrimSpace(group)
		if _, exists := groupSet[trimmed]; exists {
			continue
		}
		groupSet[trimmed] = struct{}{}
		dedupedGroups = append(dedupedGroups, trimmed)
	}
	if len(dedupedGroups) == 0 {
		return map[string]int{}, nil
	}

	channelRows, err := model.GetAutoPriorityChannelGroupTypesInGroups(ctx, dedupedGroups)
	if err != nil {
		return nil, err
	}
	channelGroups := make(map[int]string, len(channelRows))
	channelIDs := make([]int, 0, len(channelRows))
	for _, row := range channelRows {
		localGroup := strings.TrimSpace(row.LocalGroup)
		if _, exists := groupSet[localGroup]; !exists {
			continue
		}
		channelGroups[row.Id] = localGroup
		channelIDs = append(channelIDs, row.Id)
	}
	if len(channelIDs) == 0 {
		return map[string]int{}, nil
	}

	var settingsRows []autoPriorityChannelSettingsRow
	if err := model.DB.WithContext(ctx).Model(&model.Channel{}).
		Select("id", "settings").
		Where("id IN ?", channelIDs).
		Find(&settingsRows).Error; err != nil {
		return nil, err
	}

	windowHoursByGroup := make(map[string]int, len(dedupedGroups))
	for _, row := range settingsRows {
		settings := dto.ChannelOtherSettings{}
		if strings.TrimSpace(row.OtherSettings) != "" {
			if err := common.UnmarshalJsonStr(row.OtherSettings, &settings); err != nil {
				continue
			}
		}
		if !settings.ChannelAutoPriorityEnabled {
			continue
		}
		localGroup, exists := channelGroups[row.ID]
		if !exists {
			continue
		}
		windowHours := dto.NormalizeChannelAutoPriorityWindowHours(
			settings.ChannelAutoPriorityAvailabilityWindowHours,
		)
		if windowHours > windowHoursByGroup[localGroup] {
			windowHoursByGroup[localGroup] = windowHours
		}
	}
	return windowHoursByGroup, nil
}

func updateAutoPriorityCostBounds(bounds map[string][2]float64, cohort string, multiplier float64) {
	if !isValidAutoPriorityMultiplier(multiplier) {
		return
	}
	current, exists := bounds[cohort]
	if !exists {
		bounds[cohort] = [2]float64{multiplier, multiplier}
		return
	}
	if multiplier < current[0] {
		current[0] = multiplier
	}
	if multiplier > current[1] {
		current[1] = multiplier
	}
	bounds[cohort] = current
}

// autoPriorityLocalGroupCostBounds returns, keyed by autoPriorityCohortKey
// (localGroup + "#" + channelType), the [min, max] nominal rate multiplier
// across all enabled or temporarily auto-disabled auto-priority channels in that
// exact local group and channel type. Normal channels use their channel-level
// rate multiplier (1x when unset), while upstream-generated channels retain the
// effective rate from their mapping. This auxiliary bounds query uses the same
// eligibility rules as the primary full-cohort worker.
//
// Bounds are recomputed each run from the current enabled channels, so newly
// added/removed channels only shift a peer's normalization at its next run
// (self-healing; still strictly better than the pre-fix "cost ignored" state).
// It assumes one generating mapping per enabled generated channel; a stale or
// duplicate mapping would only widen the range and compress scores toward 100,
// never invert cheaper-vs-dearer ordering.
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

	channelRows, err := model.GetAutoPriorityChannelGroupTypesInGroups(ctx, dedupedGroups)
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

	var settingsRows []autoPriorityChannelSettingsRow
	if err := model.DB.WithContext(ctx).Model(&model.Channel{}).
		Select("id", "settings").
		Where("id IN ?", channelIDs).
		Find(&settingsRows).Error; err != nil {
		return nil, err
	}

	generatedChannelIDs := make([]int, 0, len(settingsRows))
	bounds := make(map[string][2]float64, len(channelCohort))
	for _, row := range settingsRows {
		cohort, ok := channelCohort[row.ID]
		if !ok {
			continue
		}
		settings := dto.ChannelOtherSettings{}
		if strings.TrimSpace(row.OtherSettings) != "" {
			if err := common.UnmarshalJsonStr(row.OtherSettings, &settings); err != nil {
				continue
			}
		}
		if settings.GeneratedByUpstreamSourceID != 0 || settings.GeneratedByUpstreamMappingID != 0 {
			generatedChannelIDs = append(generatedChannelIDs, row.ID)
			continue
		}
		if !settings.ChannelAutoPriorityEnabled {
			continue
		}
		multiplier := settings.ChannelAutoPriorityRateMultiplier
		if !isValidAutoPriorityMultiplier(multiplier) {
			multiplier = 1
		}
		updateAutoPriorityCostBounds(bounds, cohort, multiplier)
	}
	if len(generatedChannelIDs) == 0 {
		return bounds, nil
	}

	var mappingRows []autoPriorityMappingCostRow
	if err := model.DB.WithContext(ctx).Model(&model.UpstreamSourceChannelMapping{}).
		Where("local_channel_id IN ?", generatedChannelIDs).
		Where("effective_rate_multiplier > ?", 0).
		Select("local_channel_id", "effective_rate_multiplier").
		Find(&mappingRows).Error; err != nil {
		return nil, err
	}
	for _, row := range mappingRows {
		cohort, ok := channelCohort[row.LocalChannelID]
		if !ok {
			continue
		}
		updateAutoPriorityCostBounds(bounds, cohort, row.EffectiveRateMultiplier)
	}
	return bounds, nil
}

func previousAutoPriorityEffectiveCostMultiplier(settings dto.ChannelOtherSettings) float64 {
	if settings.ChannelAutoPriorityLastScore == nil {
		return 0
	}
	return settings.ChannelAutoPriorityLastScore.EffectiveCostMultiplier
}

func previousAutoPriorityCacheAdjustedCostFactor(settings dto.ChannelOtherSettings) float64 {
	if settings.ChannelAutoPriorityLastScore == nil {
		return 0
	}
	return settings.ChannelAutoPriorityLastScore.CacheAdjustedCostFactor
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
	return fillAutoPriorityScoreInputsForWindows(ctx, pending, indexes, windowStart, windowStart, scoreInputs, result, resultSlots, monitorCollector, usageCollector)
}

func fillAutoPriorityScoreInputsForWindows(ctx context.Context, pending []upstreamSourceAutoPriorityCandidate, indexes []int, availabilityWindowStart int64, usageWindowStart int64, scoreInputs []AutoPriorityScoreInput, result *dto.UpstreamSourceAutoPriorityResult, resultSlots []*dto.UpstreamSourceAutoPriorityChannelResult, monitorCollector autoPriorityMonitorStatsCollector, usageCollector autoPriorityUsageStatsCollector) error {
	channelIDList := make([]int, 0, len(indexes))
	for _, idx := range indexes {
		channelIDList = append(channelIDList, pending[idx].channel.Id)
	}

	monitorStats, err := monitorCollector(ctx, channelIDList, availabilityWindowStart)
	if err != nil {
		markAutoPriorityGroupFailure(result, resultSlots, pending, indexes, "monitor_stats_failed", err)
		return err
	}
	usageStats, err := usageCollector(ctx, channelIDList, usageWindowStart)
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
	err := model.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return updateAutoPriorityCandidate(tx, candidate, score, now)
	})
	if err != nil {
		if errors.Is(err, errAutoPriorityGeneratedChannelChanged) {
			return "generated_channel_changed", nil
		}
		return "", err
	}
	return "", nil
}

func updateAutoPriorityCandidate(tx *gorm.DB, candidate upstreamSourceAutoPriorityCandidate, score AutoPriorityScoreResult, now int64) error {
	settings := candidate.settings
	settings.ChannelAutoPriorityEnabled = candidate.resolution.AutoPriorityEnabled
	settings.ChannelAutoPriorityIntervalMinutes = candidate.resolution.AutoPriorityIntervalMinutes
	settings.ChannelAutoPriorityWindowHours = candidate.resolution.AutoPriorityWindowHours
	settings.ChannelAutoPriorityAvailabilityWindowHours = candidate.resolution.AutoPriorityAvailabilityWindowHours
	settings.ChannelAutoPriorityLastRunAt = now
	settings.ChannelAutoPriorityLastScore = buildChannelAutoPriorityScoreSnapshot(score, candidate.windowStart, candidate.windowEnd)
	if score.Applied {
		settings.ChannelAutoPriorityLastAppliedAt = now
	}

	settingsObject := make(map[string]any)
	if strings.TrimSpace(candidate.channel.OtherSettings) != "" {
		if err := common.UnmarshalJsonStr(candidate.channel.OtherSettings, &settingsObject); err != nil {
			return err
		}
	}
	encodedSettings, err := common.Marshal(settings)
	if err != nil {
		return err
	}
	workerSettings := make(map[string]any)
	if err := common.Unmarshal(encodedSettings, &workerSettings); err != nil {
		return err
	}
	workerSettingKeys := []string{
		"channel_auto_priority_enabled",
		"channel_auto_priority_interval_minutes",
		"channel_auto_priority_window_hours",
		"channel_auto_priority_availability_window_hours",
		"channel_auto_priority_last_run_at",
		"channel_auto_priority_last_applied_at",
		"channel_auto_priority_last_score",
	}
	for _, key := range workerSettingKeys {
		if value, exists := workerSettings[key]; exists {
			settingsObject[key] = value
		} else {
			delete(settingsObject, key)
		}
	}
	mergedSettings, err := common.Marshal(settingsObject)
	if err != nil {
		return err
	}

	updates := map[string]any{
		"settings": string(mergedSettings),
	}
	if score.Applied {
		updates["priority"] = score.NewPriority
	}
	res := tx.Model(&model.Channel{}).
		Where("status = ?", candidate.channel.Status).
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
			NominalRateMultiplier:   candidate.scoreInput.EffectiveRateMultiplier,
			CacheAdjustedCostFactor: candidate.scoreInput.CacheAdjustedCostFactor,
			EffectiveCostMultiplier: candidate.scoreInput.EffectiveRateMultiplier * candidate.scoreInput.CacheAdjustedCostFactor,
			EffectivePriceScore:     0,
			NominalPriceScore:       0,
			CacheScore:              0,
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
		Version:                  upstreamSourceAutoPriorityScoreVersion,
		ComputedAt:               windowEnd,
		WindowStart:              windowStart,
		WindowEnd:                windowEnd,
		Cohort:                   score.Cohort,
		CohortFloor:              score.CohortFloor,
		CohortCeil:               score.CohortCeil,
		CohortMemberCount:        score.CohortMemberCount,
		EffectiveRateMultiplier:  score.EffectiveRateMultiplier,
		NominalRateMultiplier:    score.NominalRateMultiplier,
		CacheAdjustedCostFactor:  score.CacheAdjustedCostFactor,
		EffectiveCostMultiplier:  score.EffectiveCostMultiplier,
		EffectivePriceScore:      score.EffectivePriceScore,
		NominalPriceScore:        score.NominalPriceScore,
		CacheScore:               score.CacheScore,
		AvailabilityScore:        score.AvailabilityScore,
		FirstTokenScore:          score.FirstTokenScore,
		ThroughputScore:          score.ThroughputScore,
		FinalScore:               score.FinalScore,
		OldPriority:              score.OldPriority,
		NewPriority:              score.NewPriority,
		Applied:                  score.Applied,
		Reason:                   score.Reason,
		UsageLogCount:            score.UsageLogCount,
		MonitorCheckCount:        score.MonitorCheckCount,
		FirstTokenSampleCount:    score.FirstTokenSampleCount,
		ThroughputSampleCount:    score.ThroughputSampleCount,
		CacheFactorSource:        score.CacheFactorSource,
		CacheFactorPrior:         score.CacheFactorPrior,
		CacheFactorOwnConfidence: score.CacheFactorOwnConfidence,
	}
}
