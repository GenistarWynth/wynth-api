package clientidentity

import (
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	codexidentity "github.com/QuantumNous/new-api/relay/channel/codex/identity"
	"github.com/stretchr/testify/require"
)

func TestClaudeCodeMimicryHeadersExact(t *testing.T) {
	require.Equal(t, map[string]string{
		"User-Agent":                                "claude-cli/2.1.161 (external, cli)",
		"X-Stainless-Lang":                          "js",
		"X-Stainless-Package-Version":               "0.94.0",
		"X-Stainless-OS":                            "Linux",
		"X-Stainless-Arch":                          "arm64",
		"X-Stainless-Runtime":                       "node",
		"X-Stainless-Runtime-Version":               "v24.3.0",
		"X-App":                                     "cli",
		"Anthropic-Dangerous-Direct-Browser-Access": "true",
	}, ClaudeCodeMimicryHeaders())
}

func TestApplyClientIdentityPresetExactHeaders(t *testing.T) {
	tests := []struct {
		name   string
		preset string
		want   http.Header
	}{
		{
			name:   "off",
			preset: dto.ClientIdentityPresetOff,
			want:   http.Header{},
		},
		{
			name:   "codex cli",
			preset: dto.ClientIdentityPresetCodexCLI,
			want: http.Header{
				"Openai-Beta": {"responses=experimental"},
				"Originator":  {codexidentity.CodexCLIOriginator},
				"User-Agent":  {codexidentity.CodexCLIUserAgent},
			},
		},
		{
			name:   "claude code",
			preset: dto.ClientIdentityPresetClaudeCode,
			want: http.Header{
				"Anthropic-Dangerous-Direct-Browser-Access": {"true"},
				"User-Agent":                  {"claude-cli/2.1.161 (external, cli)"},
				"X-App":                       {"cli"},
				"X-Stainless-Arch":            {"arm64"},
				"X-Stainless-Lang":            {"js"},
				"X-Stainless-Os":              {"Linux"},
				"X-Stainless-Package-Version": {"0.94.0"},
				"X-Stainless-Runtime":         {"node"},
				"X-Stainless-Runtime-Version": {"v24.3.0"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			header := http.Header{}

			Apply(header, tt.preset)

			require.Equal(t, tt.want, header)
		})
	}
}
