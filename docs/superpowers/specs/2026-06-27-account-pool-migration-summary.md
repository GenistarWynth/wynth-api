# sub2api Account-Pool Logic → Wynth: Migration Summary

- **Date:** 2026-06-27
- **Branch:** `sub2api-account-pool` (base `2127d143`, 33 commits)
- **Scope:** Wynth's OpenAI/Codex account pool (the only platform the pool supports). sub2api used as a behavioral **reference only** — re-implemented in Wynth's structure (Gin/GORM/3-DB, `common.*` JSON), never copied.
- **Status:** Complete & verified. Full backend suite (`model service controller relay types`) green; account-pool tests flake-free under `-count=3` and `-race`; gofmt/vet clean.

## What was migrated (by slice)

| Slice | Capability | Key commits |
|---|---|---|
| 1 | **Failure handling & retry**: upstream-driven 429 cooldowns (`x-codex-*` headers / OpenAI body `resets_at`, else configurable fallback); 408/5xx/529/transport/400-phrase handling; escalation state machine (403 three-strike, OAuth 401 two-strike, 5xx tiering + hard-cap) in a `failure_state` JSON column; monotonic cooldowns; selection robustness (skip corrupt row); pool-mode same-account retries | `32bd3e40`…`0b5c92af` |
| 2 | **Session-affinity correctness**: stop migrating a stateful session's pin on transient unavailability (eviction owned by relay-failure path + TTL); hard 4h TTL cap | `d4ff3f7b`, `4fd6b895` |
| 3-4 | **Import robustness**: clearer CPA YAML errors; accurate dry-run proxy counts; dedup decrypt-failure logging; orphan-proxy cleanup on partial failure | `4dbf9eb5`, `7f15b775`, `8c9e8860` |
| 5 | **In-process fast-path block**: just-failed account excluded immediately, bridging the DB-cooldown propagation window | `4c343554`, `3c27112c` |
| 6 | **Single-query lease loop**: load candidates once; iterate the lease loop in-memory; decrypt/proxy only for the winner (was O(N) DB queries + decrypts under saturation) | `26437a10` |
| 7 | **Per-user concurrency limit**: per-binding `MaxUserConcurrency` (0=off); in-memory per-(binding,user) slot once per request; config cached via the codebase's HybridCache pattern (invalidated on binding mutations) | `4bdad22c`, `8ea85223` |
| 8 | **Account expiry auto-pause**: `ExpiresAt` + `AutoPauseOnExpired`; expired accounts excluded via `IsSchedulableAt` | `1ba29018` |
| 9 | **Per-account request quota**: `RequestQuota` + rolling window; excluded when exceeded; incremented on success only when configured (default-off = no extra DB work) | `c9a1fe5a` |
| 10 | **Admin UI**: new fields surfaced in the account/binding edit drawers + i18n | `38c31942` |
| — | Flaky-test fix; admin re-enable clears escalation state; block-only-on-real-cooldown; LastStatus diagnostic | `3f7db4d2`, `d9209e12` |

## Audit remediation
All **13 findings** from the prior scheduler audit are remediated: failure escalation (was: `failure_count` written, never read), 408/non-auth-4xx cooldowns, retry budget, selection robustness, affinity migrate-on-transient + sliding-TTL, import YAML/dry-run/dedup/orphan-proxies, the affinity/lease stale-read race, and the O(N)-query lease loop.

## New schema (3-DB-safe; idempotent `ALTER ADD COLUMN` + AutoMigrate; TEXT for JSON, BIGINT for timestamps)
- `account_pool_accounts`: `overload_until`, `failure_state` (JSON), `runtime_options` (JSON), `expires_at`, `auto_pause_on_expired`, `request_quota`, `request_quota_used`, `request_quota_window_start`, `request_quota_window_seconds`.
- `account_pool_channel_bindings`: `max_user_concurrency`.
- `types.NewAPIError`: `UpstreamHeader` / `UpstreamBody` / `UpstreamStatusCode` (populated in `RelayErrorHandler`).
- Configurable defaults (sub2api-matching) in `service/account_pool_failure_settings.go`.

## Deferred / skipped (with rationale)
- **Reverse-proxy gateway — SKIP.** Overlaps Wynth's native relay; porting wholesale would fight the architecture (per handoff "reference only, don't pollute structure").
- **Other-platform pool logic (Anthropic 5h/7d windows, Gemini/Antigravity smart-retry, etc.) — N/A.** Wynth's pool is OpenAI/Codex-only; these need new platform support, which is a separate effort, not pool-logic migration.
- **Proxy active health-probe — deferred.** Heavy background infra; proxy fallback chains already give reactive dead-proxy resilience.
- **Per-account quota over tokens / per-model rate-limits — deferred.** Request-count quota covers the core subscription-rationing need; per-model `model_rate_limits` (sub2api uses raw `jsonb_set`) would need a cross-DB design.
- **Redis-backed scheduler snapshot (HA) — deferred.** All in-memory managers (lease/recency/block/user-concurrency/affinity) are process-local → under horizontal scaling, concurrency limits and affinity are per-process (best-effort, perf-only; durable state is DB-backed). Documented architectural limitation.
- **Precise OAuth-401 refresh-race guard — deferred.** Two-strike + success-reset provides the race tolerance; precise token-version threading is a future refinement.
- **Frontend translations for the 6 new labels — follow-up.** English fallbacks are in place across all locales (i18n falls back to English).

## How this was built
Test-driven, slice by slice: a Claude implementer subagent per task, an independent adversarial review per task (Codex/GPT-5.x where its runtime was healthy, Claude/Opus otherwise — the riskiest reviews on Opus), fixes verified before merge, parallel git-worktrees for independent slices, and a final whole-branch Opus review for cross-cutting issues. Cross-model review caught real bugs single-model self-review missed (pool-mode credential-restore, 429 exhausted-window edge, body-read context gap, and the admin re-enable wedge).
