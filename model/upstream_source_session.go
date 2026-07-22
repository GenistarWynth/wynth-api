package model

import (
	"errors"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	UpstreamSourceAuthStatusUnknown  = "unknown"
	UpstreamSourceAuthStatusHealthy  = "healthy"
	UpstreamSourceAuthStatusExpiring = "expiring"
	UpstreamSourceAuthStatusExpired  = "expired"
	UpstreamSourceAuthStatusFailed   = "failed"
)

// UpstreamSourceSession stores replaceable, short-lived login material and
// its health independently from the source-owned connection and credentials.
// SessionConfig uses the same encrypted secret envelope as AuthConfig.
type UpstreamSourceSession struct {
	Id              int    `json:"id"`
	SourceID        int    `json:"source_id" gorm:"not null;uniqueIndex"`
	SessionConfig   string `json:"-" gorm:"type:text"`
	SessionSource   string `json:"session_source" gorm:"type:varchar(32)"`
	AuthStatus      string `json:"auth_status" gorm:"type:varchar(32);not null;index"`
	LastValidatedAt int64  `json:"last_validated_at" gorm:"bigint"`
	LastRefreshedAt int64  `json:"last_refreshed_at" gorm:"bigint"`
	ExpiresAt       int64  `json:"expires_at" gorm:"bigint;index"`
	LastAuthError   string `json:"last_auth_error" gorm:"type:varchar(1024)"`
	CreatedTime     int64  `json:"created_time" gorm:"bigint"`
	UpdatedTime     int64  `json:"updated_time" gorm:"bigint"`
}

func (session *UpstreamSourceSession) BeforeCreate(tx *gorm.DB) error {
	now := common.GetTimestamp()
	if session.AuthStatus == "" {
		session.AuthStatus = UpstreamSourceAuthStatusUnknown
	}
	if session.CreatedTime == 0 {
		session.CreatedTime = now
	}
	if session.UpdatedTime == 0 {
		session.UpdatedTime = now
	}
	return nil
}

func GetUpstreamSourceSession(sourceID int) (*UpstreamSourceSession, error) {
	if sourceID == 0 {
		return nil, errors.New("source ID is required")
	}
	var session UpstreamSourceSession
	result := DB.Where("source_id = ?", sourceID).Limit(1).Find(&session)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		return nil, nil
	}
	return &session, nil
}

func UpsertUpstreamSourceSessionTx(tx *gorm.DB, session *UpstreamSourceSession) error {
	if tx == nil {
		return errors.New("database transaction is required")
	}
	if session == nil || session.SourceID == 0 {
		return errors.New("upstream source session with source ID is required")
	}
	return tx.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "source_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"session_config",
			"session_source",
			"auth_status",
			"last_validated_at",
			"last_refreshed_at",
			"expires_at",
			"last_auth_error",
			"updated_time",
		}),
	}).Create(session).Error
}

func ClearUpstreamSourceSessionTx(tx *gorm.DB, sourceID int) error {
	if tx == nil {
		return errors.New("database transaction is required")
	}
	if sourceID == 0 {
		return errors.New("source ID is required")
	}
	return tx.Where("source_id = ?", sourceID).Delete(&UpstreamSourceSession{}).Error
}
