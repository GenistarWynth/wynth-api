package model

import (
	"errors"
	"strings"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	UpstreamSourceNotificationEventAll                      = "*"
	UpstreamSourceNotificationEventGroupAdded               = "group_added"
	UpstreamSourceNotificationEventGroupRemoved             = "group_removed"
	UpstreamSourceNotificationEventGroupRestored            = "group_restored"
	UpstreamSourceNotificationEventRateChanged              = "rate_changed"
	UpstreamSourceNotificationEventAnnouncementNew          = "announcement_new"
	UpstreamSourceNotificationEventSubscriptionRemainingLow = "subscription_remaining_low"
	UpstreamSourceNotificationEventSubscriptionExpiring     = "subscription_expiring"
	UpstreamSourceNotificationEventBalanceLow               = "balance_low"
	UpstreamSourceNotificationEventRateGroupBatch           = "rate_group_batch"

	UpstreamSourceNotificationDeliverySuccess = "success"
	UpstreamSourceNotificationDeliveryFailed  = "failed"
)

type UpstreamSourceNotificationSubscription struct {
	Id              int    `json:"id"`
	UserID          int    `json:"user_id" gorm:"not null;uniqueIndex:idx_upstream_source_notification_rule,priority:1;index"`
	SourceID        int    `json:"source_id" gorm:"not null;uniqueIndex:idx_upstream_source_notification_rule,priority:2;index"`
	EventType       string `json:"event_type" gorm:"type:varchar(64);not null;uniqueIndex:idx_upstream_source_notification_rule,priority:3;index"`
	GroupID         string `json:"group_id" gorm:"type:varchar(191);not null;uniqueIndex:idx_upstream_source_notification_rule,priority:4"`
	Enabled         bool   `json:"enabled" gorm:"not null;index"`
	CooldownSeconds int64  `json:"cooldown_seconds" gorm:"bigint;not null"`
	CreatedAt       int64  `json:"created_at" gorm:"bigint;not null"`
	UpdatedAt       int64  `json:"updated_at" gorm:"bigint;not null"`
}

func (subscription *UpstreamSourceNotificationSubscription) BeforeCreate(tx *gorm.DB) error {
	now := common.GetTimestamp()
	if subscription.CreatedAt == 0 {
		subscription.CreatedAt = now
	}
	if subscription.UpdatedAt == 0 {
		subscription.UpdatedAt = now
	}
	if strings.TrimSpace(subscription.EventType) == "" {
		subscription.EventType = UpstreamSourceNotificationEventAll
	}
	return nil
}

func (subscription *UpstreamSourceNotificationSubscription) BeforeUpdate(tx *gorm.DB) error {
	subscription.UpdatedAt = common.GetTimestamp()
	return nil
}

func (subscription UpstreamSourceNotificationSubscription) Matches(sourceID int, eventType string, groupID string) bool {
	if !subscription.Enabled {
		return false
	}
	if subscription.SourceID != 0 && subscription.SourceID != sourceID {
		return false
	}
	if subscription.EventType != UpstreamSourceNotificationEventAll && subscription.EventType != eventType {
		return false
	}
	return subscription.GroupID == "" || subscription.GroupID == groupID
}

type UpstreamSourceNotificationCooldown struct {
	Id              int    `json:"id"`
	UserID          int    `json:"user_id" gorm:"not null;uniqueIndex:idx_upstream_source_notification_cooldown,priority:1;index"`
	SourceID        int    `json:"source_id" gorm:"not null;uniqueIndex:idx_upstream_source_notification_cooldown,priority:2;index"`
	EventType       string `json:"event_type" gorm:"type:varchar(64);not null;uniqueIndex:idx_upstream_source_notification_cooldown,priority:3"`
	GroupID         string `json:"group_id" gorm:"type:varchar(191);not null;uniqueIndex:idx_upstream_source_notification_cooldown,priority:4"`
	LastDeliveredAt int64  `json:"last_delivered_at" gorm:"bigint;not null;index"`
}

type UpstreamSourceNotificationCooldownKey struct {
	UserID    int
	SourceID  int
	EventType string
	GroupID   string
}

func IsUpstreamSourceNotificationCooldownReady(key UpstreamSourceNotificationCooldownKey, now int64, cooldownSeconds int64) (bool, error) {
	if key.UserID == 0 || key.SourceID == 0 || strings.TrimSpace(key.EventType) == "" {
		return false, errors.New("notification cooldown key is incomplete")
	}
	if cooldownSeconds <= 0 {
		return true, nil
	}
	var cooldown UpstreamSourceNotificationCooldown
	err := DB.Where("user_id = ? AND source_id = ? AND event_type = ? AND group_id = ?", key.UserID, key.SourceID, key.EventType, key.GroupID).First(&cooldown).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return cooldown.LastDeliveredAt+cooldownSeconds <= now, nil
}

func RecordUpstreamSourceNotificationCooldown(key UpstreamSourceNotificationCooldownKey, deliveredAt int64) error {
	if key.UserID == 0 || key.SourceID == 0 || strings.TrimSpace(key.EventType) == "" {
		return errors.New("notification cooldown key is incomplete")
	}
	cooldown := UpstreamSourceNotificationCooldown{
		UserID:          key.UserID,
		SourceID:        key.SourceID,
		EventType:       key.EventType,
		GroupID:         key.GroupID,
		LastDeliveredAt: deliveredAt,
	}
	return DB.Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "user_id"},
			{Name: "source_id"},
			{Name: "event_type"},
			{Name: "group_id"},
		},
		DoUpdates: clause.AssignmentColumns([]string{"last_delivered_at"}),
	}).Create(&cooldown).Error
}

type UpstreamSourceNotificationDelivery struct {
	Id           int    `json:"id"`
	UserID       int    `json:"user_id" gorm:"not null;index"`
	SourceID     int    `json:"source_id" gorm:"not null;index"`
	ScanID       int    `json:"scan_id" gorm:"not null;index"`
	EventType    string `json:"event_type" gorm:"type:varchar(64);not null;index"`
	EventKey     string `json:"event_key" gorm:"type:varchar(191);not null;index"`
	Status       string `json:"status" gorm:"type:varchar(32);not null;index"`
	Attempts     int    `json:"attempts" gorm:"not null"`
	ErrorSummary string `json:"error_summary,omitempty" gorm:"type:varchar(1024)"`
	CreatedAt    int64  `json:"created_at" gorm:"bigint;not null;index"`
}

func (delivery *UpstreamSourceNotificationDelivery) BeforeCreate(tx *gorm.DB) error {
	if delivery.CreatedAt == 0 {
		delivery.CreatedAt = common.GetTimestamp()
	}
	return nil
}
