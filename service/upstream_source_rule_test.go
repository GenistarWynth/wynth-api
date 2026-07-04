package service

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpstreamSourceRuleConfigPreservesExplicitFalseOverrides(t *testing.T) {
	raw, err := common.Marshal(map[string]any{
		"local_group":                " fallback ",
		"enable_monitor":             true,
		"monitor_interval_minutes":   3,
		"auto_sync_enabled":          true,
		"auto_sync_interval_minutes": 4,
		"model_strategy":             upstreamSourceModelStrategyFixed,
		"fixed_models":               []string{" GPT-4o ", "GPT-4o", "Claude-3"},
		"local_group_rules": []map[string]any{
			{
				"name":                 " Paid ",
				"local_group":          " paid ",
				"platforms":            []string{" OpenAI ", "openai", "CLAUDE"},
				"name_contains":        []string{" Pro ", "pro"},
				"description_contains": []string{" Plus ", "plus"},
				"exclude_keywords":     []string{" Sandbox ", "sandbox"},
				"monitor": map[string]any{
					"enabled":          false,
					"interval_minutes": 3,
					"model":            " gpt-4o-mini ",
				},
				"auto_sync": map[string]any{
					"enabled":          false,
					"interval_minutes": 4,
				},
				"model_strategy": upstreamSourceModelStrategyFixed,
				"fixed_models":   []string{" GPT-4o ", "GPT-4o", "Claude-3"},
			},
		},
	})
	require.NoError(t, err)

	config, err := parseUpstreamSourceSyncConfig(string(raw))

	require.NoError(t, err)
	assert.Equal(t, "fallback", config.LocalGroup)
	assert.Equal(t, 3, config.MonitorIntervalMinutes)
	assert.True(t, config.AutoSyncEnabled)
	assert.Equal(t, 4, config.AutoSyncIntervalMinutes)
	assert.Equal(t, upstreamSourceModelStrategyFixed, config.ModelStrategy)
	assert.Equal(t, []string{"GPT-4o", "Claude-3"}, config.FixedModels)
	require.Len(t, config.LocalGroupRules, 1)
	rule := config.LocalGroupRules[0]
	assert.Equal(t, "Paid", rule.Name)
	assert.Equal(t, "paid", rule.LocalGroup)
	assert.Equal(t, []string{"openai", "claude"}, rule.Platforms)
	assert.Equal(t, []string{"pro"}, rule.NameContains)
	assert.Equal(t, []string{"plus"}, rule.DescriptionContains)
	assert.Equal(t, []string{"sandbox"}, rule.ExcludeKeywords)
	require.NotNil(t, rule.Monitor)
	require.NotNil(t, rule.Monitor.Enabled)
	assert.False(t, *rule.Monitor.Enabled)
	assert.Equal(t, 3, rule.Monitor.IntervalMinutes)
	assert.Equal(t, "gpt-4o-mini", rule.Monitor.Model)
	require.NotNil(t, rule.AutoSync)
	require.NotNil(t, rule.AutoSync.Enabled)
	assert.False(t, *rule.AutoSync.Enabled)
	assert.Equal(t, 4, rule.AutoSync.IntervalMinutes)
	assert.Equal(t, upstreamSourceModelStrategyFixed, rule.ModelStrategy)
	assert.Equal(t, []string{"GPT-4o", "Claude-3"}, rule.FixedModels)
}

func TestParseUpstreamSourceSyncConfigSupportsAutoPriority(t *testing.T) {
	raw, err := common.Marshal(map[string]any{
		"auto_priority_enabled":          true,
		"auto_priority_interval_minutes": 3,
		"auto_priority_window_hours":     999,
		"local_group_rules": []map[string]any{
			{
				"name":        "OpenAI pro",
				"local_group": "paid",
				"platforms":   []string{"openai"},
				"auto_priority": map[string]any{
					"enabled":          false,
					"interval_minutes": 0,
					"window_hours":     48,
				},
			},
		},
	})
	require.NoError(t, err)

	config, err := parseUpstreamSourceSyncConfig(string(raw))

	require.NoError(t, err)
	assert.True(t, config.AutoPriorityEnabled)
	assert.Equal(t, 3, config.AutoPriorityIntervalMinutes)
	assert.Equal(t, 168, config.AutoPriorityWindowHours)
	require.Len(t, config.LocalGroupRules, 1)
	rule := config.LocalGroupRules[0]
	require.NotNil(t, rule.AutoPriority)
	require.NotNil(t, rule.AutoPriority.Enabled)
	assert.False(t, *rule.AutoPriority.Enabled)
	require.NotNil(t, rule.AutoPriority.IntervalMinutes)
	require.NotNil(t, rule.AutoPriority.WindowHours)
	assert.Equal(t, 0, *rule.AutoPriority.IntervalMinutes)
	assert.Equal(t, 48, *rule.AutoPriority.WindowHours)
}

func TestResolveUpstreamSourceRuleAutoPriorityOverridesFallback(t *testing.T) {
	// The source-level auto_priority_* fields are legacy migration inputs
	// only; they no longer feed resolution once a rule matches. A rule that
	// overrides "enabled" but leaves interval/window unset falls back to the
	// hardcoded defaults below, not these (vestigial) source-level values.
	config := mustParseUpstreamSourceRuleTestConfig(t, map[string]any{
		"sync_config_version":            1,
		"auto_priority_enabled":          true,
		"auto_priority_interval_minutes": 5,
		"auto_priority_window_hours":     48,
		"local_group_rules": []map[string]any{
			{
				"name":        "OpenAI pro",
				"local_group": "paid",
				"platforms":   []string{"openai"},
				"auto_priority": map[string]any{
					"enabled": false,
				},
			},
		},
	})
	mapping := &model.UpstreamSourceChannelMapping{
		SyncEnabled:       true,
		DiscoveryStatus:   model.UpstreamMappingDiscoveryStatusActive,
		UpstreamPlatform:  "openai",
		UpstreamGroupName: "ChatGPT Pro",
	}

	resolution := resolveUpstreamSourceRule(config, mapping)

	assert.True(t, resolution.SyncEligible)
	assert.False(t, resolution.AutoPriorityEnabled)
	assert.Equal(t, upstreamSourceAutoPriorityDefaultIntervalMinutes, resolution.AutoPriorityIntervalMinutes)
	assert.Equal(t, upstreamSourceAutoPriorityDefaultWindowHours, resolution.AutoPriorityWindowHours)
}

func TestResolveUpstreamSourceRuleAutoPriorityPreservesExplicitZeroInterval(t *testing.T) {
	config := mustParseUpstreamSourceRuleTestConfig(t, map[string]any{
		"sync_config_version": 1,
		"local_group_rules": []map[string]any{
			{
				"name":        "OpenAI pro",
				"local_group": "paid",
				"platforms":   []string{"openai"},
				"auto_priority": map[string]any{
					"enabled":          true,
					"interval_minutes": 0,
					"window_hours":     48,
				},
			},
		},
	})
	mapping := &model.UpstreamSourceChannelMapping{
		SyncEnabled:       true,
		DiscoveryStatus:   model.UpstreamMappingDiscoveryStatusActive,
		UpstreamPlatform:  "openai",
		UpstreamGroupName: "ChatGPT Pro",
	}

	resolution := resolveUpstreamSourceRule(config, mapping)

	assert.True(t, resolution.SyncEligible)
	assert.True(t, resolution.AutoPriorityEnabled)
	assert.Equal(t, 0, resolution.AutoPriorityIntervalMinutes)
	assert.Equal(t, 48, resolution.AutoPriorityWindowHours)
}

func TestResolveUpstreamSourceRuleImageBridgePolicyOverridesFallback(t *testing.T) {
	config := mustParseUpstreamSourceRuleTestConfig(t, map[string]any{
		"codex_image_generation_bridge_policy": dto.CodexImageGenerationBridgePolicyEnabled,
		"local_group_rules": []map[string]any{
			{
				"name":                                 "OpenAI pro",
				"local_group":                          "paid",
				"platforms":                            []string{"openai"},
				"codex_image_generation_bridge_policy": dto.CodexImageGenerationBridgePolicyDisabled,
			},
		},
	})
	mapping := &model.UpstreamSourceChannelMapping{
		SyncEnabled:       true,
		DiscoveryStatus:   model.UpstreamMappingDiscoveryStatusActive,
		UpstreamPlatform:  "openai",
		UpstreamGroupName: "ChatGPT Pro",
	}

	resolution := resolveUpstreamSourceRule(config, mapping)

	assert.True(t, resolution.SyncEligible)
	assert.Equal(t, dto.CodexImageGenerationBridgePolicyDisabled, resolution.CodexImageGenerationBridgePolicy)
}

func TestResolveUpstreamSourceRuleImageBridgePolicyUsesFallbackForNewAPIGroup(t *testing.T) {
	config := mustParseUpstreamSourceRuleTestConfig(t, map[string]any{
		"codex_image_generation_bridge_policy": dto.CodexImageGenerationBridgePolicyDisabled,
		"local_group_rules": []map[string]any{
			{
				"name":        "New API OpenAI",
				"local_group": "openai",
				"platforms":   []string{"openai"},
			},
		},
	})
	mapping := &model.UpstreamSourceChannelMapping{
		SyncEnabled:       true,
		DiscoveryStatus:   model.UpstreamMappingDiscoveryStatusActive,
		UpstreamPlatform:  "openai",
		UpstreamGroupName: "ChatGPT",
	}

	resolution := resolveUpstreamSourceRule(config, mapping)

	assert.True(t, resolution.SyncEligible)
	assert.Equal(t, dto.CodexImageGenerationBridgePolicyDisabled, resolution.CodexImageGenerationBridgePolicy)
}

func TestResolveUpstreamSourceRuleMatchesPlatformAndKeywords(t *testing.T) {
	config := mustParseUpstreamSourceRuleTestConfig(t, map[string]any{
		"default_local_group":        "fallback",
		"enable_monitor":             false,
		"monitor_interval_minutes":   30,
		"auto_sync_enabled":          false,
		"auto_sync_interval_minutes": 20,
		"model_strategy":             upstreamSourceModelStrategyAllUpstream,
		"local_group_rules": []map[string]any{
			{
				"name":          "OpenAI paid",
				"local_group":   "paid",
				"platforms":     []string{"openai"},
				"name_contains": []string{"gpt"},
				"monitor": map[string]any{
					"enabled":          true,
					"interval_minutes": 3,
				},
				"auto_sync": map[string]any{
					"enabled":          true,
					"interval_minutes": 4,
				},
				"model_strategy": upstreamSourceModelStrategyFixed,
				"fixed_models":   []string{" GPT-4o ", "Claude-3"},
			},
		},
	})
	mapping := &model.UpstreamSourceChannelMapping{
		SyncEnabled:              true,
		UpstreamGroupName:        "GPT Pro",
		UpstreamGroupDescription: "Business plan",
		UpstreamPlatform:         " OpenAI ",
		DiscoveryStatus:          model.UpstreamMappingDiscoveryStatusActive,
	}

	resolution := resolveUpstreamSourceRule(config, mapping)

	assert.True(t, resolution.Matched)
	assert.True(t, resolution.SyncEligible)
	assert.Equal(t, "OpenAI paid", resolution.RuleName)
	assert.Equal(t, upstreamSourceMatchReasonMatched, resolution.Reason)
	assert.Equal(t, "paid", resolution.LocalGroup)
	assert.True(t, resolution.MonitorEnabled)
	assert.Equal(t, 3, resolution.MonitorIntervalMinutes)
	assert.True(t, resolution.AutoSyncEnabled)
	assert.Equal(t, 4, resolution.AutoSyncIntervalMinutes)
	assert.Equal(t, upstreamSourceModelStrategyFixed, resolution.ModelStrategy)
	assert.Equal(t, []string{"GPT-4o", "Claude-3"}, resolution.FixedModels)
}

func TestUpstreamSourceRuleMatchesPlatformGateHandlesUnknownPlatform(t *testing.T) {
	platformKeyword := dto.UpstreamSourceLocalGroupRule{Platforms: []string{"openai"}, NameContains: []string{"对接"}}
	platformOnly := dto.UpstreamSourceLocalGroupRule{Platforms: []string{"openai"}}
	nameOnly := dto.UpstreamSourceLocalGroupRule{NameContains: []string{"对接"}}

	cases := []struct {
		name         string
		rule         dto.UpstreamSourceLocalGroupRule
		platform     string
		groupName    string
		wantMatch    bool
		wantExcluded bool
	}{
		// Reported bug: a billing-tier group whose platform can't be inferred
		// (empty — no OpenAI/Claude signal in "对接倍率") must still match a
		// platform+keyword rule by its name instead of being silently excluded.
		{"unknown platform matches platform+keyword rule by name", platformKeyword, "", "对接倍率", true, false},
		{"unknown platform + name miss does not match", platformKeyword, "", "国产模型", false, false},
		{"known conflicting platform excluded even if name matches", platformKeyword, "anthropic", "对接倍率", false, false},
		{"known matching platform + name match", platformKeyword, "openai", "对接倍率", true, false},
		{"known matching platform + name miss does not match", platformKeyword, "openai", "国产模型", false, false},
		{"platform-only rule needs a known platform (unknown => no match)", platformOnly, "", "对接倍率", false, false},
		{"platform-only rule matches its known platform", platformOnly, "openai", "对接倍率", true, false},
		{"name-only rule unaffected by empty platform", nameOnly, "", "对接倍率", true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			matched, excluded := upstreamSourceRuleMatches(tc.rule, tc.platform, tc.groupName, "")
			assert.Equal(t, tc.wantMatch, matched, "matched")
			assert.Equal(t, tc.wantExcluded, excluded, "excluded")
		})
	}
}

func TestResolveMatchedRuleOverridesChannelTypePriorityWeight(t *testing.T) {
	priority := int64(42)
	weight := uint(9)
	cfg := upstreamSourceSyncConfig{
		ChannelType: constant.ChannelTypeOpenAI, DefaultPriority: 1, DefaultWeight: 2,
		LocalGroupRules: []dto.UpstreamSourceLocalGroupRule{{
			Name: "r", NameContains: []string{"gpt"},
			ChannelType: constant.ChannelTypeAnthropic, Priority: &priority, Weight: &weight,
		}},
	}
	cfg = normalizeUpstreamSourceSyncConfig(cfg)
	mapping := &model.UpstreamSourceChannelMapping{SyncEnabled: true, DiscoveryStatus: model.UpstreamMappingDiscoveryStatusActive, UpstreamGroupName: "gpt-pro"}
	res := resolveUpstreamSourceRule(cfg, mapping)
	assert.True(t, res.SyncEligible)
	assert.Equal(t, constant.ChannelTypeAnthropic, res.ChannelType)
	assert.Equal(t, int64(42), res.Priority)
	assert.Equal(t, uint(9), res.Weight)
}

func TestResolveUpstreamSourceRuleAllowsZeroRuleIntervalsToOverrideFallback(t *testing.T) {
	config := mustParseUpstreamSourceRuleTestConfig(t, map[string]any{
		"default_local_group":        "fallback",
		"enable_monitor":             true,
		"monitor_interval_minutes":   30,
		"auto_sync_enabled":          true,
		"auto_sync_interval_minutes": 30,
		"local_group_rules": []map[string]any{
			{
				"name":        "OpenAI realtime",
				"local_group": "openai",
				"platforms":   []string{"openai"},
				"monitor": map[string]any{
					"enabled":          true,
					"interval_minutes": 3,
				},
				"auto_sync": map[string]any{
					"enabled":          true,
					"interval_minutes": 0,
				},
			},
		},
	})
	mapping := &model.UpstreamSourceChannelMapping{
		SyncEnabled:      true,
		UpstreamPlatform: "openai",
		DiscoveryStatus:  model.UpstreamMappingDiscoveryStatusActive,
		LastSyncedAt:     3590,
	}

	resolution := resolveUpstreamSourceRule(config, mapping)

	assert.True(t, resolution.Matched)
	assert.True(t, resolution.MonitorEnabled)
	assert.Equal(t, 3, resolution.MonitorIntervalMinutes)
	assert.True(t, resolution.AutoSyncEnabled)
	assert.Equal(t, 0, resolution.AutoSyncIntervalMinutes)
	assert.True(t, upstreamSourceMappingAutoSyncDue(config, mapping, 3600))
}

func TestResolveUpstreamSourceRuleTreatsClaudePlatformAsAnthropic(t *testing.T) {
	config := mustParseUpstreamSourceRuleTestConfig(t, map[string]any{
		"default_local_group": "fallback",
		"local_group_rules": []map[string]any{
			{
				"name":        "Anthropic paid",
				"local_group": "paid",
				"platforms":   []string{"anthropic"},
			},
		},
	})
	mapping := &model.UpstreamSourceChannelMapping{
		SyncEnabled:      true,
		UpstreamPlatform: "claude",
		DiscoveryStatus:  model.UpstreamMappingDiscoveryStatusActive,
	}

	resolution := resolveUpstreamSourceRule(config, mapping)

	assert.True(t, resolution.Matched)
	assert.True(t, resolution.SyncEligible)
	assert.Equal(t, "Anthropic paid", resolution.RuleName)
	assert.Equal(t, "paid", resolution.LocalGroup)
}

func TestResolveUpstreamSourceRuleKeepsFallbackOnlyRule(t *testing.T) {
	// sync_config_version 1 keeps this a pure steady-state rule-matching
	// check: the rule's local_group stays genuinely empty (no migration
	// backfill from the source-level default), and resolution still falls
	// back to default_local_group for the local group itself.
	config := mustParseUpstreamSourceRuleTestConfig(t, map[string]any{
		"sync_config_version": 1,
		"default_local_group": "fallback",
		"local_group_rules": []map[string]any{
			{
				"name":        "OpenAI fallback",
				"local_group": "",
				"platforms":   []string{"openai"},
			},
		},
	})
	require.Len(t, config.LocalGroupRules, 1)
	assert.Empty(t, config.LocalGroupRules[0].LocalGroup)
	assert.Equal(t, []string{"openai"}, config.LocalGroupRules[0].Platforms)
	mapping := &model.UpstreamSourceChannelMapping{
		SyncEnabled:      true,
		UpstreamPlatform: "openai",
		DiscoveryStatus:  model.UpstreamMappingDiscoveryStatusActive,
	}

	resolution := resolveUpstreamSourceRule(config, mapping)

	assert.True(t, resolution.Matched)
	assert.True(t, resolution.SyncEligible)
	assert.Equal(t, "OpenAI fallback", resolution.RuleName)
	assert.Equal(t, upstreamSourceMatchReasonMatched, resolution.Reason)
	assert.Equal(t, "fallback", resolution.LocalGroup)
}

func TestResolveUpstreamSourceRuleAllowsEmptyDiscoveryStatusInLegacyMode(t *testing.T) {
	config := mustParseUpstreamSourceRuleTestConfig(t, map[string]any{
		"default_local_group": "fallback",
	})
	mapping := &model.UpstreamSourceChannelMapping{
		SyncEnabled: true,
	}

	resolution := resolveUpstreamSourceRule(config, mapping)

	assert.True(t, resolution.Matched)
	assert.True(t, resolution.SyncEligible)
	assert.Equal(t, upstreamSourceMatchReasonMatched, resolution.Reason)
	assert.Equal(t, "fallback", resolution.LocalGroup)
}

func TestResolveUpstreamSourceRuleKeepsKeywordFieldsSpecific(t *testing.T) {
	tests := []struct {
		name        string
		rule        map[string]any
		groupName   string
		description string
	}{
		{
			name: "name keyword does not match description",
			rule: map[string]any{
				"name":          "name-only",
				"local_group":   "paid",
				"name_contains": []string{"pro"},
			},
			groupName:   "GPT",
			description: "pro only",
		},
		{
			name: "description keyword does not match name",
			rule: map[string]any{
				"name":                 "description-only",
				"local_group":          "paid",
				"description_contains": []string{"pro"},
			},
			groupName:   "GPT Pro",
			description: "basic only",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := mustParseUpstreamSourceRuleTestConfig(t, map[string]any{
				"default_local_group": "fallback",
				"local_group_rules":   []map[string]any{tt.rule},
			})
			mapping := &model.UpstreamSourceChannelMapping{
				SyncEnabled:              true,
				UpstreamGroupName:        tt.groupName,
				UpstreamGroupDescription: tt.description,
				DiscoveryStatus:          model.UpstreamMappingDiscoveryStatusActive,
			}

			resolution := resolveUpstreamSourceRule(config, mapping)

			assert.False(t, resolution.Matched)
			assert.False(t, resolution.SyncEligible)
			assert.Equal(t, upstreamSourceMatchReasonNoMatchingRule, resolution.Reason)
			assert.Equal(t, "fallback", resolution.LocalGroup)
		})
	}
}

func TestResolveUpstreamSourceRuleRejectsExcludeKeywords(t *testing.T) {
	config := mustParseUpstreamSourceRuleTestConfig(t, map[string]any{
		"default_local_group": "fallback",
		"local_group_rules": []map[string]any{
			{
				"name":             "OpenAI paid",
				"local_group":      "paid",
				"platforms":        []string{"openai"},
				"name_contains":    []string{"gpt"},
				"exclude_keywords": []string{"sandbox"},
			},
		},
	})
	mapping := &model.UpstreamSourceChannelMapping{
		SyncEnabled:              true,
		UpstreamGroupName:        "GPT Sandbox",
		UpstreamGroupDescription: "Business plan",
		UpstreamPlatform:         "openai",
		DiscoveryStatus:          model.UpstreamMappingDiscoveryStatusActive,
	}

	resolution := resolveUpstreamSourceRule(config, mapping)

	assert.False(t, resolution.Matched)
	assert.False(t, resolution.SyncEligible)
	assert.Equal(t, upstreamSourceMatchReasonExcludedByKeyword, resolution.Reason)
	assert.Equal(t, "fallback", resolution.LocalGroup)
}

func TestResolveUpstreamSourceRuleUsesFirstMatch(t *testing.T) {
	config := mustParseUpstreamSourceRuleTestConfig(t, map[string]any{
		"default_local_group": "fallback",
		"local_group_rules": []map[string]any{
			{
				"name":          "first",
				"local_group":   "first-group",
				"name_contains": []string{"gpt"},
			},
			{
				"name":          "second",
				"local_group":   "second-group",
				"name_contains": []string{"gpt"},
			},
		},
	})
	mapping := &model.UpstreamSourceChannelMapping{
		SyncEnabled:       true,
		UpstreamGroupName: "GPT Pro",
		DiscoveryStatus:   model.UpstreamMappingDiscoveryStatusActive,
	}

	resolution := resolveUpstreamSourceRule(config, mapping)

	assert.True(t, resolution.Matched)
	assert.True(t, resolution.SyncEligible)
	assert.Equal(t, "first", resolution.RuleName)
	assert.Equal(t, "first-group", resolution.LocalGroup)
}

func TestResolveUpstreamSourceRuleLeavesUnmatchedGroupsUnsynced(t *testing.T) {
	// The source-level fields below are legacy migration inputs only. The
	// mapping matches no rule here, so resolution must fall through to the
	// hardcoded fallback constants and must NOT leak any of these
	// source-level values (that reliance is exactly what this fold-down
	// removes).
	config := mustParseUpstreamSourceRuleTestConfig(t, map[string]any{
		"sync_config_version":        1,
		"default_local_group":        "fallback",
		"enable_monitor":             true,
		"monitor_interval_minutes":   10,
		"auto_sync_enabled":          true,
		"auto_sync_interval_minutes": 15,
		"model_strategy":             upstreamSourceModelStrategyFixed,
		"fixed_models":               []string{"GPT-4o"},
		"local_group_rules": []map[string]any{
			{
				"name":          "claude",
				"local_group":   "claude-group",
				"name_contains": []string{"claude"},
			},
		},
	})
	mapping := &model.UpstreamSourceChannelMapping{
		SyncEnabled:              true,
		UpstreamGroupName:        "GPT Pro",
		UpstreamGroupDescription: "Business plan",
		UpstreamPlatform:         "openai",
		DiscoveryStatus:          model.UpstreamMappingDiscoveryStatusActive,
	}

	resolution := resolveUpstreamSourceRule(config, mapping)

	assert.False(t, resolution.Matched)
	assert.False(t, resolution.SyncEligible)
	assert.Equal(t, upstreamSourceMatchReasonNoMatchingRule, resolution.Reason)
	assert.Equal(t, "fallback", resolution.LocalGroup)
	assert.False(t, resolution.MonitorEnabled)
	assert.Equal(t, 0, resolution.MonitorIntervalMinutes)
	assert.False(t, resolution.AutoSyncEnabled)
	assert.Equal(t, 0, resolution.AutoSyncIntervalMinutes)
	assert.Equal(t, upstreamSourceModelStrategyAllUpstream, resolution.ModelStrategy)
	assert.Empty(t, resolution.FixedModels)
}

func TestMonitorIntervalMinimumIsOne(t *testing.T) {
	assert.Equal(t, 2, normalizeUpstreamSourceRuleInterval(2))
	assert.Equal(t, 1, normalizeUpstreamSourceRuleInterval(1))
	assert.Equal(t, 0, normalizeUpstreamSourceRuleInterval(0)) // 0 = inherit/disabled
	cfg := normalizeUpstreamSourceSyncConfig(upstreamSourceSyncConfig{MonitorIntervalMinutes: 2})
	assert.Equal(t, 2, cfg.MonitorIntervalMinutes)
}

func TestLegacyNoRulesMigratesToCatchAll(t *testing.T) {
	// version 0 (absent) blob with source-level defaults, no rules
	raw := `{"channel_type":14,"default_priority":7,"default_weight":11,"enable_monitor":true,"monitor_interval_minutes":3,"local_group":"grp"}`
	cfg, err := parseUpstreamSourceSyncConfig(raw)
	require.NoError(t, err)
	require.Len(t, cfg.LocalGroupRules, 1)
	r := cfg.LocalGroupRules[0]
	assert.Equal(t, 14, r.ChannelType)
	require.NotNil(t, r.Priority)
	assert.Equal(t, int64(7), *r.Priority)
	require.NotNil(t, r.Weight)
	assert.Equal(t, uint(11), *r.Weight)
	require.NotNil(t, r.Monitor)
	require.NotNil(t, r.Monitor.Enabled)
	assert.True(t, *r.Monitor.Enabled)
	// migrated catch-all matches any group
	m := &model.UpstreamSourceChannelMapping{SyncEnabled: true, DiscoveryStatus: model.UpstreamMappingDiscoveryStatusActive, UpstreamGroupName: "anything"}
	assert.True(t, resolveUpstreamSourceRule(cfg, m).SyncEligible)
}

func TestNoRulesV1SyncsNothing(t *testing.T) {
	cfg, err := parseUpstreamSourceSyncConfig(`{"sync_config_version":1,"local_group_rules":[]}`)
	require.NoError(t, err)
	m := &model.UpstreamSourceChannelMapping{SyncEnabled: true, DiscoveryStatus: model.UpstreamMappingDiscoveryStatusActive, UpstreamGroupName: "x"}
	assert.False(t, resolveUpstreamSourceRule(cfg, m).SyncEligible)
}

func TestEmptyMatcherRuleMatchesAll(t *testing.T) {
	cfg, err := parseUpstreamSourceSyncConfig(`{"sync_config_version":1,"local_group_rules":[{"name":"catch","local_group":"g"}]}`)
	require.NoError(t, err)
	require.Len(t, cfg.LocalGroupRules, 1)
	m := &model.UpstreamSourceChannelMapping{SyncEnabled: true, DiscoveryStatus: model.UpstreamMappingDiscoveryStatusActive, UpstreamGroupName: "whatever"}
	assert.True(t, resolveUpstreamSourceRule(cfg, m).SyncEligible)
}

// TestResolveUpstreamSourceRuleBlankRuleLocalGroupFallsBackToSourceBaseGroup
// pins the contract that a matched rule which leaves local_group blank
// inherits the source's real base group (LocalGroup), not a stale
// "default" placeholder. DefaultLocalGroup starts empty here (as
// normalizeUpstreamSourceSyncConfig produces before copying LocalGroup into
// it), matching what a correctly-behaving create/update path should hand
// off to the service layer.
func TestResolveUpstreamSourceRuleBlankRuleLocalGroupFallsBackToSourceBaseGroup(t *testing.T) {
	config := mustParseUpstreamSourceRuleTestConfig(t, map[string]any{
		"sync_config_version": 1,
		"local_group":         "paid",
		"local_group_rules": []map[string]any{
			{
				"name":        "OpenAI catch-all",
				"local_group": "",
				"platforms":   []string{"openai"},
			},
		},
	})
	require.Equal(t, "paid", config.LocalGroup)
	require.Equal(t, "paid", config.DefaultLocalGroup)
	mapping := &model.UpstreamSourceChannelMapping{
		SyncEnabled:      true,
		DiscoveryStatus:  model.UpstreamMappingDiscoveryStatusActive,
		UpstreamPlatform: "openai",
	}

	resolution := resolveUpstreamSourceRule(config, mapping)

	assert.True(t, resolution.Matched)
	assert.True(t, resolution.SyncEligible)
	assert.Equal(t, "paid", resolution.LocalGroup)
}

func mustParseUpstreamSourceRuleTestConfig(t *testing.T, values map[string]any) upstreamSourceSyncConfig {
	t.Helper()

	raw, err := common.Marshal(values)
	require.NoError(t, err)
	config, err := parseUpstreamSourceSyncConfig(string(raw))
	require.NoError(t, err)
	return config
}
