package xai

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConvertOpenAIResponsesRequestReasoningParity(t *testing.T) {
	tests := []struct {
		name       string
		model      string
		effort     string
		upstream   string
		wantModel  string
		wantEffort string
	}{
		{name: "absent", model: "grok-3-mini", wantModel: "grok-3-mini"},
		{name: "explicit low", model: "grok-3-mini", effort: "low", wantModel: "grok-3-mini", wantEffort: "low"},
		{name: "explicit high", model: "grok-3-mini", effort: "high", wantModel: "grok-3-mini", wantEffort: "high"},
		{name: "unsupported explicit survives", model: "grok-3-mini", effort: "medium", wantModel: "grok-3-mini", wantEffort: "medium"},
		{name: "suffix derived", model: "grok-3-mini-high", wantModel: "grok-3-mini", wantEffort: "high"},
		{name: "explicit takes precedence", model: "grok-3-mini-low", effort: "high", wantModel: "grok-3-mini", wantEffort: "high"},
		{name: "mapped upstream model", model: "client-alias", upstream: "grok-3-mini", effort: "low", wantModel: "grok-3-mini", wantEffort: "low"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upstreamModel := tt.upstream
			if upstreamModel == "" {
				upstreamModel = tt.model
			}
			info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: upstreamModel}}
			req := dto.OpenAIResponsesRequest{Model: tt.model}
			if tt.effort != "" {
				req.Reasoning = &dto.Reasoning{Effort: tt.effort}
			}

			converted, err := (&Adaptor{}).ConvertOpenAIResponsesRequest(nil, info, req)
			require.NoError(t, err)
			body, err := common.Marshal(converted)
			require.NoError(t, err)
			var upstream dto.OpenAIResponsesRequest
			require.NoError(t, common.Unmarshal(body, &upstream))
			assert.Equal(t, tt.wantModel, upstream.Model)
			if tt.wantEffort == "" {
				assert.Nil(t, upstream.Reasoning)
			} else {
				require.NotNil(t, upstream.Reasoning)
				assert.Equal(t, tt.wantEffort, upstream.Reasoning.Effort)
			}
			assert.Equal(t, tt.wantEffort, info.ReasoningEffort)
		})
	}
}

func TestConvertImageRequestOmitsUnsupportedFields(t *testing.T) {
	n := uint(2)
	req := dto.ImageRequest{Model: "grok-imagine-image", Prompt: "a fox", N: &n, ResponseFormat: "b64_json", Size: "1024x1024", Quality: "hd", Style: []byte(`"vivid"`)}
	converted, err := (&Adaptor{}).ConvertImageRequest(nil, nil, req)
	require.NoError(t, err)
	body, err := common.Marshal(converted)
	require.NoError(t, err)
	var upstream map[string]any
	require.NoError(t, common.Unmarshal(body, &upstream))
	assert.Equal(t, "grok-imagine-image", upstream["model"])
	assert.Equal(t, "a fox", upstream["prompt"])
	assert.Equal(t, float64(2), upstream["n"])
	assert.Equal(t, "b64_json", upstream["response_format"])
	assert.NotContains(t, upstream, "size")
	assert.NotContains(t, upstream, "quality")
	assert.NotContains(t, upstream, "style")
}

func TestAdvertisedImageModelsRouteThroughImageConversion(t *testing.T) {
	models := []string{"grok-imagine-image-pro", "grok-imagine-image", "grok-2-image-1212"}
	for _, model := range models {
		t.Run(model, func(t *testing.T) {
			assert.Contains(t, (&Adaptor{}).GetModelList(), model)
			converted, err := (&Adaptor{}).ConvertImageRequest(nil, nil, dto.ImageRequest{Model: model, Prompt: "test"})
			require.NoError(t, err)
			assert.Equal(t, model, converted.(ImageRequest).Model)
		})
	}
}
