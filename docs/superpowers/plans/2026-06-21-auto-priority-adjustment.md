# Automatic Priority Adjustment Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Automatically update priority for upstream-source-generated channels using effective price, availability, and first-token latency.

**Architecture:** Add an opt-in upstream-source automatic-priority pipeline that computes scores outside the request path and writes the existing `channels.priority` field. The request scheduler remains unchanged and continues to rely on strict priority plus existing weight selection. Store the latest score explanation in channel settings for UI display.

**Tech Stack:** Go 1.22+, Gin, GORM, SQLite/MySQL/PostgreSQL-compatible queries, React 19, TypeScript, Base UI, Bun.

---

## File Map

Backend data contract:

- Modify `dto/channel_settings.go`: add channel auto-priority score snapshot types and fields inside `ChannelOtherSettings`.
- Modify `dto/upstream_source.go`: add source-level and rule-level automatic-priority request/response fields.
- Modify `service/upstream_source_rule.go`: parse, normalize, and resolve automatic-priority config with rule overrides.
- Modify `controller/upstream_source.go`: persist automatic-priority config in `sync_config` and expose it in API responses.

Backend scoring and write path:

- Create `service/channel_auto_priority_stats.go`: collect and parse real usage logs for cache-adjusted effective cost and first-token latency.
- Create `service/channel_auto_priority_score.go`: normalize candidate metrics and compute priority values.
- Create `service/upstream_source_auto_priority.go`: resolve eligible generated channels, compute scores, apply snapshots and priority writes.
- Create `service/upstream_source_auto_priority_worker.go`: background worker and due-source listing.
- Modify `controller/channel-test.go`: mark channel-test consume logs with `is_channel_test`.
- Modify `controller/upstream_source.go`: manual run endpoint.
- Modify `router/api-router.go`: route `POST /api/upstream_sources/:id/auto_priority/run`.
- Modify `main.go`: start automatic-priority worker.

Backend tests:

- Modify `service/upstream_source_rule_test.go`.
- Modify `controller/upstream_source_test.go`.
- Create `service/channel_auto_priority_stats_test.go`.
- Create `service/channel_auto_priority_score_test.go`.
- Create `service/upstream_source_auto_priority_test.go`.
- Modify `controller/channel_monitor_test.go` or `controller/channel_test_internal_test.go` for the channel-test marker.

Frontend:

- Modify `web/default/src/features/upstream-sources/types.ts`.
- Modify `web/default/src/features/upstream-sources/rules.ts`.
- Modify `web/default/src/features/upstream-sources/rules.test.ts`.
- Modify `web/default/src/features/upstream-sources/index.tsx`.
- Modify `web/default/src/features/channels/types.ts`.
- Modify `web/default/src/features/channels/components/channels-columns.tsx`.
- Add or update targeted frontend tests near the changed files.
- Update `web/default/src/i18n/locales/*.json` through `bun run i18n:sync`.

---

## Task 1: Backend Config Contract

**Files:**
- Modify: `dto/channel_settings.go`
- Modify: `dto/upstream_source.go`
- Modify: `service/upstream_source_rule.go`
- Modify: `service/upstream_source_rule_test.go`
- Modify: `controller/upstream_source.go`
- Modify: `controller/upstream_source_test.go`

- [ ] **Step 1: Write failing service config tests**

Add these tests to `service/upstream_source_rule_test.go`:

```go
func TestParseUpstreamSourceSyncConfigSupportsAutoPriority(t *testing.T) {
	raw := `{
		"auto_priority_enabled": true,
		"auto_priority_interval_minutes": 3,
		"auto_priority_window_hours": 999,
		"local_group_rules": [{
			"name": "cheap gpt",
			"platforms": ["openai"],
			"name_contains": ["gpt"],
			"auto_priority": {
				"enabled": false,
				"interval_minutes": 0,
				"window_hours": 48
			}
		}]
	}`

	config, err := parseUpstreamSourceSyncConfig(raw)

	require.NoError(t, err)
	assert.True(t, config.AutoPriorityEnabled)
	assert.Equal(t, 3, config.AutoPriorityIntervalMinutes)
	assert.Equal(t, 168, config.AutoPriorityWindowHours)
	require.Len(t, config.LocalGroupRules, 1)
	require.NotNil(t, config.LocalGroupRules[0].AutoPriority)
	require.NotNil(t, config.LocalGroupRules[0].AutoPriority.Enabled)
	assert.False(t, *config.LocalGroupRules[0].AutoPriority.Enabled)
	require.NotNil(t, config.LocalGroupRules[0].AutoPriority.IntervalMinutes)
	require.NotNil(t, config.LocalGroupRules[0].AutoPriority.WindowHours)
	assert.Equal(t, 0, *config.LocalGroupRules[0].AutoPriority.IntervalMinutes)
	assert.Equal(t, 48, *config.LocalGroupRules[0].AutoPriority.WindowHours)
}

func TestResolveUpstreamSourceRuleAutoPriorityOverridesFallback(t *testing.T) {
	enabled := true
	config, err := parseUpstreamSourceSyncConfig(`{
		"auto_priority_enabled": false,
		"auto_priority_interval_minutes": 30,
		"auto_priority_window_hours": 24,
		"local_group_rules": [{
			"name": "pro",
			"platforms": ["openai"],
			"name_contains": ["pro"],
			"auto_priority": {
				"enabled": true,
				"interval_minutes": 7,
				"window_hours": 72
			}
		}]
	}`)
	require.NoError(t, err)
	config.LocalGroupRules[0].AutoPriority.Enabled = &enabled

	resolution := resolveUpstreamSourceRule(config, &model.UpstreamSourceChannelMapping{
		SyncEnabled:       true,
		DiscoveryStatus:   model.UpstreamMappingDiscoveryStatusActive,
		UpstreamPlatform:  "openai",
		UpstreamGroupName: "ChatGPT Pro",
	})

	require.True(t, resolution.SyncEligible)
	assert.True(t, resolution.AutoPriorityEnabled)
	assert.Equal(t, 7, resolution.AutoPriorityIntervalMinutes)
	assert.Equal(t, 72, resolution.AutoPriorityWindowHours)
}
```

- [ ] **Step 2: Run tests and verify they fail**

Run:

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./service -run "TestParseUpstreamSourceSyncConfigSupportsAutoPriority|TestResolveUpstreamSourceRuleAutoPriorityOverridesFallback" -count=1
```

Expected: compile failure because automatic-priority fields and rule DTOs do not exist yet.

- [ ] **Step 3: Add DTO fields**

In `dto/upstream_source.go`, add presence-aware fields to `UpstreamSourceCreateRequest` and `UpstreamSourceUpdateRequest`:

```go
AutoPriorityEnabled         bool `json:"auto_priority_enabled"`
AutoPriorityIntervalMinutes *int `json:"auto_priority_interval_minutes,omitempty"`
AutoPriorityWindowHours     *int `json:"auto_priority_window_hours,omitempty"`
```

Add normalized response fields to `UpstreamSourceResponse`:

```go
AutoPriorityEnabled         bool `json:"auto_priority_enabled"`
AutoPriorityIntervalMinutes int  `json:"auto_priority_interval_minutes"`
AutoPriorityWindowHours     int  `json:"auto_priority_window_hours"`
```

Add to `UpstreamSourceLocalGroupRule`:

```go
AutoPriority *UpstreamSourceRuleAutoPriority `json:"auto_priority,omitempty"`
```

Add this DTO type:

```go
type UpstreamSourceRuleAutoPriority struct {
	Enabled         *bool `json:"enabled,omitempty"`
	IntervalMinutes *int  `json:"interval_minutes,omitempty"`
	WindowHours     *int  `json:"window_hours,omitempty"`
}
```

In `dto/channel_settings.go`, add score types below `ChannelOtherSettings`:

```go
type ChannelAutoPriorityScore struct {
	Version                 string  `json:"version,omitempty"`
	ComputedAt              int64   `json:"computed_at"`
	WindowStart             int64   `json:"window_start"`
	WindowEnd               int64   `json:"window_end"`
	Cohort                  string  `json:"cohort,omitempty"`
	EffectiveRateMultiplier float64 `json:"effective_rate_multiplier"`
	CacheAdjustedCostFactor float64 `json:"cache_adjusted_cost_factor"`
	EffectiveCostMultiplier float64 `json:"effective_cost_multiplier"`
	EffectivePriceScore     float64 `json:"effective_price_score"`
	AvailabilityScore       float64 `json:"availability_score"`
	FirstTokenScore         float64 `json:"first_token_score"`
	FinalScore              float64 `json:"final_score"`
	OldPriority             int64   `json:"old_priority"`
	NewPriority             int64   `json:"new_priority"`
	Applied                 bool    `json:"applied"`
	Reason                  string  `json:"reason,omitempty"`
	UsageLogCount           int64   `json:"usage_log_count"`
	MonitorCheckCount       int64   `json:"monitor_check_count"`
	FirstTokenSampleCount   int64   `json:"first_token_sample_count"`
}
```

Add these fields to `ChannelOtherSettings`:

```go
ChannelAutoPriorityEnabled         bool                      `json:"channel_auto_priority_enabled,omitempty"`
ChannelAutoPriorityIntervalMinutes int                       `json:"channel_auto_priority_interval_minutes,omitempty"`
ChannelAutoPriorityWindowHours     int                       `json:"channel_auto_priority_window_hours,omitempty"`
ChannelAutoPriorityLastRunAt       int64                     `json:"channel_auto_priority_last_run_at,omitempty"`
ChannelAutoPriorityLastAppliedAt   int64                     `json:"channel_auto_priority_last_applied_at,omitempty"`
ChannelAutoPriorityLastScore       *ChannelAutoPriorityScore `json:"channel_auto_priority_last_score,omitempty"`
```

- [ ] **Step 4: Add service config normalization and resolution**

In `service/upstream_source_rule.go`, add constants:

```go
const (
	defaultUpstreamSourceAutoPriorityIntervalMinutes = 30
	defaultUpstreamSourceAutoPriorityWindowHours     = 24
	maximumUpstreamSourceAutoPriorityWindowHours     = 168
)
```

Add fields to `upstreamSourceSyncConfig`:

```go
AutoPriorityEnabled         bool `json:"auto_priority_enabled"`
AutoPriorityIntervalMinutes int  `json:"auto_priority_interval_minutes"`
AutoPriorityWindowHours     int  `json:"auto_priority_window_hours"`
```

Add fields to `upstreamSourceRuleResolution`:

```go
AutoPriorityEnabled         bool
AutoPriorityIntervalMinutes int
AutoPriorityWindowHours     int
```

Add normalization helpers:

```go
func normalizeUpstreamSourceAutoPriorityInterval(intervalMinutes int) int {
	if intervalMinutes < 0 {
		return defaultUpstreamSourceAutoPriorityIntervalMinutes
	}
	if intervalMinutes > 0 && intervalMinutes < 5 {
		return 5
	}
	return intervalMinutes
}

func normalizeUpstreamSourceAutoPriorityWindowHours(windowHours int) int {
	if windowHours <= 0 {
		return defaultUpstreamSourceAutoPriorityWindowHours
	}
	if windowHours > maximumUpstreamSourceAutoPriorityWindowHours {
		return maximumUpstreamSourceAutoPriorityWindowHours
	}
	return windowHours
}

func normalizeUpstreamSourceRuleAutoPriority(autoPriority *dto.UpstreamSourceRuleAutoPriority) *dto.UpstreamSourceRuleAutoPriority {
	if autoPriority == nil {
		return nil
	}
	return &dto.UpstreamSourceRuleAutoPriority{
		Enabled:         cloneUpstreamSourceRuleBool(autoPriority.Enabled),
		IntervalMinutes: normalizeUpstreamSourceAutoPriorityInterval(autoPriority.IntervalMinutes),
		WindowHours:     normalizeUpstreamSourceAutoPriorityWindowHours(autoPriority.WindowHours),
	}
}
```

Update `normalizeUpstreamSourceSyncConfig`, `normalizeUpstreamSourceLocalGroupRules`, `upstreamSourceRuleFallbackResolution`, and `resolveUpstreamSourceMatchedRule` so source and rule automatic-priority fields follow the same fallback style as monitor and auto-sync settings.

- [ ] **Step 5: Add controller config persistence and response fields**

In `controller/upstream_source.go`, add fields to `upstreamSourceControllerSyncConfig`:

```go
AutoPriorityEnabled         bool `json:"auto_priority_enabled"`
AutoPriorityIntervalMinutes int  `json:"auto_priority_interval_minutes"`
AutoPriorityWindowHours     int  `json:"auto_priority_window_hours"`
```

Pass create/update request fields through `marshalUpstreamSourceSyncConfig`, and return them from `upstreamSourceResponse`.

Add controller normalization helpers equivalent to the service normalization:

```go
func normalizeUpstreamSourceControllerAutoPriorityInterval(intervalMinutes int) int {
	if intervalMinutes < 0 {
		return 30
	}
	if intervalMinutes > 0 && intervalMinutes < 5 {
		return 5
	}
	return intervalMinutes
}

func normalizeUpstreamSourceControllerAutoPriorityWindowHours(windowHours int) int {
	if windowHours <= 0 {
		return 24
	}
	if windowHours > 168 {
		return 168
	}
	return windowHours
}
```

- [ ] **Step 6: Add controller API tests**

In `controller/upstream_source_test.go`, add a test that creates and updates an upstream source with source-level automatic-priority settings and a rule override. Assert the response echoes the normalized fields and that `SyncConfig` stores:

```json
"auto_priority_enabled": true
"auto_priority_interval_minutes": 5
"auto_priority_window_hours": 168
"auto_priority": {"enabled": false, "interval_minutes": 0, "window_hours": 48}
```

- [ ] **Step 7: Run focused tests**

Run:

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./service ./controller -run "AutoPriority|UpstreamSource" -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit**

```powershell
git add dto/channel_settings.go dto/upstream_source.go service/upstream_source_rule.go service/upstream_source_rule_test.go controller/upstream_source.go controller/upstream_source_test.go
git commit -m "feat: add upstream auto priority config"
```

---

## Task 2: Usage Stats and Effective Cost

**Files:**
- Create: `service/channel_auto_priority_stats.go`
- Create: `service/channel_auto_priority_stats_test.go`
- Modify: `controller/channel-test.go`
- Modify: `controller/channel_test_internal_test.go`

- [ ] **Step 1: Write failing stats tests**

Create `service/channel_auto_priority_stats_test.go` with tests for cache-adjusted effective cost:

```go
package service

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func autoPriorityLog(t *testing.T, channelID int, content string, tokenName string, prompt int, completion int, other map[string]interface{}) model.Log {
	t.Helper()
	raw, err := common.Marshal(other)
	require.NoError(t, err)
	return model.Log{
		Type:             model.LogTypeConsume,
		ChannelId:        channelID,
		Content:          content,
		TokenName:        tokenName,
		PromptTokens:     prompt,
		CompletionTokens: completion,
		Other:            string(raw),
		CreatedAt:        1000,
	}
}

func TestBuildAutoPriorityUsageStatsIncludesCacheInEffectiveCost(t *testing.T) {
	logs := []model.Log{
		autoPriorityLog(t, 1, "real", "prod", 1000, 100, map[string]interface{}{
			"input_tokens_total": float64(1000),
			"cache_tokens":       float64(800),
			"cache_ratio":        float64(0.1),
			"completion_ratio":   float64(2),
			"frt":                float64(1200),
		}),
	}

	stats := buildAutoPriorityUsageStatsFromLogs(logs, 0)

	require.Contains(t, stats, 1)
	assert.Equal(t, int64(1), stats[1].UsageLogCount)
	assert.Equal(t, int64(1), stats[1].FirstTokenSampleCount)
	assert.InDelta(t, 480.0/1200.0, stats[1].CacheAdjustedCostFactor, 0.0001)
	assert.InDelta(t, 1200, stats[1].AverageFirstTokenLatencyMS, 0.0001)
}

func TestBuildAutoPriorityUsageStatsHandlesCacheCreationCost(t *testing.T) {
	logs := []model.Log{
		autoPriorityLog(t, 1, "real", "prod", 1000, 0, map[string]interface{}{
			"input_tokens_total":       float64(1000),
			"cache_tokens":             float64(300),
			"cache_ratio":              float64(0.1),
			"cache_creation_tokens_5m":  float64(200),
			"cache_creation_ratio_5m":   float64(1.25),
			"cache_creation_tokens_1h":  float64(100),
			"cache_creation_ratio_1h":   float64(2.0),
			"completion_ratio":         float64(1),
		}),
	}

	stats := buildAutoPriorityUsageStatsFromLogs(logs, 0)

	require.Contains(t, stats, 1)
	assert.InDelta(t, 880.0/1000.0, stats[1].CacheAdjustedCostFactor, 0.0001)
}

func TestBuildAutoPriorityUsageStatsExcludesChannelTests(t *testing.T) {
	logs := []model.Log{
		autoPriorityLog(t, 1, "real", "prod", 100, 0, map[string]interface{}{"frt": float64(300)}),
		autoPriorityLog(t, 1, "模型测试", "模型测试", 1000, 0, map[string]interface{}{"frt": float64(50)}),
		autoPriorityLog(t, 1, "real", "prod", 1000, 0, map[string]interface{}{"is_channel_test": true, "frt": float64(40)}),
	}

	stats := buildAutoPriorityUsageStatsFromLogs(logs, 0)

	require.Contains(t, stats, 1)
	assert.Equal(t, int64(1), stats[1].UsageLogCount)
	assert.Equal(t, int64(1), stats[1].FirstTokenSampleCount)
	assert.InDelta(t, 300, stats[1].AverageFirstTokenLatencyMS, 0.0001)
}
```

- [ ] **Step 2: Run tests and verify they fail**

Run:

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./service -run "TestBuildAutoPriorityUsageStats" -count=1
```

Expected: compile failure because `buildAutoPriorityUsageStatsFromLogs` does not exist.

- [ ] **Step 3: Implement usage stats parser**

Create `service/channel_auto_priority_stats.go`:

```go
package service

import (
	"math"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

type AutoPriorityUsageStats struct {
	ChannelID                  int
	UsageLogCount              int64
	NormalCostUnits            float64
	AdjustedCostUnits          float64
	CacheAdjustedCostFactor    float64
	FirstTokenSampleCount      int64
	FirstTokenLatencyTotalMS   float64
	AverageFirstTokenLatencyMS float64
}

type autoPriorityLogOther struct {
	CompletionRatio       float64 `json:"completion_ratio"`
	CacheTokens           float64 `json:"cache_tokens"`
	CacheRatio            float64 `json:"cache_ratio"`
	CacheCreationTokens   float64 `json:"cache_creation_tokens"`
	CacheCreationRatio    float64 `json:"cache_creation_ratio"`
	CacheCreationTokens5m float64 `json:"cache_creation_tokens_5m"`
	CacheCreationRatio5m  float64 `json:"cache_creation_ratio_5m"`
	CacheCreationTokens1h float64 `json:"cache_creation_tokens_1h"`
	CacheCreationRatio1h  float64 `json:"cache_creation_ratio_1h"`
	CacheWriteTokens      float64 `json:"cache_write_tokens"`
	InputTokensTotal      float64 `json:"input_tokens_total"`
	FirstResponseTimeMS   float64 `json:"frt"`
	IsChannelTest         bool    `json:"is_channel_test"`
}
```

Implement:

```go
func CollectAutoPriorityUsageStats(channelIDs []int, windowStart int64) (map[int]AutoPriorityUsageStats, error) {
	stats := make(map[int]AutoPriorityUsageStats, len(channelIDs))
	if len(channelIDs) == 0 {
		return stats, nil
	}

	var logs []model.Log
	if err := model.LOG_DB.Model(&model.Log{}).
		Where("type = ? AND channel_id IN ? AND created_at >= ?", model.LogTypeConsume, channelIDs, windowStart).
		Find(&logs).Error; err != nil {
		return nil, err
	}
	return buildAutoPriorityUsageStatsFromLogs(logs, windowStart), nil
}

func buildAutoPriorityUsageStatsFromLogs(logs []model.Log, windowStart int64) map[int]AutoPriorityUsageStats {
	stats := make(map[int]AutoPriorityUsageStats)
	for _, log := range logs {
		if log.ChannelId == 0 || log.Type != model.LogTypeConsume || log.CreatedAt < windowStart {
			continue
		}
		var other autoPriorityLogOther
		if strings.TrimSpace(log.Other) != "" {
			if err := common.UnmarshalJsonStr(log.Other, &other); err != nil {
				continue
			}
		}
		if isAutoPriorityChannelTestLog(log, other) {
			continue
		}

		stat := stats[log.ChannelId]
		stat.ChannelID = log.ChannelId
		stat.UsageLogCount++

		normal, adjusted := autoPriorityCostUnits(log, other)
		if normal > 0 && adjusted >= 0 {
			stat.NormalCostUnits += normal
			stat.AdjustedCostUnits += adjusted
		}

		if other.FirstResponseTimeMS > 0 {
			stat.FirstTokenSampleCount++
			stat.FirstTokenLatencyTotalMS += other.FirstResponseTimeMS
		}
		stats[log.ChannelId] = stat
	}

	for channelID, stat := range stats {
		if stat.NormalCostUnits > 0 {
			stat.CacheAdjustedCostFactor = stat.AdjustedCostUnits / stat.NormalCostUnits
		} else {
			stat.CacheAdjustedCostFactor = 1
		}
		if stat.FirstTokenSampleCount > 0 {
			stat.AverageFirstTokenLatencyMS = stat.FirstTokenLatencyTotalMS / float64(stat.FirstTokenSampleCount)
		}
		stats[channelID] = stat
	}
	return stats
}
```

Implement helper behavior exactly:

```go
func isAutoPriorityChannelTestLog(log model.Log, other autoPriorityLogOther) bool {
	if other.IsChannelTest {
		return true
	}
	return strings.TrimSpace(log.Content) == "模型测试" || strings.TrimSpace(log.TokenName) == "模型测试"
}

func autoPriorityCostUnits(log model.Log, other autoPriorityLogOther) (normal float64, adjusted float64) {
	inputTotal := other.InputTokensTotal
	if inputTotal <= 0 {
		inputTotal = float64(log.PromptTokens)
	}
	completionRatio := other.CompletionRatio
	if completionRatio <= 0 {
		completionRatio = 1
	}
	completionUnits := float64(log.CompletionTokens) * completionRatio
	if inputTotal <= 0 && completionUnits <= 0 {
		return 0, 0
	}

	cacheReadTokens := clampNonNegative(other.CacheTokens)
	cacheReadRatio := other.CacheRatio
	if cacheReadRatio < 0 {
		cacheReadRatio = 0
	}

	cacheWriteUnits, cacheWriteTokens := autoPriorityCacheWriteUnits(other)
	uncachedInput := math.Max(inputTotal-cacheReadTokens-cacheWriteTokens, 0)

	normal = inputTotal + completionUnits
	adjusted = uncachedInput + cacheReadTokens*cacheReadRatio + cacheWriteUnits + completionUnits
	return normal, adjusted
}
```

`autoPriorityCacheWriteUnits` must include every cache creation bucket present in
the log, including `cache_creation_tokens`, `cache_creation_tokens_5m`, and
`cache_creation_tokens_1h`. Cache creation ratios greater than `1` raise the
effective cost factor; they are not an independent quality metric.

- [ ] **Step 4: Mark channel-test logs**

In `controller/channel-test.go`, update `buildTestLogOther` after `GenerateTextOtherInfo`:

```go
other["is_channel_test"] = true
```

In `controller/channel_test_internal_test.go`, add:

```go
func TestBuildTestLogOtherMarksChannelTest(t *testing.T) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	info := &relaycommon.RelayInfo{StartTime: time.Unix(1, 0), FirstResponseTime: time.Unix(1, 0)}
	usage := &dto.Usage{PromptTokens: 10}

	other := buildTestLogOther(ctx, info, types.PriceData{ModelRatio: 1, CompletionRatio: 1}, usage, nil)

	assert.Equal(t, true, other["is_channel_test"])
}
```

- [ ] **Step 5: Run focused tests**

Run:

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./service ./controller -run "TestBuildAutoPriorityUsageStats|TestBuildTestLogOtherMarksChannelTest|TestRecordChannelTestConsumeLogSkipsMonitorProbes" -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```powershell
git add service/channel_auto_priority_stats.go service/channel_auto_priority_stats_test.go controller/channel-test.go controller/channel_test_internal_test.go
git commit -m "feat: derive auto priority usage cost stats"
```

---

## Task 3: Scoring Engine

**Files:**
- Create: `service/channel_auto_priority_score.go`
- Create: `service/channel_auto_priority_score_test.go`

- [ ] **Step 1: Write failing scoring tests**

Create `service/channel_auto_priority_score_test.go`:

```go
package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScoreAutoPriorityCandidatesUsesEffectivePriceDominantly(t *testing.T) {
	inputs := []AutoPriorityScoreInput{
		{ChannelID: 1, LocalGroup: "OpenAI", ChannelType: 1, CurrentPriority: 0, EffectiveRateMultiplier: 0.01, CacheAdjustedCostFactor: 1, Availability: floatPtr(0.50), FirstTokenLatencyMS: 5000, UsageLogCount: 1, MonitorCheckCount: 10, FirstTokenSampleCount: 1},
		{ChannelID: 2, LocalGroup: "OpenAI", ChannelType: 1, CurrentPriority: 0, EffectiveRateMultiplier: 0.10, CacheAdjustedCostFactor: 1, Availability: floatPtr(1.00), FirstTokenLatencyMS: 200, UsageLogCount: 1, MonitorCheckCount: 10, FirstTokenSampleCount: 1},
	}

	results := ScoreAutoPriorityCandidates(inputs, 1000)

	require.Len(t, results, 2)
	assert.Greater(t, results[0].NewPriority, results[1].NewPriority)
	assert.Greater(t, results[0].EffectivePriceScore, results[1].EffectivePriceScore)
}

func TestScoreAutoPriorityCandidatesGroupsByCohort(t *testing.T) {
	inputs := []AutoPriorityScoreInput{
		{ChannelID: 1, LocalGroup: "OpenAI", ChannelType: 1, CurrentPriority: 0, EffectiveRateMultiplier: 0.01, CacheAdjustedCostFactor: 1},
		{ChannelID: 2, LocalGroup: "OpenAI-Pro", ChannelType: 1, CurrentPriority: 0, EffectiveRateMultiplier: 0.10, CacheAdjustedCostFactor: 1},
	}

	results := ScoreAutoPriorityCandidates(inputs, 1000)

	require.Len(t, results, 2)
	assert.Equal(t, float64(100), results[0].EffectivePriceScore)
	assert.Equal(t, float64(100), results[1].EffectivePriceScore)
}

func TestScoreAutoPriorityCandidatesAppliesHysteresis(t *testing.T) {
	inputs := []AutoPriorityScoreInput{
		{ChannelID: 1, LocalGroup: "OpenAI", ChannelType: 1, CurrentPriority: 995, EffectiveRateMultiplier: 0.01, CacheAdjustedCostFactor: 1, HasPreviousSnapshot: true},
	}

	results := ScoreAutoPriorityCandidates(inputs, 1000)

	require.Len(t, results, 1)
	assert.False(t, results[0].Applied)
	assert.Equal(t, "hysteresis_delta_below_threshold", results[0].Reason)
	assert.Equal(t, int64(995), results[0].OldPriority)
	assert.Equal(t, int64(1000), results[0].ComputedPriority)
}

func floatPtr(v float64) *float64 {
	return &v
}
```

- [ ] **Step 2: Run tests and verify they fail**

Run:

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./service -run "TestScoreAutoPriorityCandidates" -count=1
```

Expected: compile failure because scoring types and functions do not exist.

- [ ] **Step 3: Implement scoring types**

Create `service/channel_auto_priority_score.go`:

```go
package service

import (
	"fmt"
	"math"
	"strings"
)

const (
	autoPriorityScoreVersion              = "v1"
	autoPriorityHysteresisThreshold int64 = 10
	autoPriorityDefaultScore              = 70.0
)

type AutoPriorityScoreInput struct {
	ChannelID                int
	LocalGroup               string
	ChannelType              int
	CurrentPriority          int64
	EffectiveRateMultiplier  float64
	CacheAdjustedCostFactor  float64
	Availability             *float64
	FirstTokenLatencyMS      float64
	UsageLogCount            int64
	MonitorCheckCount        int64
	FirstTokenSampleCount    int64
	HasPreviousSnapshot      bool
}

type AutoPriorityScoreResult struct {
	ChannelID                 int
	Cohort                    string
	EffectiveRateMultiplier   float64
	CacheAdjustedCostFactor   float64
	EffectiveCostMultiplier   float64
	EffectivePriceScore       float64
	AvailabilityScore         float64
	FirstTokenScore           float64
	FinalScore                float64
	OldPriority               int64
	ComputedPriority          int64
	NewPriority               int64
	Applied                   bool
	Reason                    string
	UsageLogCount             int64
	MonitorCheckCount         int64
	FirstTokenSampleCount     int64
}
```

Implement `ScoreAutoPriorityCandidates(inputs []AutoPriorityScoreInput, maxPriority int64) []AutoPriorityScoreResult`:

- set `maxPriority` to `1000` when non-positive;
- cohort key is `strings.TrimSpace(LocalGroup) + "#" + fmt.Sprint(ChannelType)`;
- effective cost is `EffectiveRateMultiplier * CacheAdjustedCostFactor`;
- if `CacheAdjustedCostFactor <= 0`, use `1`;
- if multiplier is invalid, score should produce `Reason: "missing_effective_rate_multiplier"` and `Applied: false`;
- price score: one valid candidate in cohort gets `100`; multiple valid costs use log normalization;
- availability: `Availability * 100`, or `70` when nil;
- first token score: `70` when no sample, otherwise inverse log normalization by cohort;
- final score formula is `0.80`, `0.17`, `0.03`;
- computed priority is `round(finalScore * 10)`, clamped to `0..maxPriority`;
- apply when no previous snapshot or absolute delta is at least `10`;
- if not applied due hysteresis, keep `NewPriority = OldPriority`, `ComputedPriority` as the calculated value, `Reason = "hysteresis_delta_below_threshold"`.

- [ ] **Step 4: Run focused tests**

Run:

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./service -run "TestScoreAutoPriorityCandidates" -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```powershell
git add service/channel_auto_priority_score.go service/channel_auto_priority_score_test.go
git commit -m "feat: score generated channel priority"
```

---

## Task 4: Apply Priority to Generated Channels

**Files:**
- Create: `service/upstream_source_auto_priority.go`
- Create: `service/upstream_source_auto_priority_test.go`
- Modify: `model/ability.go` if a single-channel priority update helper is needed.

- [ ] **Step 1: Write failing service apply tests**

Create `service/upstream_source_auto_priority_test.go` with a test database fixture:

```go
func setupAutoPriorityServiceTestDB(t *testing.T) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+strings.ReplaceAll(t.Name(), "/", "_")+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	model.LOG_DB = db
	require.NoError(t, db.AutoMigrate(
		&model.UpstreamSource{},
		&model.UpstreamSourceChannelMapping{},
		&model.Channel{},
		&model.Ability{},
		&model.ChannelMonitorLog{},
		&model.Log{},
	))
}
```

Add tests:

```go
func TestRunUpstreamSourceAutoPriorityAppliesPriorityAbilityAndSnapshot(t *testing.T) {
	setupAutoPriorityServiceTestDB(t)
	source := createAutoPrioritySource(t, true, 0, 24)
	channel, mapping := createGeneratedAutoPriorityChannel(t, source.Id, 0.01, "OpenAI", 10)
	require.NoError(t, model.RecordChannelMonitorLog(model.ChannelMonitorLog{ChannelID: channel.Id, Status: model.ChannelMonitorStatusSuccess, CheckedAt: 990}))

	result, err := (&UpstreamSourceService{}).RunAutoPriority(context.Background(), source.Id, 1000)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, result.Results, 1)
	assert.Equal(t, mapping.Id, result.Results[0].MappingID)
	assert.True(t, result.Results[0].Applied)

	var reloaded model.Channel
	require.NoError(t, model.DB.First(&reloaded, channel.Id).Error)
	require.NotNil(t, reloaded.Priority)
	assert.Equal(t, result.Results[0].NewPriority, *reloaded.Priority)

	var ability model.Ability
	require.NoError(t, model.DB.Where("channel_id = ?", channel.Id).First(&ability).Error)
	require.NotNil(t, ability.Priority)
	assert.Equal(t, result.Results[0].NewPriority, *ability.Priority)

	settings := reloaded.GetOtherSettings()
	require.NotNil(t, settings.ChannelAutoPriorityLastScore)
	assert.True(t, settings.ChannelAutoPriorityLastScore.Applied)
	assert.Equal(t, source.Id, settings.GeneratedByUpstreamSourceID)
}

func TestRunUpstreamSourceAutoPriorityHysteresisUpdatesSnapshotOnly(t *testing.T) {
	setupAutoPriorityServiceTestDB(t)
	source := createAutoPrioritySource(t, true, 0, 24)
	channel, _ := createGeneratedAutoPriorityChannel(t, source.Id, 0.01, "OpenAI", 1000)
	settings := channel.GetOtherSettings()
	settings.ChannelAutoPriorityLastScore = &dto.ChannelAutoPriorityScore{Version: "v1", ComputedAt: 1, NewPriority: 1000, Applied: true}
	channel.SetOtherSettings(settings)
	require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", channel.Id).Updates(map[string]any{
		"settings": channel.OtherSettings,
	}).Error)

	result, err := (&UpstreamSourceService{}).RunAutoPriority(context.Background(), source.Id, 2000)

	require.NoError(t, err)
	require.Len(t, result.Results, 1)
	assert.False(t, result.Results[0].Applied)
	assert.Equal(t, "hysteresis_delta_below_threshold", result.Results[0].Reason)

	var reloaded model.Channel
	require.NoError(t, model.DB.First(&reloaded, channel.Id).Error)
	require.NotNil(t, reloaded.Priority)
	assert.Equal(t, int64(1000), *reloaded.Priority)
	settings = reloaded.GetOtherSettings()
	require.NotNil(t, settings.ChannelAutoPriorityLastScore)
	assert.False(t, settings.ChannelAutoPriorityLastScore.Applied)
	assert.Equal(t, "hysteresis_delta_below_threshold", settings.ChannelAutoPriorityLastScore.Reason)
}

func TestRunUpstreamSourceAutoPrioritySkipsNonGeneratedChannels(t *testing.T) {
	setupAutoPriorityServiceTestDB(t)
	source := createAutoPrioritySource(t, true, 0, 24)
	channel, _ := createManualMappedChannelWithoutGeneratedMetadata(t, source.Id, 0.01, "OpenAI", 10)

	result, err := (&UpstreamSourceService{}).RunAutoPriority(context.Background(), source.Id, 1000)

	require.NoError(t, err)
	require.Len(t, result.Results, 1)
	assert.False(t, result.Results[0].Applied)
	assert.Equal(t, "generated_channel_metadata_mismatch", result.Results[0].Reason)

	var reloaded model.Channel
	require.NoError(t, model.DB.First(&reloaded, channel.Id).Error)
	require.NotNil(t, reloaded.Priority)
	assert.Equal(t, int64(10), *reloaded.Priority)
}
```

- [ ] **Step 2: Run tests and verify they fail**

Run:

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./service -run "TestRunUpstreamSourceAutoPriority" -count=1
```

Expected: compile failure because `RunAutoPriority` and result DTOs do not exist.

- [ ] **Step 3: Add result DTOs**

In `dto/upstream_source.go`, add:

```go
type UpstreamSourceAutoPriorityChannelResult struct {
	MappingID                int     `json:"mapping_id"`
	LocalChannelID           int     `json:"local_channel_id"`
	OldPriority              int64   `json:"old_priority"`
	NewPriority              int64   `json:"new_priority"`
	ComputedPriority         int64   `json:"computed_priority"`
	Applied                  bool    `json:"applied"`
	Reason                   string  `json:"reason,omitempty"`
	EffectiveRateMultiplier  float64 `json:"effective_rate_multiplier,omitempty"`
	CacheAdjustedCostFactor  float64 `json:"cache_adjusted_cost_factor,omitempty"`
	EffectiveCostMultiplier  float64 `json:"effective_cost_multiplier,omitempty"`
	EffectivePriceScore      float64 `json:"effective_price_score,omitempty"`
	AvailabilityScore        float64 `json:"availability_score,omitempty"`
	FirstTokenScore          float64 `json:"first_token_score,omitempty"`
	FinalScore               float64 `json:"final_score,omitempty"`
}

type UpstreamSourceAutoPriorityResult struct {
	SourceID int                                       `json:"source_id"`
	Updated  int                                       `json:"updated"`
	Skipped  int                                       `json:"skipped"`
	Failed   int                                       `json:"failed"`
	Results  []UpstreamSourceAutoPriorityChannelResult `json:"results"`
	Error    string                                    `json:"error,omitempty"`
}
```

- [ ] **Step 4: Implement generated-channel resolver and apply path**

Create `service/upstream_source_auto_priority.go`.

Implement:

```go
func (s *UpstreamSourceService) RunAutoPriority(ctx context.Context, sourceID int, now int64) (*dto.UpstreamSourceAutoPriorityResult, error)
```

Behavior:

- load enabled source by `sourceID`;
- parse sync config;
- load mappings by source ID ordered by ID;
- for each mapping, call `resolveUpstreamSourceRule`;
- skip if `AutoPriorityEnabled` is false, `SyncEligible` is false, `LocalChannelID == 0`, or mapping lacks `EffectiveRateMultiplier`;
- load channel and verify `channel.GetOtherSettings().GeneratedByUpstreamSourceID == source.Id` and `GeneratedByUpstreamMappingID == mapping.Id`;
- collect `AutoPriorityScoreInput` values;
- collect monitor stats through `model.GetChannelMonitorStats`;
- collect usage stats through `CollectAutoPriorityUsageStats`;
- score via `ScoreAutoPriorityCandidates`;
- apply each result using a transaction.

Implement apply helper:

```go
func applyAutoPriorityResult(channel *model.Channel, resolution upstreamSourceRuleResolution, score AutoPriorityScoreResult, now int64) error
```

The transaction must:

- read current settings from the channel;
- write `ChannelAutoPriorityEnabled`, interval, window, last run, and score snapshot;
- if `score.Applied`, update `channels.priority`, `channels.settings`, and `abilities.priority`;
- if not applied, update only `channels.settings`;
- set `ChannelAutoPriorityLastAppliedAt` only when priority changed.

Use GORM APIs:

```go
tx.Model(&model.Channel{}).Where("id = ?", channel.Id).Updates(map[string]any{
	"priority": score.NewPriority,
	"settings": channel.OtherSettings,
})
tx.Model(&model.Ability{}).Where("channel_id = ?", channel.Id).Update("priority", score.NewPriority)
```

After at least one applied priority change, call `model.InitChannelCache()`.

- [ ] **Step 5: Run focused tests**

Run:

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./service -run "TestRunUpstreamSourceAutoPriority" -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```powershell
git add dto/upstream_source.go service/upstream_source_auto_priority.go service/upstream_source_auto_priority_test.go
git commit -m "feat: apply auto priority to generated channels"
```

---

## Task 5: Manual Endpoint and Background Worker

**Files:**
- Create: `service/upstream_source_auto_priority_worker.go`
- Modify: `service/upstream_source_auto_priority_test.go`
- Modify: `controller/upstream_source.go`
- Modify: `controller/upstream_source_test.go`
- Modify: `router/api-router.go`
- Modify: `main.go`

- [ ] **Step 1: Write failing due-list and endpoint tests**

In `service/upstream_source_auto_priority_test.go`, add:

```go
func TestListDueUpstreamSourcesForAutoPriorityHonorsIntervalZero(t *testing.T) {
	setupAutoPriorityServiceTestDB(t)
	source := createAutoPrioritySource(t, true, 0, 24)
	_, mapping := createGeneratedAutoPriorityChannel(t, source.Id, 0.01, "OpenAI", 1000)
	require.NoError(t, model.DB.Model(&model.UpstreamSourceChannelMapping{}).Where("id = ?", mapping.Id).Update("last_synced_at", 1999).Error)

	due, err := ListDueUpstreamSourcesForAutoPriority(2000)

	require.NoError(t, err)
	require.Len(t, due, 1)
	assert.Equal(t, source.Id, due[0].Id)
}

func TestListDueUpstreamSourcesForAutoPrioritySkipsNotDue(t *testing.T) {
	setupAutoPriorityServiceTestDB(t)
	source := createAutoPrioritySource(t, true, 30, 24)
	channel, _ := createGeneratedAutoPriorityChannel(t, source.Id, 0.01, "OpenAI", 1000)
	settings := channel.GetOtherSettings()
	settings.ChannelAutoPriorityLastRunAt = 1900
	channel.SetOtherSettings(settings)
	require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", channel.Id).Update("settings", channel.OtherSettings).Error)

	due, err := ListDueUpstreamSourcesForAutoPriority(2000)

	require.NoError(t, err)
	assert.Empty(t, due)
}
```

In `controller/upstream_source_test.go`, add a test for:

```text
POST /api/upstream_sources/:id/auto_priority/run
```

Assert the JSON response has `success=true`, the source ID, and a `results` array.

- [ ] **Step 2: Run tests and verify they fail**

Run:

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./service ./controller -run "AutoPriority" -count=1
```

Expected: compile failure because due listing and controller endpoint do not exist.

- [ ] **Step 3: Implement worker**

Create `service/upstream_source_auto_priority_worker.go`:

```go
package service

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"

	"github.com/bytedance/gopkg/util/gopool"
)

const (
	upstreamSourceAutoPriorityTickInterval  = time.Minute
	upstreamSourceAutoPrioritySourceTimeout = 2 * time.Minute
)

var (
	upstreamSourceAutoPriorityOnce    sync.Once
	upstreamSourceAutoPriorityRunning atomic.Bool
)
```

Implement:

```go
func ListDueUpstreamSourcesForAutoPriority(now int64) ([]model.UpstreamSource, error)
func StartUpstreamSourceAutoPriorityWorker()
func runDueUpstreamSourceAutoPriorityOnce()
```

Use the same master-node and atomic-running pattern as `service/upstream_source_autosync.go`.

Due logic:

- source status must be enabled;
- config must contain source or rule automatic-priority schedule;
- at least one eligible mapping/channel must be due;
- interval `0` means due;
- otherwise due when `now - settings.ChannelAutoPriorityLastRunAt >= intervalMinutes * 60`.

- [ ] **Step 4: Implement endpoint and route**

In `controller/upstream_source.go`, add:

```go
func RunUpstreamSourceAutoPriority(c *gin.Context) {
	source, ok := loadUpstreamSourceForController(c)
	if !ok {
		return
	}
	result, err := (&service.UpstreamSourceService{}).RunAutoPriority(c.Request.Context(), source.Id, common.GetTimestamp())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "upstream_source.auto_priority_run", map[string]interface{}{
		"id":      source.Id,
		"name":    source.Name,
		"updated": result.Updated,
		"skipped": result.Skipped,
		"failed":  result.Failed,
	})
	common.ApiSuccess(c, result)
}
```

In `router/api-router.go`, add inside `upstreamSourceRoute`:

```go
upstreamSourceRoute.POST("/:id/auto_priority/run", controller.RunUpstreamSourceAutoPriority)
```

In `main.go`, call:

```go
service.StartUpstreamSourceAutoPriorityWorker()
```

next to `service.StartUpstreamSourceAutoSyncWorker()`.

- [ ] **Step 5: Run focused tests**

Run:

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./service ./controller -run "AutoPriority" -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```powershell
git add service/upstream_source_auto_priority_worker.go service/upstream_source_auto_priority_test.go controller/upstream_source.go controller/upstream_source_test.go router/api-router.go main.go
git commit -m "feat: run upstream auto priority adjustments"
```

---

## Task 6: Frontend Controls and Display

**Files:**
- Modify: `web/default/src/features/upstream-sources/types.ts`
- Modify: `web/default/src/features/upstream-sources/rules.ts`
- Modify: `web/default/src/features/upstream-sources/rules.test.ts`
- Modify: `web/default/src/features/upstream-sources/index.tsx`
- Modify: `web/default/src/features/channels/types.ts`
- Modify: `web/default/src/features/channels/components/channels-columns.tsx`
- Add or modify: frontend tests near the touched files.

- [ ] **Step 1: Update frontend types with failing tests**

In `web/default/src/features/upstream-sources/rules.test.ts`, add tests that normalize and preserve:

```ts
auto_priority: { enabled: true, interval_minutes: 0, window_hours: 48 }
```

Expected initial failure: TypeScript compile/test failure because `auto_priority` does not exist in types.

Run:

```powershell
bun test src/features/upstream-sources/rules.test.ts
```

- [ ] **Step 2: Add upstream source frontend types**

In `web/default/src/features/upstream-sources/types.ts`, add:

```ts
export type UpstreamSourceRuleAutoPriority = {
  enabled?: boolean
  interval_minutes?: number
  window_hours?: number
}
```

Add source-level fields to `UpstreamSource` and `UpstreamSourceFormValues`:

```ts
auto_priority_enabled: boolean
auto_priority_interval_minutes: number
auto_priority_window_hours: number
```

Add to `UpstreamSourceLocalGroupRule`:

```ts
auto_priority?: UpstreamSourceRuleAutoPriority
```

Add result types for manual runs:

```ts
export type UpstreamSourceAutoPriorityChannelResult = {
  mapping_id: number
  local_channel_id: number
  old_priority: number
  new_priority: number
  computed_priority: number
  applied: boolean
  reason?: string
  effective_rate_multiplier?: number
  cache_adjusted_cost_factor?: number
  effective_cost_multiplier?: number
  final_score?: number
}

export type UpstreamSourceAutoPriorityResult = {
  source_id: number
  updated: number
  skipped: number
  failed: number
  results: UpstreamSourceAutoPriorityChannelResult[]
  error?: string
}
```

- [ ] **Step 3: Preserve auto-priority in rules helpers**

In `web/default/src/features/upstream-sources/rules.ts`, update rule clone/normalize helpers to include:

```ts
...(rule.auto_priority
  ? {
      auto_priority: {
        enabled: rule.auto_priority.enabled,
        interval_minutes: Math.max(0, rule.auto_priority.interval_minutes ?? 30),
        window_hours: Math.min(168, Math.max(1, rule.auto_priority.window_hours ?? 24)),
      },
    }
  : {}),
```

Run:

```powershell
bun test src/features/upstream-sources/rules.test.ts
```

Expected: PASS.

- [ ] **Step 4: Add source-level and rule-level UI controls**

In `web/default/src/features/upstream-sources/index.tsx`:

Add constants:

```ts
const DEFAULT_AUTO_PRIORITY_INTERVAL_MINUTES = 30
const DEFAULT_AUTO_PRIORITY_WINDOW_HOURS = 24
```

Add form initialization fields:

```ts
auto_priority_enabled: source?.auto_priority_enabled ?? false,
auto_priority_interval_minutes:
  source?.auto_priority_interval_minutes ?? DEFAULT_AUTO_PRIORITY_INTERVAL_MINUTES,
auto_priority_window_hours:
  source?.auto_priority_window_hours ?? DEFAULT_AUTO_PRIORITY_WINDOW_HOURS,
```

Add create/update payload fields:

```ts
auto_priority_enabled: values.auto_priority_enabled,
auto_priority_interval_minutes: values.auto_priority_enabled
  ? Math.max(0, values.auto_priority_interval_minutes)
  : 0,
auto_priority_window_hours: Math.min(
  168,
  Math.max(1, values.auto_priority_window_hours)
),
```

Add a `SideDrawerSection` after "Sync Schedule":

```tsx
<SideDrawerSection>
  <SideDrawerSectionHeader title={t('Automatic Priority')} />
  <SwitchRow
    label={t('Enable Automatic Priority')}
    checked={form.auto_priority_enabled}
    onCheckedChange={(checked) => setField('auto_priority_enabled', checked)}
  />
  <div className='grid gap-4 sm:grid-cols-2'>
    <FieldBlock label={t('Automatic Priority Interval Minutes')} htmlFor='source-auto-priority-interval'>
      <Input
        id='source-auto-priority-interval'
        type='number'
        min={0}
        value={form.auto_priority_interval_minutes}
        disabled={!form.auto_priority_enabled}
        onChange={(event) =>
          setField('auto_priority_interval_minutes', parseIntegerInput(event.target.value, DEFAULT_AUTO_PRIORITY_INTERVAL_MINUTES))
        }
      />
    </FieldBlock>
    <FieldBlock label={t('Metrics Window Hours')} htmlFor='source-auto-priority-window'>
      <Input
        id='source-auto-priority-window'
        type='number'
        min={1}
        max={168}
        value={form.auto_priority_window_hours}
        disabled={!form.auto_priority_enabled}
        onChange={(event) =>
          setField('auto_priority_window_hours', parseIntegerInput(event.target.value, DEFAULT_AUTO_PRIORITY_WINDOW_HOURS))
        }
      />
    </FieldBlock>
  </div>
</SideDrawerSection>
```

Inside each sync rule, add a compact automatic-priority control beside monitor/auto-sync:

```tsx
<div className='grid gap-3'>
  <SwitchRow
    label={t('Automatic Priority')}
    checked={rule.auto_priority?.enabled ?? form.auto_priority_enabled}
    onCheckedChange={(checked) =>
      setLocalGroupRule(index, {
        ...rule,
        auto_priority: {
          enabled: checked,
          interval_minutes:
            rule.auto_priority?.interval_minutes ??
            form.auto_priority_interval_minutes,
          window_hours:
            rule.auto_priority?.window_hours ??
            form.auto_priority_window_hours,
        },
      })
    }
  />
  <FieldBlock label={t('Automatic Priority Interval Minutes')} htmlFor={`source-rule-auto-priority-interval-${index}`}>
    <Input
      id={`source-rule-auto-priority-interval-${index}`}
      type='number'
      min={0}
      value={rule.auto_priority?.interval_minutes ?? form.auto_priority_interval_minutes}
      onChange={(event) =>
        setLocalGroupRule(index, {
          ...rule,
          auto_priority: {
            enabled: rule.auto_priority?.enabled ?? form.auto_priority_enabled,
            interval_minutes: parseIntegerInput(event.target.value, form.auto_priority_interval_minutes),
            window_hours: rule.auto_priority?.window_hours ?? form.auto_priority_window_hours,
          },
        })
      }
    />
  </FieldBlock>
</div>
```

- [ ] **Step 5: Show channel score explanation**

In `web/default/src/features/channels/types.ts`, extend `ChannelOtherSettings`:

```ts
channel_auto_priority_enabled?: boolean
channel_auto_priority_interval_minutes?: number
channel_auto_priority_window_hours?: number
channel_auto_priority_last_run_at?: number
channel_auto_priority_last_applied_at?: number
channel_auto_priority_last_score?: {
  version?: string
  computed_at?: number
  effective_cost_multiplier?: number
  effective_price_score?: number
  availability_score?: number
  first_token_score?: number
  final_score?: number
  old_priority?: number
  new_priority?: number
  applied?: boolean
  reason?: string
}
generated_by_upstream_source_id?: number
generated_by_upstream_mapping_id?: number
```

In `web/default/src/features/channels/components/channels-columns.tsx`, extend the priority cell or adjacent metadata so generated managed channels display a small automatic-priority indicator:

```tsx
const autoPriorityScore = parseChannelSettings(channel.settings)
  ?.channel_auto_priority_last_score
```

Render translated text like:

```tsx
{autoPriorityScore && (
  <span className='text-muted-foreground text-xs'>
    {t('Auto priority')}: {Math.round(autoPriorityScore.final_score ?? 0)}
  </span>
)}
```

If the channel has generated metadata and `channel_auto_priority_enabled`, show a tooltip/message near manual priority editing:

```tsx
t('Automatic priority is enabled and may overwrite manual priority edits')
```

- [ ] **Step 6: Run frontend checks**

Run:

```powershell
cd web/default
bun test src/features/upstream-sources/rules.test.ts
bun run typecheck
bun run i18n:sync
```

Expected: tests and typecheck PASS; locale files updated for new English keys.

- [ ] **Step 7: Commit**

```powershell
git add web/default/src/features/upstream-sources/types.ts web/default/src/features/upstream-sources/rules.ts web/default/src/features/upstream-sources/rules.test.ts web/default/src/features/upstream-sources/index.tsx web/default/src/features/channels/types.ts web/default/src/features/channels/components/channels-columns.tsx web/default/src/i18n/locales
git commit -m "feat: configure upstream auto priority"
```

---

## Task 7: Full Verification and Claude Review

**Files:**
- No production files expected unless review finds issues.
- Create temporary prompt under `.codex/` only if needed.

- [ ] **Step 1: Format Go files**

Run:

```powershell
gofmt -w dto/channel_settings.go dto/upstream_source.go service/channel_auto_priority_stats.go service/channel_auto_priority_score.go service/upstream_source_auto_priority.go service/upstream_source_auto_priority_worker.go service/upstream_source_rule.go controller/upstream_source.go controller/channel-test.go router/api-router.go main.go service/*auto_priority*_test.go controller/*upstream_source*_test.go controller/channel_test_internal_test.go
```

- [ ] **Step 2: Run backend tests**

Run:

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./service ./controller ./model -run "AutoPriority|UpstreamSource|ChannelTest|ChannelMonitor" -count=1
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./service ./controller ./model -count=1
```

Expected: PASS.

- [ ] **Step 3: Run frontend tests**

Run:

```powershell
cd web/default
bun test src/features/upstream-sources/rules.test.ts
bun run typecheck
```

Expected: PASS.

- [ ] **Step 4: Run diff hygiene**

Run:

```powershell
git diff --check
git status --short
```

Expected: no whitespace errors; only intentional files changed plus known untracked `.codex` artifacts if still present.

- [ ] **Step 5: Claude review**

Use default Claude settings first. If quota fails, retry with `--settings ~/.claude/settings.wynth.json`.

Prompt focus:

```text
Review the automatic priority adjustment implementation.
Focus on:
- cache rate modeled inside effective price, not as independent quality;
- writes limited to upstream-source-generated channels;
- channel priority, ability priority, and score snapshot consistency;
- cross-database GORM compatibility;
- request path scheduler unchanged;
- tests covering hysteresis, interval zero, channel-test exclusion, and cache creation cost.
Return blockers only.
```

Suggested command:

```powershell
git diff origin/upstream-source-sync...HEAD | claude -p --model sonnet --effort medium --output-format json --disable-slash-commands --tools ""
```

If Claude reports blockers, apply `superpowers:receiving-code-review`, fix with TDD, and re-run focused tests.

- [ ] **Step 6: Final commit or amend**

If verification or review required changes:

```powershell
git add <changed-files>
git commit -m "fix: harden upstream auto priority"
```

If no changes were needed, do not create an empty commit.

- [ ] **Step 7: Push**

Run:

```powershell
git push origin upstream-source-sync
```

Expected: branch pushed successfully.
