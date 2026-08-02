package service

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaydto "github.com/QuantumNous/new-api/relaykit/dto"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func validEmpiricalAutoPriorityCostRows(t *testing.T, now time.Time) (model.UpstreamSource, model.UpstreamSourceChannelMapping, model.Channel, model.UpstreamSourceBillingProbe) {
	t.Helper()
	source := model.UpstreamSource{
		Id:           11,
		Type:         model.UpstreamSourceTypeSub2API,
		Status:       model.UpstreamSourceStatusEnabled,
		RelayBaseURL: "https://relay.example",
		SyncConfig:   `{}`,
	}
	advertised := 0.25
	mapping := model.UpstreamSourceChannelMapping{
		Id:                      22,
		SourceID:                source.Id,
		SyncEnabled:             true,
		DiscoveryStatus:         model.UpstreamMappingDiscoveryStatusActive,
		UpstreamKeyID:           "key-id",
		LocalChannelID:          33,
		EffectiveRateMultiplier: &advertised,
	}
	channel := model.Channel{
		Id:      mapping.LocalChannelID,
		Type:    constant.ChannelTypeOpenAI,
		Status:  common.ChannelStatusEnabled,
		Key:     "sk-empirical-cost-test",
		BaseURL: common.GetPointer(source.RelayBaseURL),
	}
	channel.SetOtherSettings(relaydto.ChannelOtherSettings{
		GeneratedByUpstreamSourceID:  source.Id,
		GeneratedByUpstreamMappingID: mapping.Id,
	})
	resolvedRate := 0.8
	groupRate := 0.8
	effectiveRate := 0.8
	peakEnabled := false
	probe := model.UpstreamSourceBillingProbe{
		SourceID:               source.Id,
		MappingID:              mapping.Id,
		Enabled:                true,
		IntervalMinutes:        5,
		Status:                 model.UpstreamSourceBillingProbeStatusOK,
		GroupRateMultiplier:    &groupRate,
		ResolvedRateMultiplier: &resolvedRate,
		PeakRateEnabled:        &peakEnabled,
		EffectiveRateMultiplier: &effectiveRate,
		ObservedAt:             now.Add(-time.Minute).UTC().Format(time.RFC3339Nano),
		ReceivedAt:             now.Add(-time.Minute).Unix(),
		FreshUntil:             now.Add(time.Hour).Unix(),
		LastGoodGeneration:     7,
	}
	identity, identityErr := upstreamSourceBillingProbeIdentityFromRows(&probe, &source, &mapping, &channel)
	require.Nil(t, identityErr)
	probe.LastGoodIdentityFingerprint = identity.Fingerprint
	return source, mapping, channel, probe
}

func TestResolveUpstreamSourceAutoPriorityCostFailsClosedForUnsafeEmpiricalState(t *testing.T) {
	now := time.Date(2026, time.August, 1, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name            string
		mutate          func(*model.UpstreamSource, *model.UpstreamSourceChannelMapping, *model.Channel, *model.UpstreamSourceBillingProbe)
		authorizeLatest bool
		wantReason      string
	}{
		{
			name: "disabled is distinct from missing",
			mutate: func(_ *model.UpstreamSource, _ *model.UpstreamSourceChannelMapping, _ *model.Channel, probe *model.UpstreamSourceBillingProbe) {
				probe.Enabled = false
				probe.LastGoodGeneration = 0
				probe.LastGoodIdentityFingerprint = ""
			},
			wantReason: "empirical_probe_disabled",
		},
		{
			name: "missing last good generation",
			mutate: func(_ *model.UpstreamSource, _ *model.UpstreamSourceChannelMapping, _ *model.Channel, probe *model.UpstreamSourceBillingProbe) {
				probe.LastGoodGeneration = 0
			},
			wantReason: "empirical_probe_missing",
		},
		{
			name: "unsupported invalidates retained data",
			mutate: func(_ *model.UpstreamSource, _ *model.UpstreamSourceChannelMapping, _ *model.Channel, probe *model.UpstreamSourceBillingProbe) {
				probe.Status = model.UpstreamSourceBillingProbeStatusUnsupported
			},
			wantReason: "empirical_probe_unsupported",
		},
		{
			name: "idle status is malformed",
			mutate: func(_ *model.UpstreamSource, _ *model.UpstreamSourceChannelMapping, _ *model.Channel, probe *model.UpstreamSourceBillingProbe) {
				probe.Status = model.UpstreamSourceBillingProbeStatusIdle
			},
			wantReason: "empirical_probe_malformed",
		},
		{
			name: "missing sanitized component is malformed",
			mutate: func(_ *model.UpstreamSource, _ *model.UpstreamSourceChannelMapping, _ *model.Channel, probe *model.UpstreamSourceBillingProbe) {
				probe.EffectiveRateMultiplier = nil
			},
			wantReason: "empirical_probe_malformed",
		},
		{
			name: "freshness expires on destination clock boundary",
			mutate: func(_ *model.UpstreamSource, _ *model.UpstreamSourceChannelMapping, _ *model.Channel, probe *model.UpstreamSourceBillingProbe) {
				probe.FreshUntil = now.Unix()
			},
			wantReason: "empirical_probe_stale",
		},
		{
			name: "generated mapping ownership mismatch",
			mutate: func(_ *model.UpstreamSource, _ *model.UpstreamSourceChannelMapping, channel *model.Channel, _ *model.UpstreamSourceBillingProbe) {
				settings := channel.GetOtherSettings()
				settings.GeneratedByUpstreamMappingID++
				channel.SetOtherSettings(settings)
			},
			wantReason: "empirical_probe_ineligible",
		},
		{
			name: "latest attempt identity never authorizes retained last good",
			mutate: func(_ *model.UpstreamSource, _ *model.UpstreamSourceChannelMapping, channel *model.Channel, _ *model.UpstreamSourceBillingProbe) {
				channel.Key = "sk-latest-attempt"
			},
			authorizeLatest: true,
			wantReason:      "empirical_probe_identity_mismatch",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source, mapping, channel, probe := validEmpiricalAutoPriorityCostRows(t, now)
			mapping.AutoPriorityCostSource = model.UpstreamSourceAutoPriorityCostSourceEmpiricalProbe
			mapping.EffectiveRateMultiplier = nil
			tt.mutate(&source, &mapping, &channel, &probe)
			if tt.authorizeLatest {
				latest, latestErr := upstreamSourceBillingProbeIdentityFromRows(&probe, &source, &mapping, &channel)
				require.Nil(t, latestErr)
				probe.ClaimIdentityFingerprint = latest.Fingerprint
			}

			resolved, reason := resolveUpstreamSourceAutoPriorityCost(now, &source, &mapping, &channel, &probe)

			assert.Equal(t, tt.wantReason, reason)
			assert.Zero(t, resolved.nominalRateMultiplier)
		})
	}
}

func TestResolveUpstreamSourceAutoPriorityCostDefaultsAdvertisedAndNeverConsumesEmpiricalImplicitly(t *testing.T) {
	now := time.Date(2026, time.August, 1, 12, 0, 0, 0, time.UTC)
	source, mapping, channel, probe := validEmpiricalAutoPriorityCostRows(t, now)

	resolved, reason := resolveUpstreamSourceAutoPriorityCost(now, &source, &mapping, &channel, &probe)
	require.Empty(t, reason)
	assert.Equal(t, model.UpstreamSourceAutoPriorityCostSourceAdvertised, resolved.costSource)
	assert.Equal(t, 0.25, resolved.nominalRateMultiplier)
	assert.Zero(t, resolved.evidence.lastGoodGeneration)

	mapping.EffectiveRateMultiplier = nil
	resolved, reason = resolveUpstreamSourceAutoPriorityCost(now, &source, &mapping, &channel, &probe)
	assert.Equal(t, "missing_effective_rate_multiplier", reason)
	assert.Zero(t, resolved.nominalRateMultiplier)
}

func TestResolveUpstreamSourceAutoPriorityCostConsumesRetainedEmpiricalUntilFreshnessExpiry(t *testing.T) {
	now := time.Date(2026, time.August, 1, 12, 0, 0, 0, time.UTC)
	source, mapping, channel, probe := validEmpiricalAutoPriorityCostRows(t, now)
	mapping.AutoPriorityCostSource = model.UpstreamSourceAutoPriorityCostSourceEmpiricalProbe
	mapping.EffectiveRateMultiplier = nil

	probe.Status = model.UpstreamSourceBillingProbeStatusFailed
	resolved, reason := resolveUpstreamSourceAutoPriorityCost(now, &source, &mapping, &channel, &probe)
	require.Empty(t, reason)
	assert.Equal(t, model.UpstreamSourceAutoPriorityCostSourceEmpiricalProbe, resolved.costSource)
	assert.Equal(t, 0.8, resolved.nominalRateMultiplier)
	assert.Equal(t, probe.LastGoodGeneration, resolved.evidence.lastGoodGeneration)
	assert.Equal(t, probe.LastGoodIdentityFingerprint, resolved.evidence.lastGoodIdentityFingerprint)
	assert.Equal(t, probe.ReceivedAt, resolved.evidence.receivedAt)
	assert.Equal(t, probe.FreshUntil, resolved.evidence.freshUntil)

	resolved, reason = resolveUpstreamSourceAutoPriorityCost(
		time.Unix(probe.FreshUntil, 0),
		&source,
		&mapping,
		&channel,
		&probe,
	)
	assert.Equal(t, "empirical_probe_stale", reason)
	assert.Zero(t, resolved.nominalRateMultiplier)
}

func TestResolveUpstreamSourceAutoPriorityCostRecomputesCurrentPeakAcrossTimezoneBoundaries(t *testing.T) {
	base := time.Date(2026, time.August, 1, 12, 0, 0, 0, time.UTC)
	source, mapping, channel, probe := validEmpiricalAutoPriorityCostRows(t, base)
	mapping.AutoPriorityCostSource = model.UpstreamSourceAutoPriorityCostSourceEmpiricalProbe
	mapping.EffectiveRateMultiplier = nil
	resolvedRate := 0.5
	peakEnabled := true
	peakFactor := 2.0
	storedAppliedDiagnostic := 99.0
	storedEffectiveDiagnostic := 123.0
	probe.ResolvedRateMultiplier = &resolvedRate
	probe.PeakRateEnabled = &peakEnabled
	probe.PeakStart = "09:00"
	probe.PeakEnd = "17:00"
	probe.PeakRateMultiplier = &peakFactor
	probe.AppliedPeakMultiplier = &storedAppliedDiagnostic
	probe.EffectiveRateMultiplier = &storedEffectiveDiagnostic
	probe.Timezone = "America/New_York"
	probe.ReceivedAt = base.Add(-time.Hour).Unix()
	probe.FreshUntil = base.Add(24 * time.Hour).Unix()

	tests := []struct {
		name string
		now  time.Time
		want float64
	}{
		{name: "pre peak", now: time.Date(2026, time.August, 1, 12, 59, 0, 0, time.UTC), want: 0.5},
		{name: "inclusive peak start", now: time.Date(2026, time.August, 1, 13, 0, 0, 0, time.UTC), want: 1.0},
		{name: "exclusive peak end", now: time.Date(2026, time.August, 1, 21, 0, 0, 0, time.UTC), want: 0.5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolved, reason := resolveUpstreamSourceAutoPriorityCost(tt.now, &source, &mapping, &channel, &probe)
			require.Empty(t, reason)
			assert.Equal(t, tt.want, resolved.nominalRateMultiplier)
			assert.Equal(t, tt.want, resolved.evidence.nominalRateMultiplier)
		})
	}

	probe.Timezone = "Asia/Tokyo"
	resolved, reason := resolveUpstreamSourceAutoPriorityCost(
		time.Date(2026, time.August, 1, 1, 0, 0, 0, time.UTC),
		&source,
		&mapping,
		&channel,
		&probe,
	)
	require.Empty(t, reason)
	assert.Equal(t, 1.0, resolved.nominalRateMultiplier, "10:00 in Tokyo must be inside the stored 09:00-17:00 window")
}
