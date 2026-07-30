package dto

import (
	"testing"

	kitutil "github.com/QuantumNous/new-api/relaykit/relayconvert/kitutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGeminiChatResponseUsageMetadataPresence(t *testing.T) {
	var missing GeminiChatResponse
	require.NoError(t, kitutil.Unmarshal([]byte(`{"candidates":[]}`), &missing))
	assert.False(t, missing.HasUsageMetadata)
	assert.Nil(t, missing.GetUsageMetadata())

	var empty GeminiChatResponse
	require.NoError(t, kitutil.Unmarshal([]byte(`{"candidates":[],"usageMetadata":{}}`), &empty))
	assert.True(t, empty.HasUsageMetadata)
	require.NotNil(t, empty.GetUsageMetadata())
	assert.False(t, HasGeminiUsageMetadataTokens(empty.GetUsageMetadata()))

	var populated GeminiChatResponse
	require.NoError(t, kitutil.Unmarshal([]byte(`{"candidates":[],"usageMetadata":{"promptTokenCount":3}}`), &populated))
	assert.True(t, populated.HasUsageMetadata)
	require.NotNil(t, populated.GetUsageMetadata())
	assert.True(t, HasGeminiUsageMetadataTokens(populated.GetUsageMetadata()))
}

func TestGeminiChatResponseMarshalKeepsUsageMetadataField(t *testing.T) {
	data, err := kitutil.Marshal(GeminiChatResponse{})
	require.NoError(t, err)
	assert.Contains(t, string(data), `"usageMetadata"`)
}

func TestGeminiChatResponseUnmarshalModelVersionSnakeCase(t *testing.T) {
	raw := []byte(`{
		"model_version":"gemini-2.5-pro-001",
		"candidates":[],
		"usageMetadata":{"totalTokenCount":0}
	}`)

	var resp GeminiChatResponse
	require.NoError(t, kitutil.Unmarshal(raw, &resp))
	assert.Equal(t, "gemini-2.5-pro-001", resp.ModelVersion)
}

func TestGeminiChatResponseUnmarshalPrefersCamelCaseAndPreservesFields(t *testing.T) {
	raw := []byte(`{
		"modelVersion":"gemini-camel",
		"model_version":"gemini-snake",
		"candidates":[],
		"usageMetadata":{"totalTokenCount":7}
	}`)

	var resp GeminiChatResponse
	require.NoError(t, kitutil.Unmarshal(raw, &resp))
	assert.Equal(t, "gemini-camel", resp.ModelVersion)
	assert.Equal(t, 7, resp.UsageMetadata.TotalTokenCount)
}
