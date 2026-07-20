package clientidentity

import (
	"net/http"

	"github.com/QuantumNous/new-api/dto"
	codexidentity "github.com/QuantumNous/new-api/relay/channel/codex/identity"
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

// Apply overwrites identity headers with the selected built-in official CLI
// bundle. It intentionally leaves unrelated headers untouched.
func Apply(header http.Header, preset string) {
	if header == nil {
		return
	}

	switch dto.NormalizeClientIdentityPreset(preset) {
	case dto.ClientIdentityPresetCodexCLI:
		header.Set("originator", codexidentity.CodexCLIOriginator)
		header.Set("User-Agent", codexidentity.CodexCLIUserAgent)
		if header.Get("OpenAI-Beta") == "" {
			header.Set("OpenAI-Beta", "responses=experimental")
		}
		codexidentity.NormalizeIdentityHeaders(header)
	case dto.ClientIdentityPresetClaudeCode:
		for name, value := range ClaudeCodeMimicryHeaders() {
			header.Set(name, value)
		}
	}
}
