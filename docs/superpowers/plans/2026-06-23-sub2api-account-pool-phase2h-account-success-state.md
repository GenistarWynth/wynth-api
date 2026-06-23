# Account Pool Phase 2H Account Success State Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Record account-pool runtime success state after a selected account completes an upstream request.

**Architecture:** Add a small service-layer success recorder that only updates enabled accounts. Wire the relay account runtime wrapper to call it after the selected lease has been released and before returning success.

**Tech Stack:** Go, GORM, Gin relay context, testify tests.

---

### Task 1: Service Success Recorder

**Files:**
- Create: `service/account_pool_success_test.go`
- Create: `service/account_pool_success.go`

- [ ] **Step 1: Write the failing test**

Create `service/account_pool_success_test.go` with tests that prove a successful runtime attempt records `last_used_at` and clears transient failure fields, while zero IDs and non-enabled accounts are no-ops.

- [ ] **Step 2: Run test to verify it fails**

Run:

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./service -run "TestRecordAccountPoolRuntimeAttemptSuccess" -count=1
```

Expected: fail because `RecordAccountPoolRuntimeAttemptSuccess` does not exist yet.

- [ ] **Step 3: Write minimal implementation**

Create `service/account_pool_success.go` with:

```go
func RecordAccountPoolRuntimeAttemptSuccess(accountID int, now int64) error
```

It should:
- no-op for `accountID <= 0`;
- default `now` to `common.GetTimestamp()` when needed;
- update only `status = enabled` accounts;
- set `last_used_at`;
- clear `rate_limited_until`, `temp_disabled_until`, `temp_disabled_reason`, and `last_error`.

- [ ] **Step 4: Run test to verify it passes**

Run the same focused service command and confirm it passes.

### Task 2: Relay Success Hook

**Files:**
- Modify: `relay/account_pool_runtime_test.go`
- Modify: `relay/account_pool_runtime.go`

- [ ] **Step 1: Write the failing test**

Add a relay wrapper test showing that a successful selected account attempt updates the selected account with a non-zero `last_used_at` and clears prior transient failure fields.

- [ ] **Step 2: Run test to verify it fails**

Run:

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./relay -run "TestAccountPoolRuntimeAttemptsRecordSuccess" -count=1
```

Expected: fail because the relay wrapper does not call the success recorder.

- [ ] **Step 3: Write minimal implementation**

In `runAccountPoolRuntimeAttempts`, after the attempt returns `nil` and the selection lease is released, call:

```go
_ = service.RecordAccountPoolRuntimeAttemptSuccess(selectedAccountID, common.GetTimestamp())
```

only when `selectedAccountID > 0`.

- [ ] **Step 4: Run tests and review**

Run:

```powershell
gofmt -w service/account_pool_success.go service/account_pool_success_test.go relay/account_pool_runtime.go relay/account_pool_runtime_test.go
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./service ./relay -run "TestRecordAccountPoolRuntimeAttemptSuccess|TestAccountPoolRuntimeAttemptsRecordSuccess" -count=1
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./service ./relay ./controller -count=1
```

Then request Claude review of the diff and address any Critical or Important findings.
