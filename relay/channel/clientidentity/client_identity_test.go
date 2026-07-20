package clientidentity

import (
	"encoding/json"
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
	t.Run("off", func(t *testing.T) {
		header := http.Header{}
		Apply(header, dto.ClientIdentityPresetOff)
		require.Equal(t, http.Header{}, header)
	})

	t.Run("codex interactive full fingerprint", func(t *testing.T) {
		header := http.Header{}
		header.Set("User-Agent", "custom-override")
		Apply(header, dto.ClientIdentityPresetCodexCLI)

		require.Equal(t, codexidentity.CodexCLIOriginator, header.Get("originator"))
		require.Equal(t, codexidentity.CodexCLIUserAgent, header.Get("User-Agent"))
		require.Equal(t, "responses=experimental", header.Get("OpenAI-Beta"))
		require.Equal(t, "text/event-stream", header.Get("Accept"))
		require.Equal(t, "remote_compaction_v2", header.Get("x-codex-beta-features"))
		require.Equal(t, "true", header.Get("x-openai-internal-codex-responses-lite"))

		sessionID := header.Get("session-id")
		require.NotEmpty(t, sessionID)
		require.Equal(t, sessionID, header.Get("thread-id"))
		require.Equal(t, sessionID, header.Get("session_id"))
		require.Equal(t, sessionID, header.Get("thread_id"))
		require.Equal(t, sessionID, header.Get("x-client-request-id"))
		require.Equal(t, sessionID+":0", header.Get("x-codex-window-id"))
		require.NotEmpty(t, header.Get("x-codex-installation-id"))

		var meta map[string]any
		require.NoError(t, json.Unmarshal([]byte(header.Get("x-codex-turn-metadata")), &meta))
		require.Equal(t, sessionID, meta["session_id"])
		require.Equal(t, sessionID, meta["thread_id"])
		require.Equal(t, "turn", meta["request_kind"])
		require.Equal(t, "user", meta["thread_source"])
	})

	t.Run("claude code", func(t *testing.T) {
		header := http.Header{}
		Apply(header, dto.ClientIdentityPresetClaudeCode)
		require.Equal(t, http.Header{
			"Anthropic-Dangerous-Direct-Browser-Access": {"true"},
			"User-Agent":                  {"claude-cli/2.1.161 (external, cli)"},
			"X-App":                       {"cli"},
			"X-Stainless-Arch":            {"arm64"},
			"X-Stainless-Lang":            {"js"},
			"X-Stainless-Os":              {"Linux"},
			"X-Stainless-Package-Version": {"0.94.0"},
			"X-Stainless-Runtime":         {"node"},
			"X-Stainless-Runtime-Version": {"v24.3.0"},
		}, header)
	})
}
