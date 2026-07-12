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
		Model:             "gpt-5-codex",
		Metadata:          []byte(`{"trace":"batch3"}`),
		Tools:             []byte(`[{"type":"function","name":"lookup"}]`),
		ParallelToolCalls: []byte(`false`),
		Reasoning:         &dto.Reasoning{Effort: "high", Summary: "detailed"},
		ServiceTier:       "priority",
		PromptCacheKey:    []byte(`"cache-stable"`),
		Text:              []byte(`{"format":{"type":"text"}}`),
	}

	body, err := common.Marshal(request)
	require.NoError(t, err)
	assert.JSONEq(t, `{"model":"gpt-5-codex","metadata":{"trace":"batch3"},"tools":[{"type":"function","name":"lookup"}],"parallel_tool_calls":false,"reasoning":{"effort":"high","summary":"detailed"},"service_tier":"priority","prompt_cache_key":"cache-stable","text":{"format":{"type":"text"}}}`, string(body))
}
