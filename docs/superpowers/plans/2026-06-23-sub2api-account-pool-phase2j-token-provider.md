# Account Pool Phase 2J Token Provider Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Resolve account-pool runtime credentials through a token provider so OAuth accounts can refresh expired or missing access tokens before relay.

**Architecture:** Keep scheduling separate from token refresh. The scheduler selects an account and returns encrypted credential state; runtime credential resolution then chooses API key, valid access token, or refreshes OAuth token with singleflight and optimistic token-state writeback.

**Tech Stack:** Go, GORM, existing Codex OAuth refresh helper, `golang.org/x/sync/singleflight`, testify tests.

---

### Task 1: Token Provider Service

**Files:**
- Create: `service/account_pool_token_provider_test.go`
- Create: `service/account_pool_token_provider.go`

- [ ] **Step 1: Write failing tests**

Add tests proving:
- API-key accounts return the static API key without refresh;
- OAuth accounts reuse a non-expired access token;
- OAuth accounts with expired or missing access tokens call the refresh function;
- refreshed token state is encrypted and written back with incremented version;
- refresh failures temporarily disable the account and sanitize stored errors;
- concurrent refreshes for the same account share a single refresh call.

- [ ] **Step 2: Run tests to verify they fail**

Run:

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./service -run "TestAccountPoolTokenProvider" -count=1
```

Expected: fail because `ResolveAccountPoolRuntimeCredential` does not exist.

- [ ] **Step 3: Write minimal implementation**

Implement:

```go
type AccountPoolRuntimeCredentialRequest struct {
    AccountID   int
    Credential  AccountPoolCredentialConfig
    TokenState  AccountPoolTokenState
    ProxyURL    string
    Now         int64
}

func ResolveAccountPoolRuntimeCredential(ctx context.Context, req AccountPoolRuntimeCredentialRequest) (string, error)
```

Rules:
- API key wins when present.
- OAuth access token is usable when non-empty and `ExpiresAt == 0` or beyond a refresh skew window.
- Expired/missing OAuth token requires a refresh token from token state first, then credential config.
- Refresh uses a package-level function variable wrapping `RefreshCodexOAuthTokenWithProxy` for test substitution.
- Refresh uses account-id keyed singleflight.
- Writeback encrypts the next token state and updates `token_state` only if the stored token state has not changed.
- Refresh failure sets `temp_disabled_until`, `temp_disabled_reason`, and `last_error`.

- [ ] **Step 4: Run service tests**

Run the same focused service command, then:

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./service -count=1
```

### Task 2: Runtime Integration

**Files:**
- Modify: `service/account_pool_runtime.go`
- Modify: `service/account_pool_runtime_test.go`

- [ ] **Step 1: Write failing runtime test**

Add an integration test proving `ApplyAccountPoolRuntimeSelection` refreshes an expired OAuth account and applies the new access token to `info.ApiKey`.

- [ ] **Step 2: Run test to verify it fails**

Run:

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./service -run "TestAccountPoolRuntimeRefreshesExpiredOAuthToken" -count=1
```

Expected: fail because runtime still reads `selection.TokenState.AccessToken` directly.

- [ ] **Step 3: Wire runtime selection**

Replace direct credential selection in `ApplyAccountPoolRuntimeSelection` with `ResolveAccountPoolRuntimeCredential`.

Set selected account metadata and attempted account ID before resolving the credential so refresh/config errors do not let the same account be retried in the same request.

- [ ] **Step 4: Run tests and review**

Run:

```powershell
gofmt -w service/account_pool_token_provider.go service/account_pool_token_provider_test.go service/account_pool_runtime.go service/account_pool_runtime_test.go
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./service -run "TestAccountPoolTokenProvider|TestAccountPoolRuntimeRefreshesExpiredOAuthToken" -count=1
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./service ./relay ./relay/channel ./controller -count=1
```

Then request Claude review of the diff and address any Critical or Important findings.
