# Batch 2 Production Stability Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add image billing/disconnect correctness, bounded compact JSON/SSE normalization, and xAI reasoning/image compatibility without replacing current relay infrastructure.

**Architecture:** Implement each domain as an independently committed TDD slice. Reuse current stream scanner, billing settlement, JSON wrappers, and adapters; port upstream behavior rather than file structure. Review each slice before the next and run a whole-branch adversarial review before merge.

**Tech Stack:** Go 1.22+, Gin, GORM v2, testify, existing relay scanner and billing services.

## Global Constraints

- Support SQLite, MySQL >= 5.7.8, and PostgreSQL >= 9.6.
- Use `common.*` wrappers for JSON marshal/unmarshal.
- New Go tests use `require` for setup/fatal assertions and `assert` for values.
- Preserve explicit optional zero/false values.
- Failed/incomplete compact responses settle zero quota; successful terminal usage settles once.
- Image disconnects never reduce billable count below requested count.
- No heartbeat, active provider probe, Grok video, schema migration, or unrelated refactor.

---

### Task 1: OpenAI Image Stream Billing and Disconnect Correctness

**Files:**
- Modify: `relay/channel/openai/relay_image.go`
- Modify if dispatch metadata is needed: `relay/channel/openai/adaptor.go`
- Test: `relay/channel/openai/image_stream_test.go`
- Test: `relay/channel/openai/relay_image_test.go` or nearest existing image test file

**Interfaces:**
- Consumes: `relayInfo.PriceData.OtherRatios()["n"]`, `RelayInfo.UsePrice`, `StreamScannerHandler`, `StreamResult.Stop`
- Produces: one reconciliation path that updates fixed-price image count before settlement

- [ ] Add failing table tests: JSON requested 3/actual 2 → 2; empty data → 3; non-fixed pricing → unchanged; clean SSE two completed events → 2; disconnect after one of three → 3; disconnect after two when requested one → 2; invalid counts ignored.
- [ ] Add failing tests proving JSON-to-SSE emits each image, downstream write failure stops the stream, no `[DONE]` follows a failed write, and non-2xx stays JSON.
- [ ] Run `go test ./relay/channel/openai -run 'Image|ImageStream' -count=1`; verify RED for missing reconciliation/write handling.
- [ ] Implement actual-count observation and fixed-price reconciliation with clean-completion versus disconnect rules; keep scanner infrastructure and existing response formats.
- [ ] Make downstream writes propagate errors through `StreamResult.Stop`; preserve nil safety and non-2xx handling.
- [ ] Run focused tests and `go test -race ./relay/channel/openai -run 'Image|ImageStream' -count=1`; expect PASS.
- [ ] Commit only image changes.

### Task 2: Bounded Compact JSON/SSE Normalization

**Files:**
- Modify: `relay/channel/openai/relay_responses_compact.go`
- Modify: `relay/responses_handler.go`
- Modify if signal/DTO parity is required: `dto/openai_responses_compaction_request.go`
- Create/Test: `relay/channel/openai/relay_responses_compact_test.go`
- Test: focused settlement tests near `relay/responses_handler_test.go`

**Interfaces:**
- Consumes: compact upstream `*http.Response`
- Produces: normalized terminal compact JSON and usage only on success; explicit bounded-body and SSE parsing errors

- [ ] Add failing JSON compatibility and body-size-limit tests.
- [ ] Add failing SSE tests covering raw `response.output_item.done.item`, compaction-only `added` fallback, unknown-field preservation, missing compaction supplementation, and exact deduplication.
- [ ] Add failing error tests for malformed/truncated SSE, `response.failed`, missing/contradictory terminal response, cancellation, and oversized bodies; assert no usage/settlement.
- [ ] Add settlement tests proving successful terminal usage settles exactly once and failures settle zero times.
- [ ] Run focused OpenAI relay tests; verify RED against the current unbounded JSON-only handler.
- [ ] Implement bounded reading, framing detection, request-scoped SSE accumulator, raw item preservation via `json.RawMessage`, stable identity dedupe, sanitized bounded errors, and terminal usage extraction through `common.*` serialization.
- [ ] Keep path-based compact output JSON; do not add heartbeat or SSE synthesis.
- [ ] Run `go test ./relay/channel/openai ./relay -run 'Compact|Compaction' -count=1` and relevant race tests; expect PASS.
- [ ] Commit only compact normalization changes.

### Task 3: xAI Reasoning-Effort Parity

**Files:**
- Modify: `relay/channel/xai/adaptor.go`
- Test: `relay/channel/xai/adaptor_test.go`

**Interfaces:**
- Consumes: Chat and Responses request reasoning fields, model suffix/mapping
- Produces: one effective forwarded effort mirrored exactly in `RelayInfo.ReasoningEffort`

- [ ] Add failing table tests for absent, low/high, unsupported, suffix-derived, explicit-versus-suffix precedence, mapped model, and Responses nested reasoning; assert both upstream JSON and relay metadata.
- [ ] Run `go test ./relay/channel/xai -run Reasoning -count=1`; verify RED for Responses parity gaps.
- [ ] Implement deterministic normalization shared by the existing conversion branches without adding a one-caller package helper unless it expresses stable domain behavior.
- [ ] Run all xAI tests; expect PASS.
- [ ] Commit only xAI reasoning changes.

### Task 4: xAI Image Behavior Regression Locks

**Files:**
- Test: `relay/channel/xai/adaptor_test.go` or existing xAI image tests

**Interfaces:**
- Consumes: existing xAI image request conversion
- Produces: regression coverage only

- [ ] Add tests asserting unsupported `size`, `quality`, and `style` are absent upstream while model, prompt, `n`, and response format survive.
- [ ] Add a table over all currently advertised xAI image models and assert image routing.
- [ ] Run xAI tests; expected PASS if existing behavior is complete. Do not alter production code unless a test exposes a real contract defect; if it does, follow a new RED/GREEN cycle.
- [ ] Commit regression locks or the minimal exposed fix.

### Task 5: Whole-Branch Verification and Merge

**Files:**
- Review all changes from Batch 2 base to HEAD

- [ ] Run `gofmt` and `git diff --check`.
- [ ] Run focused tests for OpenAI image, compact, xAI, relay settlement, and billing.
- [ ] Run race tests for touched streaming packages.
- [ ] Run all compilable Go package tests; build/provide embedded frontend assets before attempting root `go test ./...`.
- [ ] Run multi-angle adversarial review over correctness, billing, streaming concurrency, protocol framing, error disclosure, cross-database behavior, reuse, and test quality.
- [ ] Fix every confirmed finding with tests and rerun verification.
- [ ] Commit the plan if not already committed and ensure the worktree is clean.
- [ ] Fast-forward local `main` only after verifying it remains an ancestor and no dirty main-checkout file overlaps the diff; do not push.
- [ ] Re-run focused verification against the merged `main` reference.
