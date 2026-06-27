package service

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseAccountPool429ResetAt(t *testing.T) {
	const now = int64(1000000)

	tests := []struct {
		name        string
		header      http.Header
		body        []byte
		wantResetAt int64
		wantOK      bool
	}{
		{
			name: "primary window exhausted with reset-after uses now+reset",
			header: http.Header{
				"X-Codex-Primary-Used-Percent":        []string{"100"},
				"X-Codex-Primary-Reset-After-Seconds": []string{"30"},
			},
			body:        nil,
			wantResetAt: now + 30,
			wantOK:      true,
		},
		{
			name: "both windows exhausted picks max reset-after",
			header: http.Header{
				"X-Codex-Primary-Used-Percent":          []string{"100"},
				"X-Codex-Primary-Reset-After-Seconds":   []string{"60"},
				"X-Codex-Secondary-Used-Percent":        []string{"100"},
				"X-Codex-Secondary-Reset-After-Seconds": []string{"3600"},
			},
			body:        nil,
			wantResetAt: now + 3600,
			wantOK:      true,
		},
		{
			name: "no window exhausted but reset-after present uses min positive",
			header: http.Header{
				"X-Codex-Primary-Reset-After-Seconds": []string{"30"},
			},
			body:        nil,
			wantResetAt: now + 30,
			wantOK:      true,
		},
		{
			name:        "no codex headers fallback to body resets_at number",
			header:      http.Header{},
			body:        []byte(`{"error":{"type":"usage_limit_reached","resets_at":1777283883}}`),
			wantResetAt: 1777283883,
			wantOK:      true,
		},
		{
			name:        "body with resets_at as string",
			header:      http.Header{},
			body:        []byte(`{"error":{"type":"usage_limit_reached","resets_at":"1777283883"}}`),
			wantResetAt: 1777283883,
			wantOK:      true,
		},
		{
			name:        "body rate_limit_exceeded with resets_in_seconds",
			header:      http.Header{},
			body:        []byte(`{"error":{"type":"rate_limit_exceeded","resets_in_seconds":3600}}`),
			wantResetAt: now + 3600,
			wantOK:      true,
		},
		{
			name:        "body with unrecognized error type returns false",
			header:      http.Header{},
			body:        []byte(`{"error":{"type":"invalid_request_error"}}`),
			wantResetAt: 0,
			wantOK:      false,
		},
		{
			name:        "empty header and empty body returns false",
			header:      http.Header{},
			body:        []byte{},
			wantResetAt: 0,
			wantOK:      false,
		},
		{
			name:        "malformed body does not panic and returns false",
			header:      http.Header{},
			body:        []byte(`{not json`),
			wantResetAt: 0,
			wantOK:      false,
		},
		{
			// Primary window is exhausted (100%) but has NO reset-after header.
			// Secondary window is NOT exhausted (10%) but has a reset-after of 30s.
			// Per spec: must NOT fall back to a non-exhausted window's reset when a window is exhausted.
			// Expected: (0, false) — header path yields nothing usable, body is empty.
			name: "exhausted window without reset-after must not use non-exhausted window reset",
			header: http.Header{
				"X-Codex-Primary-Used-Percent":          []string{"100"},
				"X-Codex-Secondary-Used-Percent":        []string{"10"},
				"X-Codex-Secondary-Reset-After-Seconds": []string{"30"},
			},
			body:        nil,
			wantResetAt: 0,
			wantOK:      false,
		},
		{
			// Primary exhausted WITH reset 60s; secondary exhausted WITHOUT reset.
			// Expected: now+60 (max of exhausted windows that have a reset-after).
			name: "two exhausted windows only one has reset-after picks that one",
			header: http.Header{
				"X-Codex-Primary-Used-Percent":        []string{"100"},
				"X-Codex-Primary-Reset-After-Seconds": []string{"60"},
				"X-Codex-Secondary-Used-Percent":      []string{"100"},
			},
			body:        nil,
			wantResetAt: now + 60,
			wantOK:      true,
		},
		// Step 3 — generic Retry-After fallback cases.
		{
			// No codex headers, no body: Retry-After integer seconds → now+120.
			name: "retry-after integer seconds fallback",
			header: http.Header{
				"Retry-After": []string{"120"},
			},
			body:        nil,
			wantResetAt: now + 120,
			wantOK:      true,
		},
		{
			// No codex headers, no body: Retry-After HTTP-date in the future → that unix.
			name: "retry-after http-date in future fallback",
			header: func() http.Header {
				h := http.Header{}
				// now=1000000; pick a date comfortably in the future.
				future := time.Unix(now+300, 0).UTC()
				h.Set("Retry-After", future.Format(http.TimeFormat))
				return h
			}(),
			body:        nil,
			wantResetAt: now + 300,
			wantOK:      true,
		},
		{
			// Retry-After with value 0 → not usable.
			name:        "retry-after zero not usable",
			header:      http.Header{"Retry-After": []string{"0"}},
			body:        nil,
			wantResetAt: 0,
			wantOK:      false,
		},
		{
			// Retry-After absent → (0, false).
			name:        "retry-after absent returns false",
			header:      http.Header{},
			body:        nil,
			wantResetAt: 0,
			wantOK:      false,
		},
		{
			// Codex header wins over Retry-After: both present, codex should be returned.
			name: "codex header takes precedence over retry-after",
			header: http.Header{
				"X-Codex-Primary-Reset-After-Seconds": []string{"30"},
				"Retry-After":                         []string{"120"},
			},
			body:        nil,
			wantResetAt: now + 30,
			wantOK:      true,
		},
		{
			// OpenAI body wins over Retry-After: body with usage_limit_reached should be returned.
			name: "openai body takes precedence over retry-after",
			header: http.Header{
				"Retry-After": []string{"120"},
			},
			body:        []byte(`{"error":{"type":"usage_limit_reached","resets_at":1777283883}}`),
			wantResetAt: 1777283883,
			wantOK:      true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resetAt, ok := parseAccountPool429ResetAt(tc.header, tc.body, now)
			assert.Equal(t, tc.wantOK, ok)
			assert.Equal(t, tc.wantResetAt, resetAt)
		})
	}
}

// TestAccountPoolParseRetryAfterHeader verifies the generic Retry-After header helper.
func TestAccountPoolParseRetryAfterHeader(t *testing.T) {
	const now = int64(1_000_000)

	t.Run("integer seconds positive", func(t *testing.T) {
		h := http.Header{"Retry-After": []string{"60"}}
		v, ok := accountPoolParseRetryAfterHeader(h, now)
		require.True(t, ok)
		assert.Equal(t, now+60, v)
	})

	t.Run("integer seconds zero not usable", func(t *testing.T) {
		h := http.Header{"Retry-After": []string{"0"}}
		_, ok := accountPoolParseRetryAfterHeader(h, now)
		assert.False(t, ok)
	})

	t.Run("integer seconds negative not usable", func(t *testing.T) {
		h := http.Header{"Retry-After": []string{"-5"}}
		_, ok := accountPoolParseRetryAfterHeader(h, now)
		assert.False(t, ok)
	})

	t.Run("http-date in future", func(t *testing.T) {
		future := time.Unix(now+500, 0).UTC()
		h := http.Header{"Retry-After": []string{future.Format(http.TimeFormat)}}
		v, ok := accountPoolParseRetryAfterHeader(h, now)
		require.True(t, ok)
		assert.Equal(t, now+500, v)
	})

	t.Run("http-date in past not usable", func(t *testing.T) {
		past := time.Unix(now-100, 0).UTC()
		h := http.Header{"Retry-After": []string{past.Format(http.TimeFormat)}}
		_, ok := accountPoolParseRetryAfterHeader(h, now)
		assert.False(t, ok)
	})

	t.Run("header absent", func(t *testing.T) {
		_, ok := accountPoolParseRetryAfterHeader(http.Header{}, now)
		assert.False(t, ok)
	})

	t.Run("header malformed", func(t *testing.T) {
		h := http.Header{"Retry-After": []string{"not-a-number-or-date"}}
		_, ok := accountPoolParseRetryAfterHeader(h, now)
		assert.False(t, ok)
	})
}

// TestClassifyAccountPoolFailureXAI429WithRetryAfter verifies that a 429 from an
// xai-platform account (which uses the default/openai classifier path) correctly
// honours a Retry-After header when the body carries no OpenAI-style resets.
//
// This is the classifier-level contract test: platform="xai" → default branch →
// parseAccountPool429ResetAt → Step 3 Retry-After → rate_limited_until≈now+retryAfter.
func TestClassifyAccountPoolFailureXAI429WithRetryAfter(t *testing.T) {
	const (
		now            = int64(1_000_000)
		retryAfterSecs = int64(180)
	)

	baseAccount := model.AccountPoolAccount{
		Status: model.AccountPoolAccountStatusEnabled,
	}

	header := http.Header{"Retry-After": []string{"180"}}
	err := types.NewErrorWithStatusCode(errors.New("rate limited"), types.ErrorCodeBadResponseStatusCode, 429)
	err.SetUpstreamResponse(header, nil, 429)

	got := classifyAccountPoolFailure(baseAccount, err, false, now, model.AccountPoolPlatformXAI, "")

	require.Contains(t, got, "rate_limited_until", "xai 429 with Retry-After must set rate_limited_until")
	assert.Equal(t, now+retryAfterSecs, got["rate_limited_until"])
	assert.NotContains(t, got, "status", "rate-limited account must not be expired")
	assert.NotContains(t, got, "temp_disabled_until")
}
