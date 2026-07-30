package xai

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type xaiAbruptReader struct {
	data         []byte
	read         bool
	waitForWrite <-chan struct{}
}

func (r *xaiAbruptReader) Read(p []byte) (int, error) {
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

type xaiWriteSignalWriter struct {
	gin.ResponseWriter
	once   sync.Once
	signal chan struct{}
}

func (w *xaiWriteSignalWriter) Write(data []byte) (int, error) {
	n, err := w.ResponseWriter.Write(data)
	if n > 0 {
		w.once.Do(func() { close(w.signal) })
	}
	return n, err
}

func (w *xaiWriteSignalWriter) WriteString(data string) (int, error) {
	n, err := w.ResponseWriter.WriteString(data)
	if n > 0 {
		w.once.Do(func() { close(w.signal) })
	}
	return n, err
}

func newXAITerminationFixture(t *testing.T, body io.Reader, signal chan struct{}) (*gin.Context, *httptest.ResponseRecorder, *http.Response, *relaycommon.RelayInfo) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	if signal != nil {
		c.Writer = &xaiWriteSignalWriter{ResponseWriter: c.Writer, signal: signal}
	}
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Set(common.RequestIdKey, "xai-termination")
	info := &relaycommon.RelayInfo{
		IsStream:    true,
		RelayFormat: types.RelayFormatOpenAI,
		DisablePing: true,
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "grok-test"},
	}
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(body),
	}
	return c, recorder, resp, info
}

func TestXAIStreamTerminationGatesDoneFrame(t *testing.T) {
	chunk := `{"id":"chatcmpl-xai","object":"chat.completion.chunk","created":1,"model":"grok-test","choices":[{"index":0,"delta":{"content":"partial"},"finish_reason":null}]}`
	tests := []struct {
		name         string
		body         func(chan struct{}) io.Reader
		signal       bool
		wantErr      bool
		wantContains string
		wantTerminal bool
	}{
		{name: "zero byte abnormal", body: func(chan struct{}) io.Reader { return &xaiAbruptReader{} }, wantErr: true},
		{
			name: "partial abnormal",
			body: func(signal chan struct{}) io.Reader {
				return &xaiAbruptReader{
					data:         []byte("data: " + chunk + "\n\n"),
					waitForWrite: signal,
				}
			},
			signal:       true,
			wantErr:      true,
			wantContains: "partial",
		},
		{
			name:         "normal empty",
			body:         func(chan struct{}) io.Reader { return strings.NewReader("data: [DONE]\n\n") },
			wantTerminal: true,
		},
		{
			name: "normal success",
			body: func(chan struct{}) io.Reader {
				return strings.NewReader("data: " + chunk + "\n\ndata: [DONE]\n\n")
			},
			wantContains: "partial",
			wantTerminal: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var signal chan struct{}
			if test.signal {
				signal = make(chan struct{})
			}
			c, recorder, resp, info := newXAITerminationFixture(t, test.body(signal), signal)

			usage, apiErr := xAIStreamHandler(c, info, resp)

			require.NotNil(t, usage)
			if test.wantErr {
				require.NotNil(t, apiErr)
				assert.Equal(t, types.ErrorCodeReadResponseBodyFailed, apiErr.GetErrorCode())
			} else {
				require.Nil(t, apiErr)
			}
			if test.wantContains != "" {
				assert.Contains(t, recorder.Body.String(), test.wantContains)
			}
			if test.wantTerminal {
				assert.Contains(t, recorder.Body.String(), "[DONE]")
			} else {
				assert.NotContains(t, recorder.Body.String(), "[DONE]")
			}
			if test.name == "zero byte abnormal" {
				assert.Empty(t, recorder.Body.String())
				assert.False(t, info.HasSendResponse())
			}
		})
	}
}
