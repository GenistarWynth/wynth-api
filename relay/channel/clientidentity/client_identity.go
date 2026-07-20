package clientidentity

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	codexidentity "github.com/QuantumNous/new-api/relay/channel/codex/identity"
	"github.com/google/uuid"
)

// ClaudeCodeMimicryHeaders returns the exact built-in Claude Code CLI identity
// header bundle. Callers receive a new map so they cannot mutate the bundle.
func ClaudeCodeMimicryHeaders() map[string]string {
	return map[string]string{
		"User-Agent":                                "claude-cli/2.1.161 (external, cli)",
		"X-Stainless-Lang":                          "js",
		"X-Stainless-Package-Version":               "0.94.0",
		"X-Stainless-OS":                            "Linux",
		"X-Stainless-Arch":                          "arm64",
		"X-Stainless-Runtime":                       "node",
		"X-Stainless-Runtime-Version":               "v24.3.0",
		"X-App":                                     "cli",
		"Anthropic-Dangerous-Direct-Browser-Access": "true",
	}
}

// CodexInteractiveIdentityHeaders returns the identity/session header bundle that
// interactive Codex (codex_cli_rs) sends on /v1/responses requests.
//
// Values are aligned with:
//   - relay/channel/codex/identity constants (originator/UA pairing)
//   - real Codex outbound captures (session/thread/x-codex metadata family)
//   - the channel param-override Codex CLI passthrough header allowlist
//
// Each call generates a fresh request correlation set (session/thread/request IDs).
func CodexInteractiveIdentityHeaders() map[string]string {
	sessionID := uuid.NewString()
	turnID := uuid.NewString()
	windowID := sessionID + ":0"
	installationID := uuid.NewString()
	turnMetadata, _ := json.Marshal(map[string]any{
		"installation_id":         installationID,
		"session_id":              sessionID,
		"thread_id":               sessionID,
		"turn_id":                 turnID,
		"window_id":               windowID,
		"request_kind":            "turn",
		"thread_source":           "user",
		"turn_started_at_unix_ms": time.Now().UnixMilli(),
	})

	return map[string]string{
		"originator":                             codexidentity.CodexCLIOriginator,
		"User-Agent":                             codexidentity.CodexCLIUserAgent,
		"OpenAI-Beta":                            "responses=experimental",
		"Accept":                                 "text/event-stream",
		"session-id":                             sessionID,
		"thread-id":                              sessionID,
		"session_id":                             sessionID,
		"thread_id":                              sessionID,
		"x-client-request-id":                    sessionID,
		"x-codex-beta-features":                  "remote_compaction_v2",
		"x-codex-turn-metadata":                  string(turnMetadata),
		"x-codex-window-id":                      windowID,
		"x-codex-installation-id":                installationID,
		"x-openai-internal-codex-responses-lite": "true",
	}
}

// Apply overwrites identity headers with the selected built-in official CLI
// bundle. It intentionally leaves auth headers untouched.
func Apply(header http.Header, preset string) {
	if header == nil {
		return
	}

	switch dto.NormalizeClientIdentityPreset(preset) {
	case dto.ClientIdentityPresetCodexCLI:
		applyCodexInteractiveIdentity(header)
	case dto.ClientIdentityPresetClaudeCode:
		for name, value := range ClaudeCodeMimicryHeaders() {
			header.Set(name, value)
		}
	}
}

func applyCodexInteractiveIdentity(header http.Header) {
	// Force interactive codex_cli_rs pairing first so NormalizeIdentityHeaders keeps
	// the official interactive identity rather than rewriting a third-party UA.
	header.Set("originator", codexidentity.CodexCLIOriginator)
	header.Set("User-Agent", codexidentity.CodexCLIUserAgent)
	codexidentity.NormalizeIdentityHeaders(header)

	bundle := CodexInteractiveIdentityHeaders()
	for name, value := range bundle {
		switch name {
		case "originator", "User-Agent":
			continue
		case "OpenAI-Beta", "Accept":
			if header.Get(name) == "" {
				header.Set(name, value)
			}
		default:
			// Always set correlation/metadata headers so channel tests and non-Codex
			// clients still look like a complete interactive Codex request.
			header.Set(name, value)
		}
	}

	if header.Get("Accept") == "" {
		header.Set("Accept", "text/event-stream")
	}
	if sid := header.Get("session-id"); sid != "" && header.Get("thread-id") == "" {
		header.Set("thread-id", sid)
	}
	if meta := header.Get("x-codex-turn-metadata"); meta != "" {
		var probe any
		if err := common.UnmarshalJsonStr(meta, &probe); err != nil {
			header.Set("x-codex-turn-metadata", bundle["x-codex-turn-metadata"])
		}
	}
	if header.Get("x-codex-installation-id") == "" {
		header.Set("x-codex-installation-id", uuid.NewString())
	}
	if header.Get("originator") == "" {
		header.Set("originator", codexidentity.CodexCLIOriginator)
		header.Set("User-Agent", codexidentity.CodexCLIUserAgent)
	}
}
