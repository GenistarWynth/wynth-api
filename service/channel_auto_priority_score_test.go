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
	assert.Equal(t, 0.85, autoPriorityPriceWeight)
	assert.Equal(t, 0.08, autoPriorityAvailabilityWeight)
	assert.Equal(t, 0.03, autoPriorityFirstTokenWeight)
	assert.Equal(t, 0.04, autoPriorityThroughputWeight)
	assert.InDelta(t, 1.0, autoPriorityPriceWeight+autoPriorityAvailabilityWeight+autoPriorityFirstTokenWeight+autoPriorityThroughputWeight, 1e-12)
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
		// or throughput samples, so both use the neutral default.
		assert.InDelta(t, 0.85*100+0.08*50+0.03*autoPriorityNeutralPerfScore+0.04*autoPriorityNeutralPerfScore, results[0].FinalScore, 1e-9)
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
		// or throughput samples, so both use the neutral default.
		assert.InDelta(t, 0.85*100+0.08*0+0.03*autoPriorityNeutralPerfScore+0.04*autoPriorityNeutralPerfScore, results[0].FinalScore, 1e-9)
		assert.Equal(t, int64(899), results[0].ComputedPriority)
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

	t.Run("cache adjusted cost changes the effective price ordering", func(t *testing.T) {
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
		assert.Greater(t, r302.EffectivePriceScore, r301.EffectivePriceScore)
		assert.Greater(t, r302.ComputedPriority, r301.ComputedPriority)
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
		assert.InDelta(t, 0.87, results[0].CacheAdjustedCostFactor, 0.0001)
		assert.InDelta(t, 0.87, results[0].EffectiveCostMultiplier, 0.0001)
	})

	t.Run("partial usage sample blends cache benefit toward neutral", func(t *testing.T) {
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
		assert.InDelta(t, 0.75, results[0].CacheAdjustedCostFactor, 0.0001)
		assert.InDelta(t, 1.5, results[0].EffectiveCostMultiplier, 0.0001)
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
		assert.InDelta(t, 0.85, results[0].CacheAdjustedCostFactor, 0.0001)
		assert.InDelta(t, 1.7, results[0].EffectiveCostMultiplier, 0.0001)
	})

	t.Run("hysteresis blocks small changes when a previous snapshot exists", func(t *testing.T) {
		results := ScoreAutoPriorityCandidates([]AutoPriorityScoreInput{
			{
				ChannelID:               401,
				LocalGroup:              "shared",
				ChannelType:             constant.ChannelTypeOpenAI,
				CurrentPriority:         967,
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
		assert.Equal(t, int64(967), result.OldPriority)
		assert.Equal(t, int64(964), result.ComputedPriority)
		assert.False(t, result.Applied)
		assert.Equal(t, int64(967), result.NewPriority)
		assert.Equal(t, "hysteresis_delta_below_threshold", result.Reason)
	})

	t.Run("small changes apply when there is no previous snapshot", func(t *testing.T) {
		results := ScoreAutoPriorityCandidates([]AutoPriorityScoreInput{
			{
				ChannelID:               402,
				LocalGroup:              "shared",
				ChannelType:             constant.ChannelTypeOpenAI,
				CurrentPriority:         967,
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
	base := []AutoPriorityScoreInput{
		{ChannelID: 1, LocalGroup: "shared", ChannelType: constant.ChannelTypeOpenAI, EffectiveRateMultiplier: 0.02, CacheAdjustedCostFactor: 0.35, UsageLogCount: 421},
		{ChannelID: 2, LocalGroup: "shared", ChannelType: constant.ChannelTypeOpenAI, EffectiveRateMultiplier: 0.05, CacheAdjustedCostFactor: 0.35, UsageLogCount: 200},
	}

	t.Run("zero samples use a conservative mature peer prior", func(t *testing.T) {
		inputs := append(append([]AutoPriorityScoreInput{}, base...), AutoPriorityScoreInput{
			ChannelID: 3, LocalGroup: "shared", ChannelType: constant.ChannelTypeOpenAI,
			EffectiveRateMultiplier: 0.02, UsageLogCount: 0,
		})
		results := ScoreAutoPriorityCandidates(inputs, 1000)
		fresh := resultByChannelID(results, 3)
		mature := resultByChannelID(results, 1)
		require.NotNil(t, fresh)
		require.NotNil(t, mature)
		assert.Greater(t, fresh.CacheAdjustedCostFactor, mature.CacheAdjustedCostFactor)
		assert.Less(t, fresh.CacheAdjustedCostFactor, 1.0)
		assert.Greater(t, fresh.EffectivePriceScore, 35.0)
		assert.Less(t, fresh.EffectivePriceScore, mature.EffectivePriceScore)
	})

	t.Run("no trustworthy same cohort peer stays neutral", func(t *testing.T) {
		results := ScoreAutoPriorityCandidates([]AutoPriorityScoreInput{
			{ChannelID: 10, LocalGroup: "shared", ChannelType: constant.ChannelTypeOpenAI, EffectiveRateMultiplier: 0.02},
			{ChannelID: 11, LocalGroup: "shared", ChannelType: constant.ChannelTypeOpenAI, EffectiveRateMultiplier: 0.03, CacheAdjustedCostFactor: 0.35, UsageLogCount: 19},
			{ChannelID: 12, LocalGroup: "other", ChannelType: constant.ChannelTypeOpenAI, EffectiveRateMultiplier: 0.03, CacheAdjustedCostFactor: 0.35, UsageLogCount: 200},
			{ChannelID: 13, LocalGroup: "shared", ChannelType: constant.ChannelTypeAnthropic, EffectiveRateMultiplier: 0.03, CacheAdjustedCostFactor: 0.35, UsageLogCount: 200},
		}, 1000)
		fresh := resultByChannelID(results, 10)
		require.NotNil(t, fresh)
		assert.Equal(t, 1.0, fresh.CacheAdjustedCostFactor)
	})

	t.Run("median resists one pathological peer", func(t *testing.T) {
		inputs := append(append([]AutoPriorityScoreInput{}, base...),
			AutoPriorityScoreInput{ChannelID: 4, LocalGroup: "shared", ChannelType: constant.ChannelTypeOpenAI, EffectiveRateMultiplier: 0.04, CacheAdjustedCostFactor: 50, UsageLogCount: 200},
			AutoPriorityScoreInput{ChannelID: 5, LocalGroup: "shared", ChannelType: constant.ChannelTypeOpenAI, EffectiveRateMultiplier: 0.02},
		)
		fresh := resultByChannelID(ScoreAutoPriorityCandidates(inputs, 1000), 5)
		require.NotNil(t, fresh)
		assert.InDelta(t, 0.675, fresh.CacheAdjustedCostFactor, 1e-9)
	})

	t.Run("a lone pathological peer cannot make cold start worse than neutral", func(t *testing.T) {
		fresh := resultByChannelID(ScoreAutoPriorityCandidates([]AutoPriorityScoreInput{
			{ChannelID: 20, LocalGroup: "shared", ChannelType: constant.ChannelTypeOpenAI, EffectiveRateMultiplier: 0.04, CacheAdjustedCostFactor: 50, UsageLogCount: 200},
			{ChannelID: 21, LocalGroup: "shared", ChannelType: constant.ChannelTypeOpenAI, EffectiveRateMultiplier: 0.02},
		}, 1000), 21)
		require.NotNil(t, fresh)
		assert.Equal(t, 1.0, fresh.CacheAdjustedCostFactor)
	})

	t.Run("own samples continuously replace the prior", func(t *testing.T) {
		factors := make([]float64, 0, 4)
		for _, count := range []int64{0, 1, 10, 20} {
			inputs := append(append([]AutoPriorityScoreInput{}, base...), AutoPriorityScoreInput{
				ChannelID: 6, LocalGroup: "shared", ChannelType: constant.ChannelTypeOpenAI,
				EffectiveRateMultiplier: 0.02, CacheAdjustedCostFactor: 0.5, UsageLogCount: count,
			})
			fresh := resultByChannelID(ScoreAutoPriorityCandidates(inputs, 1000), 6)
			require.NotNil(t, fresh)
			factors = append(factors, fresh.CacheAdjustedCostFactor)
		}
		assert.Equal(t, []float64{0.675, 0.66625, 0.5875, 0.5}, factors)
	})
}

func TestAutoPriorityColdStartDiagnosticsRoundTrip(t *testing.T) {
	results := ScoreAutoPriorityCandidates([]AutoPriorityScoreInput{
		{ChannelID: 1, LocalGroup: "shared", ChannelType: constant.ChannelTypeOpenAI, EffectiveRateMultiplier: 0.02, CacheAdjustedCostFactor: 0.35, UsageLogCount: 100},
		{ChannelID: 2, LocalGroup: "shared", ChannelType: constant.ChannelTypeOpenAI, EffectiveRateMultiplier: 0.02},
	}, 1000)
	fresh := resultByChannelID(results, 2)
	require.NotNil(t, fresh)
	assert.Equal(t, "cohort_prior", fresh.CacheFactorSource)
	assert.Equal(t, 0.675, fresh.CacheFactorPrior)
	assert.Equal(t, 0.0, fresh.CacheFactorOwnConfidence)

	snapshot := buildChannelAutoPriorityScoreSnapshot(*fresh, 100, 200)
	encoded, err := common.Marshal(snapshot)
	require.NoError(t, err)
	var decoded dto.ChannelAutoPriorityScore
	require.NoError(t, common.Unmarshal(encoded, &decoded))
	assert.Equal(t, fresh.Cohort, decoded.Cohort)
	assert.Equal(t, fresh.EffectiveRateMultiplier, decoded.EffectiveRateMultiplier)
	assert.Equal(t, fresh.CacheAdjustedCostFactor, decoded.CacheAdjustedCostFactor)
	assert.Equal(t, fresh.EffectiveCostMultiplier, decoded.EffectiveCostMultiplier)
	assert.Equal(t, fresh.EffectivePriceScore, decoded.EffectivePriceScore)
	assert.Equal(t, fresh.AvailabilityScore, decoded.AvailabilityScore)
	assert.Equal(t, fresh.FirstTokenScore, decoded.FirstTokenScore)
	assert.Equal(t, fresh.ThroughputScore, decoded.ThroughputScore)
	assert.Equal(t, fresh.FinalScore, decoded.FinalScore)
	assert.Equal(t, fresh.OldPriority, decoded.OldPriority)
	assert.Equal(t, fresh.NewPriority, decoded.NewPriority)
	assert.Equal(t, fresh.Applied, decoded.Applied)
	assert.Equal(t, "cohort_prior", decoded.CacheFactorSource)
	assert.Equal(t, 0.675, decoded.CacheFactorPrior)
	assert.Equal(t, 0.0, decoded.CacheFactorOwnConfidence)

	// New fields remain optional so snapshots written by older versions still decode.
	var legacy dto.ChannelAutoPriorityScore
	require.NoError(t, common.Unmarshal([]byte(`{"effective_rate_multiplier":0.02,"cache_adjusted_cost_factor":1}`), &legacy))
	assert.Empty(t, legacy.CacheFactorSource)
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
		// price 100 + availability 100, but no first-token/throughput samples (neutral 70):
		// 0.85*100 + 0.08*100 + 0.03*70 + 0.04*70 = 97.9 -> 979.
		assert.Equal(t, int64(979), results[0].ComputedPriority)
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

		assert.Greater(t, cheapest.EffectivePriceScore, dearest.EffectivePriceScore)
		assert.Greater(t, cheapest.ComputedPriority, dearest.ComputedPriority)
		assert.False(t, cheapest.EffectivePriceScore == middle.EffectivePriceScore && middle.EffectivePriceScore == dearest.EffectivePriceScore,
			"cost must differentiate priority across the widened cohort instead of all scoring equally")
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

func TestScoreAutoPriorityCandidatesExtremeCostDominance(t *testing.T) {
	t.Run("four times cost gap has a materially stronger formula-based advantage", func(t *testing.T) {
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

		previousWeightedScore := func(result *AutoPriorityScoreResult) float64 {
			return 0.75*result.EffectivePriceScore +
				0.12*result.AvailabilityScore +
				0.05*result.FirstTokenScore +
				0.08*result.ThroughputScore
		}
		previousGap := previousWeightedScore(cheap) - previousWeightedScore(expensive)
		currentGap := cheap.FinalScore - expensive.FinalScore

		// This pins the representative formula inputs, not a new hard 4x rule.
		assert.InDelta(t, 100, cheap.EffectivePriceScore, 1e-9)
		assert.InDelta(t, 25, expensive.EffectivePriceScore, 1e-9)
		assert.Greater(t, currentGap, previousGap+5)
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
		assert.InDelta(t, 1, cheap.EffectivePriceScore, 1e-12)
		assert.InDelta(t, 0.125, expensive.EffectivePriceScore, 1e-12)
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
		syntheticExpensivePriceScore := 100 * 0.00001 / 0.05
		syntheticExpensiveFinalScore := 0.85*syntheticExpensivePriceScore + 0.08*100 + 0.03*100 + 0.04*100
		syntheticExpensivePriority := int64(math.Round(syntheticExpensiveFinalScore * 10))
		assert.InDelta(t, 1, result.EffectivePriceScore, 0.0001)
		assert.GreaterOrEqual(t, result.FinalScore-syntheticExpensiveFinalScore, 1.0)
		assert.GreaterOrEqual(t, result.ComputedPriority-syntheticExpensivePriority, int64(10))
	})
}

func TestAutoPriorityDeltaBelowThreshold(t *testing.T) {
	assert.True(t, autoPriorityDeltaBelowThreshold(100, 109, 10))
	assert.False(t, autoPriorityDeltaBelowThreshold(100, 110, 10))
	assert.False(t, autoPriorityDeltaBelowThreshold(math.MinInt64+1, math.MaxInt64, 10))
}
