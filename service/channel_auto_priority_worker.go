package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	relaydto "github.com/QuantumNous/new-api/relaykit/dto"

	"github.com/bytedance/gopkg/util/gopool"
	"gorm.io/gorm"
)

const (
	channelAutoPriorityTickInterval = time.Minute
	// Enabled auto-priority scores normally start at 0; recompute lowers this further
	// when hysteresis preserves a user-entered negative priority.
	channelAutoPriorityDefaultSinkPriority = model.ManuallyDisabledChannelDefaultPriority
)

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
	return runChannelAutoPriorityGroupsForSourceMappings(ctx, sourceID, now, nil, true)
}

func runChannelAutoPriorityGroupsForSourceMappings(
	ctx context.Context,
	sourceID int,
	now int64,
	mappingFilter map[int]struct{},
	includeAllCohortResults bool,
) (*dto.UpstreamSourceAutoPriorityResult, error) {
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
	candidateChannelIDs := make([]int, 0, len(mappings))
	eligibleMappingsByID := make(map[int]model.UpstreamSourceChannelMapping, len(mappings))
	triggerMappingIDs := make(map[int]struct{}, len(mappings))
	triggerMappingIDsByChannelID := make(map[int][]int, len(mappings))
	mappingIDByChannelID := make(map[int]int, len(mappings))
	preResultsByMappingID := make(map[int]dto.UpstreamSourceAutoPriorityChannelResult)
	for _, mapping := range mappings {
		resolution := resolveUpstreamSourceRule(config, &mapping)
		selected := mappingFilter == nil
		if !selected {
			_, selected = mappingFilter[mapping.Id]
		}
		if !resolution.SyncEligible || !resolution.AutoPriorityEnabled {
			if selected {
				result.Skipped++
			}
			continue
		}
		eligibleMappingsByID[mapping.Id] = mapping
		preResultReason := ""
		costSource, validCostSource := model.ResolveUpstreamSourceAutoPriorityCostSource(mapping.AutoPriorityCostSource)
		switch {
		case !validCostSource:
			preResultReason = "invalid_auto_priority_cost_source"
		case costSource == model.UpstreamSourceAutoPriorityCostSourceAdvertised &&
			(mapping.EffectiveRateMultiplier == nil || !isValidAutoPriorityMultiplier(*mapping.EffectiveRateMultiplier)):
			preResultReason = "missing_effective_rate_multiplier"
		}
		if selected && preResultReason != "" {
			preResultsByMappingID[mapping.Id] = dto.UpstreamSourceAutoPriorityChannelResult{
				MappingID:      mapping.Id,
				LocalChannelID: mapping.LocalChannelID,
				Reason:         preResultReason,
			}
		}
		if mapping.LocalChannelID != 0 {
			candidateChannelIDs = append(candidateChannelIDs, mapping.LocalChannelID)
		}
		if mapping.LocalChannelID == 0 {
			if selected {
				if preResultReason == "" {
					preResultsByMappingID[mapping.Id] = dto.UpstreamSourceAutoPriorityChannelResult{
						MappingID: mapping.Id,
						Reason:    "local_channel_missing",
					}
				}
				result.Skipped++
			}
			continue
		}
		if selected {
			channelIDs = append(channelIDs, mapping.LocalChannelID)
			triggerMappingIDs[mapping.Id] = struct{}{}
			triggerMappingIDsByChannelID[mapping.LocalChannelID] = append(
				triggerMappingIDsByChannelID[mapping.LocalChannelID],
				mapping.Id,
			)
		}
	}
	if len(channelIDs) == 0 {
		for _, mapping := range mappings {
			if preResult, ok := preResultsByMappingID[mapping.Id]; ok {
				result.Results = append(result.Results, preResult)
			}
		}
		return result, nil
	}

	var channels []model.Channel
	if err := model.DB.WithContext(ctx).
		Select("id", "group", "status", "priority", "settings").
		Where("id IN ?", candidateChannelIDs).
		Order("id").
		Find(&channels).Error; err != nil {
		return nil, err
	}
	localGroups := make(map[string]struct{}, len(channels))
	for _, channel := range channels {
		selectedMappingIDs := triggerMappingIDsByChannelID[channel.Id]
		for _, mappingID := range selectedMappingIDs {
			delete(triggerMappingIDs, mappingID)
		}
		settings, settingsOK := readChannelOtherSettingsForAutoPriorityDue(channel)
		ownerMapping, ownerExists := eligibleMappingsByID[settings.GeneratedByUpstreamMappingID]
		ownerValid := settingsOK &&
			ownerExists &&
			ownerMapping.LocalChannelID == channel.Id &&
			isGeneratedChannelMetadataMatching(&settings, sourceID, ownerMapping.Id)
		if ownerValid {
			mappingIDByChannelID[channel.Id] = ownerMapping.Id
		}
		for _, selectedMappingID := range selectedMappingIDs {
			if ownerValid && selectedMappingID == ownerMapping.Id {
				continue
			}
			preResultsByMappingID[selectedMappingID] = dto.UpstreamSourceAutoPriorityChannelResult{
				MappingID:      selectedMappingID,
				LocalChannelID: channel.Id,
				OldPriority:    channel.GetPriority(),
				NewPriority:    channel.GetPriority(),
				Reason:         "generated_channel_metadata_mismatch",
			}
			result.Skipped++
		}
		if !ownerValid {
			continue
		}
		ownerWasSelected := false
		for _, selectedMappingID := range selectedMappingIDs {
			if selectedMappingID == ownerMapping.Id {
				ownerWasSelected = true
				break
			}
		}
		if !ownerWasSelected {
			continue
		}
		if channel.Status != common.ChannelStatusEnabled && channel.Status != common.ChannelStatusAutoDisabled {
			result.Skipped++
			continue
		}
		localGroups[strings.TrimSpace(channel.Group)] = struct{}{}
	}
	result.Skipped += len(triggerMappingIDs)
	if len(localGroups) == 0 {
		for _, mapping := range mappings {
			if preResult, ok := preResultsByMappingID[mapping.Id]; ok {
				result.Results = append(result.Results, preResult)
			}
		}
		return result, nil
	}

	runResults, err := runChannelAutoPriority(ctx, now, localGroups, true)
	if err != nil {
		return nil, err
	}
	runResultByMappingID := make(map[int]dto.UpstreamSourceAutoPriorityChannelResult, len(runResults))
	otherCohortResults := make([]dto.UpstreamSourceAutoPriorityChannelResult, 0)
	for _, runResult := range runResults {
		mappingID, belongsToSource := mappingIDByChannelID[runResult.ChannelID]
		if !belongsToSource && !includeAllCohortResults {
			continue
		}
		reason := runResult.Reason
		if reason == "" {
			reason = runResult.score.Reason
		}
		channelResult := dto.UpstreamSourceAutoPriorityChannelResult{
			MappingID:               mappingID,
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
		}
		switch {
		case runResult.Applied:
			result.Updated++
		case runResult.Reason == "update_failed" || strings.HasSuffix(runResult.Reason, "_failed"):
			result.Failed++
		default:
			result.Skipped++
		}
		if belongsToSource {
			runResultByMappingID[mappingID] = channelResult
		} else {
			otherCohortResults = append(otherCohortResults, channelResult)
		}
	}
	for _, mapping := range mappings {
		if preResult, ok := preResultsByMappingID[mapping.Id]; ok {
			result.Results = append(result.Results, preResult)
			continue
		}
		if runResult, ok := runResultByMappingID[mapping.Id]; ok {
			result.Results = append(result.Results, runResult)
		}
	}
	result.Results = append(result.Results, otherCohortResults...)
	return result, nil
}

type configuredAutoPriorityChannel struct {
	channel        model.Channel
	settings       relaydto.ChannelOtherSettings
	localGroup     string
	rateMultiplier float64
	costEvidence   upstreamSourceAutoPriorityCostEvidence
	invalidReason  string
}

type generatedAutoPriorityState struct {
	sourceID            int
	localChannelID      int
	ruleResolved        bool
	autoPriorityEnabled bool
	rateMultiplier      float64
	rateValid           bool
	costEvidence        upstreamSourceAutoPriorityCostEvidence
	invalidReason       string
}

func loadGeneratedAutoPriorityStates(ctx context.Context, mappingIDs []int, now time.Time) (map[int]generatedAutoPriorityState, error) {
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
			Where("id IN ?", sourceIDs).
			Find(&sources).Error; err != nil {
			return nil, err
		}
	}
	channelIDs := make([]int, 0, len(mappings))
	for i := range mappings {
		if mappings[i].LocalChannelID != 0 {
			channelIDs = append(channelIDs, mappings[i].LocalChannelID)
		}
	}
	channelsByID := make(map[int]model.Channel, len(channelIDs))
	if len(channelIDs) > 0 {
		var channels []model.Channel
		if err := model.DB.WithContext(ctx).Where("id IN ?", channelIDs).Find(&channels).Error; err != nil {
			return nil, err
		}
		for i := range channels {
			channelsByID[channels[i].Id] = channels[i]
		}
	}
	probesByMappingID := make(map[int]model.UpstreamSourceBillingProbe, len(mappingIDs))
	var probes []model.UpstreamSourceBillingProbe
	if err := model.DB.WithContext(ctx).Where("mapping_id IN ?", mappingIDs).Find(&probes).Error; err != nil {
		return nil, err
	}
	for i := range probes {
		probesByMappingID[probes[i].MappingID] = probes[i]
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
		if !state.autoPriorityEnabled {
			states[mapping.Id] = state
			continue
		}
		channel, channelExists := channelsByID[mapping.LocalChannelID]
		var channelPtr *model.Channel
		if channelExists {
			channelPtr = &channel
		}
		probe, probeExists := probesByMappingID[mapping.Id]
		var probePtr *model.UpstreamSourceBillingProbe
		if probeExists {
			probePtr = &probe
		}
		resolvedCost, invalidReason := resolveUpstreamSourceAutoPriorityCost(
			now,
			&source,
			&mapping,
			channelPtr,
			probePtr,
		)
		state.invalidReason = invalidReason
		if invalidReason == "" {
			state.rateMultiplier = resolvedCost.nominalRateMultiplier
			state.rateValid = true
			state.costEvidence = resolvedCost.evidence
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
	appliedAny := false
	defer func() {
		if appliedAny {
			initChannelCacheAfterAutoPriority(ctx)
		}
	}()

	var channels []model.Channel
	if err := model.DB.WithContext(ctx).
		Where("status IN ?", []int{
			common.ChannelStatusEnabled,
			common.ChannelStatusManuallyDisabled,
			common.ChannelStatusAutoDisabled,
		}).
		Order("id").
		Find(&channels).Error; err != nil {
		return nil, err
	}

	type channelWithSettings struct {
		channel    model.Channel
		settings   relaydto.ChannelOtherSettings
		localGroup string
		ownership  autoPriorityChannelOwnership
	}
	selectedChannels := make([]model.Channel, 0, len(channels))
	manuallyDisabledCandidates := make([]model.Channel, 0, len(channels))
	for _, channel := range channels {
		localGroup := strings.TrimSpace(channel.Group)
		if localGroupFilter != nil {
			if _, ok := localGroupFilter[localGroup]; !ok {
				continue
			}
		}
		if channel.Status == common.ChannelStatusManuallyDisabled {
			manuallyDisabledCandidates = append(manuallyDisabledCandidates, channel)
		}
		selectedChannels = append(selectedChannels, channel)
	}
	ownershipByChannelID, err := loadAutoPriorityChannelOwnership(ctx, selectedChannels)
	if err != nil {
		return nil, err
	}
	channelsWithSettings := make([]channelWithSettings, 0, len(selectedChannels))
	mappingIDs := make([]int, 0, len(selectedChannels))
	for _, channel := range selectedChannels {
		ownership := ownershipByChannelID[channel.Id]
		if !ownership.settingsValid && !ownership.generated {
			logger.LogWarn(ctx, fmt.Sprintf("channel auto-priority: skip channel_id=%d invalid settings", channel.Id))
			continue
		}
		channelsWithSettings = append(channelsWithSettings, channelWithSettings{
			channel:    channel,
			settings:   ownership.settings,
			localGroup: strings.TrimSpace(channel.Group),
			ownership:  ownership,
		})
		if ownership.mappingValid {
			mappingIDs = append(mappingIDs, ownership.mapping.Id)
		}
	}

	generatedStates, err := loadGeneratedAutoPriorityStates(ctx, mappingIDs, time.Unix(now, 0))
	if err != nil {
		return nil, err
	}
	configuredChannels := make([]configuredAutoPriorityChannel, 0, len(channelsWithSettings))
	for _, configured := range channelsWithSettings {
		settings := configured.settings
		if !configured.ownership.generated {
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
		settings.ChannelAutoPriorityEnabled = true
		if configured.ownership.invalidReason != "" {
			configuredChannels = append(configuredChannels, configuredAutoPriorityChannel{
				channel:       configured.channel,
				settings:      settings,
				localGroup:    configured.localGroup,
				invalidReason: configured.ownership.invalidReason,
			})
			continue
		}

		state, stateExists := generatedStates[configured.ownership.mapping.Id]
		if stateExists && state.ruleResolved && !state.autoPriorityEnabled {
			continue
		}
		if (!stateExists || !state.ruleResolved) && !settings.ChannelAutoPriorityEnabled {
			continue
		}
		invalidReason := ""
		if !stateExists || !state.ruleResolved {
			invalidReason = "upstream_rule_resolution_failed"
		} else if state.localChannelID != configured.channel.Id || state.sourceID != settings.GeneratedByUpstreamSourceID {
			invalidReason = "generated_channel_metadata_mismatch"
		} else if state.invalidReason != "" {
			invalidReason = state.invalidReason
		} else if !state.rateValid {
			invalidReason = "missing_effective_rate_multiplier"
		}
		configuredChannels = append(configuredChannels, configuredAutoPriorityChannel{
			channel:        configured.channel,
			settings:       settings,
			localGroup:     configured.localGroup,
			rateMultiplier: state.rateMultiplier,
			costEvidence:   state.costEvidence,
			invalidReason:  invalidReason,
		})
	}
	invalidGroups := make(map[string]string)
	for _, configuredChannel := range configuredChannels {
		if configuredChannel.invalidReason != "" {
			invalidGroups[configuredChannel.localGroup] = configuredChannel.invalidReason
		}
	}
	manuallyDisabledByGroup := make(map[string][]model.Channel)
	for _, channel := range manuallyDisabledCandidates {
		localGroup := strings.TrimSpace(channel.Group)
		manuallyDisabledByGroup[localGroup] = append(manuallyDisabledByGroup[localGroup], channel)
	}
	enabledConfiguredChannels := configuredChannels[:0]
	for _, configuredChannel := range configuredChannels {
		if configuredChannel.channel.Status == common.ChannelStatusManuallyDisabled {
			continue
		}
		enabledConfiguredChannels = append(enabledConfiguredChannels, configuredChannel)
	}
	configuredChannels = enabledConfiguredChannels

	groupSchedules := make(map[string]*autoPriorityGroupSchedule)
	for _, configuredChannel := range configuredChannels {
		schedule := groupSchedules[configuredChannel.localGroup]
		if schedule == nil {
			schedule = &autoPriorityGroupSchedule{lastRunAt: configuredChannel.settings.ChannelAutoPriorityLastRunAt}
			groupSchedules[configuredChannel.localGroup] = schedule
		} else if schedule.lastRunAt != 0 && (configuredChannel.settings.ChannelAutoPriorityLastRunAt == 0 || configuredChannel.settings.ChannelAutoPriorityLastRunAt < schedule.lastRunAt) {
			schedule.lastRunAt = configuredChannel.settings.ChannelAutoPriorityLastRunAt
		}
		intervalMinutes := relaydto.NormalizeChannelAutoPriorityInterval(configuredChannel.settings.ChannelAutoPriorityIntervalMinutes)
		if intervalMinutes > schedule.intervalMinutes {
			schedule.intervalMinutes = intervalMinutes
		}
		lastRunAt := configuredChannel.settings.ChannelAutoPriorityLastRunAt
		if intervalMinutes == 0 || lastRunAt == 0 || now-lastRunAt >= int64(intervalMinutes)*60 {
			schedule.due = true
		}
		schedule.members = append(schedule.members, configuredChannel)
	}

	selectedGroups := make([]string, 0, len(groupSchedules)+len(manuallyDisabledByGroup))
	selectedGroupSet := make(map[string]struct{}, cap(selectedGroups))
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
		selectedGroupSet[localGroup] = struct{}{}
	}
	if force {
		for localGroup := range manuallyDisabledByGroup {
			if _, selected := selectedGroupSet[localGroup]; selected {
				continue
			}
			selectedGroups = append(selectedGroups, localGroup)
		}
	}
	sort.Strings(selectedGroups)

	groupAvailabilityWindowHours := make(map[string]int, len(selectedGroups))
	for _, configuredChannel := range configuredChannels {
		windowHours := relaydto.NormalizeChannelAutoPriorityWindowHours(
			configuredChannel.settings.ChannelAutoPriorityAvailabilityWindowHours,
		)
		if windowHours > groupAvailabilityWindowHours[configuredChannel.localGroup] {
			groupAvailabilityWindowHours[configuredChannel.localGroup] = windowHours
		}
	}

	pending := make([]upstreamSourceAutoPriorityCandidate, 0, len(configuredChannels))
	groupedPending := make(map[autoPriorityWindowKey][]int, len(configuredChannels))
	pendingIndexesByGroup := make(map[string][]int, len(selectedGroups))
	for _, localGroup := range selectedGroups {
		schedule := groupSchedules[localGroup]
		if schedule == nil {
			continue
		}
		for _, configuredChannel := range schedule.members {
			channel := configuredChannel.channel
			settings := configuredChannel.settings
			windowHours := relaydto.NormalizeChannelAutoPriorityWindowHours(settings.ChannelAutoPriorityWindowHours)
			availabilityWindowHours := groupAvailabilityWindowHours[configuredChannel.localGroup]
			if availabilityWindowHours == 0 {
				availabilityWindowHours = relaydto.NormalizeChannelAutoPriorityWindowHours(
					settings.ChannelAutoPriorityAvailabilityWindowHours,
				)
			}

			rateMultiplier := configuredChannel.rateMultiplier
			if configuredChannel.invalidReason != "" {
				rateMultiplier = 1
				invalidGroups[localGroup] = configuredChannel.invalidReason
			}

			pending = append(pending, upstreamSourceAutoPriorityCandidate{
				channel:      channel,
				settings:     settings,
				costEvidence: configuredChannel.costEvidence,
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
					PreviousCacheAdjustedCostFactor: previousAutoPriorityCacheAdjustedCostFactor(settings),
					PreviousEffectiveCostMultiplier: previousAutoPriorityEffectiveCostMultiplier(settings),
					HasPreviousSnapshot:             settings.ChannelAutoPriorityLastScore != nil,
					HardUnavailable:                 channel.Status == common.ChannelStatusAutoDisabled,
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
	processedManuallyDisabled := make(map[int]struct{}, len(manuallyDisabledCandidates))

	for _, localGroup := range selectedGroups {
		indexes := pendingIndexesByGroup[localGroup]
		manuallyDisabled := manuallyDisabledByGroup[localGroup]
		if len(indexes) == 0 && len(manuallyDisabled) == 0 {
			continue
		}
		if len(indexes) > 0 {
			if reason, failed := failedReasons[indexes[0]]; failed {
				for _, idx := range indexes {
					oldPriority := pending[idx].channel.GetPriority()
					results = append(results, ChannelAutoPriorityRunResult{
						ChannelID: pending[idx].channel.Id,
						Applied:   false,
						Reason:    reason,
						score: AutoPriorityScoreResult{
							ChannelID:   pending[idx].channel.Id,
							OldPriority: oldPriority,
							NewPriority: oldPriority,
							Reason:      reason,
						},
					})
				}
				for _, channel := range manuallyDisabled {
					processedManuallyDisabled[channel.Id] = struct{}{}
					priority := channel.GetPriority()
					results = append(results, ChannelAutoPriorityRunResult{
						ChannelID: channel.Id,
						Applied:   false,
						Reason:    reason,
						score: AutoPriorityScoreResult{
							ChannelID:        channel.Id,
							OldPriority:      priority,
							ComputedPriority: priority,
							NewPriority:      priority,
							Applied:          false,
							Reason:           reason,
						},
					})
				}
				continue
			}
		}

		sinkPriority := channelAutoPriorityDefaultSinkPriority
		for _, idx := range indexes {
			if pending[idx].scoreInput.HardUnavailable {
				continue
			}
			enabledPriority := scoreResults[idx].NewPriority
			if enabledPriority > sinkPriority {
				continue
			}
			if enabledPriority == math.MinInt64 {
				sinkPriority = math.MinInt64
				continue
			}
			sinkPriority = enabledPriority - 1
		}
		reason, manualSinkResults, err := persistChannelAutoPriorityGroup(ctx, pending, scoreResults, indexes, manuallyDisabled, sinkPriority, now)
		if err != nil {
			logger.LogWarn(ctx, fmt.Sprintf("channel auto-priority: persist group=%q failed: %v", localGroup, err))
			reason = "update_failed"
		} else if reason != "" {
			// A cohort-level CAS conflict rolls the whole transaction back just
			// like any other persistence failure. Report one consistent group
			// outcome; the post-rollback refresh below carries the DB truth.
			reason = "update_failed"
		}
		if reason != "" {
			affectedChannelIDs := make([]int, 0, len(indexes)+len(manuallyDisabled))
			for _, idx := range indexes {
				affectedChannelIDs = append(affectedChannelIDs, pending[idx].channel.Id)
			}
			for _, channel := range manuallyDisabled {
				affectedChannelIDs = append(affectedChannelIDs, channel.Id)
			}
			var persistedChannels []model.Channel
			if refreshErr := model.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				return tx.
					Select("id", "status", "priority", "settings").
					Where("id IN ?", affectedChannelIDs).
					Find(&persistedChannels).Error
			}); refreshErr != nil {
				return nil, fmt.Errorf(
					"channel auto-priority: refresh persisted priorities after group=%q failure: %w",
					localGroup,
					refreshErr,
				)
			}
			persistedChannelsByID := make(map[int]model.Channel, len(persistedChannels))
			for _, channel := range persistedChannels {
				persistedChannelsByID[channel.Id] = channel
			}
			for _, channelID := range affectedChannelIDs {
				if _, exists := persistedChannelsByID[channelID]; !exists {
					return nil, fmt.Errorf(
						"channel auto-priority: refresh persisted priorities after group=%q failure omitted channel_id=%d",
						localGroup,
						channelID,
					)
				}
			}
			for _, idx := range indexes {
				score := scoreResults[idx]
				persistedChannel := persistedChannelsByID[pending[idx].channel.Id]
				persistedPriority := persistedChannel.GetPriority()
				score.Applied = false
				score.Reason = reason
				// Failed-unapplied results define both old and new priority as
				// the post-rollback persisted value. ComputedPriority remains
				// the attempted target for diagnostics.
				score.OldPriority = persistedPriority
				score.NewPriority = persistedPriority
				results = append(results, ChannelAutoPriorityRunResult{
					ChannelID: pending[idx].channel.Id,
					Applied:   false,
					Reason:    reason,
					score:     score,
				})
			}
			for _, channel := range manuallyDisabled {
				processedManuallyDisabled[channel.Id] = struct{}{}
				persistedChannel := persistedChannelsByID[channel.Id]
				persistedPriority := persistedChannel.GetPriority()
				results = append(results, ChannelAutoPriorityRunResult{
					ChannelID: channel.Id,
					Applied:   false,
					Reason:    reason,
					score: AutoPriorityScoreResult{
						ChannelID:        channel.Id,
						OldPriority:      persistedPriority,
						ComputedPriority: sinkPriority,
						NewPriority:      persistedPriority,
						Applied:          false,
						Reason:           reason,
					},
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
		manualSinkByChannelID := make(map[int]model.ManuallyDisabledChannelSinkResult, len(manualSinkResults))
		for _, sinkResult := range manualSinkResults {
			manualSinkByChannelID[sinkResult.ChannelID] = sinkResult
		}
		for _, channel := range manuallyDisabled {
			processedManuallyDisabled[channel.Id] = struct{}{}
			sinkResult := manualSinkByChannelID[channel.Id]
			applied := sinkResult.Applied
			resultReason := "manually_disabled_at_bottom"
			if applied {
				appliedAny = true
				resultReason = "manually_disabled_sunk"
			}
			results = append(results, ChannelAutoPriorityRunResult{
				ChannelID: channel.Id,
				Applied:   applied,
				Reason:    resultReason,
				score: AutoPriorityScoreResult{
					ChannelID:        channel.Id,
					OldPriority:      sinkResult.OldPriority,
					ComputedPriority: sinkPriority,
					NewPriority:      sinkResult.NewPriority,
					Applied:          applied,
					Reason:           resultReason,
				},
			})
		}
	}

	remainingManuallyDisabledIDs := make([]int, 0, len(manuallyDisabledCandidates))
	for _, channel := range manuallyDisabledCandidates {
		if _, processed := processedManuallyDisabled[channel.Id]; processed {
			continue
		}
		if invalidGroups[strings.TrimSpace(channel.Group)] != "" {
			continue
		}
		remainingManuallyDisabledIDs = append(remainingManuallyDisabledIDs, channel.Id)
	}
	remainingSinkResults, err := sinkManuallyDisabledChannels(ctx, nil, remainingManuallyDisabledIDs, nil)
	if err != nil {
		return nil, err
	}
	for _, sinkResult := range remainingSinkResults {
		if sinkResult.Applied {
			appliedAny = true
			results = append(results, manuallyDisabledSinkRunResult(sinkResult))
		}
	}

	return results, nil
}

func manuallyDisabledSinkRunResult(sinkResult model.ManuallyDisabledChannelSinkResult) ChannelAutoPriorityRunResult {
	return ChannelAutoPriorityRunResult{
		ChannelID: sinkResult.ChannelID,
		Applied:   true,
		Reason:    "manually_disabled_sunk",
		score: AutoPriorityScoreResult{
			ChannelID:        sinkResult.ChannelID,
			OldPriority:      sinkResult.OldPriority,
			ComputedPriority: sinkResult.NewPriority,
			NewPriority:      sinkResult.NewPriority,
			Applied:          true,
			Reason:           "manually_disabled_sunk",
		},
	}
}

func persistChannelAutoPriorityGroup(
	ctx context.Context,
	candidates []upstreamSourceAutoPriorityCandidate,
	scores []AutoPriorityScoreResult,
	indexes []int,
	manuallyDisabled []model.Channel,
	sinkPriority int64,
	now int64,
) (string, []model.ManuallyDisabledChannelSinkResult, error) {
	var manualSinkResults []model.ManuallyDisabledChannelSinkResult
	err := model.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		empiricalEvidence := make([]upstreamSourceAutoPriorityCostEvidence, 0, len(indexes))
		seenEmpiricalEvidence := make(map[upstreamSourceAutoPriorityCostEvidence]struct{})
		for _, idx := range indexes {
			if candidates[idx].costEvidence.lastGoodGeneration != 0 {
				seenEmpiricalEvidence[candidates[idx].costEvidence] = struct{}{}
			}
			for _, evidence := range candidates[idx].cohortCostEvidence {
				if evidence.lastGoodGeneration != 0 {
					seenEmpiricalEvidence[evidence] = struct{}{}
				}
			}
		}
		for evidence := range seenEmpiricalEvidence {
			empiricalEvidence = append(empiricalEvidence, evidence)
		}
		sort.SliceStable(empiricalEvidence, func(i int, j int) bool {
			if empiricalEvidence[i].sourceID != empiricalEvidence[j].sourceID {
				return empiricalEvidence[i].sourceID < empiricalEvidence[j].sourceID
			}
			if empiricalEvidence[i].mappingID != empiricalEvidence[j].mappingID {
				return empiricalEvidence[i].mappingID < empiricalEvidence[j].mappingID
			}
			return empiricalEvidence[i].channelID < empiricalEvidence[j].channelID
		})
		for _, evidence := range empiricalEvidence {
			if err := revalidateUpstreamSourceAutoPriorityCostTx(tx, evidence, now); err != nil {
				return err
			}
		}
		for _, idx := range indexes {
			if err := updateAutoPriorityCandidate(tx, candidates[idx], scores[idx], now); err != nil {
				return err
			}
		}
		if len(manuallyDisabled) > 0 {
			channelIDs := make([]int, 0, len(manuallyDisabled))
			for _, channel := range manuallyDisabled {
				channelIDs = append(channelIDs, channel.Id)
			}
			var err error
			manualSinkResults, err = sinkManuallyDisabledChannels(ctx, tx, channelIDs, map[string]int64{
				strings.TrimSpace(manuallyDisabled[0].Group): sinkPriority,
			})
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err == nil {
		return "", manualSinkResults, nil
	}
	if errors.Is(err, errAutoPriorityGeneratedChannelChanged) {
		return "generated_channel_changed", nil, nil
	}
	if errors.Is(err, errAutoPriorityEmpiricalProbeChanged) {
		return "empirical_probe_changed", nil, nil
	}
	return "", nil, err
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
