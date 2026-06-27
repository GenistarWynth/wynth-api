package service

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Only an enabled account that opted into auto_pause_on_expired AND whose
// expires_at has passed must be flipped to expired; every other combination is
// left untouched.
func TestRunAccountPoolExpiryAutoPause(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	svc := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, svc)
	now := common.GetTimestamp()

	mk := func(name, status string, expiresAt int64, autoPause bool) int {
		acct := model.AccountPoolAccount{
			PoolID:             pool.Id,
			Name:               name,
			Status:             status,
			ExpiresAt:          expiresAt,
			AutoPauseOnExpired: autoPause,
		}
		require.NoError(t, model.DB.Create(&acct).Error)
		return acct.Id
	}

	swept := mk("expired-autopause-enabled", model.AccountPoolAccountStatusEnabled, now-10, true)
	notAutoPause := mk("expired-no-autopause", model.AccountPoolAccountStatusEnabled, now-10, false)
	notExpired := mk("future-autopause", model.AccountPoolAccountStatusEnabled, now+1000, true)
	noExpiry := mk("no-expiry-autopause", model.AccountPoolAccountStatusEnabled, 0, true)
	alreadyDisabled := mk("expired-autopause-disabled", model.AccountPoolAccountStatusDisabled, now-10, true)

	count, err := RunAccountPoolExpiryAutoPause(now)
	require.NoError(t, err)
	assert.Equal(t, int64(1), count, "only the enabled+autopause+expired account is paused")

	assertStatus := func(id int, want string) {
		var a model.AccountPoolAccount
		require.NoError(t, model.DB.First(&a, id).Error)
		assert.Equal(t, want, a.Status)
	}
	assertStatus(swept, model.AccountPoolAccountStatusExpired)
	assertStatus(notAutoPause, model.AccountPoolAccountStatusEnabled)
	assertStatus(notExpired, model.AccountPoolAccountStatusEnabled)
	assertStatus(noExpiry, model.AccountPoolAccountStatusEnabled)
	assertStatus(alreadyDisabled, model.AccountPoolAccountStatusDisabled)
}
