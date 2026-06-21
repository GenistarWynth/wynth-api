package dto

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGeminiChatResponseUnmarshalModelVersionSnakeCase(t *testing.T) {
	raw := []byte(`{
		"model_version":"gemini-2.5-pro-001",
		"candidates":[],
		"usageMetadata":{"totalTokenCount":0}
	}`)

	var resp GeminiChatResponse
	require.NoError(t, common.Unmarshal(raw, &resp))

	assert.Equal(t, "gemini-2.5-pro-001", resp.ModelVersion)
}
