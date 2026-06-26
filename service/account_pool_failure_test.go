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
	require.NoError(t, model.DB.Model(&model.AccountPoolAccount{}).
		Where("id = ?", account.Id).
		Updates(map[string]any{
			"success_count": 7,
			"failure_count": 2,
		}).Error)
	err := types.NewErrorWithStatusCode(errors.New("authorization: bearer sk-secret-token-value"), types.ErrorCodeBadResponseStatusCode, http.StatusUnauthorized)

	require.NoError(t, RecordAccountPoolRuntimeAttemptFailure(account.Id, err, 1000))

	var reloaded model.AccountPoolAccount
	require.NoError(t, model.DB.First(&reloaded, account.Id).Error)
	assert.Equal(t, model.AccountPoolAccountStatusExpired, reloaded.Status)
	assert.Equal(t, int64(7), reloaded.SuccessCount)
	assert.Equal(t, int64(3), reloaded.FailureCount)
	assert.Equal(t, int64(1000), reloaded.LastFailureAt)
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
	assert.Equal(t, int64(1005), reloaded.RateLimitedUntil)
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

func TestClassifyAccountPoolFailure(t *testing.T) {
	const now = int64(1000)
	baseAccount := model.AccountPoolAccount{
		Status: model.AccountPoolAccountStatusEnabled,
		// all cooldowns zero
	}

	makeErr := func(msg string, code int) *types.NewAPIError {
		return types.NewErrorWithStatusCode(errors.New(msg), types.ErrorCodeBadResponseStatusCode, code)
	}
	makeErrWithUpstream := func(msg string, statusCode int, header http.Header, body []byte) *types.NewAPIError {
		e := types.NewErrorWithStatusCode(errors.New(msg), types.ErrorCodeBadResponseStatusCode, statusCode)
		e.SetUpstreamResponse(header, body, statusCode)
		return e
	}
	makeNetworkErr := func(msg string) *types.NewAPIError {
		return types.NewError(errors.New(msg), types.ErrorCodeDoRequestFailed)
	}

	tests := []struct {
		name    string
		account model.AccountPoolAccount
		err     *types.NewAPIError
		check   func(t *testing.T, got map[string]any)
	}{
		{
			name:    "base fields always present",
			account: baseAccount,
			err:     makeErr("some error", 404),
			check: func(t *testing.T, got map[string]any) {
				require.Contains(t, got, "last_error")
				require.Contains(t, got, "last_failure_at")
				assert.Equal(t, now, got["last_failure_at"])
				require.Contains(t, got, "failure_count")
			},
		},
		{
			name:    "429 codex header reset-after=30",
			account: baseAccount,
			err: func() *types.NewAPIError {
				h := http.Header{}
				h.Set("x-codex-primary-reset-after-seconds", "30")
				h.Set("x-codex-primary-used-percent", "100")
				return makeErrWithUpstream("rate limited", 429, h, nil)
			}(),
			check: func(t *testing.T, got map[string]any) {
				assert.Equal(t, now+30, got["rate_limited_until"])
				assert.NotContains(t, got, "status")
				assert.NotContains(t, got, "temp_disabled_until")
				assert.NotContains(t, got, "overload_until")
			},
		},
		{
			name:    "429 no header fallback",
			account: baseAccount,
			err:     makeErr("too many requests", 429),
			check: func(t *testing.T, got map[string]any) {
				assert.Equal(t, now+5, got["rate_limited_until"])
				assert.NotContains(t, got, "status")
			},
		},
		{
			name:    "408 request timeout",
			account: baseAccount,
			err:     makeErr("request timeout", 408),
			check: func(t *testing.T, got map[string]any) {
				assert.Equal(t, now+60, got["temp_disabled_until"])
				assert.NotContains(t, got, "status")
				assert.NotContains(t, got, "rate_limited_until")
			},
		},
		{
			name:    "500 server error",
			account: baseAccount,
			err:     makeErr("internal server error", 500),
			check: func(t *testing.T, got map[string]any) {
				assert.Equal(t, now+60, got["temp_disabled_until"])
				assert.NotContains(t, got, "status")
			},
		},
		{
			name:    "529 overload",
			account: baseAccount,
			err:     makeErr("overloaded", 529),
			check: func(t *testing.T, got map[string]any) {
				assert.Equal(t, now+600, got["overload_until"])
				assert.NotContains(t, got, "status")
				assert.NotContains(t, got, "temp_disabled_until")
				assert.NotContains(t, got, "rate_limited_until")
			},
		},
		{
			name:    "404 client error no cooldown",
			account: baseAccount,
			err:     makeErr("not found", 404),
			check: func(t *testing.T, got map[string]any) {
				assert.NotContains(t, got, "rate_limited_until")
				assert.NotContains(t, got, "temp_disabled_until")
				assert.NotContains(t, got, "overload_until")
				assert.NotContains(t, got, "status")
			},
		},
		{
			name:    "401 unauthorized",
			account: baseAccount,
			err:     makeErr("unauthorized", 401),
			check: func(t *testing.T, got map[string]any) {
				assert.Equal(t, model.AccountPoolAccountStatusExpired, got["status"])
				assert.Equal(t, int64(0), got["rate_limited_until"])
				assert.Equal(t, int64(0), got["temp_disabled_until"])
				assert.Equal(t, int64(0), got["overload_until"])
			},
		},
		{
			name:    "403 forbidden",
			account: baseAccount,
			err:     makeErr("forbidden", 403),
			check: func(t *testing.T, got map[string]any) {
				assert.Equal(t, model.AccountPoolAccountStatusExpired, got["status"])
			},
		},
		{
			name:    "400 organization disabled body",
			account: baseAccount,
			err: func() *types.NewAPIError {
				body := []byte(`{"error":{"message":"Your organization has been disabled"}}`)
				return makeErrWithUpstream("bad request", 400, nil, body)
			}(),
			check: func(t *testing.T, got map[string]any) {
				assert.Equal(t, model.AccountPoolAccountStatusExpired, got["status"])
				assert.Equal(t, int64(0), got["rate_limited_until"])
				assert.Equal(t, int64(0), got["temp_disabled_until"])
				assert.Equal(t, int64(0), got["overload_until"])
			},
		},
		{
			name:    "400 plain bad request",
			account: baseAccount,
			err:     makeErr("bad request body", 400),
			check: func(t *testing.T, got map[string]any) {
				assert.NotContains(t, got, "status")
				assert.NotContains(t, got, "rate_limited_until")
				assert.NotContains(t, got, "temp_disabled_until")
				assert.NotContains(t, got, "overload_until")
			},
		},
		{
			name:    "network error persistent connection refused",
			account: baseAccount,
			err:     makeNetworkErr("connection refused"),
			check: func(t *testing.T, got map[string]any) {
				assert.Equal(t, now+600, got["temp_disabled_until"])
				assert.NotContains(t, got, "status")
			},
		},
		{
			name:    "network error transient deadline exceeded",
			account: baseAccount,
			err:     makeNetworkErr("context deadline exceeded"),
			check: func(t *testing.T, got map[string]any) {
				assert.Equal(t, now+60, got["temp_disabled_until"])
				assert.NotContains(t, got, "status")
			},
		},
		{
			name: "monotonic keeps larger existing cooldown",
			account: model.AccountPoolAccount{
				Status:            model.AccountPoolAccountStatusEnabled,
				TempDisabledUntil: now + 10000,
			},
			err: makeErr("server error", 500),
			check: func(t *testing.T, got map[string]any) {
				assert.Equal(t, now+10000, got["temp_disabled_until"])
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyAccountPoolFailure(tc.account, tc.err, false, now)
			tc.check(t, got)
		})
	}
}

func TestClassifyTransportError(t *testing.T) {
	tests := []struct {
		name       string
		msg        string
		persistent bool
	}{
		{"connection refused is persistent", "dial tcp: connection refused", true},
		{"no route to host is persistent", "no route to host", true},
		{"network unreachable is persistent", "network is unreachable", true},
		{"no such host is persistent", "dial tcp: no such host", true},
		{"proxy auth required is persistent", "proxy authentication required", true},
		{"auth failed is persistent", "authentication failed", true},
		{"deadline exceeded is transient", "context deadline exceeded", false},
		{"eof is transient", "unexpected EOF", false},
		{"timeout is transient", "i/o timeout", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := types.NewError(errors.New(tc.msg), types.ErrorCodeDoRequestFailed)
			assert.Equal(t, tc.persistent, classifyTransportError(err))
		})
	}
}
