# Changelog

All notable changes to the **Wynth** fork (`GenistarWynth/wynth-api`) are documented here.

Wynth is a downstream fork of [New API](https://github.com/QuantumNous/new-api) by QuantumNous. This changelog records what Wynth adds or changes on top of upstream; upstream New API features are not re-listed.

## [Unreleased]

## [v1.0.0-rc.46] - 2026-07-20

### Changed
- Upstream-source rule auto-priority overrides no longer expose or apply availability window hours; availability remains a channel/group-level setting, while metrics window hours remain configurable per rule.

## [v1.0.0-rc.45] - 2026-07-20

### Fixed
- Channel affinity no longer overrides higher-priority channels; stickiness now applies only within the current highest priority tier.

## [v1.0.0-rc.44] - 2026-07-20

### Fixed
- Manually disabled channels now fully exit auto-priority: channel and ability priorities are sunk immediately and on every worker tick, while auto-priority score metadata is cleared so stale competitive scores are no longer displayed; auto-disabled channels remain unchanged.

## [v1.0.0-rc.43] - 2026-07-20

### Changed
- Post-mortem recovery is now opt-in per channel under **Channel Monitor → Post-mortem recovery**, using `channel_dead_recovery_enabled`, `channel_dead_recovery_min_minutes`, and `channel_dead_recovery_max_minutes`. Existing channels default to off, the former global Routing Reliability controls are no longer used, and the worker retains a fixed safety cap of five probes per minute.

## [v1.0.0-rc.42] - 2026-07-20

### Added
- Auto-priority scheduling now runs per local channel group: any overdue or never-run enabled member makes the full manual/generated cohort due, and successful runs synchronize the shared interval and last-run timestamp across the group.
- Channel Auto Priority settings can force an immediate recompute for the current group, bypassing the due gate once.

### Changed
- Auto-priority intervals are now group-scoped channel settings; upstream source rules retain only the auto-priority enable switch and no longer override or display an interval.

### Fixed
- Manually disabled channels are excluded from competitive auto-priority ranking and forced to the bottom of their group; auto-disabled channels are unchanged by this sink rule.

## [v1.0.0-rc.41] - 2026-07-20

### Fixed
- Client identity simulation for Codex CLI now emits a full interactive Codex fingerprint: `codex_cli_rs` originator/UA pairing plus session/thread correlation headers and the `x-codex-*` metadata family captured from real Codex outbound requests, not only the minimal UA/originator pair.
- Client identity simulation for Claude Code now emits the full official Claude Code fingerprint (`claude-cli` UA, Stainless headers, `X-App`, `anthropic-version`, Claude Code beta flags) and forces channel Test/Monitor onto Anthropic Messages (`/v1/messages`) with stream enabled.

### Added
- Auto-disabled channels that do **not** enable per-channel monitor now receive sparse randomized post-mortem recovery probes (15–120 minutes after death/last failed recovery, up to 5 per minute tick) so they can auto-recover without a fixed stampede schedule. Manually disabled channels and monitor-enabled channels are left to their existing paths.
- Dead recovery scheduling is configurable in **System Settings → Models → Routing Reliability** via `dead_channel_recovery_min_minutes` (default 15), `dead_channel_recovery_max_minutes` (default 120), and `dead_channel_recovery_max_per_tick` (default 5).
- Channel Monitor UI/API now shows the next post-mortem recovery check time for eligible auto-disabled channels.

## [v1.0.0-rc.40] - 2026-07-20

### Fixed
- Channel Test and Channel Monitor force OpenAI Responses + stream for channels with Client identity simulation = Codex CLI, matching strict codex-only upstream policy (`codex_requires_responses_protocol`).
- Codex CLI identity preset now also sets the built-in `OpenAI-Beta: responses=experimental` default when empty, matching the native Codex channel adaptor.

## [v1.0.0-rc.39] - 2026-07-20

### Added
- Channel account import now supports multi-select of CPA/auth JSON files and merges multiple single-account files into one batch import payload.
- Channel settings gain a compact **Client identity simulation** control (`off` / `codex_cli` / `claude_code`) for OpenAI, Anthropic, and Codex channels. Outbound requests can reuse the exact built-in Codex CLI and Claude Code header bundles after header overrides, without inventing User-Agent/Stainless/originator values.

### Notes
- Client identity simulation changes fingerprint headers only; it does not change channel auth schemes. Account-pool OAuth paths continue to apply their existing Claude Code mimicry independently.

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

## [v1.0.0-rc.38] - 2026-07-19

> Packaged from previously Unreleased account-pool work.


### Added — Account Pool (号池)
- Account import now accepts current CLIProxyAPI/CPA `codex-api-key` YAML/JSON lists and single or batched auth JSON files for OpenAI/Codex, Anthropic/Claude, Gemini/Antigravity/Google One/Vertex, and xAI pools. Imports retain compatible identity, token-expiry, priority/status, base-URL, header, proxy, and model-policy fields; CPA `direct`/`none` explicitly bypasses a pool default proxy, and the localized file-picker workflow describes the accepted config/auth files.
- Added a complete pool-scoped Grok/X.AI OAuth login flow: PKCE authorization, single-use state sessions, callback/code exchange, encrypted account creation or update, saved-account refresh, and a guided React admin workflow.
- Added bounded Grok Web SSO-to-Build OAuth account import through trusted `x.ai` device endpoints, with proxy support and per-item results that never echo submitted SSO secrets. Conversion now runs with three workers by default (configurable up to eight), stable input-ordered aggregation, per-item timeouts, and a bounded batch deadline.
- Added current sub2api Grok OAuth import/export compatibility for the outbound `grok` platform and inbound `grok`/`xai` aliases, `rt`, rotated access/refresh tokens, RFC3339 expiry, OAuth client/team identity, subscription tier, and entitlement metadata.
- Account views now expose the non-secret credential type, so legacy refresh-token-only xAI OAuth accounts reopen in the correct admin workflow without exposing stored credentials.
- Added complete English, Chinese, French, Japanese, Russian, and Vietnamese translations for the Grok OAuth administrator workflow.
- Added pool-scoped xAI quota probe/read APIs with durable `runtime_options` snapshots, Free/paid billing observations, quota cooldown recovery, and account-table status/probe controls. Administrators can now explicitly clear local quota cooldown/exhaustion state, optionally reset the local request-quota window, and optionally force a post-reset xAI OAuth re-probe; the API/UI state clearly that this does not reset upstream quota.
- Added billing-backed xAI media eligibility so image generation/edit selection skips known-ineligible accounts while unknown eligibility and chat traffic remain unaffected.
- Added encrypted per-account base URL and header overrides for OpenAI, Anthropic, Gemini API/OAuth, Vertex service accounts, and xAI, with account-over-channel precedence, shared HTTPS/SSRF validation, platform-specific trusted hosts, dangerous-header denial, bounded input, and an explicit unsafe environment escape hatch. `grok_web` remains excluded because arbitrary overrides can invalidate its Cookie/Cloudflare session coupling.
- Added an administrator xAI OAuth reconciler that defaults to dry-run, previews missing/expired/near-expiry/rejected credential actions in the React UI, and applies refresh/expire operations with encrypted snapshot CAS guards.
- Added best-effort asynchronous xAI quota probes after OAuth account creation (including post-exchange create and successful SSO import items) plus a master-node periodic stale-snapshot sweep. Defaults are 15-minute ticks, 60-minute staleness, and 10 accounts per tick; `ACCOUNT_POOL_XAI_QUOTA_PROBE_INTERVAL_MINUTES`, `ACCOUNT_POOL_XAI_QUOTA_PROBE_STALE_MINUTES`, and `ACCOUNT_POOL_XAI_QUOTA_PROBE_MAX_PER_TICK` accept positive overrides.
- Added an apply-mode master-node xAI OAuth reconciliation sweep with immediate startup execution and a five-minute default interval (`ACCOUNT_POOL_XAI_OAUTH_RECONCILE_INTERVAL_MINUTES`). It refreshes missing/near-expiry access when a refresh token exists and expires only permanently unusable/rejected credentials.
- Added a portable `account_pool_worker_leases` table with TTL acquisition, heartbeat renewal, ownership-safe release, and expiry takeover. Both xAI quota-probe and OAuth-reconcile sweeps now require their distributed lease, preventing duplicate upstream maintenance across multiple master instances while retaining process-local overlap guards.
- Consume logs now record the selected account-pool pool/account IDs. `free_usage_24h_estimate` prefers exact Wynth-observed rolling usage from linked consume logs (`logs_24h`) and falls back to cumulative account counters (`counter_estimate`) for legacy/unlinked history; it remains read-only metadata and is never used for billing or scheduling.

### Fixed — Account Pool (号池)
- Account-level header overrides are now reapplied after channel parameter-override operations, Redis cooldown deletion failures are reported instead of being presented as a successful reset, worker lease expiry uses the shared database clock, and the SSO import batch deadline now reaches account persistence.
- xAI runtime and administrator refresh now honor the OAuth client ID captured during authorization and prefer the newest rotated refresh token in token state, while preserving default-client and stored-credential fallbacks for existing accounts.
- Administrator token refresh and missing-refresh expiration use encrypted credential/token-state compare-and-swap guards, preventing concurrent rotations from being overwritten or incorrectly retired.
- OAuth accounts with an expired access token and no refresh token are marked expired and removed from scheduling; channel-test requests remain mutation-free.
- xAI OAuth token failures now preserve typed status/code metadata without response-body leakage: permanent credential rejection expires the account, while network and 5xx failures use the existing temporary cooldown.

### Configuration — Account Pool (号池)
- `ACCOUNT_POOL_OUTBOUND_ALLOW_UNSAFE_BASE_URL=true` permits HTTP/private account base URLs for controlled development only; the legacy xAI-only option remains a compatibility alias for xAI pools.
- `ACCOUNT_POOL_WORKER_LEASE_TTL_SECONDS` defaults to 120 seconds and accepts 15–3600 seconds.
- `ACCOUNT_POOL_XAI_SSO_IMPORT_CONCURRENCY` defaults to 3 and is capped at 8; `ACCOUNT_POOL_XAI_SSO_IMPORT_TIMEOUT_SECONDS` defaults to 300 seconds and accepts 30–1800 seconds.

### Notes — Account Pool (号池)
- CPA `disable-cooling` is intentionally not mapped to Wynth's safety cooldown state, and exclusion-only model lists remain unrestricted because Wynth stores a positive supported-model policy. CPA gateway/server configuration, plugins, commercial/panel management, routing/session-affinity engines, and request-cloak behavior were not ported.
- Existing temporary-disable, overload, request-quota, per-model cooldown, affinity, retry, and lease scheduling already cover the corresponding newer sub2api pool-mode fixes.
- Periodic xAI workers still use the master-node convention, but database leases now coordinate any number of masters; credential/token-state CAS remains the final stale-write guard.
- Local quota reset does not and cannot hard-reset provider-side xAI quota. Log-backed rolling usage begins with newly linked consume logs (no backfill) and cannot observe traffic sent outside Wynth. DNS validation cannot fully eliminate post-validation rebinding by a malicious public hostname. `grok_web` outbound overrides and sub2api's Grok video/content-proxy architecture remain intentionally out of scope.

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
