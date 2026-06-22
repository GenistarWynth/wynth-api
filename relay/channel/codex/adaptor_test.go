package codex

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConvertOpenAIResponsesRequestInjectsImageGenerationToolWhenForcedEnabled(t *testing.T) {
	adaptor := &Adaptor{}
	info := &relaycommon.RelayInfo{
		RelayMode: relayconstant.RelayModeResponses,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelOtherSettings: dto.ChannelOtherSettings{
				CodexImageGenerationBridgePolicy: dto.CodexImageGenerationBridgePolicyEnabled,
			},
		},
	}

	converted, err := adaptor.ConvertOpenAIResponsesRequest(nil, info, dto.OpenAIResponsesRequest{
		Model: "gpt-5.5",
	})

	require.NoError(t, err)
	request, ok := converted.(dto.OpenAIResponsesRequest)
	require.True(t, ok)
	var tools []map[string]any
	require.NoError(t, common.Unmarshal(request.Tools, &tools))
	require.Len(t, tools, 1)
	assert.Equal(t, "image_generation", tools[0]["type"])
	assert.Equal(t, "png", tools[0]["output_format"])
	var instructions string
	require.NoError(t, common.Unmarshal(request.Instructions, &instructions))
	assert.Contains(t, instructions, "image_generation")
	assert.JSONEq(t, `false`, string(request.Store))
}

func TestConvertOpenAIResponsesRequestPreservesStructuredInstructionsWhenForcedEnabled(t *testing.T) {
	adaptor := &Adaptor{}
	info := &relaycommon.RelayInfo{
		RelayMode: relayconstant.RelayModeResponses,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelOtherSettings: dto.ChannelOtherSettings{
				CodexImageGenerationBridgePolicy: dto.CodexImageGenerationBridgePolicyEnabled,
			},
		},
	}
	instructions := []byte(`[{"type":"text","text":"keep me"}]`)

	converted, err := adaptor.ConvertOpenAIResponsesRequest(nil, info, dto.OpenAIResponsesRequest{
		Model:        "gpt-5.5",
		Instructions: instructions,
	})

	require.NoError(t, err)
	request, ok := converted.(dto.OpenAIResponsesRequest)
	require.True(t, ok)
	assert.JSONEq(t, string(instructions), string(request.Instructions))
	var tools []map[string]any
	require.NoError(t, common.Unmarshal(request.Tools, &tools))
	require.Len(t, tools, 1)
	assert.Equal(t, "image_generation", tools[0]["type"])
}

func TestConvertOpenAIResponsesRequestStripsImageGenerationToolWhenForcedDisabled(t *testing.T) {
	tools, err := common.Marshal([]map[string]any{
		{"type": "web_search_preview"},
		{"type": "image_generation", "output_format": "png"},
	})
	require.NoError(t, err)
	toolChoice, err := common.Marshal(map[string]any{"type": "image_generation"})
	require.NoError(t, err)
	adaptor := &Adaptor{}
	info := &relaycommon.RelayInfo{
		RelayMode: relayconstant.RelayModeResponses,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelOtherSettings: dto.ChannelOtherSettings{
				CodexImageGenerationBridgePolicy: dto.CodexImageGenerationBridgePolicyDisabled,
			},
		},
	}

	converted, err := adaptor.ConvertOpenAIResponsesRequest(nil, info, dto.OpenAIResponsesRequest{
		Model:      "gpt-5.5",
		Tools:      tools,
		ToolChoice: toolChoice,
	})

	require.NoError(t, err)
	request, ok := converted.(dto.OpenAIResponsesRequest)
	require.True(t, ok)
	var remainingTools []map[string]any
	require.NoError(t, common.Unmarshal(request.Tools, &remainingTools))
	require.Len(t, remainingTools, 1)
	assert.Equal(t, "web_search_preview", remainingTools[0]["type"])
	assert.Empty(t, request.ToolChoice)
}

func TestConvertOpenAIResponsesRequestDoesNotApplyImageBridgeToCompact(t *testing.T) {
	adaptor := &Adaptor{}
	info := &relaycommon.RelayInfo{
		RelayMode: relayconstant.RelayModeResponsesCompact,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelOtherSettings: dto.ChannelOtherSettings{
				CodexImageGenerationBridgePolicy: dto.CodexImageGenerationBridgePolicyEnabled,
			},
		},
	}

	converted, err := adaptor.ConvertOpenAIResponsesRequest(nil, info, dto.OpenAIResponsesRequest{
		Model: "gpt-5.5",
	})

	require.NoError(t, err)
	request, ok := converted.(dto.OpenAIResponsesRequest)
	require.True(t, ok)
	assert.Empty(t, request.Tools)
	assert.Empty(t, request.Store)
}
