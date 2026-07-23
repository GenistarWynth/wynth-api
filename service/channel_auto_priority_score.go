package service

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

type AutoPriorityScoreInput struct {
	ChannelID                       int
	LocalGroup                      string
	ChannelType                     int
	CurrentPriority                 int64
	EffectiveRateMultiplier         float64
	CacheAdjustedCostFactor         float64
	PreviousCacheAdjustedCostFactor float64
	PreviousEffectiveCostMultiplier float64
	Availability                    *float64
	FirstTokenLatencyMS             float64
	ThroughputTps                   float64
	UsageLogCount                   int64
	MonitorCheckCount               int64
	FirstTokenSampleCount           int64
	ThroughputSampleCount           int64
	HasPreviousSnapshot             bool
	HardUnavailable                 bool

	// CohortCostFloor/CohortCostCeil retain their legacy names, but when > 0 they
	// are local-group-wide nominal rate multiplier bounds (across all upstream
	// sources) for this input's cohort. Cache behavior must not move the price
	// normalization range. 0 = unset (legacy behavior).
	CohortCostFloor float64
	CohortCostCeil  float64
}

type AutoPriorityScoreResult struct {
	ChannelID                int
	Cohort                   string
	CohortFloor              float64
	CohortCeil               float64
	CohortMemberCount        int
	EffectiveRateMultiplier  float64
	NominalRateMultiplier    float64
	CacheAdjustedCostFactor  float64
	EffectiveCostMultiplier  float64
	EffectivePriceScore      float64
	NominalPriceScore        float64
	CacheScore               float64
	AvailabilityScore        float64
	FirstTokenScore          float64
	ThroughputScore          float64
	FinalScore               float64
	OldPriority              int64
	ComputedPriority         int64
	NewPriority              int64
	Applied                  bool
	Reason                   string
	UsageLogCount            int64
	MonitorCheckCount        int64
	FirstTokenSampleCount    int64
	ThroughputSampleCount    int64
	CacheFactorSource        string
	CacheFactorPrior         float64
	CacheFactorOwnConfidence float64
}

const (
	autoPriorityFullCacheSampleCount int64   = 20
	autoPriorityMinCacheCostFactor   float64 = 0.35
	// The zero-sample cache component starts at exactly 95/100. Derive the
	// corresponding factor from the inverse of autoPriorityCacheScore so this is
	// never confused with a 0.95 cost factor.
	autoPriorityDefaultCacheScore  float64 = 95
	autoPriorityDefaultCacheFactor float64 = 1 -
		(autoPriorityDefaultCacheScore/100)*(1-autoPriorityMinCacheCostFactor)
	autoPriorityCurrentSmoothingWeight  float64 = 0.65
	autoPriorityPreviousSmoothingWeight float64 = 0.35
	autoPriorityPriceWeight             float64 = 0.75
	autoPriorityCacheWeight             float64 = 0.10
	autoPriorityAvailabilityWeight      float64 = 0.08
	autoPriorityFirstTokenWeight        float64 = 0.03
	autoPriorityThroughputWeight        float64 = 0.04

	autoPriorityExtremeCostRatio        float64 = 8
	autoPriorityDominanceScoreMargin    float64 = 1
	autoPriorityDominancePriorityMargin int64   = 10

	// Below this measured availability the whole score is scaled down linearly,
	// so an unavailable channel cannot outrank a healthy one on price alone.
	autoPriorityAvailabilityGateKnee float64 = 0.5
	// Minimum monitor checks in the window before the gate trusts the
	// availability ratio; protects fresh channels from one noisy failed probe.
	autoPriorityMinAvailabilityGateSamples int64 = 3

	// Neutral score for a performance metric (first-token, throughput) that has no
	// samples in the window: it neither rewards nor hard-penalizes an unmeasured channel.
	autoPriorityNeutralPerfScore float64 = 70

	// First-token latency (TTFT) is scored on an ABSOLUTE, log-scaled curve, not
	// relative to the cohort: <= fast anchor -> 100, >= slow anchor -> 0. Absolute
	// anchoring is what lets a channel that is the only one measured still reflect
	// its real latency instead of being auto-set to 100. Tunable.
	autoPriorityFirstTokenFastMS float64 = 5000
	autoPriorityFirstTokenSlowMS float64 = 60000

	// Output throughput (tokens/sec) is scored on an ABSOLUTE, log-scaled ascending
	// curve: >= fast anchor -> 100, <= slow anchor -> 0. Matches the "流 · N t/s"
	// value shown in the usage logs (completion_tokens / use_time). Tunable.
	autoPriorityThroughputSlowTps float64 = 1
	autoPriorityThroughputFastTps float64 = 20
)

func ScoreAutoPriorityCandidates(inputs []AutoPriorityScoreInput, maxPriority int) []AutoPriorityScoreResult {
	if maxPriority <= 0 {
		maxPriority = 1000
	}

	results := make([]AutoPriorityScoreResult, len(inputs))
	priceCohorts := make(map[string][]int)
	cohortCostFloors := make(map[string]float64)
	availabilityGates := make([]float64, len(inputs))

	for i, input := range inputs {
		cohort := autoPriorityCohortKey(input.LocalGroup, input.ChannelType)
		cacheFactor, cachePrior, ownConfidence, cacheSource := autoPriorityGuardedCacheFactor(
			input.CacheAdjustedCostFactor,
			input.UsageLogCount,
		)
		result := AutoPriorityScoreResult{
			ChannelID:                input.ChannelID,
			Cohort:                   cohort,
			EffectiveRateMultiplier:  input.EffectiveRateMultiplier,
			NominalRateMultiplier:    input.EffectiveRateMultiplier,
			CacheAdjustedCostFactor:  cacheFactor,
			CacheScore:               autoPriorityCacheScore(cacheFactor),
			OldPriority:              input.CurrentPriority,
			AvailabilityScore:        autoPriorityAvailabilityScore(input.Availability),
			FirstTokenScore:          autoPriorityNeutralPerfScore,
			ThroughputScore:          autoPriorityNeutralPerfScore,
			UsageLogCount:            input.UsageLogCount,
			MonitorCheckCount:        input.MonitorCheckCount,
			FirstTokenSampleCount:    input.FirstTokenSampleCount,
			ThroughputSampleCount:    input.ThroughputSampleCount,
			CacheFactorSource:        cacheSource,
			CacheFactorPrior:         cachePrior,
			CacheFactorOwnConfidence: ownConfidence,
		}

		if !isValidAutoPriorityMultiplier(input.EffectiveRateMultiplier) {
			result.Reason = "missing_effective_rate_multiplier"
			result.ComputedPriority = input.CurrentPriority
			result.NewPriority = input.CurrentPriority
			result.Applied = false
			results[i] = result
			continue
		}

		result.EffectiveCostMultiplier = input.EffectiveRateMultiplier * cacheFactor
		// Previous snapshots smooth only the backward-compatible effective-cost
		// diagnostic. They must not change the exact default/count-confidence
		// transition used by the cache component.
		if input.HasPreviousSnapshot && isValidAutoPriorityMultiplier(input.PreviousCacheAdjustedCostFactor) {
			smoothedCacheFactor := autoPriorityCurrentSmoothingWeight*cacheFactor +
				autoPriorityPreviousSmoothingWeight*input.PreviousCacheAdjustedCostFactor
			result.EffectiveCostMultiplier = input.EffectiveRateMultiplier * smoothedCacheFactor
		}
		// Snapshots predating cache-factor diagnostics preserve the legacy
		// effective-cost smoothing behavior without changing current cache score.
		if input.HasPreviousSnapshot &&
			!isValidAutoPriorityMultiplier(input.PreviousCacheAdjustedCostFactor) &&
			isValidAutoPriorityMultiplier(input.PreviousEffectiveCostMultiplier) {
			result.EffectiveCostMultiplier = autoPriorityCurrentSmoothingWeight*result.EffectiveCostMultiplier +
				autoPriorityPreviousSmoothingWeight*input.PreviousEffectiveCostMultiplier
		}
		priceCohorts[cohort] = append(priceCohorts[cohort], i)

		// First-token and throughput are scored on ABSOLUTE curves (not relative to the
		// cohort): a channel that is the only one measured must reflect its real latency
		// and speed, never be auto-set to 100. Missing samples keep the neutral default.
		if hasSampledFirstTokenLatency(input.FirstTokenSampleCount, input.FirstTokenLatencyMS) {
			result.FirstTokenScore = normalizedAutoPriorityDescendingScore(input.FirstTokenLatencyMS, autoPriorityFirstTokenFastMS, autoPriorityFirstTokenSlowMS)
		}
		if hasSampledThroughput(input.ThroughputSampleCount, input.ThroughputTps) {
			result.ThroughputScore = absoluteAutoPriorityAscendingScore(input.ThroughputTps, autoPriorityThroughputSlowTps, autoPriorityThroughputFastTps)
		}
		results[i] = result
	}

	for _, indexes := range priceCohorts {
		if len(indexes) == 0 {
			continue
		}

		minNominalRate := results[indexes[0]].NominalRateMultiplier
		maxNominalRate := minNominalRate
		for _, idx := range indexes[1:] {
			nominalRate := results[idx].NominalRateMultiplier
			if nominalRate < minNominalRate {
				minNominalRate = nominalRate
			}
			if nominalRate > maxNominalRate {
				maxNominalRate = nominalRate
			}
		}

		// Widen the cohort floor with local-group-wide nominal rate data. Scoring
		// against this cache-independent floor preserves relative price gaps:
		// close prices stay close enough for cache and quality to matter.
		for _, idx := range indexes {
			if floor := inputs[idx].CohortCostFloor; isValidAutoPriorityMultiplier(floor) && floor < minNominalRate {
				minNominalRate = floor
			}
			if ceil := inputs[idx].CohortCostCeil; isValidAutoPriorityMultiplier(ceil) && ceil > maxNominalRate {
				maxNominalRate = ceil
			}
		}
		cohortCostFloors[results[indexes[0]].Cohort] = minNominalRate
		for _, idx := range indexes {
			priceScore := relativeAutoPriorityPriceScore(results[idx].NominalRateMultiplier, minNominalRate)
			// EffectivePriceScore is retained as a backward-compatible JSON/API
			// field. It now aliases the nominal, cache-independent price score.
			results[idx].EffectivePriceScore = priceScore
			results[idx].NominalPriceScore = priceScore
			results[idx].CohortFloor = minNominalRate
			results[idx].CohortCeil = maxNominalRate
			results[idx].CohortMemberCount = len(indexes)
		}
	}

	for i := range results {
		if results[i].Reason != "" {
			continue
		}
		if inputs[i].HardUnavailable {
			availabilityGates[i] = 0
			results[i].AvailabilityScore = 0
			results[i].FinalScore = 0
			results[i].ComputedPriority = 0
			results[i].NewPriority = results[i].OldPriority
			results[i].Applied = false
			results[i].Reason = "channel_auto_disabled"
			continue
		}

		// Availability multiplicatively gates the weighted sum: a channel whose
		// probes keep failing must fall to the bottom of the rotation no matter
		// how cheap it is, and recover automatically once probes succeed again.
		gate := autoPriorityAvailabilityGate(inputs[i].Availability, inputs[i].MonitorCheckCount)
		availabilityGates[i] = gate
		results[i].FinalScore = weightedAutoPriorityFinalScore(
			gate,
			results[i].NominalPriceScore,
			results[i].CacheScore,
			results[i].AvailabilityScore,
			results[i].FirstTokenScore,
			results[i].ThroughputScore,
		)
		results[i].ComputedPriority = clampAutoPriorityPriority(int64(math.Round(results[i].FinalScore*10)), 0, int64(maxPriority))
		results[i].NewPriority = results[i].ComputedPriority
	}

	dominanceProtected := applyAutoPriorityExtremeCostDominance(
		inputs,
		results,
		priceCohorts,
		cohortCostFloors,
		availabilityGates,
		int64(maxPriority),
	)

	for i := range results {
		if results[i].Reason != "" {
			continue
		}
		if dominanceProtected[i] {
			results[i].Applied = true
			continue
		}
		if inputs[i].HasPreviousSnapshot && autoPriorityDeltaBelowThreshold(results[i].OldPriority, results[i].ComputedPriority, 10) {
			results[i].Applied = false
			results[i].NewPriority = results[i].OldPriority
			results[i].Reason = "hysteresis_delta_below_threshold"
			continue
		}

		results[i].Applied = true
		results[i].Reason = ""
	}

	removeAutoPriorityHysteresisDominanceViolations(results, priceCohorts, availabilityGates)

	return results
}

func relativeAutoPriorityPriceScore(cost, cohortFloor float64) float64 {
	if !isValidAutoPriorityMultiplier(cost) || !isValidAutoPriorityMultiplier(cohortFloor) {
		return 0
	}
	if cost <= cohortFloor {
		return 100
	}

	score := 100 * cohortFloor / cost
	if math.IsNaN(score) || math.IsInf(score, 0) || score < 0 {
		return 0
	}
	if score > 100 {
		return 100
	}
	return score
}

func weightedAutoPriorityFinalScore(gate, price, cache, availability, firstToken, throughput float64) float64 {
	return gate * (autoPriorityPriceWeight*price +
		autoPriorityCacheWeight*cache +
		autoPriorityAvailabilityWeight*availability +
		autoPriorityFirstTokenWeight*firstToken +
		autoPriorityThroughputWeight*throughput)
}

func applyAutoPriorityExtremeCostDominance(
	inputs []AutoPriorityScoreInput,
	results []AutoPriorityScoreResult,
	priceCohorts map[string][]int,
	cohortCostFloors map[string]float64,
	availabilityGates []float64,
	maxPriority int64,
) []bool {
	protected := make([]bool, len(results))
	for cohort, indexes := range priceCohorts {
		ordered := append([]int(nil), indexes...)
		sort.Slice(ordered, func(i, j int) bool {
			left := results[ordered[i]]
			right := results[ordered[j]]
			if left.NominalRateMultiplier == right.NominalRateMultiplier {
				return left.ChannelID < right.ChannelID
			}
			return left.NominalRateMultiplier > right.NominalRateMultiplier
		})

		for cheapPosition := 0; cheapPosition < len(ordered); cheapPosition++ {
			cheapIndex := ordered[cheapPosition]
			if availabilityGates[cheapIndex] != 1 || !isValidAutoPriorityMultiplier(results[cheapIndex].NominalRateMultiplier) {
				continue
			}

			hasDominance := false
			peerFinalScore := 0.0
			peerPriority := int64(0)
			for expensivePosition := 0; expensivePosition < cheapPosition; expensivePosition++ {
				expensiveIndex := ordered[expensivePosition]
				if availabilityGates[expensiveIndex] != 1 ||
					!hasAutoPriorityExtremeNominalRateAdvantage(results[cheapIndex].NominalRateMultiplier, results[expensiveIndex].NominalRateMultiplier) {
					continue
				}
				hasDominance = true
				peerFinalScore = math.Max(peerFinalScore, results[expensiveIndex].FinalScore)
				peerPriority = max(peerPriority, results[expensiveIndex].ComputedPriority)
			}

			cohortCeil := inputs[cheapIndex].CohortCostCeil
			if hasAutoPriorityExtremeNominalRateAdvantage(results[cheapIndex].NominalRateMultiplier, cohortCeil) {
				hasDominance = true
				syntheticPriceScore := relativeAutoPriorityPriceScore(cohortCeil, cohortCostFloors[cohort])
				// Compare against a synthetic expensive peer with the best
				// possible cache and quality scores. Cache benefit therefore
				// cannot bypass nominal 8x dominance.
				syntheticFinalScore := weightedAutoPriorityFinalScore(1, syntheticPriceScore, 100, 100, 100, 100)
				peerFinalScore = math.Max(peerFinalScore, syntheticFinalScore)
				peerPriority = max(peerPriority, clampAutoPriorityPriority(int64(math.Round(syntheticFinalScore*10)), 0, maxPriority))
			}
			if !hasDominance {
				continue
			}

			protected[cheapIndex] = true
			targetFinalScore := math.Max(
				results[cheapIndex].FinalScore+autoPriorityDominanceScoreMargin,
				peerFinalScore+autoPriorityDominanceScoreMargin,
			)
			if targetFinalScore > 100 {
				targetFinalScore = 100
			}
			results[cheapIndex].FinalScore = targetFinalScore

			targetPriority := addAutoPriorityDominanceMargin(results[cheapIndex].ComputedPriority, maxPriority)
			targetPriority = max(targetPriority, addAutoPriorityDominanceMargin(peerPriority, maxPriority))
			targetPriority = max(targetPriority, clampAutoPriorityPriority(int64(math.Round(targetFinalScore*10)), 0, maxPriority))
			results[cheapIndex].ComputedPriority = targetPriority
			results[cheapIndex].NewPriority = targetPriority
		}
	}
	return protected
}

func hasAutoPriorityExtremeNominalRateAdvantage(cheapRate, expensiveRate float64) bool {
	if !isValidAutoPriorityMultiplier(cheapRate) || !isValidAutoPriorityMultiplier(expensiveRate) {
		return false
	}
	return expensiveRate/cheapRate >= autoPriorityExtremeCostRatio
}

func addAutoPriorityDominanceMargin(priority, maxPriority int64) int64 {
	target, ok := safeAddInt64(priority, autoPriorityDominancePriorityMargin)
	if !ok {
		return maxPriority
	}
	return clampAutoPriorityPriority(target, 0, maxPriority)
}

func removeAutoPriorityHysteresisDominanceViolations(
	results []AutoPriorityScoreResult,
	priceCohorts map[string][]int,
	availabilityGates []float64,
) {
	for _, indexes := range priceCohorts {
		for _, cheapIndex := range indexes {
			if availabilityGates[cheapIndex] != 1 {
				continue
			}
			for _, expensiveIndex := range indexes {
				if availabilityGates[expensiveIndex] != 1 ||
					!hasAutoPriorityExtremeNominalRateAdvantage(results[cheapIndex].NominalRateMultiplier, results[expensiveIndex].NominalRateMultiplier) ||
					results[cheapIndex].NewPriority > results[expensiveIndex].NewPriority ||
					results[expensiveIndex].Reason != "hysteresis_delta_below_threshold" {
					continue
				}
				results[expensiveIndex].Applied = true
				results[expensiveIndex].NewPriority = results[expensiveIndex].ComputedPriority
				results[expensiveIndex].Reason = ""
			}
		}
	}
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

// autoPriorityCacheScore maps the guarded cache cost factor to [0, 100].
// A factor at or above 1 has no measured cache benefit and scores 0; the
// existing 0.35 benefit floor scores 100. Lower factors are always better.
func autoPriorityCacheScore(cacheFactor float64) float64 {
	cacheFactor = normalizedAutoPriorityCacheFactor(cacheFactor)
	if cacheFactor == autoPriorityDefaultCacheFactor {
		return autoPriorityDefaultCacheScore
	}
	if cacheFactor >= 1 {
		return 0
	}
	if cacheFactor <= autoPriorityMinCacheCostFactor {
		return 100
	}
	return 100 * (1 - cacheFactor) / (1 - autoPriorityMinCacheCostFactor)
}

func autoPriorityGuardedCacheFactor(v float64, usageLogCount int64) (float64, float64, float64, string) {
	cacheFactor := normalizedAutoPriorityCacheFactor(v)
	if cacheFactor < autoPriorityMinCacheCostFactor {
		cacheFactor = autoPriorityMinCacheCostFactor
	}
	if usageLogCount <= 0 {
		return autoPriorityDefaultCacheFactor, autoPriorityDefaultCacheFactor, 0, "default_95"
	}
	if usageLogCount < autoPriorityFullCacheSampleCount {
		confidence := float64(usageLogCount) / float64(autoPriorityFullCacheSampleCount)
		blendedFactor := autoPriorityDefaultCacheFactor +
			(cacheFactor-autoPriorityDefaultCacheFactor)*confidence
		return blendedFactor, autoPriorityDefaultCacheFactor, confidence, "own_blend"
	}
	return cacheFactor, autoPriorityDefaultCacheFactor, 1, "own"
}

// autoPriorityAvailabilityGate returns the multiplicative factor availability
// applies to the final score: 1.0 (neutral) when availability is unknown, when
// there are too few monitor checks to trust it, or when it is at/above the
// knee; a linear ramp down to 0 below the knee. Continuous at the knee, so the
// existing 10-point apply-hysteresis absorbs small oscillations around it.
func autoPriorityAvailabilityGate(avail *float64, monitorCheckCount int64) float64 {
	if avail == nil || math.IsNaN(*avail) || math.IsInf(*avail, 0) {
		return 1
	}
	if monitorCheckCount < autoPriorityMinAvailabilityGateSamples {
		return 1
	}
	if *avail >= autoPriorityAvailabilityGateKnee {
		return 1
	}
	if *avail <= 0 {
		return 0
	}
	return *avail / autoPriorityAvailabilityGateKnee
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

func hasSampledThroughput(sampleCount int64, tps float64) bool {
	return sampleCount > 0 && tps > 0 && !math.IsNaN(tps) && !math.IsInf(tps, 0)
}

// absoluteAutoPriorityAscendingScore maps value to [0, 100] on a log-scaled ASCENDING
// curve between fixed anchors (higher value = higher score): value <= slowAnchor -> 0,
// value >= fastAnchor -> 100. Used for throughput (tokens/sec), where more is better.
// Absolute (not cohort-relative) so a lone channel is judged on its real speed.
func absoluteAutoPriorityAscendingScore(value, slowAnchor, fastAnchor float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	if fastAnchor <= slowAnchor {
		return 100
	}
	if value <= slowAnchor {
		return 0
	}
	if value >= fastAnchor {
		return 100
	}

	logDenominator := math.Log1p(fastAnchor - slowAnchor)
	if logDenominator <= 0 || math.IsNaN(logDenominator) || math.IsInf(logDenominator, 0) {
		return 100
	}

	score := 100 * (math.Log1p(value-slowAnchor) / logDenominator)
	if score < 0 {
		return 0
	}
	if score > 100 {
		return 100
	}
	return score
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
