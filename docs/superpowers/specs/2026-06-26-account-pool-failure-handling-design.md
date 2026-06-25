# Design: Account-Pool Failure Handling & Retry (Slice 1)

- **Date:** 2026-06-26
- **Branch:** `sub2api-account-pool`
- **Status:** Approved design — ready for implementation planning
- **Scope:** OpenAI/Codex account pools only (the only platform Wynth's pool currently supports)

## 1. Background & Motivation

Wynth's account pool is a from-scratch reimplementation inspired by sub2api. A prior
adversarial audit of Wynth's current scheduler confirmed 13 issues, concentrated in
**failure handling**: `failure_count` is incremented but never read (no escalation);
HTTP 408 and non-auth 4xx get no cooldown; cooldowns are a flat 60s with no upstream-reset
awareness; OAuth accounts are expired on the first (often transient) 401.

sub2api (reference only, at `.codex/external/sub2api-src`) implements a far richer model:
header/body-driven 429 cooldowns (default 5s, cap 7200s), OpenAI 403 escalation
(3 strikes in 180 min → disable), OAuth 401 two-strike with refresh-race protection,
529 overload as a distinct cooldown, and transport-error classification.

This slice ports those **behaviors** (not code — sub2api is ent/Vue/Postgres-only; Wynth is
Gin/GORM and must support SQLite/MySQL/PostgreSQL) into Wynth's existing structure.

**Goal:** bring Wynth's OpenAI/Codex account-pool failure handling to sub2api parity, plus a
generic escalation framework future platform slices can reuse.

**Non-goals (deferred to later slices):** Anthropic 5h/7d windows; Gemini/Antigravity
smart-retry & MODEL_CAPACITY loops; per-model rate-limits (`model_rate_limits` JSONB —
also a Rule 2 cross-DB hazard); Redis scheduler snapshot/outbox; per-account quota gate;
expiry sweep; user-configurable keyword temp-unschedulable rules.

## 2. Current State (Wynth, `sub2api-account-pool` branch)

- `service/account_pool_failure.go` — `accountPoolFailureUpdate`: 401/403 → `status=expired`;
  429 → `rate_limited_until=now+60`; 5xx/network/out-of-range → `temp_disabled_until=now+60`;
  all other codes → only `failure_count++`. No escalation. Fixed 60s.
- `model/account_pool.go:137-148` — `IsSchedulableAt`: `status=='enabled' && rate_limited_until<=now && temp_disabled_until<=now`.
- `relay/account_pool_runtime.go` — `runAccountPoolRuntimeAttempts` retry loop (bounded by
  `binding.AccountRetryTimes`); records failure at :71 and :95; per-request attempted-account set;
  streaming guard via `info.HasSendResponse()` (:172).
- `service/account_pool_success.go` — `RecordAccountPoolRuntimeAttemptSuccess` clears
  `rate_limited_until`/`temp_disabled_until`/`temp_disabled_reason`/`last_error`.
- `service/account_pool_token_provider.go` — singleflight OAuth refresh with `token_state`
  versioning (`NextVersion`, optimistic `WHERE token_state=old` update);
  `markAccountPoolRuntimeTokenRefreshFailure` sets `temp_disabled_until=now+60`.
- `types/error.go:90` — `NewAPIError{Err, RelayError, StatusCode, Metadata, ...}` — **no headers**.
- `service/error.go:86-131` — `RelayErrorHandler(ctx, resp, showBody)` reads `resp.Header` and
  body but discards both; populates only `StatusCode`/`Err`/`RelayError`.
- `relay/compatible_handler.go:205-220` (text) & `relay/responses_handler.go:133-148` (responses/codex)
  call `RelayErrorHandler` for non-2xx; both wrapped by `runAccountPoolRuntimeAttempts`.
- `model/main.go:465-473` — idempotent `ALTER TABLE ... ADD COLUMN` pattern (existence-checked, 3-DB-safe).

## 3. Design

### 3.A Data model (extend existing columns)

New columns on `account_pool_accounts`, added via the `model/main.go` idempotent
`ALTER TABLE ADD COLUMN` pattern (TEXT for JSON, BIGINT for timestamps — no JSONB, Rule 2):

| Column | Type | Purpose |
|---|---|---|
| `overload_until` | BIGINT default 0 | 529 cooldown; participates in `IsSchedulableAt` |
| `failure_state` | TEXT | JSON escalation bookkeeping, read/written only on failure/success paths |
| `runtime_options` | TEXT | JSON per-account behavioral config (pool-mode) |

`failure_state` JSON shape (marshaled via `common.*`):
```json
{ "consecutiveFailures": 0, "lastStatus": 0, "http403Count": 0,
  "http403WindowStart": 0, "last401At": 0 }
```
`runtime_options` JSON shape:
```json
{ "poolMode": false, "poolModeRetryCount": 0, "poolModeRetryStatusCodes": [] }
```

Reuse existing: `rate_limited_until`, `temp_disabled_until`, `temp_disabled_reason`,
`failure_count` (kept as audit), `last_failure_at`, `status`.

`IsSchedulableAt(now)` adds one clause:
```
status=='enabled' && rate_limited_until<=now && temp_disabled_until<=now && overload_until<=now
```
The hot scheduling path stays column-based; `failure_state`/`runtime_options` JSON are only
parsed on the failure/success/retry paths, never in `IsSchedulableAt`.

### 3.B Upstream-context plumbing

Add optional fields to `types.NewAPIError`:
```go
UpstreamHeader     http.Header // reference, not copied
UpstreamBody       []byte      // already read in RelayErrorHandler
UpstreamStatusCode int         // pre status_code_mapping
```
Populate **once** in `service.RelayErrorHandler` (the single site where `resp` and the body are
in scope). The same `*NewAPIError` already flows from there through the `attempt` closure and
`runAccountPoolRuntimeAttempts` into `RecordAccountPoolRuntimeAttemptFailure` (relay:95), so no
new context threading is required. Non-pool callers ignore the new fields.

Capture `UpstreamStatusCode` **before** `service.ResetStatusCode(...)` (status_code_mapping) so
classification uses the true upstream status.

*Rejected alternative:* stash on `gin.Context` at the error site — more coupling, and the error
object already carries the data to the right place.

### 3.C Failure classification → cooldown

`RecordAccountPoolRuntimeAttemptFailure` becomes load → classify → write, in a short GORM
transaction (read current `failure_state` + credential type, compute, persist). All magnitudes
are configurable (§3.F) with the sub2api-matching defaults below. `platform == openai` only.

| Upstream signal | Action | Default |
|---|---|---|
| 429 + `x-codex-*` reset header OR body `resets_at`/`resets_in_seconds` | `rate_limited_until` = exact reset | from upstream |
| 429, no parseable reset | `rate_limited_until` = now + fallback | 5s (cap 7200s; can disable) |
| 403, OpenAI | `http403Count++` in rolling window; `< threshold` → `temp_disabled_until=now+cooldown`; `>= threshold` → `status=expired` | 3 / 180m / 10m |
| 401, OAuth account | 1st → `temp_disabled_until=now+cooldown`, keep `enabled`, set `last401At`; 2nd within window → `status=expired`; refresh-race guard (§3.D) | 10m |
| 401/403, API-key account | `status=expired` (unchanged) | permanent |
| 400 with body phrase: organization disabled / credit balance / identity verification | `status=expired` | permanent |
| 408 | short `temp_disabled_until` (fixes audit cooldown-1) | 60s |
| other 4xx (402/404/409/410/422) | `failure_count++` only, no cooldown (correct: client errors, not account death; already not retried) | — |
| 5xx | `temp_disabled_until` with consecutive escalation (§3.D) | 60s base |
| 529 | `overload_until` = now + cooldown | 10m |
| network error (`ErrorCodeDoRequestFailed`) | `classifyTransportError`: persistent → long; transient → short | 10m / 60s |

`classifyTransportError` inspects the (masked) error string for persistent markers:
`connection refused`, `no route to host`, `network is unreachable`, DNS `no such host`,
`proxy authentication required`/`authentication failed`. Match → persistent (10m). Else transient (60s).

429 reset parsing: prefer `x-codex-*` reset headers; else parse OpenAI error body
(`type: usage_limit_reached|rate_limit_exceeded` with `resets_at` unix or `resets_in_seconds`);
else configurable fallback.

Monotonic cooldown: on the **failure path**, when writing a cooldown column, keep the **later** of
the existing value and the new one (`new = max(existing, computed)`), so a longer concurrent
cooldown is not shortened. This rule does **not** apply to the success path, which clears cooldowns
unconditionally (§3.D).

### 3.D Escalation state machine

- **Generic consecutive-failure tiering:** `failure_state.consecutiveFailures` increments **only**
  on 5xx and persistent-transport failures (the "soft infra" class), resets to 0 on success. It
  tiers that class's cooldown (default 60s → 5m → 30m by count); at the hard cap (default count
  `>= 6`) → `status=expired`. Tiers + cap configurable. 429/403/401 do **not** increment it — they
  use their own dedicated counters (`http403Count`, `last401At`) so a rate-limit cannot inflate the
  infra-failure tier and vice-versa. Any success resets all counters.
- **403 3-strike:** `http403Count` within a rolling `http403WindowStart`-based 180m window;
  out-of-window resets the count to 1; reaching threshold → `status=expired`.
- **OAuth 401 two-strike + refresh-race guard:** first 401 → temp cooldown, keep `enabled`,
  record `last401At`; a second 401 within the re-strike window (default 30m of `last401At`) →
  `status=expired`. Refresh-race guard:
  before escalating, re-check the account's `token_state` — if its version/`ExpiresAt` advanced
  after this request's selection `now` (a concurrent singleflight refresh already produced a fresh
  token), treat the 401 as stale and do **not** escalate; let the retry loop use the new token.
- **Reset on success:** extend `RecordAccountPoolRuntimeAttemptSuccess` to clear `overload_until`,
  reset `failure_state` counters, and clear `temp_disabled_reason` (cascading clear).
- **Concurrency:** read-modify-write under a GORM transaction; counters are advisory, so minor
  undercount under concurrent failures is acceptable and consistent with the pool's existing
  optimistic design.

### 3.E Retry loop & pool-mode (`relay/account_pool_runtime.go`)

- **Streaming guard:** already present via `info.HasSendResponse()` (:172). Keep; add an explicit
  regression test that no same-account retry happens after the first byte is sent.
- **Selection robustness (audit retry-1):** a per-account parse/decrypt error inside
  `SelectAccountPoolAccount` (e.g. malformed `supported_models` JSON, decrypt failure) must skip
  and log that one account rather than abort selection for the whole binding; selection-path
  transient errors become retriable instead of returning `selectedAccountID==0` and skipping retry.
- **Pool-mode:** when `runtime_options.poolMode` is set, for a status in
  `poolModeRetryStatusCodes`, skip all failure-state marking and allow up to `poolModeRetryCount`
  **same-account** retries (do not add to the attempted set) before falling through to the next
  account. Per-request; off by default.

### 3.F Configuration (`setting/`)

New admin-configurable settings, with sub2api-matching defaults so behavior is correct out of the box:

| Setting | Default |
|---|---|
| 429 fallback seconds / cap / enabled | 5 / 7200 / true |
| 403 threshold / window minutes / cooldown minutes | 3 / 180 / 10 |
| OAuth 401 cooldown minutes / re-strike window minutes | 10 / 30 |
| 529 overload cooldown minutes | 10 |
| transport persistent / transient cooldown | 10m / 60s |
| 5xx/transport escalation tiers (by `consecutiveFailures`) / hard cap | 60s, 5m, 30m / expire at `>= 6` |

### 3.G Testing (testify; table-driven; Rule 9)

- Classifier table tests: each status code, with/without `x-codex-*` headers and with/without
  parseable body reset, asserting exact `rate_limited_until`/`temp_disabled_until`/`overload_until`/`status`.
- Escalation transitions: 403 1→2→3 strikes + window-expiry reset; OAuth 401 1st→2nd; refresh-race
  no-escalate; 5xx tiering and hard-cap → expire.
- Success resets all counters + cascading clear.
- `IsSchedulableAt` with `overload_until`.
- Migration idempotency (column-exists skip) verified on SQLite/MySQL/PostgreSQL.
- Streaming guard: no same-account retry after first byte.
- Pool-mode: N same-account retries, no failure marking.
- Selection robustness: one corrupt account row does not poison the binding.

Tests must assert real, user-visible contracts (no coverage-only/fuzz/timing tests); initialize DB,
context, settings, and account state explicitly per fixture.

### 3.H Files touched (estimate)

- `model/account_pool.go` — new columns, `IsSchedulableAt` clause, `failure_state`/`runtime_options` accessors.
- `model/main.go` — migration for the 3 new columns.
- `service/account_pool_failure.go` — classifier, escalation, transport classification, monotonic cooldown.
- `service/account_pool_success.go` — counter reset + cascading clear.
- `service/account_pool_token_provider.go` — refresh-race signal used by 401 escalation.
- `types/error.go` + `service/error.go` — upstream header/body/status capture.
- `relay/compatible_handler.go`, `relay/responses_handler.go` — pass pre-mapping status into capture.
- `relay/account_pool_runtime.go` — selection-robustness + pool-mode retry behavior.
- `setting/` — new config keys.
- Tests alongside each.

## 4. Risks

- Touching shared `NewAPIError`/`RelayErrorHandler` (additive; verify non-pool relay paths unaffected).
- `status_code_mapping` could remap the upstream status before classification — mitigated by
  capturing the pre-mapping status in upstream-context.
- Read-modify-write counter races (advisory, accepted).
- Pool-mode bypasses safety marking — off by default, documented.

## 5. Open Questions

None blocking. Defaults follow sub2api; everything is configurable.
