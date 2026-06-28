# sub2api Account Pool Phase 2C Relay Hook Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Wire the Phase 2B account-pool runtime helper into the smallest safe live relay surface: OpenAI-compatible chat/text and OpenAI Responses requests.

**Architecture:** Keep runtime selection in `service.ApplyAccountPoolRuntimeSelection`. Add a relay-local adapter that maps service errors to `types.NewAPIError`, then call it after channel model mapping and before adaptor initialization/conversion. Do not activate account-pool bindings through admin APIs in this slice.

**Tech Stack:** Go 1.22+, Gin, GORM, `github.com/glebarez/sqlite` test DB, relay handlers, testify.

---

## Scope

This plan intentionally includes only:

- `relay.TextHelper` for OpenAI-compatible chat/text traffic;
- `relay.ResponsesHelper` for OpenAI Responses traffic;
- a relay-local error mapping helper;
- tests proving enabled account-pool bindings are applied or fail before upstream calls.

This plan intentionally does not include:

- Claude handler wiring;
- Gemini handler wiring;
- image/audio/embedding/rerank/task/realtime wiring;
- admin activation APIs for account-pool bindings;
- channel enable guard changes;
- account-level retry loops inside one selected channel;
- OAuth refresh;
- proxy dialing;
- `MaxConcurrency` leases;
- ChatGPT reverse-proxy protocol behavior.

Claude relay handling is excluded because it performs thinking-suffix and effort normalization after `adaptor.Init`. That ordering needs a separate Phase 2D plan so account-level model mapping does not fight Claude-specific request normalization.

## Placement Contract

For OpenAI-compatible text and Responses traffic, call account-pool runtime selection here:

1. `info.InitChannelMeta(c)`;
2. copy/normalize the request object;
3. `helper.ModelMappedHelper(c, info, request)`;
4. `applyAccountPoolRuntimeSelection(c, info, request)`;
5. `GetAdaptor(info.ApiType)`;
6. `adaptor.Init(info)`;
7. request conversion / upstream request / response handling.

This preserves model mapping order:

1. client billing model;
2. channel `ModelMapping`;
3. account-pool binding/account filters;
4. account `ModelMapping`;
5. upstream request model.

## Error Contract

`service.ApplyAccountPoolRuntimeSelection` already treats missing/draft bindings as no-op. Relay only needs to map real runtime errors:

- `service.ErrAccountPoolNoSchedulableAccount` -> `types.ErrorCodeGetChannelFailed`, HTTP `503`, no `ErrOptionWithSkipRetry`;
- any other runtime account-pool error -> `types.ErrorCodeGetChannelFailed`, HTTP `503`, no `ErrOptionWithSkipRetry`.

The no-skip-retry behavior matters: channel retry should be allowed to move to the next channel when a bound account pool is empty, all accounts are attempted, or the selected account lacks usable credentials.

## Files

- Create `relay/account_pool_runtime.go`: relay-local error mapping wrapper around `service.ApplyAccountPoolRuntimeSelection`.
- Create `relay/account_pool_runtime_test.go`: handler-level tests and relay hook tests using an in-memory SQLite DB.
- Modify `relay/compatible_handler.go`: call the relay hook after `helper.ModelMappedHelper`.
- Modify `relay/responses_handler.go`: call the relay hook after `helper.ModelMappedHelper`.
- Modify `docs/superpowers/plans/2026-06-23-sub2api-account-pool-phase2c-relay-hook.md`: keep execution status current.

---

### Task 1: Red Tests for Relay Runtime Hook

**Files:**
- Create: `relay/account_pool_runtime_test.go`

- [ ] **Step 1: Add test fixture helpers**

Use a relay-package SQLite fixture so handler tests do not depend on service-package unexported helpers.

```go
func setupAccountPoolRelayTestDB(t *testing.T) {
	t.Helper()

	oldDB := model.DB
	oldSecret := common.CryptoSecret
	oldStable := common.CryptoSecretStable

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	model.DB = db
	common.CryptoSecret = "account-pool-relay-test-secret"
	common.CryptoSecretStable = true

	require.NoError(t, model.DB.AutoMigrate(
		&model.Channel{},
		&model.Ability{},
		&model.AccountPool{},
		&model.AccountPoolAccount{},
		&model.AccountPoolProxy{},
		&model.AccountPoolChannelBinding{},
	))

	t.Cleanup(func() {
		model.DB = oldDB
		common.CryptoSecret = oldSecret
		common.CryptoSecretStable = oldStable
	})
}
```

Add a context helper that always includes a real request. Handler tests call `relaycommon.GenRelayInfo*`, which reads `c.Request.URL.Path`; a bare Gin context will panic.

```go
func newAccountPoolRelayTestContext(path string) *gin.Context {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, path, nil)
	return ctx
}
```

Add explicit pool, channel, binding, and relay-info helpers. The pool and binding must both be enabled so tests exercise the runtime scheduler, not the no-op draft path.

```go
func createAccountPoolRelayTestPool(t *testing.T) model.AccountPool {
	t.Helper()
	pool := model.AccountPool{
		Name:     "relay-pool",
		Platform: model.AccountPoolPlatformOpenAI,
		Status:   model.AccountPoolStatusEnabled,
	}
	require.NoError(t, model.DB.Create(&pool).Error)
	return pool
}

func createAccountPoolRelayTestChannel(t *testing.T) model.Channel {
	t.Helper()
	channel := model.Channel{
		Type:   constant.ChannelTypeOpenAI,
		Key:    "sk-channel",
		Name:   "relay-channel",
		Status: common.ChannelStatusManuallyDisabled,
	}
	require.NoError(t, model.DB.Create(&channel).Error)
	return channel
}

func createAccountPoolRelayTestEnabledBinding(t *testing.T, poolID int, channelID int) model.AccountPoolChannelBinding {
	t.Helper()
	binding := model.AccountPoolChannelBinding{
		PoolID:    poolID,
		ChannelID: channelID,
		Status:    model.AccountPoolBindingStatusEnabled,
	}
	require.NoError(t, model.DB.Create(&binding).Error)
	return binding
}

func newAccountPoolRelayTestInfo(channelID int, originModel string, upstreamModel string) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		OriginModelName: originModel,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelId:         channelID,
			ApiKey:            "sk-channel",
			UpstreamModelName: upstreamModel,
		},
	}
}
```

- [ ] **Step 2: Add direct relay hook behavior tests**

Cover:

- ordinary unbound channel returns nil and leaves `RelayInfo` unchanged;
- enabled binding applies selected account credential and account model mapping;
- enabled binding with no schedulable account returns HTTP `503`, `ErrorCodeGetChannelFailed`, and `types.IsSkipRetryError(err) == false`.

```go
func TestAccountPoolRelayHookMapsNoSchedulableAccountToRetriable503(t *testing.T) {
	setupAccountPoolRelayTestDB(t)
	ctx := newAccountPoolRelayTestContext()
	pool := createAccountPoolRelayTestPool(t)
	channel := createAccountPoolRelayTestChannel(t)
	createAccountPoolRelayTestEnabledBinding(t, pool.Id, channel.Id)
	info := newAccountPoolRelayTestInfo(channel.Id, "client-gpt-5", "upstream-gpt-5")
	request := &dto.GeneralOpenAIRequest{Model: "upstream-gpt-5"}

	newAPIError := applyAccountPoolRuntimeSelection(ctx, info, request)

	require.NotNil(t, newAPIError)
	require.ErrorIs(t, newAPIError, service.ErrAccountPoolNoSchedulableAccount)
	assert.Equal(t, http.StatusServiceUnavailable, newAPIError.StatusCode)
	assert.Equal(t, types.ErrorCodeGetChannelFailed, newAPIError.GetErrorCode())
	assert.False(t, types.IsSkipRetryError(newAPIError))
}
```

- [ ] **Step 3: Add handler-level no-upstream tests**

Cover:

- `TextHelper` returns retriable 503 for enabled binding with no accounts before it can attempt the upstream request;
- `ResponsesHelper` returns retriable 503 for enabled binding with no accounts before it can attempt the upstream request.

The test context should set:

```go
common.SetContextKey(ctx, constant.ContextKeyChannelId, channel.Id)
common.SetContextKey(ctx, constant.ContextKeyChannelType, constant.ChannelTypeOpenAI)
common.SetContextKey(ctx, constant.ContextKeyChannelKey, "sk-channel")
common.SetContextKey(ctx, constant.ContextKeyOriginalModel, "client-gpt-5")
ctx.Set("model_mapping", `{"client-gpt-5":"upstream-gpt-5"}`)
```

For text:

```go
request := &dto.GeneralOpenAIRequest{Model: "client-gpt-5"}
info := relaycommon.GenRelayInfoOpenAI(ctx, request)
newAPIError := TextHelper(ctx, info)
require.ErrorIs(t, newAPIError, service.ErrAccountPoolNoSchedulableAccount)
```

For Responses:

```go
request := &dto.OpenAIResponsesRequest{Model: "client-gpt-5", Input: "hello"}
info := relaycommon.GenRelayInfoResponses(ctx, request)
newAPIError := ResponsesHelper(ctx, info)
require.ErrorIs(t, newAPIError, service.ErrAccountPoolNoSchedulableAccount)
```

- [ ] **Step 4: Run red tests**

Run:

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./relay -run "TestAccountPoolRelay" -count=1
```

Expected: FAIL because `applyAccountPoolRuntimeSelection` does not exist yet, or because handlers do not yet stop on account-pool exhaustion.

### Task 2: Relay Hook Implementation

**Files:**
- Create: `relay/account_pool_runtime.go`
- Modify: `relay/compatible_handler.go`
- Modify: `relay/responses_handler.go`

- [ ] **Step 1: Add relay-local wrapper**

Create `relay/account_pool_runtime.go`:

```go
package relay

import (
	"net/http"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

func applyAccountPoolRuntimeSelection(c *gin.Context, info *relaycommon.RelayInfo, request dto.Request) *types.NewAPIError {
	err := service.ApplyAccountPoolRuntimeSelection(c, info, request)
	if err == nil {
		return nil
	}
	// Account-pool selection errors should allow the outer channel retry loop
	// to try another channel. Do not add ErrOptionWithSkipRetry here.
	return types.NewErrorWithStatusCode(
		err,
		types.ErrorCodeGetChannelFailed,
		http.StatusServiceUnavailable,
	)
}
```

Do not pass `types.ErrOptionWithSkipRetry()`.

- [ ] **Step 2: Wire `TextHelper`**

After `helper.ModelMappedHelper` succeeds in `relay/compatible_handler.go`, add:

```go
if newAPIError := applyAccountPoolRuntimeSelection(c, info, request); newAPIError != nil {
	return newAPIError
}
```

This must be before stream option handling and before `GetAdaptor(info.ApiType)`.

- [ ] **Step 3: Wire `ResponsesHelper`**

After `helper.ModelMappedHelper` succeeds in `relay/responses_handler.go`, add:

```go
if newAPIError := applyAccountPoolRuntimeSelection(c, info, request); newAPIError != nil {
	return newAPIError
}
```

This must be before `GetAdaptor(info.ApiType)`.

- [ ] **Step 4: Run green tests**

Run:

```powershell
gofmt -w relay/account_pool_runtime.go relay/account_pool_runtime_test.go relay/compatible_handler.go relay/responses_handler.go
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./relay -run "TestAccountPoolRelay" -count=1
```

Expected: PASS.

### Task 3: Broader Verification and Review

**Files:**
- New relay hook files
- Modified relay handlers
- This plan file

- [ ] **Step 1: Focused account-pool tests**

Run:

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./relay ./service -run "TestAccountPoolRelay|TestAccountPoolRuntime|TestAccountPoolScheduler" -count=1
```

Expected: PASS.

- [ ] **Step 2: Package tests**

Run:

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./relay ./service ./controller -count=1
```

Expected: PASS.

- [ ] **Step 3: Claude implementation review**

Ask Claude to review the diff before committing. Focus on:

- relay hook placement after channel model mapping;
- no accidental Claude/Gemini/image/audio wiring;
- retriable 503 error mapping;
- no skip-retry flag;
- no secret leakage in errors or logs;
- no billing model mutation.

- [ ] **Step 4: Commit**

Commit:

```powershell
git add relay/account_pool_runtime.go relay/account_pool_runtime_test.go relay/compatible_handler.go relay/responses_handler.go
git add -f docs/superpowers/plans/2026-06-23-sub2api-account-pool-phase2c-relay-hook.md
git commit -m "feat: wire account pool into openai relay"
```

## Self-Review

- Spec coverage: this plan wires only the minimal OpenAI-compatible live path and keeps account-pool activation, Claude ordering, proxy/OAuth/concurrency, and account-level retry for later.
- Placeholder scan: no TBD/fill-in-later tasks remain.
- Type consistency: `applyAccountPoolRuntimeSelection` accepts `dto.Request`, matching the request types used by both `TextHelper` and `ResponsesHelper`.
