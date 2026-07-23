# Changelog

All notable changes to the **Wynth** fork (`GenistarWynth/wynth-api`) are documented here.

Wynth is a downstream fork of [New API](https://github.com/QuantumNous/new-api) by QuantumNous. This changelog records what Wynth adds or changes on top of upstream; upstream New API features are not re-listed.

## [Unreleased]

## [v1.0.0-rc.57] - 2026-07-23

### Fixed
- Auto-priority now evaluates each score cohort atomically from the complete eligible set (enabled plus temporarily auto-disabled channels), so price floor/ceil and normalization no longer drift when only a due subset is claimed.
- Temporarily auto-disabled members continue to contribute to cohort pricing, keep hard-unavailable priority behavior, and receive refreshed score snapshots instead of remaining indefinitely on stale v2 data.
- Compatibility auto-priority paths now select groups first and then evaluate complete cohorts, preventing staggered last-run timestamps from producing inverted price scores such as 0.02 scoring below 0.06.
- Auto-priority snapshot v4 records `cohort_floor`, `cohort_ceil`, and `cohort_member_count` so UI/tooltips can explain the shared pricing basis without reading channel names.

## [v1.0.0-rc.56] - 2026-07-23

### Fixed
- Auto-priority now reads generated-channel pricing from the current upstream mapping instead of parsing channel names, while manual channels continue to use their configured rate source.
- Generated channel names and system-managed rate remarks now update together during discovery, monitor collection, normal and automatic sync, and rule reapplication, repairing historical label drift even when the upstream rate has not changed.
- Rate-label repair preserves source ownership checks, stale-without-delete behavior, administrator custom remarks, and the v1.0.0-rc.55 scoring rules.

## [v1.0.0-rc.55] - 2026-07-23

### Fixed
- Generated upstream channel rate labels now track the current mapping rate, while safe source ownership checks repair historical label drift without renaming unowned channels.
- Auto-priority now scores nominal price (75%) and cache (10%) as separate dimensions alongside availability (8%), time to first token (3%), and throughput (4%).
- Price scoring and 8x hard dominance now use nominal rate only, so cache behavior cannot appear as a cheaper price or bypass hard price dominance.
- Cache scoring defaults channels with no own samples to exactly 95/100 (`default_95`), blends 1–19 own samples linearly with `count / 20` confidence, and uses own data completely at 20 or more samples; same-cohort peer medians no longer alter the zero-sample default.
- Auto-priority snapshot v3 diagnostics expose nominal rate and price separately from cache score, source, prior, and confidence, while preserving decoding of older snapshots.

## [v1.0.0-rc.54] - 2026-07-23

### Added
- Upstream-source discovery and monitoring now persist scan records and per-group change ledgers, with admin APIs for recent runs, mapping changes, and collector outcomes.
- Upstream sources now track session and authentication health and support opt-in scheduled monitoring with isolated claims, bounded concurrency and timeouts, stale-run recovery, and collection of balances, costs, rate groups, announcements, and subscription usage from supported New API and Sub2API providers.
- Monitor notification subscriptions support source, event, and group filters, durable cooldowns, delivery history, and automatic retention cleanup for monitoring history.
- The localized admin UI adds a source-monitoring sheet with authentication status, monitor controls, current balance and subscription snapshots, recent runs, group changes, and announcements.

## [v1.0.0-rc.53] - 2026-07-22

### Fixed
- Upstream-source edits now persist provider type changes and transactionally refresh generated channels and their runtime cache with source-owned connection fields; refresh failures roll back the source update and unknown source types return an explicit validation error, while channel- and mapping-owned fields remain unchanged.

## [v1.0.0-rc.52] - 2026-07-21

### Fixed
- OpenAI-type channels using `client_identity_preset=codex_cli` now send strict Codex-compatible Responses upstreams complete Codex CLI request semantics, including typed `input_text` channel tests and missing Codex defaults, while preserving non-Codex behavior and explicit caller choices except enforcing `store=false`.

## [v1.0.0-rc.51] - 2026-07-21

### Changed
- Auto-priority introduced cache-factor source, prior, and own-sample confidence diagnostics plus continuous blending across the first 20 own usage samples; the current fixed cold-start prior is documented under Unreleased.
- Auto-priority snapshot v2 now records cache-factor source, prior, and own-sample confidence diagnostics while preserving existing scoring weights, dominance and usability gates, smoothing, hysteresis, and group scheduling behavior.

## [v1.0.0-rc.50] - 2026-07-21

### Added
- Channels can add retry status codes, auto-disable status codes, and case-insensitive auto-disable failure keywords through the localized channel editor and persisted channel settings.

### Changed
- Effective channel retry and auto-disable rules are normalized unions with the global settings, while empty channel values preserve the existing global behavior.

## [v1.0.0-rc.49] - 2026-07-21

### Changed
- Ordinary and manual channel tests now default to streaming; the localized **Non-stream Mode** toggle provides an explicit opt-in to `stream=false`, which remains supported through the backend query parameter.

### Fixed
- Stream-incompatible endpoints now force non-stream testing and show the toggle in the semantically correct enabled-state/disabled-control presentation.

## [v1.0.0-rc.48] - 2026-07-21

### Fixed
- New API upstream token creation now supports customized deployments such as 4Router by preferring usable list/search `token.key` secrets, rejecting masked values, restoring a missing `sk-` prefix, and retaining the stock `/token/{id}/key` fallback.

## [v1.0.0-rc.47] - 2026-07-21

### Fixed
- Sub2API upstream re-sync no longer fails when customized upstreams (e.g. lcodex) require `config_version` for API key updates: matching existing keys skip no-op UpdateKey, empty upstream secrets no longer wipe local channel keys, and UpdateKey retries with fetched/default `config_version` when required.

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
