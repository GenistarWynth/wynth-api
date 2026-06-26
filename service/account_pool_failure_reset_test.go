package service

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
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
				"X-Codex-Primary-Used-Percent":    []string{"100"},
				"X-Codex-Primary-Reset-After-Seconds": []string{"30"},
			},
			body:        nil,
			wantResetAt: now + 30,
			wantOK:      true,
		},
		{
			name: "both windows exhausted picks max reset-after",
			header: http.Header{
				"X-Codex-Primary-Used-Percent":      []string{"100"},
				"X-Codex-Primary-Reset-After-Seconds": []string{"60"},
				"X-Codex-Secondary-Used-Percent":    []string{"100"},
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
			name: "no codex headers fallback to body resets_at number",
			header: http.Header{},
			body:   []byte(`{"error":{"type":"usage_limit_reached","resets_at":1777283883}}`),
			wantResetAt: 1777283883,
			wantOK:      true,
		},
		{
			name: "body with resets_at as string",
			header: http.Header{},
			body:   []byte(`{"error":{"type":"usage_limit_reached","resets_at":"1777283883"}}`),
			wantResetAt: 1777283883,
			wantOK:      true,
		},
		{
			name: "body rate_limit_exceeded with resets_in_seconds",
			header: http.Header{},
			body:   []byte(`{"error":{"type":"rate_limit_exceeded","resets_in_seconds":3600}}`),
			wantResetAt: now + 3600,
			wantOK:      true,
		},
		{
			name: "body with unrecognized error type returns false",
			header: http.Header{},
			body:   []byte(`{"error":{"type":"invalid_request_error"}}`),
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
				"X-Codex-Primary-Used-Percent":        []string{"100"},
				"X-Codex-Secondary-Used-Percent":      []string{"10"},
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
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resetAt, ok := parseAccountPool429ResetAt(tc.header, tc.body, now)
			assert.Equal(t, tc.wantOK, ok)
			assert.Equal(t, tc.wantResetAt, resetAt)
		})
	}
}
