package service

import (
	"errors"
	"net/http"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeAnthropicHeader builds an http.Header using .Set() so keys are
// canonicalized exactly as they would be when received from a real HTTP response.
// Pairs must alternate key, value, key, value, ...
func makeAnthropicHeader(pairs ...string) http.Header {
	h := http.Header{}
	for i := 0; i+1 < len(pairs); i += 2 {
		h.Set(pairs[i], pairs[i+1])
	}
	return h
}

// TestParseAnthropic429ResetAt verifies the per-window reset parser for Anthropic rate-limit headers.
func TestParseAnthropic429ResetAt(t *testing.T) {
	const now = int64(1_000_000)

	tests := []struct {
		name        string
		header      http.Header
		wantResetAt int64
		wantOK      bool
	}{
		{
			// 5h window exhausted via surpassed-threshold=true, 5h reset header present.
			name:        "5h exhausted via surpassed-threshold=true uses 5h reset",
			header:      makeAnthropicHeader("Anthropic-Ratelimit-Unified-5H-Surpassed-Threshold", "true", "Anthropic-Ratelimit-Unified-5H-Reset", "1001000"),
			wantResetAt: 1001000,
			wantOK:      true,
		},
		{
			// 5h window exhausted via utilization >= 1.0.
			name:        "5h exhausted via utilization>=1.0 uses 5h reset",
			header:      makeAnthropicHeader("Anthropic-Ratelimit-Unified-5H-Utilization", "1.0", "Anthropic-Ratelimit-Unified-5H-Reset", "1002000"),
			wantResetAt: 1002000,
			wantOK:      true,
		},
		{
			// Both 5h and 7d exhausted → use 7d reset (longer cooldown).
			name: "both 5h and 7d exhausted uses 7d reset",
			header: makeAnthropicHeader(
				"Anthropic-Ratelimit-Unified-5H-Surpassed-Threshold", "true",
				"Anthropic-Ratelimit-Unified-5H-Reset", "1001000",
				"Anthropic-Ratelimit-Unified-7D-Surpassed-Threshold", "true",
				"Anthropic-Ratelimit-Unified-7D-Reset", "1050000",
			),
			wantResetAt: 1050000,
			wantOK:      true,
		},
		{
			// Only 7d exhausted (5h not exhausted) → use 7d reset.
			name:        "only 7d exhausted uses 7d reset",
			header:      makeAnthropicHeader("Anthropic-Ratelimit-Unified-7D-Surpassed-Threshold", "true", "Anthropic-Ratelimit-Unified-7D-Reset", "1060000"),
			wantResetAt: 1060000,
			wantOK:      true,
		},
		{
			// utilization=1.0 exactly triggers exhausted (boundary condition).
			name:        "utilization exactly 1.0 triggers exhausted",
			header:      makeAnthropicHeader("Anthropic-Ratelimit-Unified-5H-Utilization", "1.0", "Anthropic-Ratelimit-Unified-5H-Reset", "1003000"),
			wantResetAt: 1003000,
			wantOK:      true,
		},
		{
			// utilization < 1.0 (just below threshold) does NOT trigger exhausted.
			// Not exhausted, but reset present → sooner of present resets (only 5h here).
			name:        "utilization 0.99 does not trigger exhausted but reset present uses sooner",
			header:      makeAnthropicHeader("Anthropic-Ratelimit-Unified-5H-Utilization", "0.99", "Anthropic-Ratelimit-Unified-5H-Reset", "1004000"),
			wantResetAt: 1004000,
			wantOK:      true,
		},
		// Note: the ms-value test for 5h reset is done as a standalone subtest below
		// (it requires a realistic unix "now" so the max-age guard passes after division).

		{
			// 5h reset more than 6h in the future → ignored (treat as absent).
			// now=1_000_000; 6h = 21600s; limit = now+21600 = 1_021_600.
			// reset = 1_100_000 (>1_021_600) → ignored. No other headers → (0, false).
			name:        "5h reset beyond 6h max-age ignored",
			header:      makeAnthropicHeader("Anthropic-Ratelimit-Unified-5H-Surpassed-Threshold", "true", "Anthropic-Ratelimit-Unified-5H-Reset", "1100000"),
			wantResetAt: 0,
			wantOK:      false,
		},
		{
			// 7d reset more than 8 days in the future → ignored.
			// 8 days = 691200s; limit = now+691200 = 1_691_200.
			// reset = 2_000_000 (>1_691_200) → ignored.
			name:        "7d reset beyond 8-day max-age ignored",
			header:      makeAnthropicHeader("Anthropic-Ratelimit-Unified-7D-Surpassed-Threshold", "true", "Anthropic-Ratelimit-Unified-7D-Reset", "2000000"),
			wantResetAt: 0,
			wantOK:      false,
		},
		{
			// No anthropic headers → (0, false).
			name:        "no anthropic headers returns false",
			header:      http.Header{},
			wantResetAt: 0,
			wantOK:      false,
		},
		{
			// anthropic-ratelimit-unified-reset aggregated header fallback.
			name:        "aggregated unified-reset fallback",
			header:      makeAnthropicHeader("Anthropic-Ratelimit-Unified-Reset", "1010000"),
			wantResetAt: 1010000,
			wantOK:      true,
		},
		// Note: the ms-value test for aggregated unified-reset is done as a standalone subtest below.

		{
			// Retry-After integer seconds → now+seconds.
			name:        "Retry-After fallback",
			header:      makeAnthropicHeader("Retry-After", "3600"),
			wantResetAt: now + 3600,
			wantOK:      true,
		},
		{
			// 5h status == "rejected" also counts as 5h exhausted.
			name:        "5h-status=rejected triggers exhausted",
			header:      makeAnthropicHeader("Anthropic-Ratelimit-Unified-5H-Status", "rejected", "Anthropic-Ratelimit-Unified-5H-Reset", "1005000"),
			wantResetAt: 1005000,
			wantOK:      true,
		},
		{
			// Malformed reset value → does not panic, returns false.
			name:        "malformed reset value does not panic",
			header:      makeAnthropicHeader("Anthropic-Ratelimit-Unified-5H-Surpassed-Threshold", "true", "Anthropic-Ratelimit-Unified-5H-Reset", "not-a-number"),
			wantResetAt: 0,
			wantOK:      false,
		},
		{
			// Case-insensitive surpassed-threshold "TRUE".
			name:        "surpassed-threshold TRUE case-insensitive",
			header:      makeAnthropicHeader("Anthropic-Ratelimit-Unified-5H-Surpassed-Threshold", "TRUE", "Anthropic-Ratelimit-Unified-5H-Reset", "1006000"),
			wantResetAt: 1006000,
			wantOK:      true,
		},
		{
			// Neither window exhausted; both resets present → sooner of the two.
			name:        "neither exhausted both resets present uses sooner",
			header:      makeAnthropicHeader("Anthropic-Ratelimit-Unified-5H-Reset", "1001000", "Anthropic-Ratelimit-Unified-7D-Reset", "1005000"),
			wantResetAt: 1001000,
			wantOK:      true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resetAt, ok := parseAnthropic429ResetAt(tc.header, now)
			assert.Equal(t, tc.wantOK, ok, "ok mismatch")
			assert.Equal(t, tc.wantResetAt, resetAt, "resetAt mismatch")
		})
	}

	// The ms-division tests require a realistic unix "now" so that the max-age guard
	// passes after dividing by 1000. Use a real-ish epoch (Jan 2024).
	t.Run("5h reset in milliseconds divided by 1000", func(t *testing.T) {
		// nowReal = 1_700_000_000 (Nov 2023). Reset = now+1000s in seconds = 1_700_001_000.
		// As ms: 1_700_001_000_000 (>1e11). After /1000 = 1_700_001_000.
		// Max-age guard: 1_700_001_000 <= 1_700_000_000 + 21600 → passes.
		const nowReal = int64(1_700_000_000)
		h := makeAnthropicHeader(
			"Anthropic-Ratelimit-Unified-5H-Surpassed-Threshold", "true",
			"Anthropic-Ratelimit-Unified-5H-Reset", "1700001000000",
		)
		resetAt, ok := parseAnthropic429ResetAt(h, nowReal)
		assert.True(t, ok, "ok")
		assert.Equal(t, int64(1_700_001_000), resetAt, "resetAt should be ms/1000")
	})

	t.Run("aggregated unified-reset in ms divided by 1000", func(t *testing.T) {
		// nowReal = 1_700_000_000. Reset = 1_700_001_000 in ms = 1_700_001_000_000.
		const nowReal = int64(1_700_000_000)
		h := makeAnthropicHeader("Anthropic-Ratelimit-Unified-Reset", "1700001000000")
		resetAt, ok := parseAnthropic429ResetAt(h, nowReal)
		assert.True(t, ok, "ok")
		assert.Equal(t, int64(1_700_001_000), resetAt, "resetAt should be ms/1000")
	})

	// --- Both-exhausted edge cases (use realistic epoch so max-age guards work) ---
	// now = 1_700_000_000 (Nov 2023). 5h reset at now+3600 = 1_700_003_600 (within 6h).
	// 7d reset at now+86400 = 1_700_086_400 (within 8d).
	const nowBoth = int64(1_700_000_000)
	const reset5hVal = int64(1_700_003_600) // now + 1h (within 6h max-age guard)
	const reset7dVal = int64(1_700_086_400) // now + 1d (within 8d max-age guard)
	// beyond8d = now + 9 days — exceeds 8-day max-age guard.
	const beyond8dVal = int64(1_700_000_000 + 9*86400) // 1_700_777_600

	t.Run("both exhausted 7d reset present uses 7d reset", func(t *testing.T) {
		// Regression: both exhausted, both resets present → 7d (not 5h).
		h := makeAnthropicHeader(
			"Anthropic-Ratelimit-Unified-5H-Surpassed-Threshold", "true",
			"Anthropic-Ratelimit-Unified-5H-Reset", strconv.FormatInt(reset5hVal, 10),
			"Anthropic-Ratelimit-Unified-7D-Surpassed-Threshold", "true",
			"Anthropic-Ratelimit-Unified-7D-Reset", strconv.FormatInt(reset7dVal, 10),
		)
		resetAt, ok := parseAnthropic429ResetAt(h, nowBoth)
		assert.True(t, ok, "ok")
		assert.Equal(t, reset7dVal, resetAt, "both exhausted + both resets → 7d reset")
	})

	t.Run("both exhausted 7d reset absent aggregated header also absent returns false", func(t *testing.T) {
		// Both windows exhausted but 7d reset header is missing entirely.
		// 5h reset IS present. No aggregated header, no Retry-After.
		// Must NOT return now+5h — must return (0, false).
		h := makeAnthropicHeader(
			"Anthropic-Ratelimit-Unified-5H-Surpassed-Threshold", "true",
			"Anthropic-Ratelimit-Unified-5H-Reset", strconv.FormatInt(reset5hVal, 10),
			"Anthropic-Ratelimit-Unified-7D-Surpassed-Threshold", "true",
			// No 7D-Reset header.
		)
		resetAt, ok := parseAnthropic429ResetAt(h, nowBoth)
		assert.False(t, ok, "both exhausted with no usable 7d reset and no aggregated header must return false")
		assert.Equal(t, int64(0), resetAt, "resetAt must be 0 (not now+5h under-cool)")
	})

	t.Run("both exhausted 7d reset beyond 8d guard aggregated header used", func(t *testing.T) {
		// Both exhausted. 7d reset is present but beyond the 8-day max-age guard → filtered.
		// Aggregated header IS present and usable → outer fallback should use it.
		const aggregatedReset = int64(1_700_007_200) // now+2h, a plausible aggregated value
		h := makeAnthropicHeader(
			"Anthropic-Ratelimit-Unified-5H-Surpassed-Threshold", "true",
			"Anthropic-Ratelimit-Unified-5H-Reset", strconv.FormatInt(reset5hVal, 10),
			"Anthropic-Ratelimit-Unified-7D-Surpassed-Threshold", "true",
			"Anthropic-Ratelimit-Unified-7D-Reset", strconv.FormatInt(beyond8dVal, 10),
			"Anthropic-Ratelimit-Unified-Reset", strconv.FormatInt(aggregatedReset, 10),
		)
		resetAt, ok := parseAnthropic429ResetAt(h, nowBoth)
		assert.True(t, ok, "ok — aggregated header used as outer fallback")
		assert.Equal(t, aggregatedReset, resetAt, "both exhausted, 7d beyond guard → aggregated reset used (not 5h)")
	})

	t.Run("both exhausted 7d absent retry-after used as outer fallback", func(t *testing.T) {
		// Both exhausted. 7d reset absent. No aggregated header. Retry-After: 120 present.
		// Must use now+120 from outer fallback, NOT now+5h.
		h := makeAnthropicHeader(
			"Anthropic-Ratelimit-Unified-5H-Surpassed-Threshold", "true",
			"Anthropic-Ratelimit-Unified-5H-Reset", strconv.FormatInt(reset5hVal, 10),
			"Anthropic-Ratelimit-Unified-7D-Surpassed-Threshold", "true",
			// No 7D-Reset header.
			"Retry-After", "120",
		)
		resetAt, ok := parseAnthropic429ResetAt(h, nowBoth)
		assert.True(t, ok, "ok — Retry-After used as outer fallback")
		assert.Equal(t, nowBoth+120, resetAt, "both exhausted, 7d absent → Retry-After outer fallback (not 5h)")
	})
}

// TestClassifyAccountPoolFailureAnthropic verifies platform-gated Anthropic classification.
func TestClassifyAccountPoolFailureAnthropic(t *testing.T) {
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

	t.Run("anthropic 429 with 5h exhausted header sets rate_limited_until", func(t *testing.T) {
		h := http.Header{}
		h.Set("Anthropic-Ratelimit-Unified-5H-Surpassed-Threshold", "true")
		h.Set("Anthropic-Ratelimit-Unified-5H-Reset", "1001000")
		err := makeErrWithUpstream("rate limited", 429, h, nil)

		got := classifyAccountPoolFailure(baseAccount, err, false, now, "anthropic", "")

		require.Contains(t, got, "rate_limited_until")
		assert.Equal(t, int64(1001000), got["rate_limited_until"])
		assert.NotContains(t, got, "status")
		assert.NotContains(t, got, "temp_disabled_until")
	})

	t.Run("anthropic 429 with NO reset headers does not set rate_limited_until", func(t *testing.T) {
		// Anthropic 429 without any reset header → no cooldown (spec says no fallback for Anthropic).
		err := makeErr("too many requests", 429)

		got := classifyAccountPoolFailure(baseAccount, err, false, now, "anthropic", "")

		assert.NotContains(t, got, "rate_limited_until", "Anthropic 429 with no reset header must not set rate_limited_until")
		assert.NotContains(t, got, "status")
		// Base bookkeeping must still be present.
		require.Contains(t, got, "last_error")
		require.Contains(t, got, "last_failure_at")
		require.Contains(t, got, "failure_count")
	})

	t.Run("anthropic 400 credit balance is too low expires account", func(t *testing.T) {
		body := []byte(`{"error":{"message":"Your credit balance is too low"}}`)
		err := makeErrWithUpstream("bad request", 400, nil, body)

		got := classifyAccountPoolFailure(baseAccount, err, false, now, "anthropic", "")

		assert.Equal(t, model.AccountPoolAccountStatusExpired, got["status"])
		assert.Equal(t, int64(0), got["rate_limited_until"])
		assert.Equal(t, int64(0), got["temp_disabled_until"])
		assert.Equal(t, int64(0), got["overload_until"])
	})

	t.Run("anthropic 400 account is not active expires account", func(t *testing.T) {
		err := makeErr("account is not active", 400)

		got := classifyAccountPoolFailure(baseAccount, err, false, now, "anthropic", "")

		assert.Equal(t, model.AccountPoolAccountStatusExpired, got["status"])
	})

	t.Run("anthropic 400 plain bad request does not expire", func(t *testing.T) {
		err := makeErr("invalid request body", 400)

		got := classifyAccountPoolFailure(baseAccount, err, false, now, "anthropic", "")

		assert.NotContains(t, got, "status")
	})

	t.Run("anthropic 429 both 5h and 7d exhausted uses 7d reset", func(t *testing.T) {
		h := http.Header{}
		h.Set("Anthropic-Ratelimit-Unified-5H-Surpassed-Threshold", "true")
		h.Set("Anthropic-Ratelimit-Unified-5H-Reset", "1001000")
		h.Set("Anthropic-Ratelimit-Unified-7D-Surpassed-Threshold", "true")
		h.Set("Anthropic-Ratelimit-Unified-7D-Reset", "1050000")
		err := makeErrWithUpstream("rate limited", 429, h, nil)

		got := classifyAccountPoolFailure(baseAccount, err, false, now, "anthropic", "")

		assert.Equal(t, int64(1050000), got["rate_limited_until"])
	})
}

// TestClassifyAccountPoolFailureOpenAIUnchanged verifies OpenAI behavior is byte-identical after platform threading.
func TestClassifyAccountPoolFailureOpenAIUnchanged(t *testing.T) {
	const now = int64(1000)
	baseAccount := model.AccountPoolAccount{
		Status: model.AccountPoolAccountStatusEnabled,
	}

	makeErr := func(msg string, code int) *types.NewAPIError {
		return types.NewErrorWithStatusCode(errors.New(msg), types.ErrorCodeBadResponseStatusCode, code)
	}

	t.Run("openai 429 no header fallback still applies", func(t *testing.T) {
		// OpenAI 429 with no header should still use fallback (RateLimit429FallbackEnabled=true → 5s).
		err := makeErr("too many requests", 429)

		got := classifyAccountPoolFailure(baseAccount, err, false, now, "openai", "")

		assert.Contains(t, got, "rate_limited_until", "OpenAI 429 fallback must still set rate_limited_until")
		assert.Equal(t, now+5, got["rate_limited_until"])
	})

	t.Run("openai 400 organization disabled expires", func(t *testing.T) {
		err := makeErr("Your organization has been disabled", 400)

		got := classifyAccountPoolFailure(baseAccount, err, false, now, "openai", "")

		assert.Equal(t, model.AccountPoolAccountStatusExpired, got["status"])
	})

	t.Run("openai 400 credit balance phrase expires (shared phrase)", func(t *testing.T) {
		// "credit balance" is in the original OpenAI phrase list too.
		err := makeErr("credit balance exceeded", 400)

		got := classifyAccountPoolFailure(baseAccount, err, false, now, "openai", "")

		assert.Equal(t, model.AccountPoolAccountStatusExpired, got["status"])
	})

	t.Run("empty platform same as openai for 429 fallback", func(t *testing.T) {
		err := makeErr("too many requests", 429)

		got := classifyAccountPoolFailure(baseAccount, err, false, now, "", "")

		assert.Contains(t, got, "rate_limited_until")
		assert.Equal(t, now+5, got["rate_limited_until"])
	})
}
