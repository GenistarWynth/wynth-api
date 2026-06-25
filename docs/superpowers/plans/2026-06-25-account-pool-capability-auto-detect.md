# Account Pool Capability Auto Detect Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add pool-level scheduled model capability detection so account pools can periodically refresh account `SupportedModels` without manual clicks.

**Architecture:** Keep detection itself in the existing `AccountPoolService.DetectPoolCapabilities` path. Add pool-level scheduling fields, a due-job selector, and a master-node background worker that calls detection only for due enabled accounts. The UI only edits configuration; account health, failure penalties, and production metrics remain untouched by detection traffic.

**Tech Stack:** Go 1.22+, GORM, Gin DTO/controller mapping, React 19 TypeScript, Bun.

---

## File Structure

- `model/account_pool.go`: add pool-level auto capability detection settings.
- `dto/account_pool.go`: expose settings in create/update/response DTOs.
- `service/account_pool_service.go`: validate and persist settings.
- `service/account_pool_capability_auto_detect.go`: new due-job selector and background worker.
- `service/account_pool_capability_auto_detect_test.go`: deterministic scheduler tests with a fake detector.
- `controller/account_pool.go`: map DTO fields to service params and responses.
- `main.go`: start the worker.
- `web/default/src/features/account-pools/types.ts`: add frontend types.
- `web/default/src/features/account-pools/index.tsx`: add pool form controls.
- `web/default/src/i18n/locales/*.json`: synced by `bun run i18n:sync`.

## Task 1: Backend Data Contract

**Files:**
- Modify: `model/account_pool.go`
- Modify: `dto/account_pool.go`
- Modify: `service/account_pool_service.go`
- Modify: `controller/account_pool.go`
- Test: `service/account_pool_service_test.go`

- [ ] **Step 1: Write failing service test**

Add a test proving create/update round-trips:

```go
func TestAccountPoolServicePersistsCapabilityAutoDetectSettings(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	pool, err := (&AccountPoolService{}).CreatePool(AccountPoolCreateParams{
		Name:                           "capability-auto",
		CapabilityCheckEnabled:         true,
		CapabilityCheckIntervalMinutes: 30,
		CapabilityCheckMode:            AccountPoolCapabilityModeProbeModels,
		CapabilityCheckChannelID:       12,
		CapabilityCheckModels:          []string{"gpt-5", "gpt-5-mini"},
		CapabilityCheckTimeoutSeconds:  45,
		CapabilityCheckMerge:           true,
	})
	require.NoError(t, err)

	assert.True(t, pool.CapabilityCheckEnabled)
	assert.Equal(t, 30, pool.CapabilityCheckIntervalMinutes)
	assert.Equal(t, AccountPoolCapabilityModeProbeModels, pool.CapabilityCheckMode)
	assert.Equal(t, 12, pool.CapabilityCheckChannelID)
	assert.Equal(t, `["gpt-5","gpt-5-mini"]`, pool.CapabilityCheckModels)
	assert.Equal(t, 45, pool.CapabilityCheckTimeoutSeconds)
	assert.True(t, pool.CapabilityCheckMerge)
}
```

- [ ] **Step 2: Run test to verify RED**

Run:

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./service -run TestAccountPoolServicePersistsCapabilityAutoDetectSettings -count=1
```

Expected: compile failure because fields do not exist.

- [ ] **Step 3: Implement fields and mapping**

Add fields to `model.AccountPool`, DTOs, `AccountPoolCreateParams`, `CreatePool`, `UpdatePool`, `accountPoolCreateParams`, and `accountPoolResponse`.

- [ ] **Step 4: Run test to verify GREEN**

Run the same focused test and expect PASS.

## Task 2: Due Job Selector and Worker

**Files:**
- Create: `service/account_pool_capability_auto_detect.go`
- Create: `service/account_pool_capability_auto_detect_test.go`
- Modify: `main.go`

- [ ] **Step 1: Write failing due-selection tests**

Cover these contracts:

```go
func TestListDueAccountPoolCapabilityAutoDetectJobs(t *testing.T) {
	// enabled pool + enabled due account -> one job with that account id
	// disabled pool setting -> no job
	// recently checked account -> no job
	// deleted/disabled accounts -> excluded
}
```

- [ ] **Step 2: Write failing runner test**

Cover request shape:

```go
func TestRunDueAccountPoolCapabilityAutoDetectUsesPoolSettings(t *testing.T) {
	// fake detector records request:
	// Apply=true, Merge follows pool setting, Mode/ChannelID/CandidateModels/TimeoutSeconds copied from pool.
}
```

- [ ] **Step 3: Run tests to verify RED**

Run:

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./service -run "TestListDueAccountPoolCapabilityAutoDetectJobs|TestRunDueAccountPoolCapabilityAutoDetectUsesPoolSettings" -count=1
```

Expected: compile failure because the new functions do not exist.

- [ ] **Step 4: Implement selector and worker**

Implement:

```go
func listDueAccountPoolCapabilityAutoDetectJobs(ctx context.Context, now int64) ([]accountPoolCapabilityAutoDetectJob, error)
func (s *AccountPoolService) RunDueAccountPoolCapabilityAutoDetect(ctx context.Context, now int64) []AccountPoolCapabilityPoolResult
func StartAccountPoolCapabilityAutoDetectWorker()
```

The worker must use `common.IsMasterNode`, `sync.Once`, `atomic.Bool`, a one-minute ticker, and a per-pool timeout. It must not run if another tick is still active.

- [ ] **Step 5: Start worker from `main.go`**

Call `service.StartAccountPoolCapabilityAutoDetectWorker()` beside the upstream source workers.

- [ ] **Step 6: Run focused service tests**

Run the focused tests and expect PASS.

## Task 3: Frontend Configuration

**Files:**
- Modify: `web/default/src/features/account-pools/types.ts`
- Modify: `web/default/src/features/account-pools/index.tsx`
- Modify: `web/default/src/i18n/locales/*.json`

- [ ] **Step 1: Add type fields**

Extend `AccountPool` and create/update request types with:

```ts
capability_check_enabled: boolean
capability_check_interval_minutes: number
capability_check_mode: AccountPoolCapabilityMode
capability_check_channel_id: number
capability_check_models: string[]
capability_check_timeout_seconds: number
capability_check_merge: boolean
```

- [ ] **Step 2: Add form defaults and payload mapping**

Pool create/edit forms must default to disabled, daily interval, `models_endpoint`, empty channel, empty model list, 30 second timeout, and replace mode.

- [ ] **Step 3: Add UI controls**

Add a compact section in the pool dialog:

- enable scheduled capability detection;
- interval minutes;
- detection mode;
- channel id selector/input;
- candidate model comma input or existing model selector if local helper is already available;
- timeout seconds;
- merge detected models toggle.

- [ ] **Step 4: Run frontend checks**

Run:

```powershell
bun run typecheck
bun run i18n:sync
bun run build
```

Expected: all pass, i18n missing/untranslated counts stay zero.

## Task 4: Review and Finish

**Files:**
- All changed files.

- [ ] **Step 1: Run backend verification**

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./model ./service ./controller ./relay -count=1
```

- [ ] **Step 2: Run frontend verification**

```powershell
bun run typecheck
bun run i18n:sync
bun run build
```

- [ ] **Step 3: Claude review**

Run a quota-conscious Claude review focused on the diff:

```powershell
claude -p --model sonnet --effort medium --output-format json --disable-slash-commands --allowedTools Read,Grep,Glob --disallowedTools Bash,Edit,Write "Review the account-pool scheduled capability detection changes on branch sub2api-account-pool. Focus on background worker safety, detection traffic isolation, JSON wrapper usage, DB compatibility, and frontend config correctness. Return findings with file/line references only."
```

If the default Claude setting is out of quota, retry with:

```powershell
claude -p --model sonnet --effort medium --settings ~/.claude/settings.wynth.json --output-format json --disable-slash-commands --allowedTools Read,Grep,Glob --disallowedTools Bash,Edit,Write "Review the account-pool scheduled capability detection changes on branch sub2api-account-pool. Focus on background worker safety, detection traffic isolation, JSON wrapper usage, DB compatibility, and frontend config correctness. Return findings with file/line references only."
```

- [ ] **Step 4: Fix review findings, re-run verification, commit and push**

Commit message:

```powershell
git commit -m "feat: schedule account pool capability detection"
```
