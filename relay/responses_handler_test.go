package relay_test

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCompactRequestSharedFieldsMarshalExactly(t *testing.T) {
	request := dto.OpenAIResponsesCompactionRequest{
		Model:                "gpt-5-codex",
		Metadata:             []byte(`{"trace":"batch3"}`),
		Tools:                []byte(`[{"type":"function","name":"lookup"}]`),
		ParallelToolCalls:    []byte(`false`),
		Reasoning:            &dto.Reasoning{Effort: "high", Summary: "detailed"},
		ServiceTier:          "priority",
		PromptCacheKey:       []byte(`"cache-stable"`),
		PromptCacheOptions:   []byte(`{"ttl":0,"breakpoint":{"unknown":[false,0]}}`),
		PromptCacheRetention: []byte(`"24h"`),
		Text:                 []byte(`{"format":{"type":"text"}}`),
	}

	body, err := common.Marshal(request)
	require.NoError(t, err)
	assert.JSONEq(t, `{"model":"gpt-5-codex","metadata":{"trace":"batch3"},"tools":[{"type":"function","name":"lookup"}],"parallel_tool_calls":false,"reasoning":{"effort":"high","summary":"detailed"},"service_tier":"priority","prompt_cache_key":"cache-stable","prompt_cache_options":{"ttl":0,"breakpoint":{"unknown":[false,0]}},"prompt_cache_retention":"24h","text":{"format":{"type":"text"}}}`, string(body))
}

func TestCompactRequestConvertsOptionsAndLegacyRetentionIndependently(t *testing.T) {
	request := dto.OpenAIResponsesCompactionRequest{
		Model:                "gpt-5-codex",
		Metadata:             []byte(`{"trace":"kept"}`),
		Tools:                []byte(`[{"type":"function","name":"lookup"}]`),
		ParallelToolCalls:    []byte(`false`),
		Reasoning:            &dto.Reasoning{Effort: "high"},
		ServiceTier:          "priority",
		PromptCacheKey:       []byte(`"cache-stable"`),
		PromptCacheOptions:   []byte(`{"ttl":0,"breakpoint":{"unknown":true}}`),
		PromptCacheRetention: []byte(`"24h"`),
		Text:                 []byte(`{"format":{"type":"text"}}`),
	}

	converted := request.ToResponsesRequest()
	body, err := common.Marshal(converted)
	require.NoError(t, err)
	assert.JSONEq(t, `{"model":"gpt-5-codex","metadata":{"trace":"kept"},"tools":[{"type":"function","name":"lookup"}],"parallel_tool_calls":false,"reasoning":{"effort":"high"},"service_tier":"priority","prompt_cache_key":"cache-stable","prompt_cache_options":{"ttl":0,"breakpoint":{"unknown":true}},"prompt_cache_retention":"24h","text":{"format":{"type":"text"}}}`, string(body))

	request.PromptCacheOptions = nil
	converted = request.ToResponsesRequest()
	body, err = common.Marshal(converted)
	require.NoError(t, err)
	assert.NotContains(t, string(body), "prompt_cache_options")
	assert.Contains(t, string(body), `"prompt_cache_retention":"24h"`)
}
