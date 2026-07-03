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
	assert.Equal(t, 5, config.MonitorIntervalMinutes)
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
	assert.Equal(t, 5, rule.Monitor.IntervalMinutes)
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
	config := mustParseUpstreamSourceRuleTestConfig(t, map[string]any{
		"auto_priority_enabled":          false,
		"auto_priority_interval_minutes": 30,
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
	assert.Equal(t, 30, resolution.AutoPriorityIntervalMinutes)
	assert.Equal(t, 48, resolution.AutoPriorityWindowHours)
}

func TestResolveUpstreamSourceRuleAutoPriorityPreservesExplicitZeroInterval(t *testing.T) {
	config := mustParseUpstreamSourceRuleTestConfig(t, map[string]any{
		"auto_priority_enabled":          true,
		"auto_priority_interval_minutes": 30,
		"auto_priority_window_hours":     48,
		"local_group_rules": []map[string]any{
			{
				"name":        "OpenAI pro",
				"local_group": "paid",
				"platforms":   []string{"openai"},
				"auto_priority": map[string]any{
					"interval_minutes": 0,
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
	assert.Equal(t, 5, resolution.MonitorIntervalMinutes)
	assert.True(t, resolution.AutoSyncEnabled)
	assert.Equal(t, 4, resolution.AutoSyncIntervalMinutes)
	assert.Equal(t, upstreamSourceModelStrategyFixed, resolution.ModelStrategy)
	assert.Equal(t, []string{"GPT-4o", "Claude-3"}, resolution.FixedModels)
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
	assert.Equal(t, 5, resolution.MonitorIntervalMinutes)
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
	config := mustParseUpstreamSourceRuleTestConfig(t, map[string]any{
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
	config := mustParseUpstreamSourceRuleTestConfig(t, map[string]any{
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
	assert.True(t, resolution.MonitorEnabled)
	assert.Equal(t, 10, resolution.MonitorIntervalMinutes)
	assert.True(t, resolution.AutoSyncEnabled)
	assert.Equal(t, 15, resolution.AutoSyncIntervalMinutes)
	assert.Equal(t, upstreamSourceModelStrategyFixed, resolution.ModelStrategy)
	assert.Equal(t, []string{"GPT-4o"}, resolution.FixedModels)
}

func mustParseUpstreamSourceRuleTestConfig(t *testing.T, values map[string]any) upstreamSourceSyncConfig {
	t.Helper()

	raw, err := common.Marshal(values)
	require.NoError(t, err)
	config, err := parseUpstreamSourceSyncConfig(string(raw))
	require.NoError(t, err)
	return config
}
