# Channel-Bound Monitoring Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build per-channel explicit monitoring with independent intervals, append-only 7-day availability history, and monitor status surfaces for channel routing decisions.

**Architecture:** Store only low-frequency monitor configuration in `ChannelOtherSettings`; store every automatic monitor result in a new `channel_monitor_logs` table. The automatic runner scans channels on a fixed cadence, filters due monitored channels in Go for cross-database compatibility, probes without normal billing logs, records history, and derives latest status plus rolling 7-day availability from history.

**Tech Stack:** Go 1.22+, Gin, GORM v2, SQLite/MySQL/PostgreSQL-compatible queries, React 19, TypeScript, Rsbuild, Base UI, Tailwind CSS, Bun.

---

## Pre-Flight Notes

- Worktree: `E:\Documents\Projects\wynth-api\.worktrees\channel-bound-monitoring`
- Branch: `channel-bound-monitoring`
- Design spec: `docs/superpowers/specs/2026-06-19-channel-monitoring-design.md`
- Go command: `D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe`
- `gofmt` command: `D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\gofmt.exe`
- Baseline caveat: `go test ./service -run "TestObserveChannelAffinityUsageCacheByRelayFormat" -count=10` currently fails because tests use timestamp-derived cache keys that collide inside a fast Windows test process. Task 0 fixes only that test fixture so later service verification is meaningful.

## File Structure

- `service/channel_affinity_usage_cache_test.go`: fix pre-existing flaky test key generation.
- `dto/channel_settings.go`: add `channel_monitor_enabled` and `channel_monitor_interval_minutes`.
- `model/channel.go`: add a non-persistent `MonitorInfo` field to API JSON only.
- `model/channel_monitor.go`: new model, constants, normalization, recording, latest lookup, 7-day summary, retention cleanup, and API attachment helpers.
- `model/main.go`: register `ChannelMonitorLog` in normal and fast migrations.
- `model/channel_monitor_test.go`: database tests for settings normalization, append-only logs, latest lookup, rolling availability, retention, and API attachment.
- `controller/channel-test.go`: separate channel probe execution from normal consumption logging; replace global auto-monitor loop with per-channel due monitoring.
- `controller/channel_monitor_test.go`: tests for due filtering, manual-disabled skip, auto-disabled recovery eligibility, no normal consumption logs, and status mutation gates.
- `controller/channel.go`: attach monitor info to list/search/detail channel responses.
- `web/default/src/features/channels/types.ts`: add monitor settings and `monitor_info` API types.
- `web/default/src/features/channels/lib/channel-form.ts`: add monitor fields to schema, defaults, transform, and settings JSON builder.
- `web/default/src/features/channels/components/drawers/channel-mutate-drawer.tsx`: add per-channel monitoring controls and latest monitor readout.
- `web/default/src/features/channels/components/channels-columns.tsx`: show compact monitor status/availability on channel rows.
- `web/default/src/features/system-settings/integrations/monitoring-settings-section.tsx`: hide global automatic all-channel probe controls while keeping existing disable/recovery settings.
- `web/default/src/features/system-settings/integrations/monitoring-settings-section.tsx`: hide global automatic all-channel probe controls while leaving backend option keys compatible.
- `web/default/src/i18n/locales/*.json`: add translations for new frontend strings after running the project i18n sync flow or by using the existing flat-key style.

---

### Task 0: Stabilize Pre-Existing Channel Affinity Cache Tests

**Files:**
- Modify: `service/channel_affinity_usage_cache_test.go`

- [ ] **Step 1: Replace timestamp-derived cache keys with test-name-derived keys**

Change the helper and callers so every test has a deterministic unique key.

```go
func buildChannelAffinityStatsContextForTest(t *testing.T) *gin.Context {
	t.Helper()
	safeName := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	setChannelAffinityContext(ctx, channelAffinityMeta{
		CacheKey:       "test:" + safeName,
		TTLSeconds:     600,
		RuleName:       "rule_" + safeName,
		UsingGroup:     "default",
		KeyFingerprint: "fp_" + safeName,
	})
	return ctx
}
```

Update imports:

```go
import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)
```

Update each test:

```go
ctx := buildChannelAffinityStatsContextForTest(t)
statsCtx, ok := GetChannelAffinityStatsContext(ctx)
require.True(t, ok)
```

Use `statsCtx.RuleName`, `statsCtx.UsingGroup`, and `statsCtx.KeyFingerprint` when calling `GetChannelAffinityUsageCacheStats`.

- [ ] **Step 2: Run the focused flake reproduction**

Run:

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./service -run "TestObserveChannelAffinityUsageCacheByRelayFormat" -count=10
```

Expected: `ok github.com/QuantumNous/new-api/service`.

- [ ] **Step 3: Commit**

```powershell
git add service/channel_affinity_usage_cache_test.go
git commit -m "test: stabilize channel affinity usage cache keys"
```

---

### Task 1: Add Monitor Settings And History Model

**Files:**
- Modify: `dto/channel_settings.go`
- Modify: `model/channel.go`
- Modify: `model/main.go`
- Create: `model/channel_monitor.go`
- Create: `model/channel_monitor_test.go`

- [ ] **Step 1: Add the failing model tests**

Create `model/channel_monitor_test.go` with sqlite-backed fixtures.

```go
package model

import (
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupChannelMonitorTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	require.NoError(t, err)
	DB = db
	LOG_DB = db
	common.UsingSQLite = true
	common.UsingMySQL = false
	common.UsingPostgreSQL = false
	require.NoError(t, db.AutoMigrate(&Channel{}, &ChannelMonitorLog{}))
	return db
}

func TestNormalizeChannelMonitorSettings(t *testing.T) {
	settings := dto.ChannelOtherSettings{ChannelMonitorEnabled: true}
	normalized := NormalizeChannelMonitorSettings(settings)
	require.True(t, normalized.ChannelMonitorEnabled)
	require.Equal(t, DefaultChannelMonitorIntervalMinutes, normalized.ChannelMonitorIntervalMinutes)

	settings.ChannelMonitorIntervalMinutes = -5
	normalized = NormalizeChannelMonitorSettings(settings)
	require.Equal(t, MinimumChannelMonitorIntervalMinutes, normalized.ChannelMonitorIntervalMinutes)
}

func TestRecordChannelMonitorLogAndLatest(t *testing.T) {
	setupChannelMonitorTestDB(t)
	require.NoError(t, RecordChannelMonitorLog(ChannelMonitorLog{
		ChannelID: 1,
		Model:     "gpt-4o-mini",
		Status:    ChannelMonitorStatusFailed,
		LatencyMS: 1200,
		Message:   "timeout",
		CheckedAt: 1000,
	}))
	require.NoError(t, RecordChannelMonitorLog(ChannelMonitorLog{
		ChannelID: 1,
		Model:     "gpt-4o-mini",
		Status:    ChannelMonitorStatusSuccess,
		LatencyMS: 300,
		Message:   "",
		CheckedAt: 2000,
	}))

	latest, err := GetLatestChannelMonitorLogs([]int{1})
	require.NoError(t, err)
	require.Equal(t, ChannelMonitorStatusSuccess, latest[1].Status)
	require.EqualValues(t, 2000, latest[1].CheckedAt)
}

func TestGetChannelMonitorStatsUsesRollingWindow(t *testing.T) {
	setupChannelMonitorTestDB(t)
	require.NoError(t, RecordChannelMonitorLog(ChannelMonitorLog{ChannelID: 1, Status: ChannelMonitorStatusSuccess, LatencyMS: 100, CheckedAt: 100}))
	require.NoError(t, RecordChannelMonitorLog(ChannelMonitorLog{ChannelID: 1, Status: ChannelMonitorStatusFailed, LatencyMS: 300, CheckedAt: 200}))
	require.NoError(t, RecordChannelMonitorLog(ChannelMonitorLog{ChannelID: 1, Status: ChannelMonitorStatusSuccess, LatencyMS: 500, CheckedAt: 10}))

	stats, err := GetChannelMonitorStats([]int{1}, 100)
	require.NoError(t, err)
	require.EqualValues(t, 2, stats[1].TotalChecks)
	require.EqualValues(t, 1, stats[1].SuccessChecks)
	require.NotNil(t, stats[1].Availability)
	assert.InDelta(t, 0.5, *stats[1].Availability, 0.0001)
	assert.InDelta(t, 200, stats[1].AverageLatencyMS, 0.0001)
}

func TestDeleteOldChannelMonitorLogs(t *testing.T) {
	setupChannelMonitorTestDB(t)
	require.NoError(t, RecordChannelMonitorLog(ChannelMonitorLog{ChannelID: 1, Status: ChannelMonitorStatusSuccess, CheckedAt: 10}))
	require.NoError(t, RecordChannelMonitorLog(ChannelMonitorLog{ChannelID: 1, Status: ChannelMonitorStatusSuccess, CheckedAt: 20}))
	require.NoError(t, RecordChannelMonitorLog(ChannelMonitorLog{ChannelID: 1, Status: ChannelMonitorStatusSuccess, CheckedAt: 30}))

	deleted, err := DeleteOldChannelMonitorLogs(25, 2)
	require.NoError(t, err)
	require.EqualValues(t, 2, deleted)

	var remaining []ChannelMonitorLog
	require.NoError(t, DB.Find(&remaining).Error)
	require.Len(t, remaining, 1)
	require.EqualValues(t, 30, remaining[0].CheckedAt)
}
```

- [ ] **Step 2: Run tests and verify they fail**

Run:

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./model -run "TestNormalizeChannelMonitorSettings|TestRecordChannelMonitorLogAndLatest|TestGetChannelMonitorStatsUsesRollingWindow|TestDeleteOldChannelMonitorLogs" -count=1
```

Expected: compile failure for missing monitor types/functions.

- [ ] **Step 3: Add settings fields**

In `dto/channel_settings.go`, extend `ChannelOtherSettings`:

```go
ChannelMonitorEnabled         bool `json:"channel_monitor_enabled,omitempty"`          // 是否启用渠道监控
ChannelMonitorIntervalMinutes int  `json:"channel_monitor_interval_minutes,omitempty"` // 渠道监控间隔（分钟）
```

- [ ] **Step 4: Add non-persistent monitor info to Channel**

In `model/channel.go`, add this field near the cache-only fields:

```go
MonitorInfo *ChannelMonitorInfo `json:"monitor_info,omitempty" gorm:"-"`
```

- [ ] **Step 5: Create the history model and helpers**

Create `model/channel_monitor.go`:

```go
package model

import (
	"errors"
	"math"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"gorm.io/gorm"
)

const (
	ChannelMonitorStatusSuccess  = "success"
	ChannelMonitorStatusFailed   = "failed"
	ChannelMonitorStatusDegraded = "degraded"
	ChannelMonitorStatusError    = "error"

	DefaultChannelMonitorIntervalMinutes = 10
	MinimumChannelMonitorIntervalMinutes = 1
	ChannelMonitorRetentionSeconds       = int64(7 * 24 * 60 * 60)
)

type ChannelMonitorLog struct {
	ID        int    `json:"id" gorm:"primaryKey"`
	ChannelID int   `json:"channel_id" gorm:"index:idx_channel_monitor_logs_channel_checked_at,priority:1"`
	Model     string `json:"model" gorm:"type:varchar(191)"`
	Status    string `json:"status" gorm:"type:varchar(32)"`
	LatencyMS int64  `json:"latency_ms" gorm:"bigint"`
	Message   string `json:"message" gorm:"type:text"`
	CheckedAt int64  `json:"checked_at" gorm:"bigint;index;index:idx_channel_monitor_logs_channel_checked_at,priority:2"`
	CreatedAt int64 `json:"created_at" gorm:"bigint"`
}

type ChannelMonitorInfo struct {
	Enabled           bool     `json:"enabled"`
	IntervalMinutes   int      `json:"interval_minutes"`
	LatestStatus      string   `json:"latest_status,omitempty"`
	LatestCheckedAt   int64    `json:"latest_checked_at,omitempty"`
	LatestLatencyMS   int64    `json:"latest_latency_ms,omitempty"`
	LatestMessage     string   `json:"latest_message,omitempty"`
	SevenDayChecks    int64    `json:"seven_day_checks"`
	SevenDaySuccesses int64    `json:"seven_day_successes"`
	SevenDayAvailability *float64 `json:"seven_day_availability"`
	AverageLatencyMS  float64  `json:"average_latency_ms,omitempty"`
}

type ChannelMonitorStats struct {
	ChannelID        int
	TotalChecks      int64
	SuccessChecks    int64
	AverageLatencyMS float64
	Availability     *float64
}

func NormalizeChannelMonitorSettings(settings dto.ChannelOtherSettings) dto.ChannelOtherSettings {
	if !settings.ChannelMonitorEnabled {
		return settings
	}
	if settings.ChannelMonitorIntervalMinutes == 0 {
		settings.ChannelMonitorIntervalMinutes = DefaultChannelMonitorIntervalMinutes
	}
	if settings.ChannelMonitorIntervalMinutes < MinimumChannelMonitorIntervalMinutes {
		settings.ChannelMonitorIntervalMinutes = MinimumChannelMonitorIntervalMinutes
	}
	return settings
}

func NormalizeChannelMonitorStatus(status string) string {
	switch strings.TrimSpace(status) {
	case ChannelMonitorStatusSuccess, ChannelMonitorStatusFailed, ChannelMonitorStatusDegraded, ChannelMonitorStatusError:
		return strings.TrimSpace(status)
	default:
		return ChannelMonitorStatusError
	}
}

func RecordChannelMonitorLog(log ChannelMonitorLog) error {
	log.Status = NormalizeChannelMonitorStatus(log.Status)
	if log.CheckedAt == 0 {
		log.CheckedAt = common.GetTimestamp()
	}
	if log.CreatedAt == 0 {
		log.CreatedAt = common.GetTimestamp()
	}
	return DB.Create(&log).Error
}

func GetLatestChannelMonitorLogs(channelIDs []int) (map[int]ChannelMonitorLog, error) {
	result := make(map[int]ChannelMonitorLog, len(channelIDs))
	if len(channelIDs) == 0 {
		return result, nil
	}
	for _, channelID := range channelIDs {
		var log ChannelMonitorLog
		err := DB.Where("channel_id = ?", channelID).
			Order("checked_at DESC").
			Order("id DESC").
			First(&log).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		result[channelID] = log
	}
	return result, nil
}

func GetChannelMonitorStats(channelIDs []int, windowStart int64) (map[int]ChannelMonitorStats, error) {
	result := make(map[int]ChannelMonitorStats, len(channelIDs))
	if len(channelIDs) == 0 {
		return result, nil
	}
	var rows []struct {
		ChannelID        int
		TotalChecks      int64
		SuccessChecks    int64
		AverageLatencyMS float64
	}
	err := DB.Model(&ChannelMonitorLog{}).
		Select("channel_id, COUNT(*) AS total_checks, SUM(CASE WHEN status = ? THEN 1 ELSE 0 END) AS success_checks, AVG(latency_ms) AS average_latency_ms", ChannelMonitorStatusSuccess).
		Where("channel_id IN ? AND checked_at >= ?", channelIDs, windowStart).
		Group("channel_id").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		stats := ChannelMonitorStats{
			ChannelID:        row.ChannelID,
			TotalChecks:      row.TotalChecks,
			SuccessChecks:    row.SuccessChecks,
			AverageLatencyMS: row.AverageLatencyMS,
		}
		if row.TotalChecks > 0 {
			availability := float64(row.SuccessChecks) / float64(row.TotalChecks)
			stats.Availability = &availability
		}
		result[row.ChannelID] = stats
	}
	return result, nil
}

func DeleteOldChannelMonitorLogs(cutoff int64, batchSize int) (int64, error) {
	if batchSize <= 0 {
		batchSize = 100
	}
	total := int64(0)
	for {
		var ids []int
		if err := DB.Model(&ChannelMonitorLog{}).
			Where("checked_at < ?", cutoff).
			Order("checked_at ASC").
			Limit(batchSize).
			Pluck("id", &ids).Error; err != nil {
			return total, err
		}
		if len(ids) == 0 {
			return total, nil
		}
		tx := DB.Where("id IN ?", ids).Delete(&ChannelMonitorLog{})
		if tx.Error != nil {
			return total, tx.Error
		}
		total += tx.RowsAffected
		if len(ids) < batchSize {
			return total, nil
		}
	}
}

func AttachChannelMonitorInfo(channels []*Channel, now int64) error {
	if len(channels) == 0 {
		return nil
	}
	ids := make([]int, 0, len(channels))
	for _, channel := range channels {
		if channel != nil {
			ids = append(ids, channel.Id)
		}
	}
	latestByID, err := GetLatestChannelMonitorLogs(ids)
	if err != nil {
		return err
	}
	statsByID, err := GetChannelMonitorStats(ids, now-ChannelMonitorRetentionSeconds)
	if err != nil {
		return err
	}
	for _, channel := range channels {
		if channel == nil {
			continue
		}
		settings := NormalizeChannelMonitorSettings(channel.GetOtherSettings())
		info := &ChannelMonitorInfo{
			Enabled:         settings.ChannelMonitorEnabled,
			IntervalMinutes: settings.ChannelMonitorIntervalMinutes,
		}
		if latest, ok := latestByID[channel.Id]; ok {
			info.LatestStatus = latest.Status
			info.LatestCheckedAt = latest.CheckedAt
			info.LatestLatencyMS = latest.LatencyMS
			info.LatestMessage = latest.Message
		}
		if stats, ok := statsByID[channel.Id]; ok {
			info.SevenDayChecks = stats.TotalChecks
			info.SevenDaySuccesses = stats.SuccessChecks
			info.SevenDayAvailability = stats.Availability
			info.AverageLatencyMS = math.Round(stats.AverageLatencyMS)
		}
		channel.MonitorInfo = info
	}
	return nil
}

```

- [ ] **Step 6: Register migrations**

In `model/main.go`, add `&ChannelMonitorLog{}` to both migration lists:

```go
&ChannelMonitorLog{},
```

Place it near `&Channel{}` because it belongs to channel state.

- [ ] **Step 7: Run focused model tests**

Run:

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./model -run "TestNormalizeChannelMonitorSettings|TestRecordChannelMonitorLogAndLatest|TestGetChannelMonitorStatsUsesRollingWindow|TestDeleteOldChannelMonitorLogs" -count=1
```

Expected: PASS.

- [ ] **Step 8: Format and commit**

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\gofmt.exe' -w dto\channel_settings.go model\channel.go model\channel_monitor.go model\channel_monitor_test.go model\main.go
git add dto/channel_settings.go model/channel.go model/channel_monitor.go model/channel_monitor_test.go model/main.go
git commit -m "feat: add channel monitor history model"
```

---

### Task 2: Implement Per-Channel Monitor Runner And Probe Isolation

**Files:**
- Modify: `controller/channel-test.go`
- Create: `controller/channel_monitor_test.go`

- [ ] **Step 1: Add failing controller tests for due filtering and probe recording**

Create `controller/channel_monitor_test.go`:

```go
package controller

import (
	"bytes"
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupChannelMonitorControllerDB(t *testing.T) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	model.LOG_DB = db
	common.UsingSQLite = true
	common.UsingMySQL = false
	common.UsingPostgreSQL = false
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.ChannelMonitorLog{}, &model.User{}, &model.Log{}))
}

func channelWithMonitorSettings(id int, status int, enabled bool, interval int) *model.Channel {
	channel := &model.Channel{Id: id, Name: "ch", Status: status, Models: "gpt-4o-mini"}
	channel.SetOtherSettings(dto.ChannelOtherSettings{
		ChannelMonitorEnabled:         enabled,
		ChannelMonitorIntervalMinutes: interval,
	})
	return channel
}

func TestFilterDueChannelMonitorCandidates(t *testing.T) {
	setupChannelMonitorControllerDB(t)
	now := int64(10_000)
	due := channelWithMonitorSettings(1, common.ChannelStatusEnabled, true, 10)
	notDue := channelWithMonitorSettings(2, common.ChannelStatusEnabled, true, 10)
	manualDisabled := channelWithMonitorSettings(3, common.ChannelStatusManuallyDisabled, true, 10)
	unmonitored := channelWithMonitorSettings(4, common.ChannelStatusEnabled, false, 10)
	autoDisabled := channelWithMonitorSettings(5, common.ChannelStatusAutoDisabled, true, 10)
	require.NoError(t, model.RecordChannelMonitorLog(model.ChannelMonitorLog{ChannelID: 2, Status: model.ChannelMonitorStatusSuccess, CheckedAt: now - 60}))
	require.NoError(t, model.RecordChannelMonitorLog(model.ChannelMonitorLog{ChannelID: 5, Status: model.ChannelMonitorStatusFailed, CheckedAt: now - 3600}))
	latest, err := model.GetLatestChannelMonitorLogs([]int{1, 2, 3, 4, 5})
	require.NoError(t, err)

	candidates := filterDueChannelMonitorCandidates([]*model.Channel{due, notDue, manualDisabled, unmonitored, autoDisabled}, latest, now)
	require.Len(t, candidates, 2)
	require.Equal(t, 1, candidates[0].Id)
	require.Equal(t, 5, candidates[1].Id)
}

func TestRecordChannelTestConsumeLogSkipsMonitorProbes(t *testing.T) {
	setupChannelMonitorControllerDB(t)
	originalLogConsumeEnabled := common.LogConsumeEnabled
	defer func() { common.LogConsumeEnabled = originalLogConsumeEnabled }()
	common.LogConsumeEnabled = true
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	params := model.RecordConsumeLogParams{
		ChannelId:        1,
		PromptTokens:     1,
		CompletionTokens: 1,
		ModelName:        "gpt-4o-mini",
		TokenName:        "模型测试",
		Content:          "模型测试",
		Group:            "default",
	}

	recordChannelTestConsumeLog(channelTestOptions{recordConsumeLog: false}, ctx, 1, params)
	var count int64
	require.NoError(t, model.LOG_DB.Model(&model.Log{}).Count(&count).Error)
	require.EqualValues(t, 0, count)

	recordChannelTestConsumeLog(channelTestOptions{recordConsumeLog: true}, ctx, 1, params)
	require.NoError(t, model.LOG_DB.Model(&model.Log{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
}

func TestApplyChannelMonitorStatusMutationHonorsEnableGate(t *testing.T) {
	setupChannelMonitorControllerDB(t)
	originalAutomaticEnable := common.AutomaticEnableChannelEnabled
	defer func() { common.AutomaticEnableChannelEnabled = originalAutomaticEnable }()

	channel := channelWithMonitorSettings(1, common.ChannelStatusAutoDisabled, true, 10)
	require.NoError(t, model.DB.Create(channel).Error)

	common.AutomaticEnableChannelEnabled = false
	applyChannelMonitorStatusMutation(channel, testResult{newAPIError: nil}, 100)
	reloaded, err := model.GetChannelById(channel.Id, true)
	require.NoError(t, err)
	require.Equal(t, common.ChannelStatusAutoDisabled, reloaded.Status)

	common.AutomaticEnableChannelEnabled = true
	applyChannelMonitorStatusMutation(reloaded, testResult{newAPIError: nil}, 100)
	reloaded, err = model.GetChannelById(channel.Id, true)
	require.NoError(t, err)
	require.Equal(t, common.ChannelStatusEnabled, reloaded.Status)
}

func TestApplyChannelMonitorStatusMutationNilContextDoesNotPanic(t *testing.T) {
	setupChannelMonitorControllerDB(t)
	originalAutomaticDisable := common.AutomaticDisableChannelEnabled
	defer func() { common.AutomaticDisableChannelEnabled = originalAutomaticDisable }()
	var logBuffer bytes.Buffer
	common.LogWriterMu.Lock()
	originalErrorWriter := gin.DefaultErrorWriter
	gin.DefaultErrorWriter = &logBuffer
	common.LogWriterMu.Unlock()
	defer func() {
		common.LogWriterMu.Lock()
		gin.DefaultErrorWriter = originalErrorWriter
		common.LogWriterMu.Unlock()
	}()

	channel := channelWithMonitorSettings(1, common.ChannelStatusEnabled, true, 10)
	require.NoError(t, model.DB.Create(channel).Error)
	common.AutomaticDisableChannelEnabled = true

	require.NotPanics(t, func() {
		applyChannelMonitorStatusMutation(channel, testResult{context: nil, newAPIError: types.NewOpenAIError(fmt.Errorf("channel failed"), types.ErrorCodeChannelResponseTimeExceeded, 500)}, 100)
	})
	require.Contains(t, logBuffer.String(), "missing monitor test context")
}
```

- [ ] **Step 2: Run tests and verify they fail**

Run:

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./controller -run "TestFilterDueChannelMonitorCandidates|TestRecordChannelTestConsumeLogSkipsMonitorProbes|TestApplyChannelMonitorStatusMutationHonorsEnableGate|TestApplyChannelMonitorStatusMutationNilContextDoesNotPanic" -count=1
```

Expected: compile failure for missing monitor helpers.

- [ ] **Step 3: Add probe options to avoid normal consumption logs**

In `controller/channel-test.go`, extend `testResult` and add options:

```go
type testResult struct {
	context     *gin.Context
	localErr    error
	newAPIError *types.NewAPIError
	testedModel string
}

type channelTestOptions struct {
	recordConsumeLog bool
}

func defaultChannelTestOptions() channelTestOptions {
	return channelTestOptions{recordConsumeLog: true}
}
```

Change `testChannel` into a wrapper:

```go
func testChannel(channel *model.Channel, testUserID int, testModel string, endpointType string, isStream bool) testResult {
	return testChannelWithOptions(channel, testUserID, testModel, endpointType, isStream, defaultChannelTestOptions())
}

func testChannelWithOptions(channel *model.Channel, testUserID int, testModel string, endpointType string, isStream bool, options channelTestOptions) testResult {
	// This is the existing testChannel body with the extra options parameter.
}
```

After `testModel` has been resolved inside `testChannelWithOptions`, make every return after that point include the actual tested model:

```go
return testResult{
	context:     c,
	localErr:    err,
	newAPIError: types.NewError(err, types.ErrorCodeConvertRequestFailed),
	testedModel: testModel,
}
```

Early returns before model resolution can leave `testedModel` empty.

Add a small wrapper around consumption logging:

```go
func recordChannelTestConsumeLog(options channelTestOptions, c *gin.Context, testUserID int, params model.RecordConsumeLogParams) {
	if !options.recordConsumeLog {
		return
	}
	model.RecordConsumeLog(c, testUserID, params)
}
```

Replace the existing `model.RecordConsumeLog` call with:

```go
recordChannelTestConsumeLog(options, c, testUserID, model.RecordConsumeLogParams{
	ChannelId:        channel.Id,
	PromptTokens:     usage.PromptTokens,
	CompletionTokens: usage.CompletionTokens,
	ModelName:        info.OriginModelName,
	TokenName:        "模型测试",
	Quota:            quota,
	Content:          "模型测试",
	UseTimeSeconds:   int(consumedTime),
	IsStream:         info.IsStream,
	Group:            info.UsingGroup,
	Other:            other,
})
```

- [ ] **Step 4: Add monitor result mapping and due filtering**

Add helpers in `controller/channel-test.go`:

```go
func filterDueChannelMonitorCandidates(channels []*model.Channel, latest map[int]model.ChannelMonitorLog, now int64) []*model.Channel {
	candidates := make([]*model.Channel, 0, len(channels))
	for _, channel := range channels {
		if channel == nil || channel.Status == common.ChannelStatusManuallyDisabled {
			continue
		}
		settings := model.NormalizeChannelMonitorSettings(channel.GetOtherSettings())
		if !settings.ChannelMonitorEnabled {
			continue
		}
		latestLog, hasLatest := latest[channel.Id]
		if !hasLatest {
			candidates = append(candidates, channel)
			continue
		}
		intervalSeconds := int64(settings.ChannelMonitorIntervalMinutes * 60)
		if now-latestLog.CheckedAt >= intervalSeconds {
			candidates = append(candidates, channel)
		}
	}
	return candidates
}

func channelMonitorStatusFromResult(result testResult) string {
	if result.localErr != nil {
		return model.ChannelMonitorStatusError
	}
	if result.newAPIError != nil {
		return model.ChannelMonitorStatusFailed
	}
	return model.ChannelMonitorStatusSuccess
}

func channelMonitorMessageFromResult(result testResult) string {
	if result.localErr != nil {
		return result.localErr.Error()
	}
	if result.newAPIError != nil {
		return result.newAPIError.Error()
	}
	return ""
}
```

- [ ] **Step 5: Add automatic monitor batch execution**

Add:

```go
var channelMonitorBatchLock sync.Mutex
var channelMonitorBatchRunning bool

func runDueChannelMonitorBatch() error {
	testUserID, err := resolveChannelTestUserID(nil)
	if err != nil {
		return err
	}
	channelMonitorBatchLock.Lock()
	if channelMonitorBatchRunning {
		channelMonitorBatchLock.Unlock()
		return errors.New("渠道监控已在运行中")
	}
	channelMonitorBatchRunning = true
	channelMonitorBatchLock.Unlock()
	go func() {
		defer func() {
			channelMonitorBatchLock.Lock()
			channelMonitorBatchRunning = false
			channelMonitorBatchLock.Unlock()
		}()
		now := common.GetTimestamp()
		channels, err := model.GetAllChannels(0, 0, true, false)
		if err != nil {
			common.SysError("failed to load channels for monitor: " + err.Error())
			return
		}
		ids := make([]int, 0, len(channels))
		for _, channel := range channels {
			if channel != nil {
				ids = append(ids, channel.Id)
			}
		}
		latest, err := model.GetLatestChannelMonitorLogs(ids)
		if err != nil {
			common.SysError("failed to load latest channel monitor logs: " + err.Error())
			return
		}
		for _, channel := range filterDueChannelMonitorCandidates(channels, latest, now) {
			runChannelMonitorProbe(channel, testUserID)
			time.Sleep(common.RequestInterval)
		}
		if _, err := model.DeleteOldChannelMonitorLogs(now-model.ChannelMonitorRetentionSeconds, 100); err != nil {
			common.SysError("failed to delete old channel monitor logs: " + err.Error())
		}
	}()
	return nil
}
```

Add `runChannelMonitorProbe`:

```go
func runChannelMonitorProbe(channel *model.Channel, testUserID int) {
	tik := time.Now()
	result := testChannelWithOptions(channel, testUserID, "", "", shouldUseStreamForAutomaticChannelTest(channel), channelTestOptions{recordConsumeLog: false})
	milliseconds := time.Since(tik).Milliseconds()
	status := channelMonitorStatusFromResult(result)
	message := channelMonitorMessageFromResult(result)
	_ = model.RecordChannelMonitorLog(model.ChannelMonitorLog{
		ChannelID: channel.Id,
		Model:     result.testedModel,
		Status:    status,
		LatencyMS: milliseconds,
		Message:   message,
		CheckedAt: common.GetTimestamp(),
	})
	applyChannelMonitorStatusMutation(channel, result, milliseconds)
	channel.UpdateResponseTime(milliseconds)
}
```

Add the status mutation helper below the probe. It intentionally preserves the existing automatic-test threshold behavior from `testAllChannels`, but keeps it behind the same automatic-disable gate.

```go
func applyChannelMonitorStatusMutation(channel *model.Channel, result testResult, milliseconds int64) {
	if channel == nil {
		return
	}
	isChannelEnabled := channel.Status == common.ChannelStatusEnabled
	newAPIError := result.newAPIError
	shouldBanChannel := false
	if newAPIError != nil {
		shouldBanChannel = service.ShouldDisableChannel(result.newAPIError)
	}
	disableThreshold := int64(common.ChannelDisableThreshold * 1000)
	if disableThreshold == 0 {
		disableThreshold = 10000000
	}
	if common.AutomaticDisableChannelEnabled && !shouldBanChannel && milliseconds > disableThreshold {
		err := fmt.Errorf("响应时间 %.2fs 超过阈值 %.2fs", float64(milliseconds)/1000.0, float64(disableThreshold)/1000.0)
		newAPIError = types.NewOpenAIError(err, types.ErrorCodeChannelResponseTimeExceeded, http.StatusRequestTimeout)
		shouldBanChannel = true
	}
	if common.AutomaticDisableChannelEnabled && isChannelEnabled && shouldBanChannel && channel.GetAutoBan() && result.context == nil {
		common.SysError(fmt.Sprintf("channel monitor skipped auto-disable for channel #%d: missing monitor test context", channel.Id))
		return
	}
	if common.AutomaticDisableChannelEnabled && isChannelEnabled && shouldBanChannel && channel.GetAutoBan() && result.context != nil {
		processChannelError(result.context, *types.NewChannelError(channel.Id, channel.Type, channel.Name, channel.ChannelInfo.IsMultiKey, common.GetContextKeyString(result.context, constant.ContextKeyChannelKey), channel.GetAutoBan()), newAPIError)
	}
	if !isChannelEnabled && service.ShouldEnableChannel(newAPIError, channel.Status) {
		usingKey := ""
		if result.context != nil {
			usingKey = common.GetContextKeyString(result.context, constant.ContextKeyChannelKey)
		}
		service.EnableChannel(channel.Id, usingKey, channel.Name)
	}
}
```

- [ ] **Step 6: Replace global automatic loop**

Change `AutomaticallyTestChannels`:

```go
func AutomaticallyTestChannels() {
	if !common.IsMasterNode {
		return
	}
	autoTestChannelsOnce.Do(func() {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			if err := runDueChannelMonitorBatch(); err != nil {
				common.SysLog("channel monitor skipped: " + err.Error())
			}
		}
	})
}
```

Keep `testAllChannels(true)` for the manual all-channel action.

- [ ] **Step 7: Run focused controller tests**

Run:

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./controller -run "TestFilterDueChannelMonitorCandidates|TestRecordChannelTestConsumeLogSkipsMonitorProbes|TestApplyChannelMonitorStatusMutationHonorsEnableGate|TestApplyChannelMonitorStatusMutationNilContextDoesNotPanic" -count=1
```

Expected: PASS.

- [ ] **Step 8: Format and commit**

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\gofmt.exe' -w controller\channel-test.go controller\channel_monitor_test.go
git add controller/channel-test.go controller/channel_monitor_test.go
git commit -m "feat: run channel monitoring per channel"
```

---

### Task 3: Attach Monitor Info To Channel APIs

**Files:**
- Modify: `controller/channel.go`
- Modify: `model/channel_monitor_test.go`

- [ ] **Step 1: Add failing attachment test**

Add to `model/channel_monitor_test.go`:

```go
func TestAttachChannelMonitorInfo(t *testing.T) {
	setupChannelMonitorTestDB(t)
	channel := &Channel{Id: 1, Name: "monitored", Status: common.ChannelStatusEnabled}
	channel.SetOtherSettings(dto.ChannelOtherSettings{
		ChannelMonitorEnabled:         true,
		ChannelMonitorIntervalMinutes: 5,
	})
	require.NoError(t, DB.Create(channel).Error)
	require.NoError(t, RecordChannelMonitorLog(ChannelMonitorLog{
		ChannelID: 1,
		Status:    ChannelMonitorStatusSuccess,
		LatencyMS: 250,
		CheckedAt: 1000,
	}))

	require.NoError(t, AttachChannelMonitorInfo([]*Channel{channel}, 1000))
	require.NotNil(t, channel.MonitorInfo)
	require.True(t, channel.MonitorInfo.Enabled)
	require.Equal(t, 5, channel.MonitorInfo.IntervalMinutes)
	require.Equal(t, ChannelMonitorStatusSuccess, channel.MonitorInfo.LatestStatus)
	require.EqualValues(t, 1, channel.MonitorInfo.SevenDayChecks)
	require.NotNil(t, channel.MonitorInfo.SevenDayAvailability)
	require.Equal(t, 1.0, *channel.MonitorInfo.SevenDayAvailability)
}
```

- [ ] **Step 2: Run test to verify current behavior**

Run:

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./model -run "TestAttachChannelMonitorInfo" -count=1
```

Expected: PASS.

- [ ] **Step 3: Attach monitor info in list/search/detail responses**

In `controller/channel.go`, after `clearChannelInfo` loops in `GetAllChannels` and `SearchChannels`, call:

```go
if err := model.AttachChannelMonitorInfo(channelData, common.GetTimestamp()); err != nil {
	common.SysError("failed to attach channel monitor info: " + err.Error())
}
```

For `SearchChannels`, call it with `pagedData`:

```go
if err := model.AttachChannelMonitorInfo(pagedData, common.GetTimestamp()); err != nil {
	common.SysError("failed to attach channel monitor info: " + err.Error())
}
```

For `GetChannel`, after `clearChannelInfo(channel)`:

```go
if err := model.AttachChannelMonitorInfo([]*model.Channel{channel}, common.GetTimestamp()); err != nil {
	common.SysError("failed to attach channel monitor info: " + err.Error())
}
```

- [ ] **Step 4: Run backend focused tests**

Run:

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./model ./controller -run "TestAttachChannelMonitorInfo|TestFilterDueChannelMonitorCandidates|TestRecordChannelTestConsumeLogSkipsMonitorProbes|TestApplyChannelMonitorStatusMutationHonorsEnableGate|TestApplyChannelMonitorStatusMutationNilContextDoesNotPanic" -count=1
```

Expected: PASS.

- [ ] **Step 5: Format and commit**

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\gofmt.exe' -w controller\channel.go model\channel_monitor_test.go
git add controller/channel.go model/channel_monitor_test.go
git commit -m "feat: expose channel monitor summaries"
```

---

### Task 4: Add Per-Channel Monitoring Controls To Default Frontend

**Files:**
- Modify: `web/default/src/features/channels/types.ts`
- Modify: `web/default/src/features/channels/lib/channel-form.ts`
- Modify: `web/default/src/features/channels/components/drawers/channel-mutate-drawer.tsx`
- Modify: `web/default/src/features/channels/components/channels-columns.tsx`
- Modify: `web/default/src/features/system-settings/integrations/monitoring-settings-section.tsx`
- Modify: `web/default/src/i18n/locales/en.json`
- Modify: `web/default/src/i18n/locales/zh.json`

- [ ] **Step 1: Update frontend channel types**

In `types.ts`, add:

```ts
export interface ChannelMonitorInfo {
  enabled: boolean
  interval_minutes: number
  latest_status?: 'success' | 'failed' | 'degraded' | 'error'
  latest_checked_at?: number
  latest_latency_ms?: number
  latest_message?: string
  seven_day_checks: number
  seven_day_successes: number
  seven_day_availability?: number | null
  average_latency_ms?: number
}
```

Add to `channelSchema`:

```ts
monitor_info: z
  .object({
    enabled: z.boolean(),
    interval_minutes: z.number(),
    latest_status: z.enum(['success', 'failed', 'degraded', 'error']).optional(),
    latest_checked_at: z.number().optional(),
    latest_latency_ms: z.number().optional(),
    latest_message: z.string().optional(),
    seven_day_checks: z.number().default(0),
    seven_day_successes: z.number().default(0),
    seven_day_availability: z.number().nullable().optional(),
    average_latency_ms: z.number().optional(),
  })
  .optional(),
```

Add to `ChannelOtherSettings`:

```ts
channel_monitor_enabled?: boolean
channel_monitor_interval_minutes?: number
```

- [ ] **Step 2: Update form schema, defaults, transform, and settings builder**

In `channel-form.ts`, add fields to the schema:

```ts
channel_monitor_enabled: z.boolean().optional(),
channel_monitor_interval_minutes: z.coerce.number().int().min(1).max(1440).optional(),
```

Add defaults:

```ts
channel_monitor_enabled: false,
channel_monitor_interval_minutes: 10,
```

In `transformChannelToFormDefaults`, parse:

```ts
let channelMonitorEnabled = false
let channelMonitorIntervalMinutes = 10
```

Inside settings parse:

```ts
channelMonitorEnabled = parsed.channel_monitor_enabled === true
channelMonitorIntervalMinutes =
  typeof parsed.channel_monitor_interval_minutes === 'number' &&
  parsed.channel_monitor_interval_minutes >= 1
    ? parsed.channel_monitor_interval_minutes
    : 10
```

Return:

```ts
channel_monitor_enabled: channelMonitorEnabled,
channel_monitor_interval_minutes: channelMonitorIntervalMinutes,
```

In the settings JSON builder, add:

```ts
settingsObj.channel_monitor_enabled =
  formData.channel_monitor_enabled === true
if (settingsObj.channel_monitor_enabled) {
  settingsObj.channel_monitor_interval_minutes = Math.max(
    1,
    Number(formData.channel_monitor_interval_minutes || 10)
  )
} else if ('channel_monitor_interval_minutes' in settingsObj) {
  delete settingsObj.channel_monitor_interval_minutes
}
```

`channel_monitor_*` is a channel-type-neutral setting. Do not delete it based on provider type; only delete `channel_monitor_interval_minutes` when monitoring is disabled.

- [ ] **Step 3: Add drawer controls and latest readout**

In `channel-mutate-drawer.tsx`, add a watched value:

```ts
const channelMonitorEnabled = form.watch('channel_monitor_enabled')
const monitorInfo = currentRow?.monitor_info
```

Place a section near upstream model detection settings:

```tsx
<div className='border-border/60 flex flex-col gap-3 border-y py-4'>
  <SubHeading
    title={t('Channel Monitoring')}
    icon={<Activity className='h-3.5 w-3.5' />}
  />
  <div className='divide-border space-y-0 divide-y border-y'>
    <FormField
      control={form.control}
      name='channel_monitor_enabled'
      render={({ field }) => (
        <FormItem className='flex items-center justify-between px-4 py-3'>
          <div className='space-y-0.5'>
            <FormLabel>{t('Enable channel monitoring')}</FormLabel>
            <FormDescription>
              {t('Probe this channel on its own schedule and record availability.')}
            </FormDescription>
          </div>
          <FormControl>
            <Switch checked={field.value} onCheckedChange={field.onChange} />
          </FormControl>
        </FormItem>
      )}
    />
    <FormField
      control={form.control}
      name='channel_monitor_interval_minutes'
      render={({ field }) => (
        <FormItem className='px-4 py-3'>
          <FormLabel>{t('Monitoring interval')}</FormLabel>
          <FormControl>
            <Input
              type='number'
              min={1}
              disabled={!channelMonitorEnabled}
              {...field}
            />
          </FormControl>
          <FormDescription>{t('Interval in minutes for automatic probes.')}</FormDescription>
          <FormMessage />
        </FormItem>
      )}
    />
  </div>
  <div className='text-muted-foreground space-y-2 text-xs'>
    <div>
      <span className='text-foreground font-medium'>{t('Latest monitor status')}:</span>{' '}
      {monitorInfo?.latest_status ? t(monitorInfo.latest_status) : t('Not monitored yet')}
    </div>
    <div>
      <span className='text-foreground font-medium'>{t('7-day availability')}:</span>{' '}
      {typeof monitorInfo?.seven_day_availability === 'number'
        ? `${Math.round(monitorInfo.seven_day_availability * 100)}%`
        : t('No data')}
    </div>
  </div>
</div>
```

Add `Activity` to the lucide imports if it is not already present.

- [ ] **Step 4: Add a compact table badge**

In `channels-columns.tsx`, add a small helper:

```tsx
function ChannelMonitorBadge({ channel }: { channel: Channel }) {
  const { t } = useTranslation()
  const info = channel.monitor_info
  if (!info?.enabled) {
    return null
  }
  const availability =
    typeof info.seven_day_availability === 'number'
      ? `${Math.round(info.seven_day_availability * 100)}%`
      : t('No data')
  const variant =
    info.latest_status === 'success'
      ? 'success'
      : info.latest_status === 'failed' || info.latest_status === 'error'
        ? 'danger'
        : 'warning'
  return (
    <StatusBadge
      label={`${t('Monitor')} ${availability}`}
      variant={variant}
      size='sm'
      copyable={false}
    />
  )
}
```

Render it beside `UpstreamUpdateTags`:

```tsx
<UpstreamUpdateTags channel={channel} />
<ChannelMonitorBadge channel={channel} />
```

- [ ] **Step 5: Hide global automatic all-channel monitor controls**

In `monitoring-settings-section.tsx`, remove the form fields for:

```text
monitor_setting.auto_test_channel_enabled
monitor_setting.auto_test_channel_minutes
```

Keep existing automatic disable, response-time threshold, and recovery controls. Add a small static note:

```tsx
<p className='text-muted-foreground text-sm'>
  {t('Automatic channel probes are configured on each channel.')}
</p>
```

Leave `system-settings/types.ts` and `operations/index.tsx` compatible with backend-returned `monitor_setting.auto_test_channel_*` keys. The product change is that the controls are no longer rendered.

- [ ] **Step 6: Add translations**

Add English and Chinese entries for:

```json
{
  "Channel Monitoring": "Channel Monitoring",
  "Enable channel monitoring": "Enable channel monitoring",
  "Probe this channel on its own schedule and record availability.": "Probe this channel on its own schedule and record availability.",
  "Monitoring interval": "Monitoring interval",
  "Interval in minutes for automatic probes.": "Interval in minutes for automatic probes.",
  "Latest monitor status": "Latest monitor status",
  "7-day availability": "7-day availability",
  "Not monitored yet": "Not monitored yet",
  "No data": "No data",
  "Monitor": "Monitor",
  "Automatic channel probes are configured on each channel.": "Automatic channel probes are configured on each channel.",
  "success": "success",
  "failed": "failed",
  "degraded": "degraded",
  "error": "error"
}
```

Chinese values:

```json
{
  "Channel Monitoring": "渠道监控",
  "Enable channel monitoring": "启用渠道监控",
  "Probe this channel on its own schedule and record availability.": "按该渠道自己的计划探测并记录可用性。",
  "Monitoring interval": "监控间隔",
  "Interval in minutes for automatic probes.": "自动探测的间隔分钟数。",
  "Latest monitor status": "最近监控状态",
  "7-day availability": "7 天可用率",
  "Not monitored yet": "尚未监控",
  "No data": "无数据",
  "Monitor": "监控",
  "Automatic channel probes are configured on each channel.": "自动渠道探测在每个渠道中单独配置。",
  "success": "成功",
  "failed": "失败",
  "degraded": "降级",
  "error": "错误"
}
```

- [ ] **Step 7: Run frontend checks**

Run from `web/default`:

```powershell
bun run build
```

Expected: production build succeeds.

- [ ] **Step 8: Commit**

```powershell
git add web/default/src/features/channels web/default/src/features/system-settings web/default/src/i18n/locales
git commit -m "feat: add channel monitoring controls"
```

---

### Task 5: Verification And Claude Review

**Files:**
- Review all changed files.
- Create only temporary prompt files under the OS temp directory.

- [ ] **Step 1: Run backend formatting**

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\gofmt.exe' -w dto\channel_settings.go model\channel.go model\channel_monitor.go model\channel_monitor_test.go model\main.go controller\channel-test.go controller\channel_monitor_test.go controller\channel.go service\channel_affinity_usage_cache_test.go
```

- [ ] **Step 2: Run focused backend tests**

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./model ./controller ./service ./middleware -count=1
```

Expected: PASS.

- [ ] **Step 3: Run frontend build**

```powershell
Set-Location web\default
bun run build
```

Expected: build succeeds.

- [ ] **Step 4: Run Claude review with the Wynth settings**

Use:

```powershell
$prompt = @'
你是这个仓库的独立代码审阅者。请审阅当前 branch 的 channel-bound monitoring 实现，重点看：
1. 是否符合 docs/superpowers/specs/2026-06-19-channel-monitoring-design.md。
2. 是否避免监控探针写正常消费日志或污染计费统计。
3. 是否避免高频写 Channel.OtherSettings 或整行覆盖渠道。
4. GORM 查询是否兼容 SQLite/MySQL/PostgreSQL。
5. 自动禁用/恢复、主节点调度、7 天滚动窗口、保留清理是否正确。
6. 前端是否只展示 per-channel 监控，不再展示全局自动探测控制。

只读审阅，不要运行 Bash，不要编辑文件。输出：必须修复、建议修复、可以保留、测试缺口。
'@
$promptFile = Join-Path $env:TEMP 'claude-channel-monitor-review.txt'
Set-Content -LiteralPath $promptFile -Value $prompt -Encoding UTF8
Get-Content -Raw -LiteralPath $promptFile | claude -p --model opus --effort max --settings ~/.claude/settings.wynth.json --output-format json --disable-slash-commands --allowedTools Read,Grep,Glob --disallowedTools Bash,Edit,Write
Remove-Item -LiteralPath $promptFile -ErrorAction SilentlyContinue
```

- [ ] **Step 5: Apply Claude findings if they are correct**

For every Claude finding:

```text
Read the referenced code.
Classify it as valid, invalid, or already covered.
For valid findings, write or update a focused test first.
Make the smallest implementation change.
Run the focused test.
Commit with a message matching the fix.
```

- [ ] **Step 6: Final verification**

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./model ./controller ./service ./middleware -count=1
Set-Location web\default
bun run build
```

Expected: both pass.
