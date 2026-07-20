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
	LocalGroup                          string                             `json:"local_group"`
	ChannelType                         int                                `json:"channel_type"`
	DefaultPriority                     int64                              `json:"default_priority"`
	DefaultWeight                       uint                               `json:"default_weight"`
	EnableMonitor                       bool                               `json:"enable_monitor"`
	MonitorIntervalMinutes              int                                `json:"monitor_interval_minutes"`
	AutoSyncModels                      *bool                              `json:"auto_sync_models"`
	ModelStrategy                       string                             `json:"model_strategy"`
	FixedModels                         []string                           `json:"fixed_models"`
	AllowPrivateIP                      common.FlexibleBool                `json:"allow_private_ip"`
	AutoSyncEnabled                     bool                               `json:"auto_sync_enabled"`
	AutoSyncIntervalMinutes             int                                `json:"auto_sync_interval_minutes"`
	AutoPriorityEnabled                 bool                               `json:"auto_priority_enabled"`
	AutoPriorityIntervalMinutes         int                                `json:"auto_priority_interval_minutes"`
	AutoPriorityWindowHours             int                                `json:"auto_priority_window_hours"`
	AutoPriorityAvailabilityWindowHours int                                `json:"auto_priority_availability_window_hours"`
	CodexImageGenerationBridgePolicy    string                             `json:"codex_image_generation_bridge_policy"`
	DefaultLocalGroup                   string                             `json:"default_local_group"`
	LocalGroupRules                     []dto.UpstreamSourceLocalGroupRule `json:"local_group_rules"`
	// SyncConfigVersion marks whether this blob has already gone through
	// migrateLegacyUpstreamSourceConfig. 0 (absent/legacy) blobs get their
	// source-level defaults folded into local_group_rules on read; version 1
	// blobs are trusted as-is so the migration runs at most once.
	SyncConfigVersion int `json:"sync_config_version,omitempty"`
}

type upstreamSourceRuleResolution struct {
	Matched                             bool
	SyncEligible                        bool
	RuleName                            string
	Reason                              string
	LocalGroup                          string
	ChannelType                         int
	Priority                            int64
	Weight                              uint
	MonitorEnabled                      bool
	MonitorIntervalMinutes              int
	MonitorModel                        string
	AutoSyncEnabled                     bool
	AutoSyncIntervalMinutes             int
	AutoPriorityEnabled                 bool
	AutoPriorityIntervalMinutes         int
	AutoPriorityWindowHours             int
	AutoPriorityAvailabilityWindowHours int
	CodexImageGenerationBridgePolicy    string
	ModelStrategy                       string
	FixedModels                         []string
}

func parseUpstreamSourceSyncConfig(raw string) (upstreamSourceSyncConfig, error) {
	config := upstreamSourceSyncConfig{
		LocalGroup:                          "default",
		ChannelType:                         constant.ChannelTypeOpenAI,
		AutoSyncModels:                      common.GetPointer(true),
		DefaultPriority:                     0,
		DefaultWeight:                       0,
		ModelStrategy:                       upstreamSourceModelStrategyAllUpstream,
		AutoPriorityIntervalMinutes:         upstreamSourceAutoPriorityDefaultIntervalMinutes,
		AutoPriorityWindowHours:             upstreamSourceAutoPriorityDefaultWindowHours,
		AutoPriorityAvailabilityWindowHours: upstreamSourceAutoPriorityDefaultWindowHours,
	}
	if strings.TrimSpace(raw) != "" {
		if err := common.Unmarshal([]byte(raw), &config); err != nil {
			return config, err
		}
	}
	config = migrateLegacyUpstreamSourceConfig(config)
	return normalizeUpstreamSourceSyncConfig(config), nil
}

// migrateLegacyUpstreamSourceConfig folds legacy source-level defaults into
// per-group rules for blobs written before the fold-down (version 0),
// including a wholly empty/never-configured blob. Each existing rule
// inherits the source-level channel_type/priority/weight and any unset
// monitor/auto-sync/auto-priority/model settings; a source with no rules
// gets a single catch-all rule (empty matchers = match all), preserving the
// legacy "no rules = sync everything with these defaults" behavior. Version
// is bumped to 1 so the migration runs at most once per stored blob.
func migrateLegacyUpstreamSourceConfig(config upstreamSourceSyncConfig) upstreamSourceSyncConfig {
	if config.SyncConfigVersion >= 1 {
		return config
	}
	base := legacySourceRuleFromConfig(config)
	if len(config.LocalGroupRules) == 0 {
		config.LocalGroupRules = []dto.UpstreamSourceLocalGroupRule{base}
	} else {
		for i := range config.LocalGroupRules {
			config.LocalGroupRules[i] = backfillLegacyRule(config.LocalGroupRules[i], base)
		}
	}
	config.SyncConfigVersion = 1
	return config
}

// legacySourceRuleFromConfig builds a match-all rule carrying every source-level
// default so pre-fold behavior is preserved exactly.
func legacySourceRuleFromConfig(config upstreamSourceSyncConfig) dto.UpstreamSourceLocalGroupRule {
	priority := config.DefaultPriority
	weight := config.DefaultWeight
	monitorEnabled := config.EnableMonitor
	autoSyncEnabled := config.AutoSyncEnabled
	autoPriorityEnabled := config.AutoPriorityEnabled
	autoPriorityWindow := config.AutoPriorityWindowHours
	autoPriorityAvailabilityWindow := config.AutoPriorityAvailabilityWindowHours
	localGroup := strings.TrimSpace(config.DefaultLocalGroup)
	if localGroup == "" {
		localGroup = strings.TrimSpace(config.LocalGroup)
	}
	modelStrategy := normalizeUpstreamSourceFallbackModelStrategy(config.ModelStrategy, config.AutoSyncModels)
	return dto.UpstreamSourceLocalGroupRule{
		Name:                             "migrated",
		LocalGroup:                       localGroup,
		ChannelType:                      config.ChannelType,
		Priority:                         &priority,
		Weight:                           &weight,
		Monitor:                          &dto.UpstreamSourceRuleMonitor{Enabled: &monitorEnabled, IntervalMinutes: config.MonitorIntervalMinutes},
		AutoSync:                         &dto.UpstreamSourceRuleAutoSync{Enabled: &autoSyncEnabled, IntervalMinutes: config.AutoSyncIntervalMinutes},
		AutoPriority:                     &dto.UpstreamSourceRuleAutoPriority{Enabled: &autoPriorityEnabled, WindowHours: &autoPriorityWindow, AvailabilityWindowHours: &autoPriorityAvailabilityWindow},
		CodexImageGenerationBridgePolicy: config.CodexImageGenerationBridgePolicy,
		ModelStrategy:                    modelStrategy,
		FixedModels:                      config.FixedModels,
	}
}

// backfillLegacyRule fills only the fields a legacy rule left unset with the
// source-level base (which previously fed the fallback), preserving behavior.
func backfillLegacyRule(rule dto.UpstreamSourceLocalGroupRule, base dto.UpstreamSourceLocalGroupRule) dto.UpstreamSourceLocalGroupRule {
	if rule.ChannelType == 0 {
		rule.ChannelType = base.ChannelType
	}
	if rule.Priority == nil {
		rule.Priority = base.Priority
	}
	if rule.Weight == nil {
		rule.Weight = base.Weight
	}
	if rule.Monitor == nil {
		rule.Monitor = base.Monitor
	}
	if rule.AutoSync == nil {
		rule.AutoSync = base.AutoSync
	}
	if rule.AutoPriority == nil {
		rule.AutoPriority = base.AutoPriority
	}
	if strings.TrimSpace(rule.CodexImageGenerationBridgePolicy) == "" {
		rule.CodexImageGenerationBridgePolicy = base.CodexImageGenerationBridgePolicy
	}
	if strings.TrimSpace(rule.ModelStrategy) == "" {
		rule.ModelStrategy = base.ModelStrategy
		rule.FixedModels = base.FixedModels
	}
	if strings.TrimSpace(rule.LocalGroup) == "" {
		rule.LocalGroup = base.LocalGroup
	}
	return rule
}

// MigrateAndNormalizeUpstreamSourceSyncConfigRaw parses raw sync_config JSON
// (running the legacy-defaults-to-rules migration and normalization) and
// re-marshals the result back to a JSON string. The controller uses this so
// a create/update persists an already-migrated (version 1) blob that carries
// the folded rules, instead of leaving that to happen only at the next
// runtime sync read.
func MigrateAndNormalizeUpstreamSourceSyncConfigRaw(raw string) (string, error) {
	config, err := parseUpstreamSourceSyncConfig(raw)
	if err != nil {
		return "", err
	}
	data, err := common.Marshal(config)
	if err != nil {
		return "", err
	}
	return string(data), nil
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
	if config.AutoSyncEnabled {
		if config.AutoSyncIntervalMinutes < 0 {
			config.AutoSyncIntervalMinutes = 0
		}
	} else {
		config.AutoSyncIntervalMinutes = 0
	}
	config.AutoPriorityIntervalMinutes = normalizeUpstreamSourceAutoPriorityInterval(config.AutoPriorityIntervalMinutes)
	config.AutoPriorityWindowHours = normalizeUpstreamSourceAutoPriorityWindow(config.AutoPriorityWindowHours)
	config.AutoPriorityAvailabilityWindowHours = normalizeUpstreamSourceAutoPriorityWindow(config.AutoPriorityAvailabilityWindowHours)
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
	if autoPriority.WindowHours != nil {
		value := normalizeUpstreamSourceAutoPriorityWindow(*autoPriority.WindowHours)
		normalized.WindowHours = &value
	}
	if autoPriority.AvailabilityWindowHours != nil {
		value := normalizeUpstreamSourceAutoPriorityWindow(*autoPriority.AvailabilityWindowHours)
		normalized.AvailabilityWindowHours = &value
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
	if intervalMinutes < 0 {
		return 0
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
	resolution, _ := resolveUpstreamSourceRuleWithIndex(config, mapping)
	return resolution
}

func resolveUpstreamSourceRuleWithIndex(config upstreamSourceSyncConfig, mapping *model.UpstreamSourceChannelMapping) (upstreamSourceRuleResolution, int) {
	fallback := upstreamSourceRuleFallbackResolution(config)
	if mapping == nil {
		return fallback, -1
	}
	discoveryStatus := strings.TrimSpace(mapping.DiscoveryStatus)
	if discoveryStatus != "" && discoveryStatus != model.UpstreamMappingDiscoveryStatusActive {
		fallback.Reason = upstreamSourceMatchReasonInactiveDiscovery
		return fallback, -1
	}
	if !mapping.SyncEnabled {
		fallback.Reason = upstreamSourceMatchReasonManualDisabled
		return fallback, -1
	}

	name := strings.ToLower(strings.TrimSpace(mapping.UpstreamGroupName))
	description := strings.ToLower(strings.TrimSpace(mapping.UpstreamGroupDescription))
	platform := strings.ToLower(strings.TrimSpace(mapping.UpstreamPlatform))
	excluded := false
	for index, rule := range config.LocalGroupRules {
		matched, ruleExcluded := upstreamSourceRuleMatches(rule, platform, name, description)
		if ruleExcluded {
			excluded = true
			continue
		}
		if matched {
			return resolveUpstreamSourceMatchedRule(config, rule), index
		}
	}
	if excluded {
		fallback.Reason = upstreamSourceMatchReasonExcludedByKeyword
		return fallback, -1
	}
	return fallback, -1
}

// upstreamSourceRuleFallbackResolution is the resolution used when no rule
// applies: no rules configured at all ("no rules" = sync nothing), no rule
// matched the mapping, the mapping is manually disabled, or its discovery is
// inactive. It intentionally does NOT read source-level defaults (channel
// type, priority, weight, monitor, auto-sync, ...) — those are legacy inputs
// consumed only by migrateLegacyUpstreamSourceConfig. A hardcoded, neutral
// baseline keeps "no matching rule" from silently reviving source-level
// defaults for configs that are supposed to have none.
func upstreamSourceRuleFallbackResolution(config upstreamSourceSyncConfig) upstreamSourceRuleResolution {
	return upstreamSourceRuleResolution{
		Reason:                              upstreamSourceMatchReasonNoMatchingRule,
		LocalGroup:                          upstreamSourceDefaultLocalGroup(config),
		ChannelType:                         constant.ChannelTypeOpenAI,
		Priority:                            0,
		Weight:                              1,
		MonitorEnabled:                      false,
		MonitorIntervalMinutes:              0,
		AutoSyncEnabled:                     false,
		AutoSyncIntervalMinutes:             0,
		AutoPriorityEnabled:                 false,
		AutoPriorityIntervalMinutes:         upstreamSourceAutoPriorityDefaultIntervalMinutes,
		AutoPriorityWindowHours:             upstreamSourceAutoPriorityDefaultWindowHours,
		AutoPriorityAvailabilityWindowHours: upstreamSourceAutoPriorityDefaultWindowHours,
		CodexImageGenerationBridgePolicy:    dto.CodexImageGenerationBridgePolicyFollow,
		ModelStrategy:                       upstreamSourceModelStrategyAllUpstream,
		FixedModels:                         nil,
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
	// A rule's platform filter EXCLUDES a group only when the group has a KNOWN
	// platform that is not in the rule's list. A group whose platform could not be
	// inferred (empty — common for billing-tier groups whose name/description carry
	// no OpenAI/Claude signal, e.g. "对接倍率") is NOT excluded here; it falls
	// through to keyword matching so a platform+keyword rule can still match it by
	// name/description instead of silently matching nothing.
	platformKnown := normalizeUpstreamSourceRulePlatform(platform) != ""
	if len(rule.Platforms) > 0 && platformKnown && !upstreamSourceRulePlatformMatches(platform, rule.Platforms) {
		return false, false
	}
	if upstreamSourceRuleKeywordsMatchAnyText([]string{name, description}, rule.ExcludeKeywords) {
		return false, true
	}
	if len(rule.Platforms) == 0 && len(rule.NameContains) == 0 && len(rule.DescriptionContains) == 0 {
		return true, false // no matchers => match every group (catch-all)
	}

	if len(rule.NameContains) == 0 && len(rule.DescriptionContains) == 0 {
		// Platform-only rule: require a positive (known) platform match. An
		// unknown platform is not enough to match a platform-only rule.
		return upstreamSourceRulePlatformMatches(platform, rule.Platforms), false
	}
	return upstreamSourceKeywordsMatch(name, rule.NameContains) ||
		upstreamSourceKeywordsMatch(description, rule.DescriptionContains), false
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
		if rule.AutoPriority.WindowHours != nil {
			resolution.AutoPriorityWindowHours = normalizeUpstreamSourceAutoPriorityWindow(*rule.AutoPriority.WindowHours)
		}
		if rule.AutoPriority.AvailabilityWindowHours != nil {
			resolution.AutoPriorityAvailabilityWindowHours = normalizeUpstreamSourceAutoPriorityWindow(*rule.AutoPriority.AvailabilityWindowHours)
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
