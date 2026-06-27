package dto

type AccountPoolCreateRequest struct {
	Name                           string   `json:"name" binding:"required"`
	Platform                       string   `json:"platform"`
	DefaultProxyID                 int      `json:"default_proxy_id"`
	DefaultMonitorEnabled          bool     `json:"default_monitor_enabled"`
	DefaultSchedulePolicy          string   `json:"default_schedule_policy"`
	CapabilityCheckEnabled         bool     `json:"capability_check_enabled"`
	CapabilityCheckIntervalMinutes int      `json:"capability_check_interval_minutes"`
	CapabilityCheckMode            string   `json:"capability_check_mode"`
	CapabilityCheckChannelID       int      `json:"capability_check_channel_id"`
	CapabilityCheckModels          []string `json:"capability_check_models"`
	CapabilityCheckTimeoutSeconds  int      `json:"capability_check_timeout_seconds"`
	CapabilityCheckMerge           bool     `json:"capability_check_merge"`
	Remark                         string   `json:"remark"`
}

type AccountPoolUpdateRequest = AccountPoolCreateRequest

type AccountPoolResponse struct {
	Id                             int      `json:"id"`
	Name                           string   `json:"name"`
	Platform                       string   `json:"platform"`
	Status                         string   `json:"status"`
	DefaultProxyID                 int      `json:"default_proxy_id"`
	DefaultMonitorEnabled          bool     `json:"default_monitor_enabled"`
	DefaultSchedulePolicy          string   `json:"default_schedule_policy"`
	CapabilityCheckEnabled         bool     `json:"capability_check_enabled"`
	CapabilityCheckIntervalMinutes int      `json:"capability_check_interval_minutes"`
	CapabilityCheckMode            string   `json:"capability_check_mode"`
	CapabilityCheckChannelID       int      `json:"capability_check_channel_id"`
	CapabilityCheckModels          []string `json:"capability_check_models"`
	CapabilityCheckTimeoutSeconds  int      `json:"capability_check_timeout_seconds"`
	CapabilityCheckMerge           bool     `json:"capability_check_merge"`
	Remark                         string   `json:"remark"`
	CreatedTime                    int64    `json:"created_time"`
	UpdatedTime                    int64    `json:"updated_time"`
}

type AccountPoolCredentialConfigRequest struct {
	Type         string `json:"type"`
	APIKey       string `json:"api_key"`
	Email        string `json:"email"`
	RefreshToken string `json:"refresh_token"`
	// OAuthType selects the Gemini OAuth sub-type ("code_assist" or "ai_studio").
	// Only meaningful for Gemini OAuth accounts; ignored otherwise.
	OAuthType string `json:"oauth_type"`
	// ServiceAccountJSON carries the raw GCP service-account JSON for a Gemini
	// Vertex AI service_account credential (SECRET). Only used when Type is
	// "service_account".
	ServiceAccountJSON string `json:"service_account_json"`
	// Location is the Vertex AI region (e.g. us-central1) for a service_account
	// credential. Defaults server-side when empty.
	Location string `json:"location"`
}

type AccountPoolTokenStateRequest struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresAt    int64  `json:"expires_at"`
	Version      int64  `json:"version"`
}

type AccountPoolAccountCreateRequest struct {
	Name                      string                             `json:"name" binding:"required"`
	AccountIdentifier         string                             `json:"account_identifier"`
	Credential                AccountPoolCredentialConfigRequest `json:"credential"`
	TokenState                AccountPoolTokenStateRequest       `json:"token_state"`
	Status                    string                             `json:"status"`
	Priority                  int64                              `json:"priority"`
	Weight                    uint                               `json:"weight"`
	MaxConcurrency            *int                               `json:"max_concurrency"`
	ProxyID                   int                                `json:"proxy_id"`
	SupportedModels           []string                           `json:"supported_models"`
	ModelMapping              map[string]string                  `json:"model_mapping"`
	LastUsedAt                int64                              `json:"last_used_at"`
	RateLimitedUntil          int64                              `json:"rate_limited_until"`
	TempDisabledUntil         int64                              `json:"temp_disabled_until"`
	TempDisabledReason        string                             `json:"temp_disabled_reason"`
	LastError                 string                             `json:"last_error"`
	ExpiresAt                 int64                              `json:"expires_at"`
	AutoPauseOnExpired        bool                               `json:"auto_pause_on_expired"`
	RequestQuota              int64                              `json:"request_quota"`
	RequestQuotaWindowSeconds int64                              `json:"request_quota_window_seconds"`
}

type AccountPoolAccountResponse struct {
	Id                        int               `json:"id"`
	PoolID                    int               `json:"pool_id"`
	Name                      string            `json:"name"`
	AccountIdentifier         string            `json:"account_identifier"`
	OAuthType                 string            `json:"oauth_type"`
	Status                    string            `json:"status"`
	Priority                  int64             `json:"priority"`
	Weight                    uint              `json:"weight"`
	MaxConcurrency            int               `json:"max_concurrency"`
	ProxyID                   int               `json:"proxy_id"`
	SupportedModels           []string          `json:"supported_models"`
	ModelMapping              map[string]string `json:"model_mapping"`
	LastUsedAt                int64             `json:"last_used_at"`
	RateLimitedUntil          int64             `json:"rate_limited_until"`
	TempDisabledUntil         int64             `json:"temp_disabled_until"`
	TempDisabledReason        string            `json:"temp_disabled_reason"`
	LastError                 string            `json:"last_error"`
	ExpiresAt                 int64             `json:"expires_at"`
	AutoPauseOnExpired        bool              `json:"auto_pause_on_expired"`
	LastCapabilityCheckAt     int64             `json:"last_capability_check_at"`
	LastCapabilityCheckStatus string            `json:"last_capability_check_status"`
	LastCapabilityCheckError  string            `json:"last_capability_check_error"`
	LastCapabilityCheckModels []string          `json:"last_capability_check_models"`
	HasCredential             bool              `json:"has_credential"`
	HasToken                  bool              `json:"has_token"`
	RequestQuota              int64             `json:"request_quota"`
	RequestQuotaUsed          int64             `json:"request_quota_used"`
	RequestQuotaWindowStart   int64             `json:"request_quota_window_start"`
	RequestQuotaWindowSeconds int64             `json:"request_quota_window_seconds"`
	CreatedTime               int64             `json:"created_time"`
	UpdatedTime               int64             `json:"updated_time"`
}

type AccountPoolAccountImportDefaultsRequest struct {
	Status          string            `json:"status"`
	Priority        int64             `json:"priority"`
	Weight          uint              `json:"weight"`
	MaxConcurrency  *int              `json:"max_concurrency"`
	ProxyID         int               `json:"proxy_id"`
	SupportedModels []string          `json:"supported_models"`
	ModelMapping    map[string]string `json:"model_mapping"`
}

type AccountPoolAccountImportRequest struct {
	Format   string                                  `json:"format" binding:"required"`
	Content  string                                  `json:"content" binding:"required"`
	Defaults AccountPoolAccountImportDefaultsRequest `json:"defaults"`
	DryRun   bool                                    `json:"dry_run"`
}

type AccountPoolAccountImportError struct {
	Index   int    `json:"index,omitempty"`
	Name    string `json:"name,omitempty"`
	Message string `json:"message"`
}

type AccountPoolAccountImportResponse struct {
	Imported     int                             `json:"imported"`
	Skipped      int                             `json:"skipped"`
	Failed       int                             `json:"failed"`
	ProxyCreated int                             `json:"proxy_created"`
	ProxyReused  int                             `json:"proxy_reused"`
	Accounts     []AccountPoolAccountResponse    `json:"accounts,omitempty"`
	Errors       []AccountPoolAccountImportError `json:"errors,omitempty"`
}

type AccountPoolCapabilityDetectRequest struct {
	Mode            string            `json:"mode"`
	ChannelID       int               `json:"channel_id"`
	AccountIDs      []int             `json:"account_ids"`
	CandidateModels []string          `json:"candidate_models"`
	Apply           bool              `json:"apply"`
	Merge           bool              `json:"merge"`
	ModelMapping    map[string]string `json:"model_mapping"`
	TimeoutSeconds  int               `json:"timeout_seconds"`
}

type AccountPoolCapabilityDetectResult struct {
	AccountID      int               `json:"account_id"`
	Status         string            `json:"status"`
	Mode           string            `json:"mode"`
	DetectedModels []string          `json:"detected_models"`
	AppliedModels  []string          `json:"applied_models"`
	ModelMapping   map[string]string `json:"model_mapping"`
	Errors         []string          `json:"errors"`
}

type AccountPoolCapabilityPoolResult struct {
	Total     int                                 `json:"total"`
	Succeeded int                                 `json:"succeeded"`
	Failed    int                                 `json:"failed"`
	Results   []AccountPoolCapabilityDetectResult `json:"results"`
}

type AccountPoolBindingCreateRequest struct {
	ChannelID          int      `json:"channel_id" binding:"required"`
	AccountIDs         []int    `json:"account_ids"`
	ModelStrategy      string   `json:"model_strategy"`
	FixedModels        []string `json:"fixed_models"`
	SchedulePolicy     string   `json:"schedule_policy"`
	AccountRetryTimes  int      `json:"account_retry_times"`
	MaxUserConcurrency int      `json:"max_user_concurrency"`
}

type AccountPoolBoundChannelCreateRequest struct {
	Name               string   `json:"name" binding:"required"`
	ChannelType        int      `json:"type"`
	AccountIDs         []int    `json:"account_ids"`
	ModelStrategy      string   `json:"model_strategy"`
	FixedModels        []string `json:"fixed_models"`
	SchedulePolicy     string   `json:"schedule_policy"`
	AccountRetryTimes  int      `json:"account_retry_times"`
	MaxUserConcurrency int      `json:"max_user_concurrency"`
}

type AccountPoolBindingResponse struct {
	Id                  int    `json:"id"`
	PoolID              int    `json:"pool_id"`
	ChannelID           int    `json:"channel_id"`
	ChannelName         string `json:"channel_name"`
	ChannelStatus       int    `json:"channel_status"`
	AccountFilterConfig string `json:"account_filter_config"`
	ModelPolicy         string `json:"model_policy"`
	SchedulePolicy      string `json:"schedule_policy"`
	AccountRetryTimes   int    `json:"account_retry_times"`
	MaxUserConcurrency  int    `json:"max_user_concurrency"`
	Status              string `json:"status"`
	RuntimeEnabled      bool   `json:"runtime_enabled"`
	CreatedTime         int64  `json:"created_time"`
	UpdatedTime         int64  `json:"updated_time"`
}

type AccountPoolProxyCreateRequest struct {
	Name            string `json:"name" binding:"required"`
	Protocol        string `json:"protocol" binding:"required"`
	Host            string `json:"host" binding:"required"`
	Port            int    `json:"port" binding:"required"`
	Username        string `json:"username"`
	Password        string `json:"password"`
	Status          string `json:"status"`
	FallbackProxyID int    `json:"fallback_proxy_id"`
}

type AccountPoolProxyResponse struct {
	Id              int    `json:"id"`
	Name            string `json:"name"`
	Protocol        string `json:"protocol"`
	Host            string `json:"host"`
	Port            int    `json:"port"`
	Username        string `json:"username"`
	Status          string `json:"status"`
	FallbackProxyID int    `json:"fallback_proxy_id"`
	HasPassword     bool   `json:"has_password"`
	CreatedTime     int64  `json:"created_time"`
	UpdatedTime     int64  `json:"updated_time"`
}
