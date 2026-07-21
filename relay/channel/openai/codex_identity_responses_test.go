package openai

import (
	"encoding/json"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConvertOpenAIResponsesRequestNormalizesCodexCLIIdentity(t *testing.T) {
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelOtherSettings: dto.ChannelOtherSettings{
		ClientIdentityPreset: dto.ClientIdentityPresetCodexCLI,
	}}}
	converted, err := (&Adaptor{}).ConvertOpenAIResponsesRequest(nil, info, dto.OpenAIResponsesRequest{
		Model: "gpt-5.6-sol",
		Input: json.RawMessage(`[{"role":"user","content":"hi"}]`),
	})
	require.NoError(t, err)
	request, ok := converted.(dto.OpenAIResponsesRequest)
	require.True(t, ok)
	assert.NotEmpty(t, request.Instructions)
	assert.JSONEq(t, `false`, string(request.Store))
	assert.JSONEq(t, `[]`, string(request.Tools))
	assert.JSONEq(t, `"auto"`, string(request.ToolChoice))
	assert.JSONEq(t, `true`, string(request.ParallelToolCalls))
	assert.NotEmpty(t, request.PromptCacheKey)
}

func TestConvertOpenAIResponsesRequestDoesNotNormalizeDefaultIdentity(t *testing.T) {
	converted, err := (&Adaptor{}).ConvertOpenAIResponsesRequest(nil, &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}, dto.OpenAIResponsesRequest{
		Model: "gpt-5.6-sol",
		Input: json.RawMessage(`[{"role":"user","content":"hi"}]`),
	})
	require.NoError(t, err)
	request, ok := converted.(dto.OpenAIResponsesRequest)
	require.True(t, ok)
	assert.Empty(t, request.Instructions)
	assert.Empty(t, request.Store)
	assert.Empty(t, request.Tools)
	assert.Empty(t, request.ToolChoice)
	assert.Empty(t, request.ParallelToolCalls)
	assert.Empty(t, request.PromptCacheKey)
}
