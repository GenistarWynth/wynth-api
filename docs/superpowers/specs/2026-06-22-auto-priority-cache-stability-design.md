# Auto Priority Cache Stability Design

## Goal

Automatic priority scoring should react to real cache economics without letting a small or unusually cache-heavy window distort nominal price. Cache behavior is dynamic, so the score must be useful when there is enough data and conservative when there is not.

## Scope

Cache is an independent component of automatic priority scoring. It never changes the nominal price floor, nominal price score, or 8x dominance comparison. The final-score weights are:

- nominal price score: 75%
- cache score: 10%
- availability score: 8%
- first-token latency score: 3%
- throughput score: 4%

## Rules

1. Sample confidence:
   - no usage logs: use an exact cache score of `95`, which contributes `95 * 10% = 9.5` points before the availability gate;
   - derive the corresponding factor by inverting the existing cache-score mapping, not by using `0.95`: `1 - 0.95 * (1 - 0.35) = 0.3825`;
   - trusted same-cohort peer medians do not change this zero-sample default;
   - 1 to 19 usage logs: blend from `0.3825` toward the channel's own bounded factor with confidence `usage_log_count / 20`;
   - 20 or more usage logs: use the channel's own bounded factor completely.

2. Cache benefit floor:
   - cache-adjusted cost factor cannot go below `0.35`;
   - this prevents short bursts of extreme cache hits from making a channel appear nearly free.

3. Historical diagnostics and scoring:
   - the cache factor and cache score always follow the exact current-window confidence transition above; a previous snapshot never alters them;
   - when a previous snapshot has a valid cache factor, smooth only the backward-compatible effective-cost diagnostic:

```text
smoothed_cache_factor_for_diagnostics =
  0.65 * current_cache_factor
  + 0.35 * previous_cache_factor

effective_cost_diagnostic =
  nominal_rate * smoothed_cache_factor_for_diagnostics
```

   - snapshots that predate cache-factor diagnostics retain the former effective-cost smoothing as a compatibility fallback;
   - the resulting guarded cache factor maps monotonically to `cache_score`: factor `1.0` or worse is `0`, factor `0.35` is `100`, and intermediate factors are linearly interpolated;
   - nominal rate changes cannot manufacture cache benefit; effective cost remains diagnostic and never affects nominal price or hard dominance.

4. Existing safeguards remain:
   - missing or invalid upstream effective rate skips priority updates;
   - same-cohort relative nominal price scoring stays cache-independent;
   - 8x dominance compares nominal source/channel rates only;
   - hysteresis still suppresses priority changes smaller than 10 points after the first snapshot.

## Stored Snapshot

Snapshot v3 stores the nominal rate and nominal price score separately from the guarded cache factor and cache score. It records `default_95`, `own_blend`, or `own` as the cache-factor source, `0.3825` as the prior factor, and the own-sample confidence (including zero). The legacy `effective_rate_multiplier`, `effective_price_score`, and `effective_cost_multiplier` fields remain populated for compatibility; effective price aliases nominal price, while effective cost is diagnostic.
