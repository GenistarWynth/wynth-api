package service

import (
	"context"
	"time"

	"github.com/QuantumNous/new-api/model"
	relaydto "github.com/QuantumNous/new-api/relaykit/dto"
)

type autoPriorityChannelOwnership struct {
	settings      relaydto.ChannelOtherSettings
	settingsValid bool
	generated     bool
	mapping       model.UpstreamSourceChannelMapping
	mappingValid  bool
	invalidReason string
}

func loadAutoPriorityChannelOwnership(ctx context.Context, channels []model.Channel) (map[int]autoPriorityChannelOwnership, error) {
	ownershipByChannelID := make(map[int]autoPriorityChannelOwnership, len(channels))
	channelIDs := make([]int, 0, len(channels))
	for _, channel := range channels {
		channelIDs = append(channelIDs, channel.Id)
	}

	mappingsByChannelID := make(map[int][]model.UpstreamSourceChannelMapping, len(channels))
	if len(channelIDs) > 0 {
		var mappings []model.UpstreamSourceChannelMapping
		if err := model.DB.WithContext(ctx).
			Where("local_channel_id IN ?", channelIDs).
			Order("id").
			Find(&mappings).Error; err != nil {
			return nil, err
		}
		for _, mapping := range mappings {
			mappingsByChannelID[mapping.LocalChannelID] = append(mappingsByChannelID[mapping.LocalChannelID], mapping)
		}
	}

	for _, channel := range channels {
		settings, settingsValid := readChannelOtherSettingsForAutoPriorityDue(channel)
		ownership := autoPriorityChannelOwnership{
			settings:      settings,
			settingsValid: settingsValid,
		}
		mappings := mappingsByChannelID[channel.Id]
		claimsGenerated := settingsValid &&
			(settings.GeneratedByUpstreamSourceID != 0 || settings.GeneratedByUpstreamMappingID != 0)
		ownership.generated = claimsGenerated || len(mappings) > 0
		if !ownership.generated {
			ownershipByChannelID[channel.Id] = ownership
			continue
		}
		if !settingsValid || settings.GeneratedByUpstreamSourceID == 0 || settings.GeneratedByUpstreamMappingID == 0 {
			ownership.invalidReason = "generated_channel_metadata_mismatch"
			ownershipByChannelID[channel.Id] = ownership
			continue
		}
		for _, mapping := range mappings {
			if mapping.Id != settings.GeneratedByUpstreamMappingID || mapping.SourceID != settings.GeneratedByUpstreamSourceID {
				continue
			}
			ownership.mapping = mapping
			ownership.mappingValid = true
			break
		}
		if !ownership.mappingValid {
			ownership.invalidReason = "generated_channel_metadata_mismatch"
		}
		ownershipByChannelID[channel.Id] = ownership
	}
	return ownershipByChannelID, nil
}

type upstreamSourceAutoPriorityCostEvidence struct {
	sourceID                    int
	mappingID                   int
	channelID                   int
	lastGoodGeneration          int64
	lastGoodIdentityFingerprint string
	receivedAt                  int64
	freshUntil                  int64
	nominalRateMultiplier       float64
}

type resolvedUpstreamSourceAutoPriorityCost struct {
	costSource            string
	nominalRateMultiplier float64
	evidence              upstreamSourceAutoPriorityCostEvidence
}

func resolveUpstreamSourceAutoPriorityCost(
	now time.Time,
	source *model.UpstreamSource,
	mapping *model.UpstreamSourceChannelMapping,
	channel *model.Channel,
	probe *model.UpstreamSourceBillingProbe,
) (resolvedUpstreamSourceAutoPriorityCost, string) {
	if mapping == nil {
		return resolvedUpstreamSourceAutoPriorityCost{}, "mapping_missing"
	}
	costSource, valid := model.ResolveUpstreamSourceAutoPriorityCostSource(mapping.AutoPriorityCostSource)
	if !valid {
		return resolvedUpstreamSourceAutoPriorityCost{}, "invalid_auto_priority_cost_source"
	}
	if costSource == model.UpstreamSourceAutoPriorityCostSourceEmpiricalProbe {
		resolved := resolvedUpstreamSourceAutoPriorityCost{costSource: costSource}
		if probe == nil {
			return resolved, "empirical_probe_missing"
		}
		if !probe.Enabled {
			return resolved, "empirical_probe_disabled"
		}
		if probe.LastGoodGeneration <= 0 || probe.ReceivedAt <= 0 ||
			probe.LastGoodIdentityFingerprint == "" {
			return resolved, "empirical_probe_missing"
		}
		if probe.Status == model.UpstreamSourceBillingProbeStatusUnsupported {
			return resolved, "empirical_probe_unsupported"
		}
		if probe.Status != model.UpstreamSourceBillingProbeStatusOK &&
			probe.Status != model.UpstreamSourceBillingProbeStatusFailed {
			return resolved, "empirical_probe_malformed"
		}
		if probe.GroupRateMultiplier == nil || probe.ResolvedRateMultiplier == nil ||
			probe.PeakRateEnabled == nil || probe.EffectiveRateMultiplier == nil ||
			probe.ObservedAt == "" ||
			!validUpstreamSourceBillingMultiplier(*probe.GroupRateMultiplier) ||
			!isValidAutoPriorityMultiplier(*probe.ResolvedRateMultiplier) ||
			!validUpstreamSourceBillingMultiplier(*probe.EffectiveRateMultiplier) ||
			(probe.UserRateMultiplier != nil && !validUpstreamSourceBillingMultiplier(*probe.UserRateMultiplier)) ||
			(probe.AppliedPeakMultiplier != nil && !validUpstreamSourceBillingMultiplier(*probe.AppliedPeakMultiplier)) {
			return resolved, "empirical_probe_malformed"
		}
		observedAt, observedAtErr := time.Parse(time.RFC3339Nano, probe.ObservedAt)
		if observedAtErr != nil || observedAt.IsZero() {
			return resolved, "empirical_probe_malformed"
		}
		if probe.FreshUntil <= probe.ReceivedAt || now.UTC().Unix() >= probe.FreshUntil {
			return resolved, "empirical_probe_stale"
		}
		if source == nil || channel == nil {
			return resolved, "empirical_probe_ineligible"
		}
		rows := model.UpstreamSourceBillingProbeIdentityRows{
			Probe:   *probe,
			Source:  *source,
			Mapping: *mapping,
			Channel: *channel,
		}
		if !model.IsUpstreamSourceBillingProbeIdentityRowsEligible(&rows) {
			return resolved, "empirical_probe_ineligible"
		}
		identity, identityErr := upstreamSourceBillingProbeIdentityFromRows(probe, source, mapping, channel)
		if identityErr != nil {
			return resolved, "empirical_probe_identity_mismatch"
		}
		if identity.Fingerprint != probe.LastGoodIdentityFingerprint {
			return resolved, "empirical_probe_identity_mismatch"
		}
		nominalRateMultiplier := *probe.ResolvedRateMultiplier
		if *probe.PeakRateEnabled {
			if probe.PeakRateMultiplier == nil || !isValidAutoPriorityMultiplier(*probe.PeakRateMultiplier) ||
				probe.Timezone == "" || probe.Timezone == "Local" {
				return resolved, "empirical_probe_malformed"
			}
			startMinute, startOK := parseUpstreamSourceBillingClock(probe.PeakStart)
			endMinute, endOK := parseUpstreamSourceBillingClock(probe.PeakEnd)
			if !startOK || !endOK || startMinute >= endMinute {
				return resolved, "empirical_probe_malformed"
			}
			location, err := time.LoadLocation(probe.Timezone)
			if err != nil {
				return resolved, "empirical_probe_malformed"
			}
			localNow := now.In(location)
			currentMinute := localNow.Hour()*60 + localNow.Minute()
			if currentMinute >= startMinute && currentMinute < endMinute {
				nominalRateMultiplier *= *probe.PeakRateMultiplier
				if !isValidAutoPriorityMultiplier(nominalRateMultiplier) {
					return resolved, "empirical_probe_malformed"
				}
			}
		}
		resolved.nominalRateMultiplier = nominalRateMultiplier
		resolved.evidence = upstreamSourceAutoPriorityCostEvidence{
			sourceID:                    source.Id,
			mappingID:                   mapping.Id,
			channelID:                   channel.Id,
			lastGoodGeneration:          probe.LastGoodGeneration,
			lastGoodIdentityFingerprint: probe.LastGoodIdentityFingerprint,
			receivedAt:                  probe.ReceivedAt,
			freshUntil:                  probe.FreshUntil,
			nominalRateMultiplier:       resolved.nominalRateMultiplier,
		}
		return resolved, ""
	}
	if mapping.EffectiveRateMultiplier == nil || !isValidAutoPriorityMultiplier(*mapping.EffectiveRateMultiplier) {
		return resolvedUpstreamSourceAutoPriorityCost{costSource: costSource}, "missing_effective_rate_multiplier"
	}
	return resolvedUpstreamSourceAutoPriorityCost{
		costSource:            costSource,
		nominalRateMultiplier: *mapping.EffectiveRateMultiplier,
	}, ""
}
