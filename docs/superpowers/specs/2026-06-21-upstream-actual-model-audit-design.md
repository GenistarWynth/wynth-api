# Upstream Actual Model Audit Design

## Goal

Record and display the model that an upstream relay actually used, separately from the model requested by the client and the model Wynth API sent upstream after local mapping.

This feature is intentionally split into two phases so the first implementation stays observable and low risk, while later work can make model detection more precise and feed pricing or routing decisions.

## Reference Findings

ClaudeCodeHub stores three distinct model concepts:

- `originalModel`: the client-requested model.
- `model`: the effective model after provider model redirects.
- `actualResponseModel`: the model extracted from the upstream response body.

Its extraction logic is response-protocol aware. It reads OpenAI Chat `model`, OpenAI Responses `response.model`, Anthropic `message.model` or stream `message_start.message.model`, and Gemini `modelVersion` / `model_version`. For Anthropic streaming responses, it can also decode the model from `signature_delta` thinking signatures, which is more precise when an intermediate relay rewrites the cleartext response model.

Reference commits:

- `ding113/claude-code-hub@f96d00fce9131694b2c499f12ca7fbff872e898b`
- `deanxv/done-hub@976eaafbe6f8608fd6c3ec0ab9e7eb0e453d7029`

Relevant ClaudeCodeHub files:

- `src/app/v1/_lib/proxy/actual-response-model.ts`
- `src/app/v1/_lib/proxy/anthropic-actual-response-model.ts`
- `src/app/v1/_lib/proxy/thinking-signature-model.ts`
- `src/lib/utils/model-audit-display.ts`

done-hub has useful request-side mapping concepts, but it does not appear to persist an independent response-side `actual_response_model`. Its `UnifiedRequestResponseModelEnabled` path rewrites the client-visible response model back to the original model, which is the opposite of an audit signal.

## Product Decisions

- Implement actual response model audit in two phases.
- Phase 1 stores and displays observed model data only.
- Phase 2 improves Anthropic precision and can feed pricing or routing decisions.
- Do not change billing, priority, weight, channel selection, or upstream sync behavior in Phase 1.
- Do not rewrite the client-visible response model just to make audit display cleaner.
- Prefer storing first-pass audit fields in log `Other` metadata unless a query or filtering requirement justifies a first-class database column.

## Current Wynth API State

Wynth API already distinguishes model concepts during relay:

- `RelayInfo.OriginModelName`: the user-facing requested and billed model.
- `RelayInfo.UpstreamModelName`: the model sent to the selected upstream after channel model mapping.
- `RelayInfo.IsModelMapped`: whether local model mapping changed the upstream request model.

Text consume logs already include `upstream_model_name` in `Other` when model mapping is active. This makes `Other` the lowest-risk place to add first-pass model audit metadata.

The current repo locations to verify before implementation are:

- `relay/common/relay_info.go` for `RelayInfo` and `ChannelMeta` model fields.
- `relay/helper/model_mapped.go` for local model mapping behavior.
- `service/log_info_generate.go` and consume-log writers for `Other` metadata.

## Phase 1: Basic Model Audit

Phase 1 should add a provider-aware response model extractor and persist the result in consume-log metadata.

### Data Contract

For successful text-like relay requests, log metadata should be able to include:

- `upstream_model_name`: model Wynth API sent upstream. Phase 1 should write it when known, even if no local model mapping occurred, so UI comparison has a stable baseline.
- `actual_response_model`: model detected from the upstream response body.
- `actual_response_model_source`: extraction source, for example `openai_chat`, `openai_responses`, `anthropic_message`, or `gemini_model_version`.

Do not let `actual_response_model` affect billing in Phase 1. Billing remains based on the existing model pricing rules and `OriginModelName`.

If the extractor cannot determine a model, omit `actual_response_model` instead of writing an empty string or copying a guessed value.

If future scheduling or filtering needs to query `actual_response_model` efficiently, promote it from `Other` metadata to first-class log columns in a separate cross-database migration. Do not rely on database-specific JSON operators for Phase 1.

### Extractor Scope

The first implementation should support:

- OpenAI-compatible chat non-stream responses: top-level `model`.
- OpenAI-compatible chat stream chunks: first non-empty top-level `model`.
- OpenAI Responses non-stream responses: top-level `model` or `response.model` where present.
- OpenAI Responses stream events: `response.model` from response envelope events.
- Anthropic non-stream responses: top-level `model`.
- Anthropic stream events: `message_start.message.model`.
- Gemini non-stream and stream responses: `modelVersion` or `model_version`.

The extractor must never fail the user request. Malformed JSON, missing fields, unknown formats, and partial streams should return no audit model.

### Relay Integration

The extractor should run in the channel adapter or stream handler layer that parses the upstream response, not in the final generic consume-log layer. This is important because converted requests may be transformed into a different downstream client format before logging. The audit value must reflect the upstream provider response format.

Phase 1 may add optional fields to `RelayInfo`, such as `ActualResponseModel` and `ActualResponseModelSource`, so adapters can pass detected values into the existing consume-log metadata path without re-parsing adapted downstream output.

For stream requests, the implementation may use already-collected stream text or the last parsed response event where available. It should not add a second full buffering path if the relay already has enough data.

For converted responses, extraction should follow the upstream provider response format, not the downstream client format. For example, if Wynth API converts Anthropic upstream output into OpenAI-style downstream chunks, the audit should still record the upstream Anthropic model when available.

### Frontend Display

The usage log model column should follow a ClaudeCodeHub-like display:

- Primary line: existing model/charging model display.
- Secondary line: `-> actual_response_model` only when it differs from the effective upstream request model.
- Tooltip or detail panel: show requested model, upstream request model, actual response model, and extraction source.

The UI should compare `actual_response_model` against `upstream_model_name` when present, otherwise against the requested model. It should not show duplicate second lines when the actual model equals that baseline or is missing.

The billed model shown in logs remains tied to the existing billing model, normally `OriginModelName`. `actual_response_model` is an audit line, not a statement of what was charged.

## Phase 2: Precision And Scheduling Inputs

Phase 2 should add stronger model verification and make the audit data available to later pricing or scheduling work.

### Anthropic Thinking Signature Detection

Implement Anthropic streaming signature detection after Phase 1 is stable.

Target behavior:

- Detect `content_block_delta` events with `delta.type == "signature_delta"`.
- Decode base64 or base64url signature payloads.
- Walk the known protobuf length-delimited field path used by ClaudeCodeHub.
- Use the decoded model as a higher-confidence `actual_response_model`.
- Record `actual_response_model_source` as `anthropic_thinking_signature`.

If signature decoding fails, fall back to Phase 1 `message_start.message.model`.

### Future Scheduling Inputs

Once actual model audit is reliable, later price-to-priority work can use it as an input. That later feature should explicitly decide whether priority is based on:

- configured upstream group multiplier;
- requested model price;
- upstream request model price;
- actual response model price;
- availability and latency metrics;
- cache hit or cache write behavior.

This design does not choose those scoring rules.

## Error Handling

Model audit extraction must be best-effort:

- never abort a relay request;
- never change the upstream or downstream response body;
- never change quota settlement;
- log extractor errors only at debug level unless they indicate a code bug;
- avoid storing large response fragments or raw thinking signatures in logs.

## Testing

Backend tests should cover:

- OpenAI Chat non-stream and stream extraction.
- OpenAI Responses non-stream and stream extraction.
- Anthropic non-stream and stream extraction.
- Gemini `modelVersion` and `model_version` extraction.
- malformed and empty input returning no model.
- consume-log metadata includes `actual_response_model` only when extraction succeeds.
- model mapping logs can show requested, upstream, and actual model distinctly.
- converted upstream responses record the upstream-format model, not the downstream client-format model.

Frontend tests should cover:

- no secondary line when actual model is missing.
- no secondary line when actual model matches upstream request model.
- no secondary line when no mapping occurred and actual model matches requested model.
- secondary line when actual model differs.
- details display requested model, upstream model, actual model, and source.

Phase 2 tests should add Anthropic thinking signature fixtures only after the decoder is implemented.

## Out Of Scope

- Price-to-priority adjustment.
- Cache-rate scoring.
- Availability-based scheduling.
- Automatic channel priority or weight updates.
- Rewriting downstream `model` fields for compatibility.
- Storing raw upstream response bodies or thinking signatures.
- Supporting every provider-specific response format in the first pass.
