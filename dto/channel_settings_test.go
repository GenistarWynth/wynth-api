package dto

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeClientIdentityPreset(t *testing.T) {
	tests := []struct {
		name   string
		preset string
		want   string
	}{
		{name: "off", preset: ClientIdentityPresetOff, want: ClientIdentityPresetOff},
		{name: "codex cli", preset: ClientIdentityPresetCodexCLI, want: ClientIdentityPresetCodexCLI},
		{name: "claude code", preset: ClientIdentityPresetClaudeCode, want: ClientIdentityPresetClaudeCode},
		{name: "trims whitespace", preset: "  codex_cli  ", want: ClientIdentityPresetCodexCLI},
		{name: "unknown falls back to off", preset: "custom", want: ClientIdentityPresetOff},
		{name: "empty falls back to off", preset: "", want: ClientIdentityPresetOff},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, NormalizeClientIdentityPreset(tt.preset))
		})
	}
}

func TestAdvancedCustomValidateResponsesToChatConverterPath(t *testing.T) {
	valid := &AdvancedCustomConfig{
		Routes: []AdvancedCustomRoute{
			{
				IncomingPath: "/v1/responses",
				UpstreamPath: "/v1/chat/completions",
				Converter:    AdvancedCustomConverterOpenAIResponsesToOpenAIChatCompletions,
			},
		},
	}
	require.NoError(t, valid.Validate())

	tests := []struct {
		name         string
		incomingPath string
	}{
		{name: "chat completions", incomingPath: "/v1/chat/completions"},
		{name: "responses compact", incomingPath: "/v1/responses/compact"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := &AdvancedCustomConfig{
				Routes: []AdvancedCustomRoute{
					{
						IncomingPath: tt.incomingPath,
						UpstreamPath: "/v1/chat/completions",
						Converter:    AdvancedCustomConverterOpenAIResponsesToOpenAIChatCompletions,
					},
				},
			}
			err := config.Validate()
			require.Error(t, err)
			assert.Contains(t, err.Error(), "converter does not match incoming_path")
		})
	}
}
