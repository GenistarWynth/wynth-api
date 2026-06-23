# sub2api Account Pool Phase 2B Runtime Helper Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a tested account-pool runtime helper that can apply a selected account to `RelayInfo`, without wiring it into live relay handlers yet.

**Architecture:** Keep relay handlers unchanged in this slice. Add a service-layer helper that runs after channel model mapping, calls the Phase 2A scheduler, records the selected account in request-local context, and updates `RelayInfo.ChannelMeta` with the account credential and account-level upstream model. Ordinary channels and draft bindings are no-op.

**Tech Stack:** Go 1.22+, Gin context, `relay/common.RelayInfo`, GORM-backed account-pool scheduler, testify.

---

## Scope

This plan intentionally does not:

- call the helper from `relay/*_handler.go`;
- enable admin activation of account-pool bindings;
- change channel enable guards;
- implement account-level retry loops;
- implement concurrency leases;
- resolve account proxies;
- refresh OAuth tokens;
- implement ChatGPT reverse-proxy protocol behavior.

The success condition is: tests can construct an enabled account-pool binding, call the helper after channel model mapping, and observe `RelayInfo` updated with the selected account. The normal relay path is unchanged because no handler calls the helper yet.

Future wiring note: Claude relay handling currently has additional thinking-suffix model normalization after `adaptor.Init`. Phase 2C must account for that handler-specific ordering before wiring this helper into Claude traffic.

Future audit note: Phase 2C must decide whether account-level model remapping should set `ChannelMeta.IsModelMapped` when the account upstream model differs from the channel upstream model.

## Placement Contract

Existing relay handlers follow this pattern:

1. `info.InitChannelMeta(c)`
2. `helper.ModelMappedHelper(c, info, request)`
3. request conversion / `adaptor.Init(info)` / upstream request

The account-pool helper must run after step 2 and before step 3. This is different from the earlier broad design wording because channel model mapping currently lives inside `helper.ModelMappedHelper`, not `InitChannelMeta`. Running after model mapping preserves the intended order:

1. client model for billing;
2. channel `ModelMapping`;
3. account-pool binding/account filters;
4. account `ModelMapping`;
5. upstream request model.

## Runtime Helper Contract

Add a service function with this behavior:

- no-op when the current channel has no enabled account-pool binding;
- no-op when the binding is draft/disabled;
- no-op when called before `RelayInfo.ChannelMeta` exists;
- for enabled binding:
  - call `SelectAccountPoolAccount`;
  - update `info.ApiKey` from selected API-key credential or access token;
  - update `info.UpstreamModelName`;
  - call `request.SetModelName(info.UpstreamModelName)` when a request object is supplied;
  - set request-local context fields for selected pool/binding/account IDs;
  - append selected account ID to request-local attempted account state only through a dedicated helper, not by mutating channel `use_channel`.

Do not update `OriginModelName`, `PriceData`, pre-consume data, billing session, or log model. Account-level mapping is upstream-only.

## Files

- Create `service/account_pool_runtime.go`: runtime helper, context helpers, and account credential selection.
- Create `service/account_pool_runtime_test.go`: tests for no-op behavior, selection application, model mapping order, request model update, and billing model preservation.
- Modify `docs/superpowers/plans/2026-06-23-sub2api-account-pool-phase2b-runtime-helper.md`: keep this plan current if implementation reveals a safer boundary.

---

### Task 1: Red Tests for Runtime Helper

**Files:**
- Create: `service/account_pool_runtime_test.go`

- [ ] **Step 1: Write failing tests**

Tests must cover:

- ordinary channel with no binding returns no error and leaves `RelayInfo` unchanged;
- nil `RelayInfo.ChannelMeta` returns no error and leaves `RelayInfo` unchanged;
- draft binding returns no error and leaves `RelayInfo` unchanged;
- enabled binding selects an account and updates `info.ApiKey`, `info.UpstreamModelName`, and `request.Model`;
- `info.OriginModelName` remains the client billing model;
- account `supported_models` and account `ModelMapping` use the channel upstream model produced by `ModelMappedHelper`;
- selected account IDs are exposed through a request-local helper, not through `use_channel`.

- [ ] **Step 2: Run focused tests**

Run:

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./service -run "TestAccountPoolRuntime" -count=1
```

Expected: FAIL because runtime helper does not exist.

### Task 2: Runtime Helper Implementation

**Files:**
- Create: `service/account_pool_runtime.go`

- [ ] **Step 1: Add context helpers**

Add request-local helpers:

- `GetAccountPoolAttemptedAccountIDs(c *gin.Context) map[int]struct{}`
- `AddAccountPoolAttemptedAccountID(c *gin.Context, accountID int)`
- `GetSelectedAccountPoolAccountID(c *gin.Context) int`

Use stable string context keys local to the service package.
Prefix those key names with `account_pool_` and document that they are service-local Gin context keys to avoid collision with `constant.ContextKey*`.

- [ ] **Step 2: Add apply helper**

Add:

```go
func ApplyAccountPoolRuntimeSelection(c *gin.Context, info *relaycommon.RelayInfo, request dto.Request) error
```

Behavior:

- return nil for nil context/info/channel meta;
- call `SelectAccountPoolAccount`;
- treat `ErrAccountPoolBindingNotRuntimeEnabled` as no-op;
- return `ErrAccountPoolNoSchedulableAccount` to the caller for enabled-but-empty pools;
- prefer `Credential.APIKey`, then `TokenState.AccessToken`;
- return an error when an enabled account has no usable runtime credential;
- set `info.ApiKey`, `info.UpstreamModelName`, request model, and selected context fields.

Phase 2C must map `ErrAccountPoolNoSchedulableAccount` to a retriable relay-layer 503 instead of a client 400.

### Task 3: Verification and Review

**Files:**
- New runtime helper files

- [ ] **Step 1: Focused tests**

Run:

```powershell
gofmt -w service/account_pool_runtime.go service/account_pool_runtime_test.go
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./service -run "TestAccountPoolRuntime|TestAccountPoolScheduler" -count=1
```

Expected: PASS.

- [ ] **Step 2: Package tests**

Run:

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./model ./service ./controller -run "TestAccountPool" -count=1
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./service ./controller -count=1
```

Expected: PASS.

- [ ] **Step 3: Claude review**

Ask Claude to review the runtime helper diff before committing. Focus on no live traffic wiring, billing model preservation, model mapping order, and secret leakage.

- [ ] **Step 4: Commit**

Commit:

```powershell
git add -f docs/superpowers/plans/2026-06-23-sub2api-account-pool-phase2b-runtime-helper.md
git add service/account_pool_runtime.go service/account_pool_runtime_test.go
git commit -m "feat: add account pool runtime helper"
```
