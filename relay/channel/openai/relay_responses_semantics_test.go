package openai

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type cancelRequestOnCloseBody struct {
	io.Reader
	cancel context.CancelFunc
}

type countingReadCloser struct {
	reader    io.Reader
	bytesRead atomic.Int64
}

func (b *countingReadCloser) Read(data []byte) (int, error) {
	n, err := b.reader.Read(data)
	b.bytesRead.Add(int64(n))
	return n, err
}

func (b *countingReadCloser) Close() error {
	return nil
}

type cancelOnFirstReadBody struct {
	reader io.Reader
	cancel context.CancelFunc
	once   sync.Once
}

func (b *cancelOnFirstReadBody) Read(data []byte) (int, error) {
	n, err := b.reader.Read(data)
	if n > 0 {
		b.once.Do(b.cancel)
	}
	return n, err
}

func (b *cancelOnFirstReadBody) Close() error {
	return nil
}

func (b *cancelRequestOnCloseBody) Close() error {
	b.cancel()
	return nil
}

func runDirectResponsesStream(t *testing.T, body string) (*httptest.ResponseRecorder, *relaycommon.RelayInfo, *types.NewAPIError) {
	return runDirectResponsesStreamWithRequest(t, body, nil)
}

func runDirectResponsesStreamWithRequest(t *testing.T, body string, request dto.Request) (*httptest.ResponseRecorder, *relaycommon.RelayInfo, *types.NewAPIError) {
	return runDirectResponsesStreamWithBody(t, io.NopCloser(strings.NewReader(body)), request, context.Background())
}

func runDirectResponsesStreamWithBody(
	t *testing.T,
	body io.ReadCloser,
	request dto.Request,
	requestContext context.Context,
) (*httptest.ResponseRecorder, *relaycommon.RelayInfo, *types.NewAPIError) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(requestContext)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       body,
	}
	info := &relaycommon.RelayInfo{
		DisablePing: true,
		Request:     request,
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

func remoteCompactionV2Request() *dto.OpenAIResponsesRequest {
	return &dto.OpenAIResponsesRequest{
		Model: "gpt-5.6-sol",
		Input: []byte(`[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"Retained conversation text"}]},
			{"type":"compaction_trigger"}
		]`),
		ClientMetadata: []byte(`{
			"x-codex-installation-id":"install-test",
			"session_id":"session-test",
			"thread_id":"thread-test",
			"turn_id":"turn-test",
			"x-codex-window-id":"thread-test:0",
			"x-codex-turn-metadata":"{\"installation_id\":\"install-test\",\"session_id\":\"session-test\",\"thread_id\":\"thread-test\",\"turn_id\":\"turn-test\",\"window_id\":\"thread-test:0\",\"request_kind\":\"compaction\",\"compaction\":{\"trigger\":\"manual\",\"reason\":\"user_requested\",\"implementation\":\"responses_compaction_v2\",\"phase\":\"standalone_turn\",\"strategy\":\"memento\"}}"
		}`),
	}
}

func compactionTriggerWithoutMetadataRequest() *dto.OpenAIResponsesRequest {
	return &dto.OpenAIResponsesRequest{
		Model: "gpt-5.6-sol",
		Input: []byte(`[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"ordinary request"}]},
			{"type":"compaction_trigger"}
		]`),
	}
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

func TestOaiResponsesStreamHandlerFinalTriggerWithoutMetadataUsesOrdinarySemantics(t *testing.T) {
	normalMessage := `{"type":"response.output_item.done","output_index":0,"item":{"id":"msg-ordinary","type":"message","role":"assistant","content":[{"type":"output_text","text":"ordinary response"}]}}`
	completed := `{"type":"response.completed","response":{"id":"resp-ordinary","status":"completed","model":"gpt-5.6-sol","output":[{"id":"msg-ordinary","type":"message","role":"assistant","content":[{"type":"output_text","text":"ordinary response"}]}],"usage":{"input_tokens":2,"output_tokens":2,"total_tokens":4}}}`

	recorder, _, apiErr := runDirectResponsesStreamWithRequest(
		t,
		responsesSSE(normalMessage, completed),
		compactionTriggerWithoutMetadataRequest(),
	)

	require.Nil(t, apiErr)
	assert.Contains(t, recorder.Body.String(), `"text":"ordinary response"`)
	assert.Contains(t, recorder.Body.String(), `"type":"response.completed"`)
}

func TestOaiResponsesStreamHandlerRemoteCompactionV2Semantics(t *testing.T) {
	validCompaction := `{"type":"response.output_item.done","output_index":0,"item":{"id":"cmp-1","type":"compaction","encrypted_content":"ENCRYPTED_CONTEXT_COMPACTION_SUMMARY"}}`
	emptyEncryptedCompaction := `{"type":"response.output_item.done","output_index":0,"item":{"id":"cmp-empty","type":"compaction","encrypted_content":""}}`
	legacyCompactionAlias := `{"type":"response.output_item.done","output_index":0,"item":{"id":"cmp-legacy","type":"compaction_summary","encrypted_content":"LEGACY_ENCRYPTED_CONTEXT"}}`
	normalMessage := `{"type":"response.output_item.done","output_index":0,"item":{"id":"msg-1","type":"message","role":"assistant","content":[{"type":"output_text","text":"IGNORED_COMPACT_REPLY"}]}}`
	completed := `{"type":"response.completed","response":{"id":"resp-compact","status":"completed","model":"gpt-5.6-sol","usage":{"input_tokens":10,"output_tokens":2,"total_tokens":12}}}`

	tests := []struct {
		name       string
		events     []string
		wantErr    bool
		wantInBody string
	}{
		{
			name:       "one compaction",
			events:     []string{validCompaction, completed},
			wantInBody: `"encrypted_content":"ENCRYPTED_CONTEXT_COMPACTION_SUMMARY"`,
		},
		{
			name:       "one compaction with an additional ordinary item",
			events:     []string{normalMessage, validCompaction, completed},
			wantInBody: `"type":"compaction"`,
		},
		{
			name:       "legacy compaction summary alias",
			events:     []string{legacyCompactionAlias, completed},
			wantInBody: `"type":"compaction_summary"`,
		},
		{
			name:       "empty encrypted content is still a compaction",
			events:     []string{emptyEncryptedCompaction, completed},
			wantInBody: `"encrypted_content":""`,
		},
		{
			name: "empty response id is accepted by Codex",
			events: []string{
				validCompaction,
				`{"type":"response.completed","response":{"id":"","status":"completed"}}`,
			},
			wantInBody: `"type":"response.completed"`,
		},
		{
			name:    "ordinary message only",
			events:  []string{normalMessage, completed},
			wantErr: true,
		},
		{
			name:    "multiple compactions",
			events:  []string{validCompaction, legacyCompactionAlias, completed},
			wantErr: true,
		},
		{
			name:    "compaction without response completed",
			events:  []string{validCompaction},
			wantErr: true,
		},
		{
			name: "response completed missing required id",
			events: []string{
				validCompaction,
				`{"type":"response.completed","response":{"status":"completed"}}`,
			},
			wantErr: true,
		},
		{
			name: "compaction missing encrypted content",
			events: []string{
				`{"type":"response.output_item.done","output_index":0,"item":{"id":"cmp-invalid","type":"compaction"}}`,
				completed,
			},
			wantErr: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder, info, apiErr := runDirectResponsesStreamWithRequest(
				t,
				responsesSSE(test.events...),
				remoteCompactionV2Request(),
			)

			if test.wantErr {
				require.NotNil(t, apiErr)
				assert.Equal(t, types.ErrorCodeBadResponse, apiErr.GetErrorCode())
				assert.Empty(t, recorder.Body.String())
				assert.False(t, info.HasSendResponse())
				return
			}
			require.Nil(t, apiErr)
			assert.Contains(t, recorder.Body.String(), test.wantInBody)
		})
	}
}

func TestOaiResponsesStreamHandlerRemoteCompactionV2AllowsBoundedEncryptedPayloadAbovePreludeLimit(t *testing.T) {
	encryptedContent := "BEGIN" + strings.Repeat("x", responsesPreludeBufferLimit) + "END"
	compaction := fmt.Sprintf(
		`{"type":"response.output_item.done","output_index":0,"item":{"id":"cmp-large","type":"compaction","encrypted_content":%q}}`,
		encryptedContent,
	)
	completed := `{"type":"response.completed","response":{"id":"resp-large","status":"completed"}}`

	recorder, _, apiErr := runDirectResponsesStreamWithRequest(
		t,
		responsesSSE(compaction, completed),
		remoteCompactionV2Request(),
	)

	require.Nil(t, apiErr)
	assert.Greater(t, recorder.Body.Len(), responsesPreludeBufferLimit)
	assert.Contains(t, recorder.Body.String(), `"encrypted_content":"BEGIN`)
	assert.Contains(t, recorder.Body.String(), `END"`)
}

func TestOaiResponsesStreamHandlerRemoteCompactionV2BoundsOneOversizedRawEvent(t *testing.T) {
	const expectedRawReadLimit = compactResponseBodyLimit + (64 << 10)
	const secret = "sk-oversized-compaction-event-secret"
	padding := secret + strings.Repeat("x", 2*compactResponseBodyLimit)
	event := fmt.Sprintf(
		`{"type":"response.created","response":{"id":"resp-oversized","status":"in_progress","padding":%q}}`,
		padding,
	)
	body := &countingReadCloser{reader: strings.NewReader(responsesSSE(event))}

	recorder, info, apiErr := runDirectResponsesStreamWithBody(
		t,
		body,
		remoteCompactionV2Request(),
		context.Background(),
	)

	require.NotNil(t, apiErr)
	assert.Equal(t, types.ErrorCodeBadResponseBody, apiErr.GetErrorCode())
	assert.NotContains(t, apiErr.Error(), secret)
	assert.Empty(t, recorder.Body.String())
	assert.False(t, info.HasSendResponse())
	assert.LessOrEqual(t, body.bytesRead.Load(), int64(expectedRawReadLimit+1))
}

func TestOaiResponsesStreamHandlerRemoteCompactionV2BoundsCumulativeRawEvents(t *testing.T) {
	const expectedRawReadLimit = compactResponseBodyLimit + (64 << 10)
	events := make([]string, 0, 5)
	for i := 0; i < 5; i++ {
		events = append(events, fmt.Sprintf(
			`{"type":"response.created","response":{"id":"resp-part-%d","status":"in_progress","padding":%q}}`,
			i,
			strings.Repeat("x", 2<<20),
		))
	}
	body := &countingReadCloser{reader: strings.NewReader(responsesSSE(events...))}

	recorder, info, apiErr := runDirectResponsesStreamWithBody(
		t,
		body,
		remoteCompactionV2Request(),
		context.Background(),
	)

	require.NotNil(t, apiErr)
	assert.Equal(t, types.ErrorCodeBadResponseBody, apiErr.GetErrorCode())
	assert.Empty(t, recorder.Body.String())
	assert.False(t, info.HasSendResponse())
	assert.LessOrEqual(t, body.bytesRead.Load(), int64(expectedRawReadLimit+1))
}

func TestOaiResponsesStreamHandlerRemoteCompactionV2RejectsOversizedCompletionBeforeCommit(t *testing.T) {
	compaction := `{"type":"response.output_item.done","output_index":0,"item":{"id":"cmp-completion-limit","type":"compaction","encrypted_content":"encrypted"}}`
	completed := fmt.Sprintf(
		`{"type":"response.completed","response":{"id":"resp-completion-limit","status":"completed","padding":%q}}`,
		strings.Repeat("x", compactResponseBodyLimit),
	)

	recorder, info, apiErr := runDirectResponsesStreamWithRequest(
		t,
		responsesSSE(compaction, completed),
		remoteCompactionV2Request(),
	)

	require.NotNil(t, apiErr)
	assert.Equal(t, types.ErrorCodeBadResponseBody, apiErr.GetErrorCode())
	assert.Empty(t, recorder.Body.String())
	assert.False(t, info.HasSendResponse())
}

func TestOaiResponsesStreamHandlerRemoteCompactionV2AcceptsNearBoundaryEncryptedPayload(t *testing.T) {
	encryptedContent := "BEGIN" + strings.Repeat("x", compactResponseBodyLimit-(4<<10)) + "END"
	compaction := fmt.Sprintf(
		`{"type":"response.output_item.done","output_index":0,"item":{"id":"cmp-near-limit","type":"compaction","encrypted_content":%q}}`,
		encryptedContent,
	)
	completed := `{"type":"response.completed","response":{"id":"resp-near-limit","status":"completed"}}`

	recorder, _, apiErr := runDirectResponsesStreamWithRequest(
		t,
		responsesSSE(compaction, completed),
		remoteCompactionV2Request(),
	)

	require.Nil(t, apiErr)
	assert.Greater(t, recorder.Body.Len(), compactResponseBodyLimit-(8<<10))
	body := recorder.Body.String()
	assert.Contains(t, body, `"encrypted_content":"BEGIN`)
	assert.Contains(t, body, `END"`)
	compactionIndex := strings.Index(body, `"type":"response.output_item.done"`)
	completionIndex := strings.Index(body, `"type":"response.completed"`)
	assert.GreaterOrEqual(t, compactionIndex, 0)
	assert.Greater(t, completionIndex, compactionIndex)
}

func TestOaiResponsesStreamHandlerRemoteCompactionV2CancellationWhileBufferingWins(t *testing.T) {
	requestContext, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	event := `{"type":"response.created","response":{"id":"resp-cancel-buffer","status":"in_progress"}}`
	body := &cancelOnFirstReadBody{
		reader: strings.NewReader(responsesSSE(event)),
		cancel: cancel,
	}

	recorder, info, apiErr := runDirectResponsesStreamWithBody(
		t,
		body,
		remoteCompactionV2Request(),
		requestContext,
	)

	require.ErrorIs(t, requestContext.Err(), context.Canceled)
	require.Nil(t, apiErr)
	assert.Empty(t, recorder.Body.String())
	require.NotNil(t, info.StreamStatus)
	assert.Equal(t, relaycommon.StreamEndReasonClientGone, info.StreamStatus.EndReason)
}

func TestOaiResponsesStreamHandlerOrdinaryResponsesRetainsLargeCompletionBehavior(t *testing.T) {
	text := strings.Repeat("x", compactResponseBodyLimit+(128<<10))
	completed := fmt.Sprintf(
		`{"type":"response.completed","response":{"id":"resp-ordinary-large","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":%q}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`,
		text,
	)

	recorder, _, apiErr := runDirectResponsesStream(t, responsesSSE(completed))

	require.Nil(t, apiErr)
	assert.Greater(t, recorder.Body.Len(), compactResponseBodyLimit)
	assert.Contains(t, recorder.Body.String(), `"id":"resp-ordinary-large"`)
}

func TestOaiResponsesStreamHandlerRemoteCompactionV2RejectsPayloadAboveCompactLimit(t *testing.T) {
	encryptedContent := strings.Repeat("x", compactResponseBodyLimit)
	compaction := fmt.Sprintf(
		`{"type":"response.output_item.done","output_index":0,"item":{"id":"cmp-too-large","type":"compaction","encrypted_content":%q}}`,
		encryptedContent,
	)
	completed := `{"type":"response.completed","response":{"id":"resp-too-large","status":"completed"}}`

	recorder, info, apiErr := runDirectResponsesStreamWithRequest(
		t,
		responsesSSE(compaction, completed),
		remoteCompactionV2Request(),
	)

	require.NotNil(t, apiErr)
	assert.Equal(t, types.ErrorCodeBadResponseBody, apiErr.GetErrorCode())
	assert.Empty(t, recorder.Body.String())
	assert.False(t, info.HasSendResponse())
}

func TestOaiResponsesStreamHandlerRemoteCompactionV2CancellationOverridesSemanticFailure(t *testing.T) {
	requestContext, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	body := responsesSSE(
		`{"type":"response.output_item.done","output_index":0,"item":{"id":"msg-incompatible","type":"message","role":"assistant","content":[{"type":"output_text","text":"not a compaction"}]}}`,
		`{"type":"response.completed","response":{"id":"resp-incompatible","status":"completed"}}`,
	)

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(requestContext)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: &cancelRequestOnCloseBody{
			Reader: strings.NewReader(body),
			cancel: cancel,
		},
	}
	info := &relaycommon.RelayInfo{
		DisablePing: true,
		Request:     remoteCompactionV2Request(),
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-5.6-sol"},
	}
	previousTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() {
		constant.StreamingTimeout = previousTimeout
	})

	_, apiErr := OaiResponsesStreamHandler(c, info, resp)

	require.ErrorIs(t, requestContext.Err(), context.Canceled)
	require.Nil(t, apiErr)
	assert.Empty(t, recorder.Body.String())
	require.NotNil(t, info.StreamStatus)
	assert.Equal(t, relaycommon.StreamEndReasonClientGone, info.StreamStatus.EndReason)
	assert.ErrorIs(t, info.StreamStatus.EndError, context.Canceled)
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
