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

// makeFailureStateJSON returns a marshalled accountPoolFailureState for seeding
// account.FailureState in unit tests without going through the DB.
func makeFailureStateJSON(t *testing.T, s accountPoolFailureState) string {
	t.Helper()
	raw, err := s.marshal()
	require.NoError(t, err)
	return raw
}

func TestClassifyAccountPoolFailure(t *testing.T) {
	const now = int64(1000)
	baseAccount := model.AccountPoolAccount{
		Status: model.AccountPoolAccountStatusEnabled,
		// all cooldowns zero, empty FailureState
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
		isOAuth bool
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
			// 429 must NOT set temp_disabled_reason (review fix: last_error already records message)
			name:    "429 does not set temp_disabled_reason",
			account: baseAccount,
			err:     makeErr("too many requests", 429),
			check: func(t *testing.T, got map[string]any) {
				assert.NotContains(t, got, "temp_disabled_reason")
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
		// 5xx escalation tiering
		{
			// ConsecutiveFailures starts at 0; after increment=1, tier index=0 → 60s.
			name:    "500 first hit tier-0 60s",
			account: baseAccount,
			err:     makeErr("internal server error", 500),
			check: func(t *testing.T, got map[string]any) {
				assert.Equal(t, now+60, got["temp_disabled_until"])
				assert.NotContains(t, got, "status")
				// failure_state must carry incremented ConsecutiveFailures=1
				require.Contains(t, got, "failure_state")
				fs, err := parseAccountPoolFailureState(got["failure_state"].(string))
				require.NoError(t, err)
				assert.Equal(t, 1, fs.ConsecutiveFailures)
			},
		},
		{
			// Seed ConsecutiveFailures=1; after increment=2, tier index=1 → 300s.
			name: "500 second hit tier-1 300s",
			account: model.AccountPoolAccount{
				Status:       model.AccountPoolAccountStatusEnabled,
				FailureState: makeFailureStateJSON(t, accountPoolFailureState{ConsecutiveFailures: 1}),
			},
			err: makeErr("server error", 500),
			check: func(t *testing.T, got map[string]any) {
				assert.Equal(t, now+300, got["temp_disabled_until"])
				assert.NotContains(t, got, "status")
				require.Contains(t, got, "failure_state")
				fs, err := parseAccountPoolFailureState(got["failure_state"].(string))
				require.NoError(t, err)
				assert.Equal(t, 2, fs.ConsecutiveFailures)
			},
		},
		{
			// Seed ConsecutiveFailures=5; after increment=6, >=HardCapCount(6) → expired.
			name: "500 hard cap expired",
			account: model.AccountPoolAccount{
				Status:       model.AccountPoolAccountStatusEnabled,
				FailureState: makeFailureStateJSON(t, accountPoolFailureState{ConsecutiveFailures: 5}),
			},
			err: makeErr("server error", 500),
			check: func(t *testing.T, got map[string]any) {
				assert.Equal(t, model.AccountPoolAccountStatusExpired, got["status"])
				// cooldowns cleared on hard cap
				assert.Equal(t, int64(0), got["rate_limited_until"])
				assert.Equal(t, int64(0), got["temp_disabled_until"])
				assert.Equal(t, int64(0), got["overload_until"])
			},
		},
		{
			// 529 overload must NOT increment ConsecutiveFailures.
			// 529 must NOT set temp_disabled_reason (overload_until is its own axis;
			// last_error already records the message — consistent with 429 branch).
			name:    "529 overload does not increment ConsecutiveFailures",
			account: baseAccount,
			err:     makeErr("overloaded", 529),
			check: func(t *testing.T, got map[string]any) {
				assert.Equal(t, now+600, got["overload_until"])
				assert.NotContains(t, got, "status")
				assert.NotContains(t, got, "temp_disabled_until")
				assert.NotContains(t, got, "rate_limited_until")
				assert.NotContains(t, got, "temp_disabled_reason")
				// failure_state should not be written (or if written, ConsecutiveFailures=0)
				if fsRaw, ok := got["failure_state"]; ok {
					fs, err := parseAccountPoolFailureState(fsRaw.(string))
					require.NoError(t, err)
					assert.Equal(t, 0, fs.ConsecutiveFailures)
				}
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
		// 401 behavior: non-OAuth → immediate expire; OAuth → two-strike
		{
			name:    "401 non-OAuth immediate expire",
			account: baseAccount,
			isOAuth: false,
			err:     makeErr("unauthorized", 401),
			check: func(t *testing.T, got map[string]any) {
				assert.Equal(t, model.AccountPoolAccountStatusExpired, got["status"])
				assert.Equal(t, int64(0), got["rate_limited_until"])
				assert.Equal(t, int64(0), got["temp_disabled_until"])
				assert.Equal(t, int64(0), got["overload_until"])
			},
		},
		{
			// OAuth 401 first hit: cooldown only, not expired.
			name:    "401 OAuth first hit cooldown",
			account: baseAccount,
			isOAuth: true,
			err:     makeErr("unauthorized", 401),
			check: func(t *testing.T, got map[string]any) {
				assert.NotContains(t, got, "status")
				// temp_disabled_until = now + OAuth401CooldownMinutes*60 = 1000 + 600 = 1600
				assert.Equal(t, now+600, got["temp_disabled_until"])
				// failure_state must record Last401At
				require.Contains(t, got, "failure_state")
				fs, err := parseAccountPoolFailureState(got["failure_state"].(string))
				require.NoError(t, err)
				assert.Equal(t, now, fs.Last401At)
			},
		},
		{
			// OAuth 401 second hit within restrike window: expire.
			name: "401 OAuth second hit within window expires",
			account: model.AccountPoolAccount{
				Status:       model.AccountPoolAccountStatusEnabled,
				FailureState: makeFailureStateJSON(t, accountPoolFailureState{Last401At: now - 60}),
			},
			isOAuth: true,
			err:     makeErr("unauthorized", 401),
			check: func(t *testing.T, got map[string]any) {
				assert.Equal(t, model.AccountPoolAccountStatusExpired, got["status"])
				assert.Equal(t, int64(0), got["rate_limited_until"])
				assert.Equal(t, int64(0), got["temp_disabled_until"])
				assert.Equal(t, int64(0), got["overload_until"])
			},
		},
		// 403 three-strike behavior
		{
			// 403 first hit: opens new window, count=1, stays enabled with cooldown.
			name:    "403 first hit opens window cooldown",
			account: baseAccount,
			err:     makeErr("forbidden", 403),
			check: func(t *testing.T, got map[string]any) {
				assert.NotContains(t, got, "status")
				// temp_disabled_until = now + HTTP403CooldownMinutes*60 = 1000 + 600 = 1600
				assert.Equal(t, now+600, got["temp_disabled_until"])
				require.Contains(t, got, "failure_state")
				fs, err := parseAccountPoolFailureState(got["failure_state"].(string))
				require.NoError(t, err)
				assert.Equal(t, 1, fs.HTTP403Count)
				assert.Equal(t, now, fs.HTTP403WindowStart)
			},
		},
		{
			// 403 third hit within window: threshold reached → expire.
			name: "403 third hit within window expires",
			account: model.AccountPoolAccount{
				Status: model.AccountPoolAccountStatusEnabled,
				FailureState: makeFailureStateJSON(t, accountPoolFailureState{
					HTTP403Count:       2,
					HTTP403WindowStart: now,
				}),
			},
			err: makeErr("forbidden", 403),
			check: func(t *testing.T, got map[string]any) {
				assert.Equal(t, model.AccountPoolAccountStatusExpired, got["status"])
				assert.Equal(t, int64(0), got["rate_limited_until"])
				assert.Equal(t, int64(0), got["temp_disabled_until"])
				assert.Equal(t, int64(0), got["overload_until"])
			},
		},
		{
			// 403 after window expiry (old window start): resets count to 1, stays enabled.
			name: "403 after window expiry resets count",
			account: model.AccountPoolAccount{
				Status: model.AccountPoolAccountStatusEnabled,
				FailureState: makeFailureStateJSON(t, accountPoolFailureState{
					HTTP403Count:       2,
					HTTP403WindowStart: now - 200*60, // 200 minutes ago, outside 180-min window
				}),
			},
			err: makeErr("forbidden", 403),
			check: func(t *testing.T, got map[string]any) {
				assert.NotContains(t, got, "status")
				assert.Equal(t, now+600, got["temp_disabled_until"])
				require.Contains(t, got, "failure_state")
				fs, err := parseAccountPoolFailureState(got["failure_state"].(string))
				require.NoError(t, err)
				assert.Equal(t, 1, fs.HTTP403Count)
				assert.Equal(t, now, fs.HTTP403WindowStart)
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
			// 400 with "identity verification" phrase → expire.
			name: "400 identity verification phrase expires",
			account: baseAccount,
			err:  makeErr("identity verification required", 400),
			check: func(t *testing.T, got map[string]any) {
				assert.Equal(t, model.AccountPoolAccountStatusExpired, got["status"])
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
			// Out-of-range status code → temp disable with tier-0 60s.
			name:    "out-of-range status 600 temp disable",
			account: baseAccount,
			err:     makeErr("unknown error", 600),
			check: func(t *testing.T, got map[string]any) {
				assert.Equal(t, now+60, got["temp_disabled_until"])
				assert.NotContains(t, got, "status")
			},
		},
		{
			// Persistent transport errors use a flat 10-minute (600s) cooldown,
			// NOT the 5xx escalation tier ladder. First hit → now+600.
			name:    "network error persistent connection refused flat 10m",
			account: baseAccount,
			err:     makeNetworkErr("connection refused"),
			check: func(t *testing.T, got map[string]any) {
				assert.Equal(t, now+600, got["temp_disabled_until"])
				assert.NotContains(t, got, "status")
			},
		},
		{
			// Persistent transport error with ConsecutiveFailures already at 5 (one below hard cap).
			// After incrementing to 6 (>=HardCapCount=6) → status=expired, cooldowns cleared.
			name: "network error persistent hard cap ConsecutiveFailures=5 expires",
			account: model.AccountPoolAccount{
				Status:       model.AccountPoolAccountStatusEnabled,
				FailureState: makeFailureStateJSON(t, accountPoolFailureState{ConsecutiveFailures: 5}),
			},
			err: makeNetworkErr("connection refused"),
			check: func(t *testing.T, got map[string]any) {
				assert.Equal(t, model.AccountPoolAccountStatusExpired, got["status"])
				assert.Equal(t, int64(0), got["rate_limited_until"])
				assert.Equal(t, int64(0), got["temp_disabled_until"])
				assert.Equal(t, int64(0), got["overload_until"])
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
			// Persistent transport error increments ConsecutiveFailures (tiered).
			name:    "network error persistent increments ConsecutiveFailures",
			account: baseAccount,
			err:     makeNetworkErr("connection refused"),
			check: func(t *testing.T, got map[string]any) {
				require.Contains(t, got, "failure_state")
				fs, err := parseAccountPoolFailureState(got["failure_state"].(string))
				require.NoError(t, err)
				assert.Equal(t, 1, fs.ConsecutiveFailures)
			},
		},
		{
			// Transient transport error does NOT increment ConsecutiveFailures.
			name:    "network error transient does not increment ConsecutiveFailures",
			account: baseAccount,
			err:     makeNetworkErr("context deadline exceeded"),
			check: func(t *testing.T, got map[string]any) {
				// failure_state may or may not be present; if present, ConsecutiveFailures=0
				if fsRaw, ok := got["failure_state"]; ok {
					fs, err := parseAccountPoolFailureState(fsRaw.(string))
					require.NoError(t, err)
					assert.Equal(t, 0, fs.ConsecutiveFailures)
				}
			},
		},
		{
			// Monotonic: existing rate_limited_until larger than new → keep existing.
			name: "monotonic keeps larger existing rate_limited_until for 429",
			account: model.AccountPoolAccount{
				Status:          model.AccountPoolAccountStatusEnabled,
				RateLimitedUntil: now + 10000,
			},
			err: makeErr("too many requests", 429),
			check: func(t *testing.T, got map[string]any) {
				assert.Equal(t, now+10000, got["rate_limited_until"])
			},
		},
		{
			// Monotonic: existing overload_until larger than new → keep existing.
			name: "monotonic keeps larger existing overload_until for 529",
			account: model.AccountPoolAccount{
				Status:       model.AccountPoolAccountStatusEnabled,
				OverloadUntil: now + 10000,
			},
			err: makeErr("overloaded", 529),
			check: func(t *testing.T, got map[string]any) {
				assert.Equal(t, now+10000, got["overload_until"])
			},
		},
		{
			name: "monotonic keeps larger existing temp_disabled_until for 5xx",
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
			got := classifyAccountPoolFailure(tc.account, tc.err, tc.isOAuth, now)
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
		// Windows-specific phrasings
		{"windows actively refused is persistent", "target machine actively refused", true},
		{"windows no such host is known is persistent", "no such host is known", true},
		{"windows unreachable host is persistent", "unreachable host", true},
		{"windows socket unreachable network is persistent", "a socket operation was attempted to an unreachable network", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := types.NewError(errors.New(tc.msg), types.ErrorCodeDoRequestFailed)
			assert.Equal(t, tc.persistent, classifyTransportError(err))
		})
	}
}
