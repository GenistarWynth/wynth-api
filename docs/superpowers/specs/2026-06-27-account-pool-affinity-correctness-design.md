# Design: Account-Pool Session-Affinity Correctness (Slice 2)

- **Date:** 2026-06-27
- **Branch:** `sub2api-account-pool`
- **Status:** Approved (autonomous; user waived per-slice approval)
- **Scope:** OpenAI/Codex account pools (session affinity for `previous_response_id` / conversation / session-header keyed requests)

## 1. Background

Wynth pins a session (keyed by `previous_response_id`, conversation id, or session headers — see
`service/account_pool_affinity.go` `BuildAccountPoolRuntimeAffinityKey`) to the account that last
served it, so stateful Codex/Responses conversations keep hitting the account that holds their
server-side state. A prior audit found two correctness defects:

1. **Pin migrates on transient unavailability.** `selectAccountPoolAffinityCandidate`
   (`service/account_pool_scheduler.go`) calls `forgetAccountPoolRuntimeAffinity(key)` whenever the
   pinned account is absent from the *current filtered candidate list*. That list excludes accounts
   that are merely **attempted-this-request** (a transient retry artifact) or that fail a concurrent
   **lease/capacity** acquisition. So a momentary capacity blip permanently forgets the pin, and the
   next request re-pins to a *different* account — which for a `previous_response_id` request does
   not hold the conversation state and returns an upstream error.
2. **TTL is purely sliding.** `remember()` rewrites `expiresAt = now + 30m` on every success with no
   absolute cap, so a session sending requests more often than every 30m is pinned **forever** —
   admin rebalancing / newly-added accounts never take effect on active sessions.

sub2api's principle (reference): sticky sessions are evicted only on a **real failure** of the
pinned account, not on transient scheduling pressure.

## 2. Design

### 2.A Do not forget the pin on transient non-candidacy
In `selectAccountPoolAffinityCandidate` (`service/account_pool_scheduler.go`): if the pinned account
is not in the current `candidates`, **fall through to normal selection for this request but do NOT
call `forgetAccountPoolRuntimeAffinity`**. Remove that call.

Rationale & safety:
- The relay layer already forgets the pin on a genuine attempt failure
  (`ForgetSelectedAccountPoolRuntimeAffinity` at `relay/account_pool_runtime.go` on the failure
  paths), and on a pool-wide selection failure. That is the correct eviction trigger.
- A transient blip (account attempted-this-request, or at-capacity in the lease loop) now serves the
  request from a fallback account **without** dropping the pin, so the next request re-pins to the
  original account once it is free.
- A stale pin to a genuinely dead account is harmless: the dead account is never in `candidates`
  (fails `IsSchedulableAt`), so the pin is never honored — every request falls through to normal
  selection — and the entry TTL-expires. The relay already cleared it on the failure that killed it.
- No infinite loop in the lease retry loop: an at-capacity affinity account is added to the
  per-request attempted set, so the next `SelectAccountPoolAccount` excludes it from `candidates`,
  the affinity lookup finds nothing, and selection falls through to a different account.

### 2.B Hard TTL cap (bounded sliding window)
In `service/account_pool_affinity.go`:
- Add `createdAt int64` to `accountPoolRuntimeAffinityEntry`.
- `remember()`: when an entry for the key already exists, **preserve its `createdAt`**; only set
  `createdAt = now` for a brand-new entry. Keep refreshing `expiresAt = now + TTL` (sliding idle
  timeout) as today.
- `lookup()`: in addition to the existing `expiresAt <= now` check, reject (and delete) the entry
  when `now >= createdAt + hardCapSeconds`.
- `const accountPoolRuntimeAffinityHardCapSeconds = int64(4 * 60 * 60)` (4h). After the hard cap, a
  long-running session rebalances through normal scheduling (and can form a fresh pin).

### 2.C Affinity intentionally overrides priority (no change)
The audit also noted affinity overrides account priority. This is **by design**: a stateful session
must return to the same account regardless of priority, or its server-side state is lost. We keep
this behavior and document it; we do not restrict affinity to highest-priority candidates.

### Deferred
- The affinity-1 stale-DB-read micro-race (a ms-wide window where a just-failed account is briefly
  re-selectable before its cooldown write propagates) — bounded, self-healing; an in-process
  recently-failed fast-path block is a future refinement.

## 3. Files
- `service/account_pool_scheduler.go` — remove the transient forget in `selectAccountPoolAffinityCandidate`.
- `service/account_pool_affinity.go` — `createdAt` + hard-cap in entry/remember/lookup.
- `service/account_pool_scheduler_test.go`, `service/account_pool_affinity_test.go` (or existing affinity test file) — tests.

## 4. Testing (testify, table-driven, Rule 9)
- Pin is **retained** when the pinned account is absent from `candidates` for a transient reason
  (e.g. present in `AttemptedAccountIDs`): selection returns a different account, and a subsequent
  lookup for the same key still resolves to the original account.
- Pin is honored again once the account is back in `candidates`.
- Hard-cap: an entry whose `createdAt` is older than 4h is not returned by `lookup` even if
  `expiresAt` was just refreshed; a fresh entry within the cap is returned.
- `createdAt` is preserved across `remember()` refreshes (sliding expiry, fixed birth).
- Existing `remember`/`lookup`/`forget`/digest-key behavior unchanged.

## 5. Risks
- Removing the scheduler-level forget shifts all eviction to the relay failure path + TTL — verify
  the relay forget genuinely fires on the pinned account's failure (it does: key is set in context
  on successful selection, read on the failure path). Covered by existing relay tests + a new test.
