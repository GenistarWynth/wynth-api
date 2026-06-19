# Strict Channel Priority Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make retry channel selection exhaust all untried channels in the current priority tier before moving to a lower priority tier.

**Architecture:** Keep the existing `new-api` channel model and retry loop. Add request-local attempted-channel filtering to the model selection paths, and have the service layer resolve attempted IDs from Gin context fresh on each selection call.

**Tech Stack:** Go, Gin, GORM, `testify/require`, existing `model`, `service`, `middleware`, and `controller` packages.

---

## File Structure

- Modify `model/channel_cache.go`: memory-cache selection path, candidate filtering, highest-priority tier selection, and no-mutation helpers.
- Modify `model/ability.go`: database selection path when memory cache is disabled.
- Modify `service/channel_select.go`: read attempted channel IDs from request context and change `auto` group advancement to exhaustion-based selection.
- Modify `service/task_billing_test.go`: add `model.Ability` to service test migrations so service-level selection tests can use real abilities.
- Create `model/channel_strict_priority_test.go`: deterministic model-layer tests for cache path, DB path, exhaustion, single-channel filtering, and cache slice immutability.
- Create `service/channel_select_strict_priority_test.go`: service-layer tests for attempted-ID parsing and auto group exhaustion behavior.
- Review `middleware/distributor.go` and `controller/relay.go`: no expected code changes, but verify they still feed the correct first-selection and retry-selection flow.

---

### Task 1: Model Memory-Cache Strict Priority Selection

**Files:**
- Modify: `model/channel_cache.go`
- Create: `model/channel_strict_priority_test.go`

- [ ] **Step 1: Write failing memory-cache tests**

Create `model/channel_strict_priority_test.go` with this initial content:

```go
package model

import (
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func clearStrictPriorityTables(t *testing.T) {
	t.Helper()
	require.NoError(t, DB.Exec("DELETE FROM abilities").Error)
	require.NoError(t, DB.Exec("DELETE FROM channels").Error)
}

func withMemoryCacheForStrictPriority(t *testing.T, enabled bool) {
	t.Helper()
	original := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = enabled
	t.Cleanup(func() {
		common.MemoryCacheEnabled = original
		InitChannelCache()
	})
}

func insertStrictPriorityCandidate(t *testing.T, id int, group string, modelName string, priority int64, weight uint) {
	t.Helper()
	channel := &Channel{
		Id:     id,
		Type:   1,
		Key:    fmt.Sprintf("sk-channel-%d", id),
		Status: common.ChannelStatusEnabled,
		Name:   fmt.Sprintf("channel-%d", id),
	}
	require.NoError(t, DB.Create(channel).Error)
	require.NoError(t, DB.Create(&Ability{
		Group:     group,
		Model:     modelName,
		ChannelId: id,
		Enabled:   true,
		Priority:  &priority,
		Weight:    weight,
	}).Error)
}

func TestGetRandomSatisfiedChannelKeepsSamePriorityBeforeLowerTier(t *testing.T) {
	clearStrictPriorityTables(t)
	withMemoryCacheForStrictPriority(t, true)

	insertStrictPriorityCandidate(t, 1, "default", "gpt-test", 100, 100)
	insertStrictPriorityCandidate(t, 2, "default", "gpt-test", 100, 20)
	insertStrictPriorityCandidate(t, 3, "default", "gpt-test", 50, 100)
	InitChannelCache()

	channel, err := GetRandomSatisfiedChannel("default", "gpt-test", 1, "/v1/chat/completions", map[int]struct{}{1: {}})

	require.NoError(t, err)
	require.NotNil(t, channel)
	require.Equal(t, 2, channel.Id)
}

func TestGetRandomSatisfiedChannelFallsToLowerPriorityAfterTierExhausted(t *testing.T) {
	clearStrictPriorityTables(t)
	withMemoryCacheForStrictPriority(t, true)

	insertStrictPriorityCandidate(t, 1, "default", "gpt-test", 100, 100)
	insertStrictPriorityCandidate(t, 2, "default", "gpt-test", 100, 20)
	insertStrictPriorityCandidate(t, 3, "default", "gpt-test", 50, 100)
	InitChannelCache()

	channel, err := GetRandomSatisfiedChannel("default", "gpt-test", 2, "/v1/chat/completions", map[int]struct{}{1: {}, 2: {}})

	require.NoError(t, err)
	require.NotNil(t, channel)
	require.Equal(t, 3, channel.Id)
}

func TestGetRandomSatisfiedChannelReturnsNilWhenAllCandidatesAttempted(t *testing.T) {
	clearStrictPriorityTables(t)
	withMemoryCacheForStrictPriority(t, true)

	insertStrictPriorityCandidate(t, 1, "default", "gpt-test", 100, 100)
	insertStrictPriorityCandidate(t, 2, "default", "gpt-test", 50, 100)
	InitChannelCache()

	channel, err := GetRandomSatisfiedChannel("default", "gpt-test", 2, "/v1/chat/completions", map[int]struct{}{1: {}, 2: {}})

	require.NoError(t, err)
	require.Nil(t, channel)
}

func TestGetRandomSatisfiedChannelSingleCandidateAlreadyAttemptedReturnsNil(t *testing.T) {
	clearStrictPriorityTables(t)
	withMemoryCacheForStrictPriority(t, true)

	insertStrictPriorityCandidate(t, 1, "default", "gpt-test", 100, 100)
	InitChannelCache()

	channel, err := GetRandomSatisfiedChannel("default", "gpt-test", 1, "/v1/chat/completions", map[int]struct{}{1: {}})

	require.NoError(t, err)
	require.Nil(t, channel)
}

func TestGetRandomSatisfiedChannelDoesNotMutateCachedChannelSlice(t *testing.T) {
	clearStrictPriorityTables(t)
	withMemoryCacheForStrictPriority(t, true)

	insertStrictPriorityCandidate(t, 1, "default", "gpt-test", 100, 100)
	insertStrictPriorityCandidate(t, 2, "default", "gpt-test", 100, 20)
	InitChannelCache()
	before := append([]int(nil), group2model2channels["default"]["gpt-test"]...)

	channel, err := GetRandomSatisfiedChannel("default", "gpt-test", 1, "/v1/chat/completions", map[int]struct{}{1: {}})

	require.NoError(t, err)
	require.NotNil(t, channel)
	require.Equal(t, 2, channel.Id)
	require.Equal(t, before, group2model2channels["default"]["gpt-test"])
}

func TestGetRandomSatisfiedChannelZeroWeightTierStillSelectable(t *testing.T) {
	clearStrictPriorityTables(t)
	withMemoryCacheForStrictPriority(t, true)

	insertStrictPriorityCandidate(t, 1, "default", "gpt-test", 100, 0)
	insertStrictPriorityCandidate(t, 2, "default", "gpt-test", 100, 0)
	InitChannelCache()

	channel, err := GetRandomSatisfiedChannel("default", "gpt-test", 0, "/v1/chat/completions", nil)

	require.NoError(t, err)
	require.NotNil(t, channel)
	require.Contains(t, []int{1, 2}, channel.Id)
}
```

- [ ] **Step 2: Run memory-cache tests and verify they fail**

Run:

```powershell
go test ./model -run "TestGetRandomSatisfiedChannel" -count=1
```

Expected: FAIL at compile time with an error like `too many arguments in call to GetRandomSatisfiedChannel`.

- [ ] **Step 3: Implement attempted-channel filtering in memory-cache path**

Modify `model/channel_cache.go` so `GetRandomSatisfiedChannel` accepts an optional attempted-channel set and filters candidates before priority selection:

```go
func GetRandomSatisfiedChannel(group string, model string, retry int, requestPath string, attemptedChannelIDs ...map[int]struct{}) (*Channel, error) {
	attempted := firstAttemptedChannelSet(attemptedChannelIDs)
	// if memory cache is disabled, get channel directly from database
	if !common.MemoryCacheEnabled {
		return GetChannel(group, model, retry, requestPath, attempted)
	}

	channelSyncLock.RLock()
	defer channelSyncLock.RUnlock()

	channels := filterChannelsByRequestPath(group2model2channels[group][model], requestPath)
	channels = filterChannelIDsByAttempted(channels, attempted)

	if len(channels) == 0 {
		normalizedModel := ratio_setting.FormatMatchingModelName(model)
		channels = filterChannelsByRequestPath(group2model2channels[group][normalizedModel], requestPath)
		channels = filterChannelIDsByAttempted(channels, attempted)
	}

	if len(channels) == 0 {
		return nil, nil
	}

	return selectHighestPriorityWeightedChannel(group, model, channels)
}
```

Add these helpers in `model/channel_cache.go` below `GetRandomSatisfiedChannel`:

```go
func firstAttemptedChannelSet(attemptedChannelIDs []map[int]struct{}) map[int]struct{} {
	if len(attemptedChannelIDs) == 0 {
		return nil
	}
	return attemptedChannelIDs[0]
}

func filterChannelIDsByAttempted(channels []int, attempted map[int]struct{}) []int {
	if len(channels) == 0 || len(attempted) == 0 {
		return channels
	}
	filtered := make([]int, 0, len(channels))
	for _, channelID := range channels {
		if _, used := attempted[channelID]; used {
			continue
		}
		filtered = append(filtered, channelID)
	}
	return filtered
}

func selectHighestPriorityWeightedChannel(group string, model string, channels []int) (*Channel, error) {
	if len(channels) == 0 {
		return nil, nil
	}

	targetPrioritySet := false
	var targetPriority int64
	for _, channelID := range channels {
		channel, ok := channelsIDM[channelID]
		if !ok {
			return nil, fmt.Errorf("数据库一致性错误，渠道# %d 不存在，请联系管理员修复", channelID)
		}
		priority := channel.GetPriority()
		if !targetPrioritySet || priority > targetPriority {
			targetPriority = priority
			targetPrioritySet = true
		}
	}

	var sumWeight int
	targetChannels := make([]*Channel, 0, len(channels))
	for _, channelID := range channels {
		channel := channelsIDM[channelID]
		if channel.GetPriority() == targetPriority {
			sumWeight += channel.GetWeight()
			targetChannels = append(targetChannels, channel)
		}
	}

	if len(targetChannels) == 0 {
		return nil, errors.New(fmt.Sprintf("no channel found, group: %s, model: %s, priority: %d", group, model, targetPriority))
	}

	smoothingFactor := 1
	smoothingAdjustment := 0
	if sumWeight == 0 {
		sumWeight = len(targetChannels) * 100
		smoothingAdjustment = 100
	} else if sumWeight/len(targetChannels) < 10 {
		smoothingFactor = 100
	}

	totalWeight := sumWeight * smoothingFactor
	randomWeight := rand.Intn(totalWeight)
	for _, channel := range targetChannels {
		randomWeight -= channel.GetWeight()*smoothingFactor + smoothingAdjustment
		if randomWeight < 0 {
			return channel, nil
		}
	}
	return nil, errors.New("channel not found")
}
```

Remove the old `uniquePriorities`, `sortedUniquePriorities`, and `targetPriority := int64(sortedUniquePriorities[retry])` block from `GetRandomSatisfiedChannel`.

- [ ] **Step 4: Run memory-cache tests and verify they pass**

Run:

```powershell
go test ./model -run "TestGetRandomSatisfiedChannel" -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit Task 1**

Run:

```powershell
git add model/channel_cache.go model/channel_strict_priority_test.go
git commit -m "fix: enforce strict priority in cached channel selection"
```

---

### Task 2: Database Selection Strict Priority Path

**Files:**
- Modify: `model/ability.go`
- Modify: `model/channel_strict_priority_test.go`

- [ ] **Step 1: Add failing database-path tests**

Append these tests to `model/channel_strict_priority_test.go`:

```go
func TestGetChannelDatabasePathKeepsSamePriorityBeforeLowerTier(t *testing.T) {
	clearStrictPriorityTables(t)
	withMemoryCacheForStrictPriority(t, false)

	insertStrictPriorityCandidate(t, 1, "default", "gpt-test", 100, 100)
	insertStrictPriorityCandidate(t, 2, "default", "gpt-test", 100, 20)
	insertStrictPriorityCandidate(t, 3, "default", "gpt-test", 50, 100)

	channel, err := GetChannel("default", "gpt-test", 1, "/v1/chat/completions", map[int]struct{}{1: {}})

	require.NoError(t, err)
	require.NotNil(t, channel)
	require.Equal(t, 2, channel.Id)
}

func TestGetChannelDatabasePathReturnsNilWhenAllCandidatesAttempted(t *testing.T) {
	clearStrictPriorityTables(t)
	withMemoryCacheForStrictPriority(t, false)

	insertStrictPriorityCandidate(t, 1, "default", "gpt-test", 100, 100)
	insertStrictPriorityCandidate(t, 2, "default", "gpt-test", 50, 100)

	channel, err := GetChannel("default", "gpt-test", 2, "/v1/chat/completions", map[int]struct{}{1: {}, 2: {}})

	require.NoError(t, err)
	require.Nil(t, channel)
}
```

- [ ] **Step 2: Run database-path tests and verify they fail**

Run:

```powershell
go test ./model -run "TestGetChannelDatabasePath" -count=1
```

Expected: FAIL at compile time with an error like `too many arguments in call to GetChannel`.

- [ ] **Step 3: Implement strict priority in `model.GetChannel`**

Modify the `GetChannel` signature in `model/ability.go`:

```go
func GetChannel(group string, model string, retry int, requestPath string, attemptedChannelIDs ...map[int]struct{}) (*Channel, error) {
	attempted := firstAttemptedChannelSet(attemptedChannelIDs)
	var abilities []Ability

	channelQuery := DB.Where(commonGroupCol+" = ? and model = ? and enabled = ?", group, model, true)
	err := channelQuery.Order("priority DESC").Order("weight DESC").Find(&abilities).Error
	if err != nil {
		return nil, err
	}

	abilities = filterAbilitiesByRequestPath(abilities, requestPath)
	abilities = filterAbilitiesByAttemptedChannelIDs(abilities, attempted)
	if len(abilities) == 0 {
		return nil, nil
	}

	targetPriority := abilities[0].Priority
	var targetPriorityValue int64
	if targetPriority != nil {
		targetPriorityValue = *targetPriority
	}

	targetAbilities := make([]Ability, 0, len(abilities))
	for _, ability := range abilities {
		priorityValue := int64(0)
		if ability.Priority != nil {
			priorityValue = *ability.Priority
		}
		if priorityValue == targetPriorityValue {
			targetAbilities = append(targetAbilities, ability)
		}
	}

	channel := Channel{}
	weightSum := uint(0)
	for _, ability := range targetAbilities {
		weightSum += ability.Weight + 10
	}
	weight := common.GetRandomInt(int(weightSum))
	for _, ability := range targetAbilities {
		weight -= int(ability.Weight) + 10
		if weight <= 0 {
			channel.Id = ability.ChannelId
			break
		}
	}
	if channel.Id == 0 {
		return nil, nil
	}
	err = DB.First(&channel, "id = ?", channel.Id).Error
	return &channel, err
}
```

Add this helper below `filterAbilitiesByRequestPath`:

```go
func filterAbilitiesByAttemptedChannelIDs(abilities []Ability, attempted map[int]struct{}) []Ability {
	if len(abilities) == 0 || len(attempted) == 0 {
		return abilities
	}
	filtered := make([]Ability, 0, len(abilities))
	for _, ability := range abilities {
		if _, used := attempted[ability.ChannelId]; used {
			continue
		}
		filtered = append(filtered, ability)
	}
	return filtered
}
```

Leave `getPriority` and `getChannelQuery` in place if no compile errors require removing them; they become unused only if no other code references them. If Go reports unused functions, no action is needed because package-level functions may be unused.

- [ ] **Step 4: Run database-path tests and verify they pass**

Run:

```powershell
go test ./model -run "TestGetChannelDatabasePath" -count=1
```

Expected: PASS.

- [ ] **Step 5: Run all model strict-priority tests**

Run:

```powershell
go test ./model -run "StrictPriority|GetRandomSatisfiedChannel|GetChannelDatabasePath" -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit Task 2**

Run:

```powershell
git add model/ability.go model/channel_strict_priority_test.go
git commit -m "fix: enforce strict priority in database channel selection"
```

---

### Task 3: Service Attempted-Channel Context and Auto Group Exhaustion

**Files:**
- Modify: `service/channel_select.go`
- Modify: `service/task_billing_test.go`
- Create: `service/channel_select_strict_priority_test.go`

- [ ] **Step 1: Add `Ability` migration to service tests**

Modify `service/task_billing_test.go` inside `TestMain` so the AutoMigrate call includes `&model.Ability{}`:

```go
	if err := db.AutoMigrate(
		&model.Task{},
		&model.User{},
		&model.Token{},
		&model.Log{},
		&model.Channel{},
		&model.Ability{},
		&model.TopUp{},
		&model.UserSubscription{},
	); err != nil {
		panic("failed to migrate: " + err.Error())
	}
```

- [ ] **Step 2: Write failing service tests**

Create `service/channel_select_strict_priority_test.go` with this content:

```go
package service

import (
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func clearServiceStrictPriorityTables(t *testing.T) {
	t.Helper()
	require.NoError(t, model.DB.Exec("DELETE FROM abilities").Error)
	require.NoError(t, model.DB.Exec("DELETE FROM channels").Error)
}

func insertServiceStrictPriorityCandidate(t *testing.T, id int, group string, modelName string, priority int64, weight uint) {
	t.Helper()
	require.NoError(t, model.DB.Create(&model.Channel{
		Id:     id,
		Type:   1,
		Key:    fmt.Sprintf("sk-service-channel-%d", id),
		Status: common.ChannelStatusEnabled,
		Name:   fmt.Sprintf("service-channel-%d", id),
		Group:  group,
		Models: modelName,
	}).Error)
	require.NoError(t, model.DB.Create(&model.Ability{
		Group:     group,
		Model:     modelName,
		ChannelId: id,
		Enabled:   true,
		Priority:  &priority,
		Weight:    weight,
	}).Error)
}

func strictPriorityGinContext() *gin.Context {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	return ctx
}

func withServiceStrictPrioritySettings(t *testing.T, autoGroups string, usableGroups string) {
	t.Helper()
	oldAutoGroups := setting.AutoGroups2JsonString()
	oldUsableGroups := setting.UserUsableGroups2JSONString()
	require.NoError(t, setting.UpdateAutoGroupsByJsonString(autoGroups))
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(usableGroups))
	t.Cleanup(func() {
		require.NoError(t, setting.UpdateAutoGroupsByJsonString(oldAutoGroups))
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(oldUsableGroups))
	})
}

func TestAttemptedChannelIDSetFromContextParsesUseChannel(t *testing.T) {
	ctx := strictPriorityGinContext()
	ctx.Set("use_channel", []string{"1", " 2 ", "bad", "0"})

	attempted := attemptedChannelIDSetFromContext(ctx)

	require.Contains(t, attempted, 1)
	require.Contains(t, attempted, 2)
	require.NotContains(t, attempted, 0)
	require.Len(t, attempted, 2)
}

func TestCacheGetRandomSatisfiedChannelUsesAttemptedChannels(t *testing.T) {
	clearServiceStrictPriorityTables(t)
	common.MemoryCacheEnabled = true
	t.Cleanup(func() {
		common.MemoryCacheEnabled = false
		model.InitChannelCache()
	})

	insertServiceStrictPriorityCandidate(t, 1, "default", "gpt-test", 100, 100)
	insertServiceStrictPriorityCandidate(t, 2, "default", "gpt-test", 100, 20)
	insertServiceStrictPriorityCandidate(t, 3, "default", "gpt-test", 50, 100)
	model.InitChannelCache()

	ctx := strictPriorityGinContext()
	ctx.Set("use_channel", []string{"1"})

	channel, selectedGroup, err := CacheGetRandomSatisfiedChannel(&RetryParam{
		Ctx:         ctx,
		TokenGroup:  "default",
		ModelName:   "gpt-test",
		RequestPath: "/v1/chat/completions",
		Retry:       common.GetPointer(1),
	})

	require.NoError(t, err)
	require.Equal(t, "default", selectedGroup)
	require.NotNil(t, channel)
	require.Equal(t, 2, channel.Id)
}

func TestAutoGroupTriesLowerPriorityInCurrentGroupBeforeNextGroup(t *testing.T) {
	clearServiceStrictPriorityTables(t)
	withServiceStrictPrioritySettings(t, `["default","vip"]`, `{"default":"default group","vip":"vip group"}`)
	common.MemoryCacheEnabled = true
	t.Cleanup(func() {
		common.MemoryCacheEnabled = false
		model.InitChannelCache()
	})

	insertServiceStrictPriorityCandidate(t, 1, "default", "gpt-test", 100, 100)
	insertServiceStrictPriorityCandidate(t, 2, "default", "gpt-test", 50, 100)
	insertServiceStrictPriorityCandidate(t, 3, "vip", "gpt-test", 100, 100)
	model.InitChannelCache()

	ctx := strictPriorityGinContext()
	common.SetContextKey(ctx, constant.ContextKeyUserGroup, "default")
	common.SetContextKey(ctx, constant.ContextKeyAutoGroupIndex, 0)
	ctx.Set("use_channel", []string{"1"})

	channel, selectedGroup, err := CacheGetRandomSatisfiedChannel(&RetryParam{
		Ctx:         ctx,
		TokenGroup:  "auto",
		ModelName:   "gpt-test",
		RequestPath: "/v1/chat/completions",
		Retry:       common.GetPointer(2),
	})

	require.NoError(t, err)
	require.Equal(t, "default", selectedGroup)
	require.NotNil(t, channel)
	require.Equal(t, 2, channel.Id)
}

func TestAutoGroupAdvancesWhenCurrentGroupExhausted(t *testing.T) {
	clearServiceStrictPriorityTables(t)
	withServiceStrictPrioritySettings(t, `["default","vip"]`, `{"default":"default group","vip":"vip group"}`)
	common.MemoryCacheEnabled = true
	t.Cleanup(func() {
		common.MemoryCacheEnabled = false
		model.InitChannelCache()
	})

	insertServiceStrictPriorityCandidate(t, 1, "default", "gpt-test", 100, 100)
	insertServiceStrictPriorityCandidate(t, 2, "vip", "gpt-test", 100, 100)
	model.InitChannelCache()

	ctx := strictPriorityGinContext()
	common.SetContextKey(ctx, constant.ContextKeyUserGroup, "default")
	common.SetContextKey(ctx, constant.ContextKeyAutoGroupIndex, 0)
	ctx.Set("use_channel", []string{"1"})

	channel, selectedGroup, err := CacheGetRandomSatisfiedChannel(&RetryParam{
		Ctx:         ctx,
		TokenGroup:  "auto",
		ModelName:   "gpt-test",
		RequestPath: "/v1/chat/completions",
		Retry:       common.GetPointer(1),
	})

	require.NoError(t, err)
	require.Equal(t, "vip", selectedGroup)
	require.NotNil(t, channel)
	require.Equal(t, 2, channel.Id)
}
```

- [ ] **Step 3: Run service tests and verify they fail**

Run:

```powershell
go test ./service -run "AttemptedChannelIDSet|CacheGetRandomSatisfiedChannelUsesAttempted|AutoGroup" -count=1
```

Expected: FAIL at compile time with `undefined: attemptedChannelIDSetFromContext`, or FAIL behaviorally because auto group still uses the old `priorityRetry` switch.

- [ ] **Step 4: Implement attempted-channel parsing and pass attempted IDs to model**

Modify imports in `service/channel_select.go`:

```go
import (
	"errors"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/gin-gonic/gin"
)
```

Add this helper below `RetryParam.ResetRetryNextTry`:

```go
func attemptedChannelIDSetFromContext(ctx *gin.Context) map[int]struct{} {
	if ctx == nil {
		return nil
	}
	usedChannels := ctx.GetStringSlice("use_channel")
	if len(usedChannels) == 0 {
		return nil
	}
	attempted := make(map[int]struct{}, len(usedChannels))
	for _, rawID := range usedChannels {
		id, err := strconv.Atoi(strings.TrimSpace(rawID))
		if err != nil || id <= 0 {
			continue
		}
		attempted[id] = struct{}{}
	}
	if len(attempted) == 0 {
		return nil
	}
	return attempted
}
```

At the start of `CacheGetRandomSatisfiedChannel`, resolve attempted IDs fresh:

```go
	attemptedChannelIDs := attemptedChannelIDSetFromContext(param.Ctx)
```

Use the attempted set in the normal group path:

```go
		channel, err = model.GetRandomSatisfiedChannel(param.TokenGroup, param.ModelName, param.GetRetry(), param.RequestPath, attemptedChannelIDs)
```

- [ ] **Step 5: Replace auto group priority-retry switching with exhaustion-based advancement**

Inside the `param.TokenGroup == "auto"` branch in `service/channel_select.go`, replace the loop body with exhaustion-based selection:

```go
		for i := startGroupIndex; i < len(autoGroups); i++ {
			autoGroup := autoGroups[i]
			logger.LogDebug(param.Ctx, "Auto selecting group: %s, retry: %d", autoGroup, param.GetRetry())

			channel, err = model.GetRandomSatisfiedChannel(autoGroup, param.ModelName, param.GetRetry(), param.RequestPath, attemptedChannelIDs)
			if err != nil {
				return nil, autoGroup, err
			}
			if channel == nil {
				logger.LogDebug(param.Ctx, "No remaining available channel in group %s for model %s, trying next group", autoGroup, param.ModelName)
				common.SetContextKey(param.Ctx, constant.ContextKeyAutoGroupIndex, i+1)
				continue
			}

			common.SetContextKey(param.Ctx, constant.ContextKeyAutoGroup, autoGroup)
			common.SetContextKey(param.Ctx, constant.ContextKeyAutoGroupIndex, i)
			selectGroup = autoGroup
			logger.LogDebug(param.Ctx, "Auto selected group: %s", autoGroup)
			break
		}
```

Remove the old `crossGroupRetry`, `priorityRetry`, `ContextKeyAutoGroupRetryIndex`, and `priorityRetry >= common.RetryTimes` branch from this function.

- [ ] **Step 6: Run service tests and verify they pass**

Run:

```powershell
go test ./service -run "AttemptedChannelIDSet|CacheGetRandomSatisfiedChannelUsesAttempted|AutoGroup" -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit Task 3**

Run:

```powershell
git add service/channel_select.go service/task_billing_test.go service/channel_select_strict_priority_test.go
git commit -m "fix: use attempted channels during retry selection"
```

---

### Task 4: Regression Test Pass and Formatting

**Files:**
- Modify only files changed by Tasks 1-3 if formatting requires it.

- [ ] **Step 1: Format changed Go files**

Run:

```powershell
gofmt -w model/channel_cache.go model/ability.go model/channel_strict_priority_test.go service/channel_select.go service/channel_select_strict_priority_test.go service/task_billing_test.go
```

Expected: command exits 0.

- [ ] **Step 2: Run package tests for changed packages**

Run:

```powershell
go test ./model ./service -count=1
```

Expected: PASS.

- [ ] **Step 3: Run relay-adjacent package tests**

Run:

```powershell
go test ./middleware ./controller -count=1
```

Expected: PASS.

- [ ] **Step 4: Commit formatting or regression fixes if needed**

If `git status --short` shows only formatting or test-fix changes from this task, run:

```powershell
git add model/channel_cache.go model/ability.go model/channel_strict_priority_test.go service/channel_select.go service/channel_select_strict_priority_test.go service/task_billing_test.go
git commit -m "test: cover strict channel priority scheduling"
```

If there are no changes after the test pass, do not create an empty commit.

---

### Task 5: Claude Review and Final Verification

**Files:**
- Create then delete: `.codex/claude-strict-priority-implementation-review-prompt.md`
- Modify code only if Claude finds a verified issue.

- [ ] **Step 1: Create Claude review prompt**

Create `.codex/claude-strict-priority-implementation-review-prompt.md` with:

```markdown
You are Claude Code acting as an independent outside voice for this repository.

Review the strict channel priority scheduling implementation.

Required context:
- docs/superpowers/specs/2026-06-19-strict-channel-priority-design.md
- docs/superpowers/plans/2026-06-19-strict-channel-priority.md
- model/channel_cache.go
- model/ability.go
- service/channel_select.go
- model/channel_strict_priority_test.go
- service/channel_select_strict_priority_test.go
- middleware/distributor.go
- controller/relay.go

Desired behavior:
- For one request, retry selection must exhaust all untried channels in the current priority tier before moving to lower priority.
- Same-priority selection may remain weighted random.
- Attempted channel IDs are request-local and resolved fresh on each selection.
- RetryTimes remains the total retry budget.
- Auto group advancement is driven by current-group exhaustion.

Review for:
- semantic mismatches with the spec;
- missed cache vs database path divergence;
- auto group edge cases;
- retry loop edge cases;
- tests that give false confidence;
- concurrency or shared-cache mutation risks.

Do not modify files. Do not run commands. Use only read-only inspection.

Output:
1. Findings first, ordered by severity.
2. Open questions.
3. Required fixes before merge.
```

- [ ] **Step 2: Run Claude Opus max review with the Wynth settings**

Run:

```powershell
Get-Content -Raw .codex\claude-strict-priority-implementation-review-prompt.md | claude -p --model opus --effort max --settings ~/.claude/settings.wynth.json --output-format json --disable-slash-commands --allowedTools Read,Grep,Glob --disallowedTools Bash,Edit,Write
```

Expected: Claude returns JSON with `is_error:false` and a review result.

- [ ] **Step 3: Evaluate Claude findings**

For each Claude finding:

```text
1. Verify the finding against the code.
2. If it is correct, write a failing test or identify the existing failing test.
3. Fix only the verified issue.
4. Run the focused test.
5. Commit the fix with a specific message.
```

If Claude reports no blocking issues, record that in the final response.

- [ ] **Step 4: Remove the Claude prompt file**

Run:

```powershell
Remove-Item -LiteralPath .codex\claude-strict-priority-implementation-review-prompt.md
```

Expected: prompt file is removed from the working tree.

- [ ] **Step 5: Run final tests**

Run:

```powershell
go test ./model ./service ./middleware ./controller -count=1
```

Expected: PASS.

- [ ] **Step 6: Final commit if Claude fixes were made**

If Claude review caused code changes, run:

```powershell
git add model/channel_cache.go model/ability.go model/channel_strict_priority_test.go service/channel_select.go service/channel_select_strict_priority_test.go service/task_billing_test.go
git commit -m "fix: address strict priority review findings"
```

If Claude review caused no code changes, do not create an empty commit.

---

## Self-Review

- Spec coverage: covered strict same-tier retry, lower-tier fallback after tier exhaustion, request-local attempted IDs, memory cache, database path, auto group exhaustion, retry budget semantics, and Claude review.
- Red-flag scan: no incomplete requirement markers remain in this plan.
- Type consistency: planned model signatures use optional `map[int]struct{}` attempted sets; service converts Gin context strings to the same set type; tests call the same public functions.
