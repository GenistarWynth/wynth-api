# sub2api Account Pool Phase 2D Activation Guard Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Allow admins to activate account-pool bindings without letting unsupported relay endpoints bypass account-pool runtime selection.

**Architecture:** Keep binding creation conservative: new bindings are still draft and must be attached to disabled OpenAI-compatible channels. Add explicit activate/disable endpoints for existing bindings. Activation changes only binding status; it does not enable the channel. Add a short-TTL cached enabled-binding detector and relay guard helpers so any enabled account-pool binding selected by an unsupported relay handler returns a retryable 503 instead of using the channel key directly.

**Tech Stack:** Go 1.22+, Gin, GORM, `pkg/cachex`, relay handlers, account-pool service, testify.

---

## Scope

This plan includes:

- service-level binding activation and disabling;
- API endpoints for activation and disabling;
- channel-type validation so Phase 2D bindings can only target supported OpenAI-compatible channel types;
- a short-TTL cached service helper to detect enabled account-pool bindings by channel ID;
- relay guard for enabled account-pool bindings in unsupported handlers;
- tests proving activation does not auto-enable channels;
- tests proving unsupported handlers return retryable 503 when an enabled binding is selected.

This plan does not include:

- frontend UI changes;
- automatic channel enable/disable from binding activation;
- account-level retry loops;
- proxy dialing;
- OAuth refresh;
- `MaxConcurrency` leases;
- ChatGPT reverse-proxy protocol behavior;
- wiring Claude/Gemini/image/audio/embedding/rerank/task/realtime into full account-pool runtime selection.

Phase 2D supported account-pool channel types:

- `constant.ChannelTypeOpenAI`;
- `constant.ChannelTypeCodex`.

All other channel types, including `mjproxy`/Midjourney-style task channels, must be rejected at binding creation and activation time. Disabling an existing unsupported legacy binding must remain allowed so admins can remediate bad historical data. This is a safety boundary, not a product statement; more channel types can be admitted only when their relay runtime is wired or guarded deliberately.

## Safety Contract

The critical safety issue is that channel selection happens before relay handler execution. If an admin activates an account-pool binding and then enables that channel, the selected channel may receive endpoints that Phase 2C did not wire into account-pool runtime selection. Unsupported handlers must therefore refuse enabled account-pool channels with retryable 503, so the outer retry loop can select another channel.

Allowed account-pool runtime relay formats after this phase:

- OpenAI-compatible text/chat via `TextHelper`;
- OpenAI Responses via `ResponsesHelper`.

Guarded unsupported relay handlers after this phase:

- `AudioHelper`;
- `EmbeddingHelper`;
- `ImageHelper`;
- `RerankHelper`;
- `ClaudeHelper`;
- `GeminiHelper`;
- Gemini embedding helper path;
- `WssHelper`;
- task relay path.

`mjproxy` paths are excluded by channel-type validation and must remain rejected until a future plan explicitly supports them.

## Files

- Modify `service/account_pool_service.go`: add supported-channel validation, `ActivateBinding`, `DisableBinding`, cached enabled-binding lookup, and cache invalidation.
- Modify `service/account_pool_service_test.go`: service tests for activate/disable, channel type validation, cache invalidation, and channel status invariants.
- Modify `controller/account_pool.go`: add activate/disable handlers.
- Modify `controller/account_pool_test.go`: API tests for activate/disable.
- Modify `router/api-router.go`: add binding action routes.
- Modify `relay/account_pool_runtime.go`: add channel-ID resolver and unsupported-handler guard helper.
- Modify unsupported relay handlers listed in the Safety Contract to call the guard:
  - `relay/audio_handler.go`
  - `relay/embedding_handler.go`
  - `relay/image_handler.go`
  - `relay/rerank_handler.go`
  - `relay/claude_handler.go`
  - `relay/gemini_handler.go`
  - `relay/websocket.go`
  - `relay/relay_task.go`
- Modify `relay/account_pool_runtime_test.go`: add guard tests.
- Modify this plan file as implementation reveals details.

---

### Task 1: Service Activation Tests

**Files:**
- Modify: `service/account_pool_service_test.go`

- [ ] **Step 1: Write failing service tests**

Add tests for:

- `ActivateBinding(poolID, bindingID)` changes draft binding to `enabled`;
- activation leaves the channel status unchanged;
- after activation, manual channel enable is allowed because the binding is no longer draft;
- `DisableBinding(poolID, bindingID)` changes enabled binding to `disabled`;
- activation rejects a binding outside the requested pool;
- binding creation rejects unsupported channel types;
- activation rejects unsupported channel types even if legacy data already contains such a binding;
- disabling an enabled unsupported legacy binding succeeds so admins can close risky historical data;
- activation is idempotent for an already enabled binding;
- create still rejects direct `Status: enabled`;
- `AccountPoolRuntimeEnabledForChannel` returns cached true after activation and false after disable.
- deleting a pool invalidates runtime-enabled cache entries for all bindings it bulk-disables.

Expected test shape:

```go
func TestAccountPoolServiceActivateBindingEnablesRuntimeButNotChannel(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)
	channel := createAccountPoolServiceTestChannel(t, common.ChannelStatusManuallyDisabled)
	binding, err := service.CreateBinding(AccountPoolBindingCreateParams{
		PoolID:    pool.Id,
		ChannelID: channel.Id,
	})
	require.NoError(t, err)

	activated, err := service.ActivateBinding(pool.Id, binding.Id)

	require.NoError(t, err)
	assert.Equal(t, model.AccountPoolBindingStatusEnabled, activated.Status)
	assert.Equal(t, common.ChannelStatusManuallyDisabled, activated.ChannelStatus)
	var reloaded model.Channel
	require.NoError(t, model.DB.First(&reloaded, channel.Id).Error)
	assert.Equal(t, common.ChannelStatusManuallyDisabled, reloaded.Status)
	assert.True(t, model.UpdateChannelStatus(channel.Id, "", common.ChannelStatusEnabled, "manual enable after account pool activation"))
}
```

- [ ] **Step 2: Run red service tests**

Run:

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./service -run "TestAccountPoolServiceActivateBinding|TestAccountPoolServiceDisableBinding|TestAccountPoolServiceCreateBindingRejectsNonPhaseOneStatus|TestAccountPoolServiceRuntimeEnabledCache" -count=1
```

Expected: FAIL because `ActivateBinding`, `DisableBinding`, supported-channel validation, and runtime-enabled cache helpers do not exist.

### Task 2: Service Activation Implementation

**Files:**
- Modify: `service/account_pool_service.go`

- [ ] **Step 1: Add binding loader**

Add:

```go
func getAccountPoolBindingForPool(poolID int, bindingID int) (model.AccountPoolChannelBinding, error) {
	var binding model.AccountPoolChannelBinding
	err := model.DB.Where("id = ? AND pool_id = ?", bindingID, poolID).First(&binding).Error
	return binding, err
}
```

- [ ] **Step 2: Add supported channel-type validation**

Add:

```go
func validateAccountPoolRuntimeChannel(channel model.Channel) error {
	switch channel.Type {
	case constant.ChannelTypeOpenAI, constant.ChannelTypeCodex:
		return nil
	default:
		return errors.New("account pool runtime only supports OpenAI-compatible channels in this phase")
	}
}
```

Call this from `CreateBinding` after loading the channel, before checking channel status. Existing account-pool service tests create type `1` OpenAI channels and should keep passing.

- [ ] **Step 3: Add status mutator**

Add:

```go
func (s AccountPoolService) setBindingStatus(poolID int, bindingID int, status string) (AccountPoolBindingView, error) {
	if _, err := getAccountPoolExistingPool(poolID); err != nil {
		return AccountPoolBindingView{}, err
	}
	binding, err := getAccountPoolBindingForPool(poolID, bindingID)
	if err != nil {
		return AccountPoolBindingView{}, err
	}
	var channel model.Channel
	if err := model.DB.First(&channel, binding.ChannelID).Error; err != nil {
		return AccountPoolBindingView{}, err
	}
	if status == model.AccountPoolBindingStatusEnabled {
		if err := validateAccountPoolRuntimeChannel(channel); err != nil {
			return AccountPoolBindingView{}, err
		}
	}
	now := common.GetTimestamp()
	if err := model.DB.Model(&binding).Updates(map[string]any{
		"status":       status,
		"updated_time": now,
	}).Error; err != nil {
		return AccountPoolBindingView{}, err
	}
	binding.Status = status
	binding.UpdatedTime = now
	invalidateAccountPoolRuntimeEnabledForChannel(binding.ChannelID)
	return buildAccountPoolBindingView(binding, channel), nil
}
```

- [ ] **Step 4: Add public service methods**

Add:

```go
func (s AccountPoolService) ActivateBinding(poolID int, bindingID int) (AccountPoolBindingView, error) {
	return s.setBindingStatus(poolID, bindingID, model.AccountPoolBindingStatusEnabled)
}

func (s AccountPoolService) DisableBinding(poolID int, bindingID int) (AccountPoolBindingView, error) {
	return s.setBindingStatus(poolID, bindingID, model.AccountPoolBindingStatusDisabled)
}
```

- [ ] **Step 5: Add cached enabled-binding detector**

Use `pkg/cachex.HybridCache[bool]` with a short TTL. The detector is called on relay hot paths, so it must not issue a DB query for every request.

Add:

```go
const accountPoolRuntimeEnabledCacheTTL = 30 * time.Second

var accountPoolRuntimeEnabledCache *cachex.HybridCache[bool]
var accountPoolRuntimeEnabledCacheOnce sync.Once

func getAccountPoolRuntimeEnabledCache() *cachex.HybridCache[bool] {
	accountPoolRuntimeEnabledCacheOnce.Do(func() {
		accountPoolRuntimeEnabledCache = cachex.NewHybridCache[bool](cachex.HybridCacheConfig[bool]{
			Namespace:    cachex.Namespace("account_pool:runtime_enabled"),
			Redis:        common.RDB,
			RedisCodec:   cachex.JSONCodec[bool]{},
			RedisEnabled: func() bool { return common.RedisEnabled },
			Memory: func() *hot.HotCache[string, bool] {
				return hot.NewHotCache[string, bool](hot.LRU, 1024).Build()
			},
		})
	})
	return accountPoolRuntimeEnabledCache
}
```

Add exported helper:

```go
func AccountPoolRuntimeEnabledForChannel(channelID int) (bool, error) {
	if channelID <= 0 || model.DB == nil {
		return false, nil
	}
	cacheKey := strconv.Itoa(channelID)
	if cached, found, err := getAccountPoolRuntimeEnabledCache().Get(cacheKey); err == nil && found {
		return cached, nil
	}
	var count int64
	err := model.DB.Model(&model.AccountPoolChannelBinding{}).
		Where("channel_id = ? AND status = ?", channelID, model.AccountPoolBindingStatusEnabled).
		Count(&count).Error
	if err != nil {
		return false, err
	}
	enabled := count > 0
	_ = getAccountPoolRuntimeEnabledCache().SetWithTTL(cacheKey, enabled, accountPoolRuntimeEnabledCacheTTL)
	return enabled, nil
}

func invalidateAccountPoolRuntimeEnabledForChannel(channelID int) {
	if channelID <= 0 {
		return
	}
	// HybridCache.DeleteMany accepts raw keys and applies the namespace internally.
	_, _ = getAccountPoolRuntimeEnabledCache().DeleteMany([]string{strconv.Itoa(channelID)})
}
```

Direct DB edits or migration scripts that bypass service methods can leave this cache stale until TTL expiry. That is acceptable for Phase 2D; public service/API activation, disable, and pool delete paths must invalidate immediately.

- [ ] **Step 6: Run green service tests**

Run the same service test command. Expected: PASS.

### Task 3: API Activation Tests and Implementation

**Files:**
- Modify: `controller/account_pool.go`
- Modify: `controller/account_pool_test.go`
- Modify: `router/api-router.go`

- [ ] **Step 1: Write failing API tests**

Add tests:

- `POST /api/account_pools/:id/bindings/:binding_id/activate` returns binding status `enabled`;
- activation response reports channel still disabled;
- `POST /api/account_pools/:id/bindings/:binding_id/disable` returns binding status `disabled`;
- activating a binding from another pool fails;
- creating a binding for a non-OpenAI-compatible channel fails.

- [ ] **Step 2: Add controller helpers**

Add binding ID parser:

```go
func accountPoolBindingIDFromParam(c *gin.Context) (int, bool) {
	id, err := strconv.Atoi(c.Param("binding_id"))
	if err != nil || id == 0 {
		common.ApiError(c, errors.New("invalid account pool binding id"))
		return 0, false
	}
	return id, true
}
```

Add handlers:

```go
func ActivateAccountPoolBinding(c *gin.Context) {
	poolID, ok := accountPoolIDFromParam(c)
	if !ok {
		return
	}
	bindingID, ok := accountPoolBindingIDFromParam(c)
	if !ok {
		return
	}
	binding, err := (&service.AccountPoolService{}).ActivateBinding(poolID, bindingID)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "account_pool.binding_activate", map[string]interface{}{
		"id":         binding.Id,
		"pool_id":    poolID,
		"channel_id": binding.ChannelID,
	})
	common.ApiSuccess(c, accountPoolBindingResponse(binding))
}
```

Add matching `DisableAccountPoolBinding`:

```go
func DisableAccountPoolBinding(c *gin.Context) {
	poolID, ok := accountPoolIDFromParam(c)
	if !ok {
		return
	}
	bindingID, ok := accountPoolBindingIDFromParam(c)
	if !ok {
		return
	}
	binding, err := (&service.AccountPoolService{}).DisableBinding(poolID, bindingID)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "account_pool.binding_disable", map[string]interface{}{
		"id":         binding.Id,
		"pool_id":    poolID,
		"channel_id": binding.ChannelID,
	})
	common.ApiSuccess(c, accountPoolBindingResponse(binding))
}
```

- [ ] **Step 3: Add routes**

Add:

```go
accountPoolRoute.POST("/:id/bindings/:binding_id/activate", controller.ActivateAccountPoolBinding)
accountPoolRoute.POST("/:id/bindings/:binding_id/disable", controller.DisableAccountPoolBinding)
```

- [ ] **Step 4: Run controller tests**

Run:

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./controller -run "TestAccountPoolAPI.*Binding" -count=1
```

Expected: PASS after implementation.

### Task 4: Unsupported Relay Guard Tests and Implementation

**Files:**
- Modify: `relay/account_pool_runtime.go`
- Modify: `relay/account_pool_runtime_test.go`
- Modify: unsupported relay handlers listed in Safety Contract.

- [ ] **Step 1: Write failing guard tests**

Add direct helper tests:

- no enabled binding returns nil;
- enabled binding returns HTTP `503`, `ErrorCodeGetChannelFailed`, and no skip retry;
- guard still works when `RelayInfo.ChannelMeta` is nil but Gin context has `channel_id`;
- error message names the unsupported relay kind but does not expose credentials.

Add one handler-level test for `ImageHelper`:

```go
func TestAccountPoolRelayImageHelperRejectsEnabledBindingBeforeUpstream(t *testing.T) {
	setupAccountPoolRelayTestDB(t)
	ctx := newAccountPoolRelayTestContext("/v1/images/generations")
	pool := createAccountPoolRelayTestPool(t)
	channel := createAccountPoolRelayTestChannel(t)
	createAccountPoolRelayTestEnabledBinding(t, pool.Id, channel.Id)
	setAccountPoolRelayChannelContext(ctx, channel.Id)
	request := &dto.ImageRequest{Model: "gpt-image-1"}
	info := relaycommon.GenRelayInfoImage(ctx, request)

	newAPIError := ImageHelper(ctx, info)

	require.NotNil(t, newAPIError)
	assert.Equal(t, http.StatusServiceUnavailable, newAPIError.StatusCode)
	assert.Equal(t, types.ErrorCodeGetChannelFailed, newAPIError.GetErrorCode())
	assert.False(t, types.IsSkipRetryError(newAPIError))
}
```

- [ ] **Step 2: Add channel ID resolver and relay guard helper**

The guard must not fail open when `info.ChannelMeta` is nil. Resolve the channel ID from initialized metadata first, then fall back to Gin context.

Add:

```go
func accountPoolRuntimeChannelID(c *gin.Context, info *relaycommon.RelayInfo) int {
	if info != nil && info.ChannelMeta != nil && info.ChannelId > 0 {
		return info.ChannelId
	}
	return common.GetContextKeyInt(c, constant.ContextKeyChannelId)
}
```

Add:

```go
func rejectUnsupportedAccountPoolRuntime(c *gin.Context, info *relaycommon.RelayInfo, relayName string) *types.NewAPIError {
	channelID := accountPoolRuntimeChannelID(c, info)
	if channelID <= 0 {
		return nil
	}
	enabled, err := service.AccountPoolRuntimeEnabledForChannel(channelID)
	if err != nil {
		return types.NewErrorWithStatusCode(err, types.ErrorCodeGetChannelFailed, http.StatusServiceUnavailable)
	}
	if !enabled {
		return nil
	}
	return types.NewErrorWithStatusCode(
		fmt.Errorf("account pool runtime does not support %s relay yet", relayName),
		types.ErrorCodeGetChannelFailed,
		http.StatusServiceUnavailable,
	)
}
```

Do not pass `ErrOptionWithSkipRetry`.

- [ ] **Step 3: Call guard from unsupported handlers**

For each unsupported handler, call after `info.InitChannelMeta(c)` and after request type validation/copy if needed, but before `adaptor.Init` and upstream request conversion:

```go
if newAPIError := rejectUnsupportedAccountPoolRuntime(c, info, "image"); newAPIError != nil {
	return newAPIError
}
```

Use stable relay names:

- `audio`
- `embedding`
- `image`
- `rerank`
- `claude`
- `gemini`
- `gemini_embedding`
- `realtime`
- `task`

- [ ] **Step 4: Run relay guard tests**

Run:

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./relay -run "TestAccountPoolRelay.*Guard|TestAccountPoolRelayImageHelper" -count=1
```

Expected: PASS.

### Task 5: Verification and Review

**Files:**
- All modified Phase 2D files

- [ ] **Step 1: Gofmt**

Run:

```powershell
gofmt -w service/account_pool_service.go service/account_pool_service_test.go controller/account_pool.go controller/account_pool_test.go router/api-router.go relay/account_pool_runtime.go relay/account_pool_runtime_test.go relay/*.go
```

- [ ] **Step 2: Focused tests**

Run:

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./service ./controller ./relay -run "TestAccountPool" -count=1
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

- whether activation can let unsupported endpoints bypass account-pool runtime;
- whether binding activation accidentally enables channels;
- whether unsupported relay guards are retryable;
- whether create still rejects direct `enabled` status;
- whether the enabled-binding check cache is invalidated on activate/disable;
- whether the change is too broad for Phase 2D.

- [ ] **Step 5: Commit**

Commit:

```powershell
git add service/account_pool_service.go service/account_pool_service_test.go controller/account_pool.go controller/account_pool_test.go router/api-router.go relay
git add -f docs/superpowers/plans/2026-06-23-sub2api-account-pool-phase2d-activation-guard.md
git commit -m "feat: add account pool activation guard"
```

## Self-Review

- Spec coverage: activation, disabling, supported channel-type validation, cached enabled-binding detection, and unsupported relay guard are covered. Frontend, full Claude/Gemini runtime, proxies, OAuth refresh, account retries, and concurrency leases stay out of scope.
- Placeholder scan: no TBD/fill-in-later items remain.
- Type consistency: service returns `AccountPoolBindingView`; controller responses use existing `dto.AccountPoolBindingResponse`; relay guard returns `*types.NewAPIError`.
