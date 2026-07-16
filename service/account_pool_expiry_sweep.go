package service

import (
	"context"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

// RunAccountPoolExpiryAutoPause flips every account that opted into
// auto_pause_on_expired and whose expires_at has passed from enabled → expired.
//
// Runtime selection already EXCLUDES such accounts via IsSchedulableAt, but it
// leaves their status as "enabled", so the field never truly "pauses" the account
// and the admin UI shows an expired account as healthy. This periodic sweep makes
// auto_pause_on_expired actually pause: the status flip is persistent and visible,
// and (like any non-enabled account) it stays paused until an admin re-enables it
// after renewing expiry. It is opt-in (auto_pause_on_expired defaults false) and a
// single bulk GORM update (portable across SQLite/MySQL/PostgreSQL).
//
// Returns the number of accounts paused.
func RunAccountPoolExpiryAutoPause(now int64) (int64, error) {
	return RunAccountPoolExpiryAutoPauseContext(context.Background(), now)
}

func RunAccountPoolExpiryAutoPauseContext(ctx context.Context, now int64) (int64, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if now <= 0 {
		now = common.GetTimestamp()
	}
	tx := model.DB.WithContext(ctx).Model(&model.AccountPoolAccount{}).
		Where("auto_pause_on_expired = ? AND expires_at > 0 AND expires_at <= ? AND status = ?",
			true, now, model.AccountPoolAccountStatusEnabled).
		Updates(map[string]any{
			"status":       model.AccountPoolAccountStatusExpired,
			"updated_time": now,
		})
	return tx.RowsAffected, tx.Error
}
