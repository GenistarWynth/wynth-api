package service

import (
	"math"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScoreAutoPriorityCandidates(t *testing.T) {
	t.Run("price dominates within the same cohort", func(t *testing.T) {
		results := ScoreAutoPriorityCandidates([]AutoPriorityScoreInput{
			{
				ChannelID:               101,
				LocalGroup:              " shared ",
				ChannelType:             constant.ChannelTypeOpenAI,
				CurrentPriority:         500,
				EffectiveRateMultiplier: 1,
				Availability:            floatPtr(0.10),
				FirstTokenLatencyMS:     2000,
				UsageLogCount:           4,
				MonitorCheckCount:       8,
				FirstTokenSampleCount:   2,
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
				UsageLogCount:           2,
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
				UsageLogCount:           2,
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

	t.Run("hysteresis blocks small changes when a previous snapshot exists", func(t *testing.T) {
		results := ScoreAutoPriorityCandidates([]AutoPriorityScoreInput{
			{
				ChannelID:               401,
				LocalGroup:              "shared",
				ChannelType:             constant.ChannelTypeOpenAI,
				CurrentPriority:         943,
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
		assert.Equal(t, int64(943), result.OldPriority)
		assert.Equal(t, int64(949), result.ComputedPriority)
		assert.False(t, result.Applied)
		assert.Equal(t, int64(943), result.NewPriority)
		assert.Equal(t, "hysteresis_delta_below_threshold", result.Reason)
	})

	t.Run("small changes apply when there is no previous snapshot", func(t *testing.T) {
		results := ScoreAutoPriorityCandidates([]AutoPriorityScoreInput{
			{
				ChannelID:               402,
				LocalGroup:              "shared",
				ChannelType:             constant.ChannelTypeOpenAI,
				CurrentPriority:         943,
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

	t.Run("zero latency sample participates and beats neutral first token", func(t *testing.T) {
		results := ScoreAutoPriorityCandidates([]AutoPriorityScoreInput{
			{
				ChannelID:               602,
				LocalGroup:              "shared",
				ChannelType:             constant.ChannelTypeOpenAI,
				CurrentPriority:         200,
				EffectiveRateMultiplier: 1,
				FirstTokenLatencyMS:     0,
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
				FirstTokenLatencyMS:     100,
				UsageLogCount:           1,
				MonitorCheckCount:       1,
				FirstTokenSampleCount:   1,
			},
		}, 1000)

		require.Len(t, results, 2)
		zeroLatency := resultByChannelID(results, 602)
		slowLatency := resultByChannelID(results, 603)

		require.NotNil(t, zeroLatency)
		require.NotNil(t, slowLatency)
		assert.Greater(t, zeroLatency.FirstTokenScore, slowLatency.FirstTokenScore)
		assert.NotEqual(t, 70, zeroLatency.FirstTokenScore)
		assert.NotEqual(t, 70, slowLatency.FirstTokenScore)
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

func TestAutoPriorityDeltaBelowThreshold(t *testing.T) {
	assert.True(t, autoPriorityDeltaBelowThreshold(100, 109, 10))
	assert.False(t, autoPriorityDeltaBelowThreshold(100, 110, 10))
	assert.False(t, autoPriorityDeltaBelowThreshold(math.MinInt64+1, math.MaxInt64, 10))
}
