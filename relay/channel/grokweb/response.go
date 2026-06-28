// Best-effort reverse-engineered grok.com web SSE parsing and translation to
// OpenAI chat-completion shapes. See package doc in constants.go for the
// fragility warning.
package grokweb

import (
	"bufio"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

// grokSSEFrame is the decoded JSON of a single grok.com `data:` line.
// Mirror of the shape consumed by grok2api StreamAdapter.feed.
type grokSSEFrame struct {
	Result *struct {
		Response *grokResponse `json:"response"`
	} `json:"result"`
	Error *grokInBandError `json:"error"`
}

type grokResponse struct {
	Token            string              `json:"token"`
	IsThinking       bool                `json:"isThinking"`
	IsSoftStop       bool                `json:"isSoftStop"`
	MessageTag       string              `json:"messageTag"`
	FinalMetadata    map[string]any      `json:"finalMetadata"`
	CardAttachment   *grokCardAttachment `json:"cardAttachment"`
	WebSearchResults *grokWebSearch      `json:"webSearchResults"`
}

// grokWebSearch carries deep-search source results accumulated across frames.
// Mirror of grok2api xai_chat.py webSearchResults handling.
type grokWebSearch struct {
	Results []grokSource `json:"results"`
}

type grokSource struct {
	URL   string `json:"url"`
	Title string `json:"title"`
}

// grokCardAttachment carries out-of-band rendered content (generated images,
// search cards). For image generation, jsonData is a JSON STRING that decodes to
// an object with an image_chunk field. Mirror of grok2api xai_chat.py _handle_card.
type grokCardAttachment struct {
	JsonData string `json:"jsonData"`
}

type grokInBandError struct {
	Message string `json:"message"`
	Code    any    `json:"code"`
}

// parsedFrame is the normalized outcome of decoding one grok SSE frame.
type parsedFrame struct {
	content   string       // final assistant text token (already cleaned of thinking)
	reasoning string       // thinking token
	sources   []grokSource // deep-search web sources present in this frame
	stop      bool         // soft-stop / finalMetadata terminator
	inBandErr *grokInBandError
}

// classifyLine returns (kind, payload) for a raw SSE line.
// kind is one of "data", "done", "skip". Mirror of grok2api classify_line:
// handles both "data: {...}" prefixed lines and raw JSON lines.
func classifyLine(line string) (kind, payload string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return "skip", ""
	}
	if strings.HasPrefix(line, "data:") {
		data := strings.TrimSpace(line[len("data:"):])
		if data == "[DONE]" {
			return "done", ""
		}
		return "data", data
	}
	if strings.HasPrefix(line, "event:") {
		return "skip", ""
	}
	if strings.HasPrefix(line, "{") {
		return "data", line
	}
	return "skip", ""
}

// parseFrame decodes a single grok SSE data payload.
// Mirror of grok2api StreamAdapter.feed (text-only subset): final tokens
// (think != true, tag == "final") become content; thinking tokens become
// reasoning; isSoftStop / finalMetadata terminate.
func parseFrame(payload string) (parsedFrame, bool) {
	var frame grokSSEFrame
	if err := common.UnmarshalJsonStr(payload, &frame); err != nil {
		return parsedFrame{}, false
	}
	if frame.Error != nil {
		return parsedFrame{inBandErr: frame.Error}, true
	}
	if frame.Result == nil || frame.Result.Response == nil {
		return parsedFrame{}, false
	}
	resp := frame.Result.Response

	out := parsedFrame{}
	emitted := false

	if resp.Token != "" {
		if resp.IsThinking {
			out.reasoning = resp.Token
			emitted = true
		} else if resp.MessageTag == "final" || resp.MessageTag == "" {
			// Treat untagged tokens as final content too: some frames omit the
			// tag but still carry user-facing text.
			out.content = resp.Token
			emitted = true
		}
	}

	if resp.WebSearchResults != nil {
		for _, src := range resp.WebSearchResults.Results {
			src.URL = strings.TrimSpace(src.URL)
			if src.URL != "" {
				out.sources = append(out.sources, src)
			}
		}
		if len(out.sources) > 0 {
			emitted = true
		}
	}

	if resp.IsSoftStop || len(resp.FinalMetadata) > 0 {
		out.stop = true
		emitted = true
	}

	return out, emitted
}

// buildSourcesSection formats accumulated deep-search sources as a Markdown
// "## Sources" section. Mirror of grok2api references_suffix. Returns "" for none.
func buildSourcesSection(sources []grokSource) string {
	if len(sources) == 0 {
		return ""
	}
	escaper := strings.NewReplacer("\\", "\\\\", "[", "\\[", "]", "\\]")
	var b strings.Builder
	b.WriteString("\n\n## Sources\n")
	for _, s := range sources {
		title := strings.TrimSpace(s.Title)
		if title == "" {
			title = s.URL
		}
		b.WriteString("- [")
		b.WriteString(escaper.Replace(title))
		b.WriteString("](")
		b.WriteString(s.URL)
		b.WriteString(")\n")
	}
	return b.String()
}

// accumulateSources appends new (deduped by URL) sources from a frame.
func accumulateSources(existing []grokSource, seen map[string]struct{}, frameSources []grokSource) []grokSource {
	for _, s := range frameSources {
		if _, dup := seen[s.URL]; dup {
			continue
		}
		seen[s.URL] = struct{}{}
		existing = append(existing, s)
	}
	return existing
}

// grokWebStreamHandler relays a grok.com SSE response to the client as an
// OpenAI chat.completion.chunk stream.
func grokWebStreamHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (any, *types.NewAPIError) {
	defer service.CloseResponseBodyGracefully(resp)

	id := helper.GetResponseID(c)
	created := common.GetTimestamp()
	model := info.UpstreamModelName

	helper.SetEventStreamHeaders(c)

	var contentBuilder strings.Builder
	reasoningSeen := false
	stopSeen := false
	deepSearch := isDeepSearchModel(model)
	var sources []grokSource
	sourcesSeen := make(map[string]struct{})

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	for scanner.Scan() {
		kind, payload := classifyLine(scanner.Text())
		if kind == "done" {
			break
		}
		if kind != "data" {
			continue
		}
		pf, ok := parseFrame(payload)
		if !ok {
			continue
		}
		if pf.inBandErr != nil {
			return nil, inBandErrorToAPIError(pf.inBandErr)
		}
		// Reasoning ("think"/expert-mode) tokens are surfaced as reasoning_content
		// deltas, separate from the user-facing content.
		if pf.reasoning != "" {
			reasoningSeen = true
			_ = helper.ObjectData(c, newReasoningChunk(id, created, model, pf.reasoning))
		}
		if pf.content != "" {
			contentBuilder.WriteString(pf.content)
			chunk := newContentChunk(id, created, model, pf.content)
			_ = helper.ObjectData(c, chunk)
		}
		if deepSearch && len(pf.sources) > 0 {
			sources = accumulateSources(sources, sourcesSeen, pf.sources)
		}
		if pf.stop {
			stopSeen = true
		}
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		return nil, types.NewError(err, types.ErrorCodeReadResponseBodyFailed)
	}

	// Completion tokens are billed on model-produced content only — the deep-search
	// "## Sources" section appended below is adaptor-injected formatting, not model
	// output, so it is captured for billing BEFORE the section is appended.
	billableContent := contentBuilder.String()

	// Deep-search: emit the accumulated sources as a final content chunk.
	if section := buildSourcesSection(sources); section != "" {
		contentBuilder.WriteString(section)
		_ = helper.ObjectData(c, newContentChunk(id, created, model, section))
	}

	finishReason := "stop"
	if !stopSeen && contentBuilder.Len() == 0 && !reasoningSeen {
		// No content, no reasoning and no terminator: surface as empty upstream response.
		return nil, types.NewError(errors.New("grok-web: empty upstream stream"), types.ErrorCodeEmptyResponse)
	}
	_ = helper.ObjectData(c, helper.GenerateStopResponse(id, created, model, finishReason))

	usage := buildUsage(info, billableContent)
	_ = helper.ObjectData(c, helper.GenerateFinalUsageResponse(id, created, model, *usage))
	helper.Done(c)

	return usage, nil
}

// grokWebHandler relays a grok.com SSE response to the client as a single
// non-streaming OpenAI chat.completion.
func grokWebHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (any, *types.NewAPIError) {
	defer service.CloseResponseBodyGracefully(resp)

	var contentBuilder strings.Builder
	var reasoningBuilder strings.Builder
	stopSeen := false
	deepSearch := isDeepSearchModel(info.UpstreamModelName)
	var sources []grokSource
	sourcesSeen := make(map[string]struct{})

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	for scanner.Scan() {
		kind, payload := classifyLine(scanner.Text())
		if kind == "done" {
			break
		}
		if kind != "data" {
			continue
		}
		pf, ok := parseFrame(payload)
		if !ok {
			continue
		}
		if pf.inBandErr != nil {
			return nil, inBandErrorToAPIError(pf.inBandErr)
		}
		if pf.reasoning != "" {
			reasoningBuilder.WriteString(pf.reasoning)
		}
		if pf.content != "" {
			contentBuilder.WriteString(pf.content)
		}
		if deepSearch && len(pf.sources) > 0 {
			sources = accumulateSources(sources, sourcesSeen, pf.sources)
		}
		if pf.stop {
			stopSeen = true
		}
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		return nil, types.NewError(err, types.ErrorCodeReadResponseBodyFailed)
	}

	// Bill model-produced content only; the appended "## Sources" section is
	// adaptor-injected formatting (see stream handler note).
	billableContent := contentBuilder.String()
	contentBuilder.WriteString(buildSourcesSection(sources))
	content := contentBuilder.String()
	if content == "" && reasoningBuilder.Len() == 0 && !stopSeen {
		return nil, types.NewError(errors.New("grok-web: empty upstream response"), types.ErrorCodeEmptyResponse)
	}

	usage := buildUsage(info, billableContent)
	message := dto.Message{
		Role:    "assistant",
		Content: content,
	}
	if reasoningBuilder.Len() > 0 {
		message.ReasoningContent = common.GetPointer(reasoningBuilder.String())
	}
	full := dto.OpenAITextResponse{
		Id:      helper.GetResponseID(c),
		Model:   info.UpstreamModelName,
		Object:  "chat.completion",
		Created: common.GetTimestamp(),
		Choices: []dto.OpenAITextResponseChoice{
			{
				Index:        0,
				Message:      message,
				FinishReason: "stop",
			},
		},
		Usage: *usage,
	}
	jsonResp, err := common.Marshal(full)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeJsonMarshalFailed)
	}
	c.Writer.Header().Set("Content-Type", "application/json")
	c.Writer.WriteHeader(http.StatusOK)
	_, _ = c.Writer.Write(jsonResp)

	return usage, nil
}

func newContentChunk(id string, created int64, model, content string) *dto.ChatCompletionsStreamResponse {
	return &dto.ChatCompletionsStreamResponse{
		Id:      id,
		Object:  "chat.completion.chunk",
		Created: created,
		Model:   model,
		Choices: []dto.ChatCompletionsStreamResponseChoice{
			{
				Index: 0,
				Delta: dto.ChatCompletionsStreamResponseChoiceDelta{
					Role:    "assistant",
					Content: common.GetPointer(content),
				},
			},
		},
	}
}

// newReasoningChunk emits a grok "think"/expert-mode reasoning token as an OpenAI
// reasoning_content delta (separate from user-facing content).
func newReasoningChunk(id string, created int64, model, reasoning string) *dto.ChatCompletionsStreamResponse {
	return &dto.ChatCompletionsStreamResponse{
		Id:      id,
		Object:  "chat.completion.chunk",
		Created: created,
		Model:   model,
		Choices: []dto.ChatCompletionsStreamResponseChoice{
			{
				Index: 0,
				Delta: dto.ChatCompletionsStreamResponseChoiceDelta{
					Role:             "assistant",
					ReasoningContent: common.GetPointer(reasoning),
				},
			},
		},
	}
}

// buildUsage estimates token usage. grok.com does not return token counts, so
// prompt tokens come from the pre-estimate and completion tokens are counted
// from the accumulated content.
func buildUsage(info *relaycommon.RelayInfo, content string) *dto.Usage {
	promptTokens := info.GetEstimatePromptTokens()
	completionTokens := service.CountTextToken(content, info.UpstreamModelName)
	return &dto.Usage{
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		TotalTokens:      promptTokens + completionTokens,
	}
}
