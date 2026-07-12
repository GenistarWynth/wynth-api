# Batch 3 Protocol and Administration Design

## Goal

Complete high-value protocol compatibility and administration capabilities after Batch 2 is merged, while isolating security-sensitive account header overrides and unverified model exposure.

## Scope

### Responses-via-Chat namespace and tool_search bridge

Introduce a request-scoped conversion result containing emitted callable names and exact reverse mappings. Flatten namespace children, detect collisions after final name normalization, emit a synthetic `tool_search` proxy only when safe, filter `tool_choice` against actual emitted tools, and restore namespace metadata in non-stream and streaming Responses output. Ambiguity is rejected before relay; no package globals or persisted conversion metadata are allowed.

### Codex Responses and compact field synchronization

Synchronize confirmed request fields: client metadata, reasoning mode/context, tools, parallel tool calls, reasoning, service tier, prompt cache key, and text. DTO presence alone is insufficient; `ResponsesHelper` must forward the fields. The local Codex identity implementation remains authoritative. Request and response headers use existing allow/deny policies.

### Cache usage gap audit

Before implementation, separately compare new-api cache-write/prompt-cache-key behavior and sub2api Anthropic cache-creation usage with the post-Batch-2 code. Port only missing behavior, preserving the local billing normalization chain and preventing duplicate prompt/cache charges.

### Enabled-channel unset-price administration tab

Derive candidate rows only from enabled-channel models and filter through the existing base-price predicate. In unset mode, hide destructive/raw controls while allowing editing and batch copy. Preserve explicit zero/free semantics, current drafts, memo equality, and pagination clamping. No backend or database change is required.

### API Token live concurrency statistics

Use Redis ZSET leases keyed by token ID. Acquire once per authenticated client request and release idempotently across success, failure, stream EOF/error, cancellation, WebSocket close, and panic cleanup. Upstream retries do not reacquire. Enrich list/search responses with batched counts through a response DTO; do not persist concurrency or add a migration. Redis failure is observational and must not fail traffic.

## Conflict Strategy

Batch 3 starts from the Batch 2 merged `main`. Protocol conversion signature changes are explicit and request-scoped. Current JSON wrapper rules, streaming conversion behavior, Codex identity policy, account-pool retries, Redis namespace conventions, and frontend design system remain authoritative. Features are committed and reviewed independently before final merge.

## Security and Error Handling

- Tool-name ambiguity rejects rather than silently selecting a mapping.
- Missing/dropped tool choices are omitted instead of provoking upstream errors.
- Header forwarding remains scoped and filtered.
- Cache usage cannot be counted twice across prompt and cache-creation fields.
- Concurrency metrics never include raw token strings and never affect billing or quota.
- Redis errors degrade metrics only.

## Testing

Use deterministic Go tests for request conversion, collision handling, tool-choice filtering, response restoration, Codex field forwarding, cache accounting, Redis lease lifecycle, retries, cancellation, and DTO enrichment. Use frontend pure-function/component tests for unset-price derivation, explicit free pricing, save removal, draft batch copy, and pagination. Run Go focused/race tests and Bun frontend checks/build.

## Deferred or Excluded

Account-level API-key header overrides require a dedicated security specification and are not included. Compact heartbeat/SSE synthesis, active provider quota probes, unverified Codex model manifests, Grok video, and direct Vue/framework ports are excluded.
