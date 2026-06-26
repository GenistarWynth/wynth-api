package service

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"gorm.io/gorm"
)

// IncrementAccountPoolAccountRequestQuota increments the request quota counter for the given
// account inside a DB transaction. When a rolling window is configured and the window has
// elapsed, the window is reset (start = now, used = 1) instead of just incrementing.
// Advisory races are acceptable here, consistent with the failure_state pattern.
func IncrementAccountPoolAccountRequestQuota(accountID int, now int64) error {
	if accountID <= 0 {
		return nil
	}
	if now <= 0 {
		now = common.GetTimestamp()
	}
	return model.DB.Transaction(func(tx *gorm.DB) error {
		var account model.AccountPoolAccount
		if err := tx.Select("id", "request_quota_window_start", "request_quota_window_seconds").
			First(&account, accountID).Error; err != nil {
			return err
		}
		windowElapsed := account.RequestQuotaWindowSeconds > 0 &&
			account.RequestQuotaWindowStart > 0 &&
			now >= account.RequestQuotaWindowStart+account.RequestQuotaWindowSeconds
		windowNotStarted := account.RequestQuotaWindowStart == 0

		if windowElapsed {
			// Window has elapsed: reset the window and start fresh.
			return tx.Model(&model.AccountPoolAccount{}).Where("id = ?", accountID).
				Updates(map[string]any{
					"request_quota_window_start": now,
					"request_quota_used":         int64(1),
				}).Error
		}
		if windowNotStarted {
			// First request: start the window.
			return tx.Model(&model.AccountPoolAccount{}).Where("id = ?", accountID).
				Updates(map[string]any{
					"request_quota_window_start": now,
					"request_quota_used":         int64(1),
				}).Error
		}
		// Normal case: increment the counter.
		return tx.Model(&model.AccountPoolAccount{}).Where("id = ?", accountID).
			Update("request_quota_used", gorm.Expr("request_quota_used + ?", 1)).Error
	})
}

func RecordAccountPoolRuntimeAttemptSuccess(accountID int, now int64) error {
	if accountID <= 0 {
		return nil
	}
	if now <= 0 {
		now = common.GetTimestamp()
	}
	// Clear the in-process fast-path block so a recovered account is immediately eligible.
	clearAccountPoolRuntimeBlock(accountID)
	return model.DB.Model(&model.AccountPoolAccount{}).
		Where("id = ? AND status = ?", accountID, model.AccountPoolAccountStatusEnabled).
		Updates(map[string]any{
			"last_used_at":         now,
			"last_success_at":      now,
			"success_count":        gorm.Expr("success_count + ?", 1),
			"rate_limited_until":   int64(0),
			"temp_disabled_until":  int64(0),
			"temp_disabled_reason": "",
			"last_error":           "",
			"overload_until":       int64(0),
			"failure_state":        "",
		}).Error
}
