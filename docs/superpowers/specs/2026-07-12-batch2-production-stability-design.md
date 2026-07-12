# Batch 2 Production Stability Design

## Goal

Port the highest-value upstream production-stability fixes into the current fork without replacing its relay, streaming, billing, or account-pool architecture.

## Scope

### OpenAI image stream billing and disconnect correctness

Reconcile fixed-price image billing against observed successful output count. On clean completion, use a valid actual count. On client disconnect or handler failure, never reduce the requested count; only raise it when already-observed completions exceed the request. Reuse `StreamScannerHandler`, propagate downstream write failures through `StreamResult.Stop`, preserve non-2xx JSON errors, and reject invalid counts outside the existing image-count contract.

### Compact Responses bounded normalization

Support compact upstream responses framed as either JSON or SSE. Read through an explicit size ceiling, preserve complete raw compaction items and unknown fields, supplement a missing compaction item exactly once, and return usage only after a valid terminal response. Malformed/truncated SSE, `response.failed`, contradictory terminal frames, cancellation, and oversized bodies return errors before settlement.

Path-based `/v1/responses/compact` remains JSON by default. Heartbeats and downstream SSE synthesis are not included.

### xAI reasoning-effort parity

Normalize reasoning effort for both Chat Completions and Responses. The effective value forwarded upstream must equal `RelayInfo.ReasoningEffort`, ensuring logs and effort-sensitive billing describe the request actually sent.

### xAI image regression locks

Add tests proving unsupported OpenAI image fields are omitted while supported model, prompt, count, and response format survive. No duplicate production sanitizer is added.

## Conflict Strategy

Port behavior and tests, not upstream file layouts. Keep current streaming helpers, JSON wrappers, billing settlement flow, Codex identity policy, database abstractions, and account-pool integration. Do not cherry-pick conflicting upstream commits wholesale.

## Error Handling and Billing Invariants

- A failed or incomplete compact normalization settles zero quota.
- A successful compact terminal response exposes usage once and settles once.
- Image disconnect handling cannot reduce billable count below requested count.
- Non-2xx upstream image responses remain errors, not HTTP-200 SSE.
- Provider error text is bounded and sanitized.
- No schema migration is introduced.

## Testing

Use deterministic Go tests with testify. Cover JSON/SSE compact framing, raw item preservation, deduplication, malformed and oversized responses, settlement count, image actual/requested reconciliation, downstream write failure, non-2xx behavior, xAI reasoning metadata, and xAI image field filtering. Run focused tests, race tests for touched streaming code, all compilable Go package tests, and full tests once embedded frontend assets are available.

## Deferred

Compact heartbeat, synchronized response-writer infrastructure, active xAI quota probes, Grok video, and unrelated upstream UI/framework code are excluded.
