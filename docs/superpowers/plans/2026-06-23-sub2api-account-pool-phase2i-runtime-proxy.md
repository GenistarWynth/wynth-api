# Account Pool Phase 2I Runtime Proxy Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Route account-pool runtime requests through the selected account proxy, or the pool default proxy when the account has no proxy.

**Architecture:** Build a service-layer proxy resolver that turns account-pool proxy rows into the same proxy URL format already consumed by `service.NewProxyHttpClient`. Include the resolved proxy URL in account selection results, apply it to `RelayInfo.ChannelSetting.Proxy`, and extend relay retry snapshots so proxy settings are restored between account attempts.

**Tech Stack:** Go, GORM, existing relay `ChannelSettings.Proxy`, testify tests.

---

### Task 1: Resolve Account-Pool Proxy URL

**Files:**
- Create: `service/account_pool_proxy_runtime_test.go`
- Create: `service/account_pool_proxy_runtime.go`
- Modify: `service/account_pool_scheduler.go`

- [ ] **Step 1: Write failing tests**

Add tests proving:
- account proxy takes precedence over pool default proxy;
- pool default proxy is used when the account has no proxy;
- disabled proxy rows follow their fallback proxy;
- missing or disabled configured proxies without a fallback return an error instead of silently sending direct traffic.

- [ ] **Step 2: Run tests to verify they fail**

Run:

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./service -run "TestResolveAccountPoolRuntimeProxy|TestSelectAccountPoolAccountIncludesProxy" -count=1
```

Expected: fail because runtime proxy resolution and `ProxyURL` selection output do not exist.

- [ ] **Step 3: Write minimal implementation**

Implement:

```go
func ResolveAccountPoolRuntimeProxyURL(accountProxyID int, poolDefaultProxyID int) (string, error)
```

Rules:
- choose `accountProxyID` when non-zero; otherwise choose `poolDefaultProxyID`;
- no configured proxy returns `""`;
- find the first enabled proxy in the configured proxy's fallback chain;
- decrypt proxy password with `DecryptAccountPoolProxyAuthConfig`;
- build an escaped URL: `protocol://[username[:password]@]host:port`;
- return an error for missing proxies, unsupported protocols, invalid host/port, or exhausted fallback chains.

Add `ProxyURL string` to `AccountPoolSelectionResult` and populate it during selection.

- [ ] **Step 4: Run tests to verify they pass**

Run the same focused service command.

### Task 2: Apply Proxy to Relay Runtime

**Files:**
- Modify: `service/account_pool_runtime_test.go`
- Modify: `service/account_pool_runtime.go`
- Modify: `relay/account_pool_runtime_test.go`
- Modify: `relay/account_pool_runtime.go`

- [ ] **Step 1: Write failing tests**

Add service-level tests proving `ApplyAccountPoolRuntimeSelection` applies a resolved account proxy to `info.ChannelSetting.Proxy`.

Add relay-level tests proving retry restores channel proxy settings so an account with no proxy does not inherit a failed account's proxy.

- [ ] **Step 2: Run tests to verify they fail**

Run:

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./service ./relay -run "TestAccountPoolRuntimeAppliesSelectedProxy|TestAccountPoolRuntimeAttemptsResetProxyForEachRetry" -count=1
```

Expected: fail because proxy URL is not applied and snapshot restore does not include proxy state.

- [ ] **Step 3: Write minimal implementation**

In `ApplyAccountPoolRuntimeSelection`, set `info.ChannelSetting.Proxy = selection.ProxyURL` when `selection.ProxyURL != ""`.

In `relay/account_pool_runtime.go`, add channel setting proxy to the runtime snapshot and restore it before each account attempt.

- [ ] **Step 4: Run tests and review**

Run:

```powershell
gofmt -w service/account_pool_proxy_runtime.go service/account_pool_proxy_runtime_test.go service/account_pool_scheduler.go service/account_pool_runtime.go service/account_pool_runtime_test.go relay/account_pool_runtime.go relay/account_pool_runtime_test.go
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./service ./relay -run "TestResolveAccountPoolRuntimeProxy|TestSelectAccountPoolAccountIncludesProxy|TestAccountPoolRuntimeAppliesSelectedProxy|TestAccountPoolRuntimeAttemptsResetProxyForEachRetry" -count=1
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./service ./relay ./controller -count=1
```

Then request Claude review of the diff and address any Critical or Important findings.
