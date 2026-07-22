package model

import (
	"errors"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
)

const (
	UpstreamSourceCapabilityBalance           = "balance"
	UpstreamSourceCapabilityCost              = "cost"
	UpstreamSourceCapabilityRateGroup         = "rate_group"
	UpstreamSourceCapabilityAnnouncement      = "announcement"
	UpstreamSourceCapabilitySubscriptionUsage = "subscription_usage"

	UpstreamSourceCapabilityStatusSuccess     = "success"
	UpstreamSourceCapabilityStatusFailed      = "failed"
	UpstreamSourceCapabilityStatusUnsupported = "unsupported"
	UpstreamSourceCapabilityStatusSkipped     = "skipped"
)

// UpstreamSourceBalanceSnapshot is the current balance for one source. Its
// source-owned primary key makes updates replace the current value without
// growing an unbounded balance history table.
type UpstreamSourceBalanceSnapshot struct {
	SourceID    int     `json:"source_id" gorm:"primaryKey;autoIncrement:false"`
	ScanID      int     `json:"scan_id" gorm:"not null;index"`
	Available   float64 `json:"available" gorm:"not null"`
	Currency    string  `json:"currency" gorm:"type:varchar(32);not null"`
	CollectedAt int64   `json:"collected_at" gorm:"bigint;not null;index"`
}

func (snapshot *UpstreamSourceBalanceSnapshot) BeforeCreate(tx *gorm.DB) error {
	if snapshot.CollectedAt == 0 {
		snapshot.CollectedAt = common.GetTimestamp()
	}
	return nil
}

type UpstreamSourceCostSnapshot struct {
	Id          int     `json:"id"`
	SourceID    int     `json:"source_id" gorm:"not null;index:idx_upstream_source_cost_time,priority:1"`
	ScanID      int     `json:"scan_id" gorm:"not null;index"`
	Amount      float64 `json:"amount" gorm:"not null"`
	Currency    string  `json:"currency" gorm:"type:varchar(32);not null"`
	PeriodStart int64   `json:"period_start" gorm:"bigint;not null"`
	PeriodEnd   int64   `json:"period_end" gorm:"bigint;not null"`
	CollectedAt int64   `json:"collected_at" gorm:"bigint;not null;index:idx_upstream_source_cost_time,priority:2"`
}

func (snapshot *UpstreamSourceCostSnapshot) BeforeCreate(tx *gorm.DB) error {
	if snapshot.CollectedAt == 0 {
		snapshot.CollectedAt = common.GetTimestamp()
	}
	return nil
}

// UpstreamSourceAnnouncement uses a provider ID when available and a stable
// content hash otherwise. IsNew is a durable, Batch-C-consumable flag; rows
// inserted during the first successful baseline deliberately leave it false.
type UpstreamSourceAnnouncement struct {
	Id          int    `json:"id"`
	SourceID    int    `json:"source_id" gorm:"not null;uniqueIndex:idx_upstream_source_announcement_key,priority:1;index"`
	ScanID      int    `json:"scan_id" gorm:"not null;index"`
	SourceKey   string `json:"source_key" gorm:"type:varchar(191);not null;uniqueIndex:idx_upstream_source_announcement_key,priority:2"`
	Title       string `json:"title" gorm:"type:varchar(512)"`
	Content     string `json:"content" gorm:"type:text"`
	URL         string `json:"url" gorm:"type:varchar(2048)"`
	PublishedAt int64  `json:"published_at" gorm:"bigint"`
	FirstSeenAt int64  `json:"first_seen_at" gorm:"bigint;not null;index"`
	LastSeenAt  int64  `json:"last_seen_at" gorm:"bigint;not null;index"`
	IsNew       bool   `json:"is_new" gorm:"not null;index"`
}

type UpstreamSourceAnnouncementState struct {
	SourceID          int   `json:"source_id" gorm:"primaryKey;autoIncrement:false"`
	BaselineCompleted bool  `json:"baseline_completed" gorm:"not null"`
	CompletedAt       int64 `json:"completed_at" gorm:"bigint;not null"`
	UpdatedAt         int64 `json:"updated_at" gorm:"bigint;not null"`
}

// UpstreamSourceSubscriptionUsageSnapshot stores one raw usage window per
// subscription. Pointer limits/remaining values distinguish unavailable
// provider data from explicit zero values.
type UpstreamSourceSubscriptionUsageSnapshot struct {
	Id               int      `json:"id"`
	SourceID         int      `json:"source_id" gorm:"not null;index:idx_upstream_source_subscription_time,priority:1"`
	ScanID           int      `json:"scan_id" gorm:"not null;uniqueIndex:idx_upstream_source_subscription_window,priority:1;index"`
	SubscriptionKey  string   `json:"subscription_key" gorm:"type:varchar(191);not null;uniqueIndex:idx_upstream_source_subscription_window,priority:2"`
	Name             string   `json:"name" gorm:"type:varchar(512)"`
	Window           string   `json:"window" gorm:"type:varchar(32);not null;uniqueIndex:idx_upstream_source_subscription_window,priority:3"`
	Unit             string   `json:"unit" gorm:"type:varchar(32);not null"`
	Used             float64  `json:"used" gorm:"not null"`
	Limit            *float64 `json:"limit"`
	Remaining        *float64 `json:"remaining"`
	RemainingPercent *float64 `json:"remaining_percent"`
	PeriodStart      int64    `json:"period_start" gorm:"bigint"`
	PeriodEnd        int64    `json:"period_end" gorm:"bigint"`
	ExpiresAt        int64    `json:"expires_at" gorm:"bigint;index"`
	CollectedAt      int64    `json:"collected_at" gorm:"bigint;not null;index:idx_upstream_source_subscription_time,priority:2"`
	RawData          string   `json:"raw_data,omitempty" gorm:"type:text"`
}

func (snapshot *UpstreamSourceSubscriptionUsageSnapshot) BeforeCreate(tx *gorm.DB) error {
	if snapshot.CollectedAt == 0 {
		snapshot.CollectedAt = common.GetTimestamp()
	}
	return nil
}

// UpstreamSourceCapabilityOutcome gives each monitor scan a complete capability
// matrix, including unsupported and dependency-skipped collectors.
type UpstreamSourceCapabilityOutcome struct {
	Id           int    `json:"id"`
	SourceID     int    `json:"source_id" gorm:"not null;index:idx_upstream_source_capability_time,priority:1"`
	ScanID       int    `json:"scan_id" gorm:"not null;uniqueIndex:idx_upstream_source_scan_capability,priority:1;index"`
	Capability   string `json:"capability" gorm:"type:varchar(32);not null;uniqueIndex:idx_upstream_source_scan_capability,priority:2;index"`
	Status       string `json:"status" gorm:"type:varchar(32);not null;index"`
	Baseline     bool   `json:"baseline" gorm:"not null"`
	ItemCount    int    `json:"item_count" gorm:"not null"`
	StartedAt    int64  `json:"started_at" gorm:"bigint;not null"`
	FinishedAt   int64  `json:"finished_at" gorm:"bigint;not null;index:idx_upstream_source_capability_time,priority:2"`
	ErrorSummary string `json:"error_summary,omitempty" gorm:"type:varchar(1024)"`
}

func GetLatestUpstreamSourceBalance(sourceID int) (*UpstreamSourceBalanceSnapshot, error) {
	if sourceID == 0 {
		return nil, errors.New("source ID is required")
	}
	var snapshot UpstreamSourceBalanceSnapshot
	err := DB.Where("source_id = ?", sourceID).First(&snapshot).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &snapshot, err
}

func GetLatestUpstreamSourceCost(sourceID int) (*UpstreamSourceCostSnapshot, error) {
	if sourceID == 0 {
		return nil, errors.New("source ID is required")
	}
	var snapshot UpstreamSourceCostSnapshot
	err := DB.Where("source_id = ?", sourceID).Order("collected_at DESC, id DESC").First(&snapshot).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &snapshot, err
}

func ListLatestUpstreamSourceSubscriptionUsage(sourceID int) ([]UpstreamSourceSubscriptionUsageSnapshot, error) {
	if sourceID == 0 {
		return nil, errors.New("source ID is required")
	}
	var latest UpstreamSourceSubscriptionUsageSnapshot
	err := DB.Select("scan_id").Where("source_id = ?", sourceID).Order("collected_at DESC, id DESC").First(&latest).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return []UpstreamSourceSubscriptionUsageSnapshot{}, nil
	}
	if err != nil {
		return nil, err
	}
	windows := make([]UpstreamSourceSubscriptionUsageSnapshot, 0)
	err = DB.Where("source_id = ? AND scan_id = ?", sourceID, latest.ScanID).
		Order("subscription_key, window").Find(&windows).Error
	return windows, err
}

func ListRecentUpstreamSourceAnnouncements(sourceID int, limit int) ([]UpstreamSourceAnnouncement, error) {
	if sourceID == 0 {
		return nil, errors.New("source ID is required")
	}
	limit = normalizeUpstreamSourceHistoryLimit(limit)
	announcements := make([]UpstreamSourceAnnouncement, 0, limit)
	err := DB.Where("source_id = ?", sourceID).Order("first_seen_at DESC, id DESC").Limit(limit).Find(&announcements).Error
	return announcements, err
}

func ListRecentUpstreamSourceCapabilityOutcomes(sourceID int, limit int) ([]UpstreamSourceCapabilityOutcome, error) {
	if sourceID == 0 {
		return nil, errors.New("source ID is required")
	}
	limit = normalizeUpstreamSourceHistoryLimit(limit)
	outcomes := make([]UpstreamSourceCapabilityOutcome, 0, limit)
	err := DB.Where("source_id = ?", sourceID).Order("finished_at DESC, id DESC").Limit(limit).Find(&outcomes).Error
	return outcomes, err
}
