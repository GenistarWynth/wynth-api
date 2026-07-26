package openai

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func runDirectResponsesStream(t *testing.T, body string) (*httptest.ResponseRecorder, *relaycommon.RelayInfo, *types.NewAPIError) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
	info := &relaycommon.RelayInfo{
		DisablePing: true,
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-5.6-sol"},
	}
	previousTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() {
		constant.StreamingTimeout = previousTimeout
	})

	_, apiErr := OaiResponsesStreamHandler(c, info, resp)
	return recorder, info, apiErr
}

func runDirectResponses(t *testing.T, body string) (*httptest.ResponseRecorder, *types.NewAPIError) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
	_, apiErr := OaiResponsesHandler(c, &relaycommon.RelayInfo{}, resp)
	return recorder, apiErr
}

func responsesSSE(events ...string) string {
	var body strings.Builder
	for _, event := range events {
		body.WriteString("data: ")
		body.WriteString(event)
		body.WriteString("\n\n")
	}
	return body.String()
}

func TestResponsesPreludeWriterBuffersHeartbeatUntilCommit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	writer := newResponsesPreludeWriter(c.Writer, 64)
	c.Writer = writer

	require.NoError(t, helper.PingData(c))
	assert.Empty(t, recorder.Body.String())

	require.NoError(t, writer.Commit())
	assert.Equal(t, ": PING\n\n", recorder.Body.String())
}

func TestOaiResponsesStreamHandlerClassifiesSemanticFailures(t *testing.T) {
	const secret = "sk-production-secret"
	tests := []struct {
		name       string
		event      string
		wantStatus int
		wantCode   types.ErrorCode
	}{
		{
			name:       "rate limit",
			event:      `{"type":"response.failed","response":{"status":"failed","error":{"message":"Concurrency limit exceeded Authorization: Bearer ` + secret + `","type":"requests","code":"rate_limit_exceeded"}}}`,
			wantStatus: http.StatusTooManyRequests,
			wantCode:   "rate_limit_exceeded",
		},
		{
			name:       "server error",
			event:      `{"type":"response.failed","response":{"status":"failed","error":{"message":"provider unavailable","type":"server_error","code":"server_error"}}}`,
			wantStatus: http.StatusBadGateway,
			wantCode:   "server_error",
		},
		{
			name:       "invalid request",
			event:      `{"type":"response.error","error":{"message":"invalid tool schema","type":"invalid_request_error","code":"invalid_request_error"}}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_request_error",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder, info, apiErr := runDirectResponsesStream(t, responsesSSE(test.event))

			require.NotNil(t, apiErr)
			assert.Equal(t, test.wantStatus, apiErr.StatusCode)
			assert.Equal(t, test.wantCode, apiErr.GetErrorCode())
			assert.Empty(t, recorder.Body.String())
			assert.False(t, info.HasSendResponse())
			assert.NotContains(t, apiErr.Error(), secret)
		})
	}
}

func TestOaiResponsesStreamHandlerAcceptsMeaningfulTerminalResponses(t *testing.T) {
	tests := []struct {
		name     string
		response string
	}{
		{
			name:     "text",
			response: `{"id":"resp-text","status":"completed","model":"gpt-5.6-sol","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"final-only text"}]}],"usage":{"input_tokens":1,"output_tokens":2,"total_tokens":3}}`,
		},
		{
			name:     "tool call only",
			response: `{"id":"resp-tool","status":"completed","model":"gpt-5.6-sol","output":[{"type":"function_call","id":"fc_1","call_id":"call_1","name":"lookup","arguments":"{\"q\":\"weather\"}"}],"usage":{"input_tokens":1,"output_tokens":2,"total_tokens":3}}`,
		},
		{
			name:     "refusal",
			response: `{"id":"resp-refusal","status":"completed","model":"gpt-5.6-sol","output":[{"type":"message","role":"assistant","content":[{"type":"refusal","refusal":"I cannot help with that."}]}],"usage":{"input_tokens":1,"output_tokens":2,"total_tokens":3}}`,
		},
		{
			name:     "image generation",
			response: `{"id":"resp-image","status":"completed","model":"gpt-5.6-sol","output":[{"type":"image_generation_call","id":"ig_1","status":"completed","result":"aW1hZ2U="}],"usage":{"input_tokens":1,"output_tokens":2,"total_tokens":3}}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			event := `{"type":"response.completed","response":` + test.response + `}`
			recorder, _, apiErr := runDirectResponsesStream(t, responsesSSE(event))

			require.Nil(t, apiErr)
			assert.Contains(t, recorder.Body.String(), event)
		})
	}
}

func TestOaiResponsesStreamHandlerPreservesSuccessfulEventOrder(t *testing.T) {
	events := []string{
		`{"type":"response.created","response":{"id":"resp-order","status":"in_progress","model":"gpt-5.6-sol"}}`,
		`{"type":"response.in_progress","response":{"id":"resp-order","status":"in_progress","model":"gpt-5.6-sol"}}`,
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"msg-1","role":"assistant","content":[]}}`,
		`{"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"hello"}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"type":"message","id":"msg-1","role":"assistant","content":[{"type":"output_text","text":"hello"}]}}`,
		`{"type":"response.completed","response":{"id":"resp-order","status":"completed","model":"gpt-5.6-sol","output":[{"type":"message","id":"msg-1","role":"assistant","content":[{"type":"output_text","text":"hello"}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`,
	}
	recorder, _, apiErr := runDirectResponsesStream(t, responsesSSE(events...))

	require.Nil(t, apiErr)
	body := recorder.Body.String()
	lastIndex := -1
	for _, event := range events {
		index := strings.Index(body, event)
		require.Greater(t, index, lastIndex, "event order changed for %s", event)
		lastIndex = index
	}
}

func TestOaiResponsesStreamHandlerRejectsReasoningOnlyCompletion(t *testing.T) {
	body := responsesSSE(
		`{"type":"response.reasoning_summary_text.delta","delta":"internal reasoning"}`,
		`{"type":"response.completed","response":{"id":"resp-reasoning","status":"completed","model":"gpt-5.6-sol","output":[{"type":"reasoning","content":[{"type":"summary_text","text":"internal reasoning"}]}],"usage":{"input_tokens":1,"output_tokens":2,"total_tokens":3}}}`,
	)

	recorder, info, apiErr := runDirectResponsesStream(t, body)

	require.NotNil(t, apiErr)
	assert.Equal(t, types.ErrorCodeEmptyResponse, apiErr.GetErrorCode())
	assert.Empty(t, recorder.Body.String())
	assert.False(t, info.HasSendResponse())
}

func TestOaiResponsesStreamHandlerBoundsMetadataPrelude(t *testing.T) {
	oversizedMetadata := strings.Repeat("x", 2<<20)
	body := responsesSSE(`{"type":"response.created","response":{"id":"resp-large","status":"in_progress","metadata":{"padding":"` + oversizedMetadata + `"}}}`)

	recorder, info, apiErr := runDirectResponsesStream(t, body)

	require.NotNil(t, apiErr)
	assert.Equal(t, types.ErrorCodeBadResponseBody, apiErr.GetErrorCode())
	assert.Empty(t, recorder.Body.String())
	assert.False(t, info.HasSendResponse())
}

func TestOaiResponsesHandlerRejectsSemanticFailureBeforeWrite(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantStatus int
		wantCode   types.ErrorCode
	}{
		{
			name:       "failed",
			body:       `{"id":"resp-failed","status":"failed","error":{"message":"capacity exhausted","type":"server_error","code":"server_error"},"output":[]}`,
			wantStatus: http.StatusBadGateway,
			wantCode:   "server_error",
		},
		{
			name:       "incomplete empty",
			body:       `{"id":"resp-incomplete","status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":[]}`,
			wantStatus: http.StatusBadGateway,
			wantCode:   types.ErrorCodeEmptyResponse,
		},
		{
			name:       "completed empty",
			body:       `{"id":"resp-empty","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":0,"total_tokens":1}}`,
			wantStatus: http.StatusBadGateway,
			wantCode:   types.ErrorCodeEmptyResponse,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder, apiErr := runDirectResponses(t, test.body)

			require.NotNil(t, apiErr)
			assert.Equal(t, test.wantStatus, apiErr.StatusCode)
			assert.Equal(t, test.wantCode, apiErr.GetErrorCode())
			assert.Empty(t, recorder.Body.String())
		})
	}
}

func TestOaiResponsesHandlerPreservesMeaningfulResponses(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{
			name: "text",
			body: `{"id":"resp-text","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello"}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`,
		},
		{
			name: "tool call only",
			body: `{"id":"resp-tool","status":"completed","output":[{"type":"function_call","id":"fc_1","call_id":"call_1","name":"lookup","arguments":"{}"}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`,
		},
		{
			name: "refusal",
			body: `{"id":"resp-refusal","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"refusal","refusal":"no"}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`,
		},
		{
			name: "image generation",
			body: `{"id":"resp-image","status":"completed","output":[{"type":"image_generation_call","id":"ig_1","status":"completed","result":"aW1hZ2U="}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`,
		},
		{
			name: "incomplete with text",
			body: `{"id":"resp-incomplete-text","status":"incomplete","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"partial"}]}],"incomplete_details":{"reason":"max_output_tokens"},"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder, apiErr := runDirectResponses(t, test.body)

			require.Nil(t, apiErr)
			assert.Equal(t, test.body, recorder.Body.String())
		})
	}
}
