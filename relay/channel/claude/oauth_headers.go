package claude

// AnthropicOAuthBetaFeatures is the anthropic-beta value required for OAuth tokens.
// It enables the oauth-2025-04-20 protocol flag plus Claude-Code-specific features.
// Tunable: update this list when Anthropic releases new beta flags.
const AnthropicOAuthBetaFeatures = "oauth-2025-04-20,claude-code-20250219,interleaved-thinking-2025-05-14,fine-grained-tool-streaming-2025-05-14"

// claudeCodeMimicryHeaders returns the HTTP headers that mimic a Claude Code CLI
// client when using Anthropic OAuth tokens. These headers are required for
// OAuth-authenticated requests to the Anthropic API.
func claudeCodeMimicryHeaders() map[string]string {
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
