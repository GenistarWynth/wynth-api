package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
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
}

func TestNormalizeChannelTestEndpointUsesResponsesForOpenAI(t *testing.T) {
	channel := &model.Channel{Type: constant.ChannelTypeOpenAI}

	endpointType := normalizeChannelTestEndpoint(channel, "gpt-5.4", "")
	assert.Equal(t, string(constant.EndpointTypeOpenAIResponse), endpointType)
	endpoint, ok := common.GetDefaultEndpointInfo(constant.EndpointType(endpointType))
	require.True(t, ok)
	assert.Equal(t, "/v1/responses", endpoint.Path)
}

func TestNormalizeChannelTestEndpointPreservesExplicitEndpoint(t *testing.T) {
	channel := &model.Channel{Type: constant.ChannelTypeOpenAI}

	assert.Equal(t, string(constant.EndpointTypeOpenAI), normalizeChannelTestEndpoint(channel, "gpt-5.4", string(constant.EndpointTypeOpenAI)))
}

func TestNormalizeChannelTestEndpointUsesResponsesForNativeCodex(t *testing.T) {
	codex := &model.Channel{Type: constant.ChannelTypeCodex}

	assert.Equal(t, string(constant.EndpointTypeOpenAIResponse), normalizeChannelTestEndpoint(codex, "gpt-5.4", ""))
}

func TestNormalizeChannelTestEndpointLeavesNonOpenAIUnspecified(t *testing.T) {
	anthropic := &model.Channel{Type: constant.ChannelTypeAnthropic}

	assert.Empty(t, normalizeChannelTestEndpoint(anthropic, "claude-sonnet-4", ""))
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
	channel := &model.Channel{Type: constant.ChannelTypeOpenAI}
	endpointType := normalizeChannelTestEndpoint(channel, "gpt-5.6-sol", "")
	request, ok := buildTestRequest("gpt-5.6-sol", endpointType, channel, true).(*dto.OpenAIResponsesRequest)
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

func TestResolveChannelTestStreamDefaultsOpenAIToStream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	channel := &model.Channel{Type: constant.ChannelTypeOpenAI}

	req := httptest.NewRequest(http.MethodGet, "/api/channel/test/1", nil)
	c, _ := gin.CreateTestContext(nil)
	c.Request = req
	assert.True(t, resolveChannelTestStream(c, channel))
}

func TestResolveChannelTestStreamPreservesExplicitOverride(t *testing.T) {
	gin.SetMode(gin.TestMode)
	channel := &model.Channel{Type: constant.ChannelTypeOpenAI}

	reqFalse := httptest.NewRequest(http.MethodGet, "/api/channel/test/1?stream=false", nil)
	cFalse, _ := gin.CreateTestContext(nil)
	cFalse.Request = reqFalse
	assert.False(t, resolveChannelTestStream(cFalse, channel))
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
