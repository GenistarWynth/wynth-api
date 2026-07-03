package dto

type UpstreamSourceCreateRequest struct {
	Name                             string                         `json:"name" binding:"required"`
	Type                             string                         `json:"type" binding:"required"`
	BaseURL                          string                         `json:"base_url" binding:"required"`
	AdminAPIBasePath                 string                         `json:"admin_api_base_path"`
	RelayBaseURL                     string                         `json:"relay_base_url"`
	Email                            string                         `json:"email"`
	Password                         string                         `json:"password"`
	LocalGroup                       string                         `json:"local_group"`
	ChannelType                      int                            `json:"channel_type"`
	DefaultPriority                  int64                          `json:"default_priority"`
	DefaultWeight                    uint                           `json:"default_weight"`
	EnableMonitor                    bool                           `json:"enable_monitor"`
	MonitorIntervalMinutes           int                            `json:"monitor_interval_minutes"`
	AutoSyncModels                   bool                           `json:"auto_sync_models"`
	ModelStrategy                    string                         `json:"model_strategy"`
	FixedModels                      []string                       `json:"fixed_models"`
	AllowPrivateIP                   bool                           `json:"allow_private_ip"`
	AutoSyncEnabled                  bool                           `json:"auto_sync_enabled"`
	AutoSyncIntervalMinutes          int                            `json:"auto_sync_interval_minutes"`
	AutoPriorityEnabled              bool                           `json:"auto_priority_enabled"`
	AutoPriorityIntervalMinutes      *int                           `json:"auto_priority_interval_minutes,omitempty"`
	AutoPriorityWindowHours          *int                           `json:"auto_priority_window_hours,omitempty"`
	CodexImageGenerationBridgePolicy string                         `json:"codex_image_generation_bridge_policy"`
	DefaultLocalGroup                string                         `json:"default_local_group"`
	LocalGroupRules                  []UpstreamSourceLocalGroupRule `json:"local_group_rules"`
}

type UpstreamSourceUpdateRequest struct {
	Name                             string                         `json:"name"`
	Status                           string                         `json:"status"`
	BaseURL                          string                         `json:"base_url"`
	AdminAPIBasePath                 string                         `json:"admin_api_base_path"`
	RelayBaseURL                     string                         `json:"relay_base_url"`
	LocalGroup                       string                         `json:"local_group"`
	ChannelType                      int                            `json:"channel_type"`
	DefaultPriority                  int64                          `json:"default_priority"`
	DefaultWeight                    uint                           `json:"default_weight"`
	EnableMonitor                    bool                           `json:"enable_monitor"`
	MonitorIntervalMinutes           int                            `json:"monitor_interval_minutes"`
	AutoSyncModels                   bool                           `json:"auto_sync_models"`
	ModelStrategy                    string                         `json:"model_strategy"`
	FixedModels                      []string                       `json:"fixed_models"`
	AllowPrivateIP                   bool                           `json:"allow_private_ip"`
	AutoSyncEnabled                  bool                           `json:"auto_sync_enabled"`
	AutoSyncIntervalMinutes          int                            `json:"auto_sync_interval_minutes"`
	AutoPriorityEnabled              bool                           `json:"auto_priority_enabled"`
	AutoPriorityIntervalMinutes      *int                           `json:"auto_priority_interval_minutes,omitempty"`
	AutoPriorityWindowHours          *int                           `json:"auto_priority_window_hours,omitempty"`
	CodexImageGenerationBridgePolicy string                         `json:"codex_image_generation_bridge_policy"`
	DefaultLocalGroup                string                         `json:"default_local_group"`
	LocalGroupRules                  []UpstreamSourceLocalGroupRule `json:"local_group_rules"`
}

type UpstreamSourceCredentialsUpdateRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// UpstreamSourceSessionImportRequest carries an admin-pasted upstream session
// so login can be short-circuited when Cloudflare Turnstile blocks automated
// login. new-api sources accept a raw session cookie string OR an
// access_token + user_id pair; sub2api sources accept an access_token (JWT).
type UpstreamSourceSessionImportRequest struct {
	SessionCookie string `json:"session_cookie"`
	AccessToken   string `json:"access_token"`
	UserID        int    `json:"user_id"`
	RefreshToken  string `json:"refresh_token"`
	ExpiresAt     int64  `json:"expires_at"`
}

type UpstreamSourceResponse struct {
	Id                               int                            `json:"id"`
	Name                             string                         `json:"name"`
	Type                             string                         `json:"type"`
	Status                           string                         `json:"status"`
	BaseURL                          string                         `json:"base_url"`
	AdminAPIBasePath                 string                         `json:"admin_api_base_path"`
	RelayBaseURL                     string                         `json:"relay_base_url"`
	LocalGroup                       string                         `json:"local_group"`
	ChannelType                      int                            `json:"channel_type"`
	DefaultPriority                  int64                          `json:"default_priority"`
	DefaultWeight                    uint                           `json:"default_weight"`
	EnableMonitor                    bool                           `json:"enable_monitor"`
	MonitorIntervalMinutes           int                            `json:"monitor_interval_minutes"`
	AutoSyncModels                   bool                           `json:"auto_sync_models"`
	ModelStrategy                    string                         `json:"model_strategy"`
	FixedModels                      []string                       `json:"fixed_models"`
	AllowPrivateIP                   bool                           `json:"allow_private_ip"`
	AutoSyncEnabled                  bool                           `json:"auto_sync_enabled"`
	AutoSyncIntervalMinutes          int                            `json:"auto_sync_interval_minutes"`
	AutoPriorityEnabled              bool                           `json:"auto_priority_enabled"`
	AutoPriorityIntervalMinutes      int                            `json:"auto_priority_interval_minutes"`
	AutoPriorityWindowHours          int                            `json:"auto_priority_window_hours"`
	CodexImageGenerationBridgePolicy string                         `json:"codex_image_generation_bridge_policy"`
	DefaultLocalGroup                string                         `json:"default_local_group"`
	LocalGroupRules                  []UpstreamSourceLocalGroupRule `json:"local_group_rules"`
	MaskedEmail                      string                         `json:"masked_email"`
	HasCredentials                   bool                           `json:"has_credentials"`
	SessionSource                    string                         `json:"session_source"`
	TurnstileBlocked                 bool                           `json:"turnstile_blocked"`
	LastDiscoveryTime                int64                          `json:"last_discovery_time"`
	LastDiscoveryStatus              string                         `json:"last_discovery_status"`
	LastDiscoveryError               string                         `json:"last_discovery_error"`
	LastSyncTime                     int64                          `json:"last_sync_time"`
	LastSyncStatus                   string                         `json:"last_sync_status"`
	LastSyncError                    string                         `json:"last_sync_error"`
	CreatedTime                      int64                          `json:"created_time"`
	UpdatedTime                      int64                          `json:"updated_time"`
}

type UpstreamSourceLocalGroupRule struct {
	Name                             string                          `json:"name"`
	LocalGroup                       string                          `json:"local_group"`
	ChannelType                      int                             `json:"channel_type,omitempty"`
	Priority                         *int64                          `json:"priority,omitempty"`
	Weight                           *uint                           `json:"weight,omitempty"`
	Platforms                        []string                        `json:"platforms"`
	NameContains                     []string                        `json:"name_contains"`
	DescriptionContains              []string                        `json:"description_contains"`
	ExcludeKeywords                  []string                        `json:"exclude_keywords"`
	Monitor                          *UpstreamSourceRuleMonitor      `json:"monitor,omitempty"`
	AutoSync                         *UpstreamSourceRuleAutoSync     `json:"auto_sync,omitempty"`
	AutoPriority                     *UpstreamSourceRuleAutoPriority `json:"auto_priority,omitempty"`
	CodexImageGenerationBridgePolicy string                          `json:"codex_image_generation_bridge_policy,omitempty"`
	ModelStrategy                    string                          `json:"model_strategy"`
	FixedModels                      []string                        `json:"fixed_models"`
}

type UpstreamSourceRuleMonitor struct {
	Enabled         *bool `json:"enabled,omitempty"`
	IntervalMinutes int   `json:"interval_minutes,omitempty"`
}

type UpstreamSourceRuleAutoSync struct {
	Enabled         *bool `json:"enabled,omitempty"`
	IntervalMinutes int   `json:"interval_minutes,omitempty"`
}

type UpstreamSourceRuleAutoPriority struct {
	Enabled         *bool `json:"enabled,omitempty"`
	IntervalMinutes *int  `json:"interval_minutes,omitempty"`
	WindowHours     *int  `json:"window_hours,omitempty"`
}

type UpstreamSourceMappingResponse struct {
	Id                                       int      `json:"id"`
	SourceID                                 int      `json:"source_id"`
	SyncEnabled                              bool     `json:"sync_enabled"`
	UpstreamGroupID                          string   `json:"upstream_group_id"`
	UpstreamGroupName                        string   `json:"upstream_group_name"`
	UpstreamGroupDescription                 string   `json:"upstream_group_description"`
	UpstreamPlatform                         string   `json:"upstream_platform"`
	DiscoveryStatus                          string   `json:"discovery_status"`
	UpstreamStatus                           string   `json:"upstream_status"`
	UpstreamRateMultiplier                   *float64 `json:"upstream_rate_multiplier"`
	EffectiveRateMultiplier                  *float64 `json:"effective_rate_multiplier"`
	UpstreamKeyID                            string   `json:"upstream_key_id"`
	HasUpstreamKey                           bool     `json:"has_upstream_key"`
	LocalChannelID                           int      `json:"local_channel_id"`
	SyncStatus                               string   `json:"sync_status"`
	LastError                                string   `json:"last_error"`
	LastDiscoveredAt                         int64    `json:"last_discovered_at"`
	LastSyncedAt                             int64    `json:"last_synced_at"`
	SyncEligible                             bool     `json:"sync_eligible"`
	MatchedRuleName                          string   `json:"matched_rule_name"`
	MatchReason                              string   `json:"match_reason"`
	ResolvedLocalGroup                       string   `json:"resolved_local_group"`
	ResolvedMonitorEnabled                   bool     `json:"resolved_monitor_enabled"`
	ResolvedMonitorIntervalMinutes           int      `json:"resolved_monitor_interval_minutes"`
	ResolvedAutoSyncEnabled                  bool     `json:"resolved_auto_sync_enabled"`
	ResolvedAutoSyncIntervalMinutes          int      `json:"resolved_auto_sync_interval_minutes"`
	ResolvedAutoPriorityEnabled              bool     `json:"resolved_auto_priority_enabled"`
	ResolvedAutoPriorityIntervalMinutes      int      `json:"resolved_auto_priority_interval_minutes"`
	ResolvedAutoPriorityWindowHours          int      `json:"resolved_auto_priority_window_hours"`
	ResolvedCodexImageGenerationBridgePolicy string   `json:"resolved_codex_image_generation_bridge_policy"`
	ResolvedModelStrategy                    string   `json:"resolved_model_strategy"`
	ResolvedFixedModels                      []string `json:"resolved_fixed_models"`
	ResolvedChannelType                      int      `json:"resolved_channel_type"`
	ResolvedPriority                         int64    `json:"resolved_priority"`
	ResolvedWeight                           uint     `json:"resolved_weight"`
}

type UpstreamSourceMappingUpdateRequest struct {
	MappingIDs []int `json:"mapping_ids"`
}

type UpstreamSourceMappingSyncUpdateRequest struct {
	MappingID   int  `json:"mapping_id" binding:"required"`
	SyncEnabled bool `json:"sync_enabled"`
}

type UpstreamSourceDiscoveryResult struct {
	SourceID   int                             `json:"source_id"`
	Discovered int                             `json:"discovered"`
	Active     int                             `json:"active"`
	Stale      int                             `json:"stale"`
	Invalid    int                             `json:"invalid"`
	Mappings   []UpstreamSourceMappingResponse `json:"mappings"`
	Error      string                          `json:"error,omitempty"`
}

type UpstreamSourceMappingSyncResult struct {
	MappingID       int    `json:"mapping_id"`
	UpstreamGroupID string `json:"upstream_group_id"`
	LocalChannelID  int    `json:"local_channel_id"`
	Status          string `json:"status"`
	Error           string `json:"error,omitempty"`
	Created         bool   `json:"created"`
	Updated         bool   `json:"updated"`
}

type UpstreamSourceSyncResult struct {
	SourceID int                               `json:"source_id"`
	Status   string                            `json:"status"`
	Created  int                               `json:"created"`
	Updated  int                               `json:"updated"`
	Skipped  int                               `json:"skipped"`
	Failed   int                               `json:"failed"`
	Results  []UpstreamSourceMappingSyncResult `json:"results"`
	Error    string                            `json:"error,omitempty"`
}

type UpstreamSourceAutoPriorityChannelResult struct {
	MappingID               int     `json:"mapping_id"`
	LocalChannelID          int     `json:"local_channel_id"`
	OldPriority             int64   `json:"old_priority"`
	NewPriority             int64   `json:"new_priority"`
	ComputedPriority        int64   `json:"computed_priority"`
	Applied                 bool    `json:"applied"`
	Reason                  string  `json:"reason,omitempty"`
	EffectiveRateMultiplier float64 `json:"effective_rate_multiplier"`
	CacheAdjustedCostFactor float64 `json:"cache_adjusted_cost_factor"`
	EffectiveCostMultiplier float64 `json:"effective_cost_multiplier"`
	EffectivePriceScore     float64 `json:"effective_price_score"`
	AvailabilityScore       float64 `json:"availability_score"`
	FirstTokenScore         float64 `json:"first_token_score"`
	FinalScore              float64 `json:"final_score"`
}

type UpstreamSourceAutoPriorityResult struct {
	SourceID int                                       `json:"source_id"`
	Updated  int                                       `json:"updated"`
	Skipped  int                                       `json:"skipped"`
	Failed   int                                       `json:"failed"`
	Results  []UpstreamSourceAutoPriorityChannelResult `json:"results"`
	Error    string                                    `json:"error,omitempty"`
}
