package controller

import (
	"errors"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type upstreamSourceAuthConfig struct {
	Email        string `json:"email,omitempty"`
	Password     string `json:"password,omitempty"`
	AccessToken  string `json:"access_token,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
	ExpiresAt    int64  `json:"expires_at,omitempty"`
}

// Keep this JSON shape in lockstep with service.upstreamSourceSyncConfig.
// Defaults are seeded before unmarshaling so an absent auto_sync_models key
// preserves the service default of true while explicit false still persists.
type upstreamSourceControllerSyncConfig struct {
	LocalGroup             string              `json:"local_group"`
	ChannelType            int                 `json:"channel_type"`
	DefaultPriority        int64               `json:"default_priority"`
	DefaultWeight          uint                `json:"default_weight"`
	EnableMonitor          bool                `json:"enable_monitor"`
	MonitorIntervalMinutes int                 `json:"monitor_interval_minutes"`
	AutoSyncModels         bool                `json:"auto_sync_models"`
	AllowPrivateIP         common.FlexibleBool `json:"allow_private_ip"`
}

func ListUpstreamSources(c *gin.Context) {
	var sources []model.UpstreamSource
	if err := model.DB.Where("status <> ?", model.UpstreamSourceStatusDeleted).Order("id desc").Find(&sources).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	responses := make([]dto.UpstreamSourceResponse, 0, len(sources))
	for _, source := range sources {
		responses = append(responses, upstreamSourceResponse(source))
	}
	common.ApiSuccess(c, responses)
}

func CreateUpstreamSource(c *gin.Context) {
	var req dto.UpstreamSourceCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiError(c, err)
		return
	}
	source, err := upstreamSourceFromCreateRequest(req)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if err := model.DB.Create(&source).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "upstream_source.create", map[string]interface{}{
		"id":   source.Id,
		"name": source.Name,
	})
	common.ApiSuccess(c, upstreamSourceResponse(source))
}

func GetUpstreamSource(c *gin.Context) {
	source, ok := loadUpstreamSourceForController(c)
	if !ok {
		return
	}
	common.ApiSuccess(c, upstreamSourceResponse(*source))
}

func UpdateUpstreamSource(c *gin.Context) {
	source, ok := loadUpstreamSourceForController(c)
	if !ok {
		return
	}
	var req dto.UpstreamSourceUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiError(c, err)
		return
	}
	updates, err := upstreamSourceUpdateMap(req)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if err := model.DB.Model(&model.UpstreamSource{}).Where("id = ?", source.Id).Updates(updates).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	if err := model.DB.First(source, source.Id).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "upstream_source.update", map[string]interface{}{
		"id":   source.Id,
		"name": source.Name,
	})
	common.ApiSuccess(c, upstreamSourceResponse(*source))
}

func UpdateUpstreamSourceCredentials(c *gin.Context) {
	source, ok := loadUpstreamSourceForController(c)
	if !ok {
		return
	}
	var req dto.UpstreamSourceCredentialsUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiError(c, err)
		return
	}
	authConfig, err := marshalUpstreamSourceAuthConfig(req.Email, req.Password)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if err := model.DB.Model(&model.UpstreamSource{}).Where("id = ?", source.Id).Updates(map[string]interface{}{
		"auth_config":  authConfig,
		"updated_time": common.GetTimestamp(),
	}).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	source.AuthConfig = authConfig
	recordManageAudit(c, "upstream_source.credentials_update", map[string]interface{}{
		"id":   source.Id,
		"name": source.Name,
	})
	common.ApiSuccess(c, upstreamSourceResponse(*source))
}

func DeleteUpstreamSource(c *gin.Context) {
	source, ok := loadUpstreamSourceForController(c)
	if !ok {
		return
	}
	if err := model.DB.Model(&model.UpstreamSource{}).Where("id = ?", source.Id).Updates(map[string]interface{}{
		"status":       model.UpstreamSourceStatusDeleted,
		"updated_time": common.GetTimestamp(),
	}).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "upstream_source.delete", map[string]interface{}{
		"id":   source.Id,
		"name": source.Name,
	})
	common.ApiSuccess(c, nil)
}

func DiscoverUpstreamSource(c *gin.Context) {
	source, ok := loadUpstreamSourceForController(c)
	if !ok {
		return
	}
	result, err := (&service.UpstreamSourceService{}).Discover(c.Request.Context(), source.Id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "upstream_source.discover", map[string]interface{}{
		"id":     source.Id,
		"name":   source.Name,
		"groups": result.Discovered,
	})
	common.ApiSuccess(c, result)
}

func ListUpstreamSourceMappings(c *gin.Context) {
	source, ok := loadUpstreamSourceForController(c)
	if !ok {
		return
	}
	mappings, err := listUpstreamSourceMappings(source.Id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, mappings)
}

func UpdateUpstreamSourceMappings(c *gin.Context) {
	source, ok := loadUpstreamSourceForController(c)
	if !ok {
		return
	}
	var req dto.UpstreamSourceMappingUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiError(c, err)
		return
	}
	if err := updateUpstreamSourceMappingSelection(source.Id, req.MappingIDs); err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "upstream_source.mapping_update", map[string]interface{}{
		"id":    source.Id,
		"name":  source.Name,
		"count": len(req.MappingIDs),
	})
	mappings, err := listUpstreamSourceMappings(source.Id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, mappings)
}

func SyncUpstreamSource(c *gin.Context) {
	source, ok := loadUpstreamSourceForController(c)
	if !ok {
		return
	}
	result, err := (&service.UpstreamSourceService{}).Sync(c.Request.Context(), source.Id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recordUpstreamSourceSyncAudit(c, *source, result)
	common.ApiSuccess(c, result)
}

func GetUpstreamSourceSyncResult(c *gin.Context) {
	source, ok := loadUpstreamSourceForController(c)
	if !ok {
		return
	}
	result, err := buildUpstreamSourceSyncResultFromDB(*source)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}

func loadUpstreamSourceForController(c *gin.Context) (*model.UpstreamSource, bool) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id == 0 {
		common.ApiError(c, errors.New("invalid upstream source id"))
		return nil, false
	}
	var source model.UpstreamSource
	if err := model.DB.Where("id = ? AND status <> ?", id, model.UpstreamSourceStatusDeleted).First(&source).Error; err != nil {
		common.ApiError(c, err)
		return nil, false
	}
	return &source, true
}

func upstreamSourceFromCreateRequest(req dto.UpstreamSourceCreateRequest) (model.UpstreamSource, error) {
	name := strings.TrimSpace(req.Name)
	sourceType := strings.TrimSpace(req.Type)
	baseURL := strings.TrimSpace(req.BaseURL)
	if name == "" {
		return model.UpstreamSource{}, errors.New("upstream source name is required")
	}
	if sourceType == "" {
		return model.UpstreamSource{}, errors.New("upstream source type is required")
	}
	if baseURL == "" {
		return model.UpstreamSource{}, errors.New("upstream source base URL is required")
	}
	authConfig, err := marshalUpstreamSourceAuthConfig(req.Email, req.Password)
	if err != nil {
		return model.UpstreamSource{}, err
	}
	syncConfig, err := marshalUpstreamSourceSyncConfig(upstreamSourceControllerSyncConfig{
		LocalGroup:             req.LocalGroup,
		ChannelType:            req.ChannelType,
		DefaultPriority:        req.DefaultPriority,
		DefaultWeight:          req.DefaultWeight,
		EnableMonitor:          req.EnableMonitor,
		MonitorIntervalMinutes: req.MonitorIntervalMinutes,
		AutoSyncModels:         req.AutoSyncModels,
		AllowPrivateIP:         common.FlexibleBool(req.AllowPrivateIP),
	})
	if err != nil {
		return model.UpstreamSource{}, err
	}
	relayBaseURL := strings.TrimSpace(req.RelayBaseURL)
	if relayBaseURL == "" {
		relayBaseURL = baseURL
	}
	return model.UpstreamSource{
		Name:             name,
		Type:             sourceType,
		Status:           model.UpstreamSourceStatusEnabled,
		BaseURL:          baseURL,
		AdminAPIBasePath: strings.TrimSpace(req.AdminAPIBasePath),
		RelayBaseURL:     relayBaseURL,
		AuthConfig:       authConfig,
		SyncConfig:       syncConfig,
	}, nil
}

func upstreamSourceUpdateMap(req dto.UpstreamSourceUpdateRequest) (map[string]interface{}, error) {
	syncConfig, err := marshalUpstreamSourceSyncConfig(upstreamSourceControllerSyncConfig{
		LocalGroup:             req.LocalGroup,
		ChannelType:            req.ChannelType,
		DefaultPriority:        req.DefaultPriority,
		DefaultWeight:          req.DefaultWeight,
		EnableMonitor:          req.EnableMonitor,
		MonitorIntervalMinutes: req.MonitorIntervalMinutes,
		AutoSyncModels:         req.AutoSyncModels,
		AllowPrivateIP:         common.FlexibleBool(req.AllowPrivateIP),
	})
	if err != nil {
		return nil, err
	}
	updates := map[string]interface{}{
		"sync_config":  syncConfig,
		"updated_time": common.GetTimestamp(),
	}
	if trimmed := strings.TrimSpace(req.Name); trimmed != "" {
		updates["name"] = trimmed
	}
	if trimmed := strings.TrimSpace(req.Status); trimmed != "" {
		if !isUpstreamSourceMutableStatus(trimmed) {
			return nil, errors.New("invalid upstream source status")
		}
		updates["status"] = trimmed
	}
	if trimmed := strings.TrimSpace(req.BaseURL); trimmed != "" {
		updates["base_url"] = trimmed
	}
	if trimmed := strings.TrimSpace(req.AdminAPIBasePath); trimmed != "" {
		updates["admin_api_base_path"] = trimmed
	}
	if trimmed := strings.TrimSpace(req.RelayBaseURL); trimmed != "" {
		updates["relay_base_url"] = trimmed
	}
	return updates, nil
}

func isUpstreamSourceMutableStatus(status string) bool {
	switch status {
	case model.UpstreamSourceStatusEnabled, model.UpstreamSourceStatusDisabled:
		return true
	default:
		return false
	}
}

func marshalUpstreamSourceAuthConfig(email string, password string) (string, error) {
	// Credential rotation intentionally drops cached login tokens and expiry.
	config := upstreamSourceAuthConfig{
		Email:    strings.TrimSpace(email),
		Password: password,
	}
	data, err := common.Marshal(config)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func marshalUpstreamSourceSyncConfig(config upstreamSourceControllerSyncConfig) (string, error) {
	config = normalizeUpstreamSourceControllerSyncConfig(config)
	data, err := common.Marshal(config)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func upstreamSourceResponse(source model.UpstreamSource) dto.UpstreamSourceResponse {
	auth := parseUpstreamSourceAuthConfig(source.AuthConfig)
	sync := parseUpstreamSourceSyncConfig(source.SyncConfig)
	return dto.UpstreamSourceResponse{
		Id:                     source.Id,
		Name:                   source.Name,
		Type:                   source.Type,
		Status:                 source.Status,
		BaseURL:                source.BaseURL,
		AdminAPIBasePath:       source.AdminAPIBasePath,
		RelayBaseURL:           source.RelayBaseURL,
		LocalGroup:             sync.LocalGroup,
		ChannelType:            sync.ChannelType,
		DefaultPriority:        sync.DefaultPriority,
		DefaultWeight:          sync.DefaultWeight,
		EnableMonitor:          sync.EnableMonitor,
		MonitorIntervalMinutes: sync.MonitorIntervalMinutes,
		AutoSyncModels:         sync.AutoSyncModels,
		AllowPrivateIP:         bool(sync.AllowPrivateIP),
		MaskedEmail:            common.MaskEmail(auth.Email),
		HasCredentials:         upstreamSourceHasCredentials(auth),
		LastDiscoveryTime:      source.LastDiscoveryTime,
		LastDiscoveryStatus:    source.LastDiscoveryStatus,
		LastDiscoveryError:     sanitizeUpstreamSourceResponseError(source.LastDiscoveryError),
		LastSyncTime:           source.LastSyncTime,
		LastSyncStatus:         source.LastSyncStatus,
		LastSyncError:          sanitizeUpstreamSourceResponseError(source.LastSyncError),
		CreatedTime:            source.CreatedTime,
		UpdatedTime:            source.UpdatedTime,
	}
}

func parseUpstreamSourceSyncConfig(raw string) upstreamSourceControllerSyncConfig {
	config := defaultUpstreamSourceControllerSyncConfig()
	if strings.TrimSpace(raw) == "" {
		return config
	}
	if err := common.UnmarshalJsonStr(raw, &config); err != nil {
		return defaultUpstreamSourceControllerSyncConfig()
	}
	return normalizeUpstreamSourceControllerSyncConfig(config)
}

func normalizeUpstreamSourceControllerSyncConfig(config upstreamSourceControllerSyncConfig) upstreamSourceControllerSyncConfig {
	if strings.TrimSpace(config.LocalGroup) == "" {
		config.LocalGroup = "default"
	} else {
		config.LocalGroup = strings.TrimSpace(config.LocalGroup)
	}
	if config.ChannelType == 0 {
		config.ChannelType = constant.ChannelTypeOpenAI
	}
	return config
}

func defaultUpstreamSourceControllerSyncConfig() upstreamSourceControllerSyncConfig {
	return upstreamSourceControllerSyncConfig{
		LocalGroup:     "default",
		ChannelType:    constant.ChannelTypeOpenAI,
		AutoSyncModels: true,
	}
}

func sanitizeUpstreamSourceResponseError(text string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	return service.SanitizeUpstreamSourceError(errors.New(text))
}

func parseUpstreamSourceAuthConfig(raw string) upstreamSourceAuthConfig {
	var auth upstreamSourceAuthConfig
	if strings.TrimSpace(raw) == "" {
		return auth
	}
	if err := common.UnmarshalJsonStr(raw, &auth); err != nil {
		return upstreamSourceAuthConfig{}
	}
	return auth
}

func upstreamSourceHasCredentials(auth upstreamSourceAuthConfig) bool {
	return strings.TrimSpace(auth.Email) != "" ||
		strings.TrimSpace(auth.Password) != "" ||
		strings.TrimSpace(auth.AccessToken) != "" ||
		strings.TrimSpace(auth.RefreshToken) != ""
}

func listUpstreamSourceMappings(sourceID int) ([]dto.UpstreamSourceMappingResponse, error) {
	var mappings []model.UpstreamSourceChannelMapping
	if err := model.DB.Where("source_id = ?", sourceID).Order("id").Find(&mappings).Error; err != nil {
		return nil, err
	}
	responses := make([]dto.UpstreamSourceMappingResponse, 0, len(mappings))
	for _, mapping := range mappings {
		responses = append(responses, upstreamSourceMappingDTO(mapping))
	}
	return responses, nil
}

func upstreamSourceMappingDTO(mapping model.UpstreamSourceChannelMapping) dto.UpstreamSourceMappingResponse {
	return dto.UpstreamSourceMappingResponse{
		Id:                      mapping.Id,
		SourceID:                mapping.SourceID,
		SyncEnabled:             mapping.SyncEnabled,
		UpstreamGroupID:         mapping.UpstreamGroupID,
		UpstreamGroupName:       mapping.UpstreamGroupName,
		UpstreamPlatform:        mapping.UpstreamPlatform,
		DiscoveryStatus:         mapping.DiscoveryStatus,
		UpstreamStatus:          mapping.UpstreamStatus,
		UpstreamRateMultiplier:  mapping.UpstreamRateMultiplier,
		EffectiveRateMultiplier: mapping.EffectiveRateMultiplier,
		HasUpstreamKey:          mapping.UpstreamKeyID != "",
		LocalChannelID:          mapping.LocalChannelID,
		SyncStatus:              mapping.SyncStatus,
		LastError:               sanitizeUpstreamSourceResponseError(mapping.LastError),
		LastDiscoveredAt:        mapping.LastDiscoveredAt,
		LastSyncedAt:            mapping.LastSyncedAt,
	}
}

func updateUpstreamSourceMappingSelection(sourceID int, mappingIDs []int) error {
	selected := make([]int, 0, len(mappingIDs))
	seen := make(map[int]struct{}, len(mappingIDs))
	for _, id := range mappingIDs {
		if id == 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		selected = append(selected, id)
	}
	return model.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.UpstreamSourceChannelMapping{}).
			Where("source_id = ?", sourceID).
			Update("sync_enabled", false).Error; err != nil {
			return err
		}
		if len(selected) == 0 {
			return nil
		}
		return tx.Model(&model.UpstreamSourceChannelMapping{}).
			Where("source_id = ? AND id IN ?", sourceID, selected).
			Update("sync_enabled", true).Error
	})
}

func buildUpstreamSourceSyncResultFromDB(source model.UpstreamSource) (dto.UpstreamSourceSyncResult, error) {
	var mappings []model.UpstreamSourceChannelMapping
	if err := model.DB.Where("source_id = ?", source.Id).Order("id").Find(&mappings).Error; err != nil {
		return dto.UpstreamSourceSyncResult{}, err
	}
	result := dto.UpstreamSourceSyncResult{
		SourceID: source.Id,
		Status:   source.LastSyncStatus,
		Error:    sanitizeUpstreamSourceResponseError(source.LastSyncError),
		Results:  make([]dto.UpstreamSourceMappingSyncResult, 0, len(mappings)),
	}
	for _, mapping := range mappings {
		item := dto.UpstreamSourceMappingSyncResult{
			MappingID:       mapping.Id,
			UpstreamGroupID: mapping.UpstreamGroupID,
			LocalChannelID:  mapping.LocalChannelID,
			Status:          mapping.SyncStatus,
			Error:           sanitizeUpstreamSourceResponseError(mapping.LastError),
		}
		switch mapping.SyncStatus {
		case model.UpstreamMappingSyncStatusSynced:
			result.Updated++
		case model.UpstreamMappingSyncStatusSkipped:
			result.Skipped++
		case model.UpstreamMappingSyncStatusFailed, model.UpstreamMappingSyncStatusNeedsAttention:
			result.Failed++
		}
		result.Results = append(result.Results, item)
	}
	return result, nil
}

func recordUpstreamSourceSyncAudit(c *gin.Context, source model.UpstreamSource, result *dto.UpstreamSourceSyncResult) {
	if result == nil {
		return
	}
	recordManageAudit(c, "upstream_source.sync", map[string]interface{}{
		"id":      source.Id,
		"name":    source.Name,
		"created": result.Created,
		"updated": result.Updated,
		"failed":  result.Failed,
	})
	for _, item := range result.Results {
		if !item.Created && !item.Updated {
			continue
		}
		action := "upstream_source.channel_update"
		if item.Created {
			action = "upstream_source.channel_create"
		}
		recordManageAudit(c, action, map[string]interface{}{
			"id":          source.Id,
			"name":        source.Name,
			"channelId":   item.LocalChannelID,
			"channelName": upstreamSourceAuditChannelName(item.LocalChannelID),
		})
	}
}

func upstreamSourceAuditChannelName(channelID int) string {
	if channelID == 0 {
		return ""
	}
	var channel model.Channel
	if err := model.DB.Select("name").First(&channel, channelID).Error; err != nil {
		return ""
	}
	return channel.Name
}
