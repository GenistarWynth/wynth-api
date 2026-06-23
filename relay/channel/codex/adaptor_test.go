package codex

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"

	"github.com/gin-gonic/gin"
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

func TestSetupRequestHeaderUsesLegacyOAuthJSONKey(t *testing.T) {
	adaptor := &Adaptor{}
	header := http.Header{}
	key, err := common.Marshal(map[string]string{
		"access_token": "legacy-access-token",
		"account_id":   "legacy-account-id",
	})
	require.NoError(t, err)
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ApiKey: string(key),
		},
	}

	err = adaptor.SetupRequestHeader(newCodexHeaderTestContext(), &header, info)

	require.NoError(t, err)
	assert.Equal(t, "Bearer legacy-access-token", header.Get("Authorization"))
	assert.Equal(t, "legacy-account-id", header.Get("chatgpt-account-id"))
	assert.Equal(t, "responses=experimental", header.Get("OpenAI-Beta"))
	assert.Equal(t, "codex_cli_rs", header.Get("originator"))
	assert.Equal(t, "application/json", header.Get("Content-Type"))
	assert.Equal(t, "application/json", header.Get("Accept"))
}

func TestSetupRequestHeaderUsesRuntimeRawTokenWithRuntimeAccountID(t *testing.T) {
	adaptor := &Adaptor{}
	header := http.Header{}
	info := &relaycommon.RelayInfo{
		RuntimeAccountID: "runtime-account-id",
		ChannelMeta: &relaycommon.ChannelMeta{
			ApiKey: "runtime-access-token",
		},
	}

	err := adaptor.SetupRequestHeader(newCodexHeaderTestContext(), &header, info)

	require.NoError(t, err)
	assert.Equal(t, "Bearer runtime-access-token", header.Get("Authorization"))
	assert.Equal(t, "runtime-account-id", header.Get("chatgpt-account-id"))
}

func TestSetupRequestHeaderPrefersRuntimeAccountIDForRuntimeJSONKey(t *testing.T) {
	adaptor := &Adaptor{}
	header := http.Header{}
	key, err := common.Marshal(map[string]string{
		"access_token": "json-runtime-access-token",
		"account_id":   "stored-account-id",
	})
	require.NoError(t, err)
	info := &relaycommon.RelayInfo{
		RuntimeAccountID: "runtime-account-id",
		ChannelMeta: &relaycommon.ChannelMeta{
			ApiKey: string(key),
		},
	}

	err = adaptor.SetupRequestHeader(newCodexHeaderTestContext(), &header, info)

	require.NoError(t, err)
	assert.Equal(t, "Bearer json-runtime-access-token", header.Get("Authorization"))
	assert.Equal(t, "runtime-account-id", header.Get("chatgpt-account-id"))
}

func TestSetupRequestHeaderRejectsRuntimeRawTokenWithoutRuntimeAccountID(t *testing.T) {
	adaptor := &Adaptor{}
	header := http.Header{}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ApiKey: "runtime-access-token",
		},
	}

	err := adaptor.SetupRequestHeader(newCodexHeaderTestContext(), &header, info)

	require.ErrorContains(t, err, "account_id is required")
}

func newCodexHeaderTestContext() *gin.Context {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	return ctx
}
