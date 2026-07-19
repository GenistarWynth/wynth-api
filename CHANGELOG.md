# Changelog

All notable changes to the **Wynth** fork (`GenistarWynth/wynth-api`) are documented here.

Wynth is a downstream fork of [New API](https://github.com/QuantumNous/new-api) by QuantumNous. This changelog records what Wynth adds or changes on top of upstream; upstream New API features are not re-listed.

## [v1.0.0-rc.37] - 2026-07-19

### Fixes
- Auto-priority preserves relative close-price gaps so availability, first-token latency, and throughput can outweigh small cost differences.
- Gate-usable channels with at least an 8x effective-cost advantage now receive strict score and priority dominance within their local-group and channel-type cohort.
- Group-wide cost ceilings extend extreme-cost dominance to split-worker single-member runs, and hysteresis can no longer retain an inverted extreme-cost order.
- Group cost bounds continue to include both manual and upstream-generated auto-priority channels.

## [v1.0.0-rc.36] - 2026-07-19

### Features
- Non-upstream channels can enable auto-priority with configurable rate multipliers.
- Auto-priority availability window is group-scoped and applies to all auto-priority-enabled channels in the current group, including upstream-generated channels.
- Auto Priority is a dedicated channel row-menu entry at the same level as Channel Monitor.
- Upstream source rule strategy overrides load monitor/fixed model options from matched group model lists.

### Fixes
- Channel Monitor remains monitor-only; auto-priority settings no longer clutter the monitor dialog.
- Monitor and auto-priority saves use explicit scopes so they do not overwrite each other.

## [Unreleased]

### Added — Account Pool (号池)
- Added a complete pool-scoped Grok/X.AI OAuth login flow: PKCE authorization, single-use state sessions, callback/code exchange, encrypted account creation or update, saved-account refresh, and a guided React admin workflow.
- Added bounded Grok Web SSO-to-Build OAuth account import through trusted `x.ai` device endpoints, with proxy support and per-item results that never echo submitted SSO secrets.
- Added current sub2api Grok OAuth import/export compatibility for the outbound `grok` platform and inbound `grok`/`xai` aliases, `rt`, rotated access/refresh tokens, RFC3339 expiry, OAuth client/team identity, subscription tier, and entitlement metadata.
- Account views now expose the non-secret credential type, so legacy refresh-token-only xAI OAuth accounts reopen in the correct admin workflow without exposing stored credentials.
- Added complete English, Chinese, French, Japanese, Russian, and Vietnamese translations for the Grok OAuth administrator workflow.

### Fixed — Account Pool (号池)
- xAI runtime and administrator refresh now honor the OAuth client ID captured during authorization and prefer the newest rotated refresh token in token state, while preserving default-client and stored-credential fallbacks for existing accounts.
- Administrator token refresh and missing-refresh expiration use encrypted credential/token-state compare-and-swap guards, preventing concurrent rotations from being overwritten or incorrectly retired.
- OAuth accounts with an expired access token and no refresh token are marked expired and removed from scheduling; channel-test requests remain mutation-free.

### Notes — Account Pool (号池)
- Existing temporary-disable, overload, request-quota, per-model cooldown, affinity, retry, and lease scheduling already cover the corresponding newer sub2api pool-mode fixes.
- Grok billing snapshots, Free-tier rolling quota estimates, paid-media quarantine, proactive background reconciliation, and per-account upstream URL/header overrides remain deferred pending Wynth-native probe, lifecycle, and security designs.

## [v1.0.0-rc.35] - 2026-07-18

### Added - Codex and Responses compatibility
- Added Responses request metadata, reasoning context, prompt-cache controls,
  sticky turn state, and configurable Codex field passthrough.
- Added GPT-5.6 Sol, Terra, and Luna model options, together with the current
  Codex channel options for GPT-5.4 Mini and GPT-5.5 and their compact forms.
- Added native cache-write token accounting and compact request/response
  compatibility while retaining Wynth's namespace-tool conversion behavior.

### Added - Providers, billing, and administration
- Added Seedance 2.0 resolution/video billing, safety and priority fields, and
  Wan 2.7 media mapping.
- Added Ollama non-stream tool-call handling, nested usage-token details,
  tiered-pricing estimates, ratio handling, quota-unit transfers, and safer
  pre-consume settlement.
- Added redemption cleanup, subscription quota reset, stale-instance cleanup,
  model filtering, and related admin workflows.

### Added - Security and operations
- Added outbound destination validation at dial time, DNS-result validation,
  address-range blocking, and redirect-hop safety while preserving configured
  provider and account-pool proxies.
- Hardened username/email/password handling, read-only token authorization,
  secure session cookies, redeem failure messages, OAuth setup, and graceful
  shutdown/accounting cache behavior.
- Upstream sources can now authenticate to gateways that gate login behind
  Cloudflare Turnstile: paste an already-authenticated session (cookie / token)
  via manual import, or use an optional headless-browser login sidecar
  (`browserless`, gated by `--profile upstream-browser` and
  `UPSTREAM_BROWSER_CDP_URL` in `docker-compose.yml`).
- Upstream source credentials and sessions are encrypted at rest.

### Changed - Web applications and delivery
- Refined channel management, advanced custom routes, model pricing sync,
  playground controls, group-ratio editing, home iframe behavior, and mobile
  user cards across the default and classic frontends.
- Added stream timing, first-token, duration, and throughput presentation to
  usage logs, with localized compact pricing details.
- Updated build tooling, dependencies, Make targets, Docker publishing and
  signing workflows, and classic-to-default transition guidance.

### Fixed - Stream lifecycle and frontend behavior
- Prevented stale writes after client disconnects, reconciled image billing,
  and kept stream workers and account-pool failure handling consistent.
- Fixed locale normalization, unset-price model messaging, referral copy,
  channel filter persistence, resized table layout, OAuth callback copy, and
  browser-translation interference with React roots.

### Notes
- This release contains the Wynth adaptations represented by the frozen
  upstream audit through `7c28993f`. Ten newer upstream commits remain outside
  that audit and will require a separate review.
- Responses WebSocket support, live Codex E2E verification, full Programmatic
  Tool Calling semantics, and the multi-agent beta remain explicitly outside
  the verified compatibility claim.

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
