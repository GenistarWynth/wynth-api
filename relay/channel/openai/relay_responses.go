package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

const (
	responsesPreludeBufferLimit = 1 << 20
	// Upstream SSE can contain event names, comments, and blank-line framing that
	// are not copied into the buffered downstream response.
	compactResponseStreamFramingAllowance = 64 << 10
	compactResponseStreamReadLimit        = compactResponseBodyLimit + compactResponseStreamFramingAllowance
	remoteCompactionInvalidUsageMessage   = "upstream Responses compaction completion usage is invalid"
)

var errRemoteCompactionStreamTooLarge = errors.New("upstream Responses compaction stream exceeds size limit")

type remoteCompactionStreamReader struct {
	io.ReadCloser
	mu          sync.Mutex
	remaining   int64
	terminalErr error
	exhausted   atomic.Bool
}

func (r *remoteCompactionStreamReader) Read(data []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(data) == 0 {
		return 0, nil
	}
	if r.terminalErr != nil {
		return 0, r.terminalErr
	}
	if r.remaining <= 0 {
		r.exhausted.Store(true)
		var sentinel [1]byte
		// Tolerate transient empty reads without spinning forever on a broken Reader.
		for range 100 {
			n, err := r.ReadCloser.Read(sentinel[:])
			if n > 0 {
				r.terminalErr = errRemoteCompactionStreamTooLarge
				return 0, r.terminalErr
			}
			if err != nil {
				r.terminalErr = err
				return 0, r.terminalErr
			}
		}
		r.terminalErr = io.ErrNoProgress
		return 0, r.terminalErr
	}
	if int64(len(data)) > r.remaining {
		data = data[:r.remaining]
	}
	n, err := r.ReadCloser.Read(data)
	r.remaining -= int64(n)
	if r.remaining <= 0 {
		r.exhausted.Store(true)
		if err != nil {
			r.terminalErr = err
		}
	}
	return n, err
}

func (r *remoteCompactionStreamReader) LimitExhausted() bool {
	return r != nil && r.exhausted.Load()
}

func (r *remoteCompactionStreamReader) TerminalError() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.terminalErr
}

func (r *remoteCompactionStreamReader) StopUnlessLimitNeedsConfirmation(sr *helper.StreamResult, err error) {
	if r == nil || !r.LimitExhausted() {
		sr.Stop(err)
	}
}

type codexRemoteCompactionStreamEvent struct {
	Type     string          `json:"type"`
	Item     json.RawMessage `json:"item"`
	Response json.RawMessage `json:"response"`
}

type codexRemoteCompactionItem struct {
	Type             string  `json:"type"`
	ID               *string `json:"id"`
	EncryptedContent *string `json:"encrypted_content"`
	Metadata         *struct {
		TurnID *string `json:"turn_id"`
	} `json:"internal_chat_message_metadata_passthrough"`
}

type codexResponseCompleted struct {
	ID      *string                      `json:"id"`
	EndTurn *bool                        `json:"end_turn"`
	Usage   *codexResponseCompletedUsage `json:"usage"`
}

type codexResponseCompletedUsage struct {
	InputTokens        *int64 `json:"input_tokens"`
	InputTokensDetails *struct {
		CachedTokens     *int64          `json:"cached_tokens"`
		CacheWriteTokens json.RawMessage `json:"cache_write_tokens"`
	} `json:"input_tokens_details"`
	OutputTokens        *int64 `json:"output_tokens"`
	OutputTokensDetails *struct {
		ReasoningTokens *int64 `json:"reasoning_tokens"`
	} `json:"output_tokens_details"`
	TotalTokens *int64 `json:"total_tokens"`
}

type remoteCompactionCommitState uint8

const (
	remoteCompactionPrecommit remoteCompactionCommitState = iota
	remoteCompactionCancelled
	remoteCompactionCommitting
	remoteCompactionCommitted
	remoteCompactionFailed
)

type remoteCompactionCommitGate struct {
	mu                  sync.Mutex
	state               remoteCompactionCommitState
	requestContext      context.Context
	cancellationErr     error
	cancellationDone    chan struct{}
	commitErr           error
	relayInfo           *relaycommon.RelayInfo
	stopCancellation    func() bool
	stopCalled          bool
	cancellationStopped bool
}

func newRemoteCompactionCommitGate(requestContext context.Context, info *relaycommon.RelayInfo) *remoteCompactionCommitGate {
	if requestContext == nil {
		requestContext = context.Background()
	}
	gate := &remoteCompactionCommitGate{
		state:            remoteCompactionPrecommit,
		requestContext:   requestContext,
		cancellationDone: make(chan struct{}),
		relayInfo:        info,
	}
	gate.stopCancellation = context.AfterFunc(requestContext, func() {
		defer close(gate.cancellationDone)
		gate.cancel(requestContext.Err())
	})
	return gate
}

func (g *remoteCompactionCommitGate) cancel(err error) bool {
	if g == nil {
		return false
	}
	if err == nil {
		err = context.Canceled
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.state != remoteCompactionPrecommit {
		return false
	}
	g.state = remoteCompactionCancelled
	g.cancellationErr = err
	return true
}

func (g *remoteCompactionCommitGate) markClientGone(err error) {
	if g == nil || g.relayInfo == nil {
		return
	}
	status := relaycommon.NewStreamStatus()
	status.SetEndReason(relaycommon.StreamEndReasonClientGone, err)
	g.relayInfo.StreamStatus = status
}

func (g *remoteCompactionCommitGate) observeCancellation(err error) error {
	if g == nil {
		return nil
	}

	g.mu.Lock()
	if g.state == remoteCompactionPrecommit {
		if err == nil {
			err = g.requestContext.Err()
		}
		if err != nil {
			g.state = remoteCompactionCancelled
			g.cancellationErr = err
		}
	}
	if g.state == remoteCompactionCancelled {
		err = g.cancellationErr
	} else {
		err = nil
	}
	g.mu.Unlock()

	if err != nil {
		g.markClientGone(err)
	}
	return err
}

func (g *remoteCompactionCommitGate) commit(write func() error) error {
	if g == nil {
		return write()
	}

	g.mu.Lock()
	switch g.state {
	case remoteCompactionCancelled:
		err := g.cancellationErr
		g.mu.Unlock()
		g.stop()
		g.markClientGone(err)
		return err
	case remoteCompactionCommitted:
		g.mu.Unlock()
		return nil
	case remoteCompactionFailed:
		err := g.commitErr
		g.mu.Unlock()
		return err
	}

	requestErr := g.requestContext.Err()
	if !g.stopCalled {
		g.cancellationStopped = g.stopCancellation()
		g.stopCalled = true
	}
	if !g.cancellationStopped {
		cancellationDone := g.cancellationDone
		g.mu.Unlock()
		// A false stop result means the callback owns cancellation. It may be
		// waiting for g.mu, so wait only after releasing the gate.
		<-cancellationDone

		g.mu.Lock()
		err := g.cancellationErr
		if err == nil {
			err = requestErr
		}
		if err == nil {
			err = context.Canceled
		}
		g.mu.Unlock()
		g.markClientGone(err)
		return err
	}

	// The successful stopCancellation return above is the commit linearization
	// point: it proves the cancellation callback cannot start. The state change
	// records that ownership before the first downstream write.
	g.state = remoteCompactionCommitting
	if g.relayInfo != nil {
		g.relayInfo.MarkDownstreamCommitted()
	}
	err := write()
	g.commitErr = err
	if err != nil {
		g.state = remoteCompactionFailed
	} else {
		g.state = remoteCompactionCommitted
	}
	g.mu.Unlock()
	return err
}

func (g *remoteCompactionCommitGate) stop() {
	if g == nil || g.stopCancellation == nil {
		return
	}

	g.mu.Lock()
	if !g.stopCalled {
		g.cancellationStopped = g.stopCancellation()
		g.stopCalled = true
	}
	waitForCancellation := !g.cancellationStopped
	cancellationDone := g.cancellationDone
	g.mu.Unlock()

	if waitForCancellation {
		<-cancellationDone
		g.mu.Lock()
		cancelled := g.state == remoteCompactionCancelled
		cancellationErr := g.cancellationErr
		g.mu.Unlock()
		if cancelled {
			g.markClientGone(cancellationErr)
		}
	}
}

type responsesPreludeWriter struct {
	gin.ResponseWriter
	buffer     bytes.Buffer
	limit      int
	committed  bool
	err        error
	commitGate *remoteCompactionCommitGate
}

func newResponsesPreludeWriter(writer gin.ResponseWriter, limit int) *responsesPreludeWriter {
	return &responsesPreludeWriter{
		ResponseWriter: writer,
		limit:          limit,
	}
}

func newRemoteCompactionPreludeWriter(
	writer gin.ResponseWriter,
	limit int,
	requestContext context.Context,
	info *relaycommon.RelayInfo,
) *responsesPreludeWriter {
	preludeWriter := newResponsesPreludeWriter(writer, limit)
	preludeWriter.commitGate = newRemoteCompactionCommitGate(requestContext, info)
	return preludeWriter
}

func (w *responsesPreludeWriter) Write(data []byte) (int, error) {
	if w.committed {
		return w.ResponseWriter.Write(data)
	}
	if w.err != nil {
		return 0, w.err
	}
	if len(data) > w.limit-w.buffer.Len() {
		w.err = errors.New("upstream Responses stream prelude exceeds size limit")
		return 0, w.err
	}
	return w.buffer.Write(data)
}

func (w *responsesPreludeWriter) WriteString(data string) (int, error) {
	if w.committed {
		return w.ResponseWriter.WriteString(data)
	}
	return w.Write([]byte(data))
}

func (w *responsesPreludeWriter) Flush() {
	if w.committed {
		w.ResponseWriter.Flush()
	}
}

func (w *responsesPreludeWriter) Commit() error {
	if w.err != nil {
		return w.err
	}
	if w.commitGate != nil {
		return w.commitGate.commit(w.commit)
	}
	return w.commit()
}

func (w *responsesPreludeWriter) commit() error {
	if w.err != nil {
		return w.err
	}
	w.committed = true
	if w.buffer.Len() > 0 {
		data := w.buffer.Bytes()
		written, err := w.ResponseWriter.Write(data)
		if err != nil {
			return err
		}
		if written != len(data) {
			return io.ErrShortWrite
		}
		w.buffer.Reset()
	}
	w.ResponseWriter.Flush()
	return nil
}

func (w *responsesPreludeWriter) stopCancellationWatch() {
	if w != nil {
		w.commitGate.stop()
	}
}

func (w *responsesPreludeWriter) observeCancellation(err error) error {
	if w == nil || w.commitGate == nil {
		return nil
	}
	return w.commitGate.observeCancellation(err)
}

func (w *responsesPreludeWriter) Error() error {
	return w.err
}

func OaiResponsesHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	defer service.CloseResponseBodyGracefully(resp)

	// read response body
	var responsesResponse dto.OpenAIResponsesResponse
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeReadResponseBodyFailed, http.StatusInternalServerError)
	}
	err = common.Unmarshal(responseBody, &responsesResponse)
	if err != nil {
		return nil, types.NewUpstreamBodyDecodeError(err, resp, responseBody)
	}
	status := strings.ToLower(strings.TrimSpace(common.JsonRawMessageToString(responsesResponse.Status)))
	if status == "failed" || status == "error" || responsesResponse.GetOpenAIError() != nil {
		return nil, responsesAPIError(responsesResponse.GetOpenAIError(), "upstream Responses request failed")
	}
	if !responsesResponseHasMeaningfulOutput(&responsesResponse) {
		return nil, responsesEmptyError("upstream Responses request returned no meaningful output")
	}
	if info != nil {
		info.SetActualResponseModel(responsesResponse.Model, relaycommon.ActualResponseModelSourceOpenAIResponses)
	}

	// 写入新的 response body
	service.IOCopyBytesGracefully(c, resp, responseBody)

	// compute usage
	usage := dto.Usage{}
	if responsesResponse.Usage != nil {
		usage.PromptTokens = responsesResponse.Usage.InputTokens
		usage.CompletionTokens = responsesResponse.Usage.OutputTokens
		usage.TotalTokens = responsesResponse.Usage.TotalTokens
		if responsesResponse.Usage.InputTokensDetails != nil {
			usage.PromptTokensDetails.CachedTokens = responsesResponse.Usage.InputTokensDetails.CachedTokens
			usage.PromptTokensDetails.CachedCreationTokens = responsesResponse.Usage.InputTokensDetails.CachedCreationTokens

			usage.PromptTokensDetails.CacheWriteTokens = responsesResponse.Usage.InputTokensDetails.CacheWriteTokens
		}
	}
	// Count actual tool invocations from Output (not tool declarations).
	for _, output := range responsesResponse.Output {
		switch output.Type {
		case dto.BuildInCallWebSearchCall:
			info.CountBillableToolCall(dto.BuildInCallWebSearchCall, "")
		case dto.BuildInCallFileSearchCall:
			info.CountBillableToolCall(dto.BuildInCallFileSearchCall, "")
		case dto.BuildInCallFunctionCall:
			info.CountBillableToolCall(dto.BuildInCallFunctionCall, output.Name)
		}
	}

	imageCounter := &relaycommon.ImageGenerationCallCounter{}
	if !relaycommon.IsNonBillableResponsesStatus(responsesResponse.Status) {
		for i := range responsesResponse.Output {
			idx := i
			imageCounter.Observe(&responsesResponse.Output[i], &idx)
		}
	}
	imageCounter.Commit(info)

	return &usage, nil
}

func OaiResponsesStreamHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	if resp == nil || resp.Body == nil {
		logger.LogError(c, "invalid response or response body")
		return nil, types.NewError(fmt.Errorf("invalid response"), types.ErrorCodeBadResponse)
	}

	defer service.CloseResponseBodyGracefully(resp)

	isRemoteCompactionV2 := false
	if request, ok := info.Request.(*dto.OpenAIResponsesRequest); ok {
		isRemoteCompactionV2 = request.IsRemoteCompactionV2()
	}
	bufferLimit := responsesPreludeBufferLimit
	var compactionStreamReader *remoteCompactionStreamReader
	if isRemoteCompactionV2 {
		bufferLimit = compactResponseBodyLimit
		compactionStreamReader = &remoteCompactionStreamReader{
			ReadCloser: resp.Body,
			remaining:  compactResponseStreamReadLimit,
		}
		resp.Body = compactionStreamReader
	}

	originalWriter := c.Writer
	preludeWriter := newResponsesPreludeWriter(originalWriter, bufferLimit)
	if isRemoteCompactionV2 {
		requestContext := context.Background()
		if c != nil && c.Request != nil {
			requestContext = c.Request.Context()
		}
		preludeWriter = newRemoteCompactionPreludeWriter(originalWriter, bufferLimit, requestContext, info)
	}
	c.Writer = preludeWriter
	defer func() {
		c.Writer = originalWriter
	}()
	defer preludeWriter.stopCancellationWatch()

	var usage = &dto.Usage{}
	var responseTextBuilder strings.Builder
	var semanticError *types.NewAPIError
	hasMeaningfulOutput := false
	compactionOutputCount := 0
	outputItemCount := 0
	imageCounter := &relaycommon.ImageGenerationCallCounter{}
	imageCommitted := false

	termination := helper.StreamScannerHandler(c, resp, info, func(data string, sr *helper.StreamResult) {
		if isRemoteCompactionV2 && semanticError != nil {
			return
		}
		var streamResponse dto.ResponsesStreamResponse
		eventIsMeaningful := false
		if isRemoteCompactionV2 {
			var remoteEvent codexRemoteCompactionStreamEvent
			if err := common.UnmarshalJsonStr(data, &remoteEvent); err != nil {
				semanticError = responsesCompactionError("upstream Responses compaction stream returned invalid event JSON")
				compactionStreamReader.StopUnlessLimitNeedsConfirmation(sr, semanticError)
				return
			}
			streamResponse.Type = remoteEvent.Type
			if len(remoteEvent.Response) > 0 {
				var responseMetadata struct {
					Model any `json:"model"`
				}
				if common.Unmarshal(remoteEvent.Response, &responseMetadata) == nil {
					if model, ok := responseMetadata.Model.(string); ok {
						streamResponse.Response = &dto.OpenAIResponsesResponse{Model: model}
					}
				}
			}
			switch streamResponse.Type {
			case dto.ResponsesOutputTypeItemDone:
				outputItemCount++
				trimmedItem := bytes.TrimSpace(remoteEvent.Item)
				if len(trimmedItem) == 0 || bytes.Equal(trimmedItem, []byte("null")) {
					semanticError = responsesCompactionError("upstream Responses compaction stream returned an output item without an item payload")
					compactionStreamReader.StopUnlessLimitNeedsConfirmation(sr, semanticError)
					return
				}
				var itemType struct {
					Type string `json:"type"`
				}
				if common.Unmarshal(remoteEvent.Item, &itemType) == nil {
					streamResponse.Item = &dto.ResponsesOutput{Type: itemType.Type}
				}
				if itemType.Type == "compaction" || itemType.Type == "compaction_summary" {
					var item codexRemoteCompactionItem
					if err := common.Unmarshal(remoteEvent.Item, &item); err != nil {
						semanticError = responsesCompactionError("upstream Responses compaction output does not match the Codex wire schema")
						compactionStreamReader.StopUnlessLimitNeedsConfirmation(sr, semanticError)
						return
					}
					if item.EncryptedContent == nil {
						semanticError = responsesCompactionError("upstream Responses compaction output is missing encrypted_content")
						compactionStreamReader.StopUnlessLimitNeedsConfirmation(sr, semanticError)
						return
					}
					if item.ID != nil {
						streamResponse.Item.ID = *item.ID
					}
					streamResponse.Item.EncryptedContent = item.EncryptedContent
					compactionOutputCount++
					if compactionOutputCount > 1 {
						semanticError = responsesCompactionError(fmt.Sprintf(
							"upstream Responses compaction expected exactly one compaction output item, got %d from %d output items",
							compactionOutputCount,
							outputItemCount,
						))
						compactionStreamReader.StopUnlessLimitNeedsConfirmation(sr, semanticError)
						return
					}
				}
			case "response.completed":
				var completed codexResponseCompleted
				if err := common.Unmarshal(remoteEvent.Response, &completed); err != nil {
					semanticError = responsesCompactionError("upstream Responses compaction completion does not match the Codex wire schema")
					compactionStreamReader.StopUnlessLimitNeedsConfirmation(sr, semanticError)
					return
				}
				if completed.ID == nil {
					semanticError = responsesCompactionError("upstream Responses compaction completion is missing response.id")
					compactionStreamReader.StopUnlessLimitNeedsConfirmation(sr, semanticError)
					return
				}
				if completed.Usage != nil {
					completedUsage := completed.Usage
					if completedUsage.InputTokens == nil || completedUsage.OutputTokens == nil || completedUsage.TotalTokens == nil ||
						(completedUsage.InputTokensDetails != nil && completedUsage.InputTokensDetails.CachedTokens == nil) ||
						(completedUsage.OutputTokensDetails != nil && completedUsage.OutputTokensDetails.ReasoningTokens == nil) {
						semanticError = responsesCompactionError("upstream Responses compaction completion usage does not match the Codex wire schema")
						compactionStreamReader.StopUnlessLimitNeedsConfirmation(sr, semanticError)
						return
					}
					cacheWriteTokens := int64(0)
					cacheWriteTokensPresent := completedUsage.InputTokensDetails != nil &&
						len(completedUsage.InputTokensDetails.CacheWriteTokens) > 0
					if cacheWriteTokensPresent {
						cacheWriteTokensJSON := completedUsage.InputTokensDetails.CacheWriteTokens
						if common.GetJsonType(cacheWriteTokensJSON) != "number" ||
							common.Unmarshal(cacheWriteTokensJSON, &cacheWriteTokens) != nil {
							semanticError = responsesCompactionError(remoteCompactionInvalidUsageMessage)
							compactionStreamReader.StopUnlessLimitNeedsConfirmation(sr, semanticError)
							return
						}
					}
					wireIntegers := []*int64{
						completedUsage.InputTokens,
						completedUsage.OutputTokens,
						completedUsage.TotalTokens,
					}
					if completedUsage.InputTokensDetails != nil {
						wireIntegers = append(wireIntegers, completedUsage.InputTokensDetails.CachedTokens, &cacheWriteTokens)
					}
					if completedUsage.OutputTokensDetails != nil {
						wireIntegers = append(wireIntegers, completedUsage.OutputTokensDetails.ReasoningTokens)
					}
					maxSupportedInt := int64(^uint(0) >> 1)
					for _, value := range wireIntegers {
						if *value < 0 || *value > maxSupportedInt {
							semanticError = responsesCompactionError(remoteCompactionInvalidUsageMessage)
							compactionStreamReader.StopUnlessLimitNeedsConfirmation(sr, semanticError)
							return
						}
					}
					const maxInt64 = int64(^uint64(0) >> 1)
					if *completedUsage.InputTokens > maxInt64-*completedUsage.OutputTokens ||
						*completedUsage.TotalTokens != *completedUsage.InputTokens+*completedUsage.OutputTokens {
						semanticError = responsesCompactionError(remoteCompactionInvalidUsageMessage)
						compactionStreamReader.StopUnlessLimitNeedsConfirmation(sr, semanticError)
						return
					}
					if cacheWriteTokensPresent &&
						(*completedUsage.InputTokensDetails.CachedTokens > *completedUsage.InputTokens ||
							cacheWriteTokens > *completedUsage.InputTokens-*completedUsage.InputTokensDetails.CachedTokens) {
						semanticError = responsesCompactionError(remoteCompactionInvalidUsageMessage)
						compactionStreamReader.StopUnlessLimitNeedsConfirmation(sr, semanticError)
						return
					}
					usage.PromptTokens = int(*completedUsage.InputTokens)
					usage.CompletionTokens = int(*completedUsage.OutputTokens)
					usage.TotalTokens = int(*completedUsage.TotalTokens)
					if completedUsage.InputTokensDetails != nil {
						usage.PromptTokensDetails.CachedTokens = int(*completedUsage.InputTokensDetails.CachedTokens)
						usage.PromptTokensDetails.CacheWriteTokens = int(cacheWriteTokens)
					}
					if completedUsage.OutputTokensDetails != nil {
						usage.CompletionTokenDetails.ReasoningTokens = int(*completedUsage.OutputTokensDetails.ReasoningTokens)
					}
				}
				if compactionOutputCount != 1 {
					semanticError = responsesCompactionError(fmt.Sprintf(
						"upstream Responses compaction expected exactly one compaction output item, got %d from %d output items",
						compactionOutputCount,
						outputItemCount,
					))
					compactionStreamReader.StopUnlessLimitNeedsConfirmation(sr, semanticError)
					return
				}
				model := ""
				if streamResponse.Response != nil {
					model = streamResponse.Response.Model
				}
				streamResponse.Response = &dto.OpenAIResponsesResponse{
					ID:    *completed.ID,
					Model: model,
				}
				eventIsMeaningful = true
			case "response.incomplete":
				semanticError = responsesCompactionError("upstream Responses compaction stream ended incomplete")
				compactionStreamReader.StopUnlessLimitNeedsConfirmation(sr, semanticError)
				return
			case "response.failed", "response.error":
				if err := common.UnmarshalJsonStr(data, &streamResponse); err != nil {
					semanticError = responsesCompactionError("upstream Responses compaction failure event is invalid")
					compactionStreamReader.StopUnlessLimitNeedsConfirmation(sr, semanticError)
					return
				}
			}
		} else {
			if err := common.UnmarshalJsonStr(data, &streamResponse); err != nil {
				logger.LogError(c, "failed to unmarshal stream response: "+err.Error())
				sr.Error(err)
				return
			}
			eventIsMeaningful = responsesStreamEventHasMeaningfulOutput(&streamResponse)
		}
		if streamResponse.Response != nil {
			info.SetActualResponseModel(streamResponse.Response.Model, relaycommon.ActualResponseModelSourceOpenAIResponses)
		}
		switch streamResponse.Type {
		case "response.failed", "response.error":
			oaiError := dto.GetOpenAIError(streamResponse.Error)
			if streamResponse.Response != nil && streamResponse.Response.GetOpenAIError() != nil {
				oaiError = streamResponse.Response.GetOpenAIError()
			}
			semanticError = responsesAPIError(oaiError, "upstream Responses stream failed")
			if hasMeaningfulOutput {
				if err := helper.ResponseChunkData(c, streamResponse, data); err != nil {
					compactionStreamReader.StopUnlessLimitNeedsConfirmation(sr, err)
					return
				}
			}
			compactionStreamReader.StopUnlessLimitNeedsConfirmation(sr, semanticError)
			return
		case "response.completed":
			if !hasMeaningfulOutput && !eventIsMeaningful {
				semanticError = responsesEmptyError("upstream Responses stream completed without meaningful output")
				compactionStreamReader.StopUnlessLimitNeedsConfirmation(sr, semanticError)
				return
			}
		case "response.incomplete":
			if !hasMeaningfulOutput && !eventIsMeaningful {
				semanticError = responsesEmptyError("upstream Responses stream was incomplete without meaningful output")
				compactionStreamReader.StopUnlessLimitNeedsConfirmation(sr, semanticError)
				return
			}
		}

		if !hasMeaningfulOutput && !eventIsMeaningful {
			if err := helper.ResponseChunkData(c, streamResponse, data); err != nil {
				compactionStreamReader.StopUnlessLimitNeedsConfirmation(sr, err)
				return
			}
			if preludeWriter.Error() != nil {
				semanticError = responsesBodyLimitError()
				compactionStreamReader.StopUnlessLimitNeedsConfirmation(sr, semanticError)
				return
			}
			return
		}

		eventBuffered := false
		if !hasMeaningfulOutput {
			if isRemoteCompactionV2 {
				if err := helper.ResponseChunkData(c, streamResponse, data); err != nil {
					compactionStreamReader.StopUnlessLimitNeedsConfirmation(sr, err)
					return
				}
				eventBuffered = true
				if preludeWriter.Error() != nil {
					semanticError = responsesBodyLimitError()
					compactionStreamReader.StopUnlessLimitNeedsConfirmation(sr, semanticError)
					return
				}
			}
			if !isRemoteCompactionV2 {
				if err := preludeWriter.Commit(); err != nil {
					compactionStreamReader.StopUnlessLimitNeedsConfirmation(sr, err)
					return
				}
			}
			hasMeaningfulOutput = true
		}

		if !eventBuffered {
			if err := helper.ResponseChunkData(c, streamResponse, data); err != nil {
				compactionStreamReader.StopUnlessLimitNeedsConfirmation(sr, err)
				return
			}
		}

		switch streamResponse.Type {
		case "response.completed", "response.done":
			if streamResponse.Response != nil {
				if streamResponse.Response.Usage != nil {
					if streamResponse.Response.Usage.InputTokens != 0 {
						usage.PromptTokens = streamResponse.Response.Usage.InputTokens
					}
					if streamResponse.Response.Usage.OutputTokens != 0 {
						usage.CompletionTokens = streamResponse.Response.Usage.OutputTokens
					}
					if streamResponse.Response.Usage.TotalTokens != 0 {
						usage.TotalTokens = streamResponse.Response.Usage.TotalTokens
					}
					if streamResponse.Response.Usage.InputTokensDetails != nil {
						usage.PromptTokensDetails.CachedTokens = streamResponse.Response.Usage.InputTokensDetails.CachedTokens
						usage.PromptTokensDetails.CachedCreationTokens = streamResponse.Response.Usage.InputTokensDetails.CachedCreationTokens

						usage.PromptTokensDetails.CacheWriteTokens = streamResponse.Response.Usage.InputTokensDetails.CacheWriteTokens
					}
				}
				if !imageCommitted {
					if relaycommon.IsNonBillableResponsesStatus(streamResponse.Response.Status) {
						imageCounter.Reset()
						imageCounter.Commit(info)
						imageCommitted = true
					} else {
						for i := range streamResponse.Response.Output {
							idx := i
							imageCounter.Observe(&streamResponse.Response.Output[i], &idx)
						}
						imageCounter.Commit(info)
						imageCommitted = true
					}
				}
			} else if !imageCommitted {
				imageCounter.Commit(info)
				imageCommitted = true
			}
		case "response.failed", "response.cancelled", "response.canceled":
			if !imageCommitted {
				imageCounter.Reset()
				imageCounter.Commit(info)
				imageCommitted = true
			}
			if !isRemoteCompactionV2 || !compactionStreamReader.LimitExhausted() {
				sr.Done()
			}
		case "response.incomplete":
			if !imageCommitted {
				imageCounter.Reset()
				imageCounter.Commit(info)
				imageCommitted = true
			}
			semanticError = responsesIncompleteError(streamResponse.Response)
			compactionStreamReader.StopUnlessLimitNeedsConfirmation(sr, semanticError)
		case "response.output_text.delta":
			responseTextBuilder.WriteString(streamResponse.Delta)
		case dto.ResponsesOutputTypeItemDone:
			if streamResponse.Item != nil {
				switch streamResponse.Item.Type {
				case dto.BuildInCallWebSearchCall:
					info.CountBillableToolCall(dto.BuildInCallWebSearchCall, "")
				case dto.BuildInCallFileSearchCall:
					info.CountBillableToolCall(dto.BuildInCallFileSearchCall, "")
				case dto.BuildInCallFunctionCall:
					info.CountBillableToolCall(dto.BuildInCallFunctionCall, streamResponse.Item.Name)
				case dto.ResponsesOutputTypeImageGenerationCall:
					if !imageCommitted {
						imageCounter.Observe(streamResponse.Item, streamResponse.OutputIndex)
					}
				}
			}
		}
	})

	if usage.CompletionTokens == 0 {
		// 计算输出文本的 token 数量
		tempStr := responseTextBuilder.String()
		if len(tempStr) > 0 {
			// 非正常结束，使用输出文本的 token 数量
			completionTokens := service.CountTextToken(tempStr, info.UpstreamModelName)
			usage.CompletionTokens = completionTokens
		}
	}

	if usage.PromptTokens == 0 && usage.CompletionTokens != 0 {
		usage.PromptTokens = info.GetEstimatePromptTokens()
	}

	usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens

	if !isRemoteCompactionV2 && c != nil && c.Request != nil {
		if requestErr := c.Request.Context().Err(); requestErr != nil {
			info.StreamStatus = relaycommon.NewStreamStatus()
			info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonClientGone, requestErr)
			return usage, nil
		}
	}
	if termination.IsCancelled() {
		if isRemoteCompactionV2 {
			preludeWriter.observeCancellation(termination.EndError)
		}
		return usage, nil
	}
	if isRemoteCompactionV2 && preludeWriter.observeCancellation(nil) != nil {
		return usage, nil
	}
	if errors.Is(compactionStreamReader.TerminalError(), errRemoteCompactionStreamTooLarge) ||
		errors.Is(termination.EndError, errRemoteCompactionStreamTooLarge) {
		return usage, responsesBodyLimitError()
	}
	if semanticError != nil {
		return usage, semanticError
	}
	if streamError := helper.StreamTerminationError(termination); streamError != nil {
		return usage, streamError
	}
	if !hasMeaningfulOutput {
		if isRemoteCompactionV2 {
			return usage, responsesCompactionError("upstream Responses compaction stream ended before response.completed")
		}
		return usage, responsesEmptyError("upstream Responses stream returned no meaningful output")
	}
	if isRemoteCompactionV2 {
		if err := preludeWriter.Commit(); err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return usage, nil
			}
			return usage, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
		}
	}
	return usage, nil
}

func responsesResponseHasMeaningfulOutput(response *dto.OpenAIResponsesResponse) bool {
	if response == nil {
		return false
	}
	for i := range response.Output {
		if responsesOutputIsMeaningful(&response.Output[i]) {
			return true
		}
	}
	return false
}

func responsesOutputIsMeaningful(output *dto.ResponsesOutput) bool {
	if output == nil {
		return false
	}
	outputType := strings.TrimSpace(output.Type)
	if outputType == "reasoning" {
		return false
	}
	if outputType == dto.ResponsesOutputTypeImageGenerationCall {
		return strings.TrimSpace(output.Result) != "" || strings.EqualFold(output.Status, "completed")
	}
	if outputType == "function_call" || outputType == "custom_tool_call" || strings.HasSuffix(outputType, "_call") {
		return strings.TrimSpace(output.Name) != "" ||
			strings.TrimSpace(output.CallId) != "" ||
			strings.TrimSpace(output.ID) != "" ||
			len(output.Arguments) > 0
	}
	for _, content := range output.Content {
		switch content.Type {
		case "output_text":
			if strings.TrimSpace(content.Text) != "" {
				return true
			}
		case "refusal":
			if strings.TrimSpace(content.Refusal) != "" || strings.TrimSpace(content.Text) != "" {
				return true
			}
		default:
			if strings.TrimSpace(content.Text) != "" || strings.TrimSpace(content.Refusal) != "" {
				return true
			}
		}
	}
	return false
}

func responsesStreamEventHasMeaningfulOutput(event *dto.ResponsesStreamResponse) bool {
	if event == nil {
		return false
	}
	switch event.Type {
	case "response.output_text.delta":
		return strings.TrimSpace(event.Delta) != ""
	case "response.output_text.done":
		return strings.TrimSpace(event.Text) != "" || strings.TrimSpace(event.Delta) != ""
	case "response.refusal.delta":
		return strings.TrimSpace(event.Delta) != ""
	case "response.refusal.done":
		return strings.TrimSpace(event.Refusal) != "" ||
			strings.TrimSpace(event.Text) != "" ||
			strings.TrimSpace(event.Delta) != ""
	case "response.function_call_arguments.delta",
		"response.function_call_arguments.done",
		"response.custom_tool_call_input.delta",
		"response.custom_tool_call_input.done":
		return strings.TrimSpace(event.Delta) != "" || len(event.Arguments) > 0
	case dto.ResponsesOutputTypeItemAdded, dto.ResponsesOutputTypeItemDone:
		return responsesOutputIsMeaningful(event.Item)
	case "response.completed", "response.incomplete":
		return responsesResponseHasMeaningfulOutput(event.Response)
	default:
		return strings.HasPrefix(event.Type, "response.image_generation_call.") &&
			strings.TrimSpace(event.Result) != ""
	}
}

func responsesAPIError(openAIError *types.OpenAIError, fallbackMessage string) *types.NewAPIError {
	if openAIError == nil {
		openAIError = &types.OpenAIError{}
	} else {
		cloned := *openAIError
		openAIError = &cloned
	}
	openAIError.Message = common.SanitizeSecrets(openAIError.Message)
	if openAIError.Message == "" {
		openAIError.Message = fallbackMessage
	}

	code := strings.ToLower(strings.TrimSpace(fmt.Sprintf("%v", openAIError.Code)))
	if code == "" || code == "<nil>" {
		code = strings.ToLower(strings.TrimSpace(openAIError.Type))
	}
	if code == "" {
		code = string(types.ErrorCodeBadResponse)
	}
	openAIError.Code = code
	if openAIError.Type == "" {
		openAIError.Type = "upstream_error"
	}

	classification := code + " " + strings.ToLower(openAIError.Type)
	statusCode := http.StatusBadGateway
	switch {
	case strings.Contains(classification, "rate_limit"),
		strings.Contains(classification, "too_many_requests"),
		strings.Contains(classification, "quota_exceeded"):
		statusCode = http.StatusTooManyRequests
	case strings.Contains(classification, "authentication"),
		strings.Contains(classification, "unauthorized"),
		strings.Contains(classification, "invalid_api_key"):
		statusCode = http.StatusUnauthorized
	case strings.Contains(classification, "permission"),
		strings.Contains(classification, "forbidden"):
		statusCode = http.StatusForbidden
	case strings.Contains(classification, "not_found"):
		statusCode = http.StatusNotFound
	case strings.Contains(classification, "invalid_request"),
		strings.Contains(classification, "bad_request"),
		strings.Contains(classification, "validation"),
		strings.Contains(classification, "context_length"):
		statusCode = http.StatusBadRequest
	}
	return types.WithOpenAIError(*openAIError, statusCode)
}

func responsesEmptyError(message string) *types.NewAPIError {
	return types.NewOpenAIError(
		errors.New(common.SanitizeSecrets(message)),
		types.ErrorCodeEmptyResponse,
		http.StatusBadGateway,
	)
}

func responsesCompactionError(message string) *types.NewAPIError {
	return types.NewOpenAIError(
		errors.New(common.SanitizeSecrets(message)),
		types.ErrorCodeBadResponse,
		http.StatusBadGateway,
	)
}

func responsesBodyLimitError() *types.NewAPIError {
	return types.NewOpenAIError(
		errors.New("upstream Responses stream prelude exceeds size limit"),
		types.ErrorCodeBadResponseBody,
		http.StatusBadGateway,
	)
}

func responsesIncompleteError(response *dto.OpenAIResponsesResponse) *types.NewAPIError {
	reason := ""
	if response != nil && response.IncompleteDetails != nil {
		reason = strings.TrimSpace(response.IncompleteDetails.Reason)
	}
	message := "upstream Responses stream ended incomplete after producing output"
	if reason != "" {
		message += ": " + reason
	}
	return types.NewOpenAIError(
		errors.New(common.SanitizeSecrets(message)),
		types.ErrorCodeBadResponse,
		http.StatusBadGateway,
	)
}
