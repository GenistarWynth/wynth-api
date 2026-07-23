package service

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAutoPrioritySeparatesNominalPriceAndCache(t *testing.T) {
	t.Run("same nominal rate keeps price equal while cache changes cache score and total", func(t *testing.T) {
		results := ScoreAutoPriorityCandidates([]AutoPriorityScoreInput{
			{
				ChannelID:               1,
				LocalGroup:              "shared",
				ChannelType:             constant.ChannelTypeOpenAI,
				EffectiveRateMultiplier: 1,
				CacheAdjustedCostFactor: 1,
				UsageLogCount:           20,
			},
			{
				ChannelID:               2,
				LocalGroup:              "shared",
				ChannelType:             constant.ChannelTypeOpenAI,
				EffectiveRateMultiplier: 1,
				CacheAdjustedCostFactor: autoPriorityMinCacheCostFactor,
				UsageLogCount:           20,
			},
		}, 1000)

		noBenefit := resultByChannelID(results, 1)
		cacheBenefit := resultByChannelID(results, 2)
		require.NotNil(t, noBenefit)
		require.NotNil(t, cacheBenefit)
		assert.Equal(t, noBenefit.EffectivePriceScore, cacheBenefit.EffectivePriceScore)
		assert.Less(t, noBenefit.CacheScore, cacheBenefit.CacheScore)
		assert.Less(t, noBenefit.FinalScore, cacheBenefit.FinalScore)
	})

	t.Run("eight times nominal gap dominates despite expensive cache advantage", func(t *testing.T) {
		results := ScoreAutoPriorityCandidates([]AutoPriorityScoreInput{
			{
				ChannelID:               11,
				LocalGroup:              "shared",
				ChannelType:             constant.ChannelTypeOpenAI,
				EffectiveRateMultiplier: 1,
				CacheAdjustedCostFactor: 1,
				UsageLogCount:           20,
				FirstTokenLatencyMS:     autoPriorityFirstTokenSlowMS,
				FirstTokenSampleCount:   1,
				ThroughputTps:           autoPriorityThroughputSlowTps,
				ThroughputSampleCount:   1,
				CohortCostFloor:         0.01,
			},
			{
				ChannelID:               12,
				LocalGroup:              "shared",
				ChannelType:             constant.ChannelTypeOpenAI,
				EffectiveRateMultiplier: 8,
				CacheAdjustedCostFactor: autoPriorityMinCacheCostFactor,
				UsageLogCount:           20,
				Availability:            floatPtr(1),
				MonitorCheckCount:       3,
				FirstTokenLatencyMS:     autoPriorityFirstTokenFastMS,
				FirstTokenSampleCount:   1,
				ThroughputTps:           autoPriorityThroughputFastTps,
				ThroughputSampleCount:   1,
				CohortCostFloor:         0.01,
			},
		}, 1000)

		cheap := resultByChannelID(results, 11)
		expensive := resultByChannelID(results, 12)
		require.NotNil(t, cheap)
		require.NotNil(t, expensive)
		assert.GreaterOrEqual(t, cheap.FinalScore-expensive.FinalScore, autoPriorityDominanceScoreMargin)
		assert.GreaterOrEqual(t, cheap.ComputedPriority-expensive.ComputedPriority, autoPriorityDominancePriorityMargin)
	})

	t.Run("cache cannot change nominal price floor normalization", func(t *testing.T) {
		results := ScoreAutoPriorityCandidates([]AutoPriorityScoreInput{
			{
				ChannelID:               21,
				LocalGroup:              "shared",
				ChannelType:             constant.ChannelTypeOpenAI,
				EffectiveRateMultiplier: 0.04,
				CacheAdjustedCostFactor: 1,
				UsageLogCount:           20,
				CohortCostFloor:         0.02,
			},
			{
				ChannelID:               22,
				LocalGroup:              "shared",
				ChannelType:             constant.ChannelTypeOpenAI,
				EffectiveRateMultiplier: 0.04,
				CacheAdjustedCostFactor: autoPriorityMinCacheCostFactor,
				UsageLogCount:           20,
				CohortCostFloor:         0.02,
			},
		}, 1000)

		noBenefit := resultByChannelID(results, 21)
		cacheBenefit := resultByChannelID(results, 22)
		require.NotNil(t, noBenefit)
		require.NotNil(t, cacheBenefit)
		assert.InDelta(t, 50, noBenefit.EffectivePriceScore, 1e-9)
		assert.InDelta(t, 50, cacheBenefit.EffectivePriceScore, 1e-9)
	})

	t.Run("close nominal price gap can be overturned by cache and quality", func(t *testing.T) {
		results := ScoreAutoPriorityCandidates([]AutoPriorityScoreInput{
			{
				ChannelID:               31,
				LocalGroup:              "shared",
				ChannelType:             constant.ChannelTypeOpenAI,
				EffectiveRateMultiplier: 1,
				CacheAdjustedCostFactor: 1,
				UsageLogCount:           20,
				Availability:            floatPtr(0.5),
				MonitorCheckCount:       3,
				FirstTokenLatencyMS:     autoPriorityFirstTokenSlowMS,
				FirstTokenSampleCount:   1,
				ThroughputTps:           autoPriorityThroughputSlowTps,
				ThroughputSampleCount:   1,
			},
			{
				ChannelID:               32,
				LocalGroup:              "shared",
				ChannelType:             constant.ChannelTypeOpenAI,
				EffectiveRateMultiplier: 1.1,
				CacheAdjustedCostFactor: autoPriorityMinCacheCostFactor,
				UsageLogCount:           20,
				Availability:            floatPtr(1),
				MonitorCheckCount:       3,
				FirstTokenLatencyMS:     autoPriorityFirstTokenFastMS,
				FirstTokenSampleCount:   1,
				ThroughputTps:           autoPriorityThroughputFastTps,
				ThroughputSampleCount:   1,
			},
		}, 1000)

		nominallyCheaper := resultByChannelID(results, 31)
		betterCacheAndQuality := resultByChannelID(results, 32)
		require.NotNil(t, nominallyCheaper)
		require.NotNil(t, betterCacheAndQuality)
		assert.Greater(t, nominallyCheaper.EffectivePriceScore, betterCacheAndQuality.EffectivePriceScore)
		assert.Greater(t, betterCacheAndQuality.CacheScore, nominallyCheaper.CacheScore)
		assert.Greater(t, betterCacheAndQuality.FinalScore, nominallyCheaper.FinalScore)
	})

	t.Run("cold start prior affects cache component only", func(t *testing.T) {
		results := ScoreAutoPriorityCandidates([]AutoPriorityScoreInput{
			{
				ChannelID:               41,
				LocalGroup:              "shared",
				ChannelType:             constant.ChannelTypeOpenAI,
				EffectiveRateMultiplier: 1,
				CacheAdjustedCostFactor: autoPriorityMinCacheCostFactor,
				UsageLogCount:           20,
			},
			{
				ChannelID:               42,
				LocalGroup:              "shared",
				ChannelType:             constant.ChannelTypeOpenAI,
				EffectiveRateMultiplier: 1,
			},
		}, 1000)

		mature := resultByChannelID(results, 41)
		fresh := resultByChannelID(results, 42)
		require.NotNil(t, mature)
		require.NotNil(t, fresh)
		assert.Equal(t, "cohort_prior", fresh.CacheFactorSource)
		assert.Equal(t, mature.EffectivePriceScore, fresh.EffectivePriceScore)
		assert.Greater(t, fresh.CacheScore, 0.0)
		assert.Less(t, fresh.CacheScore, mature.CacheScore)
	})
}

func TestAutoPrioritySnapshotSeparatesNominalPriceAndCacheDiagnostics(t *testing.T) {
	results := ScoreAutoPriorityCandidates([]AutoPriorityScoreInput{{
		ChannelID:               51,
		LocalGroup:              "shared",
		ChannelType:             constant.ChannelTypeOpenAI,
		EffectiveRateMultiplier: 0.02,
		CacheAdjustedCostFactor: 0.5,
		UsageLogCount:           20,
	}}, 1000)
	require.Len(t, results, 1)

	snapshot := buildChannelAutoPriorityScoreSnapshot(results[0], 100, 200)
	encoded, err := common.Marshal(snapshot)
	require.NoError(t, err)
	var diagnostics map[string]any
	require.NoError(t, common.Unmarshal(encoded, &diagnostics))

	assert.Equal(t, 0.02, diagnostics["effective_rate_multiplier"])
	assert.Equal(t, 0.02, diagnostics["nominal_rate_multiplier"])
	assert.Equal(t, diagnostics["effective_price_score"], diagnostics["nominal_price_score"])
	assert.Equal(t, results[0].CacheScore, diagnostics["cache_score"])
	assert.Equal(t, results[0].CacheAdjustedCostFactor, diagnostics["cache_adjusted_cost_factor"])
	assert.Equal(t, results[0].EffectiveCostMultiplier, diagnostics["effective_cost_multiplier"])
}
