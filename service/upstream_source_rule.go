package service

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
)

const (
	upstreamSourceModelStrategyAllUpstream           = "all_upstream"
	upstreamSourceModelStrategyFixed                 = "fixed"
	upstreamSourceAutoPriorityDefaultIntervalMinutes = 30
	upstreamSourceAutoPriorityDefaultWindowHours     = 24
	upstreamSourceAutoPriorityMaxWindowHours         = 168

	upstreamSourceMatchReasonMatched           = "matched"
	upstreamSourceMatchReasonNoMatchingRule    = "no matching rule"
	upstreamSourceMatchReasonExcludedByKeyword = "excluded by keyword"
	upstreamSourceMatchReasonManualDisabled    = "manual disabled"
	upstreamSourceMatchReasonInactiveDiscovery = "inactive discovery"
	upstreamSourceMatchReasonAutoSyncNotDue    = "auto sync interval not due"
)

// Keep this JSON shape in lockstep with controller.upstreamSourceControllerSyncConfig.
// AutoSyncModels stays pointer-based here so absent keys can preserve the
// historical default while explicit false remains distinguishable.
type upstreamSourceSyncConfig struct {
	LocalGroup                       string                             `json:"local_group"`
	ChannelType                      int                                `json:"channel_type"`
	DefaultPriority                  int64                              `json:"default_priority"`
	DefaultWeight                    uint                               `json:"default_weight"`
	EnableMonitor                    bool                               `json:"enable_monitor"`
	MonitorIntervalMinutes           int                                `json:"monitor_interval_minutes"`
	AutoSyncModels                   *bool                              `json:"auto_sync_models"`
	ModelStrategy                    string                             `json:"model_strategy"`
	FixedModels                      []string                           `json:"fixed_models"`
	AllowPrivateIP                   common.FlexibleBool                `json:"allow_private_ip"`
	AutoSyncEnabled                  bool                               `json:"auto_sync_enabled"`
	AutoSyncIntervalMinutes          int                                `json:"auto_sync_interval_minutes"`
	AutoPriorityEnabled              bool                               `json:"auto_priority_enabled"`
	AutoPriorityIntervalMinutes      int                                `json:"auto_priority_interval_minutes"`
	AutoPriorityWindowHours          int                                `json:"auto_priority_window_hours"`
	CodexImageGenerationBridgePolicy string                             `json:"codex_image_generation_bridge_policy"`
	DefaultLocalGroup                string                             `json:"default_local_group"`
	LocalGroupRules                  []dto.UpstreamSourceLocalGroupRule `json:"local_group_rules"`
}

type upstreamSourceRuleResolution struct {
	Matched                          bool
	SyncEligible                     bool
	RuleName                         string
	Reason                           string
	LocalGroup                       string
	ChannelType                      int
	Priority                         int64
	Weight                           uint
	MonitorEnabled                   bool
	MonitorIntervalMinutes           int
	MonitorModel                     string
	AutoSyncEnabled                  bool
	AutoSyncIntervalMinutes          int
	AutoPriorityEnabled              bool
	AutoPriorityIntervalMinutes      int
	AutoPriorityWindowHours          int
	CodexImageGenerationBridgePolicy string
	ModelStrategy                    string
	FixedModels                      []string
}

func parseUpstreamSourceSyncConfig(raw string) (upstreamSourceSyncConfig, error) {
	config := upstreamSourceSyncConfig{
		LocalGroup:                  "default",
		ChannelType:                 constant.ChannelTypeOpenAI,
		AutoSyncModels:              common.GetPointer(true),
		DefaultPriority:             0,
		DefaultWeight:               0,
		ModelStrategy:               upstreamSourceModelStrategyAllUpstream,
		AutoPriorityIntervalMinutes: upstreamSourceAutoPriorityDefaultIntervalMinutes,
		AutoPriorityWindowHours:     upstreamSourceAutoPriorityDefaultWindowHours,
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
	if strings.TrimSpace(config.LocalGroup) == "" {
		config.LocalGroup = "default"
	} else {
		config.LocalGroup = strings.TrimSpace(config.LocalGroup)
	}
	if config.ChannelType == 0 {
		config.ChannelType = constant.ChannelTypeOpenAI
	}
	if config.AutoSyncModels == nil {
		config.AutoSyncModels = common.GetPointer(true)
	}
	if strings.TrimSpace(config.DefaultLocalGroup) == "" {
		config.DefaultLocalGroup = config.LocalGroup
	} else {
		config.DefaultLocalGroup = strings.TrimSpace(config.DefaultLocalGroup)
	}
	if config.MonitorIntervalMinutes > 0 && config.MonitorIntervalMinutes < 5 {
		config.MonitorIntervalMinutes = 5
	}
	if config.AutoSyncEnabled {
		if config.AutoSyncIntervalMinutes < 0 {
			config.AutoSyncIntervalMinutes = 0
		}
	} else {
		config.AutoSyncIntervalMinutes = 0
	}
	config.AutoPriorityIntervalMinutes = normalizeUpstreamSourceAutoPriorityInterval(config.AutoPriorityIntervalMinutes)
	config.AutoPriorityWindowHours = normalizeUpstreamSourceAutoPriorityWindow(config.AutoPriorityWindowHours)
	config.CodexImageGenerationBridgePolicy = dto.NormalizeCodexImageGenerationBridgePolicy(config.CodexImageGenerationBridgePolicy)
	config.ModelStrategy = normalizeUpstreamSourceFallbackModelStrategy(config.ModelStrategy, config.AutoSyncModels)
	config.FixedModels = normalizeUpstreamSourceFixedModels(config.FixedModels)
	config.LocalGroupRules = normalizeUpstreamSourceLocalGroupRules(config.LocalGroupRules)
	return config
}

func normalizeUpstreamSourceFallbackModelStrategy(modelStrategy string, autoSyncModels *bool) string {
	switch strings.TrimSpace(modelStrategy) {
	case upstreamSourceModelStrategyAllUpstream:
		return upstreamSourceModelStrategyAllUpstream
	case upstreamSourceModelStrategyFixed:
		return upstreamSourceModelStrategyFixed
	case "":
		if autoSyncModels != nil && !*autoSyncModels {
			return upstreamSourceModelStrategyFixed
		}
		return upstreamSourceModelStrategyAllUpstream
	default:
		return upstreamSourceModelStrategyAllUpstream
	}
}

func normalizeUpstreamSourceRuleModelStrategy(modelStrategy string) string {
	switch strings.TrimSpace(modelStrategy) {
	case upstreamSourceModelStrategyAllUpstream:
		return upstreamSourceModelStrategyAllUpstream
	case upstreamSourceModelStrategyFixed:
		return upstreamSourceModelStrategyFixed
	default:
		return ""
	}
}

func normalizeUpstreamSourceFixedModels(models []string) []string {
	normalized := make([]string, 0, len(models))
	seen := make(map[string]struct{}, len(models))
	for _, modelName := range models {
		trimmed := strings.TrimSpace(modelName)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		normalized = append(normalized, trimmed)
	}
	return normalized
}

func normalizeUpstreamSourceLocalGroupRules(rules []dto.UpstreamSourceLocalGroupRule) []dto.UpstreamSourceLocalGroupRule {
	normalized := make([]dto.UpstreamSourceLocalGroupRule, 0, len(rules))
	for _, rule := range rules {
		localGroup := strings.TrimSpace(rule.LocalGroup)
		platforms := normalizeUpstreamSourceRuleKeywords(rule.Platforms)
		nameContains := normalizeUpstreamSourceRuleKeywords(rule.NameContains)
		descriptionContains := normalizeUpstreamSourceRuleKeywords(rule.DescriptionContains)
		if len(platforms) == 0 && len(nameContains) == 0 && len(descriptionContains) == 0 {
			continue
		}
		normalized = append(normalized, dto.UpstreamSourceLocalGroupRule{
			Name:                             strings.TrimSpace(rule.Name),
			LocalGroup:                       localGroup,
			ChannelType:                      rule.ChannelType,
			Priority:                         rule.Priority,
			Weight:                           rule.Weight,
			Platforms:                        platforms,
			NameContains:                     nameContains,
			DescriptionContains:              descriptionContains,
			ExcludeKeywords:                  normalizeUpstreamSourceRuleKeywords(rule.ExcludeKeywords),
			Monitor:                          normalizeUpstreamSourceRuleMonitor(rule.Monitor),
			AutoSync:                         normalizeUpstreamSourceRuleAutoSync(rule.AutoSync),
			AutoPriority:                     normalizeUpstreamSourceRuleAutoPriority(rule.AutoPriority),
			CodexImageGenerationBridgePolicy: normalizeUpstreamSourceRuleCodexImageGenerationBridgePolicy(rule.CodexImageGenerationBridgePolicy),
			ModelStrategy:                    normalizeUpstreamSourceRuleModelStrategy(rule.ModelStrategy),
			FixedModels:                      normalizeUpstreamSourceFixedModels(rule.FixedModels),
		})
	}
	return normalized
}

func normalizeUpstreamSourceRuleCodexImageGenerationBridgePolicy(policy string) string {
	if strings.TrimSpace(policy) == "" {
		return ""
	}
	return dto.NormalizeCodexImageGenerationBridgePolicy(policy)
}

func NormalizeUpstreamSourceLocalGroupRulesForConfig(rules []dto.UpstreamSourceLocalGroupRule) []dto.UpstreamSourceLocalGroupRule {
	return normalizeUpstreamSourceLocalGroupRules(rules)
}

func normalizeUpstreamSourceRuleMonitor(monitor *dto.UpstreamSourceRuleMonitor) *dto.UpstreamSourceRuleMonitor {
	if monitor == nil {
		return nil
	}
	normalized := &dto.UpstreamSourceRuleMonitor{
		Enabled:         cloneUpstreamSourceRuleBool(monitor.Enabled),
		IntervalMinutes: normalizeUpstreamSourceRuleInterval(monitor.IntervalMinutes),
		Model:           strings.TrimSpace(monitor.Model),
	}
	return normalized
}

func normalizeUpstreamSourceRuleAutoSync(autoSync *dto.UpstreamSourceRuleAutoSync) *dto.UpstreamSourceRuleAutoSync {
	if autoSync == nil {
		return nil
	}
	normalized := &dto.UpstreamSourceRuleAutoSync{
		Enabled:         cloneUpstreamSourceRuleBool(autoSync.Enabled),
		IntervalMinutes: normalizeUpstreamSourceAutoSyncInterval(autoSync.IntervalMinutes),
	}
	return normalized
}

func normalizeUpstreamSourceRuleAutoPriority(autoPriority *dto.UpstreamSourceRuleAutoPriority) *dto.UpstreamSourceRuleAutoPriority {
	if autoPriority == nil {
		return nil
	}
	normalized := &dto.UpstreamSourceRuleAutoPriority{
		Enabled: cloneUpstreamSourceRuleBool(autoPriority.Enabled),
	}
	if autoPriority.IntervalMinutes != nil {
		value := normalizeUpstreamSourceAutoPriorityInterval(*autoPriority.IntervalMinutes)
		normalized.IntervalMinutes = &value
	}
	if autoPriority.WindowHours != nil {
		value := normalizeUpstreamSourceAutoPriorityWindow(*autoPriority.WindowHours)
		normalized.WindowHours = &value
	}
	return normalized
}

func cloneUpstreamSourceRuleBool(value *bool) *bool {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}

func normalizeUpstreamSourceRuleInterval(intervalMinutes int) int {
	if intervalMinutes > 0 && intervalMinutes < 5 {
		return 5
	}
	return intervalMinutes
}

func normalizeUpstreamSourceRuleKeywords(values []string) []string {
	normalized := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		keyword := strings.ToLower(strings.TrimSpace(value))
		if keyword == "" {
			continue
		}
		if _, ok := seen[keyword]; ok {
			continue
		}
		seen[keyword] = struct{}{}
		normalized = append(normalized, keyword)
	}
	return normalized
}

func resolveUpstreamSourceRule(config upstreamSourceSyncConfig, mapping *model.UpstreamSourceChannelMapping) upstreamSourceRuleResolution {
	fallback := upstreamSourceRuleFallbackResolution(config)
	if mapping == nil {
		return fallback
	}
	discoveryStatus := strings.TrimSpace(mapping.DiscoveryStatus)
	if discoveryStatus != "" && discoveryStatus != model.UpstreamMappingDiscoveryStatusActive {
		fallback.Reason = upstreamSourceMatchReasonInactiveDiscovery
		return fallback
	}
	if !mapping.SyncEnabled {
		fallback.Reason = upstreamSourceMatchReasonManualDisabled
		return fallback
	}
	if len(config.LocalGroupRules) == 0 {
		fallback.Matched = true
		fallback.SyncEligible = true
		fallback.Reason = upstreamSourceMatchReasonMatched
		return fallback
	}

	name := strings.ToLower(strings.TrimSpace(mapping.UpstreamGroupName))
	description := strings.ToLower(strings.TrimSpace(mapping.UpstreamGroupDescription))
	platform := strings.ToLower(strings.TrimSpace(mapping.UpstreamPlatform))
	excluded := false
	for _, rule := range config.LocalGroupRules {
		matched, ruleExcluded := upstreamSourceRuleMatches(rule, platform, name, description)
		if ruleExcluded {
			excluded = true
			continue
		}
		if matched {
			return resolveUpstreamSourceMatchedRule(config, rule)
		}
	}
	if excluded {
		fallback.Reason = upstreamSourceMatchReasonExcludedByKeyword
		return fallback
	}
	return fallback
}

func upstreamSourceRuleFallbackResolution(config upstreamSourceSyncConfig) upstreamSourceRuleResolution {
	monitorInterval := config.MonitorIntervalMinutes
	if monitorInterval > 0 && monitorInterval < 5 {
		monitorInterval = 5
	}
	autoSyncInterval := config.AutoSyncIntervalMinutes
	if config.AutoSyncEnabled {
		if autoSyncInterval < 0 {
			autoSyncInterval = 0
		}
	} else {
		autoSyncInterval = 0
	}
	modelStrategy := normalizeUpstreamSourceFallbackModelStrategy(config.ModelStrategy, config.AutoSyncModels)
	fixedModels := []string(nil)
	if modelStrategy == upstreamSourceModelStrategyFixed {
		fixedModels = normalizeUpstreamSourceFixedModels(config.FixedModels)
	}
	channelType := config.ChannelType
	if channelType == 0 {
		channelType = constant.ChannelTypeOpenAI
	}
	return upstreamSourceRuleResolution{
		Reason:                           upstreamSourceMatchReasonNoMatchingRule,
		LocalGroup:                       upstreamSourceDefaultLocalGroup(config),
		ChannelType:                      channelType,
		Priority:                         config.DefaultPriority,
		Weight:                           config.DefaultWeight,
		MonitorEnabled:                   config.EnableMonitor,
		MonitorIntervalMinutes:           monitorInterval,
		AutoSyncEnabled:                  config.AutoSyncEnabled,
		AutoSyncIntervalMinutes:          autoSyncInterval,
		AutoPriorityEnabled:              config.AutoPriorityEnabled,
		AutoPriorityIntervalMinutes:      normalizeUpstreamSourceAutoPriorityInterval(config.AutoPriorityIntervalMinutes),
		AutoPriorityWindowHours:          normalizeUpstreamSourceAutoPriorityWindow(config.AutoPriorityWindowHours),
		CodexImageGenerationBridgePolicy: dto.NormalizeCodexImageGenerationBridgePolicy(config.CodexImageGenerationBridgePolicy),
		ModelStrategy:                    modelStrategy,
		FixedModels:                      fixedModels,
	}
}

func upstreamSourceDefaultLocalGroup(config upstreamSourceSyncConfig) string {
	if localGroup := strings.TrimSpace(config.DefaultLocalGroup); localGroup != "" {
		return localGroup
	}
	if localGroup := strings.TrimSpace(config.LocalGroup); localGroup != "" {
		return localGroup
	}
	return "default"
}

func upstreamSourceRuleMatches(rule dto.UpstreamSourceLocalGroupRule, platform string, name string, description string) (bool, bool) {
	if len(rule.Platforms) > 0 && !upstreamSourceRulePlatformMatches(platform, rule.Platforms) {
		return false, false
	}
	if upstreamSourceRuleKeywordsMatchAnyText([]string{name, description}, rule.ExcludeKeywords) {
		return false, true
	}

	includeMatched := upstreamSourceKeywordsMatch(name, rule.NameContains) ||
		upstreamSourceKeywordsMatch(description, rule.DescriptionContains)
	if len(rule.Platforms) > 0 {
		return (len(rule.NameContains) == 0 && len(rule.DescriptionContains) == 0) || includeMatched, false
	}
	return includeMatched, false
}

func upstreamSourceRulePlatformMatches(platform string, platforms []string) bool {
	normalized := normalizeUpstreamSourceRulePlatform(platform)
	if normalized == "" {
		return false
	}
	for _, candidate := range platforms {
		if normalized == normalizeUpstreamSourceRulePlatform(candidate) {
			return true
		}
	}
	return false
}

func normalizeUpstreamSourceRulePlatform(platform string) string {
	normalized := strings.ToLower(strings.TrimSpace(platform))
	if normalized == "claude" {
		return "anthropic"
	}
	return normalized
}

func upstreamSourceRuleKeywordsMatchAnyText(texts []string, keywords []string) bool {
	if len(keywords) == 0 {
		return false
	}
	for _, text := range texts {
		if upstreamSourceKeywordsMatch(strings.ToLower(strings.TrimSpace(text)), keywords) {
			return true
		}
	}
	return false
}

func resolveUpstreamSourceMatchedRule(config upstreamSourceSyncConfig, rule dto.UpstreamSourceLocalGroupRule) upstreamSourceRuleResolution {
	resolution := upstreamSourceRuleFallbackResolution(config)
	resolution.Matched = true
	resolution.SyncEligible = true
	resolution.RuleName = strings.TrimSpace(rule.Name)
	resolution.Reason = upstreamSourceMatchReasonMatched
	if localGroup := strings.TrimSpace(rule.LocalGroup); localGroup != "" {
		resolution.LocalGroup = localGroup
	}
	if rule.ChannelType != 0 {
		resolution.ChannelType = rule.ChannelType
	}
	if rule.Priority != nil {
		resolution.Priority = *rule.Priority
	}
	if rule.Weight != nil {
		resolution.Weight = *rule.Weight
	}
	if rule.Monitor != nil {
		if rule.Monitor.Enabled != nil {
			resolution.MonitorEnabled = *rule.Monitor.Enabled
		}
		if rule.Monitor.IntervalMinutes > 0 {
			resolution.MonitorIntervalMinutes = normalizeUpstreamSourceRuleInterval(rule.Monitor.IntervalMinutes)
		}
		if m := strings.TrimSpace(rule.Monitor.Model); m != "" {
			resolution.MonitorModel = m
		}
	}
	if rule.AutoSync != nil {
		if rule.AutoSync.Enabled != nil {
			resolution.AutoSyncEnabled = *rule.AutoSync.Enabled
		}
		resolution.AutoSyncIntervalMinutes = normalizeUpstreamSourceAutoSyncInterval(rule.AutoSync.IntervalMinutes)
	}
	if rule.AutoPriority != nil {
		if rule.AutoPriority.Enabled != nil {
			resolution.AutoPriorityEnabled = *rule.AutoPriority.Enabled
		}
		if rule.AutoPriority.IntervalMinutes != nil {
			resolution.AutoPriorityIntervalMinutes = normalizeUpstreamSourceAutoPriorityInterval(*rule.AutoPriority.IntervalMinutes)
		}
		if rule.AutoPriority.WindowHours != nil {
			resolution.AutoPriorityWindowHours = normalizeUpstreamSourceAutoPriorityWindow(*rule.AutoPriority.WindowHours)
		}
	}
	if strings.TrimSpace(rule.CodexImageGenerationBridgePolicy) != "" {
		resolution.CodexImageGenerationBridgePolicy = dto.NormalizeCodexImageGenerationBridgePolicy(rule.CodexImageGenerationBridgePolicy)
	}
	if modelStrategy := normalizeUpstreamSourceRuleModelStrategy(rule.ModelStrategy); modelStrategy != "" {
		resolution.ModelStrategy = modelStrategy
		if modelStrategy == upstreamSourceModelStrategyFixed {
			resolution.FixedModels = normalizeUpstreamSourceFixedModels(rule.FixedModels)
		} else {
			resolution.FixedModels = nil
		}
	}
	return resolution
}

func upstreamSourceHasAutoSyncSchedule(config upstreamSourceSyncConfig) bool {
	if config.AutoSyncEnabled {
		return true
	}
	for _, rule := range config.LocalGroupRules {
		if upstreamSourceRuleAutoSyncEnabled(config, rule) {
			return true
		}
	}
	return false
}

func upstreamSourceCoarseAutoSyncIntervalMinutes(config upstreamSourceSyncConfig) int {
	interval := 0
	if config.AutoSyncEnabled {
		interval = normalizeUpstreamSourceAutoSyncInterval(config.AutoSyncIntervalMinutes)
	}
	for _, rule := range config.LocalGroupRules {
		if !upstreamSourceRuleAutoSyncEnabled(config, rule) {
			continue
		}
		ruleInterval := upstreamSourceRuleAutoSyncInterval(config, rule)
		if ruleInterval == 0 {
			return 0
		}
		if interval == 0 || ruleInterval < interval {
			interval = ruleInterval
		}
	}
	return interval
}

func upstreamSourceMappingAutoSyncDue(config upstreamSourceSyncConfig, mapping *model.UpstreamSourceChannelMapping, now int64) bool {
	resolution := resolveUpstreamSourceRule(config, mapping)
	if !resolution.SyncEligible || !resolution.AutoSyncEnabled || resolution.AutoSyncIntervalMinutes < 0 {
		return false
	}
	if resolution.AutoSyncIntervalMinutes == 0 {
		return true
	}
	if mapping == nil || mapping.LastSyncedAt == 0 {
		return true
	}
	return now-mapping.LastSyncedAt >= int64(resolution.AutoSyncIntervalMinutes)*60
}

func upstreamSourceRuleAutoSyncEnabled(config upstreamSourceSyncConfig, rule dto.UpstreamSourceLocalGroupRule) bool {
	enabled := config.AutoSyncEnabled
	if rule.AutoSync != nil && rule.AutoSync.Enabled != nil {
		enabled = *rule.AutoSync.Enabled
	}
	return enabled
}

func upstreamSourceRuleAutoSyncInterval(config upstreamSourceSyncConfig, rule dto.UpstreamSourceLocalGroupRule) int {
	interval := 0
	if config.AutoSyncEnabled {
		interval = normalizeUpstreamSourceAutoSyncInterval(config.AutoSyncIntervalMinutes)
	}
	if rule.AutoSync != nil {
		interval = normalizeUpstreamSourceAutoSyncInterval(rule.AutoSync.IntervalMinutes)
	}
	if !upstreamSourceRuleAutoSyncEnabled(config, rule) {
		return 0
	}
	return interval
}

func normalizeUpstreamSourceAutoSyncInterval(intervalMinutes int) int {
	if intervalMinutes < 0 {
		return 0
	}
	return intervalMinutes
}

func normalizeUpstreamSourceAutoPriorityInterval(intervalMinutes int) int {
	switch {
	case intervalMinutes < 0:
		return upstreamSourceAutoPriorityDefaultIntervalMinutes
	case intervalMinutes == 0:
		return 0
	default:
		return intervalMinutes
	}
}

func normalizeUpstreamSourceAutoPriorityWindow(windowHours int) int {
	if windowHours <= 0 {
		return upstreamSourceAutoPriorityDefaultWindowHours
	}
	if windowHours > upstreamSourceAutoPriorityMaxWindowHours {
		return upstreamSourceAutoPriorityMaxWindowHours
	}
	return windowHours
}
