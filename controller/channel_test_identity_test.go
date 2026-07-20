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

func TestShouldUseStreamForAutomaticChannelTestForcesCodexCLIIdentity(t *testing.T) {
	channel := &model.Channel{Type: constant.ChannelTypeOpenAI}
	channel.SetOtherSettings(dto.ChannelOtherSettings{
		ClientIdentityPreset: dto.ClientIdentityPresetCodexCLI,
	})
	assert.True(t, shouldUseStreamForAutomaticChannelTest(channel))
}

func TestResolveChannelTestStreamDefaultsCodexCLIIdentityToStream(t *testing.T) {
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
	assert.False(t, resolveChannelTestStream(c, normal))
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

