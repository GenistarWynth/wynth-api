package model

import (
	"errors"
	"strings"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	UpstreamSourceTypeSub2API = "sub2api"
	UpstreamSourceTypeNewAPI  = "new-api"

	UpstreamSourceStatusEnabled  = "enabled"
	UpstreamSourceStatusDisabled = "disabled"
	UpstreamSourceStatusDeleted  = "deleted"

	UpstreamDiscoveryStatusNever     = "never_run"
	UpstreamDiscoveryStatusSucceeded = "succeeded"
	UpstreamDiscoveryStatusFailed    = "failed"

	UpstreamSyncStatusNever     = "never_run"
	UpstreamSyncStatusRunning   = "running"
	UpstreamSyncStatusSucceeded = "succeeded"
	UpstreamSyncStatusFailed    = "failed"

	UpstreamMappingDiscoveryStatusActive  = "active"
	UpstreamMappingDiscoveryStatusStale   = "stale"
	UpstreamMappingDiscoveryStatusInvalid = "invalid"

	UpstreamMappingSyncStatusNeverSynced    = "never_synced"
	UpstreamMappingSyncStatusSynced         = "synced"
	UpstreamMappingSyncStatusFailed         = "failed"
	UpstreamMappingSyncStatusSkipped        = "skipped"
	UpstreamMappingSyncStatusNeedsAttention = "needs_attention"
)

type UpstreamSource struct {
	Id                     int    `json:"id"`
	Name                   string `json:"name" gorm:"type:varchar(191);not null;index"`
	Type                   string `json:"type" gorm:"type:varchar(32);not null;index"`
	Status                 string `json:"status" gorm:"type:varchar(32);not null;default:'enabled';index"`
	BaseURL                string `json:"base_url" gorm:"type:varchar(512);not null"`
	AdminAPIBasePath       string `json:"admin_api_base_path" gorm:"type:varchar(128);not null;default:'/api/v1'"`
	RelayBaseURL           string `json:"relay_base_url" gorm:"type:varchar(512);not null"`
	AuthConfig             string `json:"-" gorm:"type:text"`
	SyncConfig             string `json:"sync_config" gorm:"type:text"`
	MonitorEnabled         bool   `json:"monitor_enabled" gorm:"index"`
	MonitorIntervalMinutes int    `json:"monitor_interval_minutes"`
	NextMonitorAt          int64  `json:"next_monitor_at" gorm:"bigint;index"`
	CurrentMonitorToken    string `json:"-" gorm:"type:varchar(64);index"`
	MonitorStartedAt       int64  `json:"monitor_started_at" gorm:"bigint"`
	LastMonitorTime        int64  `json:"last_monitor_time" gorm:"bigint"`
	CurrentSyncToken       string `json:"-" gorm:"type:varchar(64);index"`
	SyncStartedAt          int64  `json:"sync_started_at" gorm:"bigint"`
	LastDiscoveryTime      int64  `json:"last_discovery_time" gorm:"bigint"`
	LastDiscoveryStatus    string `json:"last_discovery_status" gorm:"type:varchar(32)"`
	LastDiscoveryError     string `json:"last_discovery_error" gorm:"type:varchar(1024)"`
	LastSyncTime           int64  `json:"last_sync_time" gorm:"bigint"`
	LastSyncStatus         string `json:"last_sync_status" gorm:"type:varchar(32)"`
	LastSyncError          string `json:"last_sync_error" gorm:"type:varchar(1024)"`
	CreatedTime            int64  `json:"created_time" gorm:"bigint"`
	UpdatedTime            int64  `json:"updated_time" gorm:"bigint"`
}

func (source *UpstreamSource) BeforeCreate(tx *gorm.DB) error {
	now := common.GetTimestamp()
	if source.CreatedTime == 0 {
		source.CreatedTime = now
	}
	if source.UpdatedTime == 0 {
		source.UpdatedTime = now
	}
	if source.Status == "" {
		source.Status = UpstreamSourceStatusEnabled
	}
	if source.AdminAPIBasePath == "" {
		source.AdminAPIBasePath = DefaultUpstreamSourceAdminAPIBasePath(source.Type)
	}
	if source.RelayBaseURL == "" {
		source.RelayBaseURL = source.BaseURL
	}
	if source.LastDiscoveryStatus == "" {
		source.LastDiscoveryStatus = UpstreamDiscoveryStatusNever
	}
	if source.LastSyncStatus == "" {
		source.LastSyncStatus = UpstreamSyncStatusNever
	}
	return nil
}

func DefaultUpstreamSourceAdminAPIBasePath(sourceType string) string {
	if strings.TrimSpace(sourceType) == UpstreamSourceTypeNewAPI {
		return "/api"
	}
	return "/api/v1"
}

func (source *UpstreamSource) BeforeUpdate(tx *gorm.DB) error {
	source.UpdatedTime = common.GetTimestamp()
	return nil
}

type UpstreamSourceChannelMapping struct {
	Id                       int      `json:"id"`
	SourceID                 int      `json:"source_id" gorm:"not null;uniqueIndex:idx_upstream_source_group;index"`
	SyncEnabled              bool     `json:"sync_enabled" gorm:"not null;default:false;index"`
	UpstreamGroupID          string   `json:"upstream_group_id" gorm:"type:varchar(191);not null;uniqueIndex:idx_upstream_source_group"`
	UpstreamGroupName        string   `json:"upstream_group_name" gorm:"type:varchar(191)"`
	UpstreamGroupDescription string   `json:"upstream_group_description" gorm:"type:varchar(512)"`
	UpstreamPlatform         string   `json:"upstream_platform" gorm:"type:varchar(64)"`
	DiscoveryStatus          string   `json:"discovery_status" gorm:"type:varchar(32);index"`
	UpstreamStatus           string   `json:"upstream_status" gorm:"type:varchar(32)"`
	UpstreamRateMultiplier   *float64 `json:"upstream_rate_multiplier"`
	EffectiveRateMultiplier  *float64 `json:"effective_rate_multiplier"`
	UpstreamKeyID            string   `json:"upstream_key_id" gorm:"type:varchar(191)"`
	LocalChannelID           int      `json:"local_channel_id" gorm:"index"`
	SyncStatus               string   `json:"sync_status" gorm:"type:varchar(32);index"`
	LastError                string   `json:"last_error" gorm:"type:varchar(1024)"`
	LastDiscoveredAt         int64    `json:"last_discovered_at" gorm:"bigint"`
	LastSyncedAt             int64    `json:"last_synced_at" gorm:"bigint"`
	CreatedTime              int64    `json:"created_time" gorm:"bigint"`
	UpdatedTime              int64    `json:"updated_time" gorm:"bigint"`
}

func (mapping *UpstreamSourceChannelMapping) BeforeCreate(tx *gorm.DB) error {
	now := common.GetTimestamp()
	if mapping.CreatedTime == 0 {
		mapping.CreatedTime = now
	}
	if mapping.UpdatedTime == 0 {
		mapping.UpdatedTime = now
	}
	if mapping.DiscoveryStatus == "" {
		mapping.DiscoveryStatus = UpstreamMappingDiscoveryStatusActive
	}
	if mapping.SyncStatus == "" {
		mapping.SyncStatus = UpstreamMappingSyncStatusNeverSynced
	}
	return nil
}

func (mapping *UpstreamSourceChannelMapping) BeforeUpdate(tx *gorm.DB) error {
	mapping.UpdatedTime = common.GetTimestamp()
	return nil
}

func UpsertDiscoveredMappings(sourceID int, mappings []UpstreamSourceChannelMapping, now int64) error {
	return UpsertDiscoveredMappingsTx(DB, sourceID, mappings, now)
}

func UpsertDiscoveredMappingsTx(tx *gorm.DB, sourceID int, mappings []UpstreamSourceChannelMapping, now int64) error {
	if tx == nil {
		return errors.New("database transaction is required")
	}
	if sourceID == 0 {
		return errors.New("source ID is required")
	}
	if len(mappings) == 0 {
		return nil
	}
	discovered := make([]UpstreamSourceChannelMapping, 0, len(mappings))
	for _, mapping := range mappings {
		groupID := strings.TrimSpace(mapping.UpstreamGroupID)
		if groupID == "" {
			return errors.New("upstream group ID is required")
		}
		discoveryStatus := mapping.DiscoveryStatus
		if discoveryStatus == "" {
			discoveryStatus = UpstreamMappingDiscoveryStatusActive
		}
		discovered = append(discovered, UpstreamSourceChannelMapping{
			SourceID:                 sourceID,
			SyncEnabled:              mapping.SyncEnabled,
			UpstreamGroupID:          groupID,
			UpstreamGroupName:        mapping.UpstreamGroupName,
			UpstreamGroupDescription: mapping.UpstreamGroupDescription,
			UpstreamPlatform:         mapping.UpstreamPlatform,
			DiscoveryStatus:          discoveryStatus,
			UpstreamStatus:           mapping.UpstreamStatus,
			UpstreamRateMultiplier:   mapping.UpstreamRateMultiplier,
			EffectiveRateMultiplier:  mapping.EffectiveRateMultiplier,
			SyncStatus:               UpstreamMappingSyncStatusNeverSynced,
			LastDiscoveredAt:         now,
			CreatedTime:              now,
			UpdatedTime:              now,
		})
	}

	return tx.Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "source_id"},
			{Name: "upstream_group_id"},
		},
		DoUpdates: clause.AssignmentColumns([]string{
			"upstream_group_name",
			"upstream_group_description",
			"upstream_platform",
			"discovery_status",
			"upstream_status",
			"upstream_rate_multiplier",
			"effective_rate_multiplier",
			// NOTE: sync_enabled is intentionally NOT updated here — re-discovery
			// preserves the admin's per-mapping selection. Rule changes re-align
			// sync_enabled via RecomputeUpstreamSourceMappingSyncEligibility on
			// config save instead.
			"last_discovered_at",
			"updated_time",
		}),
	}).Create(&discovered).Error
}

func ClaimUpstreamSourceSync(sourceID int, token string, now int64, staleAfterSeconds int64) (bool, error) {
	if sourceID == 0 {
		return false, errors.New("source ID is required")
	}
	if strings.TrimSpace(token) == "" {
		return false, errors.New("sync token is required")
	}
	staleBefore := now - staleAfterSeconds
	result := DB.Model(&UpstreamSource{}).
		Where("id = ? AND (current_sync_token = ? OR current_sync_token IS NULL OR sync_started_at <= ?)", sourceID, "", staleBefore).
		Updates(map[string]interface{}{
			"current_sync_token": token,
			"sync_started_at":    now,
			"last_sync_status":   UpstreamSyncStatusRunning,
			"last_sync_error":    "",
			"updated_time":       now,
		})
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected == 1, nil
}

func ReleaseUpstreamSourceSync(sourceID int, token string, status string, errText string, now int64) error {
	if sourceID == 0 {
		return errors.New("source ID is required")
	}
	if strings.TrimSpace(token) == "" {
		return errors.New("sync token is required")
	}
	return DB.Model(&UpstreamSource{}).
		Where("id = ? AND current_sync_token = ?", sourceID, token).
		Updates(map[string]interface{}{
			"current_sync_token": "",
			"last_sync_status":   status,
			"last_sync_error":    errText,
			"last_sync_time":     now,
			"updated_time":       now,
		}).Error
}

// ClearUpstreamSourceTurnstileBlock clears LastDiscoveryError/LastSyncError
// when they still hold the given turnstile-blocked sentinel marker, so a
// successful session import (or any other resolution of the block) is
// reflected immediately instead of leaving turnstile_blocked stuck true in
// the response that confirms it to the admin.
func ClearUpstreamSourceTurnstileBlock(sourceID int, marker string) error {
	if sourceID == 0 {
		return errors.New("source ID is required")
	}
	now := common.GetTimestamp()
	if err := DB.Model(&UpstreamSource{}).
		Where("id = ? AND last_discovery_error = ?", sourceID, marker).
		Updates(map[string]interface{}{"last_discovery_error": "", "updated_time": now}).Error; err != nil {
		return err
	}
	return DB.Model(&UpstreamSource{}).
		Where("id = ? AND last_sync_error = ?", sourceID, marker).
		Updates(map[string]interface{}{"last_sync_error": "", "updated_time": now}).Error
}
