# sub2api Account Pool Phase 2A Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add the account-pool runtime selection foundation without routing live relay traffic through account pools yet.

**Architecture:** Introduce an explicit runtime-enabled binding state and a service-level scheduler that can select one account for an enabled account-pool channel binding. The scheduler is pure backend logic for now: it reads account-pool binding/account configuration, filters unsafe candidates, applies strict priority before weighted random, and returns credential/model metadata needed by the future relay hook.

**Tech Stack:** Go 1.22+, GORM v2, testify, project `common` JSON helpers, SQLite-compatible tests.

---

## Scope

This plan implements Phase 2A as a narrow scheduler foundation.

It intentionally does not:

- enable admin-created account-pool bindings for live traffic;
- change relay handlers;
- inject account credentials into upstream requests;
- refresh OAuth tokens;
- implement ChatGPT reverse-proxy protocol behavior;
- write per-request metrics.

The success condition is: backend tests can prove which account would be selected for an enabled binding, while draft/disabled bindings remain non-runtime.

## Runtime Contract

Add `AccountPoolBindingStatusEnabled` as a runtime state. Phase 1 admin APIs still reject creating enabled bindings until a later relay integration task adds a deliberate activation flow.

Scheduler behavior:

- binding must be `enabled`;
- pool must be enabled and not deleted;
- accounts must be enabled and schedulable at the supplied timestamp;
- account filter `account_ids` limits candidates when present;
- binding model policy `fixed_models` limits accepted request/upstream models when strategy is `fixed`;
- account `supported_models` limits candidate accounts when present;
- account `model_mapping` maps the channel upstream model to the final account upstream model;
- attempted account IDs are excluded for the current request;
- highest account priority is exhausted before lower priorities;
- within the selected priority tier, weight is randomized with `weight + 10`, matching the existing DB-channel behavior;
- if no candidate remains, return a typed exhaustion error.

## Files

- Modify `model/account_pool.go`: add enabled binding constant and runtime binding lookup helpers.
- Create `service/account_pool_scheduler.go`: scheduler request/result types and selection logic.
- Create `service/account_pool_scheduler_test.go`: TDD tests for status, filters, model policy, mapping, strict priority, attempted exclusion, and exhaustion.
- Modify `service/account_pool_service.go`: keep Phase 1 API validation rejecting enabled bindings until activation exists.

---

### Task 1: Runtime Binding State

**Files:**
- Modify: `model/account_pool.go`
- Test: `service/account_pool_scheduler_test.go`

- [ ] **Step 1: Write failing tests**

Create `service/account_pool_scheduler_test.go` with setup helpers and a test that creates an enabled binding directly in the database, then asserts the scheduler can find candidates only through that enabled binding.

The first test must fail because `AccountPoolBindingStatusEnabled` and `SelectAccountPoolAccount` do not exist.

- [ ] **Step 2: Add runtime binding status**

Add:

```go
AccountPoolBindingStatusEnabled = "enabled"
```

Keep `validateAccountPoolBindingStatus` unchanged so admin creation still only accepts draft/disabled.

- [ ] **Step 3: Verify focused failure moves to scheduler**

Run:

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./service -run "TestAccountPoolScheduler" -count=1
```

Expected: FAIL because scheduler implementation is missing.

### Task 2: Account Selection Scheduler

**Files:**
- Create: `service/account_pool_scheduler.go`
- Test: `service/account_pool_scheduler_test.go`

- [ ] **Step 1: Implement result types**

Add:

```go
type AccountPoolSelectionRequest struct {
	ChannelID           int
	BindingID           int
	RequestModel        string
	ChannelUpstreamModel string
	AttemptedAccountIDs map[int]struct{}
	Now                 int64
}

type AccountPoolSelectionResult struct {
	PoolID               int
	BindingID            int
	AccountID            int
	AccountName          string
	UpstreamModelName    string
	Credential           AccountPoolCredentialConfig
	TokenState           AccountPoolTokenState
}
```

Use the actual formatting and names from implementation, but keep the same behavioral fields.

- [ ] **Step 2: Implement filtering**

Selection must:

1. load an enabled binding by `BindingID` or `ChannelID`;
2. load the enabled pool;
3. load enabled accounts for the pool;
4. parse binding/account JSON with `common.UnmarshalJsonStr`;
5. apply account filter, model policy, account supported models, attempted set, and transient schedulability.

- [ ] **Step 3: Implement strict priority and weight**

After filtering, compute the highest `Priority` among remaining accounts, keep only that tier, then choose by `Weight + 10`. Zero-weight accounts must still be selectable.

- [ ] **Step 4: Return typed errors**

Expose:

```go
var ErrAccountPoolBindingNotRuntimeEnabled = errors.New("account pool binding is not runtime enabled")
var ErrAccountPoolNoSchedulableAccount = errors.New("account pool has no schedulable account")
```

Use `errors.Is`-friendly wrapping when lookup or parsing context is needed.

### Task 3: Regression Tests

**Files:**
- Test: `service/account_pool_scheduler_test.go`

- [ ] **Step 1: Add scheduler tests**

Cover these behaviors:

- draft binding is rejected as not runtime-enabled;
- disabled pool is rejected as no schedulable account;
- account filter `account_ids` limits candidates;
- fixed model policy rejects unknown models;
- supported models filters accounts;
- account model mapping rewrites only final upstream model;
- attempted accounts are skipped and exhaustion returns `ErrAccountPoolNoSchedulableAccount`;
- strict priority selects same-tier account before lower priority;
- zero-weight candidates remain selectable.

- [ ] **Step 2: Run focused tests**

Run:

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./service -run "TestAccountPoolScheduler" -count=1
```

Expected: PASS.

### Task 4: Verification and Review

**Files:**
- Existing files touched by Tasks 1-3

- [ ] **Step 1: Format and focused backend tests**

Run:

```powershell
gofmt -w model/account_pool.go service/account_pool_scheduler.go service/account_pool_scheduler_test.go service/account_pool_service.go
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./model ./service -run "TestAccountPool" -count=1
```

Expected: PASS.

- [ ] **Step 2: Package tests**

Run:

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./service ./controller -count=1
```

Expected: PASS.

- [ ] **Step 3: Claude review**

Run a read-only Claude review on the Phase 2A diff. Ask it to focus on runtime safety, account filtering, JSON wrapper compliance, and whether enabled binding exposure can accidentally route live traffic.

- [ ] **Step 4: Commit**

Commit scheduler implementation and any review fixes:

```powershell
git add model/account_pool.go service/account_pool_scheduler.go service/account_pool_scheduler_test.go docs/superpowers/plans/2026-06-23-sub2api-account-pool-phase2a.md
git commit -m "feat: add account pool scheduler foundation"
```

