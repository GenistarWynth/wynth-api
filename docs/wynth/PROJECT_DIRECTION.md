# Wynth API Project Direction

## Baseline

Wynth API starts from `QuantumNous/new-api` and keeps its core product model:

- multi-upstream LLM gateway
- channel, model, token, user, billing, and relay management
- OpenAI-compatible, Claude-compatible, and Gemini-compatible routing
- React-based admin frontend under `web/default`

The upstream remote is kept as `upstream` so the project can continue to absorb fixes from `new-api`.

## Product Positioning

The main product line is multi-upstream channel aggregation. An internal account pool is treated as one advanced upstream channel, not as the center of the whole system.

That means Wynth API should keep `new-api` concepts as the primary language:

- Channel remains the main upstream abstraction.
- Relay remains the central request path.
- User tokens, billing, logs, and analytics stay unified across all channel types.
- Account pools plug into relay as schedulable channel implementations.

## Reference Projects

`Wei-Shaw/sub2api` is the main reference for account-pool behavior and admin layout:

- account pool management
- OAuth and API-key credential handling
- quota windows and reset behavior
- account scheduling and sticky sessions
- per-account and per-user concurrency controls
- request and token rate limiting
- dense operational dashboard layout

`deanxv/done-hub` is a reference for provider and native-route behavior:

- Claude Code reverse proxy channels
- Codex reverse proxy channels
- Gemini CLI reverse proxy channels
- native route compatibility across provider types

`ding113/claude-code-hub` is a reference for product experience and observability:

- request filtering ideas
- usage and log exploration
- monitoring and operational views
- frontend interaction patterns

## Frontend Direction

The frontend should stay on `new-api/web/default` and its React stack. The layout can be redesigned to borrow the denser operational style of `sub2api`, especially for account-pool and dashboard screens.

Avoid directly adopting the `sub2api` Vue frontend unless the backend product model changes to make account pools the primary system abstraction.

## First Implementation Boundary

The first major feature boundary should be an account-pool channel subsystem:

- pool definitions
- upstream account records
- credential storage and redaction
- quota and reset windows
- scheduler selection
- sticky session support
- relay adapter integration
- admin screens inside the existing React frontend

This boundary should be implemented without replacing the existing channel, user, token, billing, or relay foundation.
