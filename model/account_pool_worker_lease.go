package model

import (
	"context"
	"errors"
	"math"
	"strings"
)

type AccountPoolWorkerLease struct {
	LeaseKey  string `json:"lease_key" gorm:"type:varchar(191);primaryKey"`
	OwnerID   string `json:"owner_id" gorm:"type:varchar(191);not null;index"`
	ExpiresAt int64  `json:"expires_at" gorm:"bigint;not null;index"`
	UpdatedAt int64  `json:"updated_at" gorm:"bigint;not null"`
}

func AcquireAccountPoolWorkerLease(ctx context.Context, leaseKey string, ownerID string, now int64, ttlSeconds int64) (bool, error) {
	leaseKey = strings.TrimSpace(leaseKey)
	ownerID = strings.TrimSpace(ownerID)
	if leaseKey == "" || ownerID == "" || now <= 0 || ttlSeconds <= 0 {
		return false, errors.New("account pool worker lease key, owner, time, and TTL are required")
	}
	if DB == nil {
		return false, errors.New("database is not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	expiresAt := accountPoolWorkerLeaseExpiry(now, ttlSeconds)
	db := DB.WithContext(ctx)
	result := db.Model(&AccountPoolWorkerLease{}).
		Where("lease_key = ? AND (owner_id = ? OR expires_at <= ?)", leaseKey, ownerID, now).
		Updates(map[string]any{
			"owner_id":   ownerID,
			"expires_at": expiresAt,
			"updated_at": now,
		})
	if result.Error != nil {
		return false, result.Error
	}
	if result.RowsAffected > 0 {
		return true, nil
	}

	lease := AccountPoolWorkerLease{
		LeaseKey:  leaseKey,
		OwnerID:   ownerID,
		ExpiresAt: expiresAt,
		UpdatedAt: now,
	}
	if err := db.Create(&lease).Error; err == nil {
		return true, nil
	}

	var existing AccountPoolWorkerLease
	if err := db.Select("owner_id", "expires_at").Where("lease_key = ?", leaseKey).Take(&existing).Error; err != nil {
		return false, err
	}
	return existing.OwnerID == ownerID && existing.ExpiresAt > now, nil
}

func RenewAccountPoolWorkerLease(ctx context.Context, leaseKey string, ownerID string, now int64, ttlSeconds int64) (bool, error) {
	leaseKey = strings.TrimSpace(leaseKey)
	ownerID = strings.TrimSpace(ownerID)
	if leaseKey == "" || ownerID == "" || now <= 0 || ttlSeconds <= 0 {
		return false, errors.New("account pool worker lease key, owner, time, and TTL are required")
	}
	if DB == nil {
		return false, errors.New("database is not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	result := DB.WithContext(ctx).Model(&AccountPoolWorkerLease{}).
		Where("lease_key = ? AND owner_id = ? AND expires_at > ?", leaseKey, ownerID, now).
		Updates(map[string]any{
			"expires_at": accountPoolWorkerLeaseExpiry(now, ttlSeconds),
			"updated_at": now,
		})
	return result.RowsAffected > 0, result.Error
}

func ReleaseAccountPoolWorkerLease(ctx context.Context, leaseKey string, ownerID string) (bool, error) {
	leaseKey = strings.TrimSpace(leaseKey)
	ownerID = strings.TrimSpace(ownerID)
	if leaseKey == "" || ownerID == "" {
		return false, errors.New("account pool worker lease key and owner are required")
	}
	if DB == nil {
		return false, errors.New("database is not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	result := DB.WithContext(ctx).
		Where("lease_key = ? AND owner_id = ?", leaseKey, ownerID).
		Delete(&AccountPoolWorkerLease{})
	return result.RowsAffected > 0, result.Error
}

func accountPoolWorkerLeaseExpiry(now int64, ttlSeconds int64) int64 {
	if now > math.MaxInt64-ttlSeconds {
		return math.MaxInt64
	}
	return now + ttlSeconds
}
