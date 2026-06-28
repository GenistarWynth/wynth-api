# Account Pool Model Capability Detection Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add admin-triggered account-pool model capability detection so account `SupportedModels` and optional `ModelMapping` can be discovered, previewed, and applied without affecting production account health or usage metrics.

**Architecture:** Implement detection as an isolated service layer that resolves a bound local channel for upstream shape, swaps in the account runtime credential, and writes only account capability fields/metadata when `apply=true`. Controller and UI call the service through explicit admin endpoints; relay scheduling remains unchanged and only consumes the existing account fields.

**Tech Stack:** Go 1.22+, Gin, GORM, `common` JSON wrappers, `net/http` test servers, React 19, TypeScript, TanStack Query, Bun.

---

## File Structure

- Modify `model/account_pool.go`
  - Add optional capability-check metadata columns to `AccountPoolAccount`.
- Modify `service/account_pool_service.go`
  - Expose metadata in `AccountPoolAccountView`.
- Create `service/account_pool_capability.go`
  - Own request/result types, channel/account resolution, `/v1/models` detection, probe detection, model merge/replace, error classification, and sanitized persistence.
- Create `service/account_pool_capability_test.go`
  - Cover service behavior with deterministic SQLite fixtures and `httptest.Server`.
- Modify `dto/account_pool.go`
  - Add capability detection request/response DTOs and metadata fields in account response.
- Modify `controller/account_pool.go`
  - Add single-account and pool-level detection handlers.
- Modify `controller/account_pool_test.go`
  - Add API route tests for dry-run/apply and pool partial failure.
- Modify `router/api-router.go`
  - Register the two admin endpoints.
- Modify `web/default/src/features/account-pools/types.ts`
  - Add detection request/result types and account metadata fields.
- Modify `web/default/src/features/account-pools/api.ts`
  - Add detection API helpers and query invalidation key usage.
- Modify `web/default/src/features/account-pools/index.tsx`
  - Add row action, pool-level action, and a compact detection dialog.
- Modify `web/default/src/i18n/locales/{en,zh,fr,ru,ja,vi}.json`
  - Add frontend translation keys through `bun run i18n:sync`.

---

### Task 1: Persist And Expose Capability Metadata

**Files:**
- Modify: `model/account_pool.go`
- Modify: `service/account_pool_service.go`
- Modify: `dto/account_pool.go`
- Modify: `web/default/src/features/account-pools/types.ts`
- Test: `service/account_pool_service_test.go`

- [ ] **Step 1: Write the failing service view test**

Append this test to `service/account_pool_service_test.go`:

```go
func TestAccountPoolServiceAccountViewIncludesCapabilityMetadata(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)
	account, err := service.CreateAccount(AccountPoolAccountCreateParams{
		PoolID: pool.Id,
		Name:   "capability-metadata",
		Credential: AccountPoolCredentialConfig{
			Type:   AccountPoolCredentialTypeAPIKey,
			APIKey: "sk-test",
		},
		SupportedModels: []string{"gpt-5"},
	})
	require.NoError(t, err)

	require.NoError(t, model.DB.Model(&model.AccountPoolAccount{}).
		Where("id = ?", account.Id).
		Updates(map[string]any{
			"last_capability_check_at":     int64(1234),
			"last_capability_check_status": "success",
			"last_capability_check_error":  "",
			"last_capability_check_models": `["gpt-5","gpt-5-mini"]`,
		}).Error)

	accounts, err := service.ListAccounts(pool.Id)
	require.NoError(t, err)
	require.Len(t, accounts, 1)
	assert.Equal(t, int64(1234), accounts[0].LastCapabilityCheckAt)
	assert.Equal(t, "success", accounts[0].LastCapabilityCheckStatus)
	assert.Equal(t, []string{"gpt-5", "gpt-5-mini"}, accounts[0].LastCapabilityCheckModels)
}
```

- [ ] **Step 2: Run the focused failing test**

Run:

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./service -run TestAccountPoolServiceAccountViewIncludesCapabilityMetadata -count=1
```

Expected: fail because the metadata fields do not exist on `AccountPoolAccountView`.

- [ ] **Step 3: Add model/view/DTO fields**

In `model/account_pool.go`, add these fields after `LastError`:

```go
	LastCapabilityCheckAt     int64  `json:"last_capability_check_at" gorm:"bigint;index"`
	LastCapabilityCheckStatus string `json:"last_capability_check_status" gorm:"type:varchar(32)"`
	LastCapabilityCheckError  string `json:"last_capability_check_error" gorm:"type:varchar(1024)"`
	LastCapabilityCheckModels string `json:"last_capability_check_models" gorm:"type:text"`
```

In `service/account_pool_service.go`, add these fields to `AccountPoolAccountView`:

```go
	LastCapabilityCheckAt     int64    `json:"last_capability_check_at"`
	LastCapabilityCheckStatus string   `json:"last_capability_check_status"`
	LastCapabilityCheckError  string   `json:"last_capability_check_error"`
	LastCapabilityCheckModels []string `json:"last_capability_check_models"`
```

Then update `buildAccountPoolAccountView`:

```go
	var lastCapabilityCheckModels []string
	if strings.TrimSpace(account.LastCapabilityCheckModels) != "" {
		if err := common.UnmarshalJsonStr(account.LastCapabilityCheckModels, &lastCapabilityCheckModels); err != nil {
			return AccountPoolAccountView{}, err
		}
	}
```

And populate the returned view:

```go
		LastCapabilityCheckAt:     account.LastCapabilityCheckAt,
		LastCapabilityCheckStatus: account.LastCapabilityCheckStatus,
		LastCapabilityCheckError:  account.LastCapabilityCheckError,
		LastCapabilityCheckModels: lastCapabilityCheckModels,
```

In `dto/account_pool.go`, add these fields to `AccountPoolAccountResponse`:

```go
	LastCapabilityCheckAt     int64    `json:"last_capability_check_at"`
	LastCapabilityCheckStatus string   `json:"last_capability_check_status"`
	LastCapabilityCheckError  string   `json:"last_capability_check_error"`
	LastCapabilityCheckModels []string `json:"last_capability_check_models"`
```

In `controller/account_pool.go`, add the same fields in `accountPoolAccountResponse`.

In `web/default/src/features/account-pools/types.ts`, add these fields to `AccountPoolAccount`:

```ts
  last_capability_check_at: number
  last_capability_check_status: string
  last_capability_check_error: string
  last_capability_check_models: string[]
```

- [ ] **Step 4: Run the focused test**

Run:

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./service -run TestAccountPoolServiceAccountViewIncludesCapabilityMetadata -count=1
```

Expected: pass.

- [ ] **Step 5: Commit**

```powershell
git -C E:\Documents\Projects\wynth-api\.worktrees\sub2api-account-pool add model/account_pool.go service/account_pool_service.go service/account_pool_service_test.go dto/account_pool.go controller/account_pool.go web/default/src/features/account-pools/types.ts
git -C E:\Documents\Projects\wynth-api\.worktrees\sub2api-account-pool commit -m "feat: expose account pool capability metadata"
```

---

### Task 2: Implement `/v1/models` Capability Detection Service

**Files:**
- Create: `service/account_pool_capability.go`
- Create: `service/account_pool_capability_test.go`

- [ ] **Step 1: Write failing tests**

Create `service/account_pool_capability_test.go` with tests named:

```go
func TestAccountPoolCapabilityDetectModelsEndpointDryRunDoesNotWrite(t *testing.T)
func TestAccountPoolCapabilityDetectModelsEndpointApplyMergeAndReplace(t *testing.T)
func TestAccountPoolCapabilityDetectRequiresChannelWhenPoolHasMultipleBindings(t *testing.T)
func TestAccountPoolCapabilityDetectSanitizesAuthErrorsAndDoesNotDisableAccount(t *testing.T)
```

Use the existing `setupAccountPoolServiceTestDB`, `createAccountPoolServiceTestPool`, and account creation helpers. Each test should create a disabled OpenAI channel bound to the pool so detection can resolve base URL/channel type. Use `httptest.Server` returning:

```json
{"data":[{"id":"gpt-5"},{"id":"gpt-5-mini"},{"id":""},{"id":"gpt-5"}]}
```

Assert these contracts:

```go
assert.Equal(t, []string{"gpt-5-mini"}, result.DetectedModels) // when CandidateModels is []string{"gpt-5-mini", "missing"}
assert.Equal(t, []string{"existing", "gpt-5-mini"}, applied.SupportedModels) // merge
assert.Equal(t, []string{"gpt-5-mini"}, replaced.SupportedModels) // replace
assert.Zero(t, stored.TempDisabledUntil)
assert.Zero(t, stored.RateLimitedUntil)
require.NotEmpty(t, result.Errors)
assert.NotContains(t, result.Errors[0], "sk-secret")
```

- [ ] **Step 2: Run failing tests**

Run:

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./service -run "TestAccountPoolCapabilityDetect" -count=1
```

Expected: fail because the service does not exist.

- [ ] **Step 3: Add service types and normalization**

Create `service/account_pool_capability.go` with:

```go
package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
)

const (
	AccountPoolCapabilityModeAuto           = "auto"
	AccountPoolCapabilityModeModelsEndpoint = "models_endpoint"
	AccountPoolCapabilityModeProbeModels    = "probe_models"

	AccountPoolCapabilityStatusSuccess      = "success"
	AccountPoolCapabilityStatusPartial      = "partial"
	AccountPoolCapabilityStatusUnsupported  = "unsupported"
	AccountPoolCapabilityStatusAuthError    = "auth_error"
	AccountPoolCapabilityStatusNetworkError = "network_error"
	AccountPoolCapabilityStatusUpstreamError = "upstream_error"
	AccountPoolCapabilityStatusConfigError  = "config_error"
)

type AccountPoolCapabilityDetectRequest struct {
	PoolID         int
	AccountID      int
	AccountIDs     []int
	ChannelID      int
	Mode           string
	CandidateModels []string
	Apply          bool
	Merge          bool
	ModelMapping   map[string]string
	TimeoutSeconds int
}

type AccountPoolCapabilityDetectResult struct {
	AccountID       int
	Status          string
	Mode            string
	DetectedModels  []string
	AppliedModels   []string
	ModelMapping    map[string]string
	Errors          []string
}

type AccountPoolCapabilityPoolResult struct {
	Total    int
	Succeeded int
	Failed   int
	Results  []AccountPoolCapabilityDetectResult
}
```

Add helpers:

```go
func normalizeAccountPoolCapabilityMode(mode string) string
func normalizeAccountPoolCapabilityTimeout(seconds int) time.Duration
func normalizeAccountPoolCapabilityModels(models []string) []string
func intersectAccountPoolCapabilityModels(detected []string, candidates []string) []string
func mergeAccountPoolCapabilityModels(existing []string, detected []string) []string
func classifyAccountPoolCapabilityHTTPStatus(status int) string
```

- [ ] **Step 4: Implement channel/account resolution**

Add:

```go
func (s AccountPoolService) DetectAccountCapability(ctx context.Context, req AccountPoolCapabilityDetectRequest) (AccountPoolCapabilityDetectResult, error)
func (s AccountPoolService) DetectPoolCapabilities(ctx context.Context, req AccountPoolCapabilityDetectRequest) (AccountPoolCapabilityPoolResult, error)
```

Resolution rules:

- `PoolID` and `AccountID` are required for single-account detection.
- `ChannelID` is optional only when the pool has exactly one non-deleted binding.
- Account must belong to the pool and not be deleted.
- Channel must be bound to the pool and not deleted.
- Use `ResolveAccountPoolRuntimeProxyURL(account.ProxyID, pool.DefaultProxyID)` first; if empty, use `channel.GetSetting().Proxy`.
- Use `ResolveAccountPoolRuntimeCredential` with `SkipFailureRecord: true`.

- [ ] **Step 5: Implement `/v1/models` fetch path**

For OpenAI-compatible channels, derive fetch safety options from the bound channel first:

```go
options, err := FetchChannelUpstreamModelIDsOptionsForGeneratedSource(channel.GetOtherSettings())
if err != nil {
	return result, err
}
```

Then call an account-pool specific body fetch helper that accepts the normalized timeout:

```go
url := buildFetchModelsURL(channel.Type, baseURL)
headers, err := BuildFetchModelsHeaders(&channel, runtimeCredential)
body, err := fetchAccountPoolCapabilityResponseBody(ctx, http.MethodGet, url, proxyURL, headers, options, timeout)
```

`fetchAccountPoolCapabilityResponseBody` must reuse `validateFetchModelsURL`, `NewProxyHttpClient`, `fetchModelsHTTPClientWithOptions`, `io.LimitReader`, and the request timeout from `timeout_seconds`. This preserves the same private-IP behavior as generated upstream-source channels without making account-pool detection globally bypass SSRF protection.

Decode with `common.Unmarshal` into:

```go
type accountPoolCapabilityModelsResponse struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}
```

Then normalize, dedupe, and candidate-filter.

- [ ] **Step 6: Implement apply/dry-run writes**

When `Apply=false`, return `DetectedModels` and leave the account unchanged.

When `Apply=true`, write only:

```go
updates := map[string]any{
	"supported_models":               supportedModelsJSON,
	"model_mapping":                  modelMappingJSON,
	"last_capability_check_at":       common.GetTimestamp(),
	"last_capability_check_status":   result.Status,
	"last_capability_check_error":    "",
	"last_capability_check_models":   detectedModelsJSON,
}
```

On detection failure, write only capability metadata and `last_error` with sanitized text. Do not update:

- `status`
- `rate_limited_until`
- `temp_disabled_until`
- `temp_disabled_reason`
- usage token counters
- latency counters
- success/failure counters

- [ ] **Step 7: Run service tests**

Run:

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./service -run "TestAccountPoolCapabilityDetect|TestAccountPoolServiceAccountViewIncludesCapabilityMetadata" -count=1
```

Expected: pass.

- [ ] **Step 8: Commit**

```powershell
git -C E:\Documents\Projects\wynth-api\.worktrees\sub2api-account-pool add service/account_pool_capability.go service/account_pool_capability_test.go
git -C E:\Documents\Projects\wynth-api\.worktrees\sub2api-account-pool commit -m "feat: detect account pool models from upstream"
```

---

### Task 3: Add Probe Mode

**Files:**
- Modify: `service/account_pool_capability.go`
- Modify: `service/account_pool_capability_test.go`

- [ ] **Step 1: Write failing probe tests**

Add tests:

```go
func TestAccountPoolCapabilityProbeModelsAppliesOnlySupportedCandidates(t *testing.T)
func TestAccountPoolCapabilityProbeRequiresCandidateModels(t *testing.T)
```

The fake upstream should:

- return 200 for `model == "gpt-5"`;
- return 404 with `{"error":{"message":"model not found"}}` for `model == "missing-model"`;
- return 401 for `model == "auth-fails"`.

Assert:

```go
assert.Equal(t, []string{"gpt-5"}, result.DetectedModels)
assert.Equal(t, AccountPoolCapabilityStatusPartial, result.Status)
assert.Contains(t, result.Errors[0], "missing-model")
```

- [ ] **Step 2: Run failing tests**

Run:

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./service -run "TestAccountPoolCapabilityProbe" -count=1
```

Expected: fail because probe mode is not implemented.

- [ ] **Step 3: Implement OpenAI-compatible probe request**

For each candidate model, POST to `baseURL + "/v1/chat/completions"` with:

```json
{
  "model": "candidate",
  "messages": [{"role": "user", "content": "ping"}],
  "max_tokens": 1,
  "stream": false
}
```

Use `common.Marshal` for the body, `http.NewRequestWithContext`, and `BuildFetchModelsHeaders` for auth/header overrides. Reuse the resolved proxy client. Treat status codes:

- 200-299: supported
- 400/404 with model-not-found text: unsupported model, not account failure
- 401/403: auth error for account result
- network error: network error for account result
- other non-2xx: upstream error

Limit response bodies with `io.LimitReader` and sanitize stored/displayed errors.

- [ ] **Step 4: Run probe tests**

Run:

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./service -run "TestAccountPoolCapabilityProbe|TestAccountPoolCapabilityDetect" -count=1
```

Expected: pass.

- [ ] **Step 5: Commit**

```powershell
git -C E:\Documents\Projects\wynth-api\.worktrees\sub2api-account-pool add service/account_pool_capability.go service/account_pool_capability_test.go
git -C E:\Documents\Projects\wynth-api\.worktrees\sub2api-account-pool commit -m "feat: probe account pool model capability"
```

---

### Task 4: Add Admin Controller Endpoints

**Files:**
- Modify: `dto/account_pool.go`
- Modify: `controller/account_pool.go`
- Modify: `controller/account_pool_test.go`
- Modify: `router/api-router.go`

- [ ] **Step 1: Write failing controller tests**

Add tests to `controller/account_pool_test.go`:

```go
func TestAccountPoolAPIDetectAccountCapabilityDryRun(t *testing.T)
func TestAccountPoolAPIDetectPoolCapabilitiesContinuesAfterFailure(t *testing.T)
```

Use `accountPoolAPIRequest` to call:

```text
POST /api/account_pools/{pool_id}/accounts/{account_id}/capabilities/detect
POST /api/account_pools/{pool_id}/capabilities/detect
```

Assert response `success=true`, status/result fields, and account persistence for apply/dry-run.

- [ ] **Step 2: Run failing controller tests**

Run:

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./controller -run "TestAccountPoolAPIDetect" -count=1
```

Expected: fail because routes and handlers do not exist.

- [ ] **Step 3: Add DTOs**

In `dto/account_pool.go` add:

```go
type AccountPoolCapabilityDetectRequest struct {
	Mode            string            `json:"mode"`
	ChannelID       int               `json:"channel_id"`
	AccountIDs      []int             `json:"account_ids"`
	CandidateModels []string          `json:"candidate_models"`
	Apply           bool              `json:"apply"`
	Merge           bool              `json:"merge"`
	ModelMapping    map[string]string `json:"model_mapping"`
	TimeoutSeconds  int               `json:"timeout_seconds"`
}

type AccountPoolCapabilityDetectResult struct {
	AccountID      int               `json:"account_id"`
	Status         string            `json:"status"`
	Mode           string            `json:"mode"`
	DetectedModels []string          `json:"detected_models"`
	AppliedModels  []string          `json:"applied_models"`
	ModelMapping   map[string]string `json:"model_mapping"`
	Errors         []string          `json:"errors"`
}

type AccountPoolCapabilityPoolResult struct {
	Total     int                                 `json:"total"`
	Succeeded int                                 `json:"succeeded"`
	Failed    int                                 `json:"failed"`
	Results   []AccountPoolCapabilityDetectResult `json:"results"`
}
```

- [ ] **Step 4: Add handlers and route registration**

In `controller/account_pool.go`, add:

```go
func DetectAccountPoolAccountCapability(c *gin.Context)
func DetectAccountPoolCapabilities(c *gin.Context)
```

Map DTO to service request, pass `c.Request.Context()`, record audit event:

```go
recordManageAudit(c, "account_pool.capability_detect", map[string]interface{}{
	"pool_id": poolID,
	"account_id": accountID,
	"mode": req.Mode,
	"apply": req.Apply,
})
```

In `router/api-router.go`, register before `PUT("/:id/accounts/:account_id", ...)`:

```go
accountPoolRoute.POST("/:id/accounts/:account_id/capabilities/detect", controller.DetectAccountPoolAccountCapability)
accountPoolRoute.POST("/:id/capabilities/detect", controller.DetectAccountPoolCapabilities)
```

Also add these routes to `accountPoolAPIRouter()` in controller tests.

- [ ] **Step 5: Run controller tests**

Run:

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./controller -run "TestAccountPoolAPIDetect" -count=1
```

Expected: pass.

- [ ] **Step 6: Commit**

```powershell
git -C E:\Documents\Projects\wynth-api\.worktrees\sub2api-account-pool add dto/account_pool.go controller/account_pool.go controller/account_pool_test.go router/api-router.go
git -C E:\Documents\Projects\wynth-api\.worktrees\sub2api-account-pool commit -m "feat: expose account pool capability detection api"
```

---

### Task 5: Add Minimal Frontend Detection UI

**Files:**
- Modify: `web/default/src/features/account-pools/types.ts`
- Modify: `web/default/src/features/account-pools/api.ts`
- Modify: `web/default/src/features/account-pools/index.tsx`
- Modify: `web/default/src/i18n/locales/{en,zh,fr,ru,ja,vi}.json`

- [ ] **Step 1: Add TypeScript types and API helpers**

In `types.ts`, add:

```ts
export type AccountPoolCapabilityMode =
  | 'auto'
  | 'models_endpoint'
  | 'probe_models'
  | string

export type AccountPoolCapabilityDetectRequest = {
  mode: AccountPoolCapabilityMode
  channel_id: number
  account_ids?: number[]
  candidate_models: string[]
  apply: boolean
  merge: boolean
  model_mapping: Record<string, string>
  timeout_seconds: number
}

export type AccountPoolCapabilityDetectResult = {
  account_id: number
  status: string
  mode: string
  detected_models: string[]
  applied_models: string[]
  model_mapping: Record<string, string>
  errors: string[]
}

export type AccountPoolCapabilityPoolResult = {
  total: number
  succeeded: number
  failed: number
  results: AccountPoolCapabilityDetectResult[]
}
```

In `api.ts`, add:

```ts
export async function detectAccountPoolAccountCapability(
  poolID: number,
  accountID: number,
  data: AccountPoolCapabilityDetectRequest
): Promise<ApiResponse<AccountPoolCapabilityDetectResult>> {
  const res = await api.post(
    `/api/account_pools/${poolID}/accounts/${accountID}/capabilities/detect`,
    data,
    accountPoolActionConfig()
  )
  return res.data
}

export async function detectAccountPoolCapabilities(
  poolID: number,
  data: AccountPoolCapabilityDetectRequest
): Promise<ApiResponse<AccountPoolCapabilityPoolResult>> {
  const res = await api.post(
    `/api/account_pools/${poolID}/capabilities/detect`,
    data,
    accountPoolActionConfig()
  )
  return res.data
}
```

- [ ] **Step 2: Add account row action and pool action**

In `index.tsx`:

- add state `detectingAccount` and `capabilityDialogOpen`;
- pass `onDetectAccount` into `AccountSection`;
- add a `ScanSearch` or `Radar` icon row action labeled `Detect Models`;
- add a section button labeled `Batch Detect Models`;
- after success, invalidate `accountPoolsQueryKeys.accounts(selectedPoolID)`.

- [ ] **Step 3: Add compact dialog**

Add a local component `CapabilityDetectDialog` with controls:

- mode select: `auto`, `models_endpoint`, `probe_models`;
- channel select from pool bindings;
- candidate models comma/newline input;
- switches for apply and merge;
- timeout number input;
- submit button.

Show result summary:

```tsx
{result && (
  <div className='text-sm'>
    {t('Detected Models')}: {result.detected_models.length}
  </div>
)}
```

For pool-level result, show total/succeeded/failed and per-account status list.

- [ ] **Step 4: Sync translations**

Run from `web/default`:

```powershell
bun run i18n:sync
```

Then fill at least Chinese translations for new keys:

- `Detect Models` -> `检测模型`
- `Batch Detect Models` -> `批量检测模型`
- `Capability Detection` -> `能力检测`
- `Models Endpoint` -> `模型列表接口`
- `Probe Models` -> `探测模型`
- `Dry Run` -> `仅预览`
- `Apply detected models` -> `应用检测结果`
- `Merge with existing models` -> `合并已有模型`

- [ ] **Step 5: Run frontend checks**

Run:

```powershell
bun run typecheck
bun run build
```

Expected: pass.

- [ ] **Step 6: Commit**

```powershell
git -C E:\Documents\Projects\wynth-api\.worktrees\sub2api-account-pool add web/default/src/features/account-pools/types.ts web/default/src/features/account-pools/api.ts web/default/src/features/account-pools/index.tsx web/default/src/i18n/locales
git -C E:\Documents\Projects\wynth-api\.worktrees\sub2api-account-pool commit -m "feat: add account pool capability detection ui"
```

---

### Task 6: Review And Final Verification

**Files:**
- All files changed by Tasks 1-5.

- [ ] **Step 1: Format backend**

Run:

```powershell
gofmt -w model/account_pool.go service/account_pool_service.go service/account_pool_capability.go service/account_pool_capability_test.go dto/account_pool.go controller/account_pool.go controller/account_pool_test.go router/api-router.go
```

- [ ] **Step 2: Run backend tests**

Run:

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./model ./service ./controller ./relay -count=1
```

Expected: pass.

- [ ] **Step 3: Run frontend checks**

From `web/default` run:

```powershell
bun run typecheck
bun run build
```

Expected: pass.

- [ ] **Step 4: Run diff hygiene**

Run:

```powershell
git -C E:\Documents\Projects\wynth-api\.worktrees\sub2api-account-pool diff --check
git -C E:\Documents\Projects\wynth-api\.worktrees\sub2api-account-pool status --short
```

Expected: no whitespace errors; only intentional tracked changes before final commit.

- [ ] **Step 5: Ask Claude for focused review**

Use a quota-conscious prompt first:

```powershell
claude -p --model sonnet --effort medium --output-format json --disable-slash-commands --allowedTools Read,Grep,Glob --disallowedTools Bash,Edit,Write "Review the account-pool capability detection changes on branch sub2api-account-pool. Focus on credential safety, SSRF/private-IP behavior, account health isolation, JSON wrapper usage, and SQLite/MySQL/PostgreSQL compatibility. Return findings with file/line references only."
```

If Claude quota fails on default settings, retry with:

```powershell
claude -p --model sonnet --effort medium --settings ~/.claude/settings.wynth.json --output-format json --disable-slash-commands --allowedTools Read,Grep,Glob --disallowedTools Bash,Edit,Write "Review the account-pool capability detection changes on branch sub2api-account-pool. Focus on credential safety, SSRF/private-IP behavior, account health isolation, JSON wrapper usage, and SQLite/MySQL/PostgreSQL compatibility. Return findings with file/line references only."
```

- [ ] **Step 6: Address accepted review findings**

For each Claude finding:

- verify it against the code;
- fix only real issues;
- add or update a focused test for each behavior bug;
- rerun the smallest relevant test first;
- rerun Task 6 Steps 1-4.

- [ ] **Step 7: Commit review fixes if any**

```powershell
git -C E:\Documents\Projects\wynth-api\.worktrees\sub2api-account-pool add <changed-files>
git -C E:\Documents\Projects\wynth-api\.worktrees\sub2api-account-pool commit -m "fix: harden account pool capability detection"
```

- [ ] **Step 8: Push Wynth branch only**

```powershell
git -C E:\Documents\Projects\wynth-api\.worktrees\sub2api-account-pool push origin sub2api-account-pool
```

Do not push to `upstream` and do not open anything against `QuantumNous/new-api`.

---

## Self-Review Notes

- Spec coverage: the plan covers detection modes, explicit/implicit bound channel selection, dry-run/apply, merge/replace, sanitized errors, metadata persistence, controller endpoints, UI entry points, and final Claude review.
- Scope kept out: no dedicated ChatGPT reverse proxy, no scheduled detection, no automatic priority changes, no production health scoring updates.
- Database compatibility: only scalar `bigint`, `varchar`, and `text` fields are added; JSON is stored as text and parsed with `common` wrappers.
- Risk to watch during implementation: detection must not bypass SSRF/private-IP protection globally. It should inherit the bound generated channel's upstream-source fetch options, and manually created channels should keep the normal global fetch setting behavior.
