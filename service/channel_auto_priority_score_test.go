package service

import (
	"math"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAutoPriorityScoreWeights(t *testing.T) {
	assert.Equal(t, 0.75, autoPriorityPriceWeight)
	assert.Equal(t, 0.10, autoPriorityCacheWeight)
	assert.Equal(t, 0.08, autoPriorityAvailabilityWeight)
	assert.Equal(t, 0.03, autoPriorityFirstTokenWeight)
	assert.Equal(t, 0.04, autoPriorityThroughputWeight)
	assert.InDelta(t, 1.0, autoPriorityPriceWeight+autoPriorityCacheWeight+autoPriorityAvailabilityWeight+autoPriorityFirstTokenWeight+autoPriorityThroughputWeight, 1e-12)
	assert.Equal(t, 8.0, autoPriorityExtremeCostRatio)
}

func TestScoreAutoPriorityCandidates(t *testing.T) {
	t.Run("price dominates within the same cohort", func(t *testing.T) {
		results := ScoreAutoPriorityCandidates([]AutoPriorityScoreInput{
			{
				ChannelID:               101,
				LocalGroup:              " shared ",
				ChannelType:             constant.ChannelTypeOpenAI,
				CurrentPriority:         500,
				EffectiveRateMultiplier: 1,
				// Healthy availability: this subtest pins price dominance among
				// healthy channels; low availability is the gate subtests' job.
				Availability:          floatPtr(0.9),
				FirstTokenLatencyMS:   2000,
				UsageLogCount:         4,
				MonitorCheckCount:     8,
				FirstTokenSampleCount: 2,
			},
			{
				ChannelID:               102,
				LocalGroup:              "shared",
				ChannelType:             constant.ChannelTypeOpenAI,
				CurrentPriority:         500,
				EffectiveRateMultiplier: 5,
				Availability:            floatPtr(1),
				FirstTokenLatencyMS:     50,
				UsageLogCount:           4,
				MonitorCheckCount:       8,
				FirstTokenSampleCount:   2,
			},
		}, 1000)

		require.Len(t, results, 2)
		cheap := resultByChannelID(results, 101)
		expensive := resultByChannelID(results, 102)

		require.NotNil(t, cheap)
		require.NotNil(t, expensive)
		assert.Equal(t, "shared#1", cheap.Cohort)
		assert.Equal(t, "shared#1", expensive.Cohort)
		assert.Greater(t, cheap.EffectivePriceScore, expensive.EffectivePriceScore)
		assert.Greater(t, cheap.FinalScore, expensive.FinalScore)
		assert.Greater(t, cheap.ComputedPriority, expensive.ComputedPriority)
	})

	t.Run("hard unavailable member keeps price diagnostics without changing priority", func(t *testing.T) {
		results := ScoreAutoPriorityCandidates([]AutoPriorityScoreInput{
			{
				ChannelID:               1,
				LocalGroup:              "OpenAI",
				ChannelType:             constant.ChannelTypeOpenAI,
				CurrentPriority:         321,
				EffectiveRateMultiplier: 0.02,
				HardUnavailable:         true,
			},
			{
				ChannelID:               2,
				LocalGroup:              "OpenAI",
				ChannelType:             constant.ChannelTypeOpenAI,
				CurrentPriority:         222,
				EffectiveRateMultiplier: 0.06,
			},
		}, 1000)

		require.Len(t, results, 2)
		unavailable := results[0]
		assert.Equal(t, 100.0, unavailable.NominalPriceScore)
		assert.Equal(t, 0.0, unavailable.AvailabilityScore)
		assert.Equal(t, 0.0, unavailable.FinalScore)
		assert.Equal(t, int64(0), unavailable.ComputedPriority)
		assert.Equal(t, int64(321), unavailable.NewPriority)
		assert.False(t, unavailable.Applied)
		assert.Equal(t, "channel_auto_disabled", unavailable.Reason)
		assert.InDelta(t, 100.0/3.0, results[1].NominalPriceScore, 1e-9)
	})

	t.Run("low availability gates a cheap channel below a healthy pricier one", func(t *testing.T) {
		results := ScoreAutoPriorityCandidates([]AutoPriorityScoreInput{
			{
				ChannelID:               111,
				LocalGroup:              "gated",
				ChannelType:             constant.ChannelTypeOpenAI,
				EffectiveRateMultiplier: 1,
				Availability:            floatPtr(0.10),
				MonitorCheckCount:       8,
			},
			{
				ChannelID:               112,
				LocalGroup:              "gated",
				ChannelType:             constant.ChannelTypeOpenAI,
				EffectiveRateMultiplier: 5,
				Availability:            floatPtr(1),
				MonitorCheckCount:       8,
			},
		}, 1000)

		require.Len(t, results, 2)
		cheap := resultByChannelID(results, 111)
		expensive := resultByChannelID(results, 112)
		require.NotNil(t, cheap)
		require.NotNil(t, expensive)
		// Price still favors the cheap channel, but the availability gate must
		// push its overall score and priority below the healthy pricier one.
		assert.Greater(t, cheap.EffectivePriceScore, expensive.EffectivePriceScore)
		assert.Less(t, cheap.FinalScore, expensive.FinalScore)
		assert.Less(t, cheap.ComputedPriority, expensive.ComputedPriority)
	})

	t.Run("zero availability zeroes the priority", func(t *testing.T) {
		results := ScoreAutoPriorityCandidates([]AutoPriorityScoreInput{{
			ChannelID:               121,
			LocalGroup:              "dead",
			ChannelType:             constant.ChannelTypeOpenAI,
			EffectiveRateMultiplier: 0.03,
			Availability:            floatPtr(0),
			MonitorCheckCount:       5,
			CohortCostFloor:         0.02,
			CohortCostCeil:          0.08,
		}}, 1000)

		require.Len(t, results, 1)
		assert.Equal(t, float64(0), results[0].FinalScore)
		assert.Equal(t, int64(0), results[0].ComputedPriority)
	})

	t.Run("availability at the gate knee stays neutral", func(t *testing.T) {
		results := ScoreAutoPriorityCandidates([]AutoPriorityScoreInput{{
			ChannelID:               122,
			LocalGroup:              "knee",
			ChannelType:             constant.ChannelTypeOpenAI,
			EffectiveRateMultiplier: 1,
			Availability:            floatPtr(0.5),
			MonitorCheckCount:       8,
		}}, 1000)

		require.Len(t, results, 1)
		// gate == 1 at the knee: FinalScore is the ungated weighted sum. No first-token
		// or throughput samples, so both use the neutral default. With no own usage,
		// the independent cache component contributes its exact default score of 95.
		assert.InDelta(t, 0.75*100+0.10*95+0.08*50+0.03*autoPriorityNeutralPerfScore+0.04*autoPriorityNeutralPerfScore, results[0].FinalScore, 1e-9)
	})

	t.Run("too few monitor checks bypass the availability gate", func(t *testing.T) {
		results := ScoreAutoPriorityCandidates([]AutoPriorityScoreInput{{
			ChannelID:               123,
			LocalGroup:              "fresh",
			ChannelType:             constant.ChannelTypeOpenAI,
			EffectiveRateMultiplier: 1,
			Availability:            floatPtr(0),
			MonitorCheckCount:       2,
		}}, 1000)

		require.Len(t, results, 1)
		// Only the additive availability penalty applies; the gate is off. No first-token
		// or throughput samples, so both use the neutral default. Cache contributes
		// the fixed zero-sample score of 95.
		assert.InDelta(t, 0.75*100+0.10*95+0.08*0+0.03*autoPriorityNeutralPerfScore+0.04*autoPriorityNeutralPerfScore, results[0].FinalScore, 1e-9)
		assert.Equal(t, int64(894), results[0].ComputedPriority)
	})

	t.Run("same price in different cohorts still scores as 100", func(t *testing.T) {
		results := ScoreAutoPriorityCandidates([]AutoPriorityScoreInput{
			{
				ChannelID:               201,
				LocalGroup:              "alpha",
				ChannelType:             constant.ChannelTypeOpenAI,
				CurrentPriority:         300,
				EffectiveRateMultiplier: 2,
				Availability:            floatPtr(0.9),
				FirstTokenLatencyMS:     100,
				UsageLogCount:           1,
				MonitorCheckCount:       1,
				FirstTokenSampleCount:   1,
			},
			{
				ChannelID:               202,
				LocalGroup:              "beta",
				ChannelType:             constant.ChannelTypeAnthropic,
				CurrentPriority:         300,
				EffectiveRateMultiplier: 2,
				Availability:            floatPtr(0.9),
				FirstTokenLatencyMS:     100,
				UsageLogCount:           1,
				MonitorCheckCount:       1,
				FirstTokenSampleCount:   1,
			},
		}, 1000)

		require.Len(t, results, 2)
		assert.InDelta(t, 100, resultByChannelID(results, 201).EffectivePriceScore, 0.0001)
		assert.InDelta(t, 100, resultByChannelID(results, 202).EffectivePriceScore, 0.0001)
	})

	t.Run("cache adjusted cost stays diagnostic and does not redefine nominal price", func(t *testing.T) {
		results := ScoreAutoPriorityCandidates([]AutoPriorityScoreInput{
			{
				ChannelID:               301,
				LocalGroup:              "shared",
				ChannelType:             constant.ChannelTypeOpenAI,
				CurrentPriority:         400,
				EffectiveRateMultiplier: 1.0,
				CacheAdjustedCostFactor: 3.0,
				Availability:            floatPtr(0.8),
				FirstTokenLatencyMS:     120,
				UsageLogCount:           20,
				MonitorCheckCount:       3,
				FirstTokenSampleCount:   1,
			},
			{
				ChannelID:               302,
				LocalGroup:              "shared",
				ChannelType:             constant.ChannelTypeOpenAI,
				CurrentPriority:         400,
				EffectiveRateMultiplier: 1.5,
				CacheAdjustedCostFactor: 0.5,
				Availability:            floatPtr(0.8),
				FirstTokenLatencyMS:     120,
				UsageLogCount:           20,
				MonitorCheckCount:       3,
				FirstTokenSampleCount:   1,
			},
		}, 1000)

		require.Len(t, results, 2)
		r301 := resultByChannelID(results, 301)
		r302 := resultByChannelID(results, 302)

		require.NotNil(t, r301)
		require.NotNil(t, r302)
		assert.InDelta(t, 3.0, r301.EffectiveCostMultiplier, 0.0001)
		assert.InDelta(t, 0.75, r302.EffectiveCostMultiplier, 0.0001)
		assert.Greater(t, r301.EffectivePriceScore, r302.EffectivePriceScore)
		assert.Less(t, r301.CacheScore, r302.CacheScore)
		assert.Greater(t, r301.ComputedPriority, r302.ComputedPriority)
	})

	t.Run("low usage samples continuously blend cache benefit", func(t *testing.T) {
		results := ScoreAutoPriorityCandidates([]AutoPriorityScoreInput{
			{
				ChannelID:               311,
				LocalGroup:              "shared",
				ChannelType:             constant.ChannelTypeOpenAI,
				CurrentPriority:         400,
				EffectiveRateMultiplier: 1.0,
				CacheAdjustedCostFactor: 0.2,
				Availability:            floatPtr(0.8),
				FirstTokenLatencyMS:     120,
				UsageLogCount:           4,
				MonitorCheckCount:       3,
				FirstTokenSampleCount:   1,
			},
		}, 1000)

		require.Len(t, results, 1)
		assert.InDelta(t, 0.376, results[0].CacheAdjustedCostFactor, 0.0001)
		assert.InDelta(t, 0.376, results[0].EffectiveCostMultiplier, 0.0001)
	})

	t.Run("partial usage sample blends cache benefit from default 95", func(t *testing.T) {
		results := ScoreAutoPriorityCandidates([]AutoPriorityScoreInput{
			{
				ChannelID:               312,
				LocalGroup:              "shared",
				ChannelType:             constant.ChannelTypeOpenAI,
				CurrentPriority:         400,
				EffectiveRateMultiplier: 2.0,
				CacheAdjustedCostFactor: 0.5,
				Availability:            floatPtr(0.8),
				FirstTokenLatencyMS:     120,
				UsageLogCount:           10,
				MonitorCheckCount:       3,
				FirstTokenSampleCount:   1,
			},
		}, 1000)

		require.Len(t, results, 1)
		assert.InDelta(t, 0.44125, results[0].CacheAdjustedCostFactor, 0.0001)
		assert.InDelta(t, 0.8825, results[0].EffectiveCostMultiplier, 0.0001)
	})

	t.Run("cache benefit is capped for full sample", func(t *testing.T) {
		results := ScoreAutoPriorityCandidates([]AutoPriorityScoreInput{
			{
				ChannelID:               313,
				LocalGroup:              "shared",
				ChannelType:             constant.ChannelTypeOpenAI,
				CurrentPriority:         400,
				EffectiveRateMultiplier: 2.0,
				CacheAdjustedCostFactor: 0.05,
				Availability:            floatPtr(0.8),
				FirstTokenLatencyMS:     120,
				UsageLogCount:           20,
				MonitorCheckCount:       3,
				FirstTokenSampleCount:   1,
			},
		}, 1000)

		require.Len(t, results, 1)
		assert.InDelta(t, 0.35, results[0].CacheAdjustedCostFactor, 0.0001)
		assert.InDelta(t, 0.7, results[0].EffectiveCostMultiplier, 0.0001)
	})

	t.Run("previous cost snapshot smooths effective cost", func(t *testing.T) {
		results := ScoreAutoPriorityCandidates([]AutoPriorityScoreInput{
			{
				ChannelID:                       314,
				LocalGroup:                      "shared",
				ChannelType:                     constant.ChannelTypeOpenAI,
				CurrentPriority:                 400,
				EffectiveRateMultiplier:         2.0,
				CacheAdjustedCostFactor:         0.5,
				PreviousEffectiveCostMultiplier: 3.0,
				Availability:                    floatPtr(0.8),
				FirstTokenLatencyMS:             120,
				UsageLogCount:                   20,
				MonitorCheckCount:               3,
				FirstTokenSampleCount:           1,
				HasPreviousSnapshot:             true,
			},
		}, 1000)

		require.Len(t, results, 1)
		assert.InDelta(t, 0.5, results[0].CacheAdjustedCostFactor, 0.0001)
		assert.InDelta(t, 1.7, results[0].EffectiveCostMultiplier, 0.0001)
	})

	t.Run("hysteresis blocks small changes when a previous snapshot exists", func(t *testing.T) {
		results := ScoreAutoPriorityCandidates([]AutoPriorityScoreInput{
			{
				ChannelID:               401,
				LocalGroup:              "shared",
				ChannelType:             constant.ChannelTypeOpenAI,
				CurrentPriority:         950,
				EffectiveRateMultiplier: 1,
				Availability:            floatPtr(0.7),
				FirstTokenLatencyMS:     100,
				UsageLogCount:           1,
				MonitorCheckCount:       1,
				FirstTokenSampleCount:   1,
				HasPreviousSnapshot:     true,
			},
		}, 1000)

		require.Len(t, results, 1)
		result := results[0]
		assert.Equal(t, int64(950), result.OldPriority)
		assert.Equal(t, int64(954), result.ComputedPriority)
		assert.False(t, result.Applied)
		assert.Equal(t, int64(950), result.NewPriority)
		assert.Equal(t, "hysteresis_delta_below_threshold", result.Reason)
	})

	t.Run("small changes apply when there is no previous snapshot", func(t *testing.T) {
		results := ScoreAutoPriorityCandidates([]AutoPriorityScoreInput{
			{
				ChannelID:               402,
				LocalGroup:              "shared",
				ChannelType:             constant.ChannelTypeOpenAI,
				CurrentPriority:         867,
				EffectiveRateMultiplier: 1,
				Availability:            floatPtr(0.7),
				FirstTokenLatencyMS:     100,
				UsageLogCount:           1,
				MonitorCheckCount:       1,
				FirstTokenSampleCount:   1,
				HasPreviousSnapshot:     false,
			},
		}, 1000)

		require.Len(t, results, 1)
		result := results[0]
		assert.True(t, result.Applied)
		assert.Equal(t, result.ComputedPriority, result.NewPriority)
		assert.Empty(t, result.Reason)
	})

	t.Run("invalid multiplier does not affect valid cohort scoring", func(t *testing.T) {
		validOnly := ScoreAutoPriorityCandidates([]AutoPriorityScoreInput{
			{
				ChannelID:               501,
				LocalGroup:              "shared",
				ChannelType:             constant.ChannelTypeOpenAI,
				CurrentPriority:         600,
				EffectiveRateMultiplier: 1.25,
				Availability:            floatPtr(0.85),
				FirstTokenLatencyMS:     180,
				UsageLogCount:           3,
				MonitorCheckCount:       4,
				FirstTokenSampleCount:   2,
			},
		}, 1000)

		withInvalid := ScoreAutoPriorityCandidates([]AutoPriorityScoreInput{
			{
				ChannelID:               501,
				LocalGroup:              "shared",
				ChannelType:             constant.ChannelTypeOpenAI,
				CurrentPriority:         600,
				EffectiveRateMultiplier: 1.25,
				Availability:            floatPtr(0.85),
				FirstTokenLatencyMS:     180,
				UsageLogCount:           3,
				MonitorCheckCount:       4,
				FirstTokenSampleCount:   2,
			},
			{
				ChannelID:               502,
				LocalGroup:              "shared",
				ChannelType:             constant.ChannelTypeOpenAI,
				CurrentPriority:         777,
				EffectiveRateMultiplier: 0,
				Availability:            floatPtr(1),
				FirstTokenLatencyMS:     1,
				UsageLogCount:           99,
				MonitorCheckCount:       99,
				FirstTokenSampleCount:   99,
			},
		}, 1000)

		require.Len(t, validOnly, 1)
		require.Len(t, withInvalid, 2)

		validSolo := validOnly[0]
		validMixed := resultByChannelID(withInvalid, 501)
		invalid := resultByChannelID(withInvalid, 502)

		require.NotNil(t, validMixed)
		require.NotNil(t, invalid)
		assert.Equal(t, "missing_effective_rate_multiplier", invalid.Reason)
		assert.False(t, invalid.Applied)
		assert.Equal(t, invalid.OldPriority, invalid.ComputedPriority)
		assert.Equal(t, invalid.OldPriority, invalid.NewPriority)
		assert.Equal(t, validSolo.EffectivePriceScore, validMixed.EffectivePriceScore)
		assert.Equal(t, validSolo.FinalScore, validMixed.FinalScore)
		assert.Equal(t, validSolo.ComputedPriority, validMixed.ComputedPriority)
	})

	t.Run("missing availability and first token samples use neutral defaults", func(t *testing.T) {
		results := ScoreAutoPriorityCandidates([]AutoPriorityScoreInput{
			{
				ChannelID:               601,
				LocalGroup:              "shared",
				ChannelType:             constant.ChannelTypeOpenAI,
				CurrentPriority:         200,
				EffectiveRateMultiplier: 1,
				UsageLogCount:           0,
				MonitorCheckCount:       0,
				FirstTokenSampleCount:   0,
			},
		}, 1000)

		require.Len(t, results, 1)
		result := results[0]
		assert.InDelta(t, 100, result.EffectivePriceScore, 0.0001)
		assert.InDelta(t, 70, result.AvailabilityScore, 0.0001)
		assert.InDelta(t, 70, result.FirstTokenScore, 0.0001)
		assert.Equal(t, int64(0), result.UsageLogCount)
		assert.Equal(t, int64(0), result.MonitorCheckCount)
		assert.Equal(t, int64(0), result.FirstTokenSampleCount)
	})

	t.Run("first token is scored on an absolute curve: fast beats neutral, slow falls below", func(t *testing.T) {
		results := ScoreAutoPriorityCandidates([]AutoPriorityScoreInput{
			{
				ChannelID:               602,
				LocalGroup:              "shared",
				ChannelType:             constant.ChannelTypeOpenAI,
				CurrentPriority:         200,
				EffectiveRateMultiplier: 1,
				FirstTokenLatencyMS:     2000, // below the fast anchor -> 100
				UsageLogCount:           1,
				MonitorCheckCount:       1,
				FirstTokenSampleCount:   1,
			},
			{
				ChannelID:               603,
				LocalGroup:              "shared",
				ChannelType:             constant.ChannelTypeOpenAI,
				CurrentPriority:         200,
				EffectiveRateMultiplier: 1,
				FirstTokenLatencyMS:     90000, // above the slow anchor -> 0
				UsageLogCount:           1,
				MonitorCheckCount:       1,
				FirstTokenSampleCount:   1,
			},
		}, 1000)

		require.Len(t, results, 2)
		fast := resultByChannelID(results, 602)
		slow := resultByChannelID(results, 603)

		require.NotNil(t, fast)
		require.NotNil(t, slow)
		assert.InDelta(t, 100, fast.FirstTokenScore, 0.0001)
		assert.InDelta(t, 0, slow.FirstTokenScore, 0.0001)
		assert.Greater(t, fast.FirstTokenScore, slow.FirstTokenScore)
	})

	t.Run("a lone slow channel is not auto-scored 100 for first token", func(t *testing.T) {
		// Regression: previously a single-member cohort was forced to 100 regardless of
		// latency, so a channel serving 90s-first-token requests looked perfect.
		results := ScoreAutoPriorityCandidates([]AutoPriorityScoreInput{{
			ChannelID:               610,
			LocalGroup:              "solo",
			ChannelType:             constant.ChannelTypeOpenAI,
			CurrentPriority:         200,
			EffectiveRateMultiplier: 1,
			FirstTokenLatencyMS:     90000,
			UsageLogCount:           1,
			MonitorCheckCount:       1,
			FirstTokenSampleCount:   1,
		}}, 1000)

		require.Len(t, results, 1)
		assert.InDelta(t, 0, results[0].FirstTokenScore, 0.0001)
	})

	t.Run("throughput is scored on an absolute curve and a lone slow channel scores low", func(t *testing.T) {
		results := ScoreAutoPriorityCandidates([]AutoPriorityScoreInput{
			{
				ChannelID:               620,
				LocalGroup:              "tput",
				ChannelType:             constant.ChannelTypeOpenAI,
				CurrentPriority:         200,
				EffectiveRateMultiplier: 1,
				ThroughputTps:           50, // above the fast anchor -> 100
				UsageLogCount:           1,
				MonitorCheckCount:       1,
				ThroughputSampleCount:   1,
			},
			{
				ChannelID:               621,
				LocalGroup:              "tput",
				ChannelType:             constant.ChannelTypeOpenAI,
				CurrentPriority:         200,
				EffectiveRateMultiplier: 1,
				ThroughputTps:           2, // near the slow anchor -> well below neutral
				UsageLogCount:           1,
				MonitorCheckCount:       1,
				ThroughputSampleCount:   1,
			},
		}, 1000)

		require.Len(t, results, 2)
		fast := resultByChannelID(results, 620)
		slow := resultByChannelID(results, 621)
		require.NotNil(t, fast)
		require.NotNil(t, slow)
		assert.InDelta(t, 100, fast.ThroughputScore, 0.0001)
		assert.Greater(t, fast.ThroughputScore, slow.ThroughputScore)
		assert.Less(t, slow.ThroughputScore, autoPriorityNeutralPerfScore)
	})

	t.Run("missing throughput samples use the neutral default", func(t *testing.T) {
		results := ScoreAutoPriorityCandidates([]AutoPriorityScoreInput{{
			ChannelID:               630,
			LocalGroup:              "tput",
			ChannelType:             constant.ChannelTypeOpenAI,
			CurrentPriority:         200,
			EffectiveRateMultiplier: 1,
			UsageLogCount:           1,
			MonitorCheckCount:       1,
			ThroughputSampleCount:   0,
		}}, 1000)

		require.Len(t, results, 1)
		assert.InDelta(t, autoPriorityNeutralPerfScore, results[0].ThroughputScore, 0.0001)
	})

	t.Run("negative latency with sample count stays neutral", func(t *testing.T) {
		results := ScoreAutoPriorityCandidates([]AutoPriorityScoreInput{
			{
				ChannelID:               604,
				LocalGroup:              "shared",
				ChannelType:             constant.ChannelTypeOpenAI,
				CurrentPriority:         200,
				EffectiveRateMultiplier: 1,
				FirstTokenLatencyMS:     -1,
				UsageLogCount:           1,
				MonitorCheckCount:       1,
				FirstTokenSampleCount:   1,
			},
		}, 1000)

		require.Len(t, results, 1)
		assert.InDelta(t, 70, results[0].FirstTokenScore, 0.0001)
	})
}

func TestScoreAutoPriorityCandidatesColdStartCachePrior(t *testing.T) {
	const defaultPriorFactor = 0.3825

	t.Run("zero samples score exactly 95 from the fixed default", func(t *testing.T) {
		results := ScoreAutoPriorityCandidates([]AutoPriorityScoreInput{{
			ChannelID:               1,
			LocalGroup:              "shared",
			ChannelType:             constant.ChannelTypeOpenAI,
			EffectiveRateMultiplier: 0.02,
		}}, 1000)

		require.Len(t, results, 1)
		fresh := results[0]
		assert.Equal(t, 95.0, fresh.CacheScore)
		assert.InDelta(t, defaultPriorFactor, fresh.CacheAdjustedCostFactor, 1e-12)
		assert.Equal(t, "default_95", fresh.CacheFactorSource)
		assert.InDelta(t, defaultPriorFactor, fresh.CacheFactorPrior, 1e-12)
		assert.Equal(t, 0.0, fresh.CacheFactorOwnConfidence)
		assert.Equal(t, 9.5, autoPriorityCacheWeight*fresh.CacheScore)
	})

	t.Run("zero samples ignore mature same cohort peer cache data", func(t *testing.T) {
		scoreFreshWithPeerFactor := func(peerFactor float64) *AutoPriorityScoreResult {
			return resultByChannelID(ScoreAutoPriorityCandidates([]AutoPriorityScoreInput{
				{
					ChannelID:               10,
					LocalGroup:              "shared",
					ChannelType:             constant.ChannelTypeOpenAI,
					EffectiveRateMultiplier: 0.03,
					CacheAdjustedCostFactor: peerFactor,
					UsageLogCount:           200,
				},
				{
					ChannelID:               11,
					LocalGroup:              "shared",
					ChannelType:             constant.ChannelTypeOpenAI,
					EffectiveRateMultiplier: 0.02,
				},
			}, 1000), 11)
		}

		cacheHeavyPeer := scoreFreshWithPeerFactor(autoPriorityMinCacheCostFactor)
		noBenefitPeer := scoreFreshWithPeerFactor(1)
		require.NotNil(t, cacheHeavyPeer)
		require.NotNil(t, noBenefitPeer)
		assert.Equal(t, 95.0, cacheHeavyPeer.CacheScore)
		assert.Equal(t, 95.0, noBenefitPeer.CacheScore)
		assert.InDelta(t, defaultPriorFactor, cacheHeavyPeer.CacheAdjustedCostFactor, 1e-12)
		assert.InDelta(t, defaultPriorFactor, noBenefitPeer.CacheAdjustedCostFactor, 1e-12)
		assert.Equal(t, "default_95", cacheHeavyPeer.CacheFactorSource)
		assert.Equal(t, "default_95", noBenefitPeer.CacheFactorSource)
	})

	t.Run("ten samples blend the prior and own factor equally", func(t *testing.T) {
		const ownFactor = 0.8
		results := ScoreAutoPriorityCandidates([]AutoPriorityScoreInput{{
			ChannelID:               20,
			LocalGroup:              "shared",
			ChannelType:             constant.ChannelTypeOpenAI,
			EffectiveRateMultiplier: 0.02,
			CacheAdjustedCostFactor: ownFactor,
			UsageLogCount:           10,
		}}, 1000)

		require.Len(t, results, 1)
		expectedFactor := defaultPriorFactor + (ownFactor-defaultPriorFactor)*0.5
		assert.InDelta(t, expectedFactor, results[0].CacheAdjustedCostFactor, 1e-12)
		assert.InDelta(t, autoPriorityCacheScore(expectedFactor), results[0].CacheScore, 1e-12)
		assert.Equal(t, "own_blend", results[0].CacheFactorSource)
		assert.InDelta(t, defaultPriorFactor, results[0].CacheFactorPrior, 1e-12)
		assert.Equal(t, 0.5, results[0].CacheFactorOwnConfidence)
	})

	t.Run("nineteen samples report 95 percent own confidence", func(t *testing.T) {
		const ownFactor = 0.5
		results := ScoreAutoPriorityCandidates([]AutoPriorityScoreInput{{
			ChannelID:               30,
			LocalGroup:              "shared",
			ChannelType:             constant.ChannelTypeOpenAI,
			EffectiveRateMultiplier: 0.02,
			CacheAdjustedCostFactor: ownFactor,
			UsageLogCount:           19,
		}}, 1000)

		require.Len(t, results, 1)
		expectedFactor := defaultPriorFactor + (ownFactor-defaultPriorFactor)*0.95
		assert.InDelta(t, expectedFactor, results[0].CacheAdjustedCostFactor, 1e-12)
		assert.Equal(t, 0.95, results[0].CacheFactorOwnConfidence)
		assert.Equal(t, "own_blend", results[0].CacheFactorSource)
		assert.InDelta(t, defaultPriorFactor, results[0].CacheFactorPrior, 1e-12)
	})

	t.Run("twenty samples use own data completely", func(t *testing.T) {
		const ownFactor = 0.5
		results := ScoreAutoPriorityCandidates([]AutoPriorityScoreInput{{
			ChannelID:               40,
			LocalGroup:              "shared",
			ChannelType:             constant.ChannelTypeOpenAI,
			EffectiveRateMultiplier: 0.02,
			CacheAdjustedCostFactor: ownFactor,
			UsageLogCount:           20,
		}}, 1000)

		require.Len(t, results, 1)
		assert.Equal(t, ownFactor, results[0].CacheAdjustedCostFactor)
		assert.InDelta(t, autoPriorityCacheScore(ownFactor), results[0].CacheScore, 1e-12)
		assert.Equal(t, "own", results[0].CacheFactorSource)
		assert.InDelta(t, defaultPriorFactor, results[0].CacheFactorPrior, 1e-12)
		assert.Equal(t, 1.0, results[0].CacheFactorOwnConfidence)
	})

	t.Run("previous snapshots do not alter the exact confidence transition", func(t *testing.T) {
		testCases := []struct {
			name           string
			usageLogCount  int64
			expectedFactor float64
			expectedScore  float64
		}{
			{name: "zero", usageLogCount: 0, expectedFactor: defaultPriorFactor, expectedScore: 95},
			{name: "ten", usageLogCount: 10, expectedFactor: 0.44125, expectedScore: autoPriorityCacheScore(0.44125)},
			{name: "twenty", usageLogCount: 20, expectedFactor: 0.5, expectedScore: autoPriorityCacheScore(0.5)},
		}

		for _, testCase := range testCases {
			t.Run(testCase.name, func(t *testing.T) {
				results := ScoreAutoPriorityCandidates([]AutoPriorityScoreInput{{
					ChannelID:                       50,
					LocalGroup:                      "shared",
					ChannelType:                     constant.ChannelTypeOpenAI,
					EffectiveRateMultiplier:         0.02,
					CacheAdjustedCostFactor:         0.5,
					PreviousCacheAdjustedCostFactor: 1,
					UsageLogCount:                   testCase.usageLogCount,
					HasPreviousSnapshot:             true,
				}}, 1000)

				require.Len(t, results, 1)
				assert.InDelta(t, testCase.expectedFactor, results[0].CacheAdjustedCostFactor, 1e-12)
				assert.InDelta(t, testCase.expectedScore, results[0].CacheScore, 1e-12)
			})
		}
	})
}

func TestAutoPriorityColdStartDiagnosticsRoundTrip(t *testing.T) {
	results := ScoreAutoPriorityCandidates([]AutoPriorityScoreInput{{
		ChannelID:               2,
		LocalGroup:              "shared",
		ChannelType:             constant.ChannelTypeOpenAI,
		EffectiveRateMultiplier: 0.02,
	}}, 1000)
	require.Len(t, results, 1)
	fresh := results[0]
	assert.Equal(t, "default_95", fresh.CacheFactorSource)
	assert.InDelta(t, 0.3825, fresh.CacheFactorPrior, 1e-12)
	assert.Equal(t, 0.0, fresh.CacheFactorOwnConfidence)

	snapshot := buildChannelAutoPriorityScoreSnapshot(fresh, 100, 200)
	encoded, err := common.Marshal(snapshot)
	require.NoError(t, err)
	var diagnostics map[string]any
	require.NoError(t, common.Unmarshal(encoded, &diagnostics))
	assert.Equal(t, "default_95", diagnostics["cache_factor_source"])
	assert.InDelta(t, 0.3825, diagnostics["cache_factor_prior"], 1e-12)
	assert.Equal(t, 0.0, diagnostics["cache_factor_own_confidence"])
	assert.Equal(t, 0.02, diagnostics["cohort_floor"])
	assert.Equal(t, 0.02, diagnostics["cohort_ceil"])
	assert.Equal(t, float64(1), diagnostics["cohort_member_count"])
	assert.Equal(t, 0.02, diagnostics["ordinary_price_floor"])
	assert.Equal(t, "cohort_floor", diagnostics["effective_price_floor_source"])

	var decoded dto.ChannelAutoPriorityScore
	require.NoError(t, common.Unmarshal(encoded, &decoded))
	assert.Equal(t, "v5", decoded.Version)
	assert.Equal(t, fresh.Cohort, decoded.Cohort)
	assert.Equal(t, fresh.CohortFloor, decoded.CohortFloor)
	assert.Equal(t, fresh.CohortCeil, decoded.CohortCeil)
	assert.Equal(t, fresh.CohortMemberCount, decoded.CohortMemberCount)
	assert.Equal(t, fresh.OrdinaryPriceFloor, decoded.OrdinaryPriceFloor)
	assert.Equal(t, fresh.EffectivePriceFloorSource, decoded.EffectivePriceFloorSource)
	assert.Equal(t, fresh.EffectiveRateMultiplier, decoded.EffectiveRateMultiplier)
	assert.Equal(t, fresh.NominalRateMultiplier, decoded.NominalRateMultiplier)
	assert.Equal(t, fresh.CacheAdjustedCostFactor, decoded.CacheAdjustedCostFactor)
	assert.Equal(t, fresh.EffectiveCostMultiplier, decoded.EffectiveCostMultiplier)
	assert.Equal(t, fresh.EffectivePriceScore, decoded.EffectivePriceScore)
	assert.Equal(t, fresh.NominalPriceScore, decoded.NominalPriceScore)
	assert.Equal(t, fresh.CacheScore, decoded.CacheScore)
	assert.Equal(t, fresh.AvailabilityScore, decoded.AvailabilityScore)
	assert.Equal(t, fresh.FirstTokenScore, decoded.FirstTokenScore)
	assert.Equal(t, fresh.ThroughputScore, decoded.ThroughputScore)
	assert.Equal(t, fresh.FinalScore, decoded.FinalScore)
	assert.Equal(t, fresh.OldPriority, decoded.OldPriority)
	assert.Equal(t, fresh.NewPriority, decoded.NewPriority)
	assert.Equal(t, fresh.Applied, decoded.Applied)
	assert.Equal(t, "default_95", decoded.CacheFactorSource)
	assert.InDelta(t, 0.3825, decoded.CacheFactorPrior, 1e-12)
	assert.Equal(t, 0.0, decoded.CacheFactorOwnConfidence)

	// New fields remain optional so snapshots written by older versions still decode.
	var legacy dto.ChannelAutoPriorityScore
	require.NoError(t, common.Unmarshal([]byte(`{"effective_rate_multiplier":0.02,"cache_adjusted_cost_factor":1}`), &legacy))
	assert.Empty(t, legacy.CacheFactorSource)
	assert.Zero(t, legacy.NominalRateMultiplier)
	assert.Zero(t, legacy.NominalPriceScore)
	assert.Zero(t, legacy.CacheScore)
	assert.Zero(t, legacy.OrdinaryPriceFloor)
	assert.Empty(t, legacy.EffectivePriceFloorSource)
}

func floatPtr(v float64) *float64 {
	return &v
}

func resultByChannelID(results []AutoPriorityScoreResult, channelID int) *AutoPriorityScoreResult {
	for i := range results {
		if results[i].ChannelID == channelID {
			return &results[i]
		}
	}
	return nil
}

func TestScoreAutoPriorityCandidatesClampsPriorityBounds(t *testing.T) {
	t.Run("clamps to a positive low cap", func(t *testing.T) {
		results := ScoreAutoPriorityCandidates([]AutoPriorityScoreInput{
			{
				ChannelID:               701,
				LocalGroup:              "shared",
				ChannelType:             constant.ChannelTypeOpenAI,
				CurrentPriority:         9999,
				EffectiveRateMultiplier: 1,
				Availability:            floatPtr(1),
				FirstTokenLatencyMS:     1,
				UsageLogCount:           1,
				MonitorCheckCount:       1,
				FirstTokenSampleCount:   1,
			},
		}, 500)

		require.Len(t, results, 1)
		assert.Equal(t, int64(500), results[0].ComputedPriority)
		assert.Equal(t, int64(500), results[0].NewPriority)
	})

	t.Run("defaults to 1000 when maxPriority is non-positive", func(t *testing.T) {
		results := ScoreAutoPriorityCandidates([]AutoPriorityScoreInput{
			{
				ChannelID:               702,
				LocalGroup:              "shared",
				ChannelType:             constant.ChannelTypeOpenAI,
				CurrentPriority:         9999,
				EffectiveRateMultiplier: 1,
				Availability:            floatPtr(1),
				FirstTokenLatencyMS:     1,
				UsageLogCount:           1,
				MonitorCheckCount:       1,
				FirstTokenSampleCount:   1,
			},
		}, 0)

		require.Len(t, results, 1)
		assert.LessOrEqual(t, results[0].ComputedPriority, int64(1000))
		assert.GreaterOrEqual(t, results[0].ComputedPriority, int64(0))
	})
}

func TestScoreAutoPriorityCandidatesHandlesNonFiniteMultiplier(t *testing.T) {
	results := ScoreAutoPriorityCandidates([]AutoPriorityScoreInput{
		{
			ChannelID:               801,
			LocalGroup:              "shared",
			ChannelType:             constant.ChannelTypeOpenAI,
			CurrentPriority:         321,
			EffectiveRateMultiplier: math.Inf(1),
			Availability:            floatPtr(1),
			FirstTokenLatencyMS:     10,
			UsageLogCount:           1,
			MonitorCheckCount:       1,
			FirstTokenSampleCount:   1,
		},
	}, 1000)

	require.Len(t, results, 1)
	assert.Equal(t, "missing_effective_rate_multiplier", results[0].Reason)
	assert.False(t, results[0].Applied)
	assert.Equal(t, int64(321), results[0].ComputedPriority)
	assert.Equal(t, int64(321), results[0].NewPriority)
}

func TestScoreAutoPriorityCandidatesCrossSourceCohortCostBounds(t *testing.T) {
	t.Run("legacy behavior preserved when cohort bounds are unset", func(t *testing.T) {
		results := ScoreAutoPriorityCandidates([]AutoPriorityScoreInput{
			{
				ChannelID:               901,
				LocalGroup:              "shared",
				ChannelType:             constant.ChannelTypeOpenAI,
				CurrentPriority:         100,
				EffectiveRateMultiplier: 1,
				Availability:            floatPtr(1.0),
			},
		}, 1000)

		require.Len(t, results, 1)
		assert.InDelta(t, 100, results[0].EffectivePriceScore, 0.0001)
		// price 100 + availability 100 + zero-sample cache score 95, and no
		// first-token/throughput samples (neutral 70):
		// 0.75*100 + 0.10*95 + 0.08*100 + 0.03*70 + 0.04*70 = 97.4 -> 974.
		assert.Equal(t, int64(974), results[0].ComputedPriority)
	})

	t.Run("cross-source cohort bounds let cost differentiate a single-member run cohort", func(t *testing.T) {
		results := ScoreAutoPriorityCandidates([]AutoPriorityScoreInput{
			{
				ChannelID:               911,
				LocalGroup:              "shared",
				ChannelType:             constant.ChannelTypeOpenAI,
				CurrentPriority:         100,
				EffectiveRateMultiplier: 0.05,
				CacheAdjustedCostFactor: 1,
				Availability:            floatPtr(1.0),
				CohortCostFloor:         0.02,
				CohortCostCeil:          0.08,
			},
			{
				ChannelID:               912,
				LocalGroup:              "shared",
				ChannelType:             constant.ChannelTypeOpenAI,
				CurrentPriority:         100,
				EffectiveRateMultiplier: 0.06,
				CacheAdjustedCostFactor: 1,
				Availability:            floatPtr(1.0),
				CohortCostFloor:         0.02,
				CohortCostCeil:          0.08,
			},
			{
				ChannelID:               913,
				LocalGroup:              "shared",
				ChannelType:             constant.ChannelTypeOpenAI,
				CurrentPriority:         100,
				EffectiveRateMultiplier: 0.08,
				CacheAdjustedCostFactor: 1,
				Availability:            floatPtr(1.0),
				CohortCostFloor:         0.02,
				CohortCostCeil:          0.08,
			},
		}, 1000)

		require.Len(t, results, 3)
		cheapest := resultByChannelID(results, 911)
		middle := resultByChannelID(results, 912)
		dearest := resultByChannelID(results, 913)
		require.NotNil(t, cheapest)
		require.NotNil(t, middle)
		require.NotNil(t, dearest)

		assert.InDelta(t, 0.02, cheapest.OrdinaryPriceFloor, 1e-12)
		assert.Equal(t, "cohort_floor", cheapest.EffectivePriceFloorSource)
		assert.Greater(t, cheapest.EffectivePriceScore, middle.EffectivePriceScore)
		assert.Greater(t, middle.EffectivePriceScore, dearest.EffectivePriceScore)
		assert.Greater(t, cheapest.ComputedPriority, dearest.ComputedPriority)
	})

	t.Run("degenerate cohort bounds do not force spurious differentiation", func(t *testing.T) {
		results := ScoreAutoPriorityCandidates([]AutoPriorityScoreInput{
			{
				ChannelID:               921,
				LocalGroup:              "shared",
				ChannelType:             constant.ChannelTypeOpenAI,
				CurrentPriority:         100,
				EffectiveRateMultiplier: 0.05,
				CacheAdjustedCostFactor: 1,
				Availability:            floatPtr(1.0),
				CohortCostFloor:         0.05,
				CohortCostCeil:          0.05,
			},
		}, 1000)

		require.Len(t, results, 1)
		assert.InDelta(t, 100, results[0].EffectivePriceScore, 0.0001)
	})
}

func TestScoreAutoPriorityCandidatesOrdinaryPriceFloor(t *testing.T) {
	ordinarySlowLatencyMS := autoPriorityFirstTokenFastMS +
		math.Exp(0.6*math.Log1p(autoPriorityFirstTokenSlowMS-autoPriorityFirstTokenFastMS)) - 1
	inputs := []AutoPriorityScoreInput{
		{
			ChannelID:               981,
			LocalGroup:              "shared",
			ChannelType:             constant.ChannelTypeOpenAI,
			EffectiveRateMultiplier: 0.001,
			Availability:            floatPtr(1),
			MonitorCheckCount:       3,
			FirstTokenLatencyMS:     ordinarySlowLatencyMS,
			FirstTokenSampleCount:   1,
		},
		{
			ChannelID:               982,
			LocalGroup:              "shared",
			ChannelType:             constant.ChannelTypeOpenAI,
			EffectiveRateMultiplier: 0.040,
			Availability:            floatPtr(1),
			MonitorCheckCount:       3,
			FirstTokenLatencyMS:     ordinarySlowLatencyMS,
			FirstTokenSampleCount:   1,
		},
		{
			ChannelID:               983,
			LocalGroup:              "shared",
			ChannelType:             constant.ChannelTypeOpenAI,
			EffectiveRateMultiplier: 0.070,
			Availability:            floatPtr(1),
			MonitorCheckCount:       3,
			FirstTokenLatencyMS:     autoPriorityFirstTokenFastMS,
			FirstTokenSampleCount:   1,
		},
	}

	t.Run("extreme peer cannot compress the ordinary price gap below modest TTFT signal", func(t *testing.T) {
		results := ScoreAutoPriorityCandidates(inputs, 1000)

		extremeCheap := resultByChannelID(results, 981)
		ordinaryCheap := resultByChannelID(results, 982)
		ordinaryExpensive := resultByChannelID(results, 983)
		require.NotNil(t, extremeCheap)
		require.NotNil(t, ordinaryCheap)
		require.NotNil(t, ordinaryExpensive)

		assert.InDelta(t, 40, ordinaryCheap.FirstTokenScore, 1e-9)
		assert.InDelta(t, 100, ordinaryExpensive.FirstTokenScore, 1e-9)
		priceScoreGap := ordinaryCheap.NominalPriceScore - ordinaryExpensive.NominalPriceScore
		firstTokenScoreGap := ordinaryExpensive.FirstTokenScore - ordinaryCheap.FirstTokenScore
		assert.GreaterOrEqual(t, priceScoreGap, 20.0)
		assert.Greater(t, autoPriorityPriceWeight*priceScoreGap, autoPriorityFirstTokenWeight*firstTokenScoreGap)
		assert.Greater(t, ordinaryCheap.FinalScore, ordinaryExpensive.FinalScore)
		assert.Greater(t, ordinaryCheap.ComputedPriority, ordinaryExpensive.ComputedPriority)
		assert.Greater(t, extremeCheap.ComputedPriority, ordinaryCheap.ComputedPriority)

		snapshot := buildChannelAutoPriorityScoreSnapshot(*ordinaryCheap, 100, 200)
		encoded, err := common.Marshal(snapshot)
		require.NoError(t, err)
		var diagnostics map[string]any
		require.NoError(t, common.Unmarshal(encoded, &diagnostics))
		assert.Equal(t, "v5", diagnostics["version"])
		assert.Equal(t, 0.001, diagnostics["cohort_floor"])
		assert.Equal(t, 0.040, diagnostics["ordinary_price_floor"])
		assert.Equal(t, "ordinary_band", diagnostics["effective_price_floor_source"])
	})

	t.Run("recompute does not retain the old ordinary inversion through hysteresis", func(t *testing.T) {
		inputs[1].CurrentPriority = 250
		inputs[1].HasPreviousSnapshot = true
		inputs[2].CurrentPriority = 259
		inputs[2].HasPreviousSnapshot = true

		results := ScoreAutoPriorityCandidates(inputs, 1000)
		ordinaryCheap := resultByChannelID(results, 982)
		ordinaryExpensive := resultByChannelID(results, 983)
		require.NotNil(t, ordinaryCheap)
		require.NotNil(t, ordinaryExpensive)

		assert.Greater(t, ordinaryCheap.ComputedPriority, ordinaryExpensive.ComputedPriority)
		assert.Greater(t, ordinaryCheap.NewPriority, ordinaryExpensive.NewPriority)
		assert.True(t, ordinaryCheap.Applied)
	})
}

func TestScoreAutoPriorityCandidatesExtremeCostDominance(t *testing.T) {
	t.Run("four times nominal gap remains formula based below the hard threshold", func(t *testing.T) {
		results := ScoreAutoPriorityCandidates([]AutoPriorityScoreInput{
			{
				ChannelID:               991,
				LocalGroup:              "shared",
				ChannelType:             constant.ChannelTypeOpenAI,
				EffectiveRateMultiplier: 0.02,
				Availability:            floatPtr(1),
				MonitorCheckCount:       3,
			},
			{
				ChannelID:               992,
				LocalGroup:              "shared",
				ChannelType:             constant.ChannelTypeOpenAI,
				EffectiveRateMultiplier: 0.08,
				Availability:            floatPtr(1),
				MonitorCheckCount:       3,
			},
		}, 1000)

		cheap := resultByChannelID(results, 991)
		expensive := resultByChannelID(results, 992)
		require.NotNil(t, cheap)
		require.NotNil(t, expensive)

		// This pins the representative formula inputs, not a new hard 4x rule.
		assert.InDelta(t, 100, cheap.EffectivePriceScore, 1e-9)
		assert.InDelta(t, 25, expensive.EffectivePriceScore, 1e-9)
		assert.InDelta(t, 0.75*(100-25), cheap.FinalScore-expensive.FinalScore, 1e-9)
		assert.Greater(t, cheap.ComputedPriority, expensive.ComputedPriority)
	})

	t.Run("close prices preserve enough quality signal for the healthier channel to win", func(t *testing.T) {
		results := ScoreAutoPriorityCandidates([]AutoPriorityScoreInput{
			{
				ChannelID:               1001,
				LocalGroup:              "shared",
				ChannelType:             constant.ChannelTypeOpenAI,
				EffectiveRateMultiplier: 0.04,
				Availability:            floatPtr(0.4),
				MonitorCheckCount:       3,
				FirstTokenLatencyMS:     90000,
				FirstTokenSampleCount:   1,
				ThroughputTps:           1,
				ThroughputSampleCount:   1,
			},
			{
				ChannelID:               1002,
				LocalGroup:              "shared",
				ChannelType:             constant.ChannelTypeOpenAI,
				EffectiveRateMultiplier: 0.05,
				Availability:            floatPtr(1),
				MonitorCheckCount:       3,
				FirstTokenLatencyMS:     100,
				FirstTokenSampleCount:   1,
				ThroughputTps:           20,
				ThroughputSampleCount:   1,
			},
		}, 1000)

		cheap := resultByChannelID(results, 1001)
		healthy := resultByChannelID(results, 1002)
		require.NotNil(t, cheap)
		require.NotNil(t, healthy)
		assert.Greater(t, healthy.FinalScore, cheap.FinalScore)
		assert.Greater(t, healthy.ComputedPriority, cheap.ComputedPriority)
	})

	t.Run("usable extreme cheap channel dominates the best expensive metrics", func(t *testing.T) {
		results := ScoreAutoPriorityCandidates([]AutoPriorityScoreInput{
			{
				ChannelID:               1011,
				LocalGroup:              "shared",
				ChannelType:             constant.ChannelTypeOpenAI,
				EffectiveRateMultiplier: 0.001,
				Availability:            floatPtr(0),
				MonitorCheckCount:       2,
				FirstTokenLatencyMS:     90000,
				FirstTokenSampleCount:   1,
				ThroughputTps:           1,
				ThroughputSampleCount:   1,
				CohortCostFloor:         0.00001,
				CohortCostCeil:          1,
			},
			{
				ChannelID:               1012,
				LocalGroup:              "shared",
				ChannelType:             constant.ChannelTypeOpenAI,
				EffectiveRateMultiplier: 0.05,
				Availability:            floatPtr(1),
				MonitorCheckCount:       3,
				FirstTokenLatencyMS:     100,
				FirstTokenSampleCount:   1,
				ThroughputTps:           20,
				ThroughputSampleCount:   1,
				CohortCostFloor:         0.00001,
				CohortCostCeil:          1,
			},
		}, 1000)

		cheap := resultByChannelID(results, 1011)
		expensive := resultByChannelID(results, 1012)
		require.NotNil(t, cheap)
		require.NotNil(t, expensive)
		assert.GreaterOrEqual(t, cheap.FinalScore-expensive.FinalScore, 1.0)
		assert.GreaterOrEqual(t, cheap.ComputedPriority-expensive.ComputedPriority, int64(10))
	})

	t.Run("usable extreme cheap channel dominates an ordinary peer at the priority ceiling", func(t *testing.T) {
		results := ScoreAutoPriorityCandidates([]AutoPriorityScoreInput{
			{
				ChannelID:               1013,
				LocalGroup:              "shared",
				ChannelType:             constant.ChannelTypeOpenAI,
				CurrentPriority:         1000,
				EffectiveRateMultiplier: 0.001,
				CacheAdjustedCostFactor: 1,
				UsageLogCount:           20,
				Availability:            floatPtr(1),
				MonitorCheckCount:       3,
				FirstTokenLatencyMS:     autoPriorityFirstTokenSlowMS,
				FirstTokenSampleCount:   1,
				ThroughputTps:           autoPriorityThroughputSlowTps,
				ThroughputSampleCount:   1,
				HasPreviousSnapshot:     true,
			},
			{
				ChannelID:               1014,
				LocalGroup:              "shared",
				ChannelType:             constant.ChannelTypeOpenAI,
				CurrentPriority:         1000,
				EffectiveRateMultiplier: 0.040,
				CacheAdjustedCostFactor: autoPriorityMinCacheCostFactor,
				UsageLogCount:           20,
				Availability:            floatPtr(1),
				MonitorCheckCount:       3,
				FirstTokenLatencyMS:     autoPriorityFirstTokenFastMS,
				FirstTokenSampleCount:   1,
				ThroughputTps:           autoPriorityThroughputFastTps,
				ThroughputSampleCount:   1,
				HasPreviousSnapshot:     true,
			},
			{
				ChannelID:               1015,
				LocalGroup:              "shared",
				ChannelType:             constant.ChannelTypeOpenAI,
				CurrentPriority:         1000,
				EffectiveRateMultiplier: 0.070,
				Availability:            floatPtr(1),
				MonitorCheckCount:       3,
			},
		}, 1000)

		cheap := resultByChannelID(results, 1013)
		ordinary := resultByChannelID(results, 1014)
		require.NotNil(t, cheap)
		require.NotNil(t, ordinary)
		assert.InDelta(t, 0.040, ordinary.OrdinaryPriceFloor, 1e-12)
		assert.InDelta(t, 100, ordinary.NominalPriceScore, 1e-12)
		assert.InDelta(t, 100, ordinary.CacheScore, 1e-12)
		assert.InDelta(t, 100, ordinary.AvailabilityScore, 1e-12)
		assert.InDelta(t, 100, ordinary.FirstTokenScore, 1e-12)
		assert.InDelta(t, 100, ordinary.ThroughputScore, 1e-12)
		assert.InDelta(t, 100, weightedAutoPriorityFinalScore(
			1,
			ordinary.NominalPriceScore,
			ordinary.CacheScore,
			ordinary.AvailabilityScore,
			ordinary.FirstTokenScore,
			ordinary.ThroughputScore,
		), 1e-12)
		assert.GreaterOrEqual(t, cheap.FinalScore-ordinary.FinalScore, autoPriorityDominanceScoreMargin)
		assert.GreaterOrEqual(t, cheap.ComputedPriority-ordinary.ComputedPriority, autoPriorityDominancePriorityMargin)
		assert.Greater(t, cheap.NewPriority, ordinary.NewPriority)
		assert.True(t, cheap.Applied)
		assert.True(t, ordinary.Applied)
	})

	t.Run("usable extreme cheap channel dominates a currently degraded expensive peer", func(t *testing.T) {
		results := ScoreAutoPriorityCandidates([]AutoPriorityScoreInput{
			{
				ChannelID:               1016,
				LocalGroup:              "shared",
				ChannelType:             constant.ChannelTypeOpenAI,
				EffectiveRateMultiplier: 0.001,
				CacheAdjustedCostFactor: 1,
				UsageLogCount:           20,
				Availability:            floatPtr(1),
				MonitorCheckCount:       3,
				FirstTokenLatencyMS:     autoPriorityFirstTokenSlowMS,
				FirstTokenSampleCount:   1,
				ThroughputTps:           autoPriorityThroughputSlowTps,
				ThroughputSampleCount:   1,
			},
			{
				ChannelID:               1017,
				LocalGroup:              "shared",
				ChannelType:             constant.ChannelTypeOpenAI,
				EffectiveRateMultiplier: 0.040,
				CacheAdjustedCostFactor: autoPriorityMinCacheCostFactor,
				UsageLogCount:           20,
				Availability:            floatPtr(0.49),
				MonitorCheckCount:       3,
				FirstTokenLatencyMS:     autoPriorityFirstTokenFastMS,
				FirstTokenSampleCount:   1,
				ThroughputTps:           autoPriorityThroughputFastTps,
				ThroughputSampleCount:   1,
			},
			{
				ChannelID:               1018,
				LocalGroup:              "shared",
				ChannelType:             constant.ChannelTypeOpenAI,
				EffectiveRateMultiplier: 0.070,
				Availability:            floatPtr(1),
				MonitorCheckCount:       3,
			},
		}, 1000)

		cheap := resultByChannelID(results, 1016)
		degradedExpensive := resultByChannelID(results, 1017)
		require.NotNil(t, cheap)
		require.NotNil(t, degradedExpensive)
		assert.GreaterOrEqual(t, cheap.FinalScore-degradedExpensive.FinalScore, autoPriorityDominanceScoreMargin)
		assert.GreaterOrEqual(t, cheap.ComputedPriority-degradedExpensive.ComputedPriority, autoPriorityDominancePriorityMargin)
		assert.Greater(t, cheap.NewPriority, degradedExpensive.NewPriority)
	})

	t.Run("auto-disabled expensive peer stays outside competitive demotion", func(t *testing.T) {
		results := ScoreAutoPriorityCandidates([]AutoPriorityScoreInput{
			{
				ChannelID:               1019,
				LocalGroup:              "shared",
				ChannelType:             constant.ChannelTypeOpenAI,
				CurrentPriority:         5_000,
				EffectiveRateMultiplier: 0.08,
				HardUnavailable:         true,
			},
			{
				ChannelID:               1020,
				LocalGroup:              "shared",
				ChannelType:             constant.ChannelTypeOpenAI,
				CurrentPriority:         100,
				EffectiveRateMultiplier: 0.01,
				Availability:            floatPtr(1),
				MonitorCheckCount:       3,
			},
			{
				ChannelID:               1021,
				LocalGroup:              "shared",
				ChannelType:             constant.ChannelTypeOpenAI,
				CurrentPriority:         100,
				EffectiveRateMultiplier: 0.08,
				Availability:            floatPtr(1),
				MonitorCheckCount:       3,
			},
		}, 1000)

		autoDisabled := resultByChannelID(results, 1019)
		cheap := resultByChannelID(results, 1020)
		usableExpensive := resultByChannelID(results, 1021)
		require.NotNil(t, autoDisabled)
		require.NotNil(t, cheap)
		require.NotNil(t, usableExpensive)
		assert.False(t, autoDisabled.Applied)
		assert.Equal(t, "channel_auto_disabled", autoDisabled.Reason)
		assert.Equal(t, int64(5_000), autoDisabled.OldPriority)
		assert.Equal(t, int64(0), autoDisabled.ComputedPriority)
		assert.Equal(t, autoDisabled.OldPriority, autoDisabled.NewPriority)
		assert.GreaterOrEqual(t, cheap.FinalScore-usableExpensive.FinalScore, autoPriorityDominanceScoreMargin)
		assert.GreaterOrEqual(t, cheap.ComputedPriority-usableExpensive.ComputedPriority, autoPriorityDominancePriorityMargin)
		assert.Greater(t, cheap.NewPriority, usableExpensive.NewPriority)
	})

	t.Run("small priority ceiling preserves representable dominance tiers", func(t *testing.T) {
		inputs := []AutoPriorityScoreInput{
			{
				ChannelID:               1062,
				LocalGroup:              "small-ceiling",
				ChannelType:             constant.ChannelTypeOpenAI,
				EffectiveRateMultiplier: 8,
				CacheAdjustedCostFactor: autoPriorityMinCacheCostFactor,
				UsageLogCount:           autoPriorityFullCacheSampleCount,
				Availability:            floatPtr(1),
				MonitorCheckCount:       3,
				FirstTokenLatencyMS:     autoPriorityFirstTokenFastMS,
				FirstTokenSampleCount:   1,
				ThroughputTps:           autoPriorityThroughputFastTps,
				ThroughputSampleCount:   1,
			},
			{
				ChannelID:               1063,
				LocalGroup:              "small-ceiling",
				ChannelType:             constant.ChannelTypeOpenAI,
				EffectiveRateMultiplier: 1,
				CacheAdjustedCostFactor: autoPriorityMinCacheCostFactor,
				UsageLogCount:           autoPriorityFullCacheSampleCount,
				Availability:            floatPtr(1),
				MonitorCheckCount:       3,
				FirstTokenLatencyMS:     autoPriorityFirstTokenFastMS,
				FirstTokenSampleCount:   1,
				ThroughputTps:           autoPriorityThroughputFastTps,
				ThroughputSampleCount:   1,
			},
			{
				ChannelID:               1061,
				LocalGroup:              "small-ceiling",
				ChannelType:             constant.ChannelTypeOpenAI,
				EffectiveRateMultiplier: 64,
				CacheAdjustedCostFactor: autoPriorityMinCacheCostFactor,
				UsageLogCount:           autoPriorityFullCacheSampleCount,
				Availability:            floatPtr(1),
				MonitorCheckCount:       3,
				FirstTokenLatencyMS:     autoPriorityFirstTokenFastMS,
				FirstTokenSampleCount:   1,
				ThroughputTps:           autoPriorityThroughputFastTps,
				ThroughputSampleCount:   1,
			},
		}

		results := ScoreAutoPriorityCandidates(inputs, 5)
		permutedResults := ScoreAutoPriorityCandidates([]AutoPriorityScoreInput{
			inputs[2],
			inputs[0],
			inputs[1],
		}, 5)

		expensive := resultByChannelID(results, 1061)
		middle := resultByChannelID(results, 1062)
		cheap := resultByChannelID(results, 1063)
		require.NotNil(t, expensive)
		require.NotNil(t, middle)
		require.NotNil(t, cheap)
		assert.Less(t, expensive.ComputedPriority, middle.ComputedPriority)
		assert.Less(t, middle.ComputedPriority, cheap.ComputedPriority)
		assert.Less(t, expensive.NewPriority, middle.NewPriority)
		assert.Less(t, middle.NewPriority, cheap.NewPriority)
		for _, result := range results {
			assert.GreaterOrEqual(t, result.ComputedPriority, int64(0))
			assert.LessOrEqual(t, result.ComputedPriority, int64(5))
			permuted := resultByChannelID(permutedResults, result.ChannelID)
			require.NotNil(t, permuted)
			assert.Equal(t, result.ComputedPriority, permuted.ComputedPriority)
			assert.Equal(t, result.NewPriority, permuted.NewPriority)
		}
	})

	t.Run("exact eight times cost gap triggers dominance", func(t *testing.T) {
		results := ScoreAutoPriorityCandidates([]AutoPriorityScoreInput{
			{
				ChannelID:               1021,
				LocalGroup:              "shared",
				ChannelType:             constant.ChannelTypeOpenAI,
				EffectiveRateMultiplier: 0.001,
				MonitorCheckCount:       10,
				FirstTokenLatencyMS:     90000,
				FirstTokenSampleCount:   1,
				ThroughputTps:           1,
				ThroughputSampleCount:   1,
				CohortCostFloor:         0.00001,
				CohortCostCeil:          0.008,
			},
			{
				ChannelID:               1022,
				LocalGroup:              "shared",
				ChannelType:             constant.ChannelTypeOpenAI,
				EffectiveRateMultiplier: 0.008,
				Availability:            floatPtr(1),
				MonitorCheckCount:       3,
				FirstTokenLatencyMS:     100,
				FirstTokenSampleCount:   1,
				ThroughputTps:           20,
				ThroughputSampleCount:   1,
				CohortCostFloor:         0.00001,
				CohortCostCeil:          0.008,
			},
		}, 1000)

		cheap := resultByChannelID(results, 1021)
		expensive := resultByChannelID(results, 1022)
		require.NotNil(t, cheap)
		require.NotNil(t, expensive)
		assert.GreaterOrEqual(t, cheap.FinalScore-expensive.FinalScore, 1.0)
		assert.GreaterOrEqual(t, cheap.ComputedPriority-expensive.ComputedPriority, int64(10))
	})

	t.Run("tiny valid costs preserve ratios and dominance", func(t *testing.T) {
		results := ScoreAutoPriorityCandidates([]AutoPriorityScoreInput{
			{
				ChannelID:               1,
				LocalGroup:              "tiny",
				ChannelType:             constant.ChannelTypeOpenAI,
				EffectiveRateMultiplier: 1e-15,
				Availability:            floatPtr(0),
				MonitorCheckCount:       2,
				FirstTokenLatencyMS:     90000,
				FirstTokenSampleCount:   1,
				ThroughputTps:           1,
				ThroughputSampleCount:   1,
				CohortCostFloor:         1e-17,
			},
			{
				ChannelID:               2,
				LocalGroup:              "tiny",
				ChannelType:             constant.ChannelTypeOpenAI,
				EffectiveRateMultiplier: 8e-15,
				Availability:            floatPtr(1),
				MonitorCheckCount:       3,
				FirstTokenLatencyMS:     100,
				FirstTokenSampleCount:   1,
				ThroughputTps:           20,
				ThroughputSampleCount:   1,
				CohortCostFloor:         1e-17,
			},
		}, 1000)

		cheap := resultByChannelID(results, 1)
		expensive := resultByChannelID(results, 2)
		require.NotNil(t, cheap)
		require.NotNil(t, expensive)
		assert.InDelta(t, 1e-17, cheap.CohortFloor, 1e-30)
		assert.InDelta(t, 1e-15, cheap.OrdinaryPriceFloor, 1e-30)
		assert.Equal(t, "ordinary_band", cheap.EffectivePriceFloorSource)
		assert.InDelta(t, 100, cheap.EffectivePriceScore, 1e-12)
		assert.InDelta(t, 12.5, expensive.EffectivePriceScore, 1e-12)
		assert.GreaterOrEqual(t, cheap.FinalScore-expensive.FinalScore, 1.0)
		assert.GreaterOrEqual(t, cheap.ComputedPriority-expensive.ComputedPriority, int64(10))
	})

	t.Run("current unavailability overrides even an extreme price advantage", func(t *testing.T) {
		results := ScoreAutoPriorityCandidates([]AutoPriorityScoreInput{
			{
				ChannelID:               1031,
				LocalGroup:              "shared",
				ChannelType:             constant.ChannelTypeOpenAI,
				EffectiveRateMultiplier: 0.001,
				Availability:            floatPtr(0),
				MonitorCheckCount:       3,
				CohortCostFloor:         0.00001,
				CohortCostCeil:          1,
			},
			{
				ChannelID:               1032,
				LocalGroup:              "shared",
				ChannelType:             constant.ChannelTypeOpenAI,
				EffectiveRateMultiplier: 0.05,
				Availability:            floatPtr(1),
				MonitorCheckCount:       3,
				FirstTokenLatencyMS:     100,
				FirstTokenSampleCount:   1,
				ThroughputTps:           20,
				ThroughputSampleCount:   1,
				CohortCostFloor:         0.00001,
				CohortCostCeil:          1,
			},
		}, 1000)

		cheap := resultByChannelID(results, 1031)
		expensive := resultByChannelID(results, 1032)
		require.NotNil(t, cheap)
		require.NotNil(t, expensive)
		assert.Less(t, cheap.FinalScore, expensive.FinalScore)
		assert.Less(t, cheap.ComputedPriority, expensive.ComputedPriority)
	})

	t.Run("hysteresis cannot retain the expensive channel above extreme cheap", func(t *testing.T) {
		results := ScoreAutoPriorityCandidates([]AutoPriorityScoreInput{
			{
				ChannelID:               1041,
				LocalGroup:              "shared",
				ChannelType:             constant.ChannelTypeOpenAI,
				CurrentPriority:         251,
				EffectiveRateMultiplier: 0.001,
				Availability:            floatPtr(0),
				MonitorCheckCount:       2,
				FirstTokenLatencyMS:     90000,
				FirstTokenSampleCount:   1,
				ThroughputTps:           1,
				ThroughputSampleCount:   1,
				HasPreviousSnapshot:     true,
				CohortCostFloor:         0.00001,
				CohortCostCeil:          1,
			},
			{
				ChannelID:               1042,
				LocalGroup:              "shared",
				ChannelType:             constant.ChannelTypeOpenAI,
				CurrentPriority:         259,
				EffectiveRateMultiplier: 0.05,
				Availability:            floatPtr(1),
				MonitorCheckCount:       3,
				FirstTokenLatencyMS:     100,
				FirstTokenSampleCount:   1,
				ThroughputTps:           20,
				ThroughputSampleCount:   1,
				HasPreviousSnapshot:     true,
				CohortCostFloor:         0.00001,
				CohortCostCeil:          1,
			},
		}, 1000)

		cheap := resultByChannelID(results, 1041)
		expensive := resultByChannelID(results, 1042)
		require.NotNil(t, cheap)
		require.NotNil(t, expensive)
		assert.Greater(t, cheap.ComputedPriority, expensive.ComputedPriority)
		assert.Greater(t, cheap.NewPriority, expensive.NewPriority)
		assert.True(t, cheap.Applied)
	})

	t.Run("single member uses cohort ceiling for dominance correction", func(t *testing.T) {
		results := ScoreAutoPriorityCandidates([]AutoPriorityScoreInput{
			{
				ChannelID:               1051,
				LocalGroup:              "shared",
				ChannelType:             constant.ChannelTypeOpenAI,
				EffectiveRateMultiplier: 0.001,
				Availability:            floatPtr(0),
				MonitorCheckCount:       2,
				FirstTokenLatencyMS:     90000,
				FirstTokenSampleCount:   1,
				ThroughputTps:           1,
				ThroughputSampleCount:   1,
				CohortCostFloor:         0.00001,
				CohortCostCeil:          0.05,
			},
		}, 1000)

		require.Len(t, results, 1)
		result := results[0]
		syntheticExpensivePriceScore := 100 * result.OrdinaryPriceFloor / 0.05
		syntheticExpensiveFinalScore := 0.75*syntheticExpensivePriceScore + 0.10*100 + 0.08*100 + 0.03*100 + 0.04*100
		syntheticExpensivePriority := int64(math.Round(syntheticExpensiveFinalScore * 10))
		assert.InDelta(t, 0.00001, result.CohortFloor, 1e-12)
		assert.InDelta(t, 0.001, result.OrdinaryPriceFloor, 1e-12)
		assert.Equal(t, "ordinary_band", result.EffectivePriceFloorSource)
		assert.InDelta(t, 100, result.EffectivePriceScore, 0.0001)
		assert.GreaterOrEqual(t, result.FinalScore-syntheticExpensiveFinalScore, 1.0)
		assert.GreaterOrEqual(t, result.ComputedPriority-syntheticExpensivePriority, int64(10))
	})
}

func TestScoreAutoPriorityCandidatesDominanceCleanupPreservesSelectedMargin(t *testing.T) {
	results := ScoreAutoPriorityCandidates([]AutoPriorityScoreInput{
		{
			ChannelID:               1071,
			LocalGroup:              "ceiling-hysteresis-margin",
			ChannelType:             constant.ChannelTypeOpenAI,
			CurrentPriority:         1000,
			EffectiveRateMultiplier: 0.001,
			CacheAdjustedCostFactor: 1,
			UsageLogCount:           autoPriorityFullCacheSampleCount,
			Availability:            floatPtr(1),
			MonitorCheckCount:       3,
			FirstTokenLatencyMS:     autoPriorityFirstTokenSlowMS,
			FirstTokenSampleCount:   1,
			ThroughputTps:           autoPriorityThroughputSlowTps,
			ThroughputSampleCount:   1,
			HasPreviousSnapshot:     true,
		},
		{
			ChannelID:               1072,
			LocalGroup:              "ceiling-hysteresis-margin",
			ChannelType:             constant.ChannelTypeOpenAI,
			CurrentPriority:         995,
			EffectiveRateMultiplier: 0.040,
			CacheAdjustedCostFactor: autoPriorityMinCacheCostFactor,
			UsageLogCount:           autoPriorityFullCacheSampleCount,
			Availability:            floatPtr(1),
			MonitorCheckCount:       3,
			FirstTokenLatencyMS:     autoPriorityFirstTokenFastMS,
			FirstTokenSampleCount:   1,
			ThroughputTps:           autoPriorityThroughputFastTps,
			ThroughputSampleCount:   1,
			HasPreviousSnapshot:     true,
		},
		{
			ChannelID:               1073,
			LocalGroup:              "ceiling-hysteresis-margin",
			ChannelType:             constant.ChannelTypeOpenAI,
			CurrentPriority:         1000,
			EffectiveRateMultiplier: 0.070,
			Availability:            floatPtr(1),
			MonitorCheckCount:       3,
			HasPreviousSnapshot:     true,
		},
	}, 1000)

	cheap := resultByChannelID(results, 1071)
	expensive := resultByChannelID(results, 1072)
	require.NotNil(t, cheap)
	require.NotNil(t, expensive)
	assert.Equal(t, int64(1000), cheap.ComputedPriority)
	assert.Equal(t, int64(1000), cheap.NewPriority)
	assert.Equal(t, int64(990), expensive.ComputedPriority)
	assert.Equal(t, int64(990), expensive.NewPriority)
	assert.Equal(t, autoPriorityDominancePriorityMargin, cheap.NewPriority-expensive.NewPriority)
	assert.True(t, cheap.Applied)
	assert.Empty(t, cheap.Reason)
	assert.True(t, expensive.Applied)
	assert.Empty(t, expensive.Reason)
}

func TestRelativeAutoPriorityPriceScoreAvoidsIntermediateOverflow(t *testing.T) {
	score := relativeAutoPriorityPriceScore(math.MaxFloat64, math.MaxFloat64/8)

	assert.False(t, math.IsNaN(score))
	assert.False(t, math.IsInf(score, 0))
	assert.InDelta(t, 12.5, score, 1e-12)
}

func TestScoreAutoPriorityCandidatesLongDominanceChainUsesAdaptiveScoreMargin(t *testing.T) {
	const tierCount = 102
	inputs := make([]AutoPriorityScoreInput, 0, tierCount)
	rate := 1.0
	for tier := 0; tier < tierCount; tier++ {
		inputs = append(inputs, AutoPriorityScoreInput{
			ChannelID:               2000 + tier,
			LocalGroup:              "long-dominance-chain",
			ChannelType:             constant.ChannelTypeOpenAI,
			EffectiveRateMultiplier: rate,
			CacheAdjustedCostFactor: autoPriorityMinCacheCostFactor,
			UsageLogCount:           autoPriorityFullCacheSampleCount,
			Availability:            floatPtr(1),
			MonitorCheckCount:       3,
			FirstTokenLatencyMS:     autoPriorityFirstTokenFastMS,
			FirstTokenSampleCount:   1,
			ThroughputTps:           autoPriorityThroughputFastTps,
			ThroughputSampleCount:   1,
		})
		rate *= autoPriorityExtremeCostRatio
	}

	results := ScoreAutoPriorityCandidates(inputs, 1000)
	adaptiveScoreMargin := 100.0 / float64(tierCount-1)
	adaptivePriorityMargin := int64(1000 / (tierCount - 1))
	assert.Greater(t, adaptiveScoreMargin, 0.0)
	assert.Less(t, adaptiveScoreMargin, autoPriorityDominanceScoreMargin)
	assert.Greater(t, adaptivePriorityMargin, int64(0))
	assert.Less(t, adaptivePriorityMargin, autoPriorityDominancePriorityMargin)

	for cheapTier := 0; cheapTier < tierCount-1; cheapTier++ {
		cheap := resultByChannelID(results, 2000+cheapTier)
		require.NotNil(t, cheap)
		assert.False(t, math.IsNaN(cheap.FinalScore))
		assert.False(t, math.IsInf(cheap.FinalScore, 0))
		for expensiveTier := cheapTier + 1; expensiveTier < tierCount; expensiveTier++ {
			expensive := resultByChannelID(results, 2000+expensiveTier)
			require.NotNil(t, expensive)
			require.Greater(t, cheap.FinalScore, expensive.FinalScore)
			require.Greater(t, cheap.ComputedPriority, expensive.ComputedPriority)
			require.Greater(t, cheap.NewPriority, expensive.NewPriority)
		}

		adjacentExpensive := resultByChannelID(results, 2001+cheapTier)
		require.NotNil(t, adjacentExpensive)
		assert.GreaterOrEqual(t, cheap.FinalScore-adjacentExpensive.FinalScore, adaptiveScoreMargin-1e-12)
		assert.GreaterOrEqual(t, cheap.ComputedPriority-adjacentExpensive.ComputedPriority, adaptivePriorityMargin)
		assert.GreaterOrEqual(t, cheap.NewPriority-adjacentExpensive.NewPriority, adaptivePriorityMargin)
	}

	permutations := make([][]AutoPriorityScoreInput, 2)
	permutations[0] = make([]AutoPriorityScoreInput, tierCount)
	for i := range inputs {
		permutations[0][i] = inputs[tierCount-1-i]
	}
	permutations[1] = make([]AutoPriorityScoreInput, 0, tierCount)
	const rotation = 37
	for i := range inputs {
		permutations[1] = append(permutations[1], inputs[(i+rotation)%tierCount])
	}

	for _, permutation := range permutations {
		permutedResults := ScoreAutoPriorityCandidates(permutation, 1000)
		for _, result := range results {
			permuted := resultByChannelID(permutedResults, result.ChannelID)
			require.NotNil(t, permuted)
			assert.Equal(t, result.FinalScore, permuted.FinalScore)
			assert.Equal(t, result.ComputedPriority, permuted.ComputedPriority)
			assert.Equal(t, result.NewPriority, permuted.NewPriority)
		}
	}
}

func TestAutoPriorityDeltaBelowThreshold(t *testing.T) {
	assert.True(t, autoPriorityDeltaBelowThreshold(100, 109, 10))
	assert.False(t, autoPriorityDeltaBelowThreshold(100, 110, 10))
	assert.False(t, autoPriorityDeltaBelowThreshold(math.MinInt64+1, math.MaxInt64, 10))
}
