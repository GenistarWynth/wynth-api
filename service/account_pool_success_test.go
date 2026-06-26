package service

import (
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRecordAccountPoolRuntimeAttemptSuccessClearsTransientFailureState(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)
	account := createAccountPoolSchedulerAccount(t, service, pool.Id, AccountPoolAccountCreateParams{
		Name:               "successful-account",
		LastUsedAt:         100,
		RateLimitedUntil:   1200,
		TempDisabledUntil:  1300,
		TempDisabledReason: "previous temporary failure",
		LastError:          "previous error",
	})
	require.NoError(t, model.DB.Model(&model.AccountPoolAccount{}).
		Where("id = ?", account.Id).
		Updates(map[string]any{
			"success_count": 2,
			"failure_count": 5,
		}).Error)

	require.NoError(t, RecordAccountPoolRuntimeAttemptSuccess(account.Id, 2000))

	var reloaded model.AccountPoolAccount
	require.NoError(t, model.DB.First(&reloaded, account.Id).Error)
	assert.Equal(t, int64(2000), reloaded.LastUsedAt)
	assert.Equal(t, int64(2000), reloaded.LastSuccessAt)
	assert.Equal(t, int64(3), reloaded.SuccessCount)
	assert.Equal(t, int64(5), reloaded.FailureCount)
	assert.Zero(t, reloaded.RateLimitedUntil)
	assert.Zero(t, reloaded.TempDisabledUntil)
	assert.Empty(t, reloaded.TempDisabledReason)
	assert.Empty(t, reloaded.LastError)
	assert.Equal(t, model.AccountPoolAccountStatusEnabled, reloaded.Status)
}

func TestRecordAccountPoolRuntimeAttemptSuccessResetsFailureState(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)
	now := int64(3000)
	account := createAccountPoolSchedulerAccount(t, service, pool.Id, AccountPoolAccountCreateParams{
		Name:              "overloaded-account",
		TempDisabledUntil: now + 500,
	})
	require.NoError(t, model.DB.Model(&model.AccountPoolAccount{}).
		Where("id = ?", account.Id).
		Updates(map[string]any{
			"overload_until": now + 1000,
			"failure_state":  `{"consecutive_failures":3,"http403_count":2}`,
		}).Error)

	require.NoError(t, RecordAccountPoolRuntimeAttemptSuccess(account.Id, now))

	var reloaded model.AccountPoolAccount
	require.NoError(t, model.DB.First(&reloaded, account.Id).Error)
	assert.Zero(t, reloaded.OverloadUntil)
	assert.Empty(t, reloaded.FailureState)
	assert.Zero(t, reloaded.TempDisabledUntil)
	assert.Equal(t, int64(1), reloaded.SuccessCount)
}

func TestRecordAccountPoolRuntimeAttemptSuccessNoopsForInvalidOrNonEnabledAccount(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)
	account := createAccountPoolSchedulerAccount(t, service, pool.Id, AccountPoolAccountCreateParams{
		Name:               "expired-account",
		Status:             model.AccountPoolAccountStatusExpired,
		LastUsedAt:         100,
		RateLimitedUntil:   1200,
		TempDisabledUntil:  1300,
		TempDisabledReason: "previous temporary failure",
		LastError:          "previous error",
	})

	require.NoError(t, RecordAccountPoolRuntimeAttemptSuccess(0, 2000))
	require.NoError(t, RecordAccountPoolRuntimeAttemptSuccess(account.Id, 2000))

	var reloaded model.AccountPoolAccount
	require.NoError(t, model.DB.First(&reloaded, account.Id).Error)
	assert.Equal(t, int64(100), reloaded.LastUsedAt)
	assert.Equal(t, int64(1200), reloaded.RateLimitedUntil)
	assert.Equal(t, int64(1300), reloaded.TempDisabledUntil)
	assert.Equal(t, "previous temporary failure", reloaded.TempDisabledReason)
	assert.Equal(t, "previous error", reloaded.LastError)
	assert.Equal(t, model.AccountPoolAccountStatusExpired, reloaded.Status)
}
