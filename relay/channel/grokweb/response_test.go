package grokweb

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockSSEBody reproduces the grok.com web SSE frame sequence for "Hello":
// two final-text tokens, then a finalMetadata terminator, then [DONE].
const mockSSEBody = `data: {"result":{"response":{"token":"He","isThinking":false,"messageTag":"final"}}}
data: {"result":{"response":{"token":"llo","isThinking":false,"messageTag":"final"}}}
data: {"result":{"response":{"isSoftStop":true,"finalMetadata":{"followUpSuggestions":[]}}}}
data: [DONE]
`

func newHandlerContext(t *testing.T, body string, isStream bool) (*gin.Context, *httptest.ResponseRecorder, *http.Response, *relaycommon.RelayInfo) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
	}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "grok-4.3"},
		IsStream:    isStream,
	}
	return c, rec, resp, info
}

func TestGrokWebStreamHandlerYieldsHelloAndStop(t *testing.T) {
	c, rec, resp, info := newHandlerContext(t, mockSSEBody, true)

	usage, apiErr := grokWebStreamHandler(c, info, resp)
	require.Nil(t, apiErr)
	require.NotNil(t, usage)

	out := rec.Body.String()
	// reconstruct the streamed content from the delta chunks
	var content strings.Builder
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" {
			continue
		}
		var chunk dto.ChatCompletionsStreamResponse
		if err := common.UnmarshalJsonStr(payload, &chunk); err != nil {
			continue
		}
		if len(chunk.Choices) > 0 && chunk.Choices[0].Delta.Content != nil {
			content.WriteString(*chunk.Choices[0].Delta.Content)
		}
	}
	assert.Equal(t, "Hello", content.String())
	assert.Contains(t, out, `"finish_reason":"stop"`)
	assert.Contains(t, out, "data: [DONE]")
}

func TestGrokWebHandlerNonStreamSingleCompletion(t *testing.T) {
	c, rec, resp, info := newHandlerContext(t, mockSSEBody, false)

	usage, apiErr := grokWebHandler(c, info, resp)
	require.Nil(t, apiErr)
	require.NotNil(t, usage)

	var full dto.OpenAITextResponse
	require.NoError(t, common.UnmarshalJsonStr(rec.Body.String(), &full))
	require.Len(t, full.Choices, 1)
	assert.Equal(t, "chat.completion", full.Object)
	assert.Equal(t, "Hello", full.Choices[0].Message.StringContent())
	assert.Equal(t, "stop", full.Choices[0].FinishReason)
	assert.Equal(t, "assistant", full.Choices[0].Message.Role)
}

func TestGrokWebHandlerInBandErrorIsTyped(t *testing.T) {
	body := `data: {"error":{"message":"invalid-credentials","code":16}}
`
	c, _, resp, info := newHandlerContext(t, body, false)
	_, apiErr := grokWebHandler(c, info, resp)
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusUnauthorized, apiErr.StatusCode)
}

func TestClassifyLine(t *testing.T) {
	k, p := classifyLine(`data: {"a":1}`)
	assert.Equal(t, "data", k)
	assert.Equal(t, `{"a":1}`, p)

	k, _ = classifyLine("data: [DONE]")
	assert.Equal(t, "done", k)

	k, _ = classifyLine("event: message")
	assert.Equal(t, "skip", k)

	k, p = classifyLine(`{"raw":true}`)
	assert.Equal(t, "data", k)
	assert.Equal(t, `{"raw":true}`, p)
}
