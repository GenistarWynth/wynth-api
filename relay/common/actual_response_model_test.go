package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDetectActualResponseModel(t *testing.T) {
	tests := []struct {
		name       string
		kind       ActualResponseModelKind
		payload    string
		wantModel  string
		wantSource ActualResponseModelSource
	}{
		{
			name:       "openai chat top-level model",
			kind:       ActualResponseModelKindOpenAIChat,
			payload:    `{"id":"chatcmpl_1","model":"gpt-5.5","choices":[]}`,
			wantModel:  "gpt-5.5",
			wantSource: ActualResponseModelSourceOpenAIChat,
		},
		{
			name:       "openai chat stream chunk",
			kind:       ActualResponseModelKindOpenAIChat,
			payload:    `{"id":"chatcmpl_1","object":"chat.completion.chunk","model":"gpt-5.4","choices":[{"delta":{"content":"x"},"index":0}]}`,
			wantModel:  "gpt-5.4",
			wantSource: ActualResponseModelSourceOpenAIChat,
		},
		{
			name:       "openai responses top-level model",
			kind:       ActualResponseModelKindOpenAIResponses,
			payload:    `{"id":"resp_1","model":"gpt-4.1","output":[]}`,
			wantModel:  "gpt-4.1",
			wantSource: ActualResponseModelSourceOpenAIResponses,
		},
		{
			name:       "openai responses stream envelope model",
			kind:       ActualResponseModelKindOpenAIResponses,
			payload:    `{"type":"response.created","response":{"id":"resp_1","model":"gpt-4.1-2025-04-14"}}`,
			wantModel:  "gpt-4.1-2025-04-14",
			wantSource: ActualResponseModelSourceOpenAIResponses,
		},
		{
			name:       "anthropic non-stream model",
			kind:       ActualResponseModelKindAnthropic,
			payload:    `{"id":"msg_1","type":"message","model":"claude-sonnet-4-5-20250929","content":[]}`,
			wantModel:  "claude-sonnet-4-5-20250929",
			wantSource: ActualResponseModelSourceAnthropicMessage,
		},
		{
			name:       "anthropic stream message_start model",
			kind:       ActualResponseModelKindAnthropic,
			payload:    `{"type":"message_start","message":{"id":"msg_1","type":"message","model":"claude-opus-4-1-20250805"}}`,
			wantModel:  "claude-opus-4-1-20250805",
			wantSource: ActualResponseModelSourceAnthropicMessage,
		},
		{
			name:       "anthropic stream message_start falls back to top-level model",
			kind:       ActualResponseModelKindAnthropic,
			payload:    `{"type":"message_start","model":"claude-sonnet-4-5-20250929","message":{"id":"msg_1","type":"message","model":"   "}}`,
			wantModel:  "claude-sonnet-4-5-20250929",
			wantSource: ActualResponseModelSourceAnthropicMessage,
		},
		{
			name:       "gemini camel modelVersion",
			kind:       ActualResponseModelKindGemini,
			payload:    `{"modelVersion":"gemini-2.5-pro","candidates":[]}`,
			wantModel:  "gemini-2.5-pro",
			wantSource: ActualResponseModelSourceGeminiModelVersion,
		},
		{
			name:       "gemini snake model_version",
			kind:       ActualResponseModelKindGemini,
			payload:    `{"model_version":"gemini-2.5-flash","candidates":[]}`,
			wantModel:  "gemini-2.5-flash",
			wantSource: ActualResponseModelSourceGeminiModelVersion,
		},
		{
			name:    "malformed input returns empty audit",
			kind:    ActualResponseModelKindOpenAIChat,
			payload: `{"model":`,
		},
		{
			name:    "empty model returns empty audit",
			kind:    ActualResponseModelKindOpenAIChat,
			payload: `{"model":"   "}`,
		},
		{
			name:    "unknown kind returns empty audit",
			kind:    ActualResponseModelKind("unknown"),
			payload: `{"model":"gpt-5.5"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DetectActualResponseModel(tt.kind, []byte(tt.payload))
			assert.Equal(t, tt.wantModel, got.Model)
			assert.Equal(t, tt.wantSource, got.Source)
		})
	}
}

func TestRelayInfoSetActualResponseModel(t *testing.T) {
	info := &RelayInfo{}

	info.SetActualResponseModel("  gpt-5.5  ", ActualResponseModelSourceOpenAIChat)
	assert.Equal(t, "gpt-5.5", info.ActualResponseModel)
	assert.Equal(t, ActualResponseModelSourceOpenAIChat, info.ActualResponseModelSource)

	info.SetActualResponseModel("gpt-5.4", "")
	assert.Equal(t, "gpt-5.5", info.ActualResponseModel)
	assert.Equal(t, ActualResponseModelSourceOpenAIChat, info.ActualResponseModelSource)

	info.SetActualResponseModel("   ", ActualResponseModelSourceOpenAIResponses)
	assert.Equal(t, "gpt-5.5", info.ActualResponseModel)
	assert.Equal(t, ActualResponseModelSourceOpenAIChat, info.ActualResponseModelSource)

	ok := info.SetActualResponseModel("gpt-5.4", ActualResponseModelSource("   "))
	require.False(t, ok)
	assert.Equal(t, "gpt-5.5", info.ActualResponseModel)
	assert.Equal(t, ActualResponseModelSourceOpenAIChat, info.ActualResponseModelSource)

	ok = info.SetActualResponseModel("gpt-5.4", ActualResponseModelSource("bogus"))
	require.False(t, ok)
	assert.Equal(t, "gpt-5.5", info.ActualResponseModel)
	assert.Equal(t, ActualResponseModelSourceOpenAIChat, info.ActualResponseModelSource)
}

func TestRelayInfoApplyActualResponseModelAudit(t *testing.T) {
	info := &RelayInfo{}

	ok := info.ApplyActualResponseModelAudit(
		ActualResponseModelKindOpenAIResponses,
		[]byte(`{"response":{"model":"gpt-4.1"}}`),
	)

	require.True(t, ok)
	assert.Equal(t, "gpt-4.1", info.ActualResponseModel)
	assert.Equal(t, ActualResponseModelSourceOpenAIResponses, info.ActualResponseModelSource)
}
