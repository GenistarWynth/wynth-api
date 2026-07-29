package dto

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpenAIResponsesRequestPreservesCodexFields(t *testing.T) {
	input := `{"model":"gpt-5","client_metadata":{"nested":[1,false,{"x":0}]},"reasoning":{"effort":"high","mode":false,"context":0},"prompt_cache_options":{"mode":"24h","ttl":0,"breakpoint":{"unknown":[false,0]}},"prompt_cache_retention":{"legacy":true}}`
	var request OpenAIResponsesRequest
	require.NoError(t, common.UnmarshalJsonStr(input, &request))
	assert.Equal(t, `{"nested":[1,false,{"x":0}]}`, string(request.ClientMetadata))
	assert.Equal(t, `{"mode":"24h","ttl":0,"breakpoint":{"unknown":[false,0]}}`, string(request.PromptCacheOptions))
	assert.Equal(t, `{"legacy":true}`, string(request.PromptCacheRetention))
	output, err := common.Marshal(request)
	require.NoError(t, err)
	assert.JSONEq(t, input, string(output))
}

func TestOpenAIResponsesCompactionRequestPreservesCompactSuperset(t *testing.T) {
	input := `{"model":"gpt-5","metadata":{"m":1},"tools":[],"parallel_tool_calls":false,"reasoning":{"mode":false,"context":0},"service_tier":"priority","prompt_cache_key":"k","prompt_cache_options":{"mode":"24h","ttl":0,"breakpoint":{"x":false}},"prompt_cache_retention":{"legacy":true},"text":{"format":{"type":"text"}}}`
	var request OpenAIResponsesCompactionRequest
	require.NoError(t, common.UnmarshalJsonStr(input, &request))
	output, err := common.Marshal(request)
	require.NoError(t, err)
	assert.JSONEq(t, input, string(output))
}

func TestOpenAIResponsesRequestIsRemoteCompactionV2(t *testing.T) {
	validInput := []byte(`[
		{"type":"message","role":"user","content":[{"type":"input_text","text":"Retained conversation text"}]},
		{"type":"compaction_trigger"}
	]`)
	validClientMetadata := []byte(`{
		"x-codex-installation-id":"install-test",
		"session_id":"session-test",
		"thread_id":"thread-test",
		"turn_id":"turn-test",
		"x-codex-window-id":"thread-test:0",
		"x-codex-turn-metadata":"{\"installation_id\":\"install-test\",\"session_id\":\"session-test\",\"thread_id\":\"thread-test\",\"turn_id\":\"turn-test\",\"window_id\":\"thread-test:0\",\"request_kind\":\"compaction\",\"compaction\":{\"trigger\":\"manual\",\"reason\":\"user_requested\",\"implementation\":\"responses_compaction_v2\",\"phase\":\"standalone_turn\",\"strategy\":\"memento\"}}"
	}`)

	tests := []struct {
		name    string
		request *OpenAIResponsesRequest
		want    bool
	}{
		{
			name: "exact Codex remote compaction v2 request",
			request: &OpenAIResponsesRequest{
				Input:          validInput,
				ClientMetadata: validClientMetadata,
			},
			want: true,
		},
		{name: "nil request"},
		{name: "missing input", request: &OpenAIResponsesRequest{ClientMetadata: validClientMetadata}},
		{name: "malformed input", request: &OpenAIResponsesRequest{Input: []byte(`[`), ClientMetadata: validClientMetadata}},
		{
			name: "trigger is not final",
			request: &OpenAIResponsesRequest{
				Input:          []byte(`[{"type":"compaction_trigger"},{"type":"message"}]`),
				ClientMetadata: validClientMetadata,
			},
		},
		{
			name: "final trigger has extra fields",
			request: &OpenAIResponsesRequest{
				Input:          []byte(`[{"type":"compaction_trigger","future":true}]`),
				ClientMetadata: validClientMetadata,
			},
		},
		{name: "missing client metadata", request: &OpenAIResponsesRequest{Input: validInput}},
		{
			name:    "malformed client metadata",
			request: &OpenAIResponsesRequest{Input: validInput, ClientMetadata: []byte(`{"x-codex-turn-metadata"`)},
		},
		{
			name:    "turn metadata has wrong type",
			request: &OpenAIResponsesRequest{Input: validInput, ClientMetadata: []byte(`{"x-codex-turn-metadata":{"request_kind":"compaction"}}`)},
		},
		{
			name:    "unrelated client metadata",
			request: &OpenAIResponsesRequest{Input: validInput, ClientMetadata: []byte(`{"trace_id":"trace-test"}`)},
		},
		{
			name:    "malformed turn metadata JSON string",
			request: &OpenAIResponsesRequest{Input: validInput, ClientMetadata: []byte(`{"x-codex-turn-metadata":"not-json"}`)},
		},
		{
			name:    "wrong request kind",
			request: &OpenAIResponsesRequest{Input: validInput, ClientMetadata: []byte(`{"x-codex-turn-metadata":"{\"request_kind\":\"turn\",\"compaction\":{\"implementation\":\"responses_compaction_v2\"}}"}`)},
		},
		{
			name:    "missing compaction metadata",
			request: &OpenAIResponsesRequest{Input: validInput, ClientMetadata: []byte(`{"x-codex-turn-metadata":"{\"request_kind\":\"compaction\"}"}`)},
		},
		{
			name:    "wrong compaction metadata type",
			request: &OpenAIResponsesRequest{Input: validInput, ClientMetadata: []byte(`{"x-codex-turn-metadata":"{\"request_kind\":\"compaction\",\"compaction\":\"responses_compaction_v2\"}"}`)},
		},
		{
			name:    "wrong compaction implementation",
			request: &OpenAIResponsesRequest{Input: validInput, ClientMetadata: []byte(`{"x-codex-turn-metadata":"{\"request_kind\":\"compaction\",\"compaction\":{\"implementation\":\"compact_endpoint\"}}"}`)},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, test.request.IsRemoteCompactionV2())
		})
	}
}
