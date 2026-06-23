# Account Pool Phase 2G Account Failure State Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Persist account-level failure state so failed account-pool accounts stop being scheduled for the right reason after relay failures.

**Architecture:** Relay keeps deciding whether an account failure can be retried inside the selected channel. Service owns failure classification and GORM updates for `AccountPoolAccount`, because those fields are account-domain state. Updates happen only on failed account attempts, not on every successful request.

**Tech Stack:** Go 1.22+, Gin context, GORM, existing account-pool models, existing `types.NewAPIError`, testify tests.

---

## Scope

Implement only account failure state updates:

- selected account ID 0 or nil error does nothing;
- 401 / 403 marks account `expired`;
- 429 sets `RateLimitedUntil`;
- request failures and 5xx set `TempDisabledUntil` and `TempDisabledReason`;
- all account failures update sanitized, truncated `LastError`;
- relay account-pool attempt wrapper records failure before deciding whether to retry another account.

Do not implement OAuth refresh, success metrics, account recovery, proxy-specific state, distributed state, monitor aggregation, or UI in this phase.

## Constants

- `accountPoolRateLimitCooldownSeconds = 60`
- `accountPoolTemporaryDisableSeconds = 60`
- `accountPoolLastErrorMaxLength = 1024`

These are intentionally conservative defaults for the first stateful pass.

## Task 1: Service Failure Recorder

**Files:**
- Create: `service/account_pool_failure.go`
- Create: `service/account_pool_failure_test.go`

- [ ] **Step 1: Write failing service tests**

Add tests that create an account, call the recorder, and assert persistent fields:

```go
func TestRecordAccountPoolRuntimeAttemptFailureMarksAuthFailureExpired(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)
	account := createAccountPoolSchedulerAccount(t, service, pool.Id, AccountPoolAccountCreateParams{Name: "auth-failed"})
	err := types.NewErrorWithStatusCode(errors.New("authorization: bearer sk-secret-token-value"), types.ErrorCodeBadResponseStatusCode, http.StatusUnauthorized)

	require.NoError(t, RecordAccountPoolRuntimeAttemptFailure(account.Id, err, 1000))

	var reloaded model.AccountPoolAccount
	require.NoError(t, model.DB.First(&reloaded, account.Id).Error)
	assert.Equal(t, model.AccountPoolAccountStatusExpired, reloaded.Status)
	assert.Zero(t, reloaded.RateLimitedUntil)
	assert.Zero(t, reloaded.TempDisabledUntil)
	assert.Contains(t, reloaded.LastError, "status_code=401")
	assert.NotContains(t, reloaded.LastError, "sk-secret-token-value")
}
```

```go
func TestRecordAccountPoolRuntimeAttemptFailureSetsRateLimitCooldown(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)
	account := createAccountPoolSchedulerAccount(t, service, pool.Id, AccountPoolAccountCreateParams{Name: "rate-limited"})
	err := types.NewErrorWithStatusCode(errors.New("too many requests"), types.ErrorCodeBadResponseStatusCode, http.StatusTooManyRequests)

	require.NoError(t, RecordAccountPoolRuntimeAttemptFailure(account.Id, err, 1000))

	var reloaded model.AccountPoolAccount
	require.NoError(t, model.DB.First(&reloaded, account.Id).Error)
	assert.Equal(t, model.AccountPoolAccountStatusEnabled, reloaded.Status)
	assert.Equal(t, int64(1060), reloaded.RateLimitedUntil)
	assert.Zero(t, reloaded.TempDisabledUntil)
	assert.Contains(t, reloaded.LastError, "too many requests")
}
```

```go
func TestRecordAccountPoolRuntimeAttemptFailureSetsTemporaryDisableForServerError(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)
	account := createAccountPoolSchedulerAccount(t, service, pool.Id, AccountPoolAccountCreateParams{Name: "server-failed"})
	err := types.NewErrorWithStatusCode(errors.New("upstream unavailable"), types.ErrorCodeBadResponseStatusCode, http.StatusBadGateway)

	require.NoError(t, RecordAccountPoolRuntimeAttemptFailure(account.Id, err, 1000))

	var reloaded model.AccountPoolAccount
	require.NoError(t, model.DB.First(&reloaded, account.Id).Error)
	assert.Equal(t, model.AccountPoolAccountStatusEnabled, reloaded.Status)
	assert.Zero(t, reloaded.RateLimitedUntil)
	assert.Equal(t, int64(1060), reloaded.TempDisabledUntil)
	assert.Contains(t, reloaded.TempDisabledReason, "status_code=502")
	assert.Contains(t, reloaded.LastError, "upstream unavailable")
}
```

```go
func TestRecordAccountPoolRuntimeAttemptFailureTruncatesLastError(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)
	account := createAccountPoolSchedulerAccount(t, service, pool.Id, AccountPoolAccountCreateParams{Name: "long-error"})
	err := types.NewErrorWithStatusCode(errors.New(strings.Repeat("x", 2000)), types.ErrorCodeBadResponseStatusCode, http.StatusInternalServerError)

	require.NoError(t, RecordAccountPoolRuntimeAttemptFailure(account.Id, err, 1000))

	var reloaded model.AccountPoolAccount
	require.NoError(t, model.DB.First(&reloaded, account.Id).Error)
	assert.LessOrEqual(t, len(reloaded.LastError), accountPoolLastErrorMaxLength)
	assert.LessOrEqual(t, len(reloaded.TempDisabledReason), accountPoolTempDisabledReasonMaxLength)
}
```

```go
func TestRecordAccountPoolRuntimeAttemptFailureNoopsWithoutAccountOrError(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)
	account := createAccountPoolSchedulerAccount(t, service, pool.Id, AccountPoolAccountCreateParams{Name: "noop"})

	require.NoError(t, RecordAccountPoolRuntimeAttemptFailure(0, types.NewErrorWithStatusCode(errors.New("ignored"), types.ErrorCodeBadResponseStatusCode, http.StatusInternalServerError), 1000))
	require.NoError(t, RecordAccountPoolRuntimeAttemptFailure(account.Id, nil, 1000))

	var reloaded model.AccountPoolAccount
	require.NoError(t, model.DB.First(&reloaded, account.Id).Error)
	assert.Equal(t, model.AccountPoolAccountStatusEnabled, reloaded.Status)
	assert.Empty(t, reloaded.LastError)
	assert.Zero(t, reloaded.RateLimitedUntil)
	assert.Zero(t, reloaded.TempDisabledUntil)
}
```

- [ ] **Step 2: Run service tests and verify RED**

Run:

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./service -run "TestRecordAccountPoolRuntimeAttemptFailure" -count=1
```

Expected: fail because `RecordAccountPoolRuntimeAttemptFailure` does not exist.

- [ ] **Step 3: Implement recorder**

Create `service/account_pool_failure.go` with:

- `RecordAccountPoolRuntimeAttemptFailure(accountID int, err *types.NewAPIError, now int64) error`
- `accountPoolFailureUpdate(err *types.NewAPIError, now int64) map[string]any`
- `sanitizeAccountPoolFailureMessage(err *types.NewAPIError, maxLen int) string`

Use `model.DB.Model(&model.AccountPoolAccount{}).Where("id = ?", accountID).Updates(updates).Error`.

Classification:

- 401 / 403:
  - `status = expired`
  - `last_error = sanitized`
  - clear `rate_limited_until`, `temp_disabled_until`, `temp_disabled_reason`
- 429:
  - `rate_limited_until = now + 60`
  - `last_error = sanitized`
  - clear `temp_disabled_until`, `temp_disabled_reason`
- `ErrorCodeDoRequestFailed`, invalid/non-HTTP status, or 5xx:
  - `temp_disabled_until = now + 60`
  - `temp_disabled_reason = sanitized truncated to 512`
  - `last_error = sanitized`
- other non-skip errors:
  - only `last_error = sanitized`

- [ ] **Step 4: Run service tests and verify GREEN**

Run the same focused service test command.

## Task 2: Relay Integration

**Files:**
- Modify: `relay/account_pool_runtime.go`
- Modify: `relay/account_pool_runtime_test.go`

- [ ] **Step 1: Write failing relay test**

Add a test that uses `runAccountPoolRuntimeAttempts`, returns a 429 for the first selected account, and asserts the first account is not selected on the second attempt because it is now rate-limited:

```go
func TestAccountPoolRuntimeAttemptsRecordFailureBeforeRetryingNextAccount(t *testing.T) {
	setupAccountPoolRelayTestDB(t)
	ctx := newAccountPoolRelayTestContext("/v1/chat/completions")
	pool := createAccountPoolRelayTestPool(t)
	channel := createAccountPoolRelayTestChannel(t)
	createAccountPoolRelayTestEnabledBindingWithRetryTimes(t, pool.Id, channel.Id, 1)
	first := createAccountPoolRelayTestAccount(t, pool.Id, service.AccountPoolAccountCreateParams{Name: "first", Priority: 100})
	second := createAccountPoolRelayTestAccount(t, pool.Id, service.AccountPoolAccountCreateParams{Name: "second", Priority: 50})
	info := newAccountPoolRelayTestInfo(channel.Id, "client-gpt-5", "gpt-5")
	baseRequest := &dto.GeneralOpenAIRequest{Model: "gpt-5"}
	selected := make([]int, 0, 2)

	newAPIError := runAccountPoolRuntimeAttempts(ctx, info, func() (dto.Request, *types.NewAPIError) {
		request, err := common.DeepCopy(baseRequest)
		if err != nil {
			return nil, types.NewError(err, types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
		}
		return request, nil
	}, func(request dto.Request) *types.NewAPIError {
		selected = append(selected, service.GetSelectedAccountPoolAccountID(ctx))
		if len(selected) == 1 {
			return types.NewErrorWithStatusCode(errors.New("rate limited"), types.ErrorCodeBadResponseStatusCode, http.StatusTooManyRequests)
		}
		return nil
	})

	require.Nil(t, newAPIError)
	assert.Equal(t, []int{first.Id, second.Id}, selected)
	var reloaded model.AccountPoolAccount
	require.NoError(t, model.DB.First(&reloaded, first.Id).Error)
	assert.Greater(t, reloaded.RateLimitedUntil, int64(0))
	assert.Contains(t, reloaded.LastError, "rate limited")
}
```

- [ ] **Step 2: Run relay test and verify RED**

Run:

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./relay -run "TestAccountPoolRuntimeAttemptsRecordFailureBeforeRetryingNextAccount" -count=1
```

Expected: fail because relay does not call the recorder.

- [ ] **Step 3: Call recorder from relay wrapper**

In `runAccountPoolRuntimeAttempts`, after `attempt(request)` returns a non-nil error and after the lease is released, call:

```go
if selectedAccountID > 0 {
	_ = service.RecordAccountPoolRuntimeAttemptFailure(selectedAccountID, newAPIError, common.GetTimestamp())
}
```

Do not block the original relay error on recorder failure in this phase; account state update failure must not replace the user-facing upstream error.

- [ ] **Step 4: Run focused relay tests**

Run:

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./relay -run "TestAccountPoolRuntimeAttempts" -count=1
```

Expected: pass.

## Task 3: Verification and Review

- [ ] **Step 1: Format**

Run:

```powershell
gofmt -w service/account_pool_failure.go service/account_pool_failure_test.go relay/account_pool_runtime.go relay/account_pool_runtime_test.go
```

- [ ] **Step 2: Package tests**

Run:

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./service ./relay ./controller -count=1
```

- [ ] **Step 3: Claude review**

Try a short implementation review. If default Claude is still rate-limited, retry once with `--settings ~/.claude/settings.wynth.json`. If both fail, record the reason and proceed with local verification.

- [ ] **Step 4: Commit**

Commit:

```powershell
git add service/account_pool_failure.go service/account_pool_failure_test.go relay/account_pool_runtime.go relay/account_pool_runtime_test.go
git add -f docs/superpowers/plans/2026-06-23-sub2api-account-pool-phase2g-account-failure-state.md
git commit -m "feat: record account pool failure state"
```

## Self-Review

- Spec coverage: Adds account-level failure state used by the scheduler.
- Scope control: Does not implement OAuth refresh, success metrics, recovery, UI, or proxy-specific behavior.
- DB compatibility: Uses GORM `Updates` only; no raw SQL.
- Test quality: Tests observable persisted fields and scheduler-visible behavior.
- JSON rule: No JSON marshal/unmarshal added.
