package service

import (
	"errors"
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestParseGemini429ResetAt verifies the Gemini-specific 429 reset parser.
func TestParseGemini429ResetAt(t *testing.T) {
	const now = int64(1_000_000)

	tests := []struct {
		name        string
		body        []byte
		wantResetAt int64
		wantOK      bool
	}{
		{
			// retryDelay "10s" in details array → now+10.
			name:        "retryDelay 10s in details",
			body:        []byte(`{"error":{"code":429,"message":"Resource exhausted","status":"RESOURCE_EXHAUSTED","details":[{"@type":"type.googleapis.com/google.rpc.RetryInfo","retryDelay":"10s"}]}}`),
			wantResetAt: now + 10,
			wantOK:      true,
		},
		{
			// quotaResetDelay "12.3s" → ceil(12.3)=13 → now+13.
			name:        "quotaResetDelay 12.3s ceil",
			body:        []byte(`{"error":{"code":429,"message":"Quota exceeded","status":"RESOURCE_EXHAUSTED","details":[{"metadata":{"quotaResetDelay":"12.3s"}}]}}`),
			wantResetAt: now + 13,
			wantOK:      true,
		},
		{
			// message "retry in 5.5s" → ceil(5.5)=6 → now+6.
			name:        "message retry in 5.5s",
			body:        []byte(`{"error":{"code":429,"message":"Please retry in 5.5s","status":"RESOURCE_EXHAUSTED","details":[]}}`),
			wantResetAt: now + 6,
			wantOK:      true,
		},
		{
			// Daily quota message → next Pacific midnight; assert in (now, now+25h].
			name: "daily quota message requests per day",
			body: []byte(`{"error":{"code":429,"message":"You exceeded your current quota, requests per day limit","status":"RESOURCE_EXHAUSTED","details":[]}}`),
			// wantOK only; wantResetAt checked in test body.
			wantOK: true,
		},
		{
			// Daily quota "per day" variant.
			name:   "daily quota message per day",
			body:   []byte(`{"error":{"code":429,"message":"Limit exceeded: 100 requests per day","status":"RESOURCE_EXHAUSTED","details":[]}}`),
			wantOK: true,
		},
		{
			// Machine-readable metric name 'generate_content_requests_per_day' → daily path.
			name:   "daily quota machine-readable generate_content_requests_per_day",
			body:   []byte(`{"error":{"code":429,"status":"RESOURCE_EXHAUSTED","message":"Quota exceeded for quota metric 'generate_content_requests_per_day'"}}`),
			wantOK: true,
		},
		{
			// Underscore form 'requests_per_day' without surrounding words → daily path.
			name:   "daily quota underscore form requests_per_day",
			body:   []byte(`{"error":{"code":429,"status":"RESOURCE_EXHAUSTED","message":"Quota limit 'requests_per_day' exceeded"}}`),
			wantOK: true,
		},
		{
			// Generic non-daily RESOURCE_EXHAUSTED: "Resource has been exhausted (e.g. check quota)"
			// must NOT route to PST-midnight — it has no day/per-day wording.
			name:        "non-daily resource exhausted generic message uses fallback not daily",
			body:        []byte(`{"error":{"code":429,"status":"RESOURCE_EXHAUSTED","message":"Resource has been exhausted (e.g. check quota)"}}`),
			wantResetAt: 0,
			wantOK:      false,
		},
		{
			// RESOURCE_EXHAUSTED with no delay/daily → (0, false).
			name:        "resource exhausted no delay no daily",
			body:        []byte(`{"error":{"code":429,"message":"Resource has been exhausted","status":"RESOURCE_EXHAUSTED","details":[]}}`),
			wantResetAt: 0,
			wantOK:      false,
		},
		{
			// Malformed JSON → (0, false), no panic.
			name:        "malformed body no panic",
			body:        []byte(`{not json`),
			wantResetAt: 0,
			wantOK:      false,
		},
		{
			// Empty body → (0, false).
			name:        "empty body",
			body:        []byte{},
			wantResetAt: 0,
			wantOK:      false,
		},
		{
			// retryDelay "1m30s" → 90s → now+90.
			name:        "retryDelay 1m30s",
			body:        []byte(`{"error":{"code":429,"message":"Resource exhausted","status":"RESOURCE_EXHAUSTED","details":[{"@type":"type.googleapis.com/google.rpc.RetryInfo","retryDelay":"1m30s"}]}}`),
			wantResetAt: now + 90,
			wantOK:      true,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			resetAt, ok := parseGemini429ResetAt(tc.body, now)
			assert.Equal(t, tc.wantOK, ok)
			isDailyCase := tc.name == "daily quota message requests per day" ||
				tc.name == "daily quota message per day" ||
				tc.name == "daily quota machine-readable generate_content_requests_per_day" ||
				tc.name == "daily quota underscore form requests_per_day"
			if isDailyCase {
				// Daily quota: only assert the reset is in the future and within 25h.
				if ok {
					assert.Greater(t, resetAt, now, "daily reset must be > now")
					assert.LessOrEqual(t, resetAt, now+25*3600, "daily reset must be within 25h of now")
				}
			} else {
				assert.Equal(t, tc.wantResetAt, resetAt)
			}
		})
	}
}

// TestNextGeminiDailyResetUnix verifies that the Pacific midnight helper returns
// a timestamp strictly in the future and no more than 24h ahead.
func TestNextGeminiDailyResetUnix(t *testing.T) {
	// Use a fixed unix timestamp: 2024-01-15 12:00:00 UTC = 1705320000.
	// Pacific time is UTC-8 (standard) or UTC-7 (DST); in January it's UTC-8.
	// So 12:00 UTC = 04:00 PST. Next midnight PST = 08:00 UTC same day → +28800s.
	const now = int64(1_705_320_000) // 2024-01-15 12:00:00 UTC
	next := nextGeminiDailyResetUnix(now)
	assert.Greater(t, next, now, "next midnight must be in the future")
	assert.LessOrEqual(t, next, now+int64(25*3600), "next midnight must be within 25h")
}

// TestClassifyAccountPoolFailureGemini verifies platform-gated Gemini classification.
func TestClassifyAccountPoolFailureGemini(t *testing.T) {
	const now = int64(1_000_000)
	baseAccount := model.AccountPoolAccount{
		Status: model.AccountPoolAccountStatusEnabled,
	}

	makeErrWithUpstream := func(msg string, statusCode int, header http.Header, body []byte) *types.NewAPIError {
		e := types.NewErrorWithStatusCode(errors.New(msg), types.ErrorCodeBadResponseStatusCode, statusCode)
		e.SetUpstreamResponse(header, body, statusCode)
		return e
	}
	makeErr := func(msg string, code int) *types.NewAPIError {
		return types.NewErrorWithStatusCode(errors.New(msg), types.ErrorCodeBadResponseStatusCode, code)
	}

	t.Run("gemini 429 with retryDelay body sets rate_limited_until", func(t *testing.T) {
		body := []byte(`{"error":{"code":429,"message":"Resource exhausted","status":"RESOURCE_EXHAUSTED","details":[{"@type":"type.googleapis.com/google.rpc.RetryInfo","retryDelay":"10s"}]}}`)
		err := makeErrWithUpstream("rate limited", 429, nil, body)

		got := classifyAccountPoolFailure(baseAccount, err, false, now, model.AccountPoolPlatformGemini)

		require.Contains(t, got, "rate_limited_until")
		assert.Equal(t, now+10, got["rate_limited_until"])
		assert.NotContains(t, got, "status")
		assert.NotContains(t, got, "temp_disabled_until")
	})

	t.Run("gemini 429 no parseable reset applies fallback", func(t *testing.T) {
		// No retryDelay, no daily quota phrasing → fallback (RateLimit429FallbackEnabled=true, seconds=5).
		err := makeErr("too many requests", 429)

		got := classifyAccountPoolFailure(baseAccount, err, false, now, model.AccountPoolPlatformGemini)

		require.Contains(t, got, "rate_limited_until", "Gemini 429 with no parseable reset must use fallback")
		assert.Equal(t, now+5, got["rate_limited_until"])
		assert.NotContains(t, got, "status")
	})

	t.Run("gemini 400 API key not valid expires account", func(t *testing.T) {
		body := []byte(`{"error":{"code":400,"message":"API key not valid. Please pass a valid API key.","status":"INVALID_ARGUMENT"}}`)
		err := makeErrWithUpstream("bad request", 400, nil, body)

		got := classifyAccountPoolFailure(baseAccount, err, false, now, model.AccountPoolPlatformGemini)

		assert.Equal(t, model.AccountPoolAccountStatusExpired, got["status"])
		assert.Equal(t, int64(0), got["rate_limited_until"])
		assert.Equal(t, int64(0), got["temp_disabled_until"])
		assert.Equal(t, int64(0), got["overload_until"])
	})

	t.Run("gemini 400 API_KEY_INVALID status expires account", func(t *testing.T) {
		body := []byte(`{"error":{"code":400,"message":"API_KEY_INVALID","status":"INVALID_ARGUMENT"}}`)
		err := makeErrWithUpstream("bad request", 400, nil, body)

		got := classifyAccountPoolFailure(baseAccount, err, false, now, model.AccountPoolPlatformGemini)

		assert.Equal(t, model.AccountPoolAccountStatusExpired, got["status"])
	})

	t.Run("gemini 403 PERMISSION_DENIED in body expires account via 400 phrase", func(t *testing.T) {
		// Gemini sometimes returns 400 with PERMISSION_DENIED for invalid keys.
		body := []byte(`{"error":{"code":400,"message":"PERMISSION_DENIED: API key does not have access","status":"PERMISSION_DENIED"}}`)
		err := makeErrWithUpstream("bad request", 400, nil, body)

		got := classifyAccountPoolFailure(baseAccount, err, false, now, model.AccountPoolPlatformGemini)

		assert.Equal(t, model.AccountPoolAccountStatusExpired, got["status"])
	})

	t.Run("gemini 400 API key expired expires account", func(t *testing.T) {
		err := makeErr("API key expired", 400)

		got := classifyAccountPoolFailure(baseAccount, err, false, now, model.AccountPoolPlatformGemini)

		assert.Equal(t, model.AccountPoolAccountStatusExpired, got["status"])
	})

	t.Run("gemini 400 plain bad request does not expire", func(t *testing.T) {
		err := makeErr("invalid request body", 400)

		got := classifyAccountPoolFailure(baseAccount, err, false, now, model.AccountPoolPlatformGemini)

		assert.NotContains(t, got, "status")
	})
}

// TestClassifyAccountPoolFailureGeminiPlatformIsolation verifies that Gemini-specific
// phrases do NOT affect OpenAI or Anthropic classification.
func TestClassifyAccountPoolFailureGeminiPlatformIsolation(t *testing.T) {
	const now = int64(1_000_000)
	baseAccount := model.AccountPoolAccount{
		Status: model.AccountPoolAccountStatusEnabled,
	}

	makeErr := func(msg string, code int) *types.NewAPIError {
		return types.NewErrorWithStatusCode(errors.New(msg), types.ErrorCodeBadResponseStatusCode, code)
	}

	// "API key not valid" must NOT expire an OpenAI account.
	t.Run("openai 400 API key not valid does not expire", func(t *testing.T) {
		err := makeErr("API key not valid", 400)
		got := classifyAccountPoolFailure(baseAccount, err, false, now, "openai")
		assert.NotContains(t, got, "status")
	})

	// "API key not valid" must NOT expire an Anthropic account.
	t.Run("anthropic 400 API key not valid does not expire", func(t *testing.T) {
		err := makeErr("API key not valid", 400)
		got := classifyAccountPoolFailure(baseAccount, err, false, now, "anthropic")
		assert.NotContains(t, got, "status")
	})

	// OpenAI 429 fallback still works with Gemini parser in place.
	t.Run("openai 429 fallback unchanged after gemini added", func(t *testing.T) {
		err := makeErr("too many requests", 429)
		got := classifyAccountPoolFailure(baseAccount, err, false, now, "openai")
		assert.Equal(t, now+5, got["rate_limited_until"])
	})

	// Anthropic 429 with no header still does NOT apply fallback.
	t.Run("anthropic 429 no header no fallback unchanged", func(t *testing.T) {
		err := makeErr("too many requests", 429)
		got := classifyAccountPoolFailure(baseAccount, err, false, now, "anthropic")
		assert.NotContains(t, got, "rate_limited_until")
	})
}
