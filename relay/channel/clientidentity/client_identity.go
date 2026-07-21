package clientidentity

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	codexidentity "github.com/QuantumNous/new-api/relay/channel/codex/identity"
	"github.com/google/uuid"
)

// AnthropicOAuthBetaFeatures is the anthropic-beta value required for OAuth / Claude Code
// style clients. Kept here as the shared source of truth so both the Claude adaptor
// OAuth path and the channel identity preset emit the exact same flag list.
//
// It enables the oauth-2025-04-20 protocol flag plus Claude-Code-specific features.
// Tunable: update this list when Anthropic releases new beta flags.
const AnthropicOAuthBetaFeatures = "oauth-2025-04-20,claude-code-20250219,interleaved-thinking-2025-05-14,fine-grained-tool-streaming-2025-05-14"

// ClaudeCodeAnthropicVersion is the anthropic-version header used by Claude Code.
const ClaudeCodeAnthropicVersion = "2023-06-01"

// NormalizeCodexCLIResponsesRequest fills the body fields emitted by Codex CLI
// that strict Codex-compatible Responses upstreams use to distinguish Codex
// traffic. Explicit caller choices are preserved, except store is always false
// to match Codex's stateless Responses requests.
func NormalizeCodexCLIResponsesRequest(request *dto.OpenAIResponsesRequest) {
	if request == nil {
		return
	}
	if len(request.Instructions) == 0 {
		request.Instructions = json.RawMessage(`"You are Codex CLI. Complete the user's request concisely."`)
	}
	request.Store = json.RawMessage("false")
	if len(request.Tools) == 0 {
		request.Tools = json.RawMessage("[]")
	}
	if len(request.ToolChoice) == 0 {
		request.ToolChoice = json.RawMessage(`"auto"`)
	}
	if len(request.ParallelToolCalls) == 0 {
		request.ParallelToolCalls = json.RawMessage("true")
	}
	if request.Reasoning == nil {
		request.Reasoning = &dto.Reasoning{Effort: "medium", Summary: "auto"}
	}
	if len(request.Include) == 0 {
		request.Include = json.RawMessage(`["reasoning.encrypted_content"]`)
	}
	if len(request.Text) == 0 {
		request.Text = json.RawMessage(`{"verbosity":"low"}`)
	}
	if len(request.PromptCacheKey) == 0 {
		request.PromptCacheKey = json.RawMessage(`"` + uuid.NewString() + `"`)
	}
}

// ClaudeCodeMimicryHeaders returns the exact built-in Claude Code CLI identity
// header bundle (without protocol version / beta). Callers receive a new map so
// they cannot mutate the bundle.
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

// ClaudeCodeInteractiveIdentityHeaders returns the complete Claude Code outbound
// identity fingerprint used by the official Claude Code / OAuth client path.
//
// This intentionally includes:
//   - the stainless / UA / X-App mimicry bundle
//   - anthropic-version
//   - anthropic-beta (Claude Code + OAuth feature flags)
//
// Auth scheme (x-api-key vs Authorization Bearer) is NOT included; callers keep
// whatever auth the channel/adaptor already selected.
func ClaudeCodeInteractiveIdentityHeaders() map[string]string {
	headers := ClaudeCodeMimicryHeaders()
	headers["anthropic-version"] = ClaudeCodeAnthropicVersion
	headers["anthropic-beta"] = AnthropicOAuthBetaFeatures
	// Stainless retry/timeout headers appear on real Claude Code requests and are
	// part of the channel Claude CLI passthrough allowlist.
	headers["X-Stainless-Retry-Count"] = "0"
	return headers
}

// MergeAnthropicBetaFlags merges required Claude Code / OAuth bundle flags with
// client-supplied beta flags into a single deduplicated, comma-separated string.
//
// Ordering: required flags appear first (preserving their order), then any
// client-supplied flags that are not already present are appended in order.
// If clientBeta is empty, the bundle is returned unchanged.
func MergeAnthropicBetaFlags(bundle, clientBeta string) string {
	seen := make(map[string]struct{})
	result := make([]string, 0, 8)

	for _, f := range strings.Split(bundle, ",") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		if _, ok := seen[f]; !ok {
			seen[f] = struct{}{}
			result = append(result, f)
		}
	}

	if clientBeta != "" {
		for _, f := range strings.Split(clientBeta, ",") {
			f = strings.TrimSpace(f)
			if f == "" {
				continue
			}
			if _, ok := seen[f]; !ok {
				seen[f] = struct{}{}
				result = append(result, f)
			}
		}
	}

	return strings.Join(result, ",")
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
		applyClaudeCodeInteractiveIdentity(header)
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

func applyClaudeCodeInteractiveIdentity(header http.Header) {
	// Force the exact Claude Code mimicry bundle first.
	for name, value := range ClaudeCodeMimicryHeaders() {
		header.Set(name, value)
	}

	// Protocol headers required by real Claude Code / OAuth clients.
	if header.Get("anthropic-version") == "" {
		header.Set("anthropic-version", ClaudeCodeAnthropicVersion)
	}
	// Always ensure the Claude Code beta feature flags are present, while
	// preserving any extra client-supplied beta flags.
	header.Set(
		"anthropic-beta",
		MergeAnthropicBetaFlags(AnthropicOAuthBetaFeatures, header.Get("anthropic-beta")),
	)

	// Stainless retry counter is present on real Claude Code traffic.
	if header.Get("X-Stainless-Retry-Count") == "" {
		header.Set("X-Stainless-Retry-Count", "0")
	}
}
