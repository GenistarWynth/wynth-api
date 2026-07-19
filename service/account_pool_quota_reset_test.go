package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResetAccountLocalQuotaClearsLocalQuotaState(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	resetAccountPoolRuntimeBlocksForTest()
	now := time.Unix(2_000_000_000, 0).UTC()
	service := AccountPoolService{}
	pool, account := createAccountPoolXAIQuotaTestAccount(t, service, now)

	options, err := common.Marshal(AccountPoolRuntimeOptions{
		PoolMode: true,
		XAIQuota: &AccountPoolXAIQuotaSnapshot{
			Source:     "active_probe",
			StatusCode: 429,
			FetchedAt:  now.Add(-time.Minute).Unix(),
		},
	})
	require.NoError(t, err)
	require.NoError(t, model.DB.Model(&model.AccountPoolAccount{}).
		Where("id = ?", account.Id).
		Updates(map[string]any{
			"rate_limited_until":           now.Add(time.Hour).Unix(),
			"temp_disabled_until":          now.Add(time.Hour).Unix(),
			"temp_disabled_reason":         "quota exhausted by active probe",
			"last_error":                   "upstream quota exhausted",
			"request_quota_used":           int64(9),
			"request_quota_window_start":   now.Add(-time.Hour).Unix(),
			"request_quota_window_seconds": int64(3600),
			"runtime_options":              string(options),
		}).Error)
	blockAccountPoolRuntime(account.Id, now.Add(time.Hour).Unix())

	result, err := service.ResetAccountLocalQuota(context.Background(), AccountPoolLocalQuotaResetParams{
		PoolID:            pool.Id,
		AccountID:         account.Id,
		ClearCooldown:     true,
		ResetRequestQuota: true,
		Now:               now.Unix(),
	})
	require.NoError(t, err)
	assert.True(t, result.CooldownCleared)
	assert.True(t, result.RequestQuotaReset)
	assert.Nil(t, result.Probe)
	assert.Empty(t, result.ProbeError)

	stored, err := getAccountPoolAccountForPool(pool.Id, account.Id)
	require.NoError(t, err)
	assert.Zero(t, stored.RateLimitedUntil)
	assert.Zero(t, stored.TempDisabledUntil)
	assert.Empty(t, stored.TempDisabledReason)
	assert.Empty(t, stored.LastError)
	assert.Zero(t, stored.RequestQuotaUsed)
	assert.Equal(t, now.Unix(), stored.RequestQuotaWindowStart)
	assert.False(t, accountPoolRuntimeBlocked(account.Id, now.Unix()))

	storedOptions, err := parseAccountPoolRuntimeOptions(stored.RuntimeOptions)
	require.NoError(t, err)
	assert.True(t, storedOptions.PoolMode, "unrelated runtime options must be preserved")
	assert.Nil(t, storedOptions.XAIQuota, "stale exhausted observation must be cleared")
}

func TestResetAccountLocalQuotaPreservesNonQuotaTemporaryDisable(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)
	account := createAccountPoolSchedulerAccount(t, service, pool.Id, AccountPoolAccountCreateParams{Name: "auth-failure"})
	require.NoError(t, model.DB.Model(&model.AccountPoolAccount{}).
		Where("id = ?", account.Id).
		Updates(map[string]any{
			"rate_limited_until":   int64(200),
			"temp_disabled_until":  int64(300),
			"temp_disabled_reason": "invalid credential",
			"last_error":           "authentication failed",
		}).Error)

	_, err := service.ResetAccountLocalQuota(context.Background(), AccountPoolLocalQuotaResetParams{
		PoolID:        pool.Id,
		AccountID:     account.Id,
		ClearCooldown: true,
		Now:           100,
	})
	require.NoError(t, err)

	stored, err := getAccountPoolAccountForPool(pool.Id, account.Id)
	require.NoError(t, err)
	assert.Zero(t, stored.RateLimitedUntil)
	assert.Equal(t, int64(300), stored.TempDisabledUntil)
	assert.Equal(t, "invalid credential", stored.TempDisabledReason)
	assert.Equal(t, "authentication failed", stored.LastError)
}

func TestResetAccountLocalQuotaForceProbeIsPostCommitAndSecretSafe(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	now := time.Unix(2_000_000_000, 0).UTC()
	service := AccountPoolService{}
	pool, account := createAccountPoolXAIQuotaTestAccount(t, service, now)
	require.NoError(t, model.DB.Model(&model.AccountPoolAccount{}).
		Where("id = ?", account.Id).
		Update("rate_limited_until", now.Add(time.Hour).Unix()).Error)

	originalProbe := accountPoolLocalQuotaProbe
	t.Cleanup(func() { accountPoolLocalQuotaProbe = originalProbe })
	accountPoolLocalQuotaProbe = func(context.Context, int, int) (AccountPoolXAIQuotaSnapshot, error) {
		return AccountPoolXAIQuotaSnapshot{}, errors.New("probe failed for sso=super-secret-token")
	}

	result, err := service.ResetAccountLocalQuota(context.Background(), AccountPoolLocalQuotaResetParams{
		PoolID:        pool.Id,
		AccountID:     account.Id,
		ClearCooldown: true,
		ForceProbe:    true,
		Now:           now.Unix(),
	})
	require.NoError(t, err, "a post-commit probe failure must not hide a successful local reset")
	assert.Equal(t, "xai quota re-probe failed", result.ProbeError)
	assert.NotContains(t, result.ProbeError, "super-secret-token")

	stored, err := getAccountPoolAccountForPool(pool.Id, account.Id)
	require.NoError(t, err)
	assert.Zero(t, stored.RateLimitedUntil, "the local reset must remain committed")
}

func TestResetAccountLocalQuotaRejectsUnsupportedForceProbe(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)
	account := createAccountPoolSchedulerAccount(t, service, pool.Id, AccountPoolAccountCreateParams{Name: "openai"})

	_, err := service.ResetAccountLocalQuota(context.Background(), AccountPoolLocalQuotaResetParams{
		PoolID:     pool.Id,
		AccountID:  account.Id,
		ForceProbe: true,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "force probe")
}
