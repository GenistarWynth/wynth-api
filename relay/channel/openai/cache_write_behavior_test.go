package openai

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGPT56AliasRoutesToSol(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI, UpstreamModelName: "gpt-5.6"}}
	chat := &dto.GeneralOpenAIRequest{Model: "gpt-5.6"}
	_, err := (&Adaptor{}).ConvertOpenAIRequest(ctx, info, chat)
	require.NoError(t, err)
	assert.Equal(t, "gpt-5.6-sol", chat.Model)
	assert.Equal(t, "gpt-5.6-sol", info.UpstreamModelName)

	info.UpstreamModelName = "gpt-5.6"
	converted, err := (&Adaptor{}).ConvertOpenAIResponsesRequest(ctx, info, dto.OpenAIResponsesRequest{Model: "gpt-5.6"})
	require.NoError(t, err)
	responses, ok := converted.(dto.OpenAIResponsesRequest)
	require.True(t, ok)
	assert.Equal(t, "gpt-5.6-sol", responses.Model)
	assert.Equal(t, "gpt-5.6-sol", info.UpstreamModelName)
}

func TestResponsesHandlersPreserveBothCacheCreationRepresentations(t *testing.T) {
	body := `{"usage":{"input_tokens":10,"output_tokens":1,"total_tokens":11,"input_tokens_details":{"cached_tokens":2,"cached_creation_tokens":4,"cache_write_tokens":7}}}`
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	resp := &http.Response{Body: io.NopCloser(strings.NewReader(body))}
	usage, apiErr := OaiResponsesHandler(ctx, &relaycommon.RelayInfo{}, resp)
	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	assert.Equal(t, 4, usage.PromptTokensDetails.CachedCreationTokens)
	assert.Equal(t, 7, usage.PromptTokensDetails.CacheWriteTokens)
	assert.Equal(t, 7, usage.PromptTokensDetails.CacheCreationTokensTotal())
}

func TestStreamingResponsesPreservesBothCacheCreationRepresentations(t *testing.T) {
	body := "data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":10,\"output_tokens\":1,\"total_tokens\":11,\"input_tokens_details\":{\"cached_tokens\":2,\"cached_creation_tokens\":9,\"cache_write_tokens\":7}}}}\n\n"
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	resp := &http.Response{Body: io.NopCloser(strings.NewReader(body))}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-5.6-sol"}}
	oldStreamingTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldStreamingTimeout })
	usage, apiErr := OaiResponsesStreamHandler(ctx, info, resp)
	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	assert.Equal(t, 9, usage.PromptTokensDetails.CachedCreationTokens)
	assert.Equal(t, 7, usage.PromptTokensDetails.CacheWriteTokens)
	assert.Equal(t, 9, usage.PromptTokensDetails.CacheCreationTokensTotal())
}

func TestCompactHandlerPreservesBothCacheCreationRepresentations(t *testing.T) {
	body := `{"usage":{"input_tokens":10,"output_tokens":1,"total_tokens":11,"input_tokens_details":{"cached_tokens":2,"cached_creation_tokens":8,"cache_write_tokens":7}}}`
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	resp := &http.Response{Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
	usage, apiErr := OaiResponsesCompactionHandler(ctx, resp)
	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	assert.Equal(t, 8, usage.PromptTokensDetails.CachedCreationTokens)
	assert.Equal(t, 7, usage.PromptTokensDetails.CacheWriteTokens)
	assert.Equal(t, 8, usage.PromptTokensDetails.CacheCreationTokensTotal())
}
