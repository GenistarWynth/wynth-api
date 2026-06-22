package dto

type AccountPoolCreateRequest struct {
	Name                  string `json:"name" binding:"required"`
	Platform              string `json:"platform"`
	DefaultProxyID        int    `json:"default_proxy_id"`
	DefaultMonitorEnabled bool   `json:"default_monitor_enabled"`
	DefaultSchedulePolicy string `json:"default_schedule_policy"`
	Remark                string `json:"remark"`
}

type AccountPoolUpdateRequest = AccountPoolCreateRequest

type AccountPoolResponse struct {
	Id                    int    `json:"id"`
	Name                  string `json:"name"`
	Platform              string `json:"platform"`
	Status                string `json:"status"`
	DefaultProxyID        int    `json:"default_proxy_id"`
	DefaultMonitorEnabled bool   `json:"default_monitor_enabled"`
	DefaultSchedulePolicy string `json:"default_schedule_policy"`
	Remark                string `json:"remark"`
	CreatedTime           int64  `json:"created_time"`
	UpdatedTime           int64  `json:"updated_time"`
}

type AccountPoolCredentialConfigRequest struct {
	Type         string `json:"type"`
	APIKey       string `json:"api_key"`
	Email        string `json:"email"`
	RefreshToken string `json:"refresh_token"`
}

type AccountPoolTokenStateRequest struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresAt    int64  `json:"expires_at"`
	Version      int64  `json:"version"`
}

type AccountPoolAccountCreateRequest struct {
	Name               string                             `json:"name" binding:"required"`
	AccountIdentifier  string                             `json:"account_identifier"`
	Credential         AccountPoolCredentialConfigRequest `json:"credential"`
	TokenState         AccountPoolTokenStateRequest       `json:"token_state"`
	Status             string                             `json:"status"`
	Priority           int64                              `json:"priority"`
	Weight             uint                               `json:"weight"`
	MaxConcurrency     int                                `json:"max_concurrency"`
	ProxyID            int                                `json:"proxy_id"`
	SupportedModels    []string                           `json:"supported_models"`
	ModelMapping       map[string]string                  `json:"model_mapping"`
	LastUsedAt         int64                              `json:"last_used_at"`
	RateLimitedUntil   int64                              `json:"rate_limited_until"`
	TempDisabledUntil  int64                              `json:"temp_disabled_until"`
	TempDisabledReason string                             `json:"temp_disabled_reason"`
	LastError          string                             `json:"last_error"`
}

type AccountPoolAccountResponse struct {
	Id                 int               `json:"id"`
	PoolID             int               `json:"pool_id"`
	Name               string            `json:"name"`
	AccountIdentifier  string            `json:"account_identifier"`
	Status             string            `json:"status"`
	Priority           int64             `json:"priority"`
	Weight             uint              `json:"weight"`
	MaxConcurrency     int               `json:"max_concurrency"`
	ProxyID            int               `json:"proxy_id"`
	SupportedModels    []string          `json:"supported_models"`
	ModelMapping       map[string]string `json:"model_mapping"`
	LastUsedAt         int64             `json:"last_used_at"`
	RateLimitedUntil   int64             `json:"rate_limited_until"`
	TempDisabledUntil  int64             `json:"temp_disabled_until"`
	TempDisabledReason string            `json:"temp_disabled_reason"`
	LastError          string            `json:"last_error"`
	HasCredential      bool              `json:"has_credential"`
	HasToken           bool              `json:"has_token"`
	CreatedTime        int64             `json:"created_time"`
	UpdatedTime        int64             `json:"updated_time"`
}

type AccountPoolBindingCreateRequest struct {
	ChannelID         int      `json:"channel_id" binding:"required"`
	AccountIDs        []int    `json:"account_ids"`
	ModelStrategy     string   `json:"model_strategy"`
	FixedModels       []string `json:"fixed_models"`
	SchedulePolicy    string   `json:"schedule_policy"`
	AccountRetryTimes int      `json:"account_retry_times"`
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
	Status              string `json:"status"`
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
