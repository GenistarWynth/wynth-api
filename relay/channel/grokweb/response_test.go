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
	"github.com/QuantumNous/new-api/service"
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

// mockReasoningSSE interleaves expert-mode reasoning (isThinking) tokens with final
// content, mirroring grok's "think" stream.
const mockReasoningSSE = `data: {"result":{"response":{"token":"let me think","isThinking":true}}}
data: {"result":{"response":{"token":" about it","isThinking":true}}}
data: {"result":{"response":{"token":"Answer","isThinking":false,"messageTag":"final"}}}
data: {"result":{"response":{"isSoftStop":true,"finalMetadata":{}}}}
data: [DONE]
`

func TestGrokWebStreamHandlerSurfacesReasoning(t *testing.T) {
	c, rec, resp, info := newHandlerContext(t, mockReasoningSSE, true)
	usage, apiErr := grokWebStreamHandler(c, info, resp)
	require.Nil(t, apiErr)
	require.NotNil(t, usage)

	var content, reasoning strings.Builder
	for _, line := range strings.Split(rec.Body.String(), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" {
			continue
		}
		var chunk dto.ChatCompletionsStreamResponse
		if err := common.UnmarshalJsonStr(payload, &chunk); err != nil || len(chunk.Choices) == 0 {
			continue
		}
		if chunk.Choices[0].Delta.Content != nil {
			content.WriteString(*chunk.Choices[0].Delta.Content)
		}
		if chunk.Choices[0].Delta.ReasoningContent != nil {
			reasoning.WriteString(*chunk.Choices[0].Delta.ReasoningContent)
		}
	}
	assert.Equal(t, "Answer", content.String())
	assert.Equal(t, "let me think about it", reasoning.String(), "reasoning tokens must be surfaced as reasoning_content")
}

func TestGrokWebHandlerNonStreamSetsReasoningContent(t *testing.T) {
	c, rec, resp, info := newHandlerContext(t, mockReasoningSSE, false)
	_, apiErr := grokWebHandler(c, info, resp)
	require.Nil(t, apiErr)

	var full dto.OpenAITextResponse
	require.NoError(t, common.UnmarshalJsonStr(rec.Body.String(), &full))
	require.Len(t, full.Choices, 1)
	assert.Equal(t, "Answer", full.Choices[0].Message.StringContent())
	assert.Equal(t, "let me think about it", full.Choices[0].Message.GetReasoningContent())
}

// mockDeepSearchSSE carries final content plus a webSearchResults frame.
const mockDeepSearchSSE = `data: {"result":{"response":{"token":"result text","isThinking":false,"messageTag":"final"}}}
data: {"result":{"response":{"webSearchResults":{"results":[{"url":"https://example.com","title":"Example Site"},{"url":"https://example.com","title":"dup"}]}}}}
data: {"result":{"response":{"isSoftStop":true,"finalMetadata":{}}}}
data: [DONE]
`

func TestGrokWebStreamHandlerDeepSearchAppendsSources(t *testing.T) {
	c, rec, resp, info := newHandlerContext(t, mockDeepSearchSSE, true)
	info.ChannelMeta.UpstreamModelName = "grok-4-deepsearch"
	_, apiErr := grokWebStreamHandler(c, info, resp)
	require.Nil(t, apiErr)

	out := rec.Body.String()
	assert.Contains(t, out, "## Sources")
	assert.Contains(t, out, "[Example Site](https://example.com)")
	// Deduped: the duplicate URL must appear only once.
	assert.Equal(t, 1, strings.Count(out, "https://example.com)"))
}

func TestGrokWebHandlerDeepSearchNonStreamAppendsSources(t *testing.T) {
	c, rec, resp, info := newHandlerContext(t, mockDeepSearchSSE, false)
	info.ChannelMeta.UpstreamModelName = "grok-4-deepsearch"
	_, apiErr := grokWebHandler(c, info, resp)
	require.Nil(t, apiErr)

	var full dto.OpenAITextResponse
	require.NoError(t, common.UnmarshalJsonStr(rec.Body.String(), &full))
	content := full.Choices[0].Message.StringContent()
	assert.Contains(t, content, "result text")
	assert.Contains(t, content, "## Sources")
	assert.Contains(t, content, "[Example Site](https://example.com)")
}

func TestGrokWebHandlerNonDeepSearchModelOmitsSources(t *testing.T) {
	// A normal (non-deep-search) model must NOT have its output shape changed even
	// when the upstream returns webSearchResults.
	c, rec, resp, info := newHandlerContext(t, mockDeepSearchSSE, false)
	info.ChannelMeta.UpstreamModelName = "grok-4.3"
	_, apiErr := grokWebHandler(c, info, resp)
	require.Nil(t, apiErr)

	var full dto.OpenAITextResponse
	require.NoError(t, common.UnmarshalJsonStr(rec.Body.String(), &full))
	assert.NotContains(t, full.Choices[0].Message.StringContent(), "## Sources")
}

// A stream of ONLY reasoning tokens (no final content, no terminator) must not be
// treated as an empty upstream response.
func TestGrokWebStreamHandlerReasoningOnlyNotEmpty(t *testing.T) {
	body := "data: {\"result\":{\"response\":{\"token\":\"pondering\",\"isThinking\":true}}}\ndata: [DONE]\n"
	c, rec, resp, info := newHandlerContext(t, body, true)
	usage, apiErr := grokWebStreamHandler(c, info, resp)
	require.Nil(t, apiErr, "a reasoning-only stream must not be treated as empty")
	require.NotNil(t, usage)
	assert.Contains(t, rec.Body.String(), "pondering")
}

// The injected "## Sources" markdown must NOT be billed as completion tokens.
func TestGrokWebHandlerDeepSearchSourcesNotBilled(t *testing.T) {
	c, _, resp, info := newHandlerContext(t, mockDeepSearchSSE, false)
	info.ChannelMeta.UpstreamModelName = "grok-4-deepsearch"
	usage, apiErr := grokWebHandler(c, info, resp)
	require.Nil(t, apiErr)
	want := service.CountTextToken("result text", "grok-4-deepsearch")
	assert.Equal(t, want, usage.(*dto.Usage).CompletionTokens,
		"the injected ## Sources markdown must not be billed as completion tokens")
}

func TestBuildSourcesSection(t *testing.T) {
	assert.Equal(t, "", buildSourcesSection(nil))
	out := buildSourcesSection([]grokSource{
		{URL: "https://a.com", Title: "Alpha"},
		{URL: "https://b.com", Title: ""}, // empty title falls back to the URL
	})
	assert.Contains(t, out, "## Sources")
	idxA := strings.Index(out, "[Alpha](https://a.com)")
	idxB := strings.Index(out, "[https://b.com](https://b.com)")
	require.NotEqual(t, -1, idxA)
	require.NotEqual(t, -1, idxB)
	assert.Less(t, idxA, idxB, "sources preserve arrival order")
}

// Locks the intentional divergence from grok2api's strict tag=="final": grok emits
// untagged tokens carrying user-facing content, so an empty messageTag is accepted as
// content (not dropped).
func TestGrokWebHandlerEmptyMessageTagTreatedAsContent(t *testing.T) {
	body := "data: {\"result\":{\"response\":{\"token\":\"untagged text\",\"isThinking\":false}}}\n" +
		"data: {\"result\":{\"response\":{\"isSoftStop\":true,\"finalMetadata\":{}}}}\ndata: [DONE]\n"
	c, rec, resp, info := newHandlerContext(t, body, false)
	_, apiErr := grokWebHandler(c, info, resp)
	require.Nil(t, apiErr)
	var full dto.OpenAITextResponse
	require.NoError(t, common.UnmarshalJsonStr(rec.Body.String(), &full))
	assert.Equal(t, "untagged text", full.Choices[0].Message.StringContent())
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
