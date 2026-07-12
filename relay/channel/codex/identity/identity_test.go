package identity

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNormalizeIdentityHeaders(t *testing.T) {
	tests := []struct {
		name, originator, ua, wantOriginator, wantUA string
	}{
		{"fallback", "codex_cli_rs", "", "codex_cli_rs", CodexCLIUserAgent},
		{"cli", "wrong", "codex_cli_rs/0.145.0", "codex_cli_rs", "codex_cli_rs/0.145.0"},
		{"tui", "wrong", "codex-tui/0.140.2 (codex-tui; 0.140.2)", "codex-tui", "codex-tui/0.140.2 (codex-tui; 0.140.2)"},
		{"desktop", "wrong", "Codex Desktop/1.2.3", "Codex Desktop", "Codex Desktop/1.2.3"},
		{"third party", "luna", "luna/1.0", "codex_cli_rs", CodexCLIUserAgent},
		{"malformed", "x", "Codex \x01evil/1.0", "codex_cli_rs", CodexCLIUserAgent},
		{"no originator", "", "luna/1.0", "", "luna/1.0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := http.Header{}
			h.Set("originator", tt.originator)
			h.Set("User-Agent", tt.ua)
			NormalizeIdentityHeaders(h)
			assert.Equal(t, tt.wantOriginator, h.Get("originator"))
			assert.Equal(t, tt.wantUA, h.Get("User-Agent"))
		})
	}
}
