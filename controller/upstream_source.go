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
	Email         string `json:"email,omitempty"`
	Password      string `json:"password,omitempty"`
	AccessToken   string `json:"access_token,omitempty"`
	RefreshToken  string `json:"refresh_token,omitempty"`
	ExpiresAt     int64  `json:"expires_at,omitempty"`
	SessionSource string `json:"session_source,omitempty"`
}

const (
	upstreamSourceControllerModelStrategyAllUpstream = "all_upstream"
	upstreamSourceControllerModelStrategyFixed       = "fixed"
)

// Keep this JSON shape in lockstep with service.upstreamSourceSyncConfig.
// Defaults are seeded before unmarshaling so an absent auto_sync_models key
// preserves the service default of true while explicit false still persists.
//
// The source-level default fields below (ChannelType..DefaultLocalGroup) are
// no longer populated from the create/update request DTOs — everything now
// lives in per-group rules (LocalGroupRules). They are kept here only so
// legacy stored JSON (version 0, pre-fold) still parses; see
// service.MigrateAndNormalizeUpstreamSourceSyncConfigRaw for the migration.
type upstreamSourceControllerSyncConfig struct {
	LocalGroup                          string                             `json:"local_group"`
	ChannelType                         int                                `json:"channel_type"`
	DefaultPriority                     int64                              `json:"default_priority"`
	DefaultWeight                       uint                               `json:"default_weight"`
	EnableMonitor                       bool                               `json:"enable_monitor"`
	MonitorIntervalMinutes              int                                `json:"monitor_interval_minutes"`
	AutoSyncModels                      bool                               `json:"auto_sync_models"`
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
	// SyncConfigVersion marks whether this blob has already gone through the
	// legacy-defaults-to-rules migration. Configs built here from a create/
	// update request are stamped version 1 directly (never version 0) so a
	// genuinely new, rule-only source syncs nothing rather than having a
	// catch-all rule synthesized for it. Keep in lockstep with
	// service.upstreamSourceSyncConfig.SyncConfigVersion.
	SyncConfigVersion int `json:"sync_config_version,omitempty"`
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
	// Rules are authoritative for sync eligibility: re-derive each mapping's
	// sync_enabled from the just-saved rules so adding/editing a rule takes effect
	// without a manual per-mapping toggle. Non-fatal — the config was already saved.
	if err := service.RecomputeUpstreamSourceMappingSyncEligibility(c.Request.Context(), source.Id); err != nil {
		common.SysError("failed to recompute upstream source mapping sync eligibility: " + service.SanitizeUpstreamSourceError(err))
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

func ImportUpstreamSourceSession(c *gin.Context) {
	source, ok := loadUpstreamSourceForController(c)
	if !ok {
		return
	}
	var req dto.UpstreamSourceSessionImportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiError(c, err)
		return
	}
	if err := service.ApplyUpstreamSourceImportedSession(c.Request.Context(), source, req); err != nil {
		common.ApiError(c, err)
		return
	}
	if err := model.DB.First(source, source.Id).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "upstream_source.session_import", map[string]interface{}{"id": source.Id, "name": source.Name})
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
	mappings, err := listUpstreamSourceMappings(*source)
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
	mappings, err := listUpstreamSourceMappings(*source)
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

func RunUpstreamSourceAutoPriority(c *gin.Context) {
	source, ok := loadUpstreamSourceForController(c)
	if !ok {
		return
	}
	result, err := (&service.UpstreamSourceService{}).RunAutoPriority(c.Request.Context(), source.Id, common.GetTimestamp())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "upstream_source.auto_priority_run", map[string]interface{}{
		"id":      source.Id,
		"name":    source.Name,
		"updated": result.Updated,
		"skipped": result.Skipped,
		"failed":  result.Failed,
	})
	common.ApiSuccess(c, result)
}

func GetUpstreamSourceRuleModelOptions(c *gin.Context) {
	source, ok := loadUpstreamSourceForController(c)
	if !ok {
		return
	}
	var req dto.UpstreamSourceRuleModelOptionsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiError(c, err)
		return
	}
	result, err := (&service.UpstreamSourceService{}).ResolveRuleModelOptions(c.Request.Context(), source.Id, req.LocalGroupRules, req.RuleIndex)
	if err != nil {
		common.ApiError(c, err)
		return
	}
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

// normalizeUpstreamSourceURL trims surrounding whitespace and any trailing
// slashes from a base URL or base path. A trailing slash (commonly copied from
// a browser address bar) makes URL joining produce a double slash — e.g.
// "https://host/" + "/v1/chat/completions" -> "https://host//v1/..." — which
// some gateways route to their web/SPA fallback (HTTP 200 + HTML) instead of
// the API, silently breaking the generated channel. Leading slashes are kept so
// base paths like "/api/v1" stay valid.
func normalizeUpstreamSourceURL(raw string) string {
	return strings.TrimRight(strings.TrimSpace(raw), "/")
}

func upstreamSourceFromCreateRequest(req dto.UpstreamSourceCreateRequest) (model.UpstreamSource, error) {
	name := strings.TrimSpace(req.Name)
	sourceType := strings.TrimSpace(req.Type)
	baseURL := normalizeUpstreamSourceURL(req.BaseURL)
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
	syncConfigInput := defaultUpstreamSourceControllerSyncConfig()
	syncConfigInput.LocalGroup = req.LocalGroup
	syncConfigInput.AllowPrivateIP = common.FlexibleBool(req.AllowPrivateIP)
	syncConfigInput.LocalGroupRules = req.LocalGroupRules
	// Stamp the config as already-migrated (never version 0) so a genuinely
	// new, rule-only source syncs nothing instead of getting a synthesized
	// catch-all rule from the legacy migration.
	syncConfigInput.SyncConfigVersion = 1
	syncConfig, err := marshalUpstreamSourceSyncConfig(syncConfigInput)
	if err != nil {
		return model.UpstreamSource{}, err
	}
	relayBaseURL := normalizeUpstreamSourceURL(req.RelayBaseURL)
	if relayBaseURL == "" {
		relayBaseURL = baseURL
	}
	return model.UpstreamSource{
		Name:             name,
		Type:             sourceType,
		Status:           model.UpstreamSourceStatusEnabled,
		BaseURL:          baseURL,
		AdminAPIBasePath: normalizeUpstreamSourceURL(req.AdminAPIBasePath),
		RelayBaseURL:     relayBaseURL,
		AuthConfig:       authConfig,
		SyncConfig:       syncConfig,
	}, nil
}

func upstreamSourceUpdateMap(req dto.UpstreamSourceUpdateRequest) (map[string]interface{}, error) {
	syncConfigInput := defaultUpstreamSourceControllerSyncConfig()
	syncConfigInput.LocalGroup = req.LocalGroup
	syncConfigInput.AllowPrivateIP = common.FlexibleBool(req.AllowPrivateIP)
	syncConfigInput.LocalGroupRules = req.LocalGroupRules
	// Stamp the config as already-migrated (never version 0) so a genuinely
	// new, rule-only source syncs nothing instead of getting a synthesized
	// catch-all rule from the legacy migration.
	syncConfigInput.SyncConfigVersion = 1
	syncConfig, err := marshalUpstreamSourceSyncConfig(syncConfigInput)
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
	if trimmed := normalizeUpstreamSourceURL(req.BaseURL); trimmed != "" {
		updates["base_url"] = trimmed
	}
	if trimmed := normalizeUpstreamSourceURL(req.AdminAPIBasePath); trimmed != "" {
		updates["admin_api_base_path"] = trimmed
	}
	if trimmed := normalizeUpstreamSourceURL(req.RelayBaseURL); trimmed != "" {
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
	return service.WriteUpstreamSourceAuthConfig(string(data))
}

func marshalUpstreamSourceSyncConfig(config upstreamSourceControllerSyncConfig) (string, error) {
	config = normalizeUpstreamSourceControllerSyncConfig(config)
	data, err := common.Marshal(config)
	if err != nil {
		return "", err
	}
	// Route the freshly composed config through the service parser so the
	// persisted blob is upgraded to the folded (version 1) shape and carries
	// any synthesized/backfilled rules, rather than leaving that to happen
	// only at the next runtime sync read.
	migrated, err := service.MigrateAndNormalizeUpstreamSourceSyncConfigRaw(string(data))
	if err != nil {
		return "", err
	}
	return migrated, nil
}

func upstreamSourceResponse(source model.UpstreamSource) dto.UpstreamSourceResponse {
	auth := parseUpstreamSourceAuthConfig(source.AuthConfig)
	// Migrate on read too, so the admin UI sees folded rules for sources
	// whose stored blob predates this create/update-time migration.
	syncConfigRaw, err := service.MigrateAndNormalizeUpstreamSourceSyncConfigRaw(source.SyncConfig)
	if err != nil {
		syncConfigRaw = source.SyncConfig
	}
	sync := parseUpstreamSourceSyncConfig(syncConfigRaw)
	return dto.UpstreamSourceResponse{
		Id:                  source.Id,
		Name:                source.Name,
		Type:                source.Type,
		Status:              source.Status,
		BaseURL:             source.BaseURL,
		AdminAPIBasePath:    source.AdminAPIBasePath,
		RelayBaseURL:        source.RelayBaseURL,
		LocalGroup:          sync.LocalGroup,
		AllowPrivateIP:      bool(sync.AllowPrivateIP),
		LocalGroupRules:     sync.LocalGroupRules,
		MaskedEmail:         common.MaskEmail(auth.Email),
		HasCredentials:      upstreamSourceHasCredentials(auth),
		SessionSource:       auth.SessionSource,
		TurnstileBlocked:    upstreamSourceTurnstileBlocked(source),
		LastDiscoveryTime:   source.LastDiscoveryTime,
		LastDiscoveryStatus: source.LastDiscoveryStatus,
		LastDiscoveryError:  sanitizeUpstreamSourceResponseError(source.LastDiscoveryError),
		LastSyncTime:        source.LastSyncTime,
		LastSyncStatus:      source.LastSyncStatus,
		LastSyncError:       sanitizeUpstreamSourceResponseError(source.LastSyncError),
		CreatedTime:         source.CreatedTime,
		UpdatedTime:         source.UpdatedTime,
	}
}

func upstreamSourceTurnstileBlocked(source model.UpstreamSource) bool {
	marker := service.ErrUpstreamSourceTurnstileRequired.Error()
	return source.LastDiscoveryError == marker || source.LastSyncError == marker
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
	if strings.TrimSpace(config.DefaultLocalGroup) == "" {
		config.DefaultLocalGroup = config.LocalGroup
	} else {
		config.DefaultLocalGroup = strings.TrimSpace(config.DefaultLocalGroup)
	}
	if config.MonitorIntervalMinutes > 0 && config.MonitorIntervalMinutes < 5 {
		config.MonitorIntervalMinutes = 5
	}
	if config.AutoSyncEnabled {
		if config.AutoSyncIntervalMinutes < 5 {
			config.AutoSyncIntervalMinutes = 5
		}
	} else {
		config.AutoSyncIntervalMinutes = 0
	}
	switch {
	case config.AutoPriorityIntervalMinutes < 0:
		config.AutoPriorityIntervalMinutes = 30
	case config.AutoPriorityIntervalMinutes == 0:
		config.AutoPriorityIntervalMinutes = 0
	}
	switch {
	case config.AutoPriorityWindowHours <= 0:
		config.AutoPriorityWindowHours = 24
	case config.AutoPriorityWindowHours > 168:
		config.AutoPriorityWindowHours = 168
	}
	switch {
	case config.AutoPriorityAvailabilityWindowHours <= 0:
		config.AutoPriorityAvailabilityWindowHours = 24
	case config.AutoPriorityAvailabilityWindowHours > 168:
		config.AutoPriorityAvailabilityWindowHours = 168
	}
	config.ModelStrategy = normalizeUpstreamSourceControllerModelStrategy(config.ModelStrategy, config.AutoSyncModels)
	config.FixedModels = normalizeUpstreamSourceControllerFixedModels(config.FixedModels)
	config.CodexImageGenerationBridgePolicy = dto.NormalizeCodexImageGenerationBridgePolicy(config.CodexImageGenerationBridgePolicy)
	config.LocalGroupRules = service.NormalizeUpstreamSourceLocalGroupRulesForConfig(config.LocalGroupRules)
	return config
}

func defaultUpstreamSourceControllerSyncConfig() upstreamSourceControllerSyncConfig {
	return upstreamSourceControllerSyncConfig{
		LocalGroup:                          "default",
		ChannelType:                         constant.ChannelTypeOpenAI,
		AutoSyncModels:                      true,
		AutoPriorityIntervalMinutes:         30,
		AutoPriorityWindowHours:             24,
		AutoPriorityAvailabilityWindowHours: 24,
		CodexImageGenerationBridgePolicy:    dto.CodexImageGenerationBridgePolicyFollow,
		ModelStrategy:                       upstreamSourceControllerModelStrategyAllUpstream,
		// DefaultLocalGroup is intentionally left empty here (not seeded to
		// "default") so normalizeUpstreamSourceControllerSyncConfig's
		// "copy LocalGroup when DefaultLocalGroup is empty" branch actually
		// runs. The request DTO no longer carries default_local_group
		// (folded into per-rule overrides), so a non-empty seed would
		// permanently shadow the real base LocalGroup with "default".
	}
}

func normalizeUpstreamSourceControllerModelStrategy(modelStrategy string, autoSyncModels bool) string {
	switch strings.TrimSpace(modelStrategy) {
	case upstreamSourceControllerModelStrategyAllUpstream:
		return upstreamSourceControllerModelStrategyAllUpstream
	case upstreamSourceControllerModelStrategyFixed:
		return upstreamSourceControllerModelStrategyFixed
	case "":
		if !autoSyncModels {
			return upstreamSourceControllerModelStrategyFixed
		}
		return upstreamSourceControllerModelStrategyAllUpstream
	default:
		return upstreamSourceControllerModelStrategyAllUpstream
	}
}

func normalizeUpstreamSourceControllerFixedModels(models []string) []string {
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

func sanitizeUpstreamSourceResponseError(text string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	return service.SanitizeUpstreamSourceError(errors.New(text))
}

func parseUpstreamSourceAuthConfig(raw string) upstreamSourceAuthConfig {
	var auth upstreamSourceAuthConfig
	decrypted, err := service.ReadUpstreamSourceAuthConfig(raw)
	if err != nil || strings.TrimSpace(decrypted) == "" {
		return upstreamSourceAuthConfig{}
	}
	if err := common.UnmarshalJsonStr(decrypted, &auth); err != nil {
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

func listUpstreamSourceMappings(source model.UpstreamSource) ([]dto.UpstreamSourceMappingResponse, error) {
	var mappings []model.UpstreamSourceChannelMapping
	if err := model.DB.Where("source_id = ?", source.Id).Order("id").Find(&mappings).Error; err != nil {
		return nil, err
	}
	responses := make([]dto.UpstreamSourceMappingResponse, 0, len(mappings))
	for _, mapping := range mappings {
		responses = append(responses, service.BuildUpstreamSourceMappingResponse(mapping, source.SyncConfig))
	}
	return responses, nil
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
