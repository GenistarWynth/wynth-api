package clientidentity

import (
	"encoding/json"
	"net/http"
	"strings"
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

func TestClaudeCodeInteractiveIdentityHeadersExact(t *testing.T) {
	headers := ClaudeCodeInteractiveIdentityHeaders()
	for k, v := range ClaudeCodeMimicryHeaders() {
		require.Equal(t, v, headers[k], k)
	}
	require.Equal(t, ClaudeCodeAnthropicVersion, headers["anthropic-version"])
	require.Equal(t, AnthropicOAuthBetaFeatures, headers["anthropic-beta"])
	require.Equal(t, "0", headers["X-Stainless-Retry-Count"])
	for _, flag := range strings.Split(AnthropicOAuthBetaFeatures, ",") {
		require.Contains(t, headers["anthropic-beta"], flag)
	}
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

	t.Run("claude code full fingerprint", func(t *testing.T) {
		header := http.Header{}
		header.Set("User-Agent", "custom-override")
		header.Set("anthropic-beta", "custom-flag-2025")
		Apply(header, dto.ClientIdentityPresetClaudeCode)

		require.Equal(t, "claude-cli/2.1.161 (external, cli)", header.Get("User-Agent"))
		require.Equal(t, "js", header.Get("X-Stainless-Lang"))
		require.Equal(t, "0.94.0", header.Get("X-Stainless-Package-Version"))
		require.Equal(t, "Linux", header.Get("X-Stainless-OS"))
		require.Equal(t, "arm64", header.Get("X-Stainless-Arch"))
		require.Equal(t, "node", header.Get("X-Stainless-Runtime"))
		require.Equal(t, "v24.3.0", header.Get("X-Stainless-Runtime-Version"))
		require.Equal(t, "cli", header.Get("X-App"))
		require.Equal(t, "true", header.Get("Anthropic-Dangerous-Direct-Browser-Access"))
		require.Equal(t, ClaudeCodeAnthropicVersion, header.Get("anthropic-version"))
		require.Equal(t, "0", header.Get("X-Stainless-Retry-Count"))

		beta := header.Get("anthropic-beta")
		for _, flag := range strings.Split(AnthropicOAuthBetaFeatures, ",") {
			require.Contains(t, beta, flag)
		}
		require.Contains(t, beta, "custom-flag-2025")
	})
}

func TestMergeAnthropicBetaFlags(t *testing.T) {
	require.Equal(t, AnthropicOAuthBetaFeatures, MergeAnthropicBetaFlags(AnthropicOAuthBetaFeatures, ""))
	merged := MergeAnthropicBetaFlags(AnthropicOAuthBetaFeatures, "custom-flag-2025,oauth-2025-04-20")
	require.Contains(t, merged, "custom-flag-2025")
	require.Equal(t, 1, strings.Count(merged, "oauth-2025-04-20"))
}
