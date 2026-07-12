package relayconvert

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
)

const (
	responsesInputTypeFunctionCall       = "function_call"
	responsesInputTypeFunctionCallOutput = "function_call_output"
	responsesInputTypeCustomToolCall     = "custom_tool_call"
)

type ResponsesNamespacedTool struct {
	Namespace string
	Name      string
}

type ResponsesChatConversion struct {
	Request            *dto.GeneralOpenAIRequest
	ReverseToolNames   map[string]ResponsesNamespacedTool
	ToolSearchDeclared bool
}

func ResponsesRequestToChatCompletionsRequest(req *dto.OpenAIResponsesRequest) (*dto.GeneralOpenAIRequest, error) {
	conversion, err := ConvertResponsesRequestToChat(req)
	if err != nil {
		return nil, err
	}
	return conversion.Request, nil
}

func ConvertResponsesRequestToChat(req *dto.OpenAIResponsesRequest) (*ResponsesChatConversion, error) {
	if req == nil {
		return nil, errors.New("request is nil")
	}
	if req.Model == "" {
		return nil, errors.New("model is required")
	}
	if err := validateResponsesRequestChatUnsupportedFields(req); err != nil {
		return nil, err
	}

	messages, err := responsesRequestMessagesToChat(req)
	if err != nil {
		return nil, err
	}

	tools, reverseToolNames, toolSearchDeclared, err := responsesRequestToolsToChat(req.Tools)
	if err != nil {
		return nil, err
	}

	toolChoice, err := responsesRequestToolChoiceToChat(req.ToolChoice, tools)
	if err != nil {
		return nil, err
	}

	responseFormat, err := responsesRequestTextToChatResponseFormat(req.Text)
	if err != nil {
		return nil, err
	}

	out := &dto.GeneralOpenAIRequest{
		Model:                req.Model,
		Messages:             messages,
		Stream:               req.Stream,
		StreamOptions:        req.StreamOptions,
		MaxCompletionTokens:  req.MaxOutputTokens,
		Temperature:          req.Temperature,
		TopP:                 req.TopP,
		TopLogProbs:          req.TopLogProbs,
		ResponseFormat:       responseFormat,
		Tools:                tools,
		ToolChoice:           toolChoice,
		User:                 req.User,
		Store:                req.Store,
		Metadata:             req.Metadata,
		SafetyIdentifier:     req.SafetyIdentifier,
		PromptCacheRetention: req.PromptCacheRetention,
		EnableThinking:       req.EnableThinking,
	}

	if req.Reasoning != nil {
		out.ReasoningEffort = req.Reasoning.Effort
	}
	if req.ServiceTier != "" {
		out.ServiceTier, _ = common.Marshal(req.ServiceTier)
	}
	if len(req.ParallelToolCalls) > 0 && common.GetJsonType(req.ParallelToolCalls) == "boolean" {
		var parallelToolCalls bool
		if err := common.Unmarshal(req.ParallelToolCalls, &parallelToolCalls); err == nil {
			out.ParallelTooCalls = &parallelToolCalls
		}
	}
	if len(req.PromptCacheKey) > 0 && common.GetJsonType(req.PromptCacheKey) == "string" {
		var promptCacheKey string
		if err := common.Unmarshal(req.PromptCacheKey, &promptCacheKey); err == nil {
			out.PromptCacheKey = promptCacheKey
		}
	}

	return &ResponsesChatConversion{
		Request:            out,
		ReverseToolNames:   reverseToolNames,
		ToolSearchDeclared: toolSearchDeclared,
	}, nil
}

func validateResponsesRequestChatUnsupportedFields(req *dto.OpenAIResponsesRequest) error {
	unsupported := make([]string, 0, 4)
	if rawJSONPresent(req.Conversation) {
		unsupported = append(unsupported, "conversation")
	}
	if strings.TrimSpace(req.PreviousResponseID) != "" {
		unsupported = append(unsupported, "previous_response_id")
	}
	if rawJSONPresent(req.Prompt) {
		unsupported = append(unsupported, "prompt")
	}
	if rawJSONPresent(req.ContextManagement) {
		unsupported = append(unsupported, "context_management")
	}
	if len(unsupported) > 0 {
		return fmt.Errorf("responses to chat conversion does not support stateful fields: %s", strings.Join(unsupported, ", "))
	}
	return nil
}

func responsesRequestMessagesToChat(req *dto.OpenAIResponsesRequest) ([]dto.Message, error) {
	messages := make([]dto.Message, 0)
	if rawJSONPresent(req.Instructions) {
		instructions, err := responsesJSONString(req.Instructions)
		if err != nil {
			return nil, fmt.Errorf("invalid instructions: %w", err)
		}
		if strings.TrimSpace(instructions) != "" {
			messages = append(messages, dto.Message{Role: "system", Content: instructions})
		}
	}

	if !rawJSONPresent(req.Input) {
		return messages, nil
	}

	switch common.GetJsonType(req.Input) {
	case "string":
		input, err := responsesJSONString(req.Input)
		if err != nil {
			return nil, fmt.Errorf("invalid input string: %w", err)
		}
		messages = append(messages, dto.Message{Role: "user", Content: input})
		return messages, nil
	case "array":
		var items []map[string]any
		if err := common.Unmarshal(req.Input, &items); err != nil {
			return nil, fmt.Errorf("invalid input array: %w", err)
		}
		for _, item := range items {
			nextMessages, err := responsesInputItemToChatMessages(item, messages)
			if err != nil {
				return nil, err
			}
			messages = nextMessages
		}
		return messages, nil
	default:
		return nil, fmt.Errorf("unsupported responses input type %q", common.GetJsonType(req.Input))
	}
}

func responsesInputItemToChatMessages(item map[string]any, messages []dto.Message) ([]dto.Message, error) {
	itemType := strings.TrimSpace(common.Interface2String(item["type"]))
	switch itemType {
	case responsesInputTypeFunctionCall:
		toolCall, err := responsesFunctionCallItemToChatToolCall(item)
		if err != nil {
			return nil, err
		}
		return appendToolCallToLastAssistant(messages, toolCall), nil
	case responsesInputTypeCustomToolCall:
		toolCall, err := responsesCustomToolCallItemToChatToolCall(item)
		if err != nil {
			return nil, err
		}
		return appendToolCallToLastAssistant(messages, toolCall), nil
	case responsesInputTypeFunctionCallOutput:
		callID := strings.TrimSpace(common.Interface2String(item["call_id"]))
		content := responseToolOutputToChatContent(item["output"])
		return append(messages, dto.Message{Role: "tool", ToolCallId: callID, Content: content}), nil
	}

	role := strings.TrimSpace(common.Interface2String(item["role"]))
	if role == "" {
		role = "user"
	}
	content, err := responsesInputContentToChatContent(item["content"])
	if err != nil {
		return nil, err
	}
	return append(messages, dto.Message{Role: role, Content: content}), nil
}

func responsesInputContentToChatContent(content any) (any, error) {
	if content == nil {
		return "", nil
	}

	switch value := content.(type) {
	case string:
		return value, nil
	case []any:
		return responsesContentPartsToChatContent(value)
	case []map[string]any:
		parts := make([]any, 0, len(value))
		for _, part := range value {
			parts = append(parts, part)
		}
		return responsesContentPartsToChatContent(parts)
	default:
		return content, nil
	}
}

func responsesContentPartsToChatContent(parts []any) (any, error) {
	chatParts := make([]any, 0, len(parts))
	var textOnly strings.Builder
	onlyText := true

	for _, rawPart := range parts {
		part, ok := rawPart.(map[string]any)
		if !ok {
			onlyText = false
			chatParts = append(chatParts, rawPart)
			continue
		}

		partType := strings.TrimSpace(common.Interface2String(part["type"]))
		switch partType {
		case "input_text", "output_text", "text":
			text := common.Interface2String(part["text"])
			textOnly.WriteString(text)
			chatParts = append(chatParts, map[string]any{
				"type": dto.ContentTypeText,
				"text": text,
			})
		case "input_image":
			onlyText = false
			chatParts = append(chatParts, map[string]any{
				"type":      dto.ContentTypeImageURL,
				"image_url": responsesImagePartToChatImageURL(part),
			})
		case "input_file":
			onlyText = false
			chatParts = append(chatParts, map[string]any{
				"type": dto.ContentTypeFile,
				"file": responsesFilePartToChatFile(part),
			})
		case "input_audio":
			onlyText = false
			chatParts = append(chatParts, map[string]any{
				"type":        dto.ContentTypeInputAudio,
				"input_audio": responsesPartPayload(part, "input_audio"),
			})
		case "input_video":
			onlyText = false
			chatParts = append(chatParts, map[string]any{
				"type":      dto.ContentTypeVideoUrl,
				"video_url": responsesVideoPartToChatVideoURL(part),
			})
		default:
			onlyText = false
			chatParts = append(chatParts, part)
		}
	}

	if onlyText {
		return textOnly.String(), nil
	}
	return chatParts, nil
}

func responsesFunctionCallItemToChatToolCall(item map[string]any) (dto.ToolCallRequest, error) {
	name := strings.TrimSpace(common.Interface2String(item["name"]))
	if name == "" {
		return dto.ToolCallRequest{}, errors.New("function_call item is missing name")
	}
	if namespace := strings.TrimSpace(common.Interface2String(item["namespace"])); namespace != "" {
		name = flattenResponsesToolName(namespace, name)
	}
	return dto.ToolCallRequest{
		ID:   responsesCallID(item),
		Type: "function",
		Function: dto.FunctionRequest{
			Name:      name,
			Arguments: responsesArgumentsString(item["arguments"]),
		},
	}, nil
}

func responsesCustomToolCallItemToChatToolCall(item map[string]any) (dto.ToolCallRequest, error) {
	raw, err := common.Marshal(item)
	if err != nil {
		return dto.ToolCallRequest{}, err
	}
	return dto.ToolCallRequest{
		ID:     responsesCallID(item),
		Type:   dto.CustomType,
		Custom: raw,
		Function: dto.FunctionRequest{
			Name:      strings.TrimSpace(common.Interface2String(item["name"])),
			Arguments: responsesArgumentsString(item["input"]),
		},
	}, nil
}

func appendToolCallToLastAssistant(messages []dto.Message, toolCall dto.ToolCallRequest) []dto.Message {
	if len(messages) == 0 || messages[len(messages)-1].Role != "assistant" {
		messages = append(messages, dto.Message{Role: "assistant"})
	}

	idx := len(messages) - 1
	toolCalls := messages[idx].ParseToolCalls()
	toolCalls = append(toolCalls, toolCall)
	toolCallsRaw, _ := common.Marshal(toolCalls)
	messages[idx].ToolCalls = toolCallsRaw
	return messages
}

func responsesRequestToolsToChat(raw json.RawMessage) ([]dto.ToolCallRequest, map[string]ResponsesNamespacedTool, bool, error) {
	if !rawJSONPresent(raw) {
		return nil, nil, false, nil
	}

	var tools []map[string]any
	if err := common.Unmarshal(raw, &tools); err != nil {
		return nil, nil, false, fmt.Errorf("invalid tools: %w", err)
	}

	topLevel := make(map[string]struct{})
	for _, tool := range tools {
		toolType := strings.TrimSpace(common.Interface2String(tool["type"]))
		if toolType == "function" || toolType == dto.CustomType {
			if name := strings.TrimSpace(common.Interface2String(tool["name"])); name != "" {
				topLevel[name] = struct{}{}
			}
		}
	}

	out := make([]dto.ToolCallRequest, 0, len(tools))
	reverse := make(map[string]ResponsesNamespacedTool)
	toolSearchDeclared := false
	for _, tool := range tools {
		toolType := strings.TrimSpace(common.Interface2String(tool["type"]))
		switch toolType {
		case "function":
			out = append(out, responsesFunctionToolToChat(tool, strings.TrimSpace(common.Interface2String(tool["name"]))))
		case "namespace":
			namespace := strings.TrimSpace(common.Interface2String(tool["name"]))
			children, _ := tool["tools"].([]any)
			if len(children) == 0 {
				children, _ = tool["children"].([]any)
			}
			for _, rawChild := range children {
				child, ok := rawChild.(map[string]any)
				if !ok || strings.TrimSpace(common.Interface2String(child["type"])) != "function" {
					continue
				}
				name := strings.TrimSpace(common.Interface2String(child["name"]))
				if namespace == "" || name == "" {
					continue
				}
				flat := flattenResponsesToolName(namespace, name)
				if _, exists := topLevel[flat]; exists {
					return nil, nil, false, fmt.Errorf("namespace tool %q/%q conflicts with emitted tool %q", namespace, name, flat)
				}
				owner := ResponsesNamespacedTool{Namespace: namespace, Name: name}
				if previous, exists := reverse[flat]; exists {
					if previous == owner {
						continue
					}
					return nil, nil, false, fmt.Errorf("namespace tools %q/%q and %q/%q both emit %q", previous.Namespace, previous.Name, namespace, name, flat)
				}
				reverse[flat] = owner
				out = append(out, responsesFunctionToolToChat(child, flat))
			}
		case "tool_search":
			if _, exists := topLevel["tool_search"]; exists {
				return nil, nil, false, errors.New("tool_search conflicts with a declared tool of the same name")
			}
			if toolSearchDeclared {
				continue
			}
			toolSearchDeclared = true
			out = append(out, dto.ToolCallRequest{Type: "function", Function: dto.FunctionRequest{
				Name: "tool_search", Description: "Search and load tools for the current task.", Parameters: map[string]any{
					"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string"}}, "required": []string{"query"},
				},
			}})
		}
	}
	return out, reverse, toolSearchDeclared, nil
}

func responsesFunctionToolToChat(tool map[string]any, name string) dto.ToolCallRequest {
	return dto.ToolCallRequest{Type: "function", Function: dto.FunctionRequest{
		Name: name, Description: common.Interface2String(tool["description"]), Parameters: tool["parameters"],
	}}
}

func flattenResponsesToolName(namespace, name string) string {
	full := namespace + "__" + name
	if len(full) <= 64 {
		return full
	}
	sum := sha256.Sum256([]byte(full))
	suffix := "__" + hex.EncodeToString(sum[:4])
	maxPrefixBytes := 64 - len(suffix)
	prefix := full[:maxPrefixBytes]
	for !utf8.ValidString(prefix) {
		prefix = prefix[:len(prefix)-1]
	}
	return prefix + suffix
}

func responsesRequestToolChoiceToChat(raw json.RawMessage, tools []dto.ToolCallRequest) (any, error) {
	if !rawJSONPresent(raw) {
		return nil, nil
	}
	if common.GetJsonType(raw) == "string" {
		var choice string
		if err := common.Unmarshal(raw, &choice); err != nil {
			return nil, fmt.Errorf("invalid tool_choice: %w", err)
		}
		return choice, nil
	}

	var choice map[string]any
	if err := common.Unmarshal(raw, &choice); err != nil {
		return nil, fmt.Errorf("invalid tool_choice: %w", err)
	}
	choiceType := common.Interface2String(choice["type"])
	if choiceType == "tool_search" {
		choice["type"] = "function"
		choice["name"] = "tool_search"
		choiceType = "function"
	}
	if choiceType == "function" {
		name := strings.TrimSpace(common.Interface2String(choice["name"]))
		if name == "" {
			if function, ok := choice["function"].(map[string]any); ok {
				name = strings.TrimSpace(common.Interface2String(function["name"]))
			}
		}
		if name != "" {
			emitted := false
			for _, tool := range tools {
				if tool.Type == "function" && tool.Function.Name == name {
					emitted = true
					break
				}
			}
			if !emitted {
				return nil, nil
			}
			return map[string]any{
				"type": "function",
				"function": map[string]any{
					"name": name,
				},
			}, nil
		}
	}
	return choice, nil
}

func responsesRequestTextToChatResponseFormat(raw json.RawMessage) (*dto.ResponseFormat, error) {
	if !rawJSONPresent(raw) {
		return nil, nil
	}

	var textConfig map[string]any
	if err := common.Unmarshal(raw, &textConfig); err != nil {
		return nil, fmt.Errorf("invalid text config: %w", err)
	}
	format, ok := textConfig["format"].(map[string]any)
	if !ok {
		return nil, nil
	}

	formatType := strings.TrimSpace(common.Interface2String(format["type"]))
	if formatType == "" {
		return nil, nil
	}

	out := &dto.ResponseFormat{Type: formatType}
	if formatType == "json_schema" {
		schemaRaw, err := common.Marshal(format)
		if err != nil {
			return nil, err
		}
		out.JsonSchema = schemaRaw
	}
	return out, nil
}

func responsesImagePartToChatImageURL(part map[string]any) any {
	if imageURL, ok := part["image_url"]; ok {
		return imageURL
	}
	imageURL := map[string]any{}
	for _, key := range []string{"url", "file_id", "detail"} {
		if value, ok := part[key]; ok {
			imageURL[key] = value
		}
	}
	if len(imageURL) == 0 {
		return part
	}
	return imageURL
}

func responsesFilePartToChatFile(part map[string]any) any {
	if file, ok := part["file"]; ok {
		return file
	}
	file := map[string]any{}
	for _, key := range []string{"file_id", "file_data", "filename", "file_url"} {
		if value, ok := part[key]; ok {
			file[key] = value
		}
	}
	if len(file) == 0 {
		return part
	}
	return file
}

func responsesVideoPartToChatVideoURL(part map[string]any) any {
	if videoURL, ok := part["video_url"]; ok {
		if videoURLMap, ok := videoURL.(map[string]any); ok {
			if url := common.Interface2String(videoURLMap["url"]); url != "" {
				return url
			}
		}
		return videoURL
	}
	if url := common.Interface2String(part["url"]); url != "" {
		return url
	}
	return responsesPartPayload(part, "video_url")
}

func responsesPartPayload(part map[string]any, key string) any {
	if value, ok := part[key]; ok {
		return value
	}
	payload := make(map[string]any, len(part))
	for k, value := range part {
		if k == "type" {
			continue
		}
		payload[k] = value
	}
	return payload
}

func responsesCallID(item map[string]any) string {
	callID := strings.TrimSpace(common.Interface2String(item["call_id"]))
	if callID != "" {
		return callID
	}
	return strings.TrimSpace(common.Interface2String(item["id"]))
}

func responsesArgumentsString(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	default:
		raw, err := common.Marshal(v)
		if err != nil {
			return common.Interface2String(v)
		}
		return string(raw)
	}
}

func responseToolOutputToChatContent(value any) any {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	default:
		raw, err := common.Marshal(v)
		if err != nil {
			return fmt.Sprintf("%v", v)
		}
		return string(raw)
	}
}

func responsesJSONString(raw json.RawMessage) (string, error) {
	if common.GetJsonType(raw) != "string" {
		return string(raw), nil
	}
	var value string
	if err := common.Unmarshal(raw, &value); err != nil {
		return "", err
	}
	return value, nil
}

func rawJSONPresent(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	return common.GetJsonType(raw) != "null"
}
