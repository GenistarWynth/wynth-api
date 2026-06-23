# Account Pool Phase 2F Account Retry Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add the first account-level retry loop for OpenAI chat completions and Responses account-pool channels, without changing outer channel retry behavior.

**Architecture:** The service layer remains responsible for selecting accounts, storing selected runtime metadata, tracking attempted account IDs, and releasing leases. The relay layer owns retry policy for a single selected channel: if an account-pool attempt fails before any downstream output and the binding still has `AccountRetryTimes` budget, it releases the current lease, restores the channel-mapped request baseline, selects another untried account, and retries inside the same local channel. Selection errors and pool exhaustion stay retryable 503s for the existing outer channel retry loop.

**Tech Stack:** Go 1.22+, Gin context, existing GORM account-pool models, existing `types.NewAPIError`, existing relay OpenAI/Responses handlers, testify tests.

---

## Scope

Implement only Phase 2F:

- account-pool binding retry budget exposure at runtime;
- stale selected-account metadata cleanup before each runtime selection;
- relay helper for account-level retry classification and loop control;
- Text/Responses handler integration;
- focused service and relay tests.

Do not implement OAuth refresh, proxy injection, account DB state updates, metrics, distributed leases, session stickiness, or Claude/Anthropic account pools in this phase.

## Files

- Modify: `service/account_pool_runtime.go`
  - Store selected binding `AccountRetryTimes`.
  - Clear stale selected runtime metadata before every selection/no-op.
  - Add `GetSelectedAccountPoolAccountRetryTimes`.
- Modify: `service/account_pool_scheduler.go`
  - Include `AccountRetryTimes` in `AccountPoolSelectionResult`.
- Modify: `service/account_pool_runtime_test.go`
  - Assert retry budget storage and stale selection cleanup.
- Modify: `relay/account_pool_runtime.go`
  - Add account-pool retry loop helper.
  - Add retryability classifier.
  - Restore relay/request baseline between attempts.
- Modify: `relay/compatible_handler.go`
  - Split existing post-model-mapping body into `textHelperWithRuntimeSelected`.
  - Run it through the account-pool runtime attempt wrapper.
- Modify: `relay/responses_handler.go`
  - Split existing post-model-mapping body into `responsesHelperWithRuntimeSelected`.
  - Run it through the account-pool runtime attempt wrapper.
- Modify: `relay/account_pool_runtime_test.go`
  - Assert retry, no-retry, pool exhaustion, and model reset behavior.

## Task 1: Runtime Metadata Contract

**Files:**
- Modify: `service/account_pool_scheduler.go`
- Modify: `service/account_pool_runtime.go`
- Modify: `service/account_pool_runtime_test.go`

- [ ] **Step 1: Write failing service tests**

Add tests to `service/account_pool_runtime_test.go`:

```go
func TestAccountPoolRuntimeStoresBindingRetryTimes(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	ctx := newAccountPoolRuntimeTestContext()
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)
	channel := createAccountPoolServiceTestChannel(t, common.ChannelStatusManuallyDisabled)
	createEnabledAccountPoolSchedulerBindingWithRetryTimes(t, pool.Id, channel.Id, AccountPoolAccountFilterConfig{}, AccountPoolModelPolicy{}, 2)
	account := createAccountPoolSchedulerAccount(t, service, pool.Id, AccountPoolAccountCreateParams{
		Name: "retry-budget-account",
		Credential: AccountPoolCredentialConfig{
			Type:   AccountPoolCredentialTypeAPIKey,
			APIKey: "sk-retry-budget",
		},
	})
	info := newAccountPoolRuntimeTestRelayInfo(channel.Id, "client-gpt-5", "gpt-5")
	request := &dto.GeneralOpenAIRequest{Model: "gpt-5"}

	err := ApplyAccountPoolRuntimeSelection(ctx, info, request)

	require.NoError(t, err)
	assert.Equal(t, account.Id, GetSelectedAccountPoolAccountID(ctx))
	assert.Equal(t, 2, GetSelectedAccountPoolAccountRetryTimes(ctx))
}

func TestAccountPoolRuntimeClearsStaleSelectedMetadataWhenNoBindingApplies(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	ctx := newAccountPoolRuntimeTestContext()
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)
	boundChannel := createAccountPoolServiceTestChannel(t, common.ChannelStatusManuallyDisabled)
	createEnabledAccountPoolSchedulerBindingWithRetryTimes(t, pool.Id, boundChannel.Id, AccountPoolAccountFilterConfig{}, AccountPoolModelPolicy{}, 1)
	selected := createAccountPoolSchedulerAccount(t, service, pool.Id, AccountPoolAccountCreateParams{
		Name: "stale-selected",
		Credential: AccountPoolCredentialConfig{
			Type:   AccountPoolCredentialTypeAPIKey,
			APIKey: "sk-stale",
		},
	})
	boundInfo := newAccountPoolRuntimeTestRelayInfo(boundChannel.Id, "client-gpt-5", "gpt-5")
	boundRequest := &dto.GeneralOpenAIRequest{Model: "gpt-5"}
	require.NoError(t, ApplyAccountPoolRuntimeSelection(ctx, boundInfo, boundRequest))
	require.Equal(t, selected.Id, GetSelectedAccountPoolAccountID(ctx))
	require.Equal(t, 1, GetSelectedAccountPoolAccountRetryTimes(ctx))
	ReleaseAccountPoolRuntimeSelection(ctx)

	unboundChannel := createAccountPoolServiceTestChannel(t, common.ChannelStatusManuallyDisabled)
	unboundInfo := newAccountPoolRuntimeTestRelayInfo(unboundChannel.Id, "client-gpt-5", "gpt-5")
	unboundRequest := &dto.GeneralOpenAIRequest{Model: "gpt-5"}

	err := ApplyAccountPoolRuntimeSelection(ctx, unboundInfo, unboundRequest)

	require.NoError(t, err)
	assert.Zero(t, GetSelectedAccountPoolAccountID(ctx))
	assert.Zero(t, GetSelectedAccountPoolAccountRetryTimes(ctx))
	assert.Contains(t, GetAccountPoolAttemptedAccountIDs(ctx), selected.Id)
}
```

Add a test helper near existing scheduler-binding helpers:

```go
func createEnabledAccountPoolSchedulerBindingWithRetryTimes(
	t *testing.T,
	poolID int,
	channelID int,
	filter AccountPoolAccountFilterConfig,
	modelPolicy AccountPoolModelPolicy,
	accountRetryTimes int,
) model.AccountPoolChannelBinding {
	t.Helper()
	binding := createEnabledAccountPoolSchedulerBinding(t, poolID, channelID, filter, modelPolicy)
	require.NoError(t, model.DB.Model(&model.AccountPoolChannelBinding{Id: binding.Id}).Update("account_retry_times", accountRetryTimes).Error)
	require.NoError(t, model.DB.First(&binding, binding.Id).Error)
	return binding
}
```

- [ ] **Step 2: Run service tests and verify RED**

Run:

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./service -run "TestAccountPoolRuntimeStoresBindingRetryTimes|TestAccountPoolRuntimeClearsStaleSelectedMetadataWhenNoBindingApplies" -count=1
```

Expected: fail because `GetSelectedAccountPoolAccountRetryTimes` does not exist and selection result does not expose retry budget.

- [ ] **Step 3: Implement minimal service runtime metadata**

In `service/account_pool_scheduler.go`:

```go
type AccountPoolSelectionResult struct {
	PoolID            int
	BindingID         int
	AccountID         int
	AccountName       string
	MaxConcurrency    int
	AccountRetryTimes int
	UpstreamModelName string
	Credential        AccountPoolCredentialConfig
	TokenState        AccountPoolTokenState
}
```

Set `AccountRetryTimes: binding.AccountRetryTimes` in `SelectAccountPoolAccount`.

In `service/account_pool_runtime.go`:

```go
const (
	accountPoolAttemptedAccountIDsContextKey = "account_pool_attempted_account_ids"
	accountPoolSelectedPoolIDContextKey      = "account_pool_selected_pool_id"
	accountPoolSelectedBindingIDContextKey   = "account_pool_selected_binding_id"
	accountPoolSelectedAccountIDContextKey   = "account_pool_selected_account_id"
	accountPoolSelectedRetryTimesContextKey  = "account_pool_selected_retry_times"
)
```

At the start of `ApplyAccountPoolRuntimeSelection`, clear stale selected metadata but keep attempted account IDs:

```go
clearSelectedAccountPoolRuntimeSelection(c)
```

After a successful selection:

```go
c.Set(accountPoolSelectedRetryTimesContextKey, selection.AccountRetryTimes)
```

Add:

```go
func GetSelectedAccountPoolAccountRetryTimes(c *gin.Context) int {
	if c == nil {
		return 0
	}
	return c.GetInt(accountPoolSelectedRetryTimesContextKey)
}

func clearSelectedAccountPoolRuntimeSelection(c *gin.Context) {
	if c == nil {
		return
	}
	c.Set(accountPoolSelectedPoolIDContextKey, 0)
	c.Set(accountPoolSelectedBindingIDContextKey, 0)
	c.Set(accountPoolSelectedAccountIDContextKey, 0)
	c.Set(accountPoolSelectedRetryTimesContextKey, 0)
}
```

- [ ] **Step 4: Run service tests and verify GREEN**

Run the same `go test ./service -run ...` command.

Expected: pass.

## Task 2: Relay Retry Helper

**Files:**
- Modify: `relay/account_pool_runtime.go`
- Modify: `relay/account_pool_runtime_test.go`

- [ ] **Step 1: Write failing relay helper tests**

Add tests to `relay/account_pool_runtime_test.go`:

```go
func TestAccountPoolRuntimeAttemptsRetryAnotherAccountBeforeResponse(t *testing.T) {
	setupAccountPoolRelayTestDB(t)
	ctx := newAccountPoolRelayTestContext("/v1/chat/completions")
	pool := createAccountPoolRelayTestPool(t)
	channel := createAccountPoolRelayTestChannel(t)
	createAccountPoolRelayTestEnabledBindingWithRetryTimes(t, pool.Id, channel.Id, 1)
	first := createAccountPoolRelayTestAccount(t, pool.Id, service.AccountPoolAccountCreateParams{
		Name:     "first",
		Priority: 100,
		Credential: service.AccountPoolCredentialConfig{
			Type:   service.AccountPoolCredentialTypeAPIKey,
			APIKey: "sk-first",
		},
	})
	second := createAccountPoolRelayTestAccount(t, pool.Id, service.AccountPoolAccountCreateParams{
		Name:     "second",
		Priority: 50,
		Credential: service.AccountPoolCredentialConfig{
			Type:   service.AccountPoolCredentialTypeAPIKey,
			APIKey: "sk-second",
		},
	})
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
			return types.NewErrorWithStatusCode(errors.New("first account failed"), types.ErrorCodeBadResponseStatusCode, http.StatusInternalServerError)
		}
		assert.Equal(t, "sk-second", info.ApiKey)
		assert.Equal(t, second.Id, service.GetSelectedAccountPoolAccountID(ctx))
		return nil
	})

	require.Nil(t, newAPIError)
	assert.Equal(t, []int{first.Id, second.Id}, selected)
}

func TestAccountPoolRuntimeAttemptsDoNotRetrySkipRetryError(t *testing.T) {
	setupAccountPoolRelayTestDB(t)
	ctx := newAccountPoolRelayTestContext("/v1/chat/completions")
	pool := createAccountPoolRelayTestPool(t)
	channel := createAccountPoolRelayTestChannel(t)
	createAccountPoolRelayTestEnabledBindingWithRetryTimes(t, pool.Id, channel.Id, 1)
	createAccountPoolRelayTestAccount(t, pool.Id, service.AccountPoolAccountCreateParams{Name: "only"})
	info := newAccountPoolRelayTestInfo(channel.Id, "client-gpt-5", "gpt-5")
	baseRequest := &dto.GeneralOpenAIRequest{Model: "gpt-5"}
	attempts := 0

	newAPIError := runAccountPoolRuntimeAttempts(ctx, info, func() (dto.Request, *types.NewAPIError) {
		request, err := common.DeepCopy(baseRequest)
		if err != nil {
			return nil, types.NewError(err, types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
		}
		return request, nil
	}, func(request dto.Request) *types.NewAPIError {
		attempts++
		return types.NewErrorWithStatusCode(errors.New("bad request"), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	})

	require.NotNil(t, newAPIError)
	assert.Equal(t, 1, attempts)
	assert.True(t, types.IsSkipRetryError(newAPIError))
}

func TestAccountPoolRuntimeAttemptsReturnPoolExhaustionWhenRetryBudgetHasNoCandidate(t *testing.T) {
	setupAccountPoolRelayTestDB(t)
	ctx := newAccountPoolRelayTestContext("/v1/chat/completions")
	pool := createAccountPoolRelayTestPool(t)
	channel := createAccountPoolRelayTestChannel(t)
	createAccountPoolRelayTestEnabledBindingWithRetryTimes(t, pool.Id, channel.Id, 1)
	createAccountPoolRelayTestAccount(t, pool.Id, service.AccountPoolAccountCreateParams{Name: "only"})
	info := newAccountPoolRelayTestInfo(channel.Id, "client-gpt-5", "gpt-5")
	baseRequest := &dto.GeneralOpenAIRequest{Model: "gpt-5"}
	attempts := 0

	newAPIError := runAccountPoolRuntimeAttempts(ctx, info, func() (dto.Request, *types.NewAPIError) {
		request, err := common.DeepCopy(baseRequest)
		if err != nil {
			return nil, types.NewError(err, types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
		}
		return request, nil
	}, func(request dto.Request) *types.NewAPIError {
		attempts++
		return types.NewErrorWithStatusCode(errors.New("single account failed"), types.ErrorCodeBadResponseStatusCode, http.StatusInternalServerError)
	})

	require.NotNil(t, newAPIError)
	require.ErrorIs(t, newAPIError, service.ErrAccountPoolNoSchedulableAccount)
	assert.Equal(t, 1, attempts)
	assert.Equal(t, http.StatusServiceUnavailable, newAPIError.StatusCode)
	assert.Equal(t, types.ErrorCodeGetChannelFailed, newAPIError.GetErrorCode())
}

func TestAccountPoolRuntimeAttemptsResetMappedModelForEachRetry(t *testing.T) {
	setupAccountPoolRelayTestDB(t)
	ctx := newAccountPoolRelayTestContext("/v1/chat/completions")
	pool := createAccountPoolRelayTestPool(t)
	channel := createAccountPoolRelayTestChannel(t)
	createAccountPoolRelayTestEnabledBindingWithRetryTimes(t, pool.Id, channel.Id, 1)
	createAccountPoolRelayTestAccount(t, pool.Id, service.AccountPoolAccountCreateParams{
		Name:            "first",
		Priority:        100,
		SupportedModels: []string{"channel-gpt-5"},
		ModelMapping: map[string]string{
			"channel-gpt-5": "account-one-model",
		},
	})
	createAccountPoolRelayTestAccount(t, pool.Id, service.AccountPoolAccountCreateParams{
		Name:            "second",
		Priority:        50,
		SupportedModels: []string{"channel-gpt-5"},
		ModelMapping: map[string]string{
			"channel-gpt-5": "account-two-model",
		},
	})
	info := newAccountPoolRelayTestInfo(channel.Id, "client-gpt-5", "channel-gpt-5")
	baseRequest := &dto.GeneralOpenAIRequest{Model: "channel-gpt-5"}
	models := make([]string, 0, 2)

	newAPIError := runAccountPoolRuntimeAttempts(ctx, info, func() (dto.Request, *types.NewAPIError) {
		request, err := common.DeepCopy(baseRequest)
		if err != nil {
			return nil, types.NewError(err, types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
		}
		return request, nil
	}, func(request dto.Request) *types.NewAPIError {
		models = append(models, request.GetModelName())
		if len(models) == 1 {
			return types.NewErrorWithStatusCode(errors.New("mapped model account failed"), types.ErrorCodeBadResponseStatusCode, http.StatusInternalServerError)
		}
		return nil
	})

	require.Nil(t, newAPIError)
	assert.Equal(t, []string{"account-one-model", "account-two-model"}, models)
}
```

Add helper:

```go
func createAccountPoolRelayTestEnabledBindingWithRetryTimes(t *testing.T, poolID int, channelID int, accountRetryTimes int) model.AccountPoolChannelBinding {
	t.Helper()
	bindingView, err := service.AccountPoolService{}.CreateBinding(service.AccountPoolBindingCreateParams{
		PoolID:            poolID,
		ChannelID:         channelID,
		AccountRetryTimes: accountRetryTimes,
	})
	require.NoError(t, err)
	_, err = service.AccountPoolService{}.ActivateBinding(poolID, bindingView.Id)
	require.NoError(t, err)
	var binding model.AccountPoolChannelBinding
	require.NoError(t, model.DB.First(&binding, bindingView.Id).Error)
	return binding
}
```

- [ ] **Step 2: Run relay helper tests and verify RED**

Run:

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./relay -run "TestAccountPoolRuntimeAttempts" -count=1
```

Expected: fail because `runAccountPoolRuntimeAttempts` and retry-budget getter do not exist.

- [ ] **Step 3: Implement minimal relay helper**

In `relay/account_pool_runtime.go`, add:

```go
type accountPoolRuntimeRequestFactory func() (dto.Request, *types.NewAPIError)
type accountPoolRuntimeAttemptFunc func(dto.Request) *types.NewAPIError

type accountPoolRuntimeRelaySnapshot struct {
	apiKey                  string
	upstreamModelName       string
	isStream                bool
	upstreamRequestBodySize int64
	requestConversionChain  []types.RelayFormat
	finalRequestRelayFormat types.RelayFormat
}
```

Add snapshot/restore helpers and `runAccountPoolRuntimeAttempts`:

```go
func snapshotAccountPoolRuntimeRelay(info *relaycommon.RelayInfo) accountPoolRuntimeRelaySnapshot {
	snapshot := accountPoolRuntimeRelaySnapshot{}
	if info == nil {
		return snapshot
	}
	snapshot.apiKey = info.ApiKey
	snapshot.upstreamModelName = info.UpstreamModelName
	snapshot.isStream = info.IsStream
	snapshot.upstreamRequestBodySize = info.UpstreamRequestBodySize
	snapshot.finalRequestRelayFormat = info.FinalRequestRelayFormat
	if len(info.RequestConversionChain) > 0 {
		snapshot.requestConversionChain = append([]types.RelayFormat(nil), info.RequestConversionChain...)
	}
	return snapshot
}

func restoreAccountPoolRuntimeRelay(info *relaycommon.RelayInfo, snapshot accountPoolRuntimeRelaySnapshot) {
	if info == nil {
		return
	}
	info.ApiKey = snapshot.apiKey
	info.UpstreamModelName = snapshot.upstreamModelName
	info.IsStream = snapshot.isStream
	info.UpstreamRequestBodySize = snapshot.upstreamRequestBodySize
	info.FinalRequestRelayFormat = snapshot.finalRequestRelayFormat
	info.RequestConversionChain = append([]types.RelayFormat(nil), snapshot.requestConversionChain...)
}

func runAccountPoolRuntimeAttempts(
	c *gin.Context,
	info *relaycommon.RelayInfo,
	requestFactory accountPoolRuntimeRequestFactory,
	attempt accountPoolRuntimeAttemptFunc,
) *types.NewAPIError {
	if requestFactory == nil || attempt == nil {
		return nil
	}
	snapshot := snapshotAccountPoolRuntimeRelay(info)
	for attemptIndex := 0; ; attemptIndex++ {
		restoreAccountPoolRuntimeRelay(info, snapshot)
		request, newAPIError := requestFactory()
		if newAPIError != nil {
			return newAPIError
		}
		if newAPIError := applyAccountPoolRuntimeSelection(c, info, request); newAPIError != nil {
			return newAPIError
		}
		selectedAccountID := service.GetSelectedAccountPoolAccountID(c)
		accountRetryTimes := service.GetSelectedAccountPoolAccountRetryTimes(c)

		newAPIError = attempt(request)
		service.ReleaseAccountPoolRuntimeSelection(c)
		if newAPIError == nil {
			return nil
		}
		if !shouldRetryAccountPoolRuntimeAttempt(info, selectedAccountID, accountRetryTimes, attemptIndex, newAPIError) {
			return newAPIError
		}
	}
}
```

Add classifier:

```go
func shouldRetryAccountPoolRuntimeAttempt(info *relaycommon.RelayInfo, selectedAccountID int, accountRetryTimes int, attemptIndex int, err *types.NewAPIError) bool {
	if err == nil || selectedAccountID <= 0 || accountRetryTimes <= 0 || attemptIndex >= accountRetryTimes {
		return false
	}
	if types.IsSkipRetryError(err) {
		return false
	}
	if info != nil && info.HasSendResponse() {
		return false
	}
	if err.GetErrorCode() == types.ErrorCodeDoRequestFailed {
		return true
	}
	statusCode := err.StatusCode
	if statusCode < 100 || statusCode > 599 {
		return true
	}
	switch statusCode {
	case http.StatusRequestTimeout, http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests:
		return true
	}
	return statusCode >= 500
}
```

- [ ] **Step 4: Run relay helper tests and verify GREEN**

Run the same `go test ./relay -run "TestAccountPoolRuntimeAttempts" -count=1` command.

Expected: pass.

## Task 3: Text and Responses Integration

**Files:**
- Modify: `relay/compatible_handler.go`
- Modify: `relay/responses_handler.go`
- Modify: `relay/account_pool_runtime_test.go`

- [ ] **Step 1: Add integration assertions to existing tests**

Extend existing relay tests so `TextHelper` and `ResponsesHelper` still map account-pool exhaustion to retryable 503 after handler refactor:

```go
assert.False(t, types.IsSkipRetryError(newAPIError))
```

Add one focused helper-level test if needed to ensure unbound channels do not enter account-pool retry when a selected account ID existed from a previous bound channel. The stale cleanup service test should normally cover this.

- [ ] **Step 2: Refactor TextHelper**

In `relay/compatible_handler.go`, keep the initial section through `ModelMappedHelper`, then replace the direct runtime selection/defer and remainder with:

```go
mappedRequest := request
return runAccountPoolRuntimeAttempts(c, info, func() (dto.Request, *types.NewAPIError) {
	attemptRequest, err := common.DeepCopy(mappedRequest)
	if err != nil {
		return nil, types.NewError(fmt.Errorf("failed to copy mapped GeneralOpenAIRequest: %w", err), types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
	}
	return attemptRequest, nil
}, func(attemptRequest dto.Request) *types.NewAPIError {
	textRequest, ok := attemptRequest.(*dto.GeneralOpenAIRequest)
	if !ok {
		return types.NewErrorWithStatusCode(fmt.Errorf("invalid mapped request type, expected dto.GeneralOpenAIRequest, got %T", attemptRequest), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	return textHelperWithRuntimeSelected(c, info, textRequest)
})
```

Move the existing body after runtime selection into:

```go
func textHelperWithRuntimeSelected(c *gin.Context, info *relaycommon.RelayInfo, request *dto.GeneralOpenAIRequest) *types.NewAPIError {
	// existing code from includeUsage through post-consume
}
```

Do not call `service.ReleaseAccountPoolRuntimeSelection` inside `textHelperWithRuntimeSelected`; the wrapper owns release.

- [ ] **Step 3: Refactor ResponsesHelper**

In `relay/responses_handler.go`, keep the initial section through `ModelMappedHelper`, then replace the direct runtime selection/defer and remainder with:

```go
mappedRequest := request
return runAccountPoolRuntimeAttempts(c, info, func() (dto.Request, *types.NewAPIError) {
	attemptRequest, err := common.DeepCopy(mappedRequest)
	if err != nil {
		return nil, types.NewError(fmt.Errorf("failed to copy mapped OpenAIResponsesRequest: %w", err), types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
	}
	return attemptRequest, nil
}, func(attemptRequest dto.Request) *types.NewAPIError {
	responsesRequest, ok := attemptRequest.(*dto.OpenAIResponsesRequest)
	if !ok {
		return types.NewErrorWithStatusCode(fmt.Errorf("invalid mapped request type, expected dto.OpenAIResponsesRequest, got %T", attemptRequest), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	return responsesHelperWithRuntimeSelected(c, info, responsesRequest)
})
```

Move the existing body after runtime selection into:

```go
func responsesHelperWithRuntimeSelected(c *gin.Context, info *relaycommon.RelayInfo, request *dto.OpenAIResponsesRequest) *types.NewAPIError {
	// existing code from adaptor lookup through post-consume
}
```

Do not call `service.ReleaseAccountPoolRuntimeSelection` inside `responsesHelperWithRuntimeSelected`; the wrapper owns release.

- [ ] **Step 4: Run focused relay tests**

Run:

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./relay -run "TestAccountPoolRelay|TestAccountPoolRuntimeAttempts" -count=1
```

Expected: pass.

## Task 4: Review and Verification

**Files:**
- Commit all modified files after verification.

- [ ] **Step 1: Format Go files**

Run:

```powershell
gofmt -w service/account_pool_runtime.go service/account_pool_scheduler.go service/account_pool_runtime_test.go relay/account_pool_runtime.go relay/account_pool_runtime_test.go relay/compatible_handler.go relay/responses_handler.go
```

- [ ] **Step 2: Run package tests**

Run:

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./service ./relay ./controller -count=1
```

Expected: pass.

- [ ] **Step 3: Request Claude implementation review**

Run a Sonnet review first to save quota:

```powershell
claude -p --model sonnet --effort medium --output-format json --disable-slash-commands --allowedTools Read,Grep,Glob --disallowedTools Bash,Edit,Write "Review the account-pool Phase 2F implementation on branch sub2api-account-pool. Focus on whether account-level retries preserve outer channel retry semantics, release account leases before retrying, avoid retry after downstream output, avoid stale selected account metadata across channels, and reset request/model state between attempts. Return blockers first."
```

If default Claude quota is unavailable, rerun with:

```powershell
claude -p --model sonnet --effort medium --settings ~/.claude/settings.wynth.json --output-format json --disable-slash-commands --allowedTools Read,Grep,Glob --disallowedTools Bash,Edit,Write "Review the account-pool Phase 2F implementation on branch sub2api-account-pool. Focus on whether account-level retries preserve outer channel retry semantics, release account leases before retrying, avoid retry after downstream output, avoid stale selected account metadata across channels, and reset request/model state between attempts. Return blockers first."
```

- [ ] **Step 4: Evaluate Claude feedback**

Fix Critical and Important findings only if they are technically correct for this codebase. Re-run the focused tests after each fix.

- [ ] **Step 5: Commit**

Run:

```powershell
git status --short
git add service/account_pool_runtime.go service/account_pool_scheduler.go service/account_pool_runtime_test.go relay/account_pool_runtime.go relay/account_pool_runtime_test.go relay/compatible_handler.go relay/responses_handler.go docs/superpowers/plans/2026-06-23-sub2api-account-pool-phase2f-account-retry.md
git commit -m "feat: add account pool in-channel retry"
```

## Self-Review

- Spec coverage: Covers request-level failed account exclusion, retry before response output only, account retry budget, pool exhaustion to outer retry, and lease release before the next account selection.
- Scope control: Does not implement account state mutation, OAuth refresh, proxy selection, metrics, distributed coordination, or non-OpenAI account pools.
- Test quality: Tests cover behavior contracts, not coverage-only branches.
- Cross-DB: New DB interaction reuses existing GORM binding load and does not add raw SQL.
- JSON rule: No new direct `encoding/json` calls are introduced.
