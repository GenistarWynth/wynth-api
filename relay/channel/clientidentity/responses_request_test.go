package clientidentity

import (
	"encoding/json"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeCodexCLIResponsesRequestAddsMissingCodexFields(t *testing.T) {
	stream := true
	request := dto.OpenAIResponsesRequest{
		Model:  "gpt-5.6-sol",
		Input:  json.RawMessage(`[{"role":"user","content":"hi"}]`),
		Stream: &stream,
	}

	NormalizeCodexCLIResponsesRequest(&request)

	assert.True(t, *request.Stream)
	assert.JSONEq(t, `[{"role":"user","content":"hi"}]`, string(request.Input))
	assert.NotEmpty(t, request.Instructions)
	assert.JSONEq(t, `false`, string(request.Store))
	assert.JSONEq(t, `[]`, string(request.Tools))
	assert.JSONEq(t, `"auto"`, string(request.ToolChoice))
	assert.JSONEq(t, `true`, string(request.ParallelToolCalls))
	assert.JSONEq(t, `["reasoning.encrypted_content"]`, string(request.Include))
	assert.JSONEq(t, `{"verbosity":"low"}`, string(request.Text))
	assert.NotEmpty(t, request.PromptCacheKey)
	require.NotNil(t, request.Reasoning)
	assert.Equal(t, "medium", request.Reasoning.Effort)
	assert.Equal(t, "auto", request.Reasoning.Summary)
}

func TestNormalizeCodexCLIResponsesRequestPreservesExplicitOptionalFields(t *testing.T) {
	request := dto.OpenAIResponsesRequest{
		Instructions:      json.RawMessage(`"custom"`),
		Tools:             json.RawMessage(`[{"type":"function","name":"custom"}]`),
		ToolChoice:        json.RawMessage(`"required"`),
		ParallelToolCalls: json.RawMessage(`false`),
		Include:           json.RawMessage(`["message.output_text.logprobs"]`),
		Text:              json.RawMessage(`{"verbosity":"high"}`),
		PromptCacheKey:    json.RawMessage(`"stable"`),
		Reasoning:         &dto.Reasoning{Effort: "high", Summary: "detailed"},
	}

	NormalizeCodexCLIResponsesRequest(&request)

	assert.JSONEq(t, `"custom"`, string(request.Instructions))
	assert.JSONEq(t, `false`, string(request.Store))
	assert.JSONEq(t, `[{"type":"function","name":"custom"}]`, string(request.Tools))
	assert.JSONEq(t, `"required"`, string(request.ToolChoice))
	assert.JSONEq(t, `false`, string(request.ParallelToolCalls))
	assert.JSONEq(t, `["message.output_text.logprobs"]`, string(request.Include))
	assert.JSONEq(t, `{"verbosity":"high"}`, string(request.Text))
	assert.JSONEq(t, `"stable"`, string(request.PromptCacheKey))
	assert.Equal(t, "high", request.Reasoning.Effort)
	assert.Equal(t, "detailed", request.Reasoning.Summary)
}
