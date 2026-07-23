package dto

import (
	"fmt"
	"net/url"
	"strings"
)

type ChannelSettings struct {
	ForceFormat            bool   `json:"force_format,omitempty"`
	ThinkingToContent      bool   `json:"thinking_to_content,omitempty"`
	Proxy                  string `json:"proxy"`
	PassThroughBodyEnabled bool   `json:"pass_through_body_enabled,omitempty"`
	SystemPrompt           string `json:"system_prompt,omitempty"`
	SystemPromptOverride   bool   `json:"system_prompt_override,omitempty"`
}

type VertexKeyType string

const (
	VertexKeyTypeJSON   VertexKeyType = "json"
	VertexKeyTypeAPIKey VertexKeyType = "api_key"
)

type AwsKeyType string

const (
	AwsKeyTypeAKSK   AwsKeyType = "ak_sk" // 默认
	AwsKeyTypeApiKey AwsKeyType = "api_key"
)

type ChannelOtherSettings struct {
	AzureResponsesVersion                      string                    `json:"azure_responses_version,omitempty"`
	ClientIdentityPreset                       string                    `json:"client_identity_preset,omitempty"`
	VertexKeyType                              VertexKeyType             `json:"vertex_key_type,omitempty"` // "json" or "api_key"
	OpenRouterEnterprise                       *bool                     `json:"openrouter_enterprise,omitempty"`
	ClaudeBetaQuery                            bool                      `json:"claude_beta_query,omitempty"`          // Claude 渠道是否强制追加 ?beta=true
	AllowServiceTier                           bool                      `json:"allow_service_tier,omitempty"`         // 是否允许 service_tier 透传（默认过滤以避免额外计费）
	AllowInferenceGeo                          bool                      `json:"allow_inference_geo,omitempty"`        // 是否允许 inference_geo 透传（仅 Claude，默认过滤以满足数据驻留合规
	AllowSpeed                                 bool                      `json:"allow_speed,omitempty"`                // 是否允许 speed 透传（仅 Claude，默认过滤以避免意外切换推理速度模式）
	AllowSafetyIdentifier                      bool                      `json:"allow_safety_identifier,omitempty"`    // 是否允许 safety_identifier 透传（默认过滤以保护用户隐私）
	DisableStore                               bool                      `json:"disable_store,omitempty"`              // 是否禁用 store 透传（默认允许透传，禁用后可能导致 Codex 无法使用）
	AllowIncludeObfuscation                    bool                      `json:"allow_include_obfuscation,omitempty"`  // 是否允许 stream_options.include_obfuscation 透传（默认过滤以避免关闭流混淆保护）
	DisableTaskPollingSleep                    bool                      `json:"disable_task_polling_sleep,omitempty"` // 是否跳过异步任务轮询间隔（同步自 upstream）
	AwsKeyType                                 AwsKeyType                `json:"aws_key_type,omitempty"`
	UpstreamModelUpdateCheckEnabled            bool                      `json:"upstream_model_update_check_enabled,omitempty"`        // 是否检测上游模型更新
	UpstreamModelUpdateAutoSyncEnabled         bool                      `json:"upstream_model_update_auto_sync_enabled,omitempty"`    // 是否自动同步上游模型更新
	UpstreamModelUpdateLastCheckTime           int64                     `json:"upstream_model_update_last_check_time,omitempty"`      // 上次检测时间
	UpstreamModelUpdateLastDetectedModels      []string                  `json:"upstream_model_update_last_detected_models,omitempty"` // 上次检测到的可加入模型
	UpstreamModelUpdateLastRemovedModels       []string                  `json:"upstream_model_update_last_removed_models,omitempty"`  // 上次检测到的可删除模型
	UpstreamModelUpdateIgnoredModels           []string                  `json:"upstream_model_update_ignored_models,omitempty"`       // 手动忽略的模型
	ChannelMonitorEnabled                      bool                      `json:"channel_monitor_enabled,omitempty"`
	ChannelMonitorIntervalMinutes              int                       `json:"channel_monitor_interval_minutes,omitempty"`
	ChannelMonitorModel                        string                    `json:"channel_monitor_model,omitempty"`
	ChannelDeadRecoveryEnabled                 bool                      `json:"channel_dead_recovery_enabled,omitempty"`
	ChannelDeadRecoveryMinMinutes              int                       `json:"channel_dead_recovery_min_minutes,omitempty"`
	ChannelDeadRecoveryMaxMinutes              int                       `json:"channel_dead_recovery_max_minutes,omitempty"`
	ChannelAutoPriorityEnabled                 bool                      `json:"channel_auto_priority_enabled,omitempty"`
	ChannelAutoPriorityIntervalMinutes         int                       `json:"channel_auto_priority_interval_minutes,omitempty"`
	ChannelAutoPriorityWindowHours             int                       `json:"channel_auto_priority_window_hours,omitempty"`
	ChannelAutoPriorityAvailabilityWindowHours int                       `json:"channel_auto_priority_availability_window_hours,omitempty"`
	ChannelAutoPriorityRateMultiplier          float64                   `json:"channel_auto_priority_rate_multiplier,omitempty"`
	ChannelAutoPriorityLastRunAt               int64                     `json:"channel_auto_priority_last_run_at,omitempty"`
	ChannelAutoPriorityLastAppliedAt           int64                     `json:"channel_auto_priority_last_applied_at,omitempty"`
	ChannelAutoPriorityLastScore               *ChannelAutoPriorityScore `json:"channel_auto_priority_last_score,omitempty"`
	GeneratedByUpstreamSourceID                int                       `json:"generated_by_upstream_source_id,omitempty"`
	GeneratedByUpstreamMappingID               int                       `json:"generated_by_upstream_mapping_id,omitempty"`
	CodexImageGenerationBridgePolicy           string                    `json:"codex_image_generation_bridge_policy,omitempty"`
	AutoRetryTimes                             *int                      `json:"auto_retry_times,omitempty"`
	ChannelRetryStatusCodes                    string                    `json:"channel_retry_status_codes,omitempty"`
	ChannelAutoDisableStatusCodes              string                    `json:"channel_auto_disable_status_codes,omitempty"`
	ChannelFailureKeywords                     string                    `json:"channel_failure_keywords,omitempty"`
	AdvancedCustom                             *AdvancedCustomConfig     `json:"advanced_custom,omitempty"`
}

const (
	ClientIdentityPresetOff        = "off"
	ClientIdentityPresetCodexCLI   = "codex_cli"
	ClientIdentityPresetClaudeCode = "claude_code"
)

func NormalizeClientIdentityPreset(preset string) string {
	switch strings.TrimSpace(preset) {
	case ClientIdentityPresetCodexCLI:
		return ClientIdentityPresetCodexCLI
	case ClientIdentityPresetClaudeCode:
		return ClientIdentityPresetClaudeCode
	default:
		return ClientIdentityPresetOff
	}
}

const ChannelAutoRetryTimesMax = 10

const (
	ChannelAutoPriorityDefaultIntervalMinutes = 30
	ChannelAutoPriorityDefaultWindowHours     = 24
	ChannelAutoPriorityMaxWindowHours         = 168
)

const (
	CodexImageGenerationBridgePolicyFollow   = "follow"
	CodexImageGenerationBridgePolicyEnabled  = "enabled"
	CodexImageGenerationBridgePolicyDisabled = "disabled"
)

func NormalizeCodexImageGenerationBridgePolicy(policy string) string {
	switch strings.TrimSpace(policy) {
	case CodexImageGenerationBridgePolicyEnabled:
		return CodexImageGenerationBridgePolicyEnabled
	case CodexImageGenerationBridgePolicyDisabled:
		return CodexImageGenerationBridgePolicyDisabled
	default:
		return CodexImageGenerationBridgePolicyFollow
	}
}

func NormalizeChannelAutoPriorityInterval(intervalMinutes int) int {
	switch {
	case intervalMinutes < 0:
		return ChannelAutoPriorityDefaultIntervalMinutes
	case intervalMinutes == 0:
		return 0
	default:
		return intervalMinutes
	}
}

func NormalizeChannelAutoPriorityWindowHours(windowHours int) int {
	if windowHours <= 0 {
		return ChannelAutoPriorityDefaultWindowHours
	}
	if windowHours > ChannelAutoPriorityMaxWindowHours {
		return ChannelAutoPriorityMaxWindowHours
	}
	return windowHours
}

type ChannelAutoPriorityScore struct {
	Version                  string  `json:"version,omitempty"`
	ComputedAt               int64   `json:"computed_at"`
	WindowStart              int64   `json:"window_start"`
	WindowEnd                int64   `json:"window_end"`
	Cohort                   string  `json:"cohort,omitempty"`
	CohortFloor              float64 `json:"cohort_floor,omitempty"`
	CohortCeil               float64 `json:"cohort_ceil,omitempty"`
	CohortMemberCount        int     `json:"cohort_member_count,omitempty"`
	EffectiveRateMultiplier  float64 `json:"effective_rate_multiplier"`
	NominalRateMultiplier    float64 `json:"nominal_rate_multiplier"`
	CacheAdjustedCostFactor  float64 `json:"cache_adjusted_cost_factor"`
	EffectiveCostMultiplier  float64 `json:"effective_cost_multiplier"`
	EffectivePriceScore      float64 `json:"effective_price_score"`
	NominalPriceScore        float64 `json:"nominal_price_score"`
	CacheScore               float64 `json:"cache_score"`
	AvailabilityScore        float64 `json:"availability_score"`
	FirstTokenScore          float64 `json:"first_token_score"`
	ThroughputScore          float64 `json:"throughput_score"`
	FinalScore               float64 `json:"final_score"`
	OldPriority              int64   `json:"old_priority"`
	NewPriority              int64   `json:"new_priority"`
	Applied                  bool    `json:"applied"`
	Reason                   string  `json:"reason,omitempty"`
	UsageLogCount            int64   `json:"usage_log_count"`
	MonitorCheckCount        int64   `json:"monitor_check_count"`
	FirstTokenSampleCount    int64   `json:"first_token_sample_count"`
	ThroughputSampleCount    int64   `json:"throughput_sample_count"`
	CacheFactorSource        string  `json:"cache_factor_source,omitempty"`
	CacheFactorPrior         float64 `json:"cache_factor_prior,omitempty"`
	CacheFactorOwnConfidence float64 `json:"cache_factor_own_confidence"`
}

func (s *ChannelOtherSettings) IsOpenRouterEnterprise() bool {
	if s == nil || s.OpenRouterEnterprise == nil {
		return false
	}
	return *s.OpenRouterEnterprise
}

const (
	AdvancedCustomConverterNone                                         = "none"
	AdvancedCustomConverterAnthropicMessagesToOpenAIChatCompletions     = "anthropic_messages_to_openai_chat_completions"
	AdvancedCustomConverterOpenAIChatCompletionsToAnthropicMessages     = "openai_chat_completions_to_anthropic_messages"
	AdvancedCustomConverterOpenAIChatCompletionsToOpenAIResponses       = "openai_chat_completions_to_openai_responses"
	AdvancedCustomConverterOpenAIResponsesToOpenAIChatCompletions       = "openai_responses_to_openai_chat_completions"
	AdvancedCustomConverterGeminiGenerateContentToOpenAIChatCompletions = "gemini_generate_content_to_openai_chat_completions"
	AdvancedCustomConverterOpenAIChatCompletionsToGeminiGenerateContent = "openai_chat_completions_to_gemini_generate_content"
)

const (
	AdvancedCustomAuthTypeNone   = "none"
	AdvancedCustomAuthTypeHeader = "header"
	AdvancedCustomAuthTypeQuery  = "query"
)

type AdvancedCustomConfig struct {
	Routes []AdvancedCustomRoute `json:"advanced_routes,omitempty"`
}

type AdvancedCustomRoute struct {
	IncomingPath string                   `json:"incoming_path,omitempty"`
	UpstreamPath string                   `json:"upstream_path,omitempty"`
	Converter    string                   `json:"converter,omitempty"`
	Auth         *AdvancedCustomRouteAuth `json:"auth,omitempty"`
}

type AdvancedCustomRouteAuth struct {
	Type  string `json:"type,omitempty"`
	Name  string `json:"name,omitempty"`
	Value string `json:"value,omitempty"`
}

const advancedCustomModelPlaceholder = "{model}"

// MatchPath returns the first route whose IncomingPath matches requestPath.
// Matching mirrors the relay adaptor: exact match, {model} placeholder, and
// :generateContent <-> :streamGenerateContent equivalence.
func (c *AdvancedCustomConfig) MatchPath(requestPath string) (AdvancedCustomRoute, bool) {
	if c == nil {
		return AdvancedCustomRoute{}, false
	}
	for _, route := range c.Routes {
		if matchAdvancedCustomIncomingPath(strings.TrimSpace(route.IncomingPath), requestPath) {
			return route, true
		}
	}
	return AdvancedCustomRoute{}, false
}

// SupportsPath reports whether any route matches requestPath.
func (c *AdvancedCustomConfig) SupportsPath(requestPath string) bool {
	_, ok := c.MatchPath(requestPath)
	return ok
}

func matchAdvancedCustomIncomingPath(configuredPath string, requestPath string) bool {
	if matchAdvancedCustomIncomingPathTemplate(configuredPath, requestPath) {
		return true
	}
	if strings.Contains(configuredPath, ":generateContent") {
		streamPath := strings.Replace(configuredPath, ":generateContent", ":streamGenerateContent", 1)
		return matchAdvancedCustomIncomingPathTemplate(streamPath, requestPath)
	}
	return false
}

func matchAdvancedCustomIncomingPathTemplate(configuredPath string, requestPath string) bool {
	if !strings.Contains(configuredPath, advancedCustomModelPlaceholder) {
		return configuredPath == requestPath
	}

	parts := strings.Split(configuredPath, advancedCustomModelPlaceholder)
	if len(parts) != 2 {
		return false
	}
	if !strings.HasPrefix(requestPath, parts[0]) || !strings.HasSuffix(requestPath, parts[1]) {
		return false
	}

	model := strings.TrimSuffix(strings.TrimPrefix(requestPath, parts[0]), parts[1])
	return model != "" && !strings.Contains(model, "/")
}

func IsAdvancedCustomConverterAllowed(converter string) bool {
	switch converter {
	case AdvancedCustomConverterNone,
		AdvancedCustomConverterAnthropicMessagesToOpenAIChatCompletions,
		AdvancedCustomConverterOpenAIChatCompletionsToAnthropicMessages,
		AdvancedCustomConverterOpenAIChatCompletionsToOpenAIResponses,
		AdvancedCustomConverterOpenAIResponsesToOpenAIChatCompletions,
		AdvancedCustomConverterGeminiGenerateContentToOpenAIChatCompletions,
		AdvancedCustomConverterOpenAIChatCompletionsToGeminiGenerateContent:
		return true
	default:
		return false
	}
}

func (c *AdvancedCustomConfig) Validate() error {
	if c == nil {
		return fmt.Errorf("advanced_custom is required")
	}
	if len(c.Routes) == 0 {
		return fmt.Errorf("advanced_custom requires at least one route")
	}

	seenPaths := make(map[string]struct{}, len(c.Routes))
	for i := range c.Routes {
		route := c.Routes[i]
		route.IncomingPath = strings.TrimSpace(route.IncomingPath)
		upstreamPath := strings.TrimSpace(route.UpstreamPath)
		route.Converter = strings.TrimSpace(route.Converter)
		if route.Converter == "" {
			route.Converter = AdvancedCustomConverterNone
		}

		if route.IncomingPath == "" {
			return fmt.Errorf("advanced_custom.advanced_routes[%d].incoming_path is required", i)
		}
		if !strings.HasPrefix(route.IncomingPath, "/") {
			return fmt.Errorf("advanced_custom.advanced_routes[%d].incoming_path must start with /", i)
		}
		if strings.Contains(route.IncomingPath, "?") {
			return fmt.Errorf("advanced_custom.advanced_routes[%d].incoming_path must not include query", i)
		}
		if _, exists := seenPaths[route.IncomingPath]; exists {
			return fmt.Errorf("advanced_custom.advanced_routes[%d].incoming_path must be unique: %s", i, route.IncomingPath)
		}
		seenPaths[route.IncomingPath] = struct{}{}

		if upstreamPath == "" {
			return fmt.Errorf("advanced_custom.advanced_routes[%d].upstream_path is required", i)
		}
		if err := validateAdvancedCustomUpstreamTarget(i, upstreamPath); err != nil {
			return err
		}

		if !IsAdvancedCustomConverterAllowed(route.Converter) {
			return fmt.Errorf("advanced_custom.advanced_routes[%d].converter is not registered: %s", i, route.Converter)
		}
		if err := validateAdvancedCustomConverterPath(i, route.IncomingPath, route.Converter); err != nil {
			return err
		}
		if err := validateAdvancedCustomRouteAuth(i, route.Auth); err != nil {
			return err
		}
	}

	return nil
}

func validateAdvancedCustomUpstreamTarget(index int, upstreamPath string) error {
	if strings.HasPrefix(upstreamPath, "/") {
		if strings.HasPrefix(upstreamPath, "//") {
			return fmt.Errorf("advanced_custom.advanced_routes[%d].upstream_path must be a full URL or a path starting with /", index)
		}
		return nil
	}

	parsedURL, err := url.Parse(upstreamPath)
	if err != nil || parsedURL.Scheme == "" || parsedURL.Host == "" {
		return fmt.Errorf("advanced_custom.advanced_routes[%d].upstream_path must be a full URL or a path starting with /", index)
	}
	if !strings.EqualFold(parsedURL.Scheme, "http") && !strings.EqualFold(parsedURL.Scheme, "https") {
		return fmt.Errorf("advanced_custom.advanced_routes[%d].upstream_path must use http or https", index)
	}
	return nil
}

func validateAdvancedCustomConverterPath(index int, incomingPath string, converter string) error {
	switch converter {
	case AdvancedCustomConverterNone:
		return nil
	case AdvancedCustomConverterAnthropicMessagesToOpenAIChatCompletions:
		if incomingPath == "/v1/messages" {
			return nil
		}
	case AdvancedCustomConverterOpenAIChatCompletionsToAnthropicMessages,
		AdvancedCustomConverterOpenAIChatCompletionsToOpenAIResponses,
		AdvancedCustomConverterOpenAIChatCompletionsToGeminiGenerateContent:
		if incomingPath == "/v1/chat/completions" {
			return nil
		}
	case AdvancedCustomConverterOpenAIResponsesToOpenAIChatCompletions:
		if incomingPath == "/v1/responses" {
			return nil
		}
	case AdvancedCustomConverterGeminiGenerateContentToOpenAIChatCompletions:
		if strings.Contains(incomingPath, ":generateContent") || strings.Contains(incomingPath, ":streamGenerateContent") {
			return nil
		}
	}
	return fmt.Errorf("advanced_custom.advanced_routes[%d].converter does not match incoming_path: %s", index, converter)
}

func validateAdvancedCustomRouteAuth(index int, auth *AdvancedCustomRouteAuth) error {
	if auth == nil {
		return nil
	}
	authType := strings.TrimSpace(auth.Type)
	switch authType {
	case AdvancedCustomAuthTypeNone:
		return nil
	case AdvancedCustomAuthTypeHeader, AdvancedCustomAuthTypeQuery:
		if strings.TrimSpace(auth.Name) == "" {
			return fmt.Errorf("advanced_custom.advanced_routes[%d].auth.name is required", index)
		}
		if strings.TrimSpace(auth.Value) == "" {
			return fmt.Errorf("advanced_custom.advanced_routes[%d].auth.value is required", index)
		}
		return nil
	default:
		return fmt.Errorf("advanced_custom.advanced_routes[%d].auth.type is invalid: %s", index, auth.Type)
	}
}
