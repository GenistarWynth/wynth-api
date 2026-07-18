package openai

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

const compactResponseBodyLimit = 8 << 20

type compactSSEEvent struct {
	Type     string          `json:"type"`
	Item     json.RawMessage `json:"item"`
	Response json.RawMessage `json:"response"`
}

func OaiResponsesCompactionHandler(c *gin.Context, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	defer service.CloseResponseBodyGracefully(resp)

	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, compactResponseBodyLimit+1))
	if err != nil {
		return nil, types.NewOpenAIError(errors.New("failed to read compact upstream response"), types.ErrorCodeReadResponseBodyFailed, http.StatusInternalServerError)
	}
	if len(responseBody) > compactResponseBodyLimit {
		return nil, types.NewOpenAIError(errors.New("compact upstream response exceeds size limit"), types.ErrorCodeBadResponseBody, http.StatusBadGateway)
	}

	mediaType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if mediaType == "text/event-stream" || bytes.HasPrefix(bytes.TrimSpace(responseBody), []byte("data:")) {
		responseBody, err = normalizeCompactSSE(responseBody)
		if err != nil {
			return nil, types.NewOpenAIError(fmt.Errorf("invalid compact upstream event stream: %w", err), types.ErrorCodeBadResponseBody, http.StatusBadGateway)
		}
	}

	var compactResp dto.OpenAIResponsesCompactionResponse
	if err := common.Unmarshal(responseBody, &compactResp); err != nil {
		return nil, types.NewOpenAIError(errors.New("invalid compact upstream response"), types.ErrorCodeBadResponseBody, http.StatusBadGateway)
	}
	if oaiError := compactResp.GetOpenAIError(); oaiError != nil && oaiError.Type != "" {
		return nil, types.NewOpenAIError(errors.New("compact upstream request failed"), types.ErrorCodeBadResponseBody, http.StatusBadGateway)
	}

	service.IOCopyBytesGracefully(c, resp, responseBody)

	usage := dto.Usage{}
	if compactResp.Usage != nil {
		usage.PromptTokens = compactResp.Usage.InputTokens
		usage.CompletionTokens = compactResp.Usage.OutputTokens
		usage.TotalTokens = compactResp.Usage.TotalTokens
		if compactResp.Usage.InputTokensDetails != nil {
			usage.PromptTokensDetails.CachedTokens = compactResp.Usage.InputTokensDetails.CachedTokens
			usage.PromptTokensDetails.CachedCreationTokens = compactResp.Usage.InputTokensDetails.CachedCreationTokens
			usage.PromptTokensDetails.CacheWriteTokens = compactResp.Usage.InputTokensDetails.CacheWriteTokens
		}
	}
	return &usage, nil
}

func normalizeCompactSSE(body []byte) ([]byte, error) {
	var terminal json.RawMessage
	items := make([]json.RawMessage, 0)
	itemIndexes := make(map[string]int)
	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 64*1024), compactResponseBodyLimit)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") || !strings.HasPrefix(line, "data:") {
			continue
		}
		data := bytes.TrimSpace([]byte(strings.TrimPrefix(line, "data:")))
		if bytes.Equal(data, []byte("[DONE]")) {
			continue
		}
		var event compactSSEEvent
		if err := common.Unmarshal(data, &event); err != nil {
			return nil, errors.New("malformed event data")
		}
		switch event.Type {
		case "response.output_item.done", "response.output_item.added":
			if len(event.Item) == 0 {
				continue
			}
			key := compactItemIdentity(event.Item)
			if index, ok := itemIndexes[key]; ok {
				if event.Type == "response.output_item.done" {
					items[index] = append(json.RawMessage(nil), event.Item...)
				}
				continue
			}
			itemIndexes[key] = len(items)
			items = append(items, append(json.RawMessage(nil), event.Item...))
		case "response.completed":
			if len(terminal) != 0 || len(event.Response) == 0 {
				return nil, errors.New("contradictory terminal response")
			}
			terminal = append(json.RawMessage(nil), event.Response...)
		case "response.failed", "response.incomplete", "error":
			return nil, errors.New("upstream reported terminal failure")
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, errors.New("event stream framing exceeds limit")
	}
	if len(terminal) == 0 {
		return nil, errors.New("missing successful terminal response")
	}

	var response map[string]json.RawMessage
	if err := common.Unmarshal(terminal, &response); err != nil {
		return nil, errors.New("malformed terminal response")
	}
	var terminalItems []json.RawMessage
	if raw := response["output"]; len(raw) != 0 && string(raw) != "null" {
		if err := common.Unmarshal(raw, &terminalItems); err != nil {
			return nil, errors.New("invalid terminal output")
		}
	}
	merged := make([]json.RawMessage, 0, len(terminalItems)+len(items))
	mergedSeen := make(map[string]struct{})
	for _, item := range append(terminalItems, items...) {
		key := compactItemIdentity(item)
		if _, ok := mergedSeen[key]; ok {
			continue
		}
		mergedSeen[key] = struct{}{}
		merged = append(merged, item)
	}
	output, err := common.Marshal(merged)
	if err != nil {
		return nil, errors.New("failed to normalize compact output")
	}
	response["output"] = output
	normalized, err := common.Marshal(response)
	if err != nil {
		return nil, errors.New("failed to normalize compact response")
	}
	return normalized, nil
}

func compactItemIdentity(item json.RawMessage) string {
	var fields map[string]json.RawMessage
	if common.Unmarshal(item, &fields) == nil {
		if id := fields["id"]; len(id) != 0 && string(id) != `""` && string(id) != "null" {
			return "id:" + string(id)
		}
	}
	return "raw:" + string(item)
}
