package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeChannelTestEndpointUsesResponsesForCodexCLIIdentity(t *testing.T) {
	channel := &model.Channel{Type: constant.ChannelTypeOpenAI}
	channel.SetOtherSettings(dto.ChannelOtherSettings{
		ClientIdentityPreset: dto.ClientIdentityPresetCodexCLI,
	})

	assert.Equal(t, string(constant.EndpointTypeOpenAIResponse), normalizeChannelTestEndpoint(channel, "gpt-5.4", ""))
	// Explicit endpoint still wins when caller supplies one.
	assert.Equal(t, string(constant.EndpointTypeOpenAI), normalizeChannelTestEndpoint(channel, "gpt-5.4", string(constant.EndpointTypeOpenAI)))

	// Native Codex channel type still maps to responses.
	codex := &model.Channel{Type: constant.ChannelTypeCodex}
	assert.Equal(t, string(constant.EndpointTypeOpenAIResponse), normalizeChannelTestEndpoint(codex, "gpt-5.4", ""))
}

func TestBuildTestRequestUsesCodexCLIResponsesShape(t *testing.T) {
	channel := &model.Channel{Type: constant.ChannelTypeOpenAI}
	channel.SetOtherSettings(dto.ChannelOtherSettings{
		ClientIdentityPreset: dto.ClientIdentityPresetCodexCLI,
	})

	request, ok := buildTestRequest("gpt-5.6-sol", string(constant.EndpointTypeOpenAIResponse), channel, true).(*dto.OpenAIResponsesRequest)
	require.True(t, ok)
	assert.True(t, *request.Stream)
	assert.JSONEq(t, `[{"role":"user","content":[{"type":"input_text","text":"hi"}]}]`, string(request.Input))
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

func TestBuildTestRequestKeepsSimpleResponsesShapeWithoutCodexCLIIdentity(t *testing.T) {
	request, ok := buildTestRequest("gpt-5.6-sol", string(constant.EndpointTypeOpenAIResponse), &model.Channel{Type: constant.ChannelTypeOpenAI}, true).(*dto.OpenAIResponsesRequest)
	require.True(t, ok)

	assert.True(t, *request.Stream)
	assert.JSONEq(t, `[{"role":"user","content":"hi"}]`, string(request.Input))
	assert.Empty(t, request.Instructions)
	assert.Empty(t, request.Store)
	assert.Empty(t, request.Tools)
	assert.Empty(t, request.PromptCacheKey)
	assert.Nil(t, request.Reasoning)
}

func TestShouldUseStreamForAutomaticChannelTestForcesCodexCLIIdentity(t *testing.T) {
	channel := &model.Channel{Type: constant.ChannelTypeOpenAI}
	channel.SetOtherSettings(dto.ChannelOtherSettings{
		ClientIdentityPreset: dto.ClientIdentityPresetCodexCLI,
	})
	assert.True(t, shouldUseStreamForAutomaticChannelTest(channel))
}

func TestResolveChannelTestStreamDefaultsToStream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	channel := &model.Channel{Type: constant.ChannelTypeOpenAI}
	channel.SetOtherSettings(dto.ChannelOtherSettings{
		ClientIdentityPreset: dto.ClientIdentityPresetCodexCLI,
	})

	req := httptest.NewRequest(http.MethodGet, "/api/channel/test/1", nil)
	c, _ := gin.CreateTestContext(nil)
	c.Request = req
	assert.True(t, resolveChannelTestStream(c, channel))

	reqFalse := httptest.NewRequest(http.MethodGet, "/api/channel/test/1?stream=false", nil)
	cFalse, _ := gin.CreateTestContext(nil)
	cFalse.Request = reqFalse
	assert.False(t, resolveChannelTestStream(cFalse, channel))

	normal := &model.Channel{Type: constant.ChannelTypeOpenAI}
	assert.True(t, resolveChannelTestStream(c, normal))
}

func TestNormalizeChannelTestEndpointUsesMessagesForClaudeCodeIdentity(t *testing.T) {
	channel := &model.Channel{Type: constant.ChannelTypeOpenAI}
	channel.SetOtherSettings(dto.ChannelOtherSettings{
		ClientIdentityPreset: dto.ClientIdentityPresetClaudeCode,
	})
	assert.Equal(t, string(constant.EndpointTypeAnthropic), normalizeChannelTestEndpoint(channel, "claude-sonnet-4", ""))
}

func TestShouldUseStreamForAutomaticChannelTestForcesClaudeCodeIdentity(t *testing.T) {
	channel := &model.Channel{Type: constant.ChannelTypeOpenAI}
	channel.SetOtherSettings(dto.ChannelOtherSettings{
		ClientIdentityPreset: dto.ClientIdentityPresetClaudeCode,
	})
	assert.True(t, shouldUseStreamForAutomaticChannelTest(channel))
}

func TestResolveChannelTestStreamDefaultsClaudeCodeIdentityToStream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	channel := &model.Channel{Type: constant.ChannelTypeOpenAI}
	channel.SetOtherSettings(dto.ChannelOtherSettings{
		ClientIdentityPreset: dto.ClientIdentityPresetClaudeCode,
	})
	req := httptest.NewRequest(http.MethodGet, "/api/channel/test/1", nil)
	c, _ := gin.CreateTestContext(nil)
	c.Request = req
	assert.True(t, resolveChannelTestStream(c, channel))
}
