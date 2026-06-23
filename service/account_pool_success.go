package service

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

func RecordAccountPoolRuntimeAttemptSuccess(accountID int, now int64) error {
	if accountID <= 0 {
		return nil
	}
	if now <= 0 {
		now = common.GetTimestamp()
	}
	return model.DB.Model(&model.AccountPoolAccount{}).
		Where("id = ? AND status = ?", accountID, model.AccountPoolAccountStatusEnabled).
		Updates(map[string]any{
			"last_used_at":         now,
			"rate_limited_until":   int64(0),
			"temp_disabled_until":  int64(0),
			"temp_disabled_reason": "",
			"last_error":           "",
		}).Error
}
