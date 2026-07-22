package model

import (
	"errors"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
)

const (
	defaultUpstreamSourceHistoryLimit = 50
	maxUpstreamSourceHistoryLimit     = 200

	UpstreamSourceScanTypeDiscover  = "discover"
	UpstreamSourceScanTypeGroupRate = "group_rate"
	UpstreamSourceScanTypeMonitor   = "monitor"

	UpstreamSourceScanStatusRunning = "running"
	UpstreamSourceScanStatusSuccess = "success"
	UpstreamSourceScanStatusFailed  = "failed"
	UpstreamSourceScanStatusPartial = "partial"

	UpstreamSourceGroupChangeAdded       = "added"
	UpstreamSourceGroupChangeRemoved     = "removed"
	UpstreamSourceGroupChangeRestored    = "restored"
	UpstreamSourceGroupChangeRateChanged = "rate_changed"
)

// UpstreamSourceScan is a retained discovery batch. Rows are never deleted by
// the discovery path; only the running row's terminal fields are updated.
// FinishedAt is indexed so a future retention job can delete old batches in
// bounded chunks without scanning the whole table.
type UpstreamSourceScan struct {
	Id           int    `json:"id"`
	SourceID     int    `json:"source_id" gorm:"not null;index"`
	ScanType     string `json:"scan_type" gorm:"type:varchar(32);not null;index"`
	Status       string `json:"status" gorm:"type:varchar(32);not null;index"`
	Baseline     bool   `json:"baseline" gorm:"not null"`
	StartedAt    int64  `json:"started_at" gorm:"bigint;not null"`
	FinishedAt   int64  `json:"finished_at" gorm:"bigint;index"`
	ErrorSummary string `json:"error_summary,omitempty" gorm:"type:varchar(1024)"`
}

func (scan *UpstreamSourceScan) BeforeCreate(tx *gorm.DB) error {
	if scan.StartedAt == 0 {
		scan.StartedAt = common.GetTimestamp()
	}
	if scan.Status == "" {
		scan.Status = UpstreamSourceScanStatusRunning
	}
	return nil
}

// UpstreamSourceGroupChange is append-only audit history. CreatedAt is indexed
// to support a future retention job while source_id and scan_id serve reads.
type UpstreamSourceGroupChange struct {
	Id                         int      `json:"id"`
	SourceID                   int      `json:"source_id" gorm:"not null;index"`
	ScanID                     int      `json:"scan_id" gorm:"not null;index"`
	ChangeType                 string   `json:"change_type" gorm:"type:varchar(32);not null;index"`
	UpstreamGroupID            string   `json:"upstream_group_id" gorm:"type:varchar(191);not null;index"`
	UpstreamGroupName          string   `json:"upstream_group_name" gorm:"type:varchar(191)"`
	UpstreamGroupDescription   string   `json:"upstream_group_description" gorm:"type:varchar(512)"`
	UpstreamPlatform           string   `json:"upstream_platform" gorm:"type:varchar(64)"`
	OldRateMultiplier          *float64 `json:"old_rate_multiplier"`
	NewRateMultiplier          *float64 `json:"new_rate_multiplier"`
	OldEffectiveRateMultiplier *float64 `json:"old_effective_rate_multiplier"`
	NewEffectiveRateMultiplier *float64 `json:"new_effective_rate_multiplier"`
	CreatedAt                  int64    `json:"created_at" gorm:"bigint;not null;index"`
}

func (change *UpstreamSourceGroupChange) BeforeCreate(tx *gorm.DB) error {
	if change.CreatedAt == 0 {
		change.CreatedAt = common.GetTimestamp()
	}
	return nil
}

func CreateUpstreamSourceScan(sourceID int, scanType string, startedAt int64) (*UpstreamSourceScan, error) {
	if sourceID == 0 {
		return nil, errors.New("source ID is required")
	}
	if strings.TrimSpace(scanType) == "" {
		return nil, errors.New("scan type is required")
	}
	scan := UpstreamSourceScan{
		SourceID:  sourceID,
		ScanType:  scanType,
		Status:    UpstreamSourceScanStatusRunning,
		StartedAt: startedAt,
	}
	if err := DB.Create(&scan).Error; err != nil {
		return nil, err
	}
	return &scan, nil
}

func LockUpstreamSourceForScanTx(tx *gorm.DB, sourceID int) error {
	if tx == nil {
		return errors.New("database transaction is required")
	}
	var source UpstreamSource
	return lockForUpdate(tx).Select("id").Where("id = ?", sourceID).First(&source).Error
}

func HasSuccessfulUpstreamSourceScanTx(tx *gorm.DB, sourceID int, scanType string) (bool, error) {
	if tx == nil {
		return false, errors.New("database transaction is required")
	}
	var count int64
	err := tx.Model(&UpstreamSourceScan{}).
		Where("source_id = ? AND scan_type = ? AND status = ?", sourceID, scanType, UpstreamSourceScanStatusSuccess).
		Count(&count).Error
	return count > 0, err
}

func FinishUpstreamSourceScanTx(tx *gorm.DB, scanID int, status string, baseline bool, finishedAt int64, errorSummary string) error {
	if tx == nil {
		return errors.New("database transaction is required")
	}
	result := tx.Model(&UpstreamSourceScan{}).
		Where("id = ? AND status = ?", scanID, UpstreamSourceScanStatusRunning).
		Updates(map[string]interface{}{
			"status":        status,
			"baseline":      baseline,
			"finished_at":   finishedAt,
			"error_summary": errorSummary,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("upstream source scan %d is not running", scanID)
	}
	return nil
}

func ListRecentUpstreamSourceScans(sourceID int, limit int) ([]UpstreamSourceScan, error) {
	return ListRecentUpstreamSourceScansByType(sourceID, "", limit)
}

func ListRecentUpstreamSourceScansByType(sourceID int, scanType string, limit int) ([]UpstreamSourceScan, error) {
	if sourceID == 0 {
		return nil, errors.New("source ID is required")
	}
	limit = normalizeUpstreamSourceHistoryLimit(limit)
	scans := make([]UpstreamSourceScan, 0, limit)
	query := DB.Where("source_id = ?", sourceID)
	if strings.TrimSpace(scanType) != "" {
		query = query.Where("scan_type = ?", scanType)
	}
	err := query.Order("id DESC").Limit(limit).Find(&scans).Error
	return scans, err
}

func ListRecentUpstreamSourceGroupChanges(sourceID int, limit int) ([]UpstreamSourceGroupChange, error) {
	if sourceID == 0 {
		return nil, errors.New("source ID is required")
	}
	limit = normalizeUpstreamSourceHistoryLimit(limit)
	changes := make([]UpstreamSourceGroupChange, 0, limit)
	err := DB.Where("source_id = ?", sourceID).Order("id DESC").Limit(limit).Find(&changes).Error
	return changes, err
}

func normalizeUpstreamSourceHistoryLimit(limit int) int {
	if limit <= 0 {
		return defaultUpstreamSourceHistoryLimit
	}
	if limit > maxUpstreamSourceHistoryLimit {
		return maxUpstreamSourceHistoryLimit
	}
	return limit
}
