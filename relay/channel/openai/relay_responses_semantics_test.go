package openai

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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

type terminalControlledReadCloser struct {
	reader       *strings.Reader
	terminal     <-chan struct{}
	probeStarted chan struct{}
	closed       chan struct{}
	probeOnce    sync.Once
	closeOnce    sync.Once
}

func (b *terminalControlledReadCloser) Read(data []byte) (int, error) {
	n, err := b.reader.Read(data)
	if n > 0 {
		return n, nil
	}
	if err != io.EOF {
		return n, err
	}
	b.probeOnce.Do(func() {
		close(b.probeStarted)
	})
	select {
	case <-b.terminal:
		return 0, io.EOF
	case <-b.closed:
		return 0, io.ErrClosedPipe
	}
}

func (b *terminalControlledReadCloser) Close() error {
	b.closeOnce.Do(func() {
		close(b.closed)
	})
	return nil
}

type overflowSignalingReadCloser struct {
	reader       *strings.Reader
	bytesRead    atomic.Int64
	probeStarted chan struct{}
	allowRead    chan struct{}
	overflowRead chan struct{}
	closed       chan struct{}
	probeOnce    sync.Once
	allowOnce    sync.Once
	overflowOnce sync.Once
	closeOnce    sync.Once
}

func (b *overflowSignalingReadCloser) Read(data []byte) (int, error) {
	if b.bytesRead.Load() >= compactResponseStreamReadLimit {
		b.probeOnce.Do(func() {
			close(b.probeStarted)
		})
		select {
		case <-b.allowRead:
		case <-b.closed:
			return 0, io.ErrClosedPipe
		}
	}
	n, err := b.reader.Read(data)
	if b.bytesRead.Add(int64(n)) > compactResponseStreamReadLimit {
		b.overflowOnce.Do(func() {
			close(b.overflowRead)
		})
	}
	return n, err
}

func (b *overflowSignalingReadCloser) releaseOverflow() {
	b.allowOnce.Do(func() {
		close(b.allowRead)
	})
}

func (b *overflowSignalingReadCloser) Close() error {
	b.closeOnce.Do(func() {
		close(b.closed)
	})
	return nil
}

type observingResponseWriter struct {
	gin.ResponseWriter
	beforeWrite func()
	writeErr    error
	writes      atomic.Int64
	flushes     atomic.Int64
}

type errCallBarrierContext struct {
	context.Context
	target  int64
	calls   atomic.Int64
	reached chan struct{}
	once    sync.Once
}

type commitErrGapContext struct {
	context.Context
	armed    atomic.Bool
	observed chan struct{}
	release  chan struct{}
	once     sync.Once
}

type cancellationCallbackBarrierContext struct {
	context.Context
	callbackStarted chan struct{}
	commitObserved  chan struct{}
	releaseCallback chan struct{}
	errCalls        atomic.Int64
}

type trackedAfterFuncContext struct {
	context.Context
	registered   chan struct{}
	stopped      chan struct{}
	registerOnce sync.Once
	stopOnce     sync.Once
}

func (c *errCallBarrierContext) Err() error {
	err := c.Context.Err()
	if c.calls.Add(1) == c.target {
		c.once.Do(func() { close(c.reached) })
	}
	return err
}

func (c *commitErrGapContext) Err() error {
	err := c.Context.Err()
	if err == nil && c.armed.CompareAndSwap(true, false) {
		c.once.Do(func() { close(c.observed) })
		<-c.release
	}
	return err
}

func (c *cancellationCallbackBarrierContext) Err() error {
	err := c.Context.Err()
	if err == nil {
		return nil
	}
	switch c.errCalls.Add(1) {
	case 1:
		close(c.callbackStarted)
		<-c.releaseCallback
	case 2:
		close(c.commitObserved)
	}
	return err
}

func (c *trackedAfterFuncContext) AfterFunc(_ func()) func() bool {
	c.registerOnce.Do(func() { close(c.registered) })
	var once sync.Once
	return func() bool {
		stopped := false
		once.Do(func() {
			stopped = true
			c.stopOnce.Do(func() { close(c.stopped) })
		})
		return stopped
	}
}

func (c *trackedAfterFuncContext) Value(any) any {
	return nil
}

func (w *observingResponseWriter) Write(data []byte) (int, error) {
	w.writes.Add(1)
	if w.beforeWrite != nil {
		w.beforeWrite()
	}
	if w.writeErr != nil {
		return 0, w.writeErr
	}
	return w.ResponseWriter.Write(data)
}

func (w *observingResponseWriter) WriteString(data string) (int, error) {
	return w.Write([]byte(data))
}

func (w *observingResponseWriter) Flush() {
	w.flushes.Add(1)
	w.ResponseWriter.Flush()
}

type directResponsesStreamResult struct {
	apiErr *types.NewAPIError
	usage  *dto.Usage
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
	recorder, info, done := startDirectResponsesStreamWithBody(
		t,
		body,
		request,
		requestContext,
		nil,
	)
	result := <-done
	return recorder, info, result.apiErr
}

func startDirectResponsesStreamWithBody(
	t *testing.T,
	body io.ReadCloser,
	request dto.Request,
	requestContext context.Context,
	wrapWriter func(gin.ResponseWriter) gin.ResponseWriter,
) (*httptest.ResponseRecorder, *relaycommon.RelayInfo, <-chan directResponsesStreamResult) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	if wrapWriter != nil {
		c.Writer = wrapWriter(c.Writer)
	}
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

	done := make(chan directResponsesStreamResult, 1)
	go func() {
		usage, apiErr := OaiResponsesStreamHandler(c, info, resp)
		done <- directResponsesStreamResult{apiErr: apiErr, usage: usage}
	}()
	return recorder, info, done
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

func remoteCompactionStreamAtRawLimit(t *testing.T) string {
	t.Helper()
	return remoteCompactionEventsAtRawLimit(t,
		`{"type":"response.output_item.done","output_index":0,"item":{"id":"cmp-limit","type":"compaction","encrypted_content":"encrypted"}}`,
		`{"type":"response.completed","response":{"id":"resp-limit","status":"completed","model":"gpt-5.6-sol","usage":{"input_tokens":1,"output_tokens":0,"total_tokens":1}}}`,
	)
}

func remoteCompactionEventsAtRawLimit(t *testing.T, events ...string) string {
	t.Helper()
	return remoteCompactionPayloadAtRawLimit(t, responsesSSE(events...))
}

func remoteCompactionPayloadAtRawLimit(t *testing.T, stream string) string {
	t.Helper()
	paddingLength := compactResponseStreamReadLimit - len(stream)
	require.GreaterOrEqual(t, paddingLength, 2)
	return ":" + strings.Repeat("p", paddingLength-2) + "\n" + stream
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

func TestRemoteCompactionPreludeWriterCancellationWinsBeforeCommit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	requestContext, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(requestContext)

	observedWriter := &observingResponseWriter{ResponseWriter: c.Writer}
	info := &relaycommon.RelayInfo{StreamStatus: relaycommon.NewStreamStatus()}
	writer := newRemoteCompactionPreludeWriter(observedWriter, 256, requestContext, info)
	_, err := writer.WriteString("buffered remote compaction")
	require.NoError(t, err)

	cancel()
	err = writer.Commit()

	require.ErrorIs(t, err, context.Canceled)
	assert.Zero(t, observedWriter.writes.Load())
	assert.Zero(t, observedWriter.flushes.Load())
	assert.Empty(t, recorder.Body.String())
	assert.False(t, info.HasSendResponse())
	require.NotNil(t, info.StreamStatus)
	assert.Equal(t, relaycommon.StreamEndReasonClientGone, info.StreamStatus.EndReason)
	assert.ErrorIs(t, info.StreamStatus.EndError, context.Canceled)
}

func TestRemoteCompactionPreludeWriterCancellationWinsAfterErrObservation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	baseContext, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	requestContext := &commitErrGapContext{
		Context:  baseContext,
		observed: make(chan struct{}),
		release:  make(chan struct{}),
	}
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(requestContext)

	observedWriter := &observingResponseWriter{ResponseWriter: c.Writer}
	info := &relaycommon.RelayInfo{StreamStatus: relaycommon.NewStreamStatus()}
	writer := newRemoteCompactionPreludeWriter(observedWriter, 256, requestContext, info)
	_, err := writer.WriteString("buffered remote compaction")
	require.NoError(t, err)

	requestContext.armed.Store(true)
	commitDone := make(chan error, 1)
	go func() {
		commitDone <- writer.Commit()
	}()

	select {
	case <-requestContext.observed:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the precommit context observation")
	}
	cancel()
	close(requestContext.release)

	select {
	case err := <-commitDone:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for cancellation arbitration")
	}
	assert.Zero(t, observedWriter.writes.Load())
	assert.Zero(t, observedWriter.flushes.Load())
	assert.Empty(t, recorder.Body.String())
	assert.False(t, info.HasSendResponse())
	require.NotNil(t, info.StreamStatus)
	assert.Equal(t, relaycommon.StreamEndReasonClientGone, info.StreamStatus.EndReason)
	assert.ErrorIs(t, info.StreamStatus.EndError, context.Canceled)
}

func TestRemoteCompactionPreludeWriterWaitsForCancellationCallbackWithoutDeadlock(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	baseContext, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	requestContext := &cancellationCallbackBarrierContext{
		Context:         baseContext,
		callbackStarted: make(chan struct{}),
		commitObserved:  make(chan struct{}),
		releaseCallback: make(chan struct{}),
	}
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(requestContext)

	observedWriter := &observingResponseWriter{ResponseWriter: c.Writer}
	info := &relaycommon.RelayInfo{StreamStatus: relaycommon.NewStreamStatus()}
	writer := newRemoteCompactionPreludeWriter(observedWriter, 256, requestContext, info)
	_, err := writer.WriteString("buffered remote compaction")
	require.NoError(t, err)

	cancel()
	select {
	case <-requestContext.callbackStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for cancellation callback ownership")
	}
	commitDone := make(chan error, 1)
	go func() {
		commitDone <- writer.Commit()
	}()
	select {
	case <-requestContext.commitObserved:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for commit to observe cancellation")
	}
	close(requestContext.releaseCallback)

	select {
	case err := <-commitDone:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for cancellation callback completion")
	}
	assert.Zero(t, observedWriter.writes.Load())
	assert.Zero(t, observedWriter.flushes.Load())
	assert.Empty(t, recorder.Body.String())
	assert.False(t, info.HasSendResponse())
}

func TestRemoteCompactionCancellationWatchCleanupPropagatesCallbackCancellation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	baseContext, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	requestContext := &cancellationCallbackBarrierContext{
		Context:         baseContext,
		callbackStarted: make(chan struct{}),
		releaseCallback: make(chan struct{}),
	}
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(requestContext)

	observedWriter := &observingResponseWriter{ResponseWriter: c.Writer}
	info := &relaycommon.RelayInfo{StreamStatus: relaycommon.NewStreamStatus()}
	writer := newRemoteCompactionPreludeWriter(observedWriter, 256, requestContext, info)
	_, err := writer.WriteString("buffered remote compaction")
	require.NoError(t, err)

	cancel()
	select {
	case <-requestContext.callbackStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for cancellation callback ownership")
	}
	cleanupDone := make(chan struct{})
	go func() {
		writer.stopCancellationWatch()
		close(cleanupDone)
	}()
	close(requestContext.releaseCallback)

	select {
	case <-cleanupDone:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for cancellation cleanup completion")
	}
	assert.Zero(t, observedWriter.writes.Load())
	assert.Zero(t, observedWriter.flushes.Load())
	assert.Empty(t, recorder.Body.String())
	assert.False(t, info.HasSendResponse())
	require.NotNil(t, info.StreamStatus)
	assert.Equal(t, relaycommon.StreamEndReasonClientGone, info.StreamStatus.EndReason)
	assert.ErrorIs(t, info.StreamStatus.EndError, context.Canceled)
}

func TestRemoteCompactionCancellationWatchCleanupPreservesCommitOwnerState(t *testing.T) {
	tests := []struct {
		name     string
		writeErr error
	}{
		{name: "committed"},
		{name: "failed", writeErr: errors.New("downstream write failed")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			requestContext, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(requestContext)

			observedWriter := &observingResponseWriter{ResponseWriter: c.Writer, writeErr: test.writeErr}
			info := &relaycommon.RelayInfo{StreamStatus: relaycommon.NewStreamStatus()}
			writer := newRemoteCompactionPreludeWriter(observedWriter, 256, requestContext, info)
			_, err := writer.WriteString("committed remote compaction")
			require.NoError(t, err)

			err = writer.Commit()
			if test.writeErr == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, test.writeErr)
			}
			cancel()
			writer.stopCancellationWatch()

			assert.Equal(t, int64(1), observedWriter.writes.Load())
			assert.True(t, info.HasSendResponse())
			require.NotNil(t, info.StreamStatus)
			assert.NotEqual(t, relaycommon.StreamEndReasonClientGone, info.StreamStatus.EndReason)
			assert.NoError(t, info.StreamStatus.EndError)
		})
	}
}

func TestRemoteCompactionPreludeWriterCommitWinsBeforeLaterCancellation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	requestContext, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(requestContext)

	writeStarted := make(chan struct{})
	allowWrite := make(chan struct{})
	var writeOnce sync.Once
	observedWriter := &observingResponseWriter{
		ResponseWriter: c.Writer,
		beforeWrite: func() {
			writeOnce.Do(func() { close(writeStarted) })
			<-allowWrite
		},
	}
	info := &relaycommon.RelayInfo{StreamStatus: relaycommon.NewStreamStatus()}
	writer := newRemoteCompactionPreludeWriter(observedWriter, 256, requestContext, info)
	_, err := writer.WriteString("committed remote compaction")
	require.NoError(t, err)

	commitDone := make(chan error, 1)
	go func() {
		commitDone <- writer.Commit()
	}()

	select {
	case <-writeStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the first underlying write")
	}
	assert.True(t, info.HasSendResponse(), "commit must be visible before the first underlying write")
	cancel()
	close(allowWrite)

	select {
	case err := <-commitDone:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for committed write")
	}
	assert.Equal(t, int64(1), observedWriter.writes.Load())
	assert.Equal(t, int64(1), observedWriter.flushes.Load())
	assert.Equal(t, "committed remote compaction", recorder.Body.String())
	assert.True(t, info.HasSendResponse())
}

func TestRemoteCompactionPreludeWriterRejectsBufferedErrorBeforeCommit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	observedWriter := &observingResponseWriter{ResponseWriter: c.Writer}
	info := &relaycommon.RelayInfo{StreamStatus: relaycommon.NewStreamStatus()}
	writer := newRemoteCompactionPreludeWriter(observedWriter, 4, context.Background(), info)
	_, writeErr := writer.WriteString("too large")
	require.Error(t, writeErr)

	commitErr := writer.Commit()

	require.ErrorIs(t, commitErr, writeErr)
	assert.Zero(t, observedWriter.writes.Load())
	assert.Zero(t, observedWriter.flushes.Load())
	assert.Empty(t, recorder.Body.String())
	assert.False(t, info.HasSendResponse())
}

func TestRemoteCompactionPreludeWriterWriteFailureRemainsCommitted(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	underlyingErr := errors.New("downstream write failed")
	observedWriter := &observingResponseWriter{ResponseWriter: c.Writer, writeErr: underlyingErr}
	info := &relaycommon.RelayInfo{StreamStatus: relaycommon.NewStreamStatus()}
	writer := newRemoteCompactionPreludeWriter(observedWriter, 256, context.Background(), info)
	_, err := writer.WriteString("committed remote compaction")
	require.NoError(t, err)

	err = writer.Commit()
	require.ErrorIs(t, err, underlyingErr)
	assert.Equal(t, int64(1), observedWriter.writes.Load())
	assert.Zero(t, observedWriter.flushes.Load())
	assert.Empty(t, recorder.Body.String())
	assert.True(t, info.HasSendResponse())

	err = writer.Commit()
	require.ErrorIs(t, err, underlyingErr)
	assert.Equal(t, int64(1), observedWriter.writes.Load())
}

func TestRemoteCompactionPreludeWriterConcurrentCancellationArbitration(t *testing.T) {
	gin.SetMode(gin.TestMode)
	deadline := time.After(10 * time.Second)
	for range 32 {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		requestContext, cancel := context.WithCancel(context.Background())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(requestContext)

		observedWriter := &observingResponseWriter{ResponseWriter: c.Writer}
		info := &relaycommon.RelayInfo{StreamStatus: relaycommon.NewStreamStatus()}
		writer := newRemoteCompactionPreludeWriter(observedWriter, 256, requestContext, info)
		_, err := writer.WriteString("remote compaction arbitration")
		require.NoError(t, err)

		start := make(chan struct{})
		cancelDone := make(chan struct{})
		commitDone := make(chan error, 1)
		go func() {
			<-start
			cancel()
			close(cancelDone)
		}()
		go func() {
			<-start
			commitDone <- writer.Commit()
		}()
		close(start)

		select {
		case <-cancelDone:
		case <-deadline:
			t.Fatal("timed out waiting for concurrent cancellation")
		}
		select {
		case err := <-commitDone:
			if errors.Is(err, context.Canceled) {
				assert.Zero(t, observedWriter.writes.Load())
				assert.Zero(t, observedWriter.flushes.Load())
				assert.Empty(t, recorder.Body.String())
				assert.False(t, info.HasSendResponse())
			} else {
				require.NoError(t, err)
				assert.Equal(t, int64(1), observedWriter.writes.Load())
				assert.Equal(t, int64(1), observedWriter.flushes.Load())
				assert.Equal(t, "remote compaction arbitration", recorder.Body.String())
				assert.True(t, info.HasSendResponse())
			}
		case <-deadline:
			t.Fatal("timed out waiting for concurrent commit")
		}
		writer.stopCancellationWatch()
	}
}

func TestRemoteCompactionSemanticFailureStopsCancellationWatch(t *testing.T) {
	baseContext, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	requestContext := &trackedAfterFuncContext{
		Context:    baseContext,
		registered: make(chan struct{}),
		stopped:    make(chan struct{}),
	}
	_, _, apiErr := runDirectResponsesStreamWithBody(
		t,
		io.NopCloser(strings.NewReader(responsesSSE(
			`{"type":"response.completed","response":{"id":"resp-semantic-failure","usage":{"input_tokens":1,"output_tokens":0,"total_tokens":1}}}`,
		))),
		remoteCompactionV2Request(),
		requestContext,
	)

	require.NotNil(t, apiErr)
	select {
	case <-requestContext.registered:
	case <-time.After(5 * time.Second):
		t.Fatal("cancellation watch was not registered")
	}
	select {
	case <-requestContext.stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("semantic failure retained its cancellation watch")
	}
}

func TestRemoteCompactionCancellationWatchCleanupIsIdempotent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	baseContext, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	requestContext := &trackedAfterFuncContext{
		Context:    baseContext,
		registered: make(chan struct{}),
		stopped:    make(chan struct{}),
	}
	writer := newRemoteCompactionPreludeWriter(c.Writer, 256, requestContext, &relaycommon.RelayInfo{})

	cleanupDone := make(chan struct{})
	go func() {
		writer.stopCancellationWatch()
		writer.stopCancellationWatch()
		close(cleanupDone)
	}()

	select {
	case <-cleanupDone:
	case <-time.After(5 * time.Second):
		t.Fatal("repeated cancellation cleanup deadlocked")
	}
	select {
	case <-requestContext.stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("cancellation watch was not stopped")
	}
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

func TestOaiResponsesStreamHandlerRemoteCompactionV2MatchesCodexWireTypes(t *testing.T) {
	validCompletion := `{"type":"response.completed","response":{"id":"resp-wire","end_turn":false,"usage":{"input_tokens":0,"input_tokens_details":{"cached_tokens":0},"output_tokens":0,"output_tokens_details":{"reasoning_tokens":0},"total_tokens":0},"future_field":true}}`
	tests := []struct {
		name       string
		compaction string
		completion string
		wantErr    bool
	}{
		{
			name:       "compaction accepts typed passthrough metadata",
			compaction: `{"type":"response.output_item.done","item":{"type":"compaction","encrypted_content":"encrypted","internal_chat_message_metadata_passthrough":{"turn_id":"turn-1","future_field":true}}}`,
			completion: validCompletion,
		},
		{
			name:       "compaction summary accepts null optional fields",
			compaction: `{"type":"response.output_item.done","item":{"id":null,"type":"compaction_summary","encrypted_content":"encrypted","internal_chat_message_metadata_passthrough":null}}`,
			completion: `{"type":"response.completed","response":{"id":"resp-null-optionals","usage":null,"end_turn":null}}`,
		},
		{
			name:       "compaction ignores unrelated fields",
			compaction: `{"type":"response.output_item.done","item":{"type":"compaction","encrypted_content":"encrypted","role":7,"content":"ignored"}}`,
			completion: validCompletion,
		},
		{
			name:       "completion ignores unrelated fields",
			compaction: `{"type":"response.output_item.done","item":{"type":"compaction","encrypted_content":"encrypted"}}`,
			completion: `{"type":"response.completed","response":{"id":"resp-unrelated","model":7,"end_turn":true,"usage":{"input_tokens":1,"input_tokens_details":{"cached_tokens":0,"cached_creation_tokens":"ignored"},"output_tokens":2,"output_tokens_details":{"reasoning_tokens":1,"future_field":"ignored"},"total_tokens":3}}}`,
		},
		{
			name:       "rejects string passthrough metadata",
			compaction: `{"type":"response.output_item.done","item":{"type":"compaction","encrypted_content":"encrypted","internal_chat_message_metadata_passthrough":"[REDACTED]"}}`,
			completion: validCompletion,
			wantErr:    true,
		},
		{
			name:       "rejects non string passthrough turn id",
			compaction: `{"type":"response.output_item.done","item":{"type":"compaction","encrypted_content":"encrypted","internal_chat_message_metadata_passthrough":{"turn_id":7}}}`,
			completion: validCompletion,
			wantErr:    true,
		},
		{
			name:       "rejects string end turn",
			compaction: `{"type":"response.output_item.done","item":{"type":"compaction","encrypted_content":"encrypted"}}`,
			completion: `{"type":"response.completed","response":{"id":"resp-end-turn","end_turn":"false"}}`,
			wantErr:    true,
		},
		{
			name:       "rejects usage missing input tokens",
			compaction: `{"type":"response.output_item.done","item":{"type":"compaction","encrypted_content":"encrypted"}}`,
			completion: `{"type":"response.completed","response":{"id":"resp-usage","usage":{"output_tokens":2,"total_tokens":3}}}`,
			wantErr:    true,
		},
		{
			name:       "rejects usage missing output tokens",
			compaction: `{"type":"response.output_item.done","item":{"type":"compaction","encrypted_content":"encrypted"}}`,
			completion: `{"type":"response.completed","response":{"id":"resp-usage","usage":{"input_tokens":1,"total_tokens":3}}}`,
			wantErr:    true,
		},
		{
			name:       "rejects usage missing total tokens",
			compaction: `{"type":"response.output_item.done","item":{"type":"compaction","encrypted_content":"encrypted"}}`,
			completion: `{"type":"response.completed","response":{"id":"resp-usage","usage":{"input_tokens":1,"output_tokens":2}}}`,
			wantErr:    true,
		},
		{
			name:       "rejects non integer usage",
			compaction: `{"type":"response.output_item.done","item":{"type":"compaction","encrypted_content":"encrypted"}}`,
			completion: `{"type":"response.completed","response":{"id":"resp-usage","usage":{"input_tokens":1,"output_tokens":2.5,"total_tokens":3}}}`,
			wantErr:    true,
		},
		{
			name:       "rejects usage integer outside i64",
			compaction: `{"type":"response.output_item.done","item":{"type":"compaction","encrypted_content":"encrypted"}}`,
			completion: `{"type":"response.completed","response":{"id":"resp-usage","usage":{"input_tokens":9223372036854775808,"output_tokens":0,"total_tokens":0}}}`,
			wantErr:    true,
		},
		{
			name:       "rejects input details missing cached tokens",
			compaction: `{"type":"response.output_item.done","item":{"type":"compaction","encrypted_content":"encrypted"}}`,
			completion: `{"type":"response.completed","response":{"id":"resp-usage","usage":{"input_tokens":1,"input_tokens_details":{},"output_tokens":2,"total_tokens":3}}}`,
			wantErr:    true,
		},
		{
			name:       "rejects output details missing reasoning tokens",
			compaction: `{"type":"response.output_item.done","item":{"type":"compaction","encrypted_content":"encrypted"}}`,
			completion: `{"type":"response.completed","response":{"id":"resp-usage","usage":{"input_tokens":1,"output_tokens":2,"output_tokens_details":{},"total_tokens":3}}}`,
			wantErr:    true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder, info, apiErr := runDirectResponsesStreamWithRequest(
				t,
				responsesSSE(test.compaction, test.completion),
				remoteCompactionV2Request(),
			)

			if test.wantErr {
				require.NotNil(t, apiErr)
				assert.Equal(t, types.ErrorCodeBadResponse, apiErr.GetErrorCode())
				assert.NotContains(t, apiErr.Error(), "[REDACTED]")
				assert.Empty(t, recorder.Body.String())
				assert.False(t, info.HasSendResponse())
				return
			}
			require.Nil(t, apiErr)
			assert.Contains(t, recorder.Body.String(), test.compaction)
			assert.Contains(t, recorder.Body.String(), test.completion)
		})
	}
}

func TestOaiResponsesStreamHandlerRemoteCompactionV2ValidatesUsageBeforeCommit(t *testing.T) {
	maxInt64 := int64(^uint64(0) >> 1)
	maxSupportedInt := int64(^uint(0) >> 1)
	tests := []struct {
		name           string
		usage          string
		wantErr        bool
		wantPrompt     int
		wantCompletion int
		wantTotal      int
		wantCached     int
		wantReasoning  int
	}{
		{name: "negative input tokens", usage: `{"input_tokens":-1,"output_tokens":1,"total_tokens":0}`, wantErr: true},
		{name: "negative output tokens", usage: `{"input_tokens":1,"output_tokens":-1,"total_tokens":0}`, wantErr: true},
		{name: "negative total tokens", usage: `{"input_tokens":0,"output_tokens":0,"total_tokens":-1}`, wantErr: true},
		{name: "negative cached tokens", usage: `{"input_tokens":1,"input_tokens_details":{"cached_tokens":-1},"output_tokens":0,"total_tokens":1}`, wantErr: true},
		{name: "negative reasoning tokens", usage: `{"input_tokens":0,"output_tokens":1,"output_tokens_details":{"reasoning_tokens":-1},"total_tokens":1}`, wantErr: true},
		{
			name:    "input plus output overflows int64",
			usage:   fmt.Sprintf(`{"input_tokens":%d,"output_tokens":1,"total_tokens":%d}`, maxInt64, maxInt64),
			wantErr: true,
		},
		{name: "total tokens mismatch", usage: `{"input_tokens":1,"output_tokens":2,"total_tokens":4}`, wantErr: true},
		{
			name:          "valid zero usage",
			usage:         `{"input_tokens":0,"input_tokens_details":{"cached_tokens":0},"output_tokens":0,"output_tokens_details":{"reasoning_tokens":0},"total_tokens":0}`,
			wantCached:    0,
			wantReasoning: 0,
		},
		{
			name:          "highest valid non-overflow boundary",
			usage:         fmt.Sprintf(`{"input_tokens":%d,"input_tokens_details":{"cached_tokens":%d},"output_tokens":0,"output_tokens_details":{"reasoning_tokens":0},"total_tokens":%d}`, maxSupportedInt, maxSupportedInt, maxSupportedInt),
			wantPrompt:    int(maxSupportedInt),
			wantTotal:     int(maxSupportedInt),
			wantCached:    int(maxSupportedInt),
			wantReasoning: 0,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := responsesSSE(
				`{"type":"response.output_item.done","output_index":0,"item":{"id":"cmp-usage","type":"compaction","encrypted_content":"encrypted"}}`,
				fmt.Sprintf(`{"type":"response.completed","response":{"id":"resp-usage","status":"completed","usage":%s}}`, test.usage),
			)
			recorder, info, done := startDirectResponsesStreamWithBody(
				t,
				io.NopCloser(strings.NewReader(body)),
				remoteCompactionV2Request(),
				context.Background(),
				nil,
			)

			var result directResponsesStreamResult
			select {
			case result = <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("timed out waiting for usage validation")
			}

			if test.wantErr {
				require.NotNil(t, result.apiErr)
				assert.Equal(t, types.ErrorCodeBadResponse, result.apiErr.GetErrorCode())
				assert.Equal(t, "upstream Responses compaction completion usage is invalid", result.apiErr.Error())
				assert.Empty(t, recorder.Body.String())
				assert.False(t, info.HasSendResponse())
				return
			}

			require.Nil(t, result.apiErr)
			require.NotNil(t, result.usage)
			assert.Equal(t, test.wantPrompt, result.usage.PromptTokens)
			assert.Equal(t, test.wantCompletion, result.usage.CompletionTokens)
			assert.Equal(t, test.wantTotal, result.usage.TotalTokens)
			assert.Equal(t, test.wantCached, result.usage.PromptTokensDetails.CachedTokens)
			assert.Equal(t, test.wantReasoning, result.usage.CompletionTokenDetails.ReasoningTokens)
			assert.NotEmpty(t, recorder.Body.String())
			assert.True(t, info.HasSendResponse())
		})
	}
}

func TestOaiResponsesStreamHandlerRemoteCompactionV2WaitsForExactLimitTerminalConfirmation(t *testing.T) {
	terminal := make(chan struct{})
	body := &terminalControlledReadCloser{
		reader:       strings.NewReader(remoteCompactionStreamAtRawLimit(t)),
		terminal:     terminal,
		probeStarted: make(chan struct{}),
		closed:       make(chan struct{}),
	}
	var observedWriter *observingResponseWriter
	recorder, _, done := startDirectResponsesStreamWithBody(
		t,
		body,
		remoteCompactionV2Request(),
		context.Background(),
		func(writer gin.ResponseWriter) gin.ResponseWriter {
			observedWriter = &observingResponseWriter{ResponseWriter: writer}
			return observedWriter
		},
	)

	select {
	case <-body.probeStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for exact-limit terminal probe")
	}
	assert.Zero(t, observedWriter.writes.Load())
	close(terminal)

	select {
	case result := <-done:
		require.Nil(t, result.apiErr)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for terminal confirmation")
	}
	assert.Equal(t, int64(1), observedWriter.writes.Load())
	assert.Contains(t, recorder.Body.String(), `"id":"resp-limit"`)
}

func TestOaiResponsesStreamHandlerRemoteCompactionV2RejectsExactLimitPlusOneBeforeCommit(t *testing.T) {
	body := remoteCompactionStreamAtRawLimit(t) + "x"

	recorder, info, apiErr := runDirectResponsesStreamWithBody(
		t,
		io.NopCloser(strings.NewReader(body)),
		remoteCompactionV2Request(),
		context.Background(),
	)

	require.NotNil(t, apiErr)
	assert.Equal(t, types.ErrorCodeBadResponseBody, apiErr.GetErrorCode())
	assert.Empty(t, recorder.Body.String())
	assert.False(t, info.HasSendResponse())
}

func TestOaiResponsesStreamHandlerRemoteCompactionV2RawOverflowPrecedesSemanticFailure(t *testing.T) {
	overflowRead := make(chan struct{})
	requestContext := &errCallBarrierContext{
		Context: context.Background(),
		target:  7,
		reached: make(chan struct{}),
	}
	body := &overflowSignalingReadCloser{
		reader: strings.NewReader(remoteCompactionEventsAtRawLimit(
			t,
			`{"type":"response.output_item.done","item":{"type":"compaction","encrypted_content":"encrypted"}}`,
			`{"type":"response.completed","response":{"id":"resp-invalid-end-turn","end_turn":"invalid"}}`,
			`{"type":"response.created","response":{"id":"after-semantic-decision"}}`,
		) + "x"),
		probeStarted: make(chan struct{}),
		allowRead:    make(chan struct{}),
		overflowRead: overflowRead,
		closed:       make(chan struct{}),
	}
	t.Cleanup(body.releaseOverflow)
	recorder, info, done := startDirectResponsesStreamWithBody(
		t,
		body,
		remoteCompactionV2Request(),
		requestContext,
		nil,
	)

	select {
	case <-body.probeStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for semantic overflow probe")
	}
	select {
	case <-requestContext.reached:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for semantic decision barrier")
	}
	body.releaseOverflow()
	var apiErr *types.NewAPIError
	select {
	case result := <-done:
		apiErr = result.apiErr
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for semantic overflow result")
	}

	require.NotNil(t, apiErr)
	assert.Equal(t, types.ErrorCodeBadResponseBody, apiErr.GetErrorCode())
	assert.Empty(t, recorder.Body.String())
	assert.False(t, info.HasSendResponse())
}

func TestOaiResponsesStreamHandlerRemoteCompactionV2DoesNotCommitWhileOverflowRacesCompletion(t *testing.T) {
	overflowRead := make(chan struct{})
	requestContext := &errCallBarrierContext{
		Context: context.Background(),
		target:  9,
		reached: make(chan struct{}),
	}
	body := &overflowSignalingReadCloser{
		reader: strings.NewReader(remoteCompactionEventsAtRawLimit(
			t,
			`{"type":"response.output_item.done","item":{"type":"compaction","encrypted_content":"encrypted"}}`,
			`{"type":"response.completed","response":{"id":"resp-limit","usage":{"input_tokens":1,"output_tokens":0,"total_tokens":1}}}`,
			`{"type":"response.created","response":{"id":"after-completion-decision"}}`,
		) + "x"),
		probeStarted: make(chan struct{}),
		allowRead:    make(chan struct{}),
		overflowRead: overflowRead,
		closed:       make(chan struct{}),
	}
	t.Cleanup(body.releaseOverflow)
	var observedWriter *observingResponseWriter

	recorder, info, done := startDirectResponsesStreamWithBody(
		t,
		body,
		remoteCompactionV2Request(),
		requestContext,
		func(writer gin.ResponseWriter) gin.ResponseWriter {
			observedWriter = &observingResponseWriter{ResponseWriter: writer}
			return observedWriter
		},
	)

	select {
	case <-body.probeStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for overflow probe")
	}
	select {
	case <-requestContext.reached:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for completion decision barrier")
	}
	assert.Zero(t, observedWriter.writes.Load())
	body.releaseOverflow()

	select {
	case result := <-done:
		require.NotNil(t, result.apiErr)
		assert.Equal(t, types.ErrorCodeBadResponseBody, result.apiErr.GetErrorCode())
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for overflow race")
	}
	assert.Empty(t, recorder.Body.String())
	assert.False(t, info.HasSendResponse())
	assert.Zero(t, observedWriter.writes.Load())
}

func TestOaiResponsesStreamHandlerRemoteCompactionV2CommitsValidExactLimitOnce(t *testing.T) {
	var observedWriter *observingResponseWriter

	recorder, _, done := startDirectResponsesStreamWithBody(
		t,
		io.NopCloser(strings.NewReader(remoteCompactionStreamAtRawLimit(t))),
		remoteCompactionV2Request(),
		context.Background(),
		func(writer gin.ResponseWriter) gin.ResponseWriter {
			observedWriter = &observingResponseWriter{ResponseWriter: writer}
			return observedWriter
		},
	)

	select {
	case result := <-done:
		require.Nil(t, result.apiErr)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for exact-limit stream")
	}
	assert.Equal(t, int64(1), observedWriter.writes.Load())
	assert.Contains(t, recorder.Body.String(), `"id":"resp-limit"`)
}

func TestOaiResponsesStreamHandlerRemoteCompactionV2DoneTerminatesWithoutEOF(t *testing.T) {
	terminal := make(chan struct{})
	stream := responsesSSE(
		`{"type":"response.output_item.done","item":{"type":"compaction","encrypted_content":"encrypted"}}`,
		`{"type":"response.completed","response":{"id":"resp-done-no-eof"}}`,
	) + "data: [DONE]\n\n"
	body := &terminalControlledReadCloser{
		reader:       strings.NewReader(remoteCompactionPayloadAtRawLimit(t, stream)),
		terminal:     terminal,
		probeStarted: make(chan struct{}),
		closed:       make(chan struct{}),
	}

	recorder, _, done := startDirectResponsesStreamWithBody(
		t,
		body,
		remoteCompactionV2Request(),
		context.Background(),
		nil,
	)

	select {
	case result := <-done:
		require.Nil(t, result.apiErr)
	case <-body.probeStarted:
		t.Fatal("scanner waited for EOF after [DONE]")
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for [DONE] termination")
	}
	assert.Contains(t, recorder.Body.String(), `"id":"resp-done-no-eof"`)
}

func TestOaiResponsesStreamHandlerRemoteCompactionV2CancellationDuringFinalBufferingDoesNotCommit(t *testing.T) {
	baseContext, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	requestContext := &errCallBarrierContext{
		Context: baseContext,
		target:  9,
		reached: make(chan struct{}),
	}
	terminal := make(chan struct{})
	body := &terminalControlledReadCloser{
		reader: strings.NewReader(remoteCompactionEventsAtRawLimit(
			t,
			`{"type":"response.output_item.done","item":{"type":"compaction","encrypted_content":"encrypted"}}`,
			`{"type":"response.completed","response":{"id":"resp-cancel-final","usage":{"input_tokens":1,"output_tokens":0,"total_tokens":1}}}`,
			`{"type":"response.created","response":{"id":"after-cancel-decision"}}`,
		)),
		terminal:     terminal,
		probeStarted: make(chan struct{}),
		closed:       make(chan struct{}),
	}
	var observedWriter *observingResponseWriter
	recorder, info, done := startDirectResponsesStreamWithBody(
		t,
		body,
		remoteCompactionV2Request(),
		requestContext,
		func(writer gin.ResponseWriter) gin.ResponseWriter {
			observedWriter = &observingResponseWriter{ResponseWriter: writer}
			return observedWriter
		},
	)

	select {
	case <-body.probeStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for final buffer probe")
	}
	select {
	case <-requestContext.reached:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for final buffering decision barrier")
	}
	assert.Zero(t, observedWriter.writes.Load())
	cancel()

	select {
	case result := <-done:
		require.Nil(t, result.apiErr)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for cancellation")
	}
	assert.Empty(t, recorder.Body.String())
	assert.Zero(t, observedWriter.writes.Load())
	assert.Zero(t, observedWriter.flushes.Load())
	require.NotNil(t, info.StreamStatus)
	assert.Equal(t, relaycommon.StreamEndReasonClientGone, info.StreamStatus.EndReason)
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
