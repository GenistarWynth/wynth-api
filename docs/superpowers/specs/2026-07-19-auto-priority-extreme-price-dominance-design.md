# Auto-Priority Extreme Price Dominance Design

## Goal

Keep auto-priority multi-factor scoring meaningful for channels whose nominal rates are close, while guaranteeing that a currently usable channel with an extreme nominal-price advantage ranks above an at-least-8x-more-expensive peer in the same local-group and channel-type cohort.

## Approved Approach: Plan A

Plan A keeps the availability gate, shared cohort bounds, and persistence flow. Price and cache are separate score components: nominal source/channel rates define price, while guarded cache behavior contributes independently. A post-score dominance correction enforces the extreme nominal-price invariant.

The change is limited to automatic-priority scoring and cohort-bound regression coverage. It does not change request-time channel selection, channel weight, retry semantics, monitoring, or billing.

## Normal Multi-Factor Scoring

Effective cost remains an explanatory diagnostic built after guarded cache-factor confidence and previous-factor smoothing:

```text
effective_cost = effective_rate_multiplier * guarded_cache_factor
```

It does not drive price score or dominance. Within each `local_group + channel_type` cohort, the scorer finds the minimum nominal rate and widens it with valid `CohortCostFloor` values, whose legacy names now carry nominal-rate bounds. Price score is cache-independent:

```text
nominal_price_score = 100 * cohort_nominal_rate_floor / nominal_rate
```

The result is clamped to `0..100`. `effective_price_score` remains as a backward-compatible alias. A degenerate single-member cohort without external bounds retains the legacy score of `100`.

The guarded cache factor maps monotonically to a separate score: factor `1.0` or worse scores `0`, the existing `0.35` floor scores `100`, and intermediate values are linearly interpolated. A zero-sample cold start uses the fixed inverse-mapped factor `0.3825` for an exact score of `95`; same-cohort peer medians have no scoring effect. Counts 1–19 blend toward the channel's own factor using `usage_log_count / 20` confidence, and count 20 uses own data completely.

This preserves the size of the nominal price gap instead of stretching every observed minimum and maximum to `100` and `0`. For example, 0.04 and 0.05 score 100 and 80, so cache, availability, first-token latency, and throughput can make the slightly more expensive but healthier channel win. The weighted score is:

```text
final_score = availability_gate * (
    0.75 * nominal_price_score
  + 0.10 * cache_score
  + 0.08 * availability_score
  + 0.03 * first_token_score
  + 0.04 * throughput_score
)
```

## Extreme-Cost Dominance

After normal scoring and before hysteresis, the scorer performs dominance correction inside each cohort.

A real candidate participates only when:

- its nominal source/channel rate is valid;
- its existing availability gate is exactly `1`;
- the compared peer is in the same cohort and is also gate-usable.

Unknown availability and too-few monitor samples remain usable because the existing gate returns `1` for both. A channel whose gate is below `1` with enough samples does not impose or receive extreme-cost dominance.

For each usable pair, the cheaper candidate dominates when:

```text
expensive_nominal_rate / cheap_nominal_rate >= 8
```

Cache factor never participates in this comparison. The correction raises the cheaper candidate as needed so it has at least a 1-point `FinalScore` margin and a 10-point `ComputedPriority` margin over the expensive candidate. Corrections are processed from higher nominal rate toward lower nominal rate so multiple extreme relationships remain consistent.

## Split-Worker Runs

An upstream-source worker can score only one member of a larger cohort. When a usable candidate's valid nominal-rate `CohortCostCeil` is at least 8x its nominal rate, the ceiling acts as a synthetic expensive peer.

The synthetic peer uses the normal nominal price score at the ceiling and the maximum possible cache and quality scores. The cheap candidate is raised as needed above that conservative peer by the same score and priority margins. This gives a 0.001 candidate the dominance correction even when a 0.05 peer is outside the current worker batch.

`autoPriorityLocalGroupCostBounds` continues to aggregate enabled manual and upstream-generated channels. Generated channels use their mapping's effective rate multiplier and are not excluded merely because they are generated.

## Hysteresis

The existing 10-priority-point hysteresis remains for ordinary score movement.

A channel whose score or priority was raised by extreme dominance skips hysteresis. After tentative hysteresis is calculated for the remaining candidates, the scorer checks every real extreme pair again. If retained old priorities would leave the usable extreme-cheap channel at or below the extreme-expensive channel, hysteresis is removed for the affected pair so the dominance order is applied.

`ComputedPriority` always reflects the corrected computed result. `NewPriority` reflects the priority that persistence should apply.

## Failure and Boundary Behavior

- Invalid nominal multipliers keep the existing `missing_effective_rate_multiplier` no-op behavior.
- Unusable cheap channels receive no extreme dominance and can rank below healthy expensive channels.
- Dominance comparisons never cross local-group or channel-type cohort boundaries.
- Final score remains bounded to `0..100`; computed and new priorities remain bounded by `maxPriority`.
- With the production `maxPriority` of 1000, the 1-score/10-priority margins are representable. Boundary handling may lower the expensive side if a cap would otherwise prevent strict ordering.

## Regression Coverage

Tests cover:

1. 0.04 with worse but gate-usable metrics versus healthy 0.05, where 0.05 can win through normal multi-factor scoring.
2. Usable 0.001 with worst non-price metrics versus usable 0.05 with best metrics, where 0.001 has strictly higher final score and computed priority.
3. Unusable 0.001 versus healthy 0.05, where no dominance is forced and 0.001 can lose.
4. Previous priorities arranged against the new order, where hysteresis cannot retain 0.05 above usable 0.001.
5. A single-member 0.001 worker run with cohort ceiling 0.05, where external bounds trigger dominance correction.
6. Group cost bounds containing both a manual auto-priority channel and an upstream-generated channel.
