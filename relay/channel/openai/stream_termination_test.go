package openai

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type abruptStreamReader struct {
	data         []byte
	read         bool
	waitForWrite <-chan struct{}
}

func (r *abruptStreamReader) Read(p []byte) (int, error) {
	if r.read {
		if r.waitForWrite != nil {
			<-r.waitForWrite
		}
		return 0, io.ErrUnexpectedEOF
	}
	r.read = true
	if len(r.data) == 0 {
		return 0, io.ErrUnexpectedEOF
	}
	return copy(p, r.data), nil
}

type writeSignalResponseWriter struct {
	gin.ResponseWriter
	once   sync.Once
	signal chan struct{}
}

func (w *writeSignalResponseWriter) Write(data []byte) (int, error) {
	n, err := w.ResponseWriter.Write(data)
	if n > 0 {
		w.once.Do(func() { close(w.signal) })
	}
	return n, err
}

func (w *writeSignalResponseWriter) WriteString(data string) (int, error) {
	n, err := w.ResponseWriter.WriteString(data)
	if n > 0 {
		w.once.Do(func() { close(w.signal) })
	}
	return n, err
}

func newOAIStreamTerminationFixture(t *testing.T, body io.Reader, writeSignals ...chan struct{}) (*gin.Context, *httptest.ResponseRecorder, *http.Response, *relaycommon.RelayInfo) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	previousStreamingTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() {
		constant.StreamingTimeout = previousStreamingTimeout
	})
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	if len(writeSignals) > 0 && writeSignals[0] != nil {
		c.Writer = &writeSignalResponseWriter{
			ResponseWriter: c.Writer,
			signal:         writeSignals[0],
		}
	}
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	common.SetContextKey(c, constant.ContextKeyOriginalModel, "gpt-stream-test")
	request := &dto.GeneralOpenAIRequest{
		Model:  "gpt-stream-test",
		Stream: common.GetPointer(true),
	}
	info, err := relaycommon.GenRelayInfo(c, types.RelayFormatOpenAI, request, nil)
	require.NoError(t, err)
	info.ChannelMeta = &relaycommon.ChannelMeta{UpstreamModelName: request.Model}
	info.ShouldIncludeUsage = true
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(body),
	}
	return c, recorder, resp, info
}

func TestOaiStreamHandlerZeroChunkAbnormalWritesNothingAndReturnsRetryableError(t *testing.T) {
	c, recorder, resp, info := newOAIStreamTerminationFixture(t, &abruptStreamReader{})

	usage, apiErr := OaiStreamHandler(c, info, resp)

	require.NotNil(t, usage)
	require.NotNil(t, apiErr)
	assert.Equal(t, types.ErrorCodeReadResponseBodyFailed, apiErr.GetErrorCode())
	assert.Empty(t, recorder.Body.String())
	assert.False(t, info.HasSendResponse())
	require.NotNil(t, info.StreamStatus)
	assert.Equal(t, relaycommon.StreamEndReasonScannerErr, info.StreamStatus.EndReason)
}

func TestOaiStreamHandlerPartialAbnormalKeepsOnlyCommittedChunks(t *testing.T) {
	first := `{"id":"chatcmpl-partial","object":"chat.completion.chunk","created":1,"model":"gpt-stream-test","choices":[{"index":0,"delta":{"content":"first"},"finish_reason":null}]}`
	second := `{"id":"chatcmpl-partial","object":"chat.completion.chunk","created":1,"model":"gpt-stream-test","choices":[{"index":0,"delta":{"content":"second"},"finish_reason":null}]}`
	raw := []byte("data: " + first + "\n\ndata: " + second + "\n\n")
	wrote := make(chan struct{})
	c, recorder, resp, info := newOAIStreamTerminationFixture(
		t,
		&abruptStreamReader{data: raw, waitForWrite: wrote},
		wrote,
	)

	usage, apiErr := OaiStreamHandler(c, info, resp)

	require.NotNil(t, usage)
	require.NotNil(t, apiErr)
	assert.Equal(t, types.ErrorCodeReadResponseBodyFailed, apiErr.GetErrorCode())
	assert.Contains(t, recorder.Body.String(), "first")
	assert.NotContains(t, recorder.Body.String(), "second")
	assert.NotContains(t, recorder.Body.String(), "[DONE]")
	assert.True(t, info.HasSendResponse())
	require.NotNil(t, info.StreamStatus)
	assert.Equal(t, relaycommon.StreamEndReasonScannerErr, info.StreamStatus.EndReason)
}

func TestOaiStreamHandlerNormalEmptyKeepsSyntheticFinalFrames(t *testing.T) {
	c, recorder, resp, info := newOAIStreamTerminationFixture(t, strings.NewReader("data: [DONE]\n\n"))

	usage, apiErr := OaiStreamHandler(c, info, resp)

	require.NotNil(t, usage)
	require.Nil(t, apiErr)
	assert.Equal(t, 1, strings.Count(recorder.Body.String(), "data: [DONE]"))
	assert.Contains(t, recorder.Body.String(), `"usage"`)
	assert.True(t, info.HasSendResponse())
	require.NotNil(t, info.StreamStatus)
	assert.Equal(t, relaycommon.StreamEndReasonDone, info.StreamStatus.EndReason)
}

func TestOaiStreamHandlerNormalSuccessKeepsChunkAndFinalFrames(t *testing.T) {
	chunk := `{"id":"chatcmpl-success","object":"chat.completion.chunk","created":1,"model":"gpt-stream-test","choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":null}]}`
	c, recorder, resp, info := newOAIStreamTerminationFixture(
		t,
		strings.NewReader("data: "+chunk+"\n\ndata: [DONE]\n\n"),
	)

	usage, apiErr := OaiStreamHandler(c, info, resp)

	require.NotNil(t, usage)
	require.Nil(t, apiErr)
	assert.Contains(t, recorder.Body.String(), `"content":"ok"`)
	assert.Equal(t, 1, strings.Count(recorder.Body.String(), "data: [DONE]"))
	assert.True(t, info.HasSendResponse())
	require.NotNil(t, info.StreamStatus)
	assert.Equal(t, relaycommon.StreamEndReasonDone, info.StreamStatus.EndReason)
}

func TestHandleStreamFormatGeminiMetadataOnlyChunkDoesNotCommit(t *testing.T) {
	c, recorder, _, info := newOAIStreamTerminationFixture(t, strings.NewReader(""))
	info.RelayFormat = types.RelayFormatGemini
	roleOnlyChunk := `{"id":"chatcmpl-role","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant"}}]}`

	require.NoError(t, HandleStreamFormat(c, info, roleOnlyChunk, false, false))

	assert.Empty(t, recorder.Body.String())
	assert.False(t, info.HasSendResponse())
}
