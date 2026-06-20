package dto

type UpstreamSourceCreateRequest struct {
	Name                   string `json:"name" binding:"required"`
	Type                   string `json:"type" binding:"required"`
	BaseURL                string `json:"base_url" binding:"required"`
	AdminAPIBasePath       string `json:"admin_api_base_path"`
	RelayBaseURL           string `json:"relay_base_url"`
	Email                  string `json:"email"`
	Password               string `json:"password"`
	LocalGroup             string `json:"local_group"`
	ChannelType            int    `json:"channel_type"`
	DefaultPriority        int64  `json:"default_priority"`
	DefaultWeight          uint   `json:"default_weight"`
	EnableMonitor          bool   `json:"enable_monitor"`
	MonitorIntervalMinutes int    `json:"monitor_interval_minutes"`
	AutoSyncModels         bool   `json:"auto_sync_models"`
	AllowPrivateIP         bool   `json:"allow_private_ip"`
}

type UpstreamSourceUpdateRequest struct {
	Name                   string `json:"name"`
	Status                 string `json:"status"`
	BaseURL                string `json:"base_url"`
	AdminAPIBasePath       string `json:"admin_api_base_path"`
	RelayBaseURL           string `json:"relay_base_url"`
	LocalGroup             string `json:"local_group"`
	ChannelType            int    `json:"channel_type"`
	DefaultPriority        int64  `json:"default_priority"`
	DefaultWeight          uint   `json:"default_weight"`
	EnableMonitor          bool   `json:"enable_monitor"`
	MonitorIntervalMinutes int    `json:"monitor_interval_minutes"`
	AutoSyncModels         bool   `json:"auto_sync_models"`
	AllowPrivateIP         bool   `json:"allow_private_ip"`
}

type UpstreamSourceCredentialsUpdateRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type UpstreamSourceResponse struct {
	Id                     int    `json:"id"`
	Name                   string `json:"name"`
	Type                   string `json:"type"`
	Status                 string `json:"status"`
	BaseURL                string `json:"base_url"`
	AdminAPIBasePath       string `json:"admin_api_base_path"`
	RelayBaseURL           string `json:"relay_base_url"`
	LocalGroup             string `json:"local_group"`
	ChannelType            int    `json:"channel_type"`
	DefaultPriority        int64  `json:"default_priority"`
	DefaultWeight          uint   `json:"default_weight"`
	EnableMonitor          bool   `json:"enable_monitor"`
	MonitorIntervalMinutes int    `json:"monitor_interval_minutes"`
	AutoSyncModels         bool   `json:"auto_sync_models"`
	AllowPrivateIP         bool   `json:"allow_private_ip"`
	MaskedEmail            string `json:"masked_email"`
	HasCredentials         bool   `json:"has_credentials"`
	LastDiscoveryTime      int64  `json:"last_discovery_time"`
	LastDiscoveryStatus    string `json:"last_discovery_status"`
	LastDiscoveryError     string `json:"last_discovery_error"`
	LastSyncTime           int64  `json:"last_sync_time"`
	LastSyncStatus         string `json:"last_sync_status"`
	LastSyncError          string `json:"last_sync_error"`
	CreatedTime            int64  `json:"created_time"`
	UpdatedTime            int64  `json:"updated_time"`
}

type UpstreamSourceMappingResponse struct {
	Id                      int      `json:"id"`
	SourceID                int      `json:"source_id"`
	SyncEnabled             bool     `json:"sync_enabled"`
	UpstreamGroupID         string   `json:"upstream_group_id"`
	UpstreamGroupName       string   `json:"upstream_group_name"`
	UpstreamPlatform        string   `json:"upstream_platform"`
	DiscoveryStatus         string   `json:"discovery_status"`
	UpstreamStatus          string   `json:"upstream_status"`
	UpstreamRateMultiplier  *float64 `json:"upstream_rate_multiplier"`
	EffectiveRateMultiplier *float64 `json:"effective_rate_multiplier"`
	UpstreamKeyID           string   `json:"upstream_key_id"`
	HasUpstreamKey          bool     `json:"has_upstream_key"`
	LocalChannelID          int      `json:"local_channel_id"`
	SyncStatus              string   `json:"sync_status"`
	LastError               string   `json:"last_error"`
	LastDiscoveredAt        int64    `json:"last_discovered_at"`
	LastSyncedAt            int64    `json:"last_synced_at"`
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
