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
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resetAt, ok := parseAccountPool429ResetAt(tc.header, tc.body, now)
			assert.Equal(t, tc.wantOK, ok)
			assert.Equal(t, tc.wantResetAt, resetAt)
		})
	}
}
