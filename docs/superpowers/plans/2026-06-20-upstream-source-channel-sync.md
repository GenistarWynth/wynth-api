# Upstream Source Channel Sync Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the first upstream source automation loop: connect to a sub2api upstream, discover pricing groups, create/update one local channel per selected group, and persist source-channel mappings for later price-to-priority work.

**Architecture:** Add durable upstream source and mapping models, then layer a source-agnostic service over a sub2api adapter. Controllers expose admin-only APIs; the frontend adds a compact admin page for source setup, discovery, mapping selection, and manual sync.

**Tech Stack:** Go 1.22+, Gin, GORM, testify, existing SSRF-aware HTTP client, React 19, TanStack Router, TypeScript, Bun.

---

## Context

Design spec: `docs/superpowers/specs/2026-06-20-upstream-source-channel-sync-design.md`

Worktree: `E:\Documents\Projects\wynth-api\.worktrees\upstream-source-sync`

Go command:

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./model ./service ./controller -count=1
```

Frontend commands:

```powershell
cd web/default
bun run typecheck
bun run build
bun run i18n:sync
```

Sub2api contract has been checked against the local reference clone. Key facts:

- Admin API base is `/api/v1`.
- Relay base is the same origin root; OpenAI-compatible requests go to `/v1/...`.
- API responses are wrapped as `{ "code": 0, "message": "success", "data": ... }`.
- `GET /keys` is paginated under `data.items`.
- `POST /keys` returns both numeric `id` and full `key`.
- `GET /groups/rates` returns a map from group id to multiplier.
- Login may return `requires_2fa`; v1 should report this as unsupported.

## File Structure

Backend model and DTO:

- Create `model/upstream_source.go`: GORM models, constants, validation helpers, credential redaction, mapping ownership helpers, source sync claim/release.
- Create `model/upstream_source_test.go`: model validation, redaction, migration/index smoke, mapping upsert ownership, DB-level sync claim.
- Modify `model/main.go`: add new tables to both `migrateDB` and `migrateDBFast`.
- Modify `model/ability.go`: skip blank model/group tokens when generating abilities.
- Create `model/ability_empty_model_test.go`: regression coverage for empty `Models`.
- Create `dto/upstream_source.go`: request/response DTOs for source CRUD, credentials, discovery, mapping selection, and sync.

Backend service:

- Create `service/upstream_source_types.go`: source-agnostic adapter interfaces and normalized structs.
- Create `service/upstream_source_sub2api.go`: sub2api adapter, envelope decoding, auth, discovery, key CRUD.
- Create `service/upstream_source_sub2api_test.go`: fake HTTP server tests for login, 2FA, envelope errors, group/rates normalization, key create/update/list.
- Create `service/upstream_model_fetch.go`: reusable upstream model fetch helper extracted from controller logic.
- Modify `controller/channel.go`: make `FetchUpstreamModels` call the service helper.
- Modify `controller/channel_upstream_update.go`: use the same service helper.
- Create `service/upstream_model_fetch_test.go`: OpenAI-compatible `/v1/models` fetch, blank model normalization, SSRF-aware client injection where practical.
- Create `service/upstream_source.go`: source CRUD orchestration, discovery, mapping updates, sync result shaping.
- Create `service/upstream_source_test.go`: discovery and sync behavior with fake adapters.

Backend controller and routes:

- Create `controller/upstream_source.go`: admin handlers.
- Modify `controller/audit.go`: add upstream-source audit templates.
- Modify `router/api-router.go`: register `/api/upstream_sources` admin routes.
- Create `controller/upstream_source_test.go`: API response redaction and route behavior.

Frontend:

- Create `web/default/src/features/upstream-sources/types.ts`
- Create `web/default/src/features/upstream-sources/api.ts`
- Create `web/default/src/features/upstream-sources/index.tsx`
- Create `web/default/src/routes/_authenticated/upstream-sources/index.tsx`
- Modify `web/default/src/hooks/use-sidebar-data.ts`: add an admin navigation item.
- Allow `web/default/src/routeTree.gen.ts` to update through the TanStack Router plugin during frontend checks; do not hand-edit it.
- Add i18n keys through `bun run i18n:sync`.

## Task 1: Ability Empty-Model Regression

**Files:**
- Modify: `model/ability.go`
- Create: `model/ability_empty_model_test.go`

- [ ] **Step 1: Write the failing tests**

Create `model/ability_empty_model_test.go`:

```go
package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChannelAddAbilitiesSkipsEmptyModels(t *testing.T) {
	truncateTables(t)
	priority := int64(0)

	channel := &Channel{
		Id:       101,
		Type:     constant.ChannelTypeOpenAI,
		Status:   common.ChannelStatusManuallyDisabled,
		Name:     "empty-model-channel",
		Key:      "sk-test",
		Models:   "",
		Group:    "default",
		Priority: &priority,
	}
	require.NoError(t, DB.Create(channel).Error)

	require.NoError(t, channel.AddAbilities(nil))

	var count int64
	require.NoError(t, DB.Model(&Ability{}).Where("channel_id = ?", channel.Id).Count(&count).Error)
	assert.Equal(t, int64(0), count)
}

func TestChannelUpdateAbilitiesSkipsEmptyModels(t *testing.T) {
	truncateTables(t)
	priority := int64(0)

	channel := &Channel{
		Id:       102,
		Type:     constant.ChannelTypeOpenAI,
		Status:   common.ChannelStatusEnabled,
		Name:     "empty-model-update-channel",
		Key:      "sk-test",
		Models:   "gpt-4o",
		Group:    "default",
		Priority: &priority,
	}
	require.NoError(t, DB.Create(channel).Error)
	require.NoError(t, channel.AddAbilities(nil))

	channel.Models = ""
	require.NoError(t, channel.UpdateAbilities(nil))

	var count int64
	require.NoError(t, DB.Model(&Ability{}).Where("channel_id = ?", channel.Id).Count(&count).Error)
	assert.Equal(t, int64(0), count)
}
```

- [ ] **Step 2: Run tests to verify failure**

Run:

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./model -run "TestChannel.*EmptyModels" -count=1
```

Expected: FAIL because a blank `Models` string currently creates one ability with `model = ""`.

Do not add a second `TestMain` in the `model` package. Reuse the existing model package test database from `model/task_cas_test.go` for `Channel`/`Ability` tests, and use local inline pointers such as `priority := int64(0); Priority: &priority`.

- [ ] **Step 3: Implement blank-token filtering**

In `model/ability.go`, add a helper near `AddAbilities`:

```go
func splitNonEmptyCSV(value string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		result = append(result, trimmed)
	}
	return result
}
```

Replace both ability builders:

```go
models_ := splitNonEmptyCSV(channel.Models)
groups_ := splitNonEmptyCSV(channel.Group)
if len(models_) == 0 || len(groups_) == 0 {
	return nil
}
```

For `UpdateAbilities`, place the early return after deleting existing abilities, so clearing `Models` removes stale abilities.

- [ ] **Step 4: Verify**

Run:

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./model -run "TestChannel.*EmptyModels" -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```powershell
git add model\ability.go model\ability_empty_model_test.go
git commit -m "fix: skip empty model abilities"
```

## Task 2: Upstream Source Models and Migration

**Files:**
- Create: `model/upstream_source.go`
- Create: `model/upstream_source_test.go`
- Create: `dto/upstream_source.go`
- Modify: `model/main.go`

- [ ] **Step 1: Write model and migration tests first**

Create tests covering:

```go
func TestUpstreamSourceRedactedResponseOmitsAuthConfig(t *testing.T)
func TestUpstreamSourceMappingPreservesSyncFieldsOnDiscoveryUpsert(t *testing.T)
func TestUpstreamSourceClaimSyncIsDatabaseGuarded(t *testing.T)
func TestUpstreamSourceAutoMigrateCreatesUniqueMapping(t *testing.T)
```

At the top of `model/upstream_source_test.go`, add a test-local DB swap helper instead of adding another `TestMain`:

```go
func setupUpstreamSourceTestDB(t *testing.T) {
	t.Helper()

	oldDB := DB
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	DB = db
	t.Cleanup(func() {
		DB = oldDB
	})

	require.NoError(t, DB.AutoMigrate(&UpstreamSource{}, &UpstreamSourceChannelMapping{}, &Channel{}, &Ability{}))
}
```

Import `github.com/glebarez/sqlite` and `gorm.io/gorm` in that test file.

Key assertions:

```go
require.NoError(t, DB.AutoMigrate(&UpstreamSource{}, &UpstreamSourceChannelMapping{}))

source := UpstreamSource{
	Name:       "source-a",
	Type:       UpstreamSourceTypeSub2API,
	Status:     UpstreamSourceStatusEnabled,
	BaseURL:    "https://example.com",
	RelayBaseURL: "https://example.com",
	AuthConfig: `{"email":"admin@example.com","password":"secret"}`,
}
payload, err := common.Marshal(source)
require.NoError(t, err)
assert.NotContains(t, string(payload), "secret")
assert.NotContains(t, string(payload), "AuthConfig")
```

For mapping upsert ownership:

```go
mapping := UpstreamSourceChannelMapping{
	SourceID: source.Id,
	SyncEnabled: true,
	UpstreamGroupID: "10",
	UpstreamKeyID: "99",
	LocalChannelID: 123,
	SyncStatus: UpstreamMappingSyncStatusSynced,
}
require.NoError(t, DB.Create(&mapping).Error)

incoming := UpstreamSourceChannelMapping{
	SourceID: source.Id,
	UpstreamGroupID: "10",
	UpstreamGroupName: "ChatGPT Cheap",
	DiscoveryStatus: UpstreamMappingDiscoveryStatusActive,
}
require.NoError(t, UpsertDiscoveredMappings(source.Id, []UpstreamSourceChannelMapping{incoming}, now))

var reloaded UpstreamSourceChannelMapping
require.NoError(t, DB.Where("source_id = ? AND upstream_group_id = ?", source.Id, "10").First(&reloaded).Error)
assert.True(t, reloaded.SyncEnabled)
assert.Equal(t, "99", reloaded.UpstreamKeyID)
assert.Equal(t, 123, reloaded.LocalChannelID)
assert.Equal(t, UpstreamMappingSyncStatusSynced, reloaded.SyncStatus)
assert.Equal(t, "ChatGPT Cheap", reloaded.UpstreamGroupName)
```

- [ ] **Step 2: Run tests to verify failure**

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./model -run "TestUpstreamSource" -count=1
```

Expected: FAIL because types/functions do not exist.

- [ ] **Step 3: Implement models**

Create `model/upstream_source.go` with:

```go
package model

import (
	"errors"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	UpstreamSourceTypeSub2API = "sub2api"

	UpstreamSourceStatusEnabled  = "enabled"
	UpstreamSourceStatusDisabled = "disabled"
	UpstreamSourceStatusDeleted  = "deleted"

	UpstreamDiscoveryStatusNever     = "never_run"
	UpstreamDiscoveryStatusSucceeded = "succeeded"
	UpstreamDiscoveryStatusFailed    = "failed"

	UpstreamSyncStatusNever     = "never_run"
	UpstreamSyncStatusRunning   = "running"
	UpstreamSyncStatusSucceeded = "succeeded"
	UpstreamSyncStatusFailed    = "failed"

	UpstreamMappingDiscoveryStatusActive  = "active"
	UpstreamMappingDiscoveryStatusStale   = "stale"
	UpstreamMappingDiscoveryStatusInvalid = "invalid"

	UpstreamMappingSyncStatusNeverSynced    = "never_synced"
	UpstreamMappingSyncStatusSynced         = "synced"
	UpstreamMappingSyncStatusFailed         = "failed"
	UpstreamMappingSyncStatusSkipped        = "skipped"
	UpstreamMappingSyncStatusNeedsAttention = "needs_attention"
)

type UpstreamSource struct {
	Id                  int    `json:"id"`
	Name                string `json:"name" gorm:"type:varchar(191);not null;index"`
	Type                string `json:"type" gorm:"type:varchar(32);not null;index"`
	Status              string `json:"status" gorm:"type:varchar(32);not null;default:'enabled';index"`
	BaseURL             string `json:"base_url" gorm:"type:varchar(512);not null"`
	AdminAPIBasePath    string `json:"admin_api_base_path" gorm:"type:varchar(128);not null;default:'/api/v1'"`
	RelayBaseURL        string `json:"relay_base_url" gorm:"type:varchar(512);not null"`
	AuthConfig          string `json:"-" gorm:"type:text"`
	SyncConfig          string `json:"sync_config" gorm:"type:text"`
	CurrentSyncToken    string `json:"-" gorm:"type:varchar(64);index"`
	SyncStartedAt       int64  `json:"sync_started_at" gorm:"bigint"`
	LastDiscoveryTime   int64  `json:"last_discovery_time" gorm:"bigint"`
	LastDiscoveryStatus string `json:"last_discovery_status" gorm:"type:varchar(32)"`
	LastDiscoveryError  string `json:"last_discovery_error" gorm:"type:varchar(1024)"`
	LastSyncTime        int64  `json:"last_sync_time" gorm:"bigint"`
	LastSyncStatus      string `json:"last_sync_status" gorm:"type:varchar(32)"`
	LastSyncError       string `json:"last_sync_error" gorm:"type:varchar(1024)"`
	CreatedTime         int64  `json:"created_time" gorm:"bigint"`
	UpdatedTime         int64  `json:"updated_time" gorm:"bigint"`
}

type UpstreamSourceChannelMapping struct {
	Id                      int      `json:"id"`
	SourceID                int      `json:"source_id" gorm:"not null;uniqueIndex:idx_upstream_source_group;index"`
	SyncEnabled             bool     `json:"sync_enabled" gorm:"not null;default:false;index"`
	UpstreamGroupID         string   `json:"upstream_group_id" gorm:"type:varchar(191);not null;uniqueIndex:idx_upstream_source_group"`
	UpstreamGroupName       string   `json:"upstream_group_name" gorm:"type:varchar(191)"`
	UpstreamPlatform        string   `json:"upstream_platform" gorm:"type:varchar(64)"`
	DiscoveryStatus         string   `json:"discovery_status" gorm:"type:varchar(32);index"`
	UpstreamStatus          string   `json:"upstream_status" gorm:"type:varchar(32)"`
	UpstreamRateMultiplier  *float64 `json:"upstream_rate_multiplier"`
	EffectiveRateMultiplier *float64 `json:"effective_rate_multiplier"`
	UpstreamKeyID           string   `json:"upstream_key_id" gorm:"type:varchar(191)"`
	LocalChannelID          int      `json:"local_channel_id" gorm:"index"`
	SyncStatus              string   `json:"sync_status" gorm:"type:varchar(32);index"`
	LastError               string   `json:"last_error" gorm:"type:varchar(1024)"`
	LastDiscoveredAt        int64    `json:"last_discovered_at" gorm:"bigint"`
	LastSyncedAt            int64    `json:"last_synced_at" gorm:"bigint"`
	CreatedTime             int64    `json:"created_time" gorm:"bigint"`
	UpdatedTime             int64    `json:"updated_time" gorm:"bigint"`
}
```

Add `BeforeCreate`/`BeforeUpdate` hooks for timestamps and defaults. Add:

```go
func UpsertDiscoveredMappings(sourceID int, mappings []UpstreamSourceChannelMapping, now int64) error
func ClaimUpstreamSourceSync(sourceID int, token string, now int64, staleAfterSeconds int64) (bool, error)
func ReleaseUpstreamSourceSync(sourceID int, token string, status string, errText string, now int64) error
```

`UpsertDiscoveredMappings` must use `clause.OnConflict{Columns: ..., DoUpdates: clause.AssignmentColumns([...])}` and list only discovery-owned columns.

`ClaimUpstreamSourceSync` must update with a `WHERE` clause that checks blank or expired `current_sync_token`, then return `RowsAffected == 1`.

- [ ] **Step 4: Add DTOs**

Create `dto/upstream_source.go` with request and response types:

```go
package dto

type UpstreamSourceCreateRequest struct {
	Name             string `json:"name" binding:"required"`
	Type             string `json:"type" binding:"required"`
	BaseURL          string `json:"base_url" binding:"required"`
	AdminAPIBasePath string `json:"admin_api_base_path"`
	RelayBaseURL     string `json:"relay_base_url"`
	Email            string `json:"email"`
	Password         string `json:"password"`
	LocalGroup       string `json:"local_group"`
	ChannelType      int    `json:"channel_type"`
	DefaultPriority  int64  `json:"default_priority"`
	DefaultWeight    uint   `json:"default_weight"`
	EnableMonitor    bool   `json:"enable_monitor"`
	MonitorInterval  int    `json:"monitor_interval_minutes"`
	AutoSyncModels   bool   `json:"auto_sync_models"`
}

type UpstreamSourceResponse struct {
	Id                  int    `json:"id"`
	Name                string `json:"name"`
	Type                string `json:"type"`
	Status              string `json:"status"`
	BaseURL             string `json:"base_url"`
	AdminAPIBasePath    string `json:"admin_api_base_path"`
	RelayBaseURL        string `json:"relay_base_url"`
	MaskedEmail         string `json:"masked_email"`
	HasCredentials      bool   `json:"has_credentials"`
	LastDiscoveryTime   int64  `json:"last_discovery_time"`
	LastDiscoveryStatus string `json:"last_discovery_status"`
	LastDiscoveryError  string `json:"last_discovery_error"`
	LastSyncTime        int64  `json:"last_sync_time"`
	LastSyncStatus      string `json:"last_sync_status"`
	LastSyncError       string `json:"last_sync_error"`
}
```

Add mapping and sync DTOs in the same file. Do not include raw password, access token, refresh token, upstream API key, or local channel key in response DTOs.

- [ ] **Step 5: Register migrations**

Modify `model/main.go`:

```go
err := DB.AutoMigrate(
	&Channel{},
	&ChannelMonitorLog{},
	&UpstreamSource{},
	&UpstreamSourceChannelMapping{},
	...
)
```

Add both models to `migrateDBFast` migrations.

- [ ] **Step 6: Verify**

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./model -run "TestUpstreamSource|TestChannel.*EmptyModels" -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit**

```powershell
git add model\upstream_source.go model\upstream_source_test.go dto\upstream_source.go model\main.go
git commit -m "feat: add upstream source models"
```

## Task 3: sub2api Adapter

**Files:**
- Create: `service/upstream_source_types.go`
- Create: `service/upstream_source_sub2api.go`
- Create: `service/upstream_source_sub2api_test.go`

- [ ] **Step 1: Write adapter tests**

Tests should use `httptest.Server` and assert:

```go
func TestSub2APIAdapterLoginUpdatesAuthConfigWithBearerTokens(t *testing.T)
func TestSub2APIAdapterLoginReports2FARequired(t *testing.T)
func TestSub2APIAdapterDiscoverGroupsUsesUserRates(t *testing.T)
func TestSub2APIAdapterCreateKeyReturnsIDAndFullKey(t *testing.T)
func TestSub2APIAdapterListKeysReadsPaginatedItems(t *testing.T)
func TestSub2APIAdapterNonZeroEnvelopeCodeIsError(t *testing.T)
func TestSub2APIAdapterRejectsBlockedURL(t *testing.T)
func TestSub2APIAdapterSanitizesAndCapsErrors(t *testing.T)
```

Use wrapped responses:

```go
writeJSON(t, w, map[string]any{
	"code": 0,
	"message": "success",
	"data": map[string]any{"access_token": "token", "refresh_token": "refresh", "expires_in": 3600},
})
```

- [ ] **Step 2: Run tests to verify failure**

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./service -run "TestSub2APIAdapter" -count=1
```

Expected: FAIL because adapter types do not exist.

- [ ] **Step 3: Define adapter interfaces**

In `service/upstream_source_types.go`:

```go
type UpstreamSourceAdapter interface {
	DiscoverGroups(ctx context.Context, source *model.UpstreamSource) ([]UpstreamGroup, error)
	CreateKey(ctx context.Context, source *model.UpstreamSource, groupID string, name string) (UpstreamKey, error)
	UpdateKey(ctx context.Context, source *model.UpstreamSource, keyID string, groupID string, name string) (UpstreamKey, error)
	ListKeys(ctx context.Context, source *model.UpstreamSource, groupID string) ([]UpstreamKey, error)
}

type UpstreamGroup struct {
	ID                      string
	Name                    string
	Platform                string
	Status                  string
	RateMultiplier          *float64
	EffectiveRateMultiplier *float64
}

type UpstreamKey struct {
	ID      string
	Key     string
	Name    string
	GroupID string
}
```

- [ ] **Step 4: Implement sub2api client**

In `service/upstream_source_sub2api.go`, implement:

```go
type Sub2APIAdapter struct {
	Client *http.Client
}
```

Use the existing service HTTP client when `Client` is nil. Before each request, validate URLs through existing SSRF-aware validation. Use `common.Marshal`, `common.Unmarshal`, and `common.DecodeJson` for JSON.

Authentication behavior:

- If `AuthConfig` contains an unexpired `access_token`, use it.
- If the token is missing or expired, login with email/password.
- On successful login, update `source.AuthConfig` with `access_token`, `refresh_token`, and `expires_at` so the calling service can persist it.
- The service should persist changed `AuthConfig` after a successful adapter operation.

Add a helper used by the adapter and later service code:

```go
func SanitizeUpstreamSourceError(err error) string
```

It must strip bearer tokens, refresh tokens, API keys, passwords, cookies, authorization headers, and query-string token values, then cap the result at 1024 characters.

Decode envelope:

```go
type sub2APIEnvelope[T any] struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    T      `json:"data"`
}
```

Return a sanitized error when `Code != 0`.

Normalize numeric ids:

```go
func formatNullableIntID(value *int64) string {
	if value == nil {
		return ""
	}
	return strconv.FormatInt(*value, 10)
}
```

2FA behavior: if login `data.requires_2fa == true`, return `ErrUpstreamSource2FARequired`.

- [ ] **Step 5: Verify**

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./service -run "TestSub2APIAdapter" -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```powershell
git add service\upstream_source_types.go service\upstream_source_sub2api.go service\upstream_source_sub2api_test.go
git commit -m "feat: add sub2api upstream adapter"
```

## Task 4: Reusable Upstream Model Fetching

**Files:**
- Create: `service/upstream_model_fetch.go`
- Create: `service/upstream_model_fetch_test.go`
- Modify: `controller/channel.go`
- Modify: `controller/channel_upstream_update.go`

- [ ] **Step 1: Write service tests**

Add tests for OpenAI-compatible model fetching:

```go
func TestFetchChannelUpstreamModelIDsOpenAICompatible(t *testing.T)
func TestFetchChannelUpstreamModelIDsNormalizesBlankModels(t *testing.T)
```

Use a fake server responding at `/v1/models`:

```json
{
  "data": [
    { "id": "gpt-4o" },
    { "id": " gpt-4o-mini " },
    { "id": "" }
  ]
}
```

Expected normalized ids: `[]string{"gpt-4o", "gpt-4o-mini"}`.

- [ ] **Step 2: Run tests to verify failure**

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./service -run "TestFetchChannelUpstreamModelIDs" -count=1
```

Expected: FAIL because helper does not exist.

- [ ] **Step 3: Move reusable logic into service**

Create `service/upstream_model_fetch.go`:

```go
func FetchChannelUpstreamModelIDs(channel *model.Channel) ([]string, error)
func BuildFetchModelsHeaders(channel *model.Channel, key string) (http.Header, error)
```

Reimplement the small helper logic in service rather than importing controller symbols. Specifically:

- define service-local OpenAI model response structs;
- define service-local auth header builders equivalent to the controller behavior;
- define a service-local model-name normalization helper;
- use existing service HTTP clients and proxy support;
- keep provider-specific calls to Gemini and Ollama in service.

After controllers are updated, delete only the now-unused controller helpers `buildFetchModelsHeaders` and `fetchChannelUpstreamModelIDs`. Do not move or delete broadly-used controller helpers such as `GetResponseBody`, `GetAuthHeader`, `GetClaudeAuthHeader`, or `normalizeModelNames`.

- [ ] **Step 4: Update controllers**

In `controller/channel.go`, replace:

```go
ids, err := fetchChannelUpstreamModelIDs(channel)
```

with:

```go
ids, err := service.FetchChannelUpstreamModelIDs(channel)
```

In `controller/channel_upstream_update.go`, replace both internal calls with `service.FetchChannelUpstreamModelIDs(channel)`. Delete the old controller-level helper once no references remain.

- [ ] **Step 5: Verify**

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./service ./controller -run "TestFetchChannelUpstreamModelIDs|TestChannelUpstream" -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```powershell
git add service\upstream_model_fetch.go service\upstream_model_fetch_test.go controller\channel.go controller\channel_upstream_update.go
git commit -m "refactor: share upstream model fetching"
```

## Task 5: Discovery Service

**Files:**
- Create/modify: `service/upstream_source.go`
- Create: `service/upstream_source_test.go`
- Modify: `model/upstream_source.go`

- [ ] **Step 1: Write discovery tests**

Cover:

```go
func TestDiscoverUpstreamSourceUpsertsMappings(t *testing.T)
func TestDiscoverUpstreamSourcePreservesSyncOwnedMappingFields(t *testing.T)
func TestDiscoverUpstreamSourceMarksMissingMappingsStale(t *testing.T)
func TestDiscoverUpstreamSourceFailureDoesNotMutateChannels(t *testing.T)
func TestDiscoverUpstreamSourceUnknownMultiplierIsInvalidForSync(t *testing.T)
func TestDiscoverUpstreamSourceStoresSanitizedCappedError(t *testing.T)
```

Use a fake adapter implementing `UpstreamSourceAdapter`.

- [ ] **Step 2: Run tests to verify failure**

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./service -run "TestDiscoverUpstreamSource" -count=1
```

Expected: FAIL because discovery service does not exist.

- [ ] **Step 3: Implement discovery service**

In `service/upstream_source.go`:

```go
type UpstreamSourceService struct {
	AdapterFactory func(sourceType string) (UpstreamSourceAdapter, error)
	Now            func() int64
}

func (s *UpstreamSourceService) Discover(ctx context.Context, sourceID int) (*dto.UpstreamSourceDiscoveryResult, error)
```

Behavior:

- Load source where status is not `deleted`.
- Validate source type and URLs.
- Call adapter `DiscoverGroups`.
- Convert groups into `model.UpstreamSourceChannelMapping`.
- Upsert discovery-owned fields only.
- Mark previously active but now absent mappings stale.
- Update source discovery status and sanitized error.
- Do not create or mutate channels.

- [ ] **Step 4: Verify**

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./service -run "TestDiscoverUpstreamSource" -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```powershell
git add service\upstream_source.go service\upstream_source_test.go model\upstream_source.go
git commit -m "feat: discover upstream source groups"
```

## Task 6: Sync Service and Generated Channels

**Files:**
- Modify: `service/upstream_source.go`
- Modify: `service/upstream_source_test.go`
- Modify: `model/upstream_source.go`

- [ ] **Step 1: Write sync tests**

Cover:

```go
func TestSyncUpstreamSourceCreatesChannelPerSelectedGroup(t *testing.T)
func TestSyncUpstreamSourceIsIdempotentByMappingID(t *testing.T)
func TestSyncUpstreamSourcePreservesUnownedChannelFields(t *testing.T)
func TestSyncUpstreamSourceWritesOwnedZeroValues(t *testing.T)
func TestSyncUpstreamSourceMissingLocalChannelNeedsAttention(t *testing.T)
func TestSyncUpstreamSourceDoesNotEnableChannelWithoutModels(t *testing.T)
func TestSyncUpstreamSourceClaimsSourceBeforeSync(t *testing.T)
func TestSyncUpstreamSourceStoresSanitizedCappedMappingError(t *testing.T)
```

For model fetching, inject a function:

```go
FetchModels func(channel *model.Channel) ([]string, error)
```

Use it to return `[]string{"gpt-4o"}` for success and `[]string{}` for disabled/no-model path.

- [ ] **Step 2: Run tests to verify failure**

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./service -run "TestSyncUpstreamSource" -count=1
```

Expected: FAIL because sync service does not exist.

- [ ] **Step 3: Implement sync**

Add:

```go
func (s *UpstreamSourceService) Sync(ctx context.Context, sourceID int) (*dto.UpstreamSourceSyncResult, error)
```

Behavior:

- Claim source with DB-backed token.
- Load selected mappings where `sync_enabled = true`.
- For nil `EffectiveRateMultiplier`, mark mapping `skipped`.
- For existing `LocalChannelID` missing in DB, mark `needs_attention`.
- Create upstream key when `UpstreamKeyID` is empty.
- Update upstream key metadata when `UpstreamKeyID` exists.
- Create local channel if `LocalChannelID == 0`.
- Update only source-owned channel columns using `Updates(map[string]any{...})`.
- Always set non-empty `BaseURL`.
- Fetch models and enable channel only when model list is non-empty.
- If model list is empty, set manual disabled status and mapping error.
- Refresh abilities for changed channel and call channel/proxy cache refresh after batch.
- Release source sync token in a `defer`.

- [ ] **Step 4: Verify**

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./service ./model -run "TestSyncUpstreamSource|TestChannel.*EmptyModels" -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```powershell
git add service\upstream_source.go service\upstream_source_test.go model\upstream_source.go
git commit -m "feat: sync upstream source channels"
```

## Task 7: Admin API and Audit

**Files:**
- Create: `controller/upstream_source.go`
- Create: `controller/upstream_source_test.go`
- Modify: `controller/audit.go`
- Modify: `router/api-router.go`

- [ ] **Step 1: Write controller tests**

Cover:

```go
func TestUpstreamSourceAPIListRedactsSecrets(t *testing.T)
func TestUpstreamSourceAPICreateStoresCredentialsButReturnsMaskedState(t *testing.T)
func TestUpstreamSourceAPIDiscoverRequiresAdmin(t *testing.T)
func TestUpstreamSourceAPISyncReturnsMappingResults(t *testing.T)
func TestUpstreamSourceAPISyncResultReturnsLastMappingStatuses(t *testing.T)
```

- [ ] **Step 2: Run tests to verify failure**

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./controller -run "TestUpstreamSourceAPI" -count=1
```

Expected: FAIL because routes/handlers do not exist.

- [ ] **Step 3: Implement handlers**

Create handlers:

```go
func ListUpstreamSources(c *gin.Context)
func CreateUpstreamSource(c *gin.Context)
func GetUpstreamSource(c *gin.Context)
func UpdateUpstreamSource(c *gin.Context)
func UpdateUpstreamSourceCredentials(c *gin.Context)
func DeleteUpstreamSource(c *gin.Context)
func DiscoverUpstreamSource(c *gin.Context)
func ListUpstreamSourceMappings(c *gin.Context)
func UpdateUpstreamSourceMappings(c *gin.Context)
func SyncUpstreamSource(c *gin.Context)
func GetUpstreamSourceSyncResult(c *gin.Context)
```

Use `middleware.AdminAuth()` routes. Use response DTOs only.

`SyncUpstreamSource` should record source-level sync audit and channel-level generated create/update audit entries from the sync result. Audit params must include ids, names, and counts only; never include upstream keys, local channel keys, passwords, or tokens.

- [ ] **Step 4: Add audit templates**

In `controller/audit.go` add:

```go
"upstream_source.create": "Created upstream source ${name} (ID: ${id})",
"upstream_source.update": "Updated upstream source ${name} (ID: ${id})",
"upstream_source.credentials_update": "Updated upstream source credentials for ${name} (ID: ${id})",
"upstream_source.discover": "Discovered upstream source ${name} (ID: ${id}, groups ${groups})",
"upstream_source.mapping_update": "Updated upstream source mappings for ${name} (ID: ${id})",
"upstream_source.sync": "Synced upstream source ${name} (ID: ${id}, created ${created}, updated ${updated}, failed ${failed})",
"upstream_source.channel_create": "Created channel ${channelName} from upstream source ${name} (ID: ${id})",
"upstream_source.channel_update": "Updated channel ${channelName} from upstream source ${name} (ID: ${id})",
```

Audit params must not contain credentials or keys.

- [ ] **Step 5: Register routes**

In `router/api-router.go`:

```go
upstreamSourceRoute := apiRouter.Group("/upstream_sources")
upstreamSourceRoute.Use(middleware.AdminAuth())
{
	upstreamSourceRoute.GET("", controller.ListUpstreamSources)
	upstreamSourceRoute.POST("", controller.CreateUpstreamSource)
	upstreamSourceRoute.GET("/:id", controller.GetUpstreamSource)
	upstreamSourceRoute.PUT("/:id", controller.UpdateUpstreamSource)
	upstreamSourceRoute.PUT("/:id/credentials", controller.UpdateUpstreamSourceCredentials)
	upstreamSourceRoute.DELETE("/:id", controller.DeleteUpstreamSource)
	upstreamSourceRoute.POST("/:id/discover", controller.DiscoverUpstreamSource)
	upstreamSourceRoute.GET("/:id/mappings", controller.ListUpstreamSourceMappings)
	upstreamSourceRoute.PUT("/:id/mappings", controller.UpdateUpstreamSourceMappings)
	upstreamSourceRoute.POST("/:id/sync", controller.SyncUpstreamSource)
	upstreamSourceRoute.GET("/:id/sync_result", controller.GetUpstreamSourceSyncResult)
}
```

- [ ] **Step 6: Verify**

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./controller ./service ./model -run "TestUpstreamSource|TestSub2APIAdapter|TestDiscoverUpstreamSource|TestSyncUpstreamSource" -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit**

```powershell
git add controller\upstream_source.go controller\upstream_source_test.go controller\audit.go router\api-router.go
git commit -m "feat: expose upstream source admin api"
```

## Task 8: Minimal Frontend

**Files:**
- Create: `web/default/src/features/upstream-sources/types.ts`
- Create: `web/default/src/features/upstream-sources/api.ts`
- Create: `web/default/src/features/upstream-sources/index.tsx`
- Create: `web/default/src/routes/_authenticated/upstream-sources/index.tsx`
- Modify: `web/default/src/hooks/use-sidebar-data.ts`
- Modify: `web/default/src/routeTree.gen.ts` only if the TanStack Router plugin updates it during `bun run typecheck` or `bun run build`.

Every new `.ts` and `.tsx` file in this task must start with the same copyright/license header used by existing `web/default/src` files. Preserve existing protected project attribution text.

- [ ] **Step 1: Add API types and calls**

Create `api.ts` using existing `api` client:

```ts
import { api } from '@/lib/api'
import type {
  UpstreamSource,
  UpstreamSourceCreateRequest,
  UpstreamSourceMapping,
  UpstreamSourceSyncResult,
} from './types'

export async function listUpstreamSources(): Promise<{ success: boolean; data: UpstreamSource[] }> {
  const res = await api.get('/api/upstream_sources')
  return res.data
}

export async function createUpstreamSource(data: UpstreamSourceCreateRequest) {
  const res = await api.post('/api/upstream_sources', data)
  return res.data
}

export async function discoverUpstreamSource(id: number) {
  const res = await api.post(`/api/upstream_sources/${id}/discover`)
  return res.data
}

export async function listUpstreamSourceMappings(id: number): Promise<{ success: boolean; data: UpstreamSourceMapping[] }> {
  const res = await api.get(`/api/upstream_sources/${id}/mappings`)
  return res.data
}

export async function updateUpstreamSourceMappings(id: number, mappingIds: number[]) {
  const res = await api.put(`/api/upstream_sources/${id}/mappings`, { mapping_ids: mappingIds })
  return res.data
}

export async function syncUpstreamSource(id: number): Promise<{ success: boolean; data: UpstreamSourceSyncResult }> {
  const res = await api.post(`/api/upstream_sources/${id}/sync`)
  return res.data
}

export async function getUpstreamSourceSyncResult(id: number): Promise<{ success: boolean; data: UpstreamSourceSyncResult }> {
  const res = await api.get(`/api/upstream_sources/${id}/sync_result`)
  return res.data
}
```

- [ ] **Step 2: Add admin route**

Create `web/default/src/routes/_authenticated/upstream-sources/index.tsx` mirroring channel admin guard:

```tsx
import { createFileRoute, redirect } from '@tanstack/react-router'
import { UpstreamSources } from '@/features/upstream-sources'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

export const Route = createFileRoute('/_authenticated/upstream-sources/')({
  beforeLoad: () => {
    const { auth } = useAuthStore.getState()
    if (!auth.user || auth.user.role < ROLE.ADMIN) {
      throw redirect({ to: '/403' })
    }
  },
  component: UpstreamSources,
})
```

- [ ] **Step 3: Build compact UI**

`index.tsx` should show:

- source list;
- create source form with type, name, base URL, email, password, local group, priority, weight, auto sync models;
- selected source mapping table;
- buttons for Discover and Sync;
- mapping sync toggles.

Do not show passwords, tokens, upstream keys, or generated local channel keys.

- [ ] **Step 4: Add navigation**

In `web/default/src/hooks/use-sidebar-data.ts`, import a lucide icon such as `Network` and add an admin nav item near `Channels`:

```tsx
{
  title: t('Upstream Sources'),
  url: '/upstream-sources',
  icon: Network,
},
```

- [ ] **Step 5: Verify frontend**

```powershell
cd web/default
bun run typecheck
bun run i18n:sync
bun run build
```

Expected: all pass.

- [ ] **Step 6: Commit**

```powershell
git add web\default\src\features\upstream-sources web\default\src\routes\_authenticated\upstream-sources web\default\src\hooks\use-sidebar-data.ts web\default\src\i18n web\default\src\routeTree.gen.ts
git commit -m "feat: add upstream source admin ui"
```

## Task 9: Full Verification and Review

**Files:**
- No planned code changes unless verification finds issues.

- [ ] **Step 1: Format Go files**

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\gofmt.exe' -w model\ability.go model\upstream_source.go model\upstream_source_test.go model\ability_empty_model_test.go dto\upstream_source.go service\upstream_source_types.go service\upstream_source_sub2api.go service\upstream_source_sub2api_test.go service\upstream_model_fetch.go service\upstream_model_fetch_test.go service\upstream_source.go service\upstream_source_test.go controller\upstream_source.go controller\upstream_source_test.go controller\channel.go controller\channel_upstream_update.go controller\audit.go router\api-router.go
```

- [ ] **Step 2: Run backend tests**

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./model ./service ./controller ./middleware -count=1
```

Expected: PASS.

- [ ] **Step 3: Run frontend checks**

```powershell
cd web/default
bun run typecheck
bun run i18n:sync
bun run build
```

Expected: PASS and no unstaged translation drift unless `i18n:sync` intentionally added keys.

- [ ] **Step 4: Check diff hygiene**

```powershell
git diff --check
rg "wynthgenistar|snT|mdkj|XbgeIXx2Y" . -n
```

Expected: `git diff --check` has no output. The credential scan has no matches.

- [ ] **Step 5: Claude review**

Run without `--settings`:

```powershell
$promptFile = New-TemporaryFile
$diffFile = New-TemporaryFile
git diff main...HEAD > $diffFile
@'
You are Claude Code reviewing this upstream source sync implementation.
Focus on bugs, security leaks, database compatibility, SSRF, generated channel correctness, and missing tests.
Do not edit files.
'@ + "`n`nDIFF:`n" + (Get-Content -Raw $diffFile) | Set-Content -LiteralPath $promptFile -Encoding UTF8
Get-Content -Raw -LiteralPath $promptFile | claude -p --model opus --effort max --output-format json --disable-slash-commands --tools ""
Remove-Item $promptFile,$diffFile -Force
```

- [ ] **Step 6: Address review findings**

For each finding:

- verify against code;
- fix only valid issues;
- add or update targeted tests;
- rerun affected tests.

- [ ] **Step 7: Final commit**

If fixes were needed:

```powershell
git add <changed-files>
git commit -m "fix: address upstream source review findings"
```

Final branch should contain only scoped commits for this feature.

## Self Review

Spec coverage:

- Upstream source and mapping tables: Task 2.
- sub2api adapter and contract: Task 3.
- SSRF-aware outbound requests: Task 3 plus Task 9 verification.
- Discovery: Task 5.
- Sync and channel generation: Task 6.
- Empty model ability guard: Task 1.
- Admin API/audit: Task 7.
- Minimal frontend: Task 8.
- Verification and Claude review: Task 9.

Known residual risks:

- Plaintext-at-rest upstream credentials are accepted for v1 and must be disclosed in UI/docs text where credential storage is described.
- Full new-api upstream source support is intentionally out of scope.
- Price-to-priority conversion is intentionally the next feature.

Execution choice: the user already selected subagent-driven development for this repo. Use fresh worker agents for Tasks 1-3 first, then review and continue in batches to avoid overlapping writes.
