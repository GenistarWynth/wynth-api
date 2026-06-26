package service

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"gorm.io/gorm"
)

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
