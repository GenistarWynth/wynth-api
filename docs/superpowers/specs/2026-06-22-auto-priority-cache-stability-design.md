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
   - no usage logs: use the conservative mature same-cohort peer prior, or neutral `1.0` when no trustworthy peer exists;
   - 1 to 19 usage logs: continuously blend the peer/neutral prior toward the channel's own measured factor;
   - 20 or more usage logs: trust the measured cache factor.

2. Cache benefit floor:
   - cache-adjusted cost factor cannot go below `0.35`;
   - this prevents short bursts of extreme cache hits from making a channel appear nearly free.

3. Historical smoothing and scoring:
   - when a previous snapshot has a valid cache factor, smooth the cache factor independently of nominal rate:

```text
smoothed_cache_factor =
  0.65 * current_cache_factor
  + 0.35 * previous_cache_factor

effective_cost_diagnostic =
  nominal_rate * smoothed_cache_factor
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

Snapshot v3 stores the nominal rate and nominal price score separately from the guarded cache factor and cache score. The legacy `effective_rate_multiplier`, `effective_price_score`, and `effective_cost_multiplier` fields remain populated for compatibility; effective price aliases nominal price, while effective cost is diagnostic.
