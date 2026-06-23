package service

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRecordAccountPoolRuntimeAttemptFailureMarksAuthFailureExpired(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)
	account := createAccountPoolSchedulerAccount(t, service, pool.Id, AccountPoolAccountCreateParams{Name: "auth-failed"})
	err := types.NewErrorWithStatusCode(errors.New("authorization: bearer sk-secret-token-value"), types.ErrorCodeBadResponseStatusCode, http.StatusUnauthorized)

	require.NoError(t, RecordAccountPoolRuntimeAttemptFailure(account.Id, err, 1000))

	var reloaded model.AccountPoolAccount
	require.NoError(t, model.DB.First(&reloaded, account.Id).Error)
	assert.Equal(t, model.AccountPoolAccountStatusExpired, reloaded.Status)
	assert.Zero(t, reloaded.RateLimitedUntil)
	assert.Zero(t, reloaded.TempDisabledUntil)
	assert.Contains(t, reloaded.LastError, "status_code=401")
	assert.NotContains(t, reloaded.LastError, "sk-secret-token-value")
}

func TestRecordAccountPoolRuntimeAttemptFailureSetsRateLimitCooldown(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)
	account := createAccountPoolSchedulerAccount(t, service, pool.Id, AccountPoolAccountCreateParams{Name: "rate-limited"})
	err := types.NewErrorWithStatusCode(errors.New("too many requests"), types.ErrorCodeBadResponseStatusCode, http.StatusTooManyRequests)

	require.NoError(t, RecordAccountPoolRuntimeAttemptFailure(account.Id, err, 1000))

	var reloaded model.AccountPoolAccount
	require.NoError(t, model.DB.First(&reloaded, account.Id).Error)
	assert.Equal(t, model.AccountPoolAccountStatusEnabled, reloaded.Status)
	assert.Equal(t, int64(1060), reloaded.RateLimitedUntil)
	assert.Zero(t, reloaded.TempDisabledUntil)
	assert.Contains(t, reloaded.LastError, "too many requests")
}

func TestRecordAccountPoolRuntimeAttemptFailureSetsTemporaryDisableForServerError(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)
	account := createAccountPoolSchedulerAccount(t, service, pool.Id, AccountPoolAccountCreateParams{Name: "server-failed"})
	err := types.NewErrorWithStatusCode(errors.New("upstream unavailable"), types.ErrorCodeBadResponseStatusCode, http.StatusBadGateway)

	require.NoError(t, RecordAccountPoolRuntimeAttemptFailure(account.Id, err, 1000))

	var reloaded model.AccountPoolAccount
	require.NoError(t, model.DB.First(&reloaded, account.Id).Error)
	assert.Equal(t, model.AccountPoolAccountStatusEnabled, reloaded.Status)
	assert.Zero(t, reloaded.RateLimitedUntil)
	assert.Equal(t, int64(1060), reloaded.TempDisabledUntil)
	assert.Contains(t, reloaded.TempDisabledReason, "status_code=502")
	assert.Contains(t, reloaded.LastError, "upstream unavailable")
}

func TestRecordAccountPoolRuntimeAttemptFailureTruncatesLastError(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)
	account := createAccountPoolSchedulerAccount(t, service, pool.Id, AccountPoolAccountCreateParams{Name: "long-error"})
	err := types.NewErrorWithStatusCode(errors.New(strings.Repeat("x", 2000)), types.ErrorCodeBadResponseStatusCode, http.StatusInternalServerError)

	require.NoError(t, RecordAccountPoolRuntimeAttemptFailure(account.Id, err, 1000))

	var reloaded model.AccountPoolAccount
	require.NoError(t, model.DB.First(&reloaded, account.Id).Error)
	assert.LessOrEqual(t, len(reloaded.LastError), accountPoolLastErrorMaxLength)
	assert.LessOrEqual(t, len(reloaded.TempDisabledReason), accountPoolTempDisabledReasonMaxLength)
}

func TestRecordAccountPoolRuntimeAttemptFailureTruncatesUTF8Safely(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)
	account := createAccountPoolSchedulerAccount(t, service, pool.Id, AccountPoolAccountCreateParams{Name: "utf8-error"})
	err := types.NewErrorWithStatusCode(errors.New(strings.Repeat("上游错误", 400)), types.ErrorCodeBadResponseStatusCode, http.StatusInternalServerError)

	require.NoError(t, RecordAccountPoolRuntimeAttemptFailure(account.Id, err, 1000))

	var reloaded model.AccountPoolAccount
	require.NoError(t, model.DB.First(&reloaded, account.Id).Error)
	assert.LessOrEqual(t, len(reloaded.LastError), accountPoolLastErrorMaxLength)
	assert.True(t, utf8.ValidString(reloaded.LastError))
	assert.True(t, utf8.ValidString(reloaded.TempDisabledReason))
}

func TestRecordAccountPoolRuntimeAttemptFailureNoopsWithoutAccountOrError(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)
	account := createAccountPoolSchedulerAccount(t, service, pool.Id, AccountPoolAccountCreateParams{Name: "noop"})

	require.NoError(t, RecordAccountPoolRuntimeAttemptFailure(0, types.NewErrorWithStatusCode(errors.New("ignored"), types.ErrorCodeBadResponseStatusCode, http.StatusInternalServerError), 1000))
	require.NoError(t, RecordAccountPoolRuntimeAttemptFailure(account.Id, nil, 1000))

	var reloaded model.AccountPoolAccount
	require.NoError(t, model.DB.First(&reloaded, account.Id).Error)
	assert.Equal(t, model.AccountPoolAccountStatusEnabled, reloaded.Status)
	assert.Empty(t, reloaded.LastError)
	assert.Zero(t, reloaded.RateLimitedUntil)
	assert.Zero(t, reloaded.TempDisabledUntil)
}
