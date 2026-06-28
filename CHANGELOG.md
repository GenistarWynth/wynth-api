# Changelog

All notable changes to the **Wynth** fork (`GenistarWynth/wynth-api`) are documented here.

Wynth is a downstream fork of [New API](https://github.com/QuantumNous/new-api) by QuantumNous. This changelog records what Wynth adds or changes on top of upstream; upstream New API features are not re-listed.

## [v1.0.0-rc.16] — 2026-06-28

### Synced with upstream New API
Merged ~74 upstream commits (new-api `main`, 2026-06-18 → 2026-06-28), including:
- Authorization / RBAC permission system (`service/authz`), better admin permissions
- ClickHouse log database support; system task runner + instance reporter; system task log cleanup
- Passive channel monitoring mode; user token-limit configuration
- OpenAI Responses ↔ Chat Completions conversion; advanced-custom converter additions
- SMTP STARTTLS + NTLM auth; dashboard Sankey + playground/markdown improvements
- Toolchain: `tsgo` type-checking
- **Security:** DOMPurify bump `3.4.5` → `3.4.11` (XSS sanitizer hardening)

### Added — Account Pool (号池)
- Multi-platform account pooling: OpenAI / Codex, Anthropic, Gemini (API key, OAuth, Code Assist, Antigravity, Google One), Vertex AI (service account), xAI (OAuth), and grok.com web cookie.
- Scheduling: account selection with failure-grading cooldown, per-user concurrency, account affinity/stickiness, load-aware (least-in-flight) selection, expiry auto-pause, and per-account request quotas.
- Redis HA for concurrency / affinity / blocking (self-healing ZSET leases, in-memory fallback); proxy active health-probe (fail-open).
- Token providers: OAuth refresh seams (Anthropic claude-code mimicry, Gemini/Antigravity, xAI), Vertex SA JWT-bearer.
- Non-chat relay pooling (embedding / image / audio / rerank); WS / Realtime pooled forwarding.
- Account import (sub2api format) / export (redacted-by-default, opt-in secrets) + multi-platform admin frontend with platform/credential gating.

### Added — grok.com Web Reverse-Proxy channel (fragile / best-effort)
> A deliberately reverse-engineered, best-effort grey channel against grok.com's private web API (cookie / `console.x.ai` SSO token as upstream). Coexists with the official X.AI OAuth channel. Not guaranteed to be stable; labeled as such in code.
- New pool platform `grok_web` (channel type 59) with an encrypted SSO-cookie (+ optional `cf_clearance`) credential.
- OpenAI ↔ grok web-SSE translation, anti-bot headers, in-band error / quota / Cloudflare classification into the account-pool failure cooldown.
- Text chat (`grok-4.x`), **image generation** (`grok-2-image`, via the chat SSE + authed asset download → `b64_json`), **reasoning** (`grok-4-reasoning`, surfaces `reasoning_content`), and **deep-search** (`grok-4-deepsearch`, `## Sources`).
- Video / WS-Imagine / image-edit are out of scope (video delivery conflicts with the no-media-cache policy).

### Added — Other
- Upstream-source channel generation: skip mapping & disable generated channels when an upstream sync returns no usable models.

### Notes
- Image/video/deep-search live grok.com calls are not verifiable in-repo; behavior is mirrored from the grok2api reference and covered by mock-based unit tests, labeled unverifiable where applicable.
- Gates for this release: backend `go test ./model ./service ./controller ./relay ./middleware ./types` and frontend `tsgo` type-check, both green.
