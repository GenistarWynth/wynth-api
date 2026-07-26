package baidu

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type baiduAbruptReader struct {
	data         []byte
	sent         bool
	waitForWrite <-chan struct{}
}

func (reader *baiduAbruptReader) Read(buffer []byte) (int, error) {
	if reader.sent {
		if reader.waitForWrite != nil {
			<-reader.waitForWrite
		}
		return 0, io.ErrUnexpectedEOF
	}
	reader.sent = true
	if len(reader.data) == 0 {
		return 0, io.ErrUnexpectedEOF
	}
	return copy(buffer, reader.data), nil
}

type baiduWriteSignalWriter struct {
	gin.ResponseWriter
	once   sync.Once
	signal chan struct{}
}

func (writer *baiduWriteSignalWriter) Write(data []byte) (int, error) {
	count, err := writer.ResponseWriter.Write(data)
	if count > 0 {
		writer.once.Do(func() { close(writer.signal) })
	}
	return count, err
}

func (writer *baiduWriteSignalWriter) WriteString(data string) (int, error) {
	count, err := writer.ResponseWriter.WriteString(data)
	if count > 0 {
		writer.once.Do(func() { close(writer.signal) })
	}
	return count, err
}

func TestBaiduStreamTerminationReturnsAbnormalFailureWithoutExtraFrames(t *testing.T) {
	gin.SetMode(gin.TestMode)
	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })
	successChunk := `data: {"id":"baidu-id","result":"hello","sentence_id":0,"is_end":false,"usage":{"prompt_tokens":2,"total_tokens":3}}` + "\n\n"

	tests := []struct {
		name         string
		reader       func(chan struct{}) io.Reader
		signal       bool
		wantErr      bool
		wantBody     string
		wantReceived int
	}{
		{name: "zero byte abnormal", reader: func(chan struct{}) io.Reader { return &baiduAbruptReader{} }, wantErr: true},
		{
			name: "partial abnormal",
			reader: func(signal chan struct{}) io.Reader {
				return &baiduAbruptReader{data: []byte(successChunk), waitForWrite: signal}
			},
			signal:       true,
			wantErr:      true,
			wantBody:     "hello",
			wantReceived: 1,
		},
		{name: "normal empty", reader: func(chan struct{}) io.Reader { return strings.NewReader("") }},
		{
			name:         "normal success",
			reader:       func(chan struct{}) io.Reader { return strings.NewReader(successChunk) },
			wantBody:     "hello",
			wantReceived: 1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			context, _ := gin.CreateTestContext(recorder)
			var signal chan struct{}
			if test.signal {
				signal = make(chan struct{})
				context.Writer = &baiduWriteSignalWriter{ResponseWriter: context.Writer, signal: signal}
			}
			context.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			info := &relaycommon.RelayInfo{
				StartTime:   time.Now(),
				ChannelMeta: &relaycommon.ChannelMeta{},
			}
			response := &http.Response{
				Body:   io.NopCloser(test.reader(signal)),
				Header: make(http.Header),
			}

			streamErr, usage := baiduStreamHandler(context, info, response)

			if test.wantErr {
				require.NotNil(t, streamErr)
				assert.Equal(t, "read_response_body_failed", string(streamErr.GetErrorCode()))
			} else {
				assert.Nil(t, streamErr)
			}
			require.NotNil(t, usage)
			assert.Equal(t, test.wantReceived, info.ReceivedResponseCount)
			assert.Contains(t, recorder.Body.String(), test.wantBody)
			assert.NotContains(t, recorder.Body.String(), "[DONE]")
		})
	}
}
