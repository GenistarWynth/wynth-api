# Upstream Source Rule Sync Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build rule-driven upstream source synchronization so only matched upstream groups create channels, rule settings control monitoring/scheduling/model selection, and unmatched groups remain visible but unsynced.

**Architecture:** Keep upstream source storage unchanged where possible and store the new rule configuration in the existing `sync_config` JSON. Add a focused service-layer rule matcher/resolver that both sync and mapping-list responses use, so the UI state and worker behavior cannot drift. Keep source-level settings as fallback values and treat mapping `sync_enabled=false` as a manual emergency gate.

**Tech Stack:** Go 1.22+, Gin, GORM, SQLite/MySQL/PostgreSQL-compatible queries, React 19, TypeScript, Rsbuild, Base UI, Bun, testify.

---

## Context And Constraints

- Worktree: `E:\Documents\Projects\wynth-api\.worktrees\upstream-source-sync`
- Branch: `upstream-source-sync`
- Design spec: `docs/superpowers/specs/2026-06-20-upstream-source-rule-sync-design.md`
- Use full Go path if `go` is not on this process PATH: `D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe`
- Use `common.Marshal`, `common.Unmarshal`, or `common.UnmarshalJsonStr` for JSON work.
- Preserve SQLite, MySQL, and PostgreSQL compatibility.
- Use `github.com/stretchr/testify/require` for fatal Go test assertions and `assert` for value assertions.
- Use Bun for frontend commands from `web/default`.
- Claude review should use default settings first to save quota. If it hits quota/session limits, rerun the same prompt with `--settings ~/.claude/settings.wynth.json`.

## File Structure

Backend:

- Modify `dto/upstream_source.go`
  - Add rule override DTOs for monitor, auto sync, platforms, excludes, model strategy, fixed models.
  - Add mapping response fields for match status and resolved rule outputs.
- Create `service/upstream_source_rule.go`
  - Own config normalization, rule matching, resolution, model strategy resolution, and sync eligibility helpers.
- Create `service/upstream_source_rule_test.go`
  - Pure unit tests for config normalization and matcher behavior.
- Modify `service/upstream_source.go`
  - Use rule resolution in discovery, manual sync, generated channel construction, monitoring settings, and model fetching.
- Modify `service/upstream_source_autosync.go`
  - Use rule-level auto-sync intervals and mapping `last_synced_at`.
- Modify `service/upstream_source_test.go`
  - Add integration-style service tests for discovery defaults, sync eligibility, fixed models, monitor overrides, stale group disablement, and backward compatibility.
- Modify `controller/upstream_source.go`
  - Round-trip new config fields and compute mapping match status in list/discovery/sync-result responses.
- Modify `controller/upstream_source_test.go`
  - Verify API config round-trip with explicit false rule overrides.

Frontend:

- Modify `web/default/src/features/upstream-sources/types.ts`
  - Add new rule, monitor, auto-sync, model-strategy, and mapping match fields.
- Create `web/default/src/features/upstream-sources/rules.ts`
  - Normalize keyword lists, model lists, and rule payloads.
- Create `web/default/src/features/upstream-sources/rules.test.ts`
  - Node tests for payload normalization.
- Modify `web/default/src/features/upstream-sources/index.tsx`
  - Replace free-text local group fields with searchable existing-group selection.
  - Add rule-level platform, include/exclude keyword, monitor, auto-sync, model strategy, and fixed model controls.
  - Show matched/unmatched state in mapping list.
- Modify `web/default/src/features/upstream-sources/selection.ts`
  - Keep manual checkbox behavior as an emergency gate; make helper names and labels clear for rule-driven sync.
- Modify `web/default/src/features/upstream-sources/selection.test.ts`
  - Keep existing mapping checkbox coverage.
- Modify `web/default/src/i18n/locales/{en,zh,fr,ja,ru,vi}.json`
  - Add translations through `bun run i18n:sync` and fill Chinese/English text at minimum.

---

## Task 1: Backend Rule DTOs, Config Normalization, And Matcher

**Files:**
- Modify: `dto/upstream_source.go`
- Create: `service/upstream_source_rule.go`
- Create: `service/upstream_source_rule_test.go`
- Modify: `service/upstream_source.go`
- Modify: `controller/upstream_source.go`

- [ ] **Step 1: Write failing matcher and config tests**

Create `service/upstream_source_rule_test.go` with these tests:

```go
package service

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpstreamSourceRuleConfigPreservesExplicitFalseOverrides(t *testing.T) {
	monitorEnabled := false
	autoSyncEnabled := false
	raw, err := common.Marshal(map[string]any{
		"enable_monitor":             true,
		"monitor_interval_minutes":   10,
		"auto_sync_enabled":          true,
		"auto_sync_interval_minutes": 30,
		"model_strategy":             "all_upstream",
		"local_group_rules": []dto.UpstreamSourceLocalGroupRule{{
			Name:       "disabled overrides",
			LocalGroup: "pro",
			Platforms:  []string{"OpenAI"},
			Monitor: &dto.UpstreamSourceRuleMonitor{
				Enabled:         &monitorEnabled,
				IntervalMinutes: 3,
			},
			AutoSync: &dto.UpstreamSourceRuleAutoSync{
				Enabled:         &autoSyncEnabled,
				IntervalMinutes: 7,
			},
			ModelStrategy: "fixed",
			FixedModels:   []string{"gpt-4o", "gpt-4o"},
		}},
	})
	require.NoError(t, err)

	config, err := parseUpstreamSourceSyncConfig(string(raw))

	require.NoError(t, err)
	require.Len(t, config.LocalGroupRules, 1)
	rule := config.LocalGroupRules[0]
	require.NotNil(t, rule.Monitor)
	require.NotNil(t, rule.Monitor.Enabled)
	assert.False(t, *rule.Monitor.Enabled)
	assert.Equal(t, 5, rule.Monitor.IntervalMinutes)
	require.NotNil(t, rule.AutoSync)
	require.NotNil(t, rule.AutoSync.Enabled)
	assert.False(t, *rule.AutoSync.Enabled)
	assert.Equal(t, 7, rule.AutoSync.IntervalMinutes)
	assert.Equal(t, []string{"openai"}, rule.Platforms)
	assert.Equal(t, "fixed", rule.ModelStrategy)
	assert.Equal(t, []string{"gpt-4o"}, rule.FixedModels)
}

func TestResolveUpstreamSourceRuleMatchesPlatformAndKeywords(t *testing.T) {
	monitorEnabled := true
	autoSyncEnabled := true
	config := upstreamSourceSyncConfig{
		LocalGroup:              "default",
		DefaultLocalGroup:       "default",
		EnableMonitor:           false,
		MonitorIntervalMinutes:  10,
		AutoSyncEnabled:         false,
		AutoSyncIntervalMinutes: 30,
		ModelStrategy:           upstreamSourceModelStrategyAllUpstream,
		LocalGroupRules: []dto.UpstreamSourceLocalGroupRule{{
			Name:                "OpenAI cheap",
			LocalGroup:          "cheap",
			Platforms:           []string{"openai"},
			NameContains:        []string{"gpt"},
			DescriptionContains: []string{"shared"},
			Monitor:            &dto.UpstreamSourceRuleMonitor{Enabled: &monitorEnabled, IntervalMinutes: 6},
			AutoSync:           &dto.UpstreamSourceRuleAutoSync{Enabled: &autoSyncEnabled, IntervalMinutes: 12},
		}},
	}
	mapping := model.UpstreamSourceChannelMapping{
		UpstreamGroupName:        "GPT pool",
		UpstreamGroupDescription: "shared low cost group",
		UpstreamPlatform:         "OpenAI",
		DiscoveryStatus:          model.UpstreamMappingDiscoveryStatusActive,
		SyncEnabled:              true,
	}

	resolution := resolveUpstreamSourceRule(config, &mapping)

	assert.True(t, resolution.Matched)
	assert.True(t, resolution.SyncEligible)
	assert.Equal(t, "OpenAI cheap", resolution.RuleName)
	assert.Equal(t, "cheap", resolution.LocalGroup)
	assert.True(t, resolution.MonitorEnabled)
	assert.Equal(t, 6, resolution.MonitorIntervalMinutes)
	assert.True(t, resolution.AutoSyncEnabled)
	assert.Equal(t, 12, resolution.AutoSyncIntervalMinutes)
	assert.Equal(t, upstreamSourceModelStrategyAllUpstream, resolution.ModelStrategy)
	assert.Equal(t, "matched", resolution.Reason)
}

func TestResolveUpstreamSourceRuleRejectsExcludeKeywords(t *testing.T) {
	config := upstreamSourceSyncConfig{
		LocalGroup:        "default",
		DefaultLocalGroup: "default",
		ModelStrategy:     upstreamSourceModelStrategyAllUpstream,
		LocalGroupRules: []dto.UpstreamSourceLocalGroupRule{{
			Name:            "OpenAI non-pro",
			LocalGroup:      "default",
			Platforms:       []string{"openai"},
			NameContains:    []string{"gpt"},
			ExcludeKeywords: []string{"pro"},
		}},
	}
	mapping := model.UpstreamSourceChannelMapping{
		UpstreamGroupName: "GPT Pro",
		UpstreamPlatform:  "openai",
		DiscoveryStatus:   model.UpstreamMappingDiscoveryStatusActive,
		SyncEnabled:       true,
	}

	resolution := resolveUpstreamSourceRule(config, &mapping)

	assert.False(t, resolution.Matched)
	assert.False(t, resolution.SyncEligible)
	assert.Equal(t, "excluded by keyword", resolution.Reason)
}

func TestResolveUpstreamSourceRuleUsesFirstMatch(t *testing.T) {
	config := upstreamSourceSyncConfig{
		LocalGroup:        "default",
		DefaultLocalGroup: "default",
		ModelStrategy:     upstreamSourceModelStrategyAllUpstream,
		LocalGroupRules: []dto.UpstreamSourceLocalGroupRule{
			{Name: "first", LocalGroup: "default", Platforms: []string{"openai"}},
			{Name: "second", LocalGroup: "pro", Platforms: []string{"openai"}},
		},
	}
	mapping := model.UpstreamSourceChannelMapping{
		UpstreamGroupName: "GPT",
		UpstreamPlatform:  "openai",
		DiscoveryStatus:   model.UpstreamMappingDiscoveryStatusActive,
		SyncEnabled:       true,
	}

	resolution := resolveUpstreamSourceRule(config, &mapping)

	assert.True(t, resolution.Matched)
	assert.Equal(t, "first", resolution.RuleName)
	assert.Equal(t, "default", resolution.LocalGroup)
}

func TestResolveUpstreamSourceRuleLeavesUnmatchedGroupsUnsynced(t *testing.T) {
	config := upstreamSourceSyncConfig{
		LocalGroup:        "fallback",
		DefaultLocalGroup: "fallback",
		ModelStrategy:     upstreamSourceModelStrategyAllUpstream,
		LocalGroupRules: []dto.UpstreamSourceLocalGroupRule{{
			Name:       "Anthropic",
			LocalGroup: "pro",
			Platforms:  []string{"anthropic"},
		}},
	}
	mapping := model.UpstreamSourceChannelMapping{
		UpstreamGroupName: "GPT",
		UpstreamPlatform:  "openai",
		DiscoveryStatus:   model.UpstreamMappingDiscoveryStatusActive,
		SyncEnabled:       true,
	}

	resolution := resolveUpstreamSourceRule(config, &mapping)

	assert.False(t, resolution.Matched)
	assert.False(t, resolution.SyncEligible)
	assert.Equal(t, "no matching rule", resolution.Reason)
	assert.Equal(t, "fallback", resolution.LocalGroup)
}
```

- [ ] **Step 2: Run tests and verify failure**

Run:

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./service -run "TestUpstreamSourceRuleConfig|TestResolveUpstreamSourceRule" -count=1
```

Expected: fail because `UpstreamSourceRuleMonitor`, `UpstreamSourceRuleAutoSync`, rule fields, `ModelStrategy`, and `resolveUpstreamSourceRule` do not exist.

- [ ] **Step 3: Add DTO fields**

Modify `dto/upstream_source.go`:

```go
type UpstreamSourceCreateRequest struct {
	// existing fields stay unchanged
	ModelStrategy          string                         `json:"model_strategy"`
	FixedModels            []string                       `json:"fixed_models"`
	LocalGroupRules         []UpstreamSourceLocalGroupRule `json:"local_group_rules"`
}

type UpstreamSourceUpdateRequest struct {
	// existing fields stay unchanged
	ModelStrategy          string                         `json:"model_strategy"`
	FixedModels            []string                       `json:"fixed_models"`
	LocalGroupRules         []UpstreamSourceLocalGroupRule `json:"local_group_rules"`
}

type UpstreamSourceResponse struct {
	// existing fields stay unchanged
	ModelStrategy          string                         `json:"model_strategy"`
	FixedModels            []string                       `json:"fixed_models"`
	LocalGroupRules         []UpstreamSourceLocalGroupRule `json:"local_group_rules"`
}

type UpstreamSourceRuleMonitor struct {
	Enabled         *bool `json:"enabled,omitempty"`
	IntervalMinutes int  `json:"interval_minutes,omitempty"`
}

type UpstreamSourceRuleAutoSync struct {
	Enabled         *bool `json:"enabled,omitempty"`
	IntervalMinutes int  `json:"interval_minutes,omitempty"`
}

type UpstreamSourceLocalGroupRule struct {
	Name                string                      `json:"name"`
	LocalGroup          string                      `json:"local_group"`
	Platforms           []string                    `json:"platforms"`
	NameContains        []string                    `json:"name_contains"`
	DescriptionContains []string                    `json:"description_contains"`
	ExcludeKeywords     []string                    `json:"exclude_keywords"`
	Monitor             *UpstreamSourceRuleMonitor  `json:"monitor,omitempty"`
	AutoSync            *UpstreamSourceRuleAutoSync `json:"auto_sync,omitempty"`
	ModelStrategy        string                      `json:"model_strategy"`
	FixedModels          []string                    `json:"fixed_models"`
}

type UpstreamSourceMappingResponse struct {
	// existing fields stay unchanged
	SyncEligible                    bool     `json:"sync_eligible"`
	MatchedRuleName                 string   `json:"matched_rule_name"`
	MatchReason                     string   `json:"match_reason"`
	ResolvedLocalGroup              string   `json:"resolved_local_group"`
	ResolvedMonitorEnabled          bool     `json:"resolved_monitor_enabled"`
	ResolvedMonitorIntervalMinutes  int      `json:"resolved_monitor_interval_minutes"`
	ResolvedAutoSyncEnabled         bool     `json:"resolved_auto_sync_enabled"`
	ResolvedAutoSyncIntervalMinutes int      `json:"resolved_auto_sync_interval_minutes"`
	ResolvedModelStrategy           string   `json:"resolved_model_strategy"`
	ResolvedFixedModels             []string `json:"resolved_fixed_models"`
}
```

- [ ] **Step 4: Move config normalization and matcher into a new service file**

Create `service/upstream_source_rule.go`:

```go
package service

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
)

const (
	upstreamSourceModelStrategyAllUpstream = "all_upstream"
	upstreamSourceModelStrategyFixed       = "fixed"

	upstreamSourceMatchReasonMatched           = "matched"
	upstreamSourceMatchReasonNoMatchingRule    = "no matching rule"
	upstreamSourceMatchReasonExcludedByKeyword = "excluded by keyword"
	upstreamSourceMatchReasonManualDisabled    = "manual disabled"
	upstreamSourceMatchReasonInactiveDiscovery = "inactive discovery"
)

type upstreamSourceSyncConfig struct {
	LocalGroup              string                             `json:"local_group"`
	ChannelType             int                                `json:"channel_type"`
	DefaultPriority         int64                              `json:"default_priority"`
	DefaultWeight           uint                               `json:"default_weight"`
	EnableMonitor           bool                               `json:"enable_monitor"`
	MonitorIntervalMinutes  int                                `json:"monitor_interval_minutes"`
	AutoSyncModels          *bool                              `json:"auto_sync_models"`
	AllowPrivateIP          common.FlexibleBool                `json:"allow_private_ip"`
	AutoSyncEnabled         bool                               `json:"auto_sync_enabled"`
	AutoSyncIntervalMinutes int                                `json:"auto_sync_interval_minutes"`
	DefaultLocalGroup       string                             `json:"default_local_group"`
	ModelStrategy           string                             `json:"model_strategy"`
	FixedModels             []string                           `json:"fixed_models"`
	LocalGroupRules         []dto.UpstreamSourceLocalGroupRule `json:"local_group_rules"`
}

type upstreamSourceRuleResolution struct {
	Matched                 bool
	SyncEligible            bool
	RuleName                string
	Reason                  string
	LocalGroup              string
	MonitorEnabled          bool
	MonitorIntervalMinutes  int
	AutoSyncEnabled         bool
	AutoSyncIntervalMinutes int
	ModelStrategy           string
	FixedModels             []string
}
```

Move the existing `parseUpstreamSourceSyncConfig`, `normalizeUpstreamSourceLocalGroupRules`, `NormalizeUpstreamSourceLocalGroupRulesForConfig`, and keyword helpers from `service/upstream_source.go` into this file, then extend them:

```go
func parseUpstreamSourceSyncConfig(raw string) (upstreamSourceSyncConfig, error) {
	config := upstreamSourceSyncConfig{
		LocalGroup:      "default",
		ChannelType:     constant.ChannelTypeOpenAI,
		AutoSyncModels:  common.GetPointer(true),
		DefaultPriority: 0,
		DefaultWeight:   0,
		ModelStrategy:   upstreamSourceModelStrategyAllUpstream,
	}
	if strings.TrimSpace(raw) == "" {
		return normalizeUpstreamSourceSyncConfig(config), nil
	}
	if err := common.Unmarshal([]byte(raw), &config); err != nil {
		return config, err
	}
	return normalizeUpstreamSourceSyncConfig(config), nil
}

func normalizeUpstreamSourceSyncConfig(config upstreamSourceSyncConfig) upstreamSourceSyncConfig {
	config.LocalGroup = normalizeDefaultString(config.LocalGroup, "default")
	if config.ChannelType == 0 {
		config.ChannelType = constant.ChannelTypeOpenAI
	}
	if config.AutoSyncModels == nil {
		config.AutoSyncModels = common.GetPointer(true)
	}
	config.DefaultLocalGroup = normalizeDefaultString(config.DefaultLocalGroup, config.LocalGroup)
	if config.MonitorIntervalMinutes > 0 && config.MonitorIntervalMinutes < 5 {
		config.MonitorIntervalMinutes = 5
	}
	if config.AutoSyncEnabled {
		if config.AutoSyncIntervalMinutes < 5 {
			config.AutoSyncIntervalMinutes = 5
		}
	} else {
		config.AutoSyncIntervalMinutes = 0
	}
	config.ModelStrategy = normalizeUpstreamSourceModelStrategy(config.ModelStrategy, config.AutoSyncModels)
	config.FixedModels = normalizeUpstreamSourceModelList(config.FixedModels)
	config.LocalGroupRules = normalizeUpstreamSourceLocalGroupRules(config.LocalGroupRules)
	return config
}
```

Implement the matcher in the same file:

```go
func normalizeDefaultString(value string, fallback string) string {
	if trimmed := strings.TrimSpace(value); trimmed != "" {
		return trimmed
	}
	if trimmed := strings.TrimSpace(fallback); trimmed != "" {
		return trimmed
	}
	return "default"
}

func normalizeUpstreamSourceModelStrategy(value string, legacyAutoSyncModels *bool) string {
	switch strings.TrimSpace(value) {
	case upstreamSourceModelStrategyFixed:
		return upstreamSourceModelStrategyFixed
	case upstreamSourceModelStrategyAllUpstream:
		return upstreamSourceModelStrategyAllUpstream
	}
	if legacyAutoSyncModels != nil && !*legacyAutoSyncModels {
		return upstreamSourceModelStrategyFixed
	}
	return upstreamSourceModelStrategyAllUpstream
}

func normalizeUpstreamSourceModelList(values []string) []string {
	normalized := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		modelName := strings.TrimSpace(value)
		if modelName == "" {
			continue
		}
		if _, ok := seen[modelName]; ok {
			continue
		}
		seen[modelName] = struct{}{}
		normalized = append(normalized, modelName)
	}
	return normalized
}

func resolveUpstreamSourceRule(config upstreamSourceSyncConfig, mapping *model.UpstreamSourceChannelMapping) upstreamSourceRuleResolution {
	resolution := upstreamSourceFallbackResolution(config)
	if mapping == nil {
		resolution.Reason = upstreamSourceMatchReasonNoMatchingRule
		return resolution
	}
	if mapping.DiscoveryStatus != "" && mapping.DiscoveryStatus != model.UpstreamMappingDiscoveryStatusActive {
		resolution.Reason = upstreamSourceMatchReasonInactiveDiscovery
		return resolution
	}
	if !mapping.SyncEnabled {
		resolution.Reason = upstreamSourceMatchReasonManualDisabled
		return resolution
	}
	if len(config.LocalGroupRules) == 0 {
		resolution.Matched = true
		resolution.SyncEligible = true
		resolution.Reason = upstreamSourceMatchReasonMatched
		return resolution
	}
	excluded := false
	for _, rule := range config.LocalGroupRules {
		matched, excludedByRule := upstreamSourceRuleMatches(rule, mapping)
		if excludedByRule {
			excluded = true
			continue
		}
		if !matched {
			continue
		}
		return upstreamSourceResolutionFromRule(config, rule)
	}
	if excluded {
		resolution.Reason = upstreamSourceMatchReasonExcludedByKeyword
	} else {
		resolution.Reason = upstreamSourceMatchReasonNoMatchingRule
	}
	return resolution
}
```

The helper `upstreamSourceRuleMatches` must:

- lowercase and trim platform, group name, and description;
- treat platform list as optional;
- treat include keyword lists as optional when a platform is configured;
- match includes against name or description;
- reject the rule when any exclude keyword appears in name or description.

Add these helper signatures in the same file:

```go
func upstreamSourceFallbackResolution(config upstreamSourceSyncConfig) upstreamSourceRuleResolution
func upstreamSourceResolutionFromRule(config upstreamSourceSyncConfig, rule dto.UpstreamSourceLocalGroupRule) upstreamSourceRuleResolution
func upstreamSourceRuleMatches(rule dto.UpstreamSourceLocalGroupRule, mapping *model.UpstreamSourceChannelMapping) (matched bool, excluded bool)
func upstreamSourceKeywordsMatch(text string, keywords []string) bool
func upstreamSourceAnyKeywordMatches(texts []string, keywords []string) bool
```

`upstreamSourceResolutionFromRule` must set `Matched=true`, `SyncEligible=true`, `Reason="matched"`, copy `rule.Name`, use `rule.LocalGroup` when present, inherit source fallback fields when rule override structs or values are absent, and normalize fixed models with `normalizeUpstreamSourceModelList`.

- [ ] **Step 5: Delete the duplicate config type from `service/upstream_source.go`**

Remove the old `upstreamSourceSyncConfig` type and old normalizer functions from `service/upstream_source.go`. Keep callers using `parseUpstreamSourceSyncConfig`; they now resolve to the new file.

- [ ] **Step 6: Update controller config mirror**

Modify `controller/upstream_source.go`:

```go
type upstreamSourceControllerSyncConfig struct {
	LocalGroup              string                             `json:"local_group"`
	ChannelType             int                                `json:"channel_type"`
	DefaultPriority         int64                              `json:"default_priority"`
	DefaultWeight           uint                               `json:"default_weight"`
	EnableMonitor           bool                               `json:"enable_monitor"`
	MonitorIntervalMinutes  int                                `json:"monitor_interval_minutes"`
	AutoSyncModels          bool                               `json:"auto_sync_models"`
	AllowPrivateIP          common.FlexibleBool                `json:"allow_private_ip"`
	AutoSyncEnabled         bool                               `json:"auto_sync_enabled"`
	AutoSyncIntervalMinutes int                                `json:"auto_sync_interval_minutes"`
	DefaultLocalGroup       string                             `json:"default_local_group"`
	ModelStrategy           string                             `json:"model_strategy"`
	FixedModels             []string                           `json:"fixed_models"`
	LocalGroupRules         []dto.UpstreamSourceLocalGroupRule `json:"local_group_rules"`
}
```

Pass `ModelStrategy` and `FixedModels` in `upstreamSourceFromCreateRequest`, `upstreamSourceUpdateMap`, and `upstreamSourceResponse`.

- [ ] **Step 7: Run focused tests**

Run:

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./service -run "TestUpstreamSourceRuleConfig|TestResolveUpstreamSourceRule" -count=1
```

Expected: pass.

- [ ] **Step 8: Commit**

```powershell
git add dto/upstream_source.go service/upstream_source_rule.go service/upstream_source_rule_test.go service/upstream_source.go controller/upstream_source.go
git commit -m "feat: add upstream source sync rule matcher"
```

---

## Task 2: Discovery And Mapping Responses Use Rule Eligibility

**Files:**
- Modify: `service/upstream_source.go`
- Modify: `service/upstream_source_test.go`
- Modify: `controller/upstream_source.go`
- Modify: `controller/upstream_source_test.go`
- Modify: `dto/upstream_source.go`

- [ ] **Step 1: Write failing discovery tests**

Add to `service/upstream_source_test.go`:

```go
func TestDiscoverUpstreamSourceSetsSyncEnabledFromRuleEligibility(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	source := createDiscoveryTestSource(t)
	raw, err := common.Marshal(map[string]any{
		"local_group_rules": []dto.UpstreamSourceLocalGroupRule{{
			Name:       "OpenAI only",
			LocalGroup: "default",
			Platforms:  []string{"openai"},
		}},
	})
	require.NoError(t, err)
	require.NoError(t, model.DB.Model(&model.UpstreamSource{}).Where("id = ?", source.Id).Update("sync_config", string(raw)).Error)
	rate := 1.0
	svc := UpstreamSourceService{
		AdapterFactory: func(sourceType string) (UpstreamSourceAdapter, error) {
			return fakeUpstreamSourceAdapter{groups: []UpstreamGroup{
				{ID: "10", Name: "GPT", Platform: "openai", Status: "enabled", EffectiveRateMultiplier: &rate},
				{ID: "20", Name: "Claude", Platform: "anthropic", Status: "enabled", EffectiveRateMultiplier: &rate},
			}}, nil
		},
		Now: func() int64 { return 12345 },
	}

	result, err := svc.Discover(context.Background(), source.Id)

	require.NoError(t, err)
	require.Len(t, result.Mappings, 2)
	assert.True(t, result.Mappings[0].SyncEligible)
	assert.True(t, result.Mappings[0].SyncEnabled)
	assert.Equal(t, "OpenAI only", result.Mappings[0].MatchedRuleName)
	assert.Equal(t, "matched", result.Mappings[0].MatchReason)
	assert.False(t, result.Mappings[1].SyncEligible)
	assert.False(t, result.Mappings[1].SyncEnabled)
	assert.Empty(t, result.Mappings[1].MatchedRuleName)
	assert.Equal(t, "no matching rule", result.Mappings[1].MatchReason)
}

func TestDiscoverUpstreamSourceKeepsLegacyNoRuleMappingsSelected(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	source := createDiscoveryTestSource(t)
	rate := 1.0
	svc := UpstreamSourceService{
		AdapterFactory: func(sourceType string) (UpstreamSourceAdapter, error) {
			return fakeUpstreamSourceAdapter{groups: []UpstreamGroup{
				{ID: "10", Name: "GPT", Platform: "openai", Status: "enabled", EffectiveRateMultiplier: &rate},
			}}, nil
		},
		Now: func() int64 { return 12345 },
	}

	result, err := svc.Discover(context.Background(), source.Id)

	require.NoError(t, err)
	require.Len(t, result.Mappings, 1)
	assert.True(t, result.Mappings[0].SyncEnabled)
	assert.True(t, result.Mappings[0].SyncEligible)
	assert.Equal(t, "matched", result.Mappings[0].MatchReason)
}
```

- [ ] **Step 2: Run tests and verify failure**

Run:

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./service -run "TestDiscoverUpstreamSourceSetsSyncEnabledFromRuleEligibility|TestDiscoverUpstreamSourceKeepsLegacyNoRuleMappingsSelected" -count=1
```

Expected: fail because discovery still marks every active mapping selected and mapping responses do not include resolved rule fields.

- [ ] **Step 3: Pass config into discovery mapping construction**

Modify `service/upstream_source.go`:

```go
config, err := parseUpstreamSourceSyncConfig(source.SyncConfig)
if err != nil {
	return s.recordDiscoveryFailure(source.Id, now, err), err
}
mappings, discoveredIDs, invalidCount := discoveredGroupsToMappings(source.Id, groups, now, config)
```

Change the helper signature:

```go
func discoveredGroupsToMappings(sourceID int, groups []UpstreamGroup, now int64, config upstreamSourceSyncConfig) ([]model.UpstreamSourceChannelMapping, []string, int)
```

Inside the helper, set the initial selection from rule eligibility:

```go
mapping := model.UpstreamSourceChannelMapping{...}
resolution := resolveUpstreamSourceRule(config, &mapping)
mapping.SyncEnabled = discoveryStatus == model.UpstreamMappingDiscoveryStatusActive && resolution.SyncEligible
mappingByID[groupID] = mapping
```

Keep `UpsertDiscoveredMappingsTx` conflict updates from changing `sync_enabled`; this preserves manual overrides on existing mappings.

- [ ] **Step 4: Add resolved fields to service mapping responses**

Modify `service/upstream_source.go`:

```go
func buildDiscoveryResultTx(tx *gorm.DB, source model.UpstreamSource, discovered int, staleCount int, invalidCount int) (dto.UpstreamSourceDiscoveryResult, error)
func upstreamSourceMappingResponse(mapping model.UpstreamSourceChannelMapping, config upstreamSourceSyncConfig) dto.UpstreamSourceMappingResponse
```

`upstreamSourceMappingResponse` should call `resolveUpstreamSourceRule(config, &mapping)` and fill:

```go
SyncEligible:                    resolution.SyncEligible,
MatchedRuleName:                 resolution.RuleName,
MatchReason:                     resolution.Reason,
ResolvedLocalGroup:              resolution.LocalGroup,
ResolvedMonitorEnabled:          resolution.MonitorEnabled,
ResolvedMonitorIntervalMinutes:  resolution.MonitorIntervalMinutes,
ResolvedAutoSyncEnabled:         resolution.AutoSyncEnabled,
ResolvedAutoSyncIntervalMinutes: resolution.AutoSyncIntervalMinutes,
ResolvedModelStrategy:           resolution.ModelStrategy,
ResolvedFixedModels:             resolution.FixedModels,
```

- [ ] **Step 5: Add resolved fields to controller mapping responses**

Modify `controller/upstream_source.go`:

```go
func listUpstreamSourceMappings(source model.UpstreamSource) ([]dto.UpstreamSourceMappingResponse, error)
```

Expose this service helper and use it from both service and controller response paths:

```go
func BuildUpstreamSourceMappingResponse(mapping model.UpstreamSourceChannelMapping, rawConfig string) dto.UpstreamSourceMappingResponse
```

`BuildUpstreamSourceMappingResponse` should parse `rawConfig`, fall back to default config if parsing fails, call `resolveUpstreamSourceRule`, and fill every existing mapping response field plus the resolved rule fields.

- [ ] **Step 6: Run focused backend tests**

Run:

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./service ./controller -run "TestDiscoverUpstreamSourceSetsSyncEnabledFromRuleEligibility|TestDiscoverUpstreamSourceKeepsLegacyNoRuleMappingsSelected|TestUpstreamSourceAPI" -count=1
```

Expected: pass. Existing controller tests may need expected payload updates for `model_strategy`, `fixed_models`, and mapping rule response fields.

- [ ] **Step 7: Commit**

```powershell
git add dto/upstream_source.go service/upstream_source.go service/upstream_source_test.go controller/upstream_source.go controller/upstream_source_test.go
git commit -m "feat: mark upstream mappings by sync rules"
```

---

## Task 3: Sync Uses Rule Outputs, Excludes, And Fixed Model Intersections

**Files:**
- Modify: `service/upstream_source.go`
- Modify: `service/upstream_source_test.go`

- [ ] **Step 1: Write failing sync behavior tests**

Add to `service/upstream_source_test.go`:

```go
func TestSyncUpstreamSourceSkipsUnmatchedRuleDrivenMappings(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	source := createDiscoveryTestSource(t)
	raw, err := common.Marshal(map[string]any{
		"local_group_rules": []dto.UpstreamSourceLocalGroupRule{{
			Name:       "OpenAI only",
			LocalGroup: "default",
			Platforms:  []string{"openai"},
		}},
	})
	require.NoError(t, err)
	require.NoError(t, model.DB.Model(&model.UpstreamSource{}).Where("id = ?", source.Id).Update("sync_config", string(raw)).Error)
	rate := 1.0
	mappings := []model.UpstreamSourceChannelMapping{
		{SourceID: source.Id, SyncEnabled: true, UpstreamGroupID: "10", UpstreamGroupName: "GPT", UpstreamPlatform: "openai", DiscoveryStatus: model.UpstreamMappingDiscoveryStatusActive, EffectiveRateMultiplier: &rate},
		{SourceID: source.Id, SyncEnabled: true, UpstreamGroupID: "20", UpstreamGroupName: "Claude", UpstreamPlatform: "anthropic", DiscoveryStatus: model.UpstreamMappingDiscoveryStatusActive, EffectiveRateMultiplier: &rate},
	}
	require.NoError(t, model.DB.Create(&mappings).Error)
	createCalls := make([]fakeUpstreamSourceCreateKeyCall, 0)
	svc := UpstreamSourceService{
		AdapterFactory: func(sourceType string) (UpstreamSourceAdapter, error) {
			return fakeUpstreamSourceAdapter{createCalls: &createCalls}, nil
		},
		FetchModels: func(channel *model.Channel) ([]string, error) {
			return []string{"gpt-4o"}, nil
		},
		Now: func() int64 { return 12345 },
	}

	result, err := svc.Sync(context.Background(), source.Id)

	require.NoError(t, err)
	assert.Equal(t, model.UpstreamSyncStatusSucceeded, result.Status)
	assert.Equal(t, 1, result.Created)
	assert.Equal(t, 1, result.Skipped)
	require.Len(t, createCalls, 1)
	assert.Equal(t, "10", createCalls[0].GroupID)
	var skipped model.UpstreamSourceChannelMapping
	require.NoError(t, model.DB.Where("source_id = ? AND upstream_group_id = ?", source.Id, "20").First(&skipped).Error)
	assert.Equal(t, model.UpstreamMappingSyncStatusSkipped, skipped.SyncStatus)
	assert.Equal(t, "no matching rule", skipped.LastError)
}

func TestSyncUpstreamSourceUsesRuleLocalGroupAndMonitorOverrides(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	source := createDiscoveryTestSource(t)
	monitorEnabled := false
	raw, err := common.Marshal(map[string]any{
		"enable_monitor":            true,
		"monitor_interval_minutes":  20,
		"default_local_group":       "default",
		"model_strategy":            "all_upstream",
		"local_group_rules": []dto.UpstreamSourceLocalGroupRule{{
			Name:       "Pro",
			LocalGroup: "pro",
			Platforms:  []string{"openai"},
			Monitor:    &dto.UpstreamSourceRuleMonitor{Enabled: &monitorEnabled, IntervalMinutes: 5},
		}},
	})
	require.NoError(t, err)
	require.NoError(t, model.DB.Model(&model.UpstreamSource{}).Where("id = ?", source.Id).Update("sync_config", string(raw)).Error)
	rate := 1.0
	mapping := model.UpstreamSourceChannelMapping{SourceID: source.Id, SyncEnabled: true, UpstreamGroupID: "10", UpstreamGroupName: "GPT Pro", UpstreamPlatform: "openai", DiscoveryStatus: model.UpstreamMappingDiscoveryStatusActive, EffectiveRateMultiplier: &rate}
	require.NoError(t, model.DB.Create(&mapping).Error)
	svc := UpstreamSourceService{
		AdapterFactory: func(sourceType string) (UpstreamSourceAdapter, error) { return fakeUpstreamSourceAdapter{}, nil },
		FetchModels:    func(channel *model.Channel) ([]string, error) { return []string{"gpt-4o"}, nil },
		Now:            func() int64 { return 12345 },
	}

	result, err := svc.Sync(context.Background(), source.Id)

	require.NoError(t, err)
	assert.Equal(t, model.UpstreamSyncStatusSucceeded, result.Status)
	var channel model.Channel
	require.NoError(t, model.DB.First(&channel, result.Results[0].LocalChannelID).Error)
	assert.Equal(t, "pro", channel.Group)
	settings := channel.GetOtherSettings()
	assert.False(t, settings.ChannelMonitorEnabled)
	assert.Equal(t, 5, settings.ChannelMonitorIntervalMinutes)
}

func TestSyncUpstreamSourceFixedModelsIntersectsFetchedModels(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	source := createDiscoveryTestSource(t)
	raw, err := common.Marshal(map[string]any{
		"local_group_rules": []dto.UpstreamSourceLocalGroupRule{{
			Name:          "fixed",
			LocalGroup:    "default",
			Platforms:     []string{"openai"},
			ModelStrategy: "fixed",
			FixedModels:   []string{"gpt-4o", "gpt-4.1", "claude-3-5-sonnet"},
		}},
	})
	require.NoError(t, err)
	require.NoError(t, model.DB.Model(&model.UpstreamSource{}).Where("id = ?", source.Id).Update("sync_config", string(raw)).Error)
	rate := 1.0
	mapping := model.UpstreamSourceChannelMapping{SourceID: source.Id, SyncEnabled: true, UpstreamGroupID: "10", UpstreamGroupName: "GPT", UpstreamPlatform: "openai", DiscoveryStatus: model.UpstreamMappingDiscoveryStatusActive, EffectiveRateMultiplier: &rate}
	require.NoError(t, model.DB.Create(&mapping).Error)
	svc := UpstreamSourceService{
		AdapterFactory: func(sourceType string) (UpstreamSourceAdapter, error) { return fakeUpstreamSourceAdapter{}, nil },
		FetchModels:    func(channel *model.Channel) ([]string, error) { return []string{"gpt-4o", "claude-3-5-sonnet", "unused-model"}, nil },
		Now:            func() int64 { return 12345 },
	}

	result, err := svc.Sync(context.Background(), source.Id)

	require.NoError(t, err)
	var channel model.Channel
	require.NoError(t, model.DB.First(&channel, result.Results[0].LocalChannelID).Error)
	assert.Equal(t, "gpt-4o,claude-3-5-sonnet", channel.Models)
	assert.Equal(t, common.ChannelStatusEnabled, channel.Status)
}

func TestSyncUpstreamSourceFixedModelsEmptyIntersectionDisablesChannel(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	source := createDiscoveryTestSource(t)
	raw, err := common.Marshal(map[string]any{
		"local_group_rules": []dto.UpstreamSourceLocalGroupRule{{
			Name:          "fixed",
			LocalGroup:    "default",
			Platforms:     []string{"openai"},
			ModelStrategy: "fixed",
			FixedModels:   []string{"gpt-4.1"},
		}},
	})
	require.NoError(t, err)
	require.NoError(t, model.DB.Model(&model.UpstreamSource{}).Where("id = ?", source.Id).Update("sync_config", string(raw)).Error)
	rate := 1.0
	mapping := model.UpstreamSourceChannelMapping{SourceID: source.Id, SyncEnabled: true, UpstreamGroupID: "10", UpstreamGroupName: "GPT", UpstreamPlatform: "openai", DiscoveryStatus: model.UpstreamMappingDiscoveryStatusActive, EffectiveRateMultiplier: &rate}
	require.NoError(t, model.DB.Create(&mapping).Error)
	svc := UpstreamSourceService{
		AdapterFactory: func(sourceType string) (UpstreamSourceAdapter, error) { return fakeUpstreamSourceAdapter{}, nil },
		FetchModels:    func(channel *model.Channel) ([]string, error) { return []string{"gpt-4o"}, nil },
		Now:            func() int64 { return 12345 },
	}

	result, err := svc.Sync(context.Background(), source.Id)

	require.NoError(t, err)
	var channel model.Channel
	require.NoError(t, model.DB.First(&channel, result.Results[0].LocalChannelID).Error)
	assert.Empty(t, channel.Models)
	assert.Equal(t, common.ChannelStatusManuallyDisabled, channel.Status)
	var reloaded model.UpstreamSourceChannelMapping
	require.NoError(t, model.DB.First(&reloaded, mapping.Id).Error)
	assert.Contains(t, reloaded.LastError, "no configured models are available upstream")
}
```

- [ ] **Step 2: Run tests and verify failure**

Run:

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./service -run "TestSyncUpstreamSourceSkipsUnmatchedRuleDrivenMappings|TestSyncUpstreamSourceUsesRuleLocalGroupAndMonitorOverrides|TestSyncUpstreamSourceFixedModels" -count=1
```

Expected: fail because sync still queries only `sync_enabled=true`, does not skip unmatched rules, and does not support fixed model intersections.

- [ ] **Step 3: Pass rule resolution through sync**

Modify `service/upstream_source.go`:

```go
var mappings []model.UpstreamSourceChannelMapping
if err := model.DB.Where("source_id = ?", sourceID).Order("id").Find(&mappings).Error; err != nil {
	// existing failure handling
}
```

Before syncing each mapping:

```go
resolution := resolveUpstreamSourceRule(config, &mappings[i])
if !resolution.SyncEligible {
	mappingResult := skippedUpstreamSourceMappingResult(&mappings[i], resolution.Reason, now)
	result.Results = append(result.Results, mappingResult)
	result.Skipped++
	continue
}
mappingResult, changedChannel := s.syncUpstreamSourceMapping(ctx, &source, &mappings[i], config, resolution, adapter, now)
```

Create helper:

```go
func skippedUpstreamSourceMappingResult(mapping *model.UpstreamSourceChannelMapping, reason string, now int64) dto.UpstreamSourceMappingSyncResult {
	errText := SanitizeUpstreamSourceError(errors.New(reason))
	_ = updateUpstreamSourceMappingSync(mapping.Id, mapping.UpstreamKeyID, mapping.LocalChannelID, model.UpstreamMappingSyncStatusSkipped, errText, now)
	return dto.UpstreamSourceMappingSyncResult{
		MappingID:       mapping.Id,
		UpstreamGroupID: mapping.UpstreamGroupID,
		LocalChannelID:  mapping.LocalChannelID,
		Status:          model.UpstreamMappingSyncStatusSkipped,
		Error:           errText,
	}
}
```

Track whether at least one mapping was eligible. If none were eligible, return a failed source-level result with error `no upstream groups matched sync rules` when the source has rules, or the existing selected-mapping error when it has no rules.

- [ ] **Step 4: Use resolved local group and monitor settings**

Change signatures:

```go
func (s *UpstreamSourceService) syncUpstreamSourceMapping(ctx context.Context, source *model.UpstreamSource, mapping *model.UpstreamSourceChannelMapping, config upstreamSourceSyncConfig, resolution upstreamSourceRuleResolution, adapter UpstreamSourceAdapter, now int64) (dto.UpstreamSourceMappingSyncResult, *model.Channel)
func buildGeneratedChannel(source *model.UpstreamSource, mapping *model.UpstreamSourceChannelMapping, config upstreamSourceSyncConfig, resolution upstreamSourceRuleResolution, rawKey string) *model.Channel
func mergeGeneratedChannelOtherSettings(channel *model.Channel, existingChannel *model.Channel, config upstreamSourceSyncConfig, resolution upstreamSourceRuleResolution, source *model.UpstreamSource, mapping *model.UpstreamSourceChannelMapping)
```

Inside `buildGeneratedChannel`, use:

```go
Group: resolution.LocalGroup,
```

Inside `SetOtherSettings`, use:

```go
ChannelMonitorEnabled:         resolution.MonitorEnabled,
ChannelMonitorIntervalMinutes: resolution.MonitorIntervalMinutes,
```

- [ ] **Step 5: Implement fixed model intersections**

Change:

```go
models, modelErr := fetchGeneratedChannelModels(s.fetchModels(config), channel, config)
```

to:

```go
models, modelErr := fetchGeneratedChannelModels(s.fetchModels(config), channel, resolution)
```

Implement:

```go
func fetchGeneratedChannelModels(fetchModels func(channel *model.Channel) ([]string, error), channel *model.Channel, resolution upstreamSourceRuleResolution) ([]string, error) {
	models, err := fetchModels(channel)
	if err != nil {
		return nil, err
	}
	models = normalizeFetchedModelNames(models)
	if len(models) == 0 {
		return nil, errors.New("models are required before enabling generated channel")
	}
	if resolution.ModelStrategy != upstreamSourceModelStrategyFixed {
		return models, nil
	}
	allowed := make(map[string]struct{}, len(resolution.FixedModels))
	for _, modelName := range resolution.FixedModels {
		allowed[modelName] = struct{}{}
	}
	filtered := make([]string, 0, len(models))
	for _, modelName := range models {
		if _, ok := allowed[modelName]; ok {
			filtered = append(filtered, modelName)
		}
	}
	if len(filtered) == 0 {
		return nil, errors.New("no configured models are available upstream")
	}
	return filtered, nil
}
```

- [ ] **Step 6: Run focused tests**

Run:

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./service -run "TestSyncUpstreamSourceSkipsUnmatchedRuleDrivenMappings|TestSyncUpstreamSourceUsesRuleLocalGroupAndMonitorOverrides|TestSyncUpstreamSourceFixedModels|TestSyncUpstreamSourceDoesNotClaimShortNameChannelWithoutMetadata|TestSyncUpstreamSourceDoesNotOverwriteChannelOwnedByDifferentMapping" -count=1
```

Expected: pass. The ownership regression tests must keep passing.

- [ ] **Step 7: Ask Claude for focused backend sync review**

Create prompt `.codex/claude-upstream-rule-sync-backend-review.md`:

```markdown
Review the upstream source rule-driven sync backend changes on branch upstream-source-sync.

Focus on:
- rule matcher correctness;
- unmatched groups staying visible but unsynced;
- fixed model intersection behavior;
- explicit false monitor/auto-sync override preservation;
- generated channel ownership safety;
- SQLite/MySQL/PostgreSQL compatibility;
- project JSON wrapper usage.

Return only findings with file/line references and severity.
```

Run first:

```powershell
Get-Content -Raw .codex\claude-upstream-rule-sync-backend-review.md | claude -p --model sonnet --effort medium --output-format json --disable-slash-commands --allowedTools Read,Grep,Glob --disallowedTools Bash,Edit,Write
```

If quota/session limit occurs, rerun:

```powershell
Get-Content -Raw .codex\claude-upstream-rule-sync-backend-review.md | claude -p --model sonnet --effort medium --settings ~/.claude/settings.wynth.json --output-format json --disable-slash-commands --allowedTools Read,Grep,Glob --disallowedTools Bash,Edit,Write
```

Fix any valid blocking findings before committing.

- [ ] **Step 8: Commit**

```powershell
git add service/upstream_source.go service/upstream_source_test.go .codex/claude-upstream-rule-sync-backend-review.md
git commit -m "feat: sync upstream mappings by rules"
```

---

## Task 4: Rule-Aware Auto Sync Scheduling

**Files:**
- Modify: `service/upstream_source_autosync.go`
- Modify: `service/upstream_source.go`
- Modify: `service/upstream_source_test.go`

- [ ] **Step 1: Write failing auto-sync tests**

Add to `service/upstream_source_test.go`:

```go
func TestListDueUpstreamSourcesForAutoSyncUsesRuleIntervals(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	autoSyncEnabled := true
	raw, err := common.Marshal(map[string]any{
		"auto_sync_enabled": false,
		"local_group_rules": []dto.UpstreamSourceLocalGroupRule{{
			Name:       "OpenAI",
			LocalGroup: "default",
			Platforms:  []string{"openai"},
			AutoSync:   &dto.UpstreamSourceRuleAutoSync{Enabled: &autoSyncEnabled, IntervalMinutes: 10},
		}},
	})
	require.NoError(t, err)
	source := createAutoSyncTestSource(t, "rule-due", model.UpstreamSourceStatusEnabled, map[string]any{}, 0, "", 0)
	require.NoError(t, model.DB.Model(&model.UpstreamSource{}).Where("id = ?", source.Id).Update("sync_config", string(raw)).Error)
	rate := 1.0
	require.NoError(t, model.DB.Create(&model.UpstreamSourceChannelMapping{
		SourceID: source.Id, SyncEnabled: true, UpstreamGroupID: "10", UpstreamGroupName: "GPT", UpstreamPlatform: "openai",
		DiscoveryStatus: model.UpstreamMappingDiscoveryStatusActive, EffectiveRateMultiplier: &rate, LastSyncedAt: 1000,
	}).Error)

	due, err := ListDueUpstreamSourcesForAutoSync(1701)

	require.NoError(t, err)
	require.Len(t, due, 1)
	assert.Equal(t, source.Id, due[0].Id)
}

func TestRunDueUpstreamSourceAutoSyncSyncsOnlyDueMappings(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	autoSyncEnabled := true
	raw, err := common.Marshal(map[string]any{
		"local_group_rules": []dto.UpstreamSourceLocalGroupRule{{
			Name:       "OpenAI",
			LocalGroup: "default",
			Platforms:  []string{"openai"},
			AutoSync:   &dto.UpstreamSourceRuleAutoSync{Enabled: &autoSyncEnabled, IntervalMinutes: 10},
		}},
	})
	require.NoError(t, err)
	source := createAutoSyncTestSource(t, "rule-due", model.UpstreamSourceStatusEnabled, map[string]any{}, 0, "", 0)
	require.NoError(t, model.DB.Model(&model.UpstreamSource{}).Where("id = ?", source.Id).Update("sync_config", string(raw)).Error)
	rate := 1.0
	require.NoError(t, model.DB.Create(&[]model.UpstreamSourceChannelMapping{
		{SourceID: source.Id, SyncEnabled: true, UpstreamGroupID: "10", UpstreamGroupName: "old", UpstreamPlatform: "openai", DiscoveryStatus: model.UpstreamMappingDiscoveryStatusActive, EffectiveRateMultiplier: &rate, LastSyncedAt: 1000},
		{SourceID: source.Id, SyncEnabled: true, UpstreamGroupID: "20", UpstreamGroupName: "fresh", UpstreamPlatform: "openai", DiscoveryStatus: model.UpstreamMappingDiscoveryStatusActive, EffectiveRateMultiplier: &rate, LastSyncedAt: 1650},
	}).Error)
	createCalls := make([]fakeUpstreamSourceCreateKeyCall, 0)
	svc := UpstreamSourceService{
		AdapterFactory: func(sourceType string) (UpstreamSourceAdapter, error) {
			return fakeUpstreamSourceAdapter{
				groups: []UpstreamGroup{
					{ID: "10", Name: "old", Platform: "openai", Status: "enabled", EffectiveRateMultiplier: &rate},
					{ID: "20", Name: "fresh", Platform: "openai", Status: "enabled", EffectiveRateMultiplier: &rate},
				},
				createCalls: &createCalls,
			}, nil
		},
		FetchModels: func(channel *model.Channel) ([]string, error) { return []string{"gpt-4o"}, nil },
		Now:         func() int64 { return 1701 },
	}

	results := svc.RunDueUpstreamSourceAutoSync(context.Background(), 1701)

	require.Len(t, results, 1)
	require.Len(t, createCalls, 1)
	assert.Equal(t, "10", createCalls[0].GroupID)
}
```

- [ ] **Step 2: Run tests and verify failure**

Run:

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./service -run "TestListDueUpstreamSourcesForAutoSyncUsesRuleIntervals|TestRunDueUpstreamSourceAutoSyncSyncsOnlyDueMappings" -count=1
```

Expected: fail because auto sync only uses source-level `LastSyncTime` and `AutoSyncIntervalMinutes`.

- [ ] **Step 3: Add auto-sync due helpers**

In `service/upstream_source_rule.go`, add:

```go
func upstreamSourceMappingAutoSyncDue(config upstreamSourceSyncConfig, mapping *model.UpstreamSourceChannelMapping, now int64) bool {
	resolution := resolveUpstreamSourceRule(config, mapping)
	if !resolution.SyncEligible || !resolution.AutoSyncEnabled || resolution.AutoSyncIntervalMinutes <= 0 {
		return false
	}
	if mapping.LastSyncedAt == 0 {
		return true
	}
	return now-mapping.LastSyncedAt >= int64(resolution.AutoSyncIntervalMinutes)*60
}

func upstreamSourceHasAutoSyncSchedule(config upstreamSourceSyncConfig) bool {
	if config.AutoSyncEnabled && config.AutoSyncIntervalMinutes > 0 {
		return true
	}
	for _, rule := range config.LocalGroupRules {
		if rule.AutoSync != nil && rule.AutoSync.Enabled != nil && *rule.AutoSync.Enabled && rule.AutoSync.IntervalMinutes > 0 {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Update source due selection**

Modify `ListDueUpstreamSourcesForAutoSync`:

```go
if !upstreamSourceHasAutoSyncSchedule(config) {
	continue
}
if source.CurrentSyncToken != "" && source.SyncStartedAt > now-upstreamSourceSyncStaleAfterSeconds {
	continue
}
var mappings []model.UpstreamSourceChannelMapping
if err := model.DB.Where("source_id = ?", source.Id).Find(&mappings).Error; err != nil {
	return nil, err
}
for i := range mappings {
	if upstreamSourceMappingAutoSyncDue(config, &mappings[i], now) {
		due = append(due, source)
		break
	}
}
```

For sources with no discovered mappings yet, fall back to source-level schedule so the worker can run discovery:

```go
if len(mappings) == 0 && config.AutoSyncEnabled && (source.LastSyncTime == 0 || now-source.LastSyncTime >= int64(config.AutoSyncIntervalMinutes)*60) {
	due = append(due, source)
}
```

- [ ] **Step 5: Add auto-sync-only sync mode**

Modify `service/upstream_source.go`:

```go
type upstreamSourceSyncMode string

const (
	upstreamSourceSyncModeManual upstreamSourceSyncMode = "manual"
	upstreamSourceSyncModeAuto   upstreamSourceSyncMode = "auto"
)

func (s *UpstreamSourceService) Sync(ctx context.Context, sourceID int) (*dto.UpstreamSourceSyncResult, error) {
	return s.sync(ctx, sourceID, upstreamSourceSyncModeManual)
}

func (s *UpstreamSourceService) SyncDueAuto(ctx context.Context, sourceID int) (*dto.UpstreamSourceSyncResult, error) {
	return s.sync(ctx, sourceID, upstreamSourceSyncModeAuto)
}
```

Inside `sync`, skip mappings that are not due when `mode == upstreamSourceSyncModeAuto`:

```go
if mode == upstreamSourceSyncModeAuto && !upstreamSourceMappingAutoSyncDue(config, &mappings[i], now) {
	mappingResult := skippedUpstreamSourceMappingResult(&mappings[i], "auto sync interval not due", now)
	result.Results = append(result.Results, mappingResult)
	result.Skipped++
	continue
}
```

- [ ] **Step 6: Update worker to call `SyncDueAuto`**

Modify `RunDueUpstreamSourceAutoSync`:

```go
result, err := s.SyncDueAuto(sourceCtx, source.Id)
```

- [ ] **Step 7: Run focused tests**

Run:

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./service -run "TestListDueUpstreamSourcesForAutoSync|TestRunDueUpstreamSourceAutoSync" -count=1
```

Expected: pass.

- [ ] **Step 8: Commit**

```powershell
git add service/upstream_source_autosync.go service/upstream_source.go service/upstream_source_rule.go service/upstream_source_test.go
git commit -m "feat: schedule upstream sync by rules"
```

---

## Task 5: Frontend Rule Editor, Existing Group Selection, And Mapping Status

**Files:**
- Modify: `web/default/src/features/upstream-sources/types.ts`
- Create: `web/default/src/features/upstream-sources/rules.ts`
- Create: `web/default/src/features/upstream-sources/rules.test.ts`
- Modify: `web/default/src/features/upstream-sources/index.tsx`
- Modify: `web/default/src/features/upstream-sources/selection.ts`
- Modify: `web/default/src/features/upstream-sources/selection.test.ts`
- Modify: `web/default/src/i18n/locales/en.json`
- Modify: `web/default/src/i18n/locales/zh.json`
- Modify: `web/default/src/i18n/locales/fr.json`
- Modify: `web/default/src/i18n/locales/ja.json`
- Modify: `web/default/src/i18n/locales/ru.json`
- Modify: `web/default/src/i18n/locales/vi.json`

- [ ] **Step 1: Write failing frontend normalization tests**

Create `web/default/src/features/upstream-sources/rules.test.ts`:

```ts
import assert from 'node:assert/strict'
import { describe, test } from 'node:test'
import {
  normalizeKeywordList,
  normalizeModelList,
  normalizeSyncRules,
} from './rules'

describe('upstream source rule normalization', () => {
  test('normalizes comma newline and chinese-comma separated keywords', () => {
    assert.deepEqual(normalizeKeywordList(' GPT,pro， Claude\nGPT '), [
      'gpt',
      'pro',
      'claude',
    ])
  })

  test('keeps platform-only rules as valid sync rules', () => {
    assert.deepEqual(
      normalizeSyncRules([
        {
          name: 'OpenAI',
          local_group: 'default',
          platforms: ['OpenAI'],
          name_contains: [],
          description_contains: [],
          exclude_keywords: [],
          monitor: undefined,
          auto_sync: undefined,
          model_strategy: 'all_upstream',
          fixed_models: [],
        },
      ]),
      [
        {
          name: 'OpenAI',
          local_group: 'default',
          platforms: ['openai'],
          name_contains: [],
          description_contains: [],
          exclude_keywords: [],
          model_strategy: 'all_upstream',
          fixed_models: [],
        },
      ]
    )
  })

  test('normalizes fixed model rules with de-duplicated model order', () => {
    assert.deepEqual(normalizeModelList([' gpt-4o ', 'claude', 'gpt-4o']), [
      'gpt-4o',
      'claude',
    ])
  })
})
```

- [ ] **Step 2: Run frontend tests and verify failure**

Run:

```powershell
bun test src/features/upstream-sources/rules.test.ts
```

Expected: fail because `rules.ts` does not exist.

- [ ] **Step 3: Update frontend types**

Modify `web/default/src/features/upstream-sources/types.ts`:

```ts
export type UpstreamSourceModelStrategy = 'all_upstream' | 'fixed'

export type UpstreamSourceRuleMonitor = {
  enabled?: boolean
  interval_minutes?: number
}

export type UpstreamSourceRuleAutoSync = {
  enabled?: boolean
  interval_minutes?: number
}

export type UpstreamSourceLocalGroupRule = {
  name: string
  local_group: string
  platforms: string[]
  name_contains: string[]
  description_contains: string[]
  exclude_keywords: string[]
  monitor?: UpstreamSourceRuleMonitor
  auto_sync?: UpstreamSourceRuleAutoSync
  model_strategy: UpstreamSourceModelStrategy
  fixed_models: string[]
}
```

Add to `UpstreamSource`, `UpstreamSourceFormValues`, create request, and update request:

```ts
model_strategy: UpstreamSourceModelStrategy
fixed_models: string[]
```

Add to `UpstreamSourceMapping`:

```ts
sync_eligible: boolean
matched_rule_name: string
match_reason: string
resolved_local_group: string
resolved_monitor_enabled: boolean
resolved_monitor_interval_minutes: number
resolved_auto_sync_enabled: boolean
resolved_auto_sync_interval_minutes: number
resolved_model_strategy: UpstreamSourceModelStrategy
resolved_fixed_models: string[]
```

- [ ] **Step 4: Add frontend rule normalization module**

Create `web/default/src/features/upstream-sources/rules.ts`:

```ts
import type { UpstreamSourceLocalGroupRule } from './types'

export const UPSTREAM_SOURCE_MODEL_STRATEGY_ALL = 'all_upstream' as const
export const UPSTREAM_SOURCE_MODEL_STRATEGY_FIXED = 'fixed' as const

export function normalizeKeywordList(value: string | string[]): string[] {
  const raw = Array.isArray(value) ? value.join(',') : value
  const seen = new Set<string>()
  return raw
    .split(/[\n,，]+/)
    .map((item) => item.trim().toLowerCase())
    .filter((item) => {
      if (!item || seen.has(item)) return false
      seen.add(item)
      return true
    })
}

export function normalizeModelList(values: string[]): string[] {
  const seen = new Set<string>()
  return values
    .map((item) => item.trim())
    .filter((item) => {
      if (!item || seen.has(item)) return false
      seen.add(item)
      return true
    })
}

export function formatKeywordList(values: string[]) {
  return values.join(', ')
}

export function normalizeSyncRules(
  rules: UpstreamSourceLocalGroupRule[]
): UpstreamSourceLocalGroupRule[] {
  return rules
    .map((rule) => {
      const platforms = normalizeKeywordList(rule.platforms)
      const nameContains = normalizeKeywordList(rule.name_contains)
      const descriptionContains = normalizeKeywordList(rule.description_contains)
      const excludeKeywords = normalizeKeywordList(rule.exclude_keywords)
      const modelStrategy =
        rule.model_strategy === UPSTREAM_SOURCE_MODEL_STRATEGY_FIXED
          ? UPSTREAM_SOURCE_MODEL_STRATEGY_FIXED
          : UPSTREAM_SOURCE_MODEL_STRATEGY_ALL
      const fixedModels =
        modelStrategy === UPSTREAM_SOURCE_MODEL_STRATEGY_FIXED
          ? normalizeModelList(rule.fixed_models)
          : []
      return {
        name: rule.name.trim(),
        local_group: rule.local_group.trim(),
        platforms,
        name_contains: nameContains,
        description_contains: descriptionContains,
        exclude_keywords: excludeKeywords,
        ...(rule.monitor ? { monitor: rule.monitor } : {}),
        ...(rule.auto_sync ? { auto_sync: rule.auto_sync } : {}),
        model_strategy: modelStrategy,
        fixed_models: fixedModels,
      }
    })
    .filter(
      (rule) =>
        rule.local_group &&
        (rule.platforms.length > 0 ||
          rule.name_contains.length > 0 ||
          rule.description_contains.length > 0)
    )
}
```

- [ ] **Step 5: Run frontend normalization tests**

Run:

```powershell
bun test src/features/upstream-sources/rules.test.ts
```

Expected: pass.

- [ ] **Step 6: Load existing groups and models in the source drawer**

Modify `web/default/src/features/upstream-sources/index.tsx` imports:

```ts
import { getUserGroups, getUserModels } from '@/lib/api'
import { Combobox } from '@/components/ui/combobox'
```

Inside `SourceSheet`, add queries enabled when the sheet is open:

```ts
const groupsQuery = useQuery({
  queryKey: ['user-groups'],
  queryFn: getUserGroups,
  enabled: props.open,
})
const modelsQuery = useQuery({
  queryKey: ['user-models'],
  queryFn: getUserModels,
  enabled: props.open,
})
const groupOptions = Object.entries(groupsQuery.data?.data ?? {}).map(
  ([value, info]) => ({ value, label: `${value} (${info.desc || value})` })
)
const modelOptions = modelsQuery.data?.data ?? []
```

Replace default local group and rule local group free-text `<Input>` with:

```tsx
<Combobox
  options={groupOptions}
  value={form.default_local_group}
  onValueChange={(value) => value && setField('default_local_group', value)}
  placeholder={t('Select local group')}
  emptyText={t('No local group found')}
/>
```

For rule local group:

```tsx
<Combobox
  options={groupOptions}
  value={rule.local_group}
  onValueChange={(value) =>
    value &&
    setLocalGroupRule(index, {
      ...rule,
      local_group: value,
    })
  }
  placeholder={t('Select local group')}
  emptyText={t('No local group found')}
/>
```

- [ ] **Step 7: Add rule controls**

In each rule card, add:

- Platform checkboxes for `openai` and `anthropic`.
- Include keyword inputs for name and description.
- Exclude keyword input.
- Rule monitor override switch and interval input.
- Rule auto-sync override switch and interval input.
- Model strategy select with `All upstream models` and `Fixed models`.
- Fixed model searchable checkbox list when strategy is fixed.

Implement fixed model picker inside `index.tsx`:

```tsx
function ModelCheckboxList(props: {
  values: string[]
  options: string[]
  onChange: (values: string[]) => void
}) {
  const { t } = useTranslation()
  const [query, setQuery] = useState('')
  const filtered = props.options.filter((model) =>
    model.toLowerCase().includes(query.trim().toLowerCase())
  )
  const selected = new Set(props.values)
  const toggle = (model: string, checked: boolean) => {
    props.onChange(
      checked
        ? Array.from(new Set([...props.values, model]))
        : props.values.filter((item) => item !== model)
    )
  }
  return (
    <div className='grid gap-2'>
      <Input
        value={query}
        onChange={(event) => setQuery(event.target.value)}
        placeholder={t('Search models')}
      />
      <div className='border-border max-h-48 overflow-y-auto rounded-md border p-2'>
        {filtered.length === 0 ? (
          <div className='text-muted-foreground py-4 text-center text-sm'>
            {t('No models found')}
          </div>
        ) : (
          filtered.map((model) => (
            <label key={model} className='flex items-center gap-2 py-1 text-sm'>
              <Checkbox
                checked={selected.has(model)}
                onCheckedChange={(checked) => toggle(model, Boolean(checked))}
              />
              <span className='truncate'>{model}</span>
            </label>
          ))
        )}
      </div>
    </div>
  )
}
```

- [ ] **Step 8: Update mapping list display**

In `MappingRow`, show a badge:

```tsx
<StatusBadge
  label={
    mapping.sync_eligible
      ? mapping.matched_rule_name || t('Matched')
      : t(mapping.match_reason || 'Not matched')
  }
  variant={mapping.sync_eligible ? 'success' : 'neutral'}
  copyable={false}
/>
```

Show resolved local group and model strategy in the group cell:

```tsx
<span className='text-muted-foreground text-xs'>
  {t('Local group')}: {mapping.resolved_local_group || '-'}
</span>
<span className='text-muted-foreground text-xs'>
  {t('Model strategy')}: {t(mapping.resolved_model_strategy || 'all_upstream')}
</span>
```

Change selected count wording from selected mapping count to enabled mapping gate count:

```tsx
{t('{{count}} sync gates enabled', { count: selectedCount })}
```

- [ ] **Step 9: Update payload builders**

Use the new normalization helpers:

```ts
import {
  normalizeSyncRules,
  UPSTREAM_SOURCE_MODEL_STRATEGY_ALL,
} from './rules'
```

In `emptyLocalGroupRule`:

```ts
return {
  name: '',
  local_group: '',
  platforms: [],
  name_contains: [],
  description_contains: [],
  exclude_keywords: [],
  model_strategy: UPSTREAM_SOURCE_MODEL_STRATEGY_ALL,
  fixed_models: [],
}
```

In `defaultSourceFormValues`, set:

```ts
model_strategy: source?.model_strategy ?? UPSTREAM_SOURCE_MODEL_STRATEGY_ALL,
fixed_models: source?.fixed_models ?? [],
local_group_rules: source?.local_group_rules ?? [],
```

In `buildCreatePayload` and `buildUpdatePayload`, include:

```ts
model_strategy: values.model_strategy,
fixed_models:
  values.model_strategy === 'fixed' ? normalizeModelList(values.fixed_models) : [],
local_group_rules: normalizeSyncRules(values.local_group_rules),
```

- [ ] **Step 10: Sync translations**

Run:

```powershell
bun run i18n:sync
```

Fill at least `en.json` and `zh.json` for these keys:

- `Sync Rules`
- `Add rule`
- `Platforms`
- `OpenAI`
- `Anthropic`
- `Exclude Keywords`
- `Rule Monitor`
- `Rule Auto Sync`
- `Model Strategy`
- `All upstream models`
- `Fixed models`
- `Search models`
- `No models found`
- `No local group found`
- `Not matched`
- `no matching rule`
- `excluded by keyword`
- `manual disabled`
- `inactive discovery`
- `sync gates enabled`

- [ ] **Step 11: Run frontend checks**

Run:

```powershell
bun test src/features/upstream-sources/rules.test.ts src/features/upstream-sources/selection.test.ts
bun run typecheck
```

Expected: both commands pass.

- [ ] **Step 12: Commit**

```powershell
git add web/default/src/features/upstream-sources web/default/src/i18n/locales
git commit -m "feat: configure upstream sync rules in UI"
```

---

## Task 6: Final Verification, Browser Smoke Test, Claude Review, And Push

**Files:**
- Modify only files required by review findings.

- [ ] **Step 1: Format Go code**

Run:

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\gofmt.exe' -w dto/upstream_source.go service/upstream_source.go service/upstream_source_rule.go service/upstream_source_autosync.go service/upstream_source_test.go service/upstream_source_rule_test.go controller/upstream_source.go controller/upstream_source_test.go
```

Expected: no output.

- [ ] **Step 2: Run backend tests**

Run:

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./service ./controller ./model -count=1
```

Expected: pass.

- [ ] **Step 3: Run frontend tests and build**

From `web/default`:

```powershell
bun test src/features/upstream-sources/rules.test.ts src/features/upstream-sources/selection.test.ts
bun run typecheck
bun run build
```

Expected: pass.

- [ ] **Step 4: Restart local dev servers**

Stop current dev listeners for ports 3000 and 3001, then restart:

```powershell
$backendLog='C:\Users\wjx28\AppData\Local\Temp\wynth-api-backend.log'
$frontendLog='C:\Users\wjx28\AppData\Local\Temp\wynth-api-frontend.log'
Get-NetTCPConnection -LocalPort 3000,3001 -State Listen -ErrorAction SilentlyContinue |
  Select-Object -ExpandProperty OwningProcess -Unique |
  ForEach-Object { Stop-Process -Id $_ -Force -ErrorAction SilentlyContinue }
$env:PORT='3001'
$env:SQLITE_PATH='C:\Users\wjx28\AppData\Local\Temp\wynth-api-dev-one-api.db'
Start-Process -FilePath 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' -ArgumentList @('run','.') -WorkingDirectory 'E:\Documents\Projects\wynth-api\.worktrees\upstream-source-sync' -WindowStyle Hidden -RedirectStandardOutput $backendLog -RedirectStandardError $backendLog
$env:VITE_REACT_APP_SERVER_URL='http://127.0.0.1:3001'
Start-Process -FilePath 'pwsh' -ArgumentList @('-NoLogo','-NoProfile','-Command','bun run dev -- --host 127.0.0.1 --port 3000 *> C:\Users\wjx28\AppData\Local\Temp\wynth-api-frontend.log') -WorkingDirectory 'E:\Documents\Projects\wynth-api\.worktrees\upstream-source-sync\web\default' -WindowStyle Hidden
```

Verify:

```powershell
Invoke-WebRequest -Uri http://127.0.0.1:3000/api/status -UseBasicParsing -TimeoutSec 10 | Select-Object StatusCode
```

Expected: `StatusCode` is `200`.

- [ ] **Step 5: Browser smoke test**

Use browser automation to open:

```text
http://127.0.0.1:3000/upstream-sources
```

Verify:

- page does not stay black/loading;
- source form opens;
- local group fields use searchable group selection;
- rule editor can add a rule with platform, exclude keywords, auto-sync override, monitor override, and fixed models;
- mapping sheet shows matched/unmatched state after discovery.

- [ ] **Step 6: Final Claude review**

Create `.codex/claude-upstream-rule-sync-final-review.md`:

```markdown
Review the complete upstream source rule-driven sync implementation.

Spec: docs/superpowers/specs/2026-06-20-upstream-source-rule-sync-design.md
Plan: docs/superpowers/plans/2026-06-20-upstream-source-rule-sync.md

Focus on:
- spec compliance;
- rule-driven sync semantics;
- auto-sync due calculation;
- fixed model intersection;
- explicit false override persistence;
- frontend payload compatibility;
- frontend i18n coverage;
- database portability;
- security and generated-channel ownership.

Return findings only. Include severity and file/line references.
```

Run first:

```powershell
Get-Content -Raw .codex\claude-upstream-rule-sync-final-review.md | claude -p --model sonnet --effort medium --output-format json --disable-slash-commands --allowedTools Read,Grep,Glob --disallowedTools Bash,Edit,Write
```

If quota/session limit occurs, rerun:

```powershell
Get-Content -Raw .codex\claude-upstream-rule-sync-final-review.md | claude -p --model sonnet --effort medium --settings ~/.claude/settings.wynth.json --output-format json --disable-slash-commands --allowedTools Read,Grep,Glob --disallowedTools Bash,Edit,Write
```

Fix valid blocking findings. For non-blocking findings, document why they can wait.

- [ ] **Step 7: Final full checks after review fixes**

Run:

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./service ./controller ./model -count=1
```

From `web/default`:

```powershell
bun test src/features/upstream-sources/rules.test.ts src/features/upstream-sources/selection.test.ts
bun run typecheck
bun run build
```

Expected: all pass.

- [ ] **Step 8: Commit and push**

If review produced fixes:

```powershell
git add .
git commit -m "fix: harden upstream sync rules"
```

Then push:

```powershell
git status --short
git push origin upstream-source-sync
```

Expected: `git status --short` is empty before push, and push updates `origin/upstream-source-sync`.

---

## Self-Review Checklist

- Spec coverage:
  - Unmatched groups discovered but unsynced: Task 2 and Task 3.
  - Platform matching: Task 1 and Task 5.
  - Include/exclude keywords: Task 1 and Task 5.
  - Rule-level monitor settings: Task 1, Task 3, Task 5.
  - Rule-level auto-sync settings: Task 1, Task 4, Task 5.
  - Existing group selection: Task 5.
  - Fixed model intersection: Task 3 and Task 5.
  - Backward compatibility without rules: Task 2 and Task 3.
  - Claude review: Task 3 and Task 6.
- Placeholder scan:
  - No `TBD`.
  - No `TODO`.
  - No placeholder functions without names or assertions.
- Type consistency:
  - Backend DTO uses `model_strategy`, `fixed_models`, `platforms`, `exclude_keywords`, `monitor`, and `auto_sync`.
  - Frontend types use the same JSON keys.
  - Mapping response fields use `sync_eligible`, `matched_rule_name`, `match_reason`, and resolved setting fields.
