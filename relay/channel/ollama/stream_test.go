package ollama

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestOllamaChatHandlerReturnsToolCalls(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name string
		raw  string
	}{
		{
			name: "compact JSON",
			raw:  `{"model":"llama3.1","created_at":"2026-05-27T12:00:00Z","message":{"role":"assistant","content":"","tool_calls":[{"function":{"name":"get_weather","arguments":{"city":"Paris","days":0}}}]},"done":true,"done_reason":"stop","prompt_eval_count":5,"eval_count":7}`,
		},
		{
			name: "pretty JSON",
			raw: `{
  "model": "llama3.1",
  "created_at": "2026-05-27T12:00:00Z",
  "message": {
    "role": "assistant",
    "content": "",
    "tool_calls": [
      {
        "function": {
          "name": "get_weather",
          "arguments": {
            "city": "Paris",
            "days": 0
          }
        }
      }
    ]
  },
  "done": true,
  "done_reason": "stop",
  "prompt_eval_count": 5,
  "eval_count": 7
}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			resp := &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(tt.raw)),
			}

			usage, apiErr := ollamaChatHandler(c, &relaycommon.RelayInfo{
				ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "fallback-model"},
			}, resp)
			require.Nil(t, apiErr)
			require.NotNil(t, usage)
			assert.Equal(t, 12, usage.TotalTokens)

			var out dto.OpenAITextResponse
			require.NoError(t, common.Unmarshal(w.Body.Bytes(), &out))
			require.Len(t, out.Choices, 1)
			assert.Equal(t, constant.FinishReasonToolCalls, out.Choices[0].FinishReason)

			var toolCalls []dto.ToolCallResponse
			require.NoError(t, common.Unmarshal(out.Choices[0].Message.ToolCalls, &toolCalls))
			require.Len(t, toolCalls, 1)
			assert.Equal(t, "call_0", toolCalls[0].ID)
			assert.Equal(t, "function", toolCalls[0].Type)
			assert.Equal(t, "get_weather", toolCalls[0].Function.Name)
			assert.Nil(t, toolCalls[0].Index)
			assert.JSONEq(t, `{"city":"Paris","days":0}`, toolCalls[0].Function.Arguments)
		})
	}
}

func TestOllamaChatHandlerPrettyJSONWithStandaloneNull(t *testing.T) {
	gin.SetMode(gin.TestMode)
	raw := `{
  "model": "llama3.1",
  "created_at": "2026-05-27T12:00:00Z",
  "message": {
    "role": "assistant",
    "content": "using tools",
    "tool_calls": [
      {
        "function": {
          "name": "process_items",
          "arguments": {
            "items": [
              {
                "value": 0
              },
              null
            ]
          }
        }
      }
    ]
  },
  "done": true,
  "done_reason": "stop",
  "prompt_eval_count": 5,
  "eval_count": 7
}`
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	usage, apiErr := ollamaChatHandler(c, ollamaTestRelayInfo(), newOllamaTestResponse(raw))

	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	assert.Equal(t, &dto.Usage{PromptTokens: 5, CompletionTokens: 7, TotalTokens: 12}, usage)

	var out dto.OpenAITextResponse
	require.NoError(t, common.Unmarshal(w.Body.Bytes(), &out))
	require.Len(t, out.Choices, 1)
	assert.Equal(t, "llama3.1", out.Model)
	assert.Equal(t, "using tools", out.Choices[0].Message.Content)
	assert.Equal(t, constant.FinishReasonToolCalls, out.Choices[0].FinishReason)

	var toolCalls []dto.ToolCallResponse
	require.NoError(t, common.Unmarshal(out.Choices[0].Message.ToolCalls, &toolCalls))
	require.Len(t, toolCalls, 1)
	assert.Equal(t, "call_0", toolCalls[0].ID)
	assert.Equal(t, "process_items", toolCalls[0].Function.Name)
	assert.JSONEq(t, `{"items":[{"value":0},null]}`, toolCalls[0].Function.Arguments)
}

func TestOllamaChatHandlerAccumulatesToolCallsAcrossChunks(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	resp := newOllamaTestResponse(strings.Join([]string{
		`{"model":"llama3.1","created_at":"2026-05-27T12:00:00Z","message":{"role":"assistant","content":"working","tool_calls":[{"function":{"name":"calculate","arguments":{"count":0,"enabled":false,"nested":{"items":[0,false,{"value":0}]}}}},{"function":{"name":"lookup","arguments":{"items":[{"id":0}]}}}]},"done":false}`,
		`{"model":"llama3.1","created_at":"2026-05-27T12:00:01Z","message":{"role":"assistant","content":"","tool_calls":[{"function":{"name":"use_defaults","arguments":null}}]},"done":false}`,
		`{"model":"llama3.1","created_at":"2026-05-27T12:00:02Z","done":true,"done_reason":"stop","prompt_eval_count":11,"eval_count":13}`,
	}, "\n"))

	usage, apiErr := ollamaChatHandler(c, ollamaTestRelayInfo(), resp)
	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	assert.Equal(t, &dto.Usage{PromptTokens: 11, CompletionTokens: 13, TotalTokens: 24}, usage)

	var out dto.OpenAITextResponse
	require.NoError(t, common.Unmarshal(w.Body.Bytes(), &out))
	require.Len(t, out.Choices, 1)
	assert.Equal(t, "working", out.Choices[0].Message.Content)
	assert.Equal(t, constant.FinishReasonToolCalls, out.Choices[0].FinishReason)

	var toolCalls []dto.ToolCallResponse
	require.NoError(t, common.Unmarshal(out.Choices[0].Message.ToolCalls, &toolCalls))
	require.Len(t, toolCalls, 3)
	assert.Equal(t, "call_0", toolCalls[0].ID)
	assert.Equal(t, "calculate", toolCalls[0].Function.Name)
	assert.Nil(t, toolCalls[0].Index)
	assert.JSONEq(t, `{"count":0,"enabled":false,"nested":{"items":[0,false,{"value":0}]}}`, toolCalls[0].Function.Arguments)
	assert.Equal(t, "call_1", toolCalls[1].ID)
	assert.Equal(t, "lookup", toolCalls[1].Function.Name)
	assert.Nil(t, toolCalls[1].Index)
	assert.JSONEq(t, `{"items":[{"id":0}]}`, toolCalls[1].Function.Arguments)
	assert.Equal(t, "call_2", toolCalls[2].ID)
	assert.Equal(t, "use_defaults", toolCalls[2].Function.Name)
	assert.Nil(t, toolCalls[2].Index)
	assert.JSONEq(t, `{}`, toolCalls[2].Function.Arguments)

	var rawToolCalls []map[string]any
	require.NoError(t, common.Unmarshal(out.Choices[0].Message.ToolCalls, &rawToolCalls))
	for _, toolCall := range rawToolCalls {
		assert.NotContains(t, toolCall, "index")
	}
}

func TestOllamaChatHandlerPreservesTextReasoningAndFinishReason(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name           string
		raw            string
		finishReason   string
		reasoningValue string
	}{
		{
			name:           "empty reason defaults to stop",
			raw:            `{"model":"llama3.1","message":{"role":"assistant","content":"hello","thinking":"plan"},"done":true,"prompt_eval_count":2,"eval_count":3}`,
			finishReason:   "stop",
			reasoningValue: "plan",
		},
		{
			name:           "provider reason passes through",
			raw:            `{"model":"llama3.1","message":{"role":"assistant","content":"hello","thinking":"plan"},"done":true,"done_reason":"length","prompt_eval_count":2,"eval_count":3}`,
			finishReason:   "length",
			reasoningValue: "plan",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			usage, apiErr := ollamaChatHandler(c, ollamaTestRelayInfo(), newOllamaTestResponse(tt.raw))
			require.Nil(t, apiErr)
			require.NotNil(t, usage)
			assert.Equal(t, 5, usage.TotalTokens)

			var out dto.OpenAITextResponse
			require.NoError(t, common.Unmarshal(w.Body.Bytes(), &out))
			require.Len(t, out.Choices, 1)
			assert.Equal(t, "hello", out.Choices[0].Message.Content)
			assert.Equal(t, tt.finishReason, out.Choices[0].FinishReason)
			require.NotNil(t, out.Choices[0].Message.ReasoningContent)
			assert.Equal(t, tt.reasoningValue, *out.Choices[0].Message.ReasoningContent)
			assert.Empty(t, out.Choices[0].Message.ToolCalls)
		})
	}
}

func TestOllamaChatHandlerRejectsMalformedSingleResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	usage, apiErr := ollamaChatHandler(c, ollamaTestRelayInfo(), newOllamaTestResponse(`{"model":"llama3.1","message":`))

	assert.Nil(t, usage)
	require.NotNil(t, apiErr)
	assert.Equal(t, types.ErrorCodeBadResponseBody, apiErr.GetErrorCode())
}

func TestOllamaStreamHandlerPlainTextCharacterization(t *testing.T) {
	frames, usage, apiErr := runOllamaStreamHandler(t, strings.Join([]string{
		`{"model":"llama3.1","created_at":"2026-05-27T12:00:00Z","message":{"role":"assistant","content":"hello ","thinking":"plan "},"done":false}`,
		`{"model":"llama3.1","created_at":"2026-05-27T12:00:01Z","message":{"role":"assistant","content":"world","thinking":"carefully"},"done":false}`,
		`{"model":"llama3.1","created_at":"2026-05-27T12:00:02Z","done":true,"done_reason":"length","prompt_eval_count":3,"eval_count":4}`,
	}, "\n"))
	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	assert.Equal(t, &dto.Usage{PromptTokens: 3, CompletionTokens: 4, TotalTokens: 7}, usage)

	var content strings.Builder
	var reasoning strings.Builder
	var finishReason string
	var finalUsage *dto.Usage
	for _, frame := range frames {
		if frame.Usage != nil {
			finalUsage = frame.Usage
		}
		if len(frame.Choices) == 0 {
			continue
		}
		content.WriteString(frame.Choices[0].Delta.GetContentString())
		reasoning.WriteString(frame.Choices[0].Delta.GetReasoningContent())
		if frame.Choices[0].FinishReason != nil {
			finishReason = *frame.Choices[0].FinishReason
		}
	}
	assert.Equal(t, "hello world", content.String())
	assert.Equal(t, "plan carefully", reasoning.String())
	assert.Equal(t, "length", finishReason)
	require.NotNil(t, finalUsage)
	assert.Equal(t, *usage, *finalUsage)
}

func TestOllamaStreamHandlerReturnsIndexedToolCalls(t *testing.T) {
	frames, usage, apiErr := runOllamaStreamHandler(t, strings.Join([]string{
		`{"model":"llama3.1","created_at":"2026-05-27T12:00:00Z","message":{"role":"assistant","content":"","tool_calls":[{"function":{"name":"use_defaults","arguments":null}}]},"done":false}`,
		`{"model":"llama3.1","created_at":"2026-05-27T12:00:01Z","message":{"role":"assistant","content":"","tool_calls":[{"function":{"name":"calculate","arguments":{"count":0,"enabled":false,"nested":[0,false]}}}]},"done":false}`,
		`{"model":"llama3.1","created_at":"2026-05-27T12:00:02Z","done":true,"done_reason":"stop","prompt_eval_count":5,"eval_count":8}`,
	}, "\n"))
	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	assert.Equal(t, &dto.Usage{PromptTokens: 5, CompletionTokens: 8, TotalTokens: 13}, usage)

	var toolCalls []dto.ToolCallResponse
	var finishReason string
	var finalUsage *dto.Usage
	for _, frame := range frames {
		if frame.Usage != nil {
			finalUsage = frame.Usage
		}
		if len(frame.Choices) == 0 {
			continue
		}
		toolCalls = append(toolCalls, frame.Choices[0].Delta.ToolCalls...)
		if frame.Choices[0].FinishReason != nil {
			finishReason = *frame.Choices[0].FinishReason
		}
	}

	require.Len(t, toolCalls, 2)
	assert.Equal(t, "call_0", toolCalls[0].ID)
	assert.Equal(t, "function", toolCalls[0].Type)
	assert.Equal(t, "use_defaults", toolCalls[0].Function.Name)
	require.NotNil(t, toolCalls[0].Index)
	assert.Equal(t, 0, *toolCalls[0].Index)
	assert.JSONEq(t, `{}`, toolCalls[0].Function.Arguments)
	assert.Equal(t, "call_1", toolCalls[1].ID)
	assert.Equal(t, "function", toolCalls[1].Type)
	assert.Equal(t, "calculate", toolCalls[1].Function.Name)
	require.NotNil(t, toolCalls[1].Index)
	assert.Equal(t, 1, *toolCalls[1].Index)
	assert.JSONEq(t, `{"count":0,"enabled":false,"nested":[0,false]}`, toolCalls[1].Function.Arguments)
	assert.Equal(t, constant.FinishReasonToolCalls, finishReason)
	require.NotNil(t, finalUsage)
	assert.Equal(t, *usage, *finalUsage)
}

func TestOllamaStreamHandlerRejectsMalformedLine(t *testing.T) {
	frames, usage, apiErr := runOllamaStreamHandler(t, `{"model":"llama3.1","message":`)

	assert.Empty(t, frames)
	require.NotNil(t, usage)
	require.NotNil(t, apiErr)
	assert.Equal(t, types.ErrorCodeBadResponseBody, apiErr.GetErrorCode())
}

func TestOllamaToolCallsToOpenAIFallsBackForUnencodableArguments(t *testing.T) {
	toolCall := OllamaToolCall{}
	toolCall.Function.Name = "unsupported_arguments"
	toolCall.Function.Arguments = func() {}

	converted, nextIndex := ollamaToolCallsToOpenAI([]OllamaToolCall{toolCall}, 4, false)

	require.Len(t, converted, 1)
	assert.Equal(t, 5, nextIndex)
	assert.Equal(t, "call_4", converted[0].ID)
	assert.JSONEq(t, `{}`, converted[0].Function.Arguments)
	assert.Nil(t, converted[0].Index)
}

func runOllamaStreamHandler(t *testing.T, raw string) ([]dto.ChatCompletionsStreamResponse, *dto.Usage, *types.NewAPIError) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	usage, apiErr := ollamaStreamHandler(c, ollamaTestRelayInfo(), newOllamaTestResponse(raw))
	if apiErr != nil {
		return nil, usage, apiErr
	}

	var frames []dto.ChatCompletionsStreamResponse
	for _, line := range strings.Split(w.Body.String(), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		var frame dto.ChatCompletionsStreamResponse
		require.NoError(t, common.Unmarshal([]byte(data), &frame))
		frames = append(frames, frame)
	}
	return frames, usage, nil
}

func newOllamaTestResponse(raw string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(raw)),
	}
}

func ollamaTestRelayInfo() *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "fallback-model"},
	}
}
