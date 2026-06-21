package service

import (
	"fmt"
	"math"
	"strings"
)

type AutoPriorityScoreInput struct {
	ChannelID               int
	LocalGroup              string
	ChannelType             int
	CurrentPriority         int64
	EffectiveRateMultiplier float64
	CacheAdjustedCostFactor float64
	Availability            *float64
	FirstTokenLatencyMS     float64
	UsageLogCount           int64
	MonitorCheckCount       int64
	FirstTokenSampleCount   int64
	HasPreviousSnapshot     bool
}

type AutoPriorityScoreResult struct {
	ChannelID               int
	Cohort                  string
	EffectiveRateMultiplier float64
	CacheAdjustedCostFactor float64
	EffectiveCostMultiplier float64
	EffectivePriceScore     float64
	AvailabilityScore       float64
	FirstTokenScore         float64
	FinalScore              float64
	OldPriority             int64
	ComputedPriority        int64
	NewPriority             int64
	Applied                 bool
	Reason                  string
	UsageLogCount           int64
	MonitorCheckCount       int64
	FirstTokenSampleCount   int64
}

func ScoreAutoPriorityCandidates(inputs []AutoPriorityScoreInput, maxPriority int) []AutoPriorityScoreResult {
	if maxPriority <= 0 {
		maxPriority = 1000
	}

	results := make([]AutoPriorityScoreResult, len(inputs))
	priceCohorts := make(map[string][]int)
	firstTokenCohorts := make(map[string][]int)

	for i, input := range inputs {
		cohort := autoPriorityCohortKey(input.LocalGroup, input.ChannelType)
		cacheFactor := normalizedAutoPriorityCacheFactor(input.CacheAdjustedCostFactor)
		result := AutoPriorityScoreResult{
			ChannelID:               input.ChannelID,
			Cohort:                  cohort,
			EffectiveRateMultiplier: input.EffectiveRateMultiplier,
			CacheAdjustedCostFactor: cacheFactor,
			OldPriority:             input.CurrentPriority,
			AvailabilityScore:       autoPriorityAvailabilityScore(input.Availability),
			FirstTokenScore:         70,
			UsageLogCount:           input.UsageLogCount,
			MonitorCheckCount:       input.MonitorCheckCount,
			FirstTokenSampleCount:   input.FirstTokenSampleCount,
		}

		if !isValidAutoPriorityMultiplier(input.EffectiveRateMultiplier) {
			result.Reason = "missing_effective_rate_multiplier"
			result.ComputedPriority = input.CurrentPriority
			result.NewPriority = input.CurrentPriority
			result.Applied = false
			result.AvailabilityScore = autoPriorityAvailabilityScore(input.Availability)
			result.FirstTokenScore = 70
			results[i] = result
			continue
		}

		result.EffectiveCostMultiplier = input.EffectiveRateMultiplier * cacheFactor
		priceCohorts[cohort] = append(priceCohorts[cohort], i)
		if hasSampledFirstTokenLatency(input.FirstTokenSampleCount, input.FirstTokenLatencyMS) {
			firstTokenCohorts[cohort] = append(firstTokenCohorts[cohort], i)
		}
		results[i] = result
	}

	for _, indexes := range priceCohorts {
		if len(indexes) == 0 {
			continue
		}

		minCost := results[indexes[0]].EffectiveCostMultiplier
		maxCost := minCost
		for _, idx := range indexes[1:] {
			cost := results[idx].EffectiveCostMultiplier
			if cost < minCost {
				minCost = cost
			}
			if cost > maxCost {
				maxCost = cost
			}
		}

		for _, idx := range indexes {
			results[idx].EffectivePriceScore = normalizedAutoPriorityDescendingScore(results[idx].EffectiveCostMultiplier, minCost, maxCost)
		}

		if len(indexes) == 1 || nearlyEqualFloat64(minCost, maxCost) {
			results[indexes[0]].EffectivePriceScore = 100
		}
	}

	for _, indexes := range firstTokenCohorts {
		if len(indexes) == 0 {
			continue
		}

		minLatency := inputs[indexes[0]].FirstTokenLatencyMS
		maxLatency := minLatency
		for _, idx := range indexes[1:] {
			latency := inputs[idx].FirstTokenLatencyMS
			if latency < minLatency {
				minLatency = latency
			}
			if latency > maxLatency {
				maxLatency = latency
			}
		}

		for _, idx := range indexes {
			results[idx].FirstTokenScore = normalizedAutoPriorityDescendingScore(inputs[idx].FirstTokenLatencyMS, minLatency, maxLatency)
		}

		if len(indexes) == 1 || nearlyEqualFloat64(minLatency, maxLatency) {
			results[indexes[0]].FirstTokenScore = 100
		}
	}

	for i := range results {
		if results[i].Reason != "" {
			continue
		}

		results[i].FinalScore = 0.80*results[i].EffectivePriceScore + 0.17*results[i].AvailabilityScore + 0.03*results[i].FirstTokenScore
		results[i].ComputedPriority = clampAutoPriorityPriority(int64(math.Round(results[i].FinalScore*10)), 0, int64(maxPriority))
		results[i].NewPriority = results[i].ComputedPriority

		if inputs[i].HasPreviousSnapshot && autoPriorityDeltaBelowThreshold(results[i].OldPriority, results[i].ComputedPriority, 10) {
			results[i].Applied = false
			results[i].NewPriority = results[i].OldPriority
			results[i].Reason = "hysteresis_delta_below_threshold"
			continue
		}

		results[i].Applied = true
		results[i].Reason = ""
	}

	return results
}

func autoPriorityCohortKey(localGroup string, channelType int) string {
	return strings.TrimSpace(localGroup) + "#" + fmt.Sprint(channelType)
}

func isValidAutoPriorityMultiplier(v float64) bool {
	return v > 0 && !math.IsNaN(v) && !math.IsInf(v, 0)
}

func normalizedAutoPriorityCacheFactor(v float64) float64 {
	if v <= 0 || math.IsNaN(v) || math.IsInf(v, 0) {
		return 1
	}
	return v
}

func autoPriorityAvailabilityScore(avail *float64) float64 {
	if avail == nil {
		return 70
	}
	if math.IsNaN(*avail) || math.IsInf(*avail, 0) {
		return 70
	}
	score := *avail * 100
	if score < 0 {
		return 0
	}
	if score > 100 {
		return 100
	}
	return score
}

func hasSampledFirstTokenLatency(sampleCount int64, latencyMS float64) bool {
	return sampleCount > 0 && latencyMS >= 0 && !math.IsNaN(latencyMS) && !math.IsInf(latencyMS, 0)
}

func normalizedAutoPriorityDescendingScore(value, minValue, maxValue float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	if maxValue <= minValue || nearlyEqualFloat64(minValue, maxValue) {
		return 100
	}

	if value < minValue {
		value = minValue
	}
	if value > maxValue {
		value = maxValue
	}

	span := maxValue - minValue
	if span <= 0 {
		return 100
	}

	logDenominator := math.Log1p(span)
	if logDenominator <= 0 || math.IsNaN(logDenominator) || math.IsInf(logDenominator, 0) {
		return 100
	}

	logValue := math.Log1p(value - minValue)
	if logValue < 0 {
		logValue = 0
	}

	score := 100 * (1 - logValue/logDenominator)
	if score < 0 {
		return 0
	}
	if score > 100 {
		return 100
	}
	return score
}

func clampAutoPriorityPriority(value, minValue, maxValue int64) int64 {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

func autoPriorityDeltaBelowThreshold(oldPriority, computedPriority, threshold int64) bool {
	if threshold <= 0 {
		return false
	}

	delta := threshold - 1
	lower, lowerOK := safeAddInt64(oldPriority, -delta)
	if !lowerOK {
		lower = math.MinInt64
	}

	upper, upperOK := safeAddInt64(oldPriority, delta)
	if !upperOK {
		upper = math.MaxInt64
	}

	return computedPriority >= lower && computedPriority <= upper
}

func safeAddInt64(a, b int64) (int64, bool) {
	if b > 0 && a > math.MaxInt64-b {
		return 0, false
	}
	if b < 0 && a < math.MinInt64-b {
		return 0, false
	}
	return a + b, true
}

func nearlyEqualFloat64(a, b float64) bool {
	return math.Abs(a-b) <= 1e-12
}
