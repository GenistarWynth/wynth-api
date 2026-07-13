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
