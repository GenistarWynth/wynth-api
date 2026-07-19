# Remaining xAI Account-Pool Deferred Items Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Automatically maintain xAI OAuth quota/credential readiness and expose a clearly labeled, read-only 24-hour Free usage estimate.

**Architecture:** Reuse `AccountPoolService.CreateAccount`, `ProbeXAIQuota`, `ReconcileXAIOAuthAccounts`, encrypted runtime snapshots, and the existing main lifecycle. Two context-cancelable master workers use process guards; persisted credential and quota updates retain their existing CAS semantics. Usage estimation is computed from existing account counters at read time and never affects billing or scheduling.

**Tech Stack:** Go 1.22+, GORM v2, `gopool`, `sync/atomic`, `testify`, `httptest`, SQLite test fixtures.

---

### Task 1: Automatic create-time and periodic quota probes

**Files:**
- Create: `service/account_pool_xai_quota_worker.go`
- Create: `service/account_pool_xai_quota_worker_test.go`
- Modify: `service/account_pool_service.go`
- Modify: `service/account_pool_xai_oauth_test.go`

- [ ] **Step 1: Write failing create-trigger tests**

Add deterministic tests that enable the create hook, replace the probe runner with a channel-backed fake, create an xAI OAuth account, and assert the create returns before the blocked fake. Add an SSO import test that observes one scheduled probe for each successful item and a non-xAI/API-key skip test.

- [ ] **Step 2: Run the focused tests and verify RED**

Run: `GOPATH=/root/go GOMODCACHE=/root/go/pkg/mod GOCACHE=/root/.cache/go-build go test ./service -run 'TestAccountPoolXAIQuota(Create|Import|Skip)' -count=1`

Expected: FAIL because the create-time probe hook and runner do not exist.

- [ ] **Step 3: Add the best-effort create hook**

After `buildAccountPoolAccountView` succeeds, call a helper equivalent to:

```go
if pool.Platform == model.AccountPoolPlatformXAI && strings.EqualFold(params.Credential.Type, AccountPoolCredentialTypeOAuth) {
    scheduleAccountPoolXAIQuotaProbe(params.PoolID, account.Id)
}
```

The scheduler checks an atomic startup flag and uses `gopool.Go`; runner errors are sanitized/logged and never returned to `CreateAccount`.

- [ ] **Step 4: Write failing candidate/limit/config tests**

Seed enabled/disabled xAI and non-xAI pools with OAuth/API-key accounts and missing, stale, and fresh snapshots. Assert deterministic oldest-first candidates and `max_per_tick`, plus positive environment overrides and default fallback.

- [ ] **Step 5: Run candidate tests and verify RED**

Run: `GOPATH=/root/go GOMODCACHE=/root/go/pkg/mod GOCACHE=/root/.cache/go-build go test ./service -run 'Test(ListDueAccountPoolXAIQuota|AccountPoolXAIQuotaWorkerConfig)' -count=1`

Expected: FAIL because the worker does not exist.

- [ ] **Step 6: Implement the periodic worker**

Create a context-cancelable `StartAccountPoolXAIQuotaProbeWorker(ctx) <-chan struct{}` with `sync.Once`, `atomic.Bool`, an immediate tick, positive env parsing, and `common.IsMasterNode` gating. Candidate scans use GORM queries and existing encrypted config/runtime parsing. Each candidate calls the injectable proxy-aware `ProbeXAIQuota` runner and the existing persistence path handles cooldowns.

- [ ] **Step 7: Verify and commit A**

Run: `gofmt -w service/account_pool_xai_quota_worker.go service/account_pool_xai_quota_worker_test.go service/account_pool_service.go service/account_pool_xai_oauth_test.go`

Run: `GOPATH=/root/go GOMODCACHE=/root/go/pkg/mod GOCACHE=/root/.cache/go-build go test ./service -run 'AccountPoolXAIQuota|XAISSO' -count=1`

Expected: PASS.

Commit: `feat(account-pools): automate xAI quota probes`

### Task 2: Apply-mode OAuth reconciliation worker

**Files:**
- Create: `service/account_pool_xai_reconcile_worker.go`
- Create: `service/account_pool_xai_reconcile_worker_test.go`
- Modify: `service/account_pool_xai_reconcile.go`
- Modify: `service/account_pool_xai_reconcile_test.go`

- [ ] **Step 1: Write failing classification regression test**

Assert a missing-refresh account with valid access has no action, while missing/expired access without refresh expires and missing/near-expiry access with refresh refreshes.

- [ ] **Step 2: Run classification test and verify RED**

Run: `GOPATH=/root/go GOMODCACHE=/root/go/pkg/mod GOCACHE=/root/.cache/go-build go test ./service -run 'TestAccountPoolServiceReconcileXAIOAuthAccountsDryRunClassifiesCandidates' -count=1`

Expected: FAIL because valid access-only credentials are currently expired immediately.

- [ ] **Step 3: Narrow permanent missing-refresh classification**

Keep credential rejection first. When refresh is absent, return `expire_account` only if access is blank or `expires_at <= now`; otherwise return no action. Existing refresh and CAS code remains unchanged.

- [ ] **Step 4: Write failing worker due/skip/overlap tests**

Assert the sweep calls an injected reconciler only for enabled xAI pools with `DryRun:false`, skips disabled/non-xAI pools, uses the default near-expiry window, and returns without a second call while the first tick is blocked.

- [ ] **Step 5: Implement and verify the worker**

Create `StartAccountPoolXAIOAuthReconcileWorker(ctx) <-chan struct{}` with a five-minute default, `ACCOUNT_POOL_XAI_OAUTH_RECONCILE_INTERVAL_MINUTES`, immediate tick, master-node check, `sync.Once`, `atomic.Bool`, panic recovery, and context cancellation.

Run: `gofmt -w service/account_pool_xai_reconcile*.go`

Run: `GOPATH=/root/go GOMODCACHE=/root/go/pkg/mod GOCACHE=/root/.cache/go-build go test ./service -run 'XAIOAuthReconcile' -count=1`

Expected: PASS.

Commit: `feat(account-pools): reconcile xAI OAuth in background`

### Task 3: Rolling 24-hour Free usage estimate

**Files:**
- Modify: `service/account_pool_xai_quota.go`
- Modify: `service/account_pool_xai_quota_test.go`

- [ ] **Step 1: Write failing deterministic estimate tests**

Cover a new account (cumulative counters within the window), an older recently used account (lifetime-average projection), an older inactive account (zero), paid/inconclusive snapshots (no Free estimate), and account-list/read-probe enrichment without persisting the estimate.

- [ ] **Step 2: Run estimate tests and verify RED**

Run: `GOPATH=/root/go GOMODCACHE=/root/go/pkg/mod GOCACHE=/root/.cache/go-build go test ./service -run 'TestAccountPoolXAIFreeUsage24h' -count=1`

Expected: FAIL because estimate fields do not exist.

- [ ] **Step 3: Implement read-time enrichment**

Add `AccountPoolXAIFreeUsageEstimate` and `FreeUsage24hEstimate` fields. Use `SuccessCount`, `TotalPromptTokens`, `TotalCompletionTokens`, `CreatedTime`, and `LastSuccessAt`; use overflow-safe integer prorating for old accounts. Enrich returned probe snapshots, `GetXAIQuotaSnapshot`, and `buildAccountPoolAccountView`, but persist only upstream observations.

- [ ] **Step 4: Verify and commit C**

Run: `gofmt -w service/account_pool_xai_quota.go service/account_pool_xai_quota_test.go service/account_pool_service.go`

Run: `GOPATH=/root/go GOMODCACHE=/root/go/pkg/mod GOCACHE=/root/.cache/go-build go test ./service -run 'AccountPoolXAI(Quota|FreeUsage)' -count=1`

Expected: PASS.

Commit: `feat(account-pools): estimate xAI Free usage`

### Task 4: Lifecycle, documentation, and verification

**Files:**
- Modify: `main.go`
- Modify: `docs/superpowers/specs/2026-07-19-sub2api-account-pool-sync-design.md`
- Modify: `CHANGELOG.md`

- [ ] **Step 1: Wire both workers into graceful shutdown**

Start both workers beside the capability detector and proxy prober, and append both stable done channels to `serverLifecycle.workerDone`.

- [ ] **Step 2: Update status documentation and changelog**

Replace the three deferred notes with shipped behavior, environment variables, HA/CAS limits, and the counter-based estimate limitation. Do not add a quota-reset editor or cross-platform behavior.

- [ ] **Step 3: Run focused repeat/race and full verification**

Run:

```bash
GOPATH=/root/go GOMODCACHE=/root/go/pkg/mod GOCACHE=/root/.cache/go-build go test ./service -run 'AccountPoolXAI|XAIOAuth|XAISSO' -count=3
GOPATH=/root/go GOMODCACHE=/root/go/pkg/mod GOCACHE=/root/.cache/go-build go test -race ./service -run 'AccountPoolXAI.*Worker|XAIOAuthReconcile.*Worker' -count=1
GOPATH=/root/go GOMODCACHE=/root/go/pkg/mod GOCACHE=/root/.cache/go-build go vet ./service ./...
GOPATH=/root/go GOMODCACHE=/root/go/pkg/mod GOCACHE=/root/.cache/go-build go test ./...
git diff --check
```

Expected: all commands exit 0.

- [ ] **Step 4: Commit integration/docs, merge, and push**

Commit: `docs(account-pools): complete deferred xAI operations`

Fast-forward or merge the feature branch into `main`, rerun the full Go suite on `main`, and push `main` plus the feature branch. Do not create a tag.
