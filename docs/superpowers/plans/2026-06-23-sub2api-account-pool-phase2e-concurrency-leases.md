# sub2api Account Pool Phase 2E Concurrency Leases Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Enforce per-account `MaxConcurrency` for enabled account-pool runtime traffic and release slots when supported relay handlers finish.

**Architecture:** Add a process-local lease manager keyed by account ID. The scheduler remains responsible for model/status/priority filtering; a new runtime selection wrapper repeatedly selects a candidate and atomically acquires a lease, skipping accounts whose active lease count has reached `MaxConcurrency`. Supported relay handlers release the lease with `defer` after account-pool runtime selection succeeds.

**Tech Stack:** Go 1.22+, Gin, GORM, testify, existing account-pool service and relay hooks.

---

## Scope

This plan includes:

- process-local account concurrency leases;
- `MaxConcurrency` enforcement for account-pool runtime selection;
- safe release on supported OpenAI-compatible text and Responses handlers;
- tests for lease acquire/release, scheduler skipping saturated accounts, and handler release behavior.

This plan does not include:

- Redis/distributed leases;
- OAuth refresh;
- proxy dialing;
- account-level upstream retry loops;
- sticky sessions;
- per-account metrics;
- supporting unsupported relay formats beyond the Phase 2D guards.

`MaxConcurrency <= 0` is treated as unlimited at runtime for legacy or manually imported rows. Normal service/UI account creation still normalizes the default to `1`, so normal new accounts keep the conservative one-slot default while runtime avoids silently throttling old zero-valued rows.

The current codebase already has `model.AccountPoolAccount.MaxConcurrency` and `service.AccountPoolAccountCreateParams.MaxConcurrency`; this plan only wires the field into runtime scheduling.

## Files

- Create `service/account_pool_concurrency.go`: in-memory lease manager and release helpers.
- Modify `service/account_pool_scheduler.go`: add `MaxConcurrency` to selection result and add leased runtime selection wrapper.
- Modify `service/account_pool_runtime.go`: acquire a lease during runtime selection, store release callback in Gin context, release on credential/setup error.
- Modify `service/account_pool_scheduler_test.go`: scheduler skips saturated accounts.
- Modify `service/account_pool_runtime_test.go`: runtime acquires and releases leases.
- Modify `service/account_pool_service_test.go`: reset lease manager between service-package tests.
- Modify `relay/compatible_handler.go`: release lease after `applyAccountPoolRuntimeSelection` succeeds.
- Modify `relay/responses_handler.go`: release lease after `applyAccountPoolRuntimeSelection` succeeds.
- Modify `relay/account_pool_runtime_test.go`: direct relay hook tests release selected leases.

---

### Task 1: Service Lease Tests

**Files:**
- Modify: `service/account_pool_scheduler_test.go`
- Modify: `service/account_pool_runtime_test.go`
- Modify: `service/account_pool_service_test.go`

- [ ] **Step 1: Write failing scheduler test**

Add this test to `service/account_pool_scheduler_test.go`:

```go
func TestAccountPoolSchedulerWithLeaseSkipsSaturatedAccount(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)
	channel := createAccountPoolServiceTestChannel(t, common.ChannelStatusManuallyDisabled)
	createEnabledAccountPoolSchedulerBinding(t, pool.Id, channel.Id, AccountPoolAccountFilterConfig{}, AccountPoolModelPolicy{})
	first := createAccountPoolSchedulerAccount(t, service, pool.Id, AccountPoolAccountCreateParams{
		Name:           "first",
		Priority:       100,
		MaxConcurrency: 1,
	})
	second := createAccountPoolSchedulerAccount(t, service, pool.Id, AccountPoolAccountCreateParams{
		Name:           "second",
		Priority:       100,
		MaxConcurrency: 1,
	})
	releaseFirst, acquired := tryAcquireAccountPoolRuntimeLease(first.Id, first.MaxConcurrency)
	require.True(t, acquired)
	defer releaseFirst()

	selected, releaseSelected, err := SelectAccountPoolAccountWithLease(AccountPoolSelectionRequest{
		ChannelID:            channel.Id,
		RequestModel:         "gpt-5",
		ChannelUpstreamModel: "gpt-5",
		Now:                  100,
	})

	require.NoError(t, err)
	defer releaseSelected()
	assert.Equal(t, second.Id, selected.AccountID)
}
```

- [ ] **Step 2: Write failing runtime release test**

Add this test to `service/account_pool_runtime_test.go`:

```go
func TestAccountPoolRuntimeLeaseExhaustsThenAllowsSelectionAfterRelease(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	ctx := newAccountPoolRuntimeTestContext()
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)
	channel := createAccountPoolServiceTestChannel(t, common.ChannelStatusManuallyDisabled)
	createEnabledAccountPoolSchedulerBinding(t, pool.Id, channel.Id, AccountPoolAccountFilterConfig{}, AccountPoolModelPolicy{})
	account := createAccountPoolSchedulerAccount(t, service, pool.Id, AccountPoolAccountCreateParams{
		Name:           "single-slot",
		MaxConcurrency: 1,
		Credential: AccountPoolCredentialConfig{
			Type:   AccountPoolCredentialTypeAPIKey,
			APIKey: "sk-single-slot",
		},
	})
	info := newAccountPoolRuntimeTestRelayInfo(channel.Id, "client-gpt-5", "gpt-5")
	request := &dto.GeneralOpenAIRequest{Model: "gpt-5"}

	err := ApplyAccountPoolRuntimeSelection(ctx, info, request)
	require.NoError(t, err)
	assert.Equal(t, account.Id, GetSelectedAccountPoolAccountID(ctx))

	_, _, err = SelectAccountPoolAccountWithLease(AccountPoolSelectionRequest{
		ChannelID:            channel.Id,
		RequestModel:         "client-gpt-5",
		ChannelUpstreamModel: "gpt-5",
		Now:                  100,
	})
	require.ErrorIs(t, err, ErrAccountPoolNoSchedulableAccount)

	ReleaseAccountPoolRuntimeSelection(ctx)
	selected, release, err := SelectAccountPoolAccountWithLease(AccountPoolSelectionRequest{
		ChannelID:            channel.Id,
		RequestModel:         "client-gpt-5",
		ChannelUpstreamModel: "gpt-5",
		Now:                  100,
	})
	require.NoError(t, err)
	defer release()
	assert.Equal(t, account.Id, selected.AccountID)
}
```

- [ ] **Step 3: Write failing zero-concurrency compatibility test**

Add this test to `service/account_pool_scheduler_test.go`:

```go
func TestAccountPoolRuntimeLeaseTreatsZeroConcurrencyAsUnlimited(t *testing.T) {
	setupAccountPoolServiceTestDB(t)

	releaseOne, acquired := tryAcquireAccountPoolRuntimeLease(1001, 0)
	require.True(t, acquired)
	defer releaseOne()
	releaseTwo, acquired := tryAcquireAccountPoolRuntimeLease(1001, 0)
	require.True(t, acquired)
	defer releaseTwo()
}
```

- [ ] **Step 4: Reset leases in test DB setup**

Add a reset call in `setupAccountPoolServiceTestDB(t)`:

```go
resetAccountPoolRuntimeLeasesForTest()
```

This test-only reset is in the `service` package and should not be exported.

- [ ] **Step 5: Run red service tests**

Run:

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./service -run "TestAccountPoolSchedulerWithLease|TestAccountPoolRuntimeLease" -count=1
```

Expected: FAIL because `SelectAccountPoolAccountWithLease`, `tryAcquireAccountPoolRuntimeLease`, `ReleaseAccountPoolRuntimeSelection`, and `resetAccountPoolRuntimeLeasesForTest` do not exist.

### Task 2: Lease Manager and Runtime Selection

**Files:**
- Create: `service/account_pool_concurrency.go`
- Modify: `service/account_pool_scheduler.go`
- Modify: `service/account_pool_runtime.go`
- Modify: `service/account_pool_service_test.go`

- [ ] **Step 1: Add lease manager**

Create `service/account_pool_concurrency.go`:

```go
package service

import (
	"sync"

	"github.com/gin-gonic/gin"
)

const accountPoolRuntimeLeaseReleaseContextKey = "account_pool_runtime_lease_release"

type accountPoolRuntimeReleaseFunc func()

type accountPoolRuntimeLeaseManager struct {
	mu     sync.Mutex
	active map[int]int
}

var accountPoolRuntimeLeases = newAccountPoolRuntimeLeaseManager()

func newAccountPoolRuntimeLeaseManager() *accountPoolRuntimeLeaseManager {
	return &accountPoolRuntimeLeaseManager{active: map[int]int{}}
}

func tryAcquireAccountPoolRuntimeLease(accountID int, maxConcurrency int) (accountPoolRuntimeReleaseFunc, bool) {
	return accountPoolRuntimeLeases.tryAcquire(accountID, maxConcurrency)
}

func (m *accountPoolRuntimeLeaseManager) tryAcquire(accountID int, maxConcurrency int) (accountPoolRuntimeReleaseFunc, bool) {
	if accountID <= 0 {
		return nil, false
	}
	if maxConcurrency <= 0 {
		return func() {}, true
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active[accountID] >= maxConcurrency {
		return nil, false
	}
	m.active[accountID]++
	var once sync.Once
	return func() {
		once.Do(func() {
			m.mu.Lock()
			defer m.mu.Unlock()
			if m.active[accountID] <= 1 {
				delete(m.active, accountID)
				return
			}
			m.active[accountID]--
		})
	}, true
}

func ReleaseAccountPoolRuntimeSelection(c *gin.Context) {
	if c == nil {
		return
	}
	value, exists := c.Get(accountPoolRuntimeLeaseReleaseContextKey)
	if !exists || value == nil {
		return
	}
	release, ok := value.(accountPoolRuntimeReleaseFunc)
	if !ok || release == nil {
		return
	}
	release()
	c.Set(accountPoolRuntimeLeaseReleaseContextKey, nil)
}

func setAccountPoolRuntimeLeaseRelease(c *gin.Context, release accountPoolRuntimeReleaseFunc) {
	if c == nil || release == nil {
		return
	}
	c.Set(accountPoolRuntimeLeaseReleaseContextKey, release)
}

func resetAccountPoolRuntimeLeasesForTest() {
	accountPoolRuntimeLeases = newAccountPoolRuntimeLeaseManager()
}
```

- [ ] **Step 2: Add max concurrency to selection result**

In `service/account_pool_scheduler.go`, extend `AccountPoolSelectionResult`:

```go
type AccountPoolSelectionResult struct {
	PoolID            int
	BindingID         int
	AccountID         int
	AccountName       string
	MaxConcurrency    int
	UpstreamModelName string
	Credential        AccountPoolCredentialConfig
	TokenState        AccountPoolTokenState
}
```

Set `MaxConcurrency: selected.account.MaxConcurrency` when building the result.

- [ ] **Step 3: Add leased selection wrapper**

Add this function to `service/account_pool_scheduler.go`:

```go
func SelectAccountPoolAccountWithLease(req AccountPoolSelectionRequest) (AccountPoolSelectionResult, accountPoolRuntimeReleaseFunc, error) {
	attempted := make(map[int]struct{}, len(req.AttemptedAccountIDs)+1)
	for accountID := range req.AttemptedAccountIDs {
		attempted[accountID] = struct{}{}
	}
	for {
		req.AttemptedAccountIDs = attempted
		selection, err := SelectAccountPoolAccount(req)
		if err != nil {
			return AccountPoolSelectionResult{}, nil, err
		}
		release, acquired := tryAcquireAccountPoolRuntimeLease(selection.AccountID, selection.MaxConcurrency)
		if acquired {
			return selection, release, nil
		}
		attempted[selection.AccountID] = struct{}{}
	}
}
```

- [ ] **Step 4: Acquire and store release during runtime selection**

In `service/account_pool_runtime.go`, replace the call to `SelectAccountPoolAccount` and guard the acquired lease with a local defer until the success path stores it in Gin context:

```go
selection, release, err := SelectAccountPoolAccountWithLease(AccountPoolSelectionRequest{
	ChannelID:            info.ChannelId,
	RequestModel:         info.OriginModelName,
	ChannelUpstreamModel: info.UpstreamModelName,
	AttemptedAccountIDs:  GetAccountPoolAttemptedAccountIDs(c),
})
if err != nil {
	if errors.Is(err, ErrAccountPoolBindingNotRuntimeEnabled) {
		return nil
	}
	return err
}
releaseStored := false
defer func() {
	if !releaseStored {
		release()
	}
}()

runtimeCredential := strings.TrimSpace(selection.Credential.APIKey)
if runtimeCredential == "" {
	runtimeCredential = strings.TrimSpace(selection.TokenState.AccessToken)
}
if runtimeCredential == "" {
	return errors.New("account pool selected account has no runtime credential")
}

info.ApiKey = runtimeCredential
info.UpstreamModelName = selection.UpstreamModelName
if request != nil {
	request.SetModelName(selection.UpstreamModelName)
}
c.Set(accountPoolSelectedPoolIDContextKey, selection.PoolID)
c.Set(accountPoolSelectedBindingIDContextKey, selection.BindingID)
c.Set(accountPoolSelectedAccountIDContextKey, selection.AccountID)
AddAccountPoolAttemptedAccountID(c, selection.AccountID)
setAccountPoolRuntimeLeaseRelease(c, release)
releaseStored = true
return nil
```

- [ ] **Step 5: Reset leases in service test setup**

In `setupAccountPoolServiceTestDB(t)`, call:

```go
resetAccountPoolRuntimeLeasesForTest()
```

- [ ] **Step 6: Run green service tests**

Run:

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./service -run "TestAccountPoolSchedulerWithLease|TestAccountPoolRuntimeLease|TestAccountPoolRuntime" -count=1
```

Expected: PASS.

### Task 3: Relay Release Hooks

**Files:**
- Modify: `relay/compatible_handler.go`
- Modify: `relay/responses_handler.go`
- Modify: `relay/account_pool_runtime_test.go`

- [ ] **Step 1: Update supported relay handlers**

In `TextHelper`, immediately after successful account-pool runtime selection:

```go
if newAPIError := applyAccountPoolRuntimeSelection(c, info, request); newAPIError != nil {
	return newAPIError
}
defer service.ReleaseAccountPoolRuntimeSelection(c)
```

In `ResponsesHelper`, immediately after successful account-pool runtime selection:

```go
if newAPIError := applyAccountPoolRuntimeSelection(c, info, request); newAPIError != nil {
	return newAPIError
}
defer service.ReleaseAccountPoolRuntimeSelection(c)
```

The release helper is a no-op for non-account-pool channels.

- [ ] **Step 2: Update direct relay hook tests**

In `relay/account_pool_runtime_test.go`, add this defer immediately after direct successful calls to `applyAccountPoolRuntimeSelection` that select an account:

```go
defer service.ReleaseAccountPoolRuntimeSelection(ctx)
```

This is needed because those unit tests call the hook directly instead of going through `TextHelper` / `ResponsesHelper`.

- [ ] **Step 3: Run relay tests**

Run:

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./relay -run "TestAccountPoolRelay" -count=1
```

Expected: PASS.

### Task 4: Verification and Review

**Files:**
- All modified Phase 2E files

- [ ] **Step 1: Gofmt**

Run:

```powershell
gofmt -w service/account_pool_concurrency.go service/account_pool_scheduler.go service/account_pool_runtime.go service/account_pool_scheduler_test.go service/account_pool_runtime_test.go service/account_pool_service_test.go relay/compatible_handler.go relay/responses_handler.go relay/account_pool_runtime_test.go
```

- [ ] **Step 2: Focused tests**

Run:

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./service ./relay -run "TestAccountPool" -count=1
```

Expected: PASS.

- [ ] **Step 3: Package tests**

Run:

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./model ./service ./controller ./relay -count=1
```

Expected: PASS.

- [ ] **Step 4: Claude review**

Ask Claude to review before committing. Focus on:

- whether leases can leak on request errors;
- whether the release is idempotent;
- whether saturated accounts are skipped without breaking priority/weight behavior;
- whether service tests reset global lease state;
- whether distributed deployments are clearly out of scope.

- [ ] **Step 5: Commit**

Commit:

```powershell
git add service/account_pool_concurrency.go service/account_pool_scheduler.go service/account_pool_runtime.go service/account_pool_scheduler_test.go service/account_pool_runtime_test.go service/account_pool_service_test.go relay/compatible_handler.go relay/responses_handler.go relay/account_pool_runtime_test.go
git add -f docs/superpowers/plans/2026-06-23-sub2api-account-pool-phase2e-concurrency-leases.md
git commit -m "feat: enforce account pool concurrency leases"
```

## Self-Review

- Spec coverage: this implements the Phase 2 lifecycle requirement that account concurrency slots are acquired before upstream relay and released afterward. OAuth refresh, proxy dialing, account-level retry, sticky sessions, metrics, and Redis leases remain explicitly out of scope.
- Placeholder scan: no TBD or fill-in-later steps remain.
- Type consistency: `accountPoolRuntimeReleaseFunc`, `SelectAccountPoolAccountWithLease`, and `ReleaseAccountPoolRuntimeSelection` are introduced before use and referenced consistently.
