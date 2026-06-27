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

	require.NoError(t, RecordAccountPoolRuntimeAttemptSuccess(account.Id, 2000, ""))

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

	require.NoError(t, RecordAccountPoolRuntimeAttemptSuccess(account.Id, now, ""))

	var reloaded model.AccountPoolAccount
	require.NoError(t, model.DB.First(&reloaded, account.Id).Error)
	assert.Zero(t, reloaded.OverloadUntil)
	assert.Empty(t, reloaded.FailureState)
	assert.Zero(t, reloaded.TempDisabledUntil)
	assert.Equal(t, int64(1), reloaded.SuccessCount)
}

func TestIncrementAccountPoolAccountRequestQuotaFirstCallSetsWindowStart(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	svc := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, svc)
	account := createAccountPoolSchedulerAccount(t, svc, pool.Id, AccountPoolAccountCreateParams{
		Name: "quota-first",
	})
	now := int64(1_000_000)

	require.NoError(t, IncrementAccountPoolAccountRequestQuota(account.Id, now))

	var reloaded model.AccountPoolAccount
	require.NoError(t, model.DB.First(&reloaded, account.Id).Error)
	assert.Equal(t, now, reloaded.RequestQuotaWindowStart, "first call must set WindowStart to now")
	assert.Equal(t, int64(1), reloaded.RequestQuotaUsed, "first call must set Used=1")
}

func TestIncrementAccountPoolAccountRequestQuotaSubsequentCallsIncrement(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	svc := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, svc)
	account := createAccountPoolSchedulerAccount(t, svc, pool.Id, AccountPoolAccountCreateParams{
		Name: "quota-increment",
	})
	now := int64(1_000_000)

	require.NoError(t, IncrementAccountPoolAccountRequestQuota(account.Id, now))
	require.NoError(t, IncrementAccountPoolAccountRequestQuota(account.Id, now+1))
	require.NoError(t, IncrementAccountPoolAccountRequestQuota(account.Id, now+2))

	var reloaded model.AccountPoolAccount
	require.NoError(t, model.DB.First(&reloaded, account.Id).Error)
	assert.Equal(t, int64(3), reloaded.RequestQuotaUsed, "three increments must result in Used=3")
	assert.Equal(t, now, reloaded.RequestQuotaWindowStart, "WindowStart must not change on subsequent calls")
}

func TestIncrementAccountPoolAccountRequestQuotaWindowElapsedResetsToOne(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	svc := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, svc)
	account := createAccountPoolSchedulerAccount(t, svc, pool.Id, AccountPoolAccountCreateParams{
		Name: "quota-window-reset",
	})
	windowStart := int64(1_000_000)
	windowSeconds := int64(3600)

	// Manually set up a past window with some usage.
	require.NoError(t, model.DB.Model(&model.AccountPoolAccount{}).Where("id = ?", account.Id).
		Updates(map[string]any{
			"request_quota_window_start":   windowStart,
			"request_quota_window_seconds": windowSeconds,
			"request_quota_used":           int64(99),
		}).Error)

	// Increment at a time after the window has elapsed.
	now := windowStart + windowSeconds + 10
	require.NoError(t, IncrementAccountPoolAccountRequestQuota(account.Id, now))

	var reloaded model.AccountPoolAccount
	require.NoError(t, model.DB.First(&reloaded, account.Id).Error)
	assert.Equal(t, now, reloaded.RequestQuotaWindowStart, "elapsed window must reset WindowStart to now")
	assert.Equal(t, int64(1), reloaded.RequestQuotaUsed, "elapsed window must reset Used to 1")
}

func TestIncrementAccountPoolAccountRequestQuotaLifetimeNoWindowJustIncrements(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	svc := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, svc)
	account := createAccountPoolSchedulerAccount(t, svc, pool.Id, AccountPoolAccountCreateParams{
		Name: "quota-lifetime",
	})
	// Set WindowSeconds=0 (lifetime), with some existing usage and a window start already set.
	now := int64(5_000_000)
	require.NoError(t, model.DB.Model(&model.AccountPoolAccount{}).Where("id = ?", account.Id).
		Updates(map[string]any{
			"request_quota_window_start":   now - 1000,
			"request_quota_window_seconds": int64(0), // lifetime: no reset
			"request_quota_used":           int64(10),
		}).Error)

	require.NoError(t, IncrementAccountPoolAccountRequestQuota(account.Id, now))

	var reloaded model.AccountPoolAccount
	require.NoError(t, model.DB.First(&reloaded, account.Id).Error)
	// WindowSeconds==0 means the window-elapsed branch never fires; we fall into the
	// windowNotStarted==false path so Used just increments.
	assert.Equal(t, int64(11), reloaded.RequestQuotaUsed)
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

	require.NoError(t, RecordAccountPoolRuntimeAttemptSuccess(0, 2000, ""))
	require.NoError(t, RecordAccountPoolRuntimeAttemptSuccess(account.Id, 2000, ""))

	var reloaded model.AccountPoolAccount
	require.NoError(t, model.DB.First(&reloaded, account.Id).Error)
	assert.Equal(t, int64(100), reloaded.LastUsedAt)
	assert.Equal(t, int64(1200), reloaded.RateLimitedUntil)
	assert.Equal(t, int64(1300), reloaded.TempDisabledUntil)
	assert.Equal(t, "previous temporary failure", reloaded.TempDisabledReason)
	assert.Equal(t, "previous error", reloaded.LastError)
	assert.Equal(t, model.AccountPoolAccountStatusExpired, reloaded.Status)
}
