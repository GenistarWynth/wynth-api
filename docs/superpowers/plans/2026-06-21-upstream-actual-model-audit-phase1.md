# Upstream Actual Model Audit Phase 1 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Record and display the actual model reported by upstream responses without changing billing, routing, priority, weight, or upstream sync behavior.

**Architecture:** Add a small provider-aware model audit helper in `relay/common`, store detected values on `RelayInfo`, let adapters set those values while parsing upstream responses, and let the existing usage-log metadata writer persist them into `Other`. Update usage-log frontend formatting so the model column can show requested, upstream request, and actual response models distinctly.

**Tech Stack:** Go 1.22+ with Gin/GORM and project `common` JSON wrappers; React 19 + TypeScript + Bun in `web/default`; backend tests use `testify`.

---

## Scope

Implement only Phase 1 from `docs/superpowers/specs/2026-06-21-upstream-actual-model-audit-design.md`.

In scope:

- OpenAI Chat non-stream and stream actual model extraction.
- OpenAI Responses non-stream and stream actual model extraction.
- Anthropic/Claude Messages non-stream and stream actual model extraction.
- Gemini non-stream and stream actual model extraction.
- Usage-log `Other` metadata fields:
  - `upstream_model_name`
  - `actual_response_model`
  - `actual_response_model_source`
- Usage-log table/detail display for requested, upstream request, and actual response model.

Out of scope:

- Anthropic thinking signature protobuf decoding.
- New database columns.
- Price-to-priority, availability scoring, cache scoring, or scheduler changes.
- Billing changes.
- Rewriting downstream response `model` fields for audit purposes.

## File Map

- Create `relay/common/actual_response_model.go`: detector types, helpers, and `RelayInfo` setter methods.
- Create `relay/common/actual_response_model_test.go`: deterministic tests for all Phase 1 response protocols and malformed inputs.
- Modify `relay/common/relay_info.go`: add `ActualResponseModel` and `ActualResponseModelSource` fields to `RelayInfo`.
- Modify `relay/channel/openai/relay-openai.go`: set audit data for OpenAI Chat non-stream and stream responses.
- Modify `relay/channel/openai/helper.go`: set audit data from final OpenAI Chat stream chunk.
- Modify `relay/channel/openai/relay_responses.go`: set audit data for OpenAI Responses non-stream and stream responses.
- Modify `relay/channel/claude/relay-claude.go`: set audit data for Claude Messages non-stream and stream responses.
- Modify `relay/channel/gemini/relay-gemini.go`: set audit data for Gemini non-stream and stream responses.
- Modify `service/log_info_generate.go`: write model audit fields into `Other`.
- Create `service/log_info_generate_model_audit_test.go`: verifies metadata is emitted and billing model is unchanged.
- Modify `web/default/src/features/usage-logs/types.ts`: add model audit metadata fields.
- Modify `web/default/src/features/usage-logs/lib/format.ts`: return requested/upstream/actual display state.
- Create `web/default/src/features/usage-logs/lib/model-audit.test.ts`: pure display contract tests.
- Modify `web/default/src/features/usage-logs/components/model-badge.tsx`: display upstream mapping and actual response mismatch.
- Modify `web/default/src/features/usage-logs/components/columns/common-logs-columns.tsx`: pass new model display props.
- Modify `web/default/src/features/usage-logs/components/dialogs/details-dialog.tsx`: show model audit section.
- Modify frontend locale JSON files through `bun run i18n:sync` if new labels are introduced.

## Task 1: Backend Model Audit Helper

**Files:**

- Create: `relay/common/actual_response_model_test.go`
- Create: `relay/common/actual_response_model.go`
- Modify: `relay/common/relay_info.go`

- [ ] **Step 1: Write failing detector tests**

Create `relay/common/actual_response_model_test.go`:

```go
package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDetectActualResponseModel(t *testing.T) {
	tests := []struct {
		name       string
		kind       ActualResponseModelKind
		payload    string
		wantModel  string
		wantSource ActualResponseModelSource
	}{
		{
			name:       "openai chat top-level model",
			kind:       ActualResponseModelKindOpenAIChat,
			payload:    `{"id":"chatcmpl_1","model":"gpt-5.5","choices":[]}`,
			wantModel:  "gpt-5.5",
			wantSource: ActualResponseModelSourceOpenAIChat,
		},
		{
			name:       "openai chat stream chunk",
			kind:       ActualResponseModelKindOpenAIChat,
			payload:    `{"id":"chatcmpl_1","object":"chat.completion.chunk","model":"gpt-5.4","choices":[{"delta":{"content":"x"},"index":0}]}`,
			wantModel:  "gpt-5.4",
			wantSource: ActualResponseModelSourceOpenAIChat,
		},
		{
			name:       "openai responses top-level model",
			kind:       ActualResponseModelKindOpenAIResponses,
			payload:    `{"id":"resp_1","model":"gpt-4.1","output":[]}`,
			wantModel:  "gpt-4.1",
			wantSource: ActualResponseModelSourceOpenAIResponses,
		},
		{
			name:       "openai responses stream envelope model",
			kind:       ActualResponseModelKindOpenAIResponses,
			payload:    `{"type":"response.created","response":{"id":"resp_1","model":"gpt-4.1-2025-04-14"}}`,
			wantModel:  "gpt-4.1-2025-04-14",
			wantSource: ActualResponseModelSourceOpenAIResponses,
		},
		{
			name:       "anthropic non-stream model",
			kind:       ActualResponseModelKindAnthropic,
			payload:    `{"id":"msg_1","type":"message","model":"claude-sonnet-4-5-20250929","content":[]}`,
			wantModel:  "claude-sonnet-4-5-20250929",
			wantSource: ActualResponseModelSourceAnthropicMessage,
		},
		{
			name:       "anthropic stream message_start model",
			kind:       ActualResponseModelKindAnthropic,
			payload:    `{"type":"message_start","message":{"id":"msg_1","type":"message","model":"claude-opus-4-1-20250805"}}`,
			wantModel:  "claude-opus-4-1-20250805",
			wantSource: ActualResponseModelSourceAnthropicMessage,
		},
		{
			name:       "gemini camel modelVersion",
			kind:       ActualResponseModelKindGemini,
			payload:    `{"modelVersion":"gemini-2.5-pro","candidates":[]}`,
			wantModel:  "gemini-2.5-pro",
			wantSource: ActualResponseModelSourceGeminiModelVersion,
		},
		{
			name:       "gemini snake model_version",
			kind:       ActualResponseModelKindGemini,
			payload:    `{"model_version":"gemini-2.5-flash","candidates":[]}`,
			wantModel:  "gemini-2.5-flash",
			wantSource: ActualResponseModelSourceGeminiModelVersion,
		},
		{
			name:    "malformed input returns empty audit",
			kind:    ActualResponseModelKindOpenAIChat,
			payload: `{"model":`,
		},
		{
			name:    "empty model returns empty audit",
			kind:    ActualResponseModelKindOpenAIChat,
			payload: `{"model":"   "}`,
		},
		{
			name:    "unknown kind returns empty audit",
			kind:    ActualResponseModelKind("unknown"),
			payload: `{"model":"gpt-5.5"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DetectActualResponseModel(tt.kind, []byte(tt.payload))

			assert.Equal(t, tt.wantModel, got.Model)
			assert.Equal(t, tt.wantSource, got.Source)
		})
	}
}

func TestRelayInfoSetActualResponseModel(t *testing.T) {
	info := &RelayInfo{}

	info.SetActualResponseModel("  gpt-5.5  ", ActualResponseModelSourceOpenAIChat)
	assert.Equal(t, "gpt-5.5", info.ActualResponseModel)
	assert.Equal(t, ActualResponseModelSourceOpenAIChat, info.ActualResponseModelSource)

	info.SetActualResponseModel("gpt-5.4", "")
	assert.Equal(t, "gpt-5.5", info.ActualResponseModel)
	assert.Equal(t, ActualResponseModelSourceOpenAIChat, info.ActualResponseModelSource)

	info.SetActualResponseModel("   ", ActualResponseModelSourceOpenAIResponses)
	assert.Equal(t, "gpt-5.5", info.ActualResponseModel)
	assert.Equal(t, ActualResponseModelSourceOpenAIChat, info.ActualResponseModelSource)
}

func TestRelayInfoApplyActualResponseModelAudit(t *testing.T) {
	info := &RelayInfo{}

	ok := info.ApplyActualResponseModelAudit(
		ActualResponseModelKindOpenAIResponses,
		[]byte(`{"response":{"model":"gpt-4.1"}}`),
	)

	require.True(t, ok)
	assert.Equal(t, "gpt-4.1", info.ActualResponseModel)
	assert.Equal(t, ActualResponseModelSourceOpenAIResponses, info.ActualResponseModelSource)
}
```

- [ ] **Step 2: Run detector tests and confirm they fail**

Run:

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./relay/common -run "TestDetectActualResponseModel|TestRelayInfo" -count=1
```

Expected: FAIL because `ActualResponseModelKind`, `DetectActualResponseModel`, and `RelayInfo` audit fields do not exist.

- [ ] **Step 3: Add RelayInfo fields**

Modify `relay/common/relay_info.go` inside `type RelayInfo struct`, near `UpstreamRequestBodySize`:

```go
	// ActualResponseModel records the model name reported by the upstream response.
	// It is audit-only in Phase 1 and must not affect billing or routing.
	ActualResponseModel       string
	ActualResponseModelSource ActualResponseModelSource
```

- [ ] **Step 4: Implement detector**

Create `relay/common/actual_response_model.go`:

```go
package common

import (
	"strings"

	common2 "github.com/QuantumNous/new-api/common"
)

type ActualResponseModelKind string

const (
	ActualResponseModelKindOpenAIChat      ActualResponseModelKind = "openai_chat"
	ActualResponseModelKindOpenAIResponses ActualResponseModelKind = "openai_responses"
	ActualResponseModelKindAnthropic       ActualResponseModelKind = "anthropic"
	ActualResponseModelKindGemini          ActualResponseModelKind = "gemini"
)

type ActualResponseModelSource string

const (
	ActualResponseModelSourceOpenAIChat         ActualResponseModelSource = "openai_chat"
	ActualResponseModelSourceOpenAIResponses    ActualResponseModelSource = "openai_responses"
	ActualResponseModelSourceAnthropicMessage   ActualResponseModelSource = "anthropic_message"
	ActualResponseModelSourceGeminiModelVersion ActualResponseModelSource = "gemini_model_version"
)

type ActualResponseModelAudit struct {
	Model  string
	Source ActualResponseModelSource
}

func DetectActualResponseModel(kind ActualResponseModelKind, payload []byte) ActualResponseModelAudit {
	if len(payload) == 0 {
		return ActualResponseModelAudit{}
	}

	switch kind {
	case ActualResponseModelKindOpenAIChat:
		return detectOpenAIChatActualResponseModel(payload)
	case ActualResponseModelKindOpenAIResponses:
		return detectOpenAIResponsesActualResponseModel(payload)
	case ActualResponseModelKindAnthropic:
		return detectAnthropicActualResponseModel(payload)
	case ActualResponseModelKindGemini:
		return detectGeminiActualResponseModel(payload)
	default:
		return ActualResponseModelAudit{}
	}
}

func (info *RelayInfo) SetActualResponseModel(model string, source ActualResponseModelSource) bool {
	if info == nil || source == "" {
		return false
	}
	model = strings.TrimSpace(model)
	if model == "" {
		return false
	}
	info.ActualResponseModel = model
	info.ActualResponseModelSource = source
	return true
}

func (info *RelayInfo) ApplyActualResponseModelAudit(kind ActualResponseModelKind, payload []byte) bool {
	audit := DetectActualResponseModel(kind, payload)
	return info.SetActualResponseModel(audit.Model, audit.Source)
}

func detectOpenAIChatActualResponseModel(payload []byte) ActualResponseModelAudit {
	var body struct {
		Model string `json:"model"`
	}
	if err := common2.Unmarshal(payload, &body); err != nil {
		return ActualResponseModelAudit{}
	}
	return auditFromModel(body.Model, ActualResponseModelSourceOpenAIChat)
}

func detectOpenAIResponsesActualResponseModel(payload []byte) ActualResponseModelAudit {
	var body struct {
		Model    string `json:"model"`
		Response *struct {
			Model string `json:"model"`
		} `json:"response"`
	}
	if err := common2.Unmarshal(payload, &body); err != nil {
		return ActualResponseModelAudit{}
	}
	if body.Response != nil {
		if audit := auditFromModel(body.Response.Model, ActualResponseModelSourceOpenAIResponses); audit.Model != "" {
			return audit
		}
	}
	return auditFromModel(body.Model, ActualResponseModelSourceOpenAIResponses)
}

func detectAnthropicActualResponseModel(payload []byte) ActualResponseModelAudit {
	var body struct {
		Type    string `json:"type"`
		Model   string `json:"model"`
		Message *struct {
			Model string `json:"model"`
		} `json:"message"`
	}
	if err := common2.Unmarshal(payload, &body); err != nil {
		return ActualResponseModelAudit{}
	}
	if body.Type == "message_start" && body.Message != nil {
		if audit := auditFromModel(body.Message.Model, ActualResponseModelSourceAnthropicMessage); audit.Model != "" {
			return audit
		}
	}
	return auditFromModel(body.Model, ActualResponseModelSourceAnthropicMessage)
}

func detectGeminiActualResponseModel(payload []byte) ActualResponseModelAudit {
	var body struct {
		ModelVersion      string `json:"modelVersion"`
		ModelVersionSnake string `json:"model_version"`
	}
	if err := common2.Unmarshal(payload, &body); err != nil {
		return ActualResponseModelAudit{}
	}
	if audit := auditFromModel(body.ModelVersion, ActualResponseModelSourceGeminiModelVersion); audit.Model != "" {
		return audit
	}
	return auditFromModel(body.ModelVersionSnake, ActualResponseModelSourceGeminiModelVersion)
}

func auditFromModel(model string, source ActualResponseModelSource) ActualResponseModelAudit {
	model = strings.TrimSpace(model)
	if model == "" {
		return ActualResponseModelAudit{}
	}
	return ActualResponseModelAudit{
		Model:  model,
		Source: source,
	}
}
```

- [ ] **Step 5: Run detector tests and gofmt**

Run:

```powershell
gofmt -w relay\common\actual_response_model.go relay\common\actual_response_model_test.go relay\common\relay_info.go
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./relay/common -run "TestDetectActualResponseModel|TestRelayInfo" -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit helper**

Run:

```powershell
git add relay\common\actual_response_model.go relay\common\actual_response_model_test.go relay\common\relay_info.go
git commit -m "feat: add upstream response model audit helper"
```

## Task 2: Adapter Audit Capture

**Files:**

- Modify: `relay/channel/openai/relay-openai.go`
- Modify: `relay/channel/openai/helper.go`
- Modify: `relay/channel/openai/relay_responses.go`
- Modify: `relay/channel/claude/relay-claude.go`
- Modify: `relay/channel/gemini/relay-gemini.go`
- Create: `relay/channel/openai/actual_response_model_test.go`

- [ ] **Step 1: Write an adapter regression test for converted stream capture**

Create `relay/channel/openai/actual_response_model_test.go`:

```go
package openai

import (
	"testing"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandleLastResponseRecordsOpenAIModelBeforeDownstreamConversion(t *testing.T) {
	info := &relaycommon.RelayInfo{
		RelayFormat: types.RelayFormatClaude,
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "gpt-5.5",
		},
		ClaudeConvertInfo: &relaycommon.ClaudeConvertInfo{},
	}
	lastStreamData := `{"id":"chatcmpl_1","created":123,"model":"gpt-5.4","choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":null}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`
	var responseID string
	var createdAt int64
	var systemFingerprint string
	model := info.UpstreamModelName
	usage := &dto.Usage{}
	containStreamUsage := false
	shouldSendLastResp := true

	err := handleLastResponse(lastStreamData, &responseID, &createdAt, &systemFingerprint, &model, &usage, &containStreamUsage, info, &shouldSendLastResp)

	require.NoError(t, err)
	assert.Equal(t, "gpt-5.4", info.ActualResponseModel)
	assert.Equal(t, relaycommon.ActualResponseModelSourceOpenAIChat, info.ActualResponseModelSource)
	assert.Equal(t, "gpt-5.4", model)
	assert.True(t, containStreamUsage)
}
```

- [ ] **Step 2: Run adapter test and confirm it fails**

Run:

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./relay/channel/openai -run TestHandleLastResponseRecordsOpenAIModelBeforeDownstreamConversion -count=1
```

Expected: FAIL because `handleLastResponse` does not set `info.ActualResponseModel`.

- [ ] **Step 3: Set OpenAI Chat audit in stream helper**

Modify `relay/channel/openai/helper.go` inside `handleLastResponse` after `*model = lastStreamResponse.Model`:

```go
	info.SetActualResponseModel(lastStreamResponse.Model, relaycommon.ActualResponseModelSourceOpenAIChat)
```

The file already imports `relaycommon`, so no new import is required.

- [ ] **Step 4: Set OpenAI Chat audit in non-stream handler**

Modify `relay/channel/openai/relay-openai.go` after successful `common.Unmarshal(responseBody, &simpleResponse)` and after the OpenAI error check:

```go
	info.SetActualResponseModel(simpleResponse.Model, relaycommon.ActualResponseModelSourceOpenAIChat)
```

Put this after:

```go
	if oaiError := simpleResponse.GetOpenAIError(); oaiError != nil && oaiError.Type != "" {
		return nil, types.WithOpenAIError(*oaiError, resp.StatusCode)
	}
```

- [ ] **Step 5: Set OpenAI Responses audit**

Modify `relay/channel/openai/relay_responses.go`.

In `OaiResponsesHandler`, after the OpenAI error check:

```go
	info.SetActualResponseModel(responsesResponse.Model, relaycommon.ActualResponseModelSourceOpenAIResponses)
```

In `OaiResponsesStreamHandler`, inside the stream callback after unmarshalling `streamResponse` and before `sendResponsesStreamData`:

```go
	if streamResponse.Response != nil {
		info.SetActualResponseModel(streamResponse.Response.Model, relaycommon.ActualResponseModelSourceOpenAIResponses)
	}
```

- [ ] **Step 6: Set Claude audit**

Modify `relay/channel/claude/relay-claude.go`.

In `HandleStreamResponseData`, inside the `if claudeResponse.Type == "message_start"` block, after updating `info.UpstreamModelName`:

```go
				info.SetActualResponseModel(claudeResponse.Message.Model, relaycommon.ActualResponseModelSourceAnthropicMessage)
```

In `HandleClaudeResponseData`, after `maybeMarkClaudeRefusal(c, claudeResponse.StopReason)`:

```go
	info.SetActualResponseModel(claudeResponse.Model, relaycommon.ActualResponseModelSourceAnthropicMessage)
```

- [ ] **Step 7: Set Gemini audit**

Modify `relay/channel/gemini/relay-gemini.go`.

In `geminiStreamHandler`, after successful `common.UnmarshalJsonStr(data, &geminiResponse)` and before usage handling:

```go
	info.SetActualResponseModel(geminiResponse.ModelVersion, relaycommon.ActualResponseModelSourceGeminiModelVersion)
```

If `dto.GeminiChatResponse` does not yet expose `ModelVersion`, add this field to `dto/gemini.go`:

```go
	ModelVersion string `json:"modelVersion"`
```

In `GeminiChatHandler`, after successful `common.Unmarshal(responseBody, &geminiResponse)`:

```go
	info.SetActualResponseModel(geminiResponse.ModelVersion, relaycommon.ActualResponseModelSourceGeminiModelVersion)
```

- [ ] **Step 8: Run adapter tests**

Run:

```powershell
gofmt -w relay\channel\openai\relay-openai.go relay\channel\openai\helper.go relay\channel\openai\relay_responses.go relay\channel\claude\relay-claude.go relay\channel\gemini\relay-gemini.go relay\channel\openai\actual_response_model_test.go dto\gemini.go
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./relay/channel/openai ./relay/channel/claude ./relay/channel/gemini -run "ActualResponseModel|HandleLastResponse|Gemini" -count=1
```

Expected: PASS. If package-level existing tests unrelated to this change fail, capture the exact failure and run the focused new test plus `./relay/common` before continuing.

- [ ] **Step 9: Commit adapter capture**

Run:

```powershell
git add relay\channel\openai\relay-openai.go relay\channel\openai\helper.go relay\channel\openai\relay_responses.go relay\channel\claude\relay-claude.go relay\channel\gemini\relay-gemini.go relay\channel\openai\actual_response_model_test.go dto\gemini.go
git commit -m "feat: capture upstream response model in adapters"
```

## Task 3: Usage Log Metadata

**Files:**

- Modify: `service/log_info_generate.go`
- Create: `service/log_info_generate_model_audit_test.go`

- [ ] **Step 1: Write failing metadata tests**

Create `service/log_info_generate_model_audit_test.go`:

```go
package service

import (
	"testing"
	"time"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/assert"
)

func TestGenerateTextOtherInfoIncludesModelAudit(t *testing.T) {
	start := time.Unix(100, 0)
	info := &relaycommon.RelayInfo{
		StartTime:                 start,
		FirstResponseTime:         start.Add(250 * time.Millisecond),
		OriginModelName:           "gpt-5.5",
		ActualResponseModel:       "gpt-5.4",
		ActualResponseModelSource: relaycommon.ActualResponseModelSourceOpenAIChat,
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "gpt-5.5",
			IsModelMapped:     false,
		},
	}

	other := GenerateTextOtherInfo(nil, info, 1, 1, 1, 0, 1, 0, 1)

	assert.Equal(t, "gpt-5.5", other["upstream_model_name"])
	assert.NotContains(t, other, "is_model_mapped")
	assert.Equal(t, "gpt-5.4", other["actual_response_model"])
	assert.Equal(t, string(relaycommon.ActualResponseModelSourceOpenAIChat), other["actual_response_model_source"])
}

func TestGenerateTextOtherInfoKeepsMappingFlagOnlyForMappedRequests(t *testing.T) {
	start := time.Unix(100, 0)
	info := &relaycommon.RelayInfo{
		StartTime:         start,
		FirstResponseTime: start.Add(250 * time.Millisecond),
		OriginModelName:   "gpt-5.5",
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "gpt-5.4",
			IsModelMapped:     true,
		},
	}

	other := GenerateTextOtherInfo(nil, info, 1, 1, 1, 0, 1, 0, 1)

	assert.Equal(t, true, other["is_model_mapped"])
	assert.Equal(t, "gpt-5.4", other["upstream_model_name"])
	assert.NotContains(t, other, "actual_response_model")
	assert.NotContains(t, other, "actual_response_model_source")
}
```

- [ ] **Step 2: Run metadata tests and confirm they fail**

Run:

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./service -run TestGenerateTextOtherInfoIncludesModelAudit -count=1
```

Expected: FAIL because `upstream_model_name` is omitted when `IsModelMapped` is false and actual audit fields are not written.

- [ ] **Step 3: Add metadata writer helper**

Modify `service/log_info_generate.go`.

Add this function after `GenerateTextOtherInfo`:

```go
func appendModelAuditInfo(relayInfo *relaycommon.RelayInfo, other map[string]interface{}) {
	if relayInfo == nil || other == nil {
		return
	}
	if relayInfo.ChannelMeta != nil {
		upstreamModel := strings.TrimSpace(relayInfo.UpstreamModelName)
		if upstreamModel != "" {
			other["upstream_model_name"] = upstreamModel
		}
		if relayInfo.IsModelMapped {
			other["is_model_mapped"] = true
		}
	}
	actualModel := strings.TrimSpace(relayInfo.ActualResponseModel)
	if actualModel == "" || relayInfo.ActualResponseModelSource == "" {
		return
	}
	other["actual_response_model"] = actualModel
	other["actual_response_model_source"] = string(relayInfo.ActualResponseModelSource)
}
```

Modify the existing model-mapping block in `GenerateTextOtherInfo`.

Replace:

```go
	if relayInfo.IsModelMapped {
		other["is_model_mapped"] = true
		other["upstream_model_name"] = relayInfo.UpstreamModelName
	}
```

With:

```go
	appendModelAuditInfo(relayInfo, other)
```

`strings` is already imported in this file, so no import change is needed.

- [ ] **Step 4: Run metadata tests**

Run:

```powershell
gofmt -w service\log_info_generate.go service\log_info_generate_model_audit_test.go
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./service -run "TestGenerateTextOtherInfoIncludesModelAudit|TestGenerateTextOtherInfoKeepsMappingFlagOnlyForMappedRequests" -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit metadata**

Run:

```powershell
git add service\log_info_generate.go service\log_info_generate_model_audit_test.go
git commit -m "feat: write upstream model audit to usage logs"
```

## Task 4: Frontend Usage Log Display

**Files:**

- Modify: `web/default/src/features/usage-logs/types.ts`
- Modify: `web/default/src/features/usage-logs/lib/format.ts`
- Create: `web/default/src/features/usage-logs/lib/model-audit.test.ts`
- Modify: `web/default/src/features/usage-logs/components/model-badge.tsx`
- Modify: `web/default/src/features/usage-logs/components/columns/common-logs-columns.tsx`
- Modify: `web/default/src/features/usage-logs/components/dialogs/details-dialog.tsx`
- Modify: `web/default/src/i18n/locales/*.json` through `bun run i18n:sync`

- [ ] **Step 1: Write failing frontend model audit tests**

Create `web/default/src/features/usage-logs/lib/model-audit.test.ts`:

```ts
import assert from 'node:assert/strict'
import { describe, test } from 'node:test'
import type { UsageLog } from '../data/schema'
import { formatModelName } from './format'

function makeLog(other: Record<string, unknown>): UsageLog {
  return {
    id: 1,
    user_id: 1,
    username: 'yuan',
    token_name: 'Codex',
    model_name: 'gpt-5.5',
    channel: 1,
    channel_name: 'AI Wave',
    quota: 0,
    prompt_tokens: 1,
    completion_tokens: 1,
    use_time: 1,
    is_stream: true,
    created_at: 1,
    type: 2,
    content: '',
    group: 'default',
    other: JSON.stringify(other),
  } as UsageLog
}

describe('formatModelName model audit display', () => {
  test('does not show secondary actual model when no mapping and actual equals requested', () => {
    const result = formatModelName(
      makeLog({
        upstream_model_name: 'gpt-5.5',
        actual_response_model: 'gpt-5.5',
        actual_response_model_source: 'openai_chat',
      })
    )

    assert.equal(result.name, 'gpt-5.5')
    assert.equal(result.upstreamModel, 'gpt-5.5')
    assert.equal(result.actualResponseModel, 'gpt-5.5')
    assert.equal(result.secondaryActualModel, undefined)
  })

  test('shows secondary actual model when no mapping and actual differs from requested', () => {
    const result = formatModelName(
      makeLog({
        upstream_model_name: 'gpt-5.5',
        actual_response_model: 'gpt-5.4',
        actual_response_model_source: 'openai_chat',
      })
    )

    assert.equal(result.secondaryActualModel, 'gpt-5.4')
  })

  test('does not show secondary actual model when actual equals mapped upstream model', () => {
    const result = formatModelName(
      makeLog({
        is_model_mapped: true,
        upstream_model_name: 'gpt-5.4',
        actual_response_model: 'gpt-5.4',
        actual_response_model_source: 'openai_chat',
      })
    )

    assert.equal(result.isMapped, true)
    assert.equal(result.upstreamModel, 'gpt-5.4')
    assert.equal(result.secondaryActualModel, undefined)
  })

  test('shows secondary actual model when actual differs from mapped upstream model', () => {
    const result = formatModelName(
      makeLog({
        is_model_mapped: true,
        upstream_model_name: 'gpt-5.4',
        actual_response_model: 'gpt-5.3',
        actual_response_model_source: 'openai_chat',
      })
    )

    assert.equal(result.isMapped, true)
    assert.equal(result.upstreamModel, 'gpt-5.4')
    assert.equal(result.secondaryActualModel, 'gpt-5.3')
  })
})
```

- [ ] **Step 2: Run frontend test and confirm it fails**

Run:

```powershell
Set-Location web\default
bun test src\features\usage-logs\lib\model-audit.test.ts
```

Expected: FAIL because `formatModelName` does not return `upstreamModel`, `actualResponseModel`, or `secondaryActualModel`.

- [ ] **Step 3: Extend metadata types**

Modify `web/default/src/features/usage-logs/types.ts` inside `LogOtherData` near `upstream_model_name`:

```ts
  actual_response_model?: string
  actual_response_model_source?: string
```

- [ ] **Step 4: Update formatModelName**

Modify `web/default/src/features/usage-logs/lib/format.ts`.

Replace `formatModelName` with:

```ts
function cleanModelName(value: unknown): string | undefined {
  if (typeof value !== 'string') return undefined
  const trimmed = value.trim()
  return trimmed ? trimmed : undefined
}

export function formatModelName(log: UsageLog): {
  name: string
  isMapped: boolean
  upstreamModel?: string
  actualModel?: string
  actualResponseModel?: string
  actualResponseModelSource?: string
  secondaryActualModel?: string
} {
  const other = parseLogOther(log.other)
  const requestedModel = log.model_name
  const upstreamModel = cleanModelName(other?.upstream_model_name)
  const actualResponseModel = cleanModelName(other?.actual_response_model)
  const actualResponseModelSource = cleanModelName(
    other?.actual_response_model_source
  )
  const isMapped = !!(
    other?.is_model_mapped &&
    upstreamModel &&
    upstreamModel !== requestedModel
  )
  const comparisonModel = upstreamModel || requestedModel
  const secondaryActualModel =
    actualResponseModel && actualResponseModel !== comparisonModel
      ? actualResponseModel
      : undefined

  return {
    name: requestedModel,
    isMapped,
    upstreamModel,
    actualModel: isMapped ? upstreamModel : undefined,
    actualResponseModel,
    actualResponseModelSource,
    secondaryActualModel,
  }
}
```

Keep `actualModel` for compatibility with any existing call sites until `ModelBadge` is updated.

- [ ] **Step 5: Update ModelBadge props and display**

Modify `web/default/src/features/usage-logs/components/model-badge.tsx`.

Change props:

```ts
interface ModelBadgeProps {
  modelName: string
  upstreamModel?: string
  actualModel?: string
  actualResponseModel?: string
  actualResponseModelSource?: string
  secondaryActualModel?: string
  className?: string
}
```

Add helper values at the top of `ModelBadge`:

```ts
  const upstreamModel = props.upstreamModel || props.actualModel
  const showUpstreamMapping =
    upstreamModel && upstreamModel !== props.modelName
  const showActualMismatch = !!props.secondaryActualModel
```

Replace the existing `if (!props.actualModel)` branch with:

```tsx
  if (!showUpstreamMapping && !showActualMismatch) {
    return <ModelBadgeContent {...props} />
  }
```

Replace the popover body with:

```tsx
    <Popover>
      <PopoverTrigger
        render={
          <button type='button' className='inline-flex min-w-0 items-start gap-1' />
        }
      >
        <span className='flex min-w-0 flex-col gap-0.5'>
          <span className='inline-flex min-w-0 items-center gap-1'>
            <ModelBadgeContent {...props} />
            {showUpstreamMapping && (
              <Route className='text-muted-foreground size-3 shrink-0' />
            )}
          </span>
          {showActualMismatch && (
            <span className='text-muted-foreground flex min-w-0 items-center gap-1 pl-1 text-xs'>
              <span aria-hidden>↳</span>
              <span className='truncate font-mono'>
                {props.secondaryActualModel}
              </span>
            </span>
          )}
        </span>
      </PopoverTrigger>
      <PopoverContent className='w-80'>
        <div className='space-y-2'>
          <div className='flex items-start justify-between gap-3'>
            <span className='text-muted-foreground text-xs'>
              {t('Request Model:')}
            </span>
            <span className='truncate font-mono text-xs font-medium'>
              {props.modelName}
            </span>
          </div>
          {upstreamModel && (
            <div className='flex items-start justify-between gap-3'>
              <span className='text-muted-foreground text-xs'>
                {t('Upstream Model:')}
              </span>
              <span className='truncate font-mono text-xs font-medium'>
                {upstreamModel}
              </span>
            </div>
          )}
          {props.actualResponseModel && (
            <div className='flex items-start justify-between gap-3'>
              <span className='text-muted-foreground text-xs'>
                {t('Actual Response Model:')}
              </span>
              <span className='truncate font-mono text-xs font-medium'>
                {props.actualResponseModel}
              </span>
            </div>
          )}
          {props.actualResponseModelSource && (
            <div className='flex items-start justify-between gap-3'>
              <span className='text-muted-foreground text-xs'>
                {t('Detection Source:')}
              </span>
              <span className='truncate font-mono text-xs font-medium'>
                {props.actualResponseModelSource}
              </span>
            </div>
          )}
        </div>
      </PopoverContent>
    </Popover>
```

- [ ] **Step 6: Pass new props from table column**

Modify `web/default/src/features/usage-logs/components/columns/common-logs-columns.tsx` in the model cell:

```tsx
            <ModelBadge
              modelName={modelInfo.name}
              upstreamModel={modelInfo.upstreamModel}
              actualResponseModel={modelInfo.actualResponseModel}
              actualResponseModelSource={modelInfo.actualResponseModelSource}
              secondaryActualModel={modelInfo.secondaryActualModel}
            />
```

- [ ] **Step 7: Update details dialog model section**

Modify `web/default/src/features/usage-logs/components/dialogs/details-dialog.tsx`.

Replace the existing `{other?.is_model_mapped && other?.upstream_model_name && (` section with:

```tsx
        {(other?.upstream_model_name || other?.actual_response_model) && (
          <DetailSection label={t('Model Audit')}>
            <DetailRow
              label={t('Request Model')}
              value={props.log.model_name}
              mono
            />
            {other?.upstream_model_name && (
              <DetailRow
                label={t('Upstream Model')}
                value={other.upstream_model_name}
                mono
              />
            )}
            {other?.actual_response_model && (
              <DetailRow
                label={t('Actual Response Model')}
                value={other.actual_response_model}
                mono
              />
            )}
            {other?.actual_response_model_source && (
              <DetailRow
                label={t('Detection Source')}
                value={other.actual_response_model_source}
                mono
              />
            )}
          </DetailSection>
        )}
```

- [ ] **Step 8: Sync i18n and run frontend checks**

Run:

```powershell
Set-Location web\default
bun test src\features\usage-logs\lib\model-audit.test.ts
bun run i18n:sync
bun run typecheck
```

Expected: PASS for test and typecheck. `bun run i18n:sync` may update locale JSON files with new English keys.

- [ ] **Step 9: Commit frontend display**

Run:

```powershell
git add web\default\src\features\usage-logs\types.ts web\default\src\features\usage-logs\lib\format.ts web\default\src\features\usage-logs\lib\model-audit.test.ts web\default\src\features\usage-logs\components\model-badge.tsx web\default\src\features\usage-logs\components\columns\common-logs-columns.tsx web\default\src\features\usage-logs\components\dialogs\details-dialog.tsx web\default\src\i18n\locales
git commit -m "feat: show upstream actual model in usage logs"
```

## Task 5: Verification And Review

**Files:**

- No planned code changes.
- Optional create: `.codex/claude-upstream-actual-model-phase1-review-prompt.md` if saving the review prompt is useful; do not commit `.codex` files.

- [ ] **Step 1: Run focused backend tests**

Run:

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./relay/common ./relay/channel/openai ./relay/channel/claude ./relay/channel/gemini ./service -run "ActualResponseModel|GenerateTextOtherInfo" -count=1
```

Expected: PASS.

- [ ] **Step 2: Run broader backend smoke tests**

Run:

```powershell
& 'D:\SoftwareCategories\DevelopmentTools\SDKs\GoSDK\go1.26.4\go\bin\go.exe' test ./relay/common ./relay/channel/openai ./relay/channel/claude ./relay/channel/gemini ./service -count=1
```

Expected: PASS. If unrelated historical tests fail, capture exact package and failure text, then rerun the focused tests from Step 1.

- [ ] **Step 3: Run frontend tests and typecheck**

Run:

```powershell
Set-Location web\default
bun test src\features\usage-logs\lib\model-audit.test.ts
bun run typecheck
```

Expected: PASS.

- [ ] **Step 4: Run formatting and diff checks**

Run:

```powershell
Set-Location ..\..
git diff --check
git status --short
```

Expected: `git diff --check` produces no output. `git status --short` should show only intentional tracked changes and pre-existing untracked `.codex` files.

- [ ] **Step 5: Ask Claude for a scoped implementation review**

Use default Claude settings first:

```powershell
$prompt = @'
Review the Phase 1 upstream actual model audit implementation in Wynth API.

Focus on:
- Phase 1 must not affect billing, routing, priority, weight, or upstream sync.
- Actual model extraction must happen from upstream adapter-layer data, not downstream converted response data.
- Log display must not show duplicate actual-model lines when actual equals upstream/requested model.
- JSON handling must use project common wrappers in Go.

Return findings first. Say APPROVED if there are no blocking issues.
'@
$prompt | claude -p --output-format json --disable-slash-commands --allowedTools Read,Grep,Glob --disallowedTools Bash,Edit,Write
```

If default Claude quota fails, rerun the same prompt with:

```powershell
$prompt | claude -p --output-format json --disable-slash-commands --allowedTools Read,Grep,Glob --disallowedTools Bash,Edit,Write --settings ~/.claude/settings.wynth.json
```

- [ ] **Step 6: Evaluate Claude findings**

For each Claude finding:

```text
Accepted: implement only if it protects Phase 1 correctness.
Deferred: record as Phase 2 only if it concerns thinking signatures, pricing, scheduling, or database indexing.
Rejected: state the code-based reason in the final handoff.
```

If changes are made, rerun Steps 1-4 before continuing.

- [ ] **Step 7: Final commit if review fixes were needed**

If Step 6 produced code changes:

```powershell
git add relay\common\actual_response_model.go relay\common\actual_response_model_test.go relay\common\relay_info.go relay\channel\openai\relay-openai.go relay\channel\openai\helper.go relay\channel\openai\relay_responses.go relay\channel\openai\actual_response_model_test.go relay\channel\claude\relay-claude.go relay\channel\gemini\relay-gemini.go dto\gemini.go service\log_info_generate.go service\log_info_generate_model_audit_test.go web\default\src\features\usage-logs\types.ts web\default\src\features\usage-logs\lib\format.ts web\default\src\features\usage-logs\lib\model-audit.test.ts web\default\src\features\usage-logs\components\model-badge.tsx web\default\src\features\usage-logs\components\columns\common-logs-columns.tsx web\default\src\features\usage-logs\components\dialogs\details-dialog.tsx web\default\src\i18n\locales
git commit -m "fix: address actual model audit review"
```

If Step 6 produced no changes, do not create an empty commit.

## Final Verification Checklist

- [ ] No new database column or migration was added.
- [ ] `actual_response_model` is written only when adapter extraction succeeds.
- [ ] `upstream_model_name` is written when `RelayInfo.UpstreamModelName` is known, even without mapping.
- [ ] `is_model_mapped` is written only when local model mapping actually occurred.
- [ ] Billing continues to use `OriginModelName`.
- [ ] No priority, weight, channel selection, upstream sync, or monitor scheduling code was changed.
- [ ] OpenAI, Responses, Claude, and Gemini focused tests pass.
- [ ] Frontend model display tests pass.
- [ ] `bun run typecheck` passes.
- [ ] Claude review is completed or explicitly skipped only because Claude quota is unavailable.
