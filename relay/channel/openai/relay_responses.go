package openai

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

const (
	responsesPreludeBufferLimit = 1 << 20
	// Upstream SSE can contain event names, comments, and blank-line framing that
	// are not copied into the buffered downstream response.
	compactResponseStreamFramingAllowance = 64 << 10
	compactResponseStreamReadLimit        = compactResponseBodyLimit + compactResponseStreamFramingAllowance
)

var errRemoteCompactionStreamTooLarge = errors.New("upstream Responses compaction stream exceeds size limit")

type remoteCompactionStreamReader struct {
	io.ReadCloser
	mu          sync.Mutex
	remaining   int64
	terminalErr error
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
	if r.remaining <= 0 && err != nil {
		r.terminalErr = err
	}
	return n, err
}

type responsesPreludeWriter struct {
	gin.ResponseWriter
	buffer    bytes.Buffer
	limit     int
	committed bool
	err       error
}

func newResponsesPreludeWriter(writer gin.ResponseWriter, limit int) *responsesPreludeWriter {
	return &responsesPreludeWriter{
		ResponseWriter: writer,
		limit:          limit,
	}
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

	if responsesResponse.HasImageGenerationCall() {
		c.Set("image_generation_call", true)
		c.Set("image_generation_call_quality", responsesResponse.GetQuality())
		c.Set("image_generation_call_size", responsesResponse.GetSize())
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
	if info == nil || info.ResponsesUsageInfo == nil || info.ResponsesUsageInfo.BuiltInTools == nil {
		return &usage, nil
	}
	// 解析 Tools 用量
	for _, tool := range responsesResponse.Tools {
		buildToolinfo, ok := info.ResponsesUsageInfo.BuiltInTools[common.Interface2String(tool["type"])]
		if !ok || buildToolinfo == nil {
			logger.LogError(c, fmt.Sprintf("BuiltInTools not found for tool type: %v", tool["type"]))
			continue
		}
		buildToolinfo.CallCount++
	}
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
	if isRemoteCompactionV2 {
		bufferLimit = compactResponseBodyLimit
		resp.Body = &remoteCompactionStreamReader{
			ReadCloser: resp.Body,
			remaining:  compactResponseStreamReadLimit,
		}
	}

	originalWriter := c.Writer
	preludeWriter := newResponsesPreludeWriter(originalWriter, bufferLimit)
	c.Writer = preludeWriter
	defer func() {
		c.Writer = originalWriter
	}()

	var usage = &dto.Usage{}
	var responseTextBuilder strings.Builder
	var semanticError *types.NewAPIError
	hasMeaningfulOutput := false
	compactionOutputCount := 0
	outputItemCount := 0

	termination := helper.StreamScannerHandler(c, resp, info, func(data string, sr *helper.StreamResult) {
		var streamResponse dto.ResponsesStreamResponse
		if err := common.UnmarshalJsonStr(data, &streamResponse); err != nil {
			logger.LogError(c, "failed to unmarshal stream response: "+err.Error())
			if isRemoteCompactionV2 {
				semanticError = responsesCompactionError("upstream Responses compaction stream returned invalid event JSON")
				sr.Stop(semanticError)
				return
			}
			sr.Error(err)
			return
		}
		if streamResponse.Response != nil {
			info.SetActualResponseModel(streamResponse.Response.Model, relaycommon.ActualResponseModelSourceOpenAIResponses)
		}

		eventIsMeaningful := responsesStreamEventHasMeaningfulOutput(&streamResponse)
		if isRemoteCompactionV2 {
			eventIsMeaningful = false
			switch streamResponse.Type {
			case dto.ResponsesOutputTypeItemDone:
				outputItemCount++
				if streamResponse.Item == nil {
					semanticError = responsesCompactionError("upstream Responses compaction stream returned an output item without an item payload")
					sr.Stop(semanticError)
					return
				}
				if streamResponse.Item.Type == "compaction" || streamResponse.Item.Type == "compaction_summary" {
					if streamResponse.Item.EncryptedContent == nil {
						semanticError = responsesCompactionError("upstream Responses compaction output is missing encrypted_content")
						sr.Stop(semanticError)
						return
					}
					compactionOutputCount++
					if compactionOutputCount > 1 {
						semanticError = responsesCompactionError(fmt.Sprintf(
							"upstream Responses compaction expected exactly one compaction output item, got %d from %d output items",
							compactionOutputCount,
							outputItemCount,
						))
						sr.Stop(semanticError)
						return
					}
				}
			case "response.completed":
				var completedEnvelope struct {
					Response *struct {
						ID *string `json:"id"`
					} `json:"response"`
				}
				if err := common.UnmarshalJsonStr(data, &completedEnvelope); err != nil ||
					completedEnvelope.Response == nil || completedEnvelope.Response.ID == nil {
					semanticError = responsesCompactionError("upstream Responses compaction completion is missing response.id")
					sr.Stop(semanticError)
					return
				}
				if compactionOutputCount != 1 {
					semanticError = responsesCompactionError(fmt.Sprintf(
						"upstream Responses compaction expected exactly one compaction output item, got %d from %d output items",
						compactionOutputCount,
						outputItemCount,
					))
					sr.Stop(semanticError)
					return
				}
				eventIsMeaningful = true
			case "response.incomplete":
				semanticError = responsesCompactionError("upstream Responses compaction stream ended incomplete")
				sr.Stop(semanticError)
				return
			}
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
					sr.Stop(err)
					return
				}
			}
			sr.Stop(semanticError)
			return
		case "response.completed":
			if !hasMeaningfulOutput && !eventIsMeaningful {
				semanticError = responsesEmptyError("upstream Responses stream completed without meaningful output")
				sr.Stop(semanticError)
				return
			}
		case "response.incomplete":
			if !hasMeaningfulOutput && !eventIsMeaningful {
				semanticError = responsesEmptyError("upstream Responses stream was incomplete without meaningful output")
				sr.Stop(semanticError)
				return
			}
		}

		if !hasMeaningfulOutput && !eventIsMeaningful {
			if err := helper.ResponseChunkData(c, streamResponse, data); err != nil {
				sr.Stop(err)
				return
			}
			if preludeWriter.Error() != nil {
				semanticError = responsesBodyLimitError()
				sr.Stop(semanticError)
				return
			}
			return
		}

		eventBuffered := false
		if !hasMeaningfulOutput {
			if isRemoteCompactionV2 {
				if err := helper.ResponseChunkData(c, streamResponse, data); err != nil {
					sr.Stop(err)
					return
				}
				eventBuffered = true
				if preludeWriter.Error() != nil {
					semanticError = responsesBodyLimitError()
					sr.Stop(semanticError)
					return
				}
			}
			if err := preludeWriter.Commit(); err != nil {
				sr.Stop(err)
				return
			}
			hasMeaningfulOutput = true
		}

		if !eventBuffered {
			if err := helper.ResponseChunkData(c, streamResponse, data); err != nil {
				sr.Stop(err)
				return
			}
		}

		switch streamResponse.Type {
		case "response.completed":
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
				if streamResponse.Response.HasImageGenerationCall() {
					c.Set("image_generation_call", true)
					c.Set("image_generation_call_quality", streamResponse.Response.GetQuality())
					c.Set("image_generation_call_size", streamResponse.Response.GetSize())
				}
			}
			sr.Done()
		case "response.incomplete":
			semanticError = responsesIncompleteError(streamResponse.Response)
			sr.Stop(semanticError)
		case "response.output_text.delta":
			responseTextBuilder.WriteString(streamResponse.Delta)
		case dto.ResponsesOutputTypeItemDone:
			if streamResponse.Item != nil {
				switch streamResponse.Item.Type {
				case dto.BuildInCallWebSearchCall:
					if info != nil && info.ResponsesUsageInfo != nil && info.ResponsesUsageInfo.BuiltInTools != nil {
						if webSearchTool, exists := info.ResponsesUsageInfo.BuiltInTools[dto.BuildInToolWebSearchPreview]; exists && webSearchTool != nil {
							webSearchTool.CallCount++
						}
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

	if c != nil && c.Request != nil {
		if requestErr := c.Request.Context().Err(); requestErr != nil {
			info.StreamStatus = relaycommon.NewStreamStatus()
			info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonClientGone, requestErr)
			return usage, nil
		}
	}
	if termination.IsCancelled() {
		return usage, nil
	}
	if errors.Is(termination.EndError, errRemoteCompactionStreamTooLarge) {
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
