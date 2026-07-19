# sub2api Account-Pool Updates -> Wynth Design

- Date: 2026-07-19
- Phase 1 branch: `feat/sub2api-account-pool-sync-2026-07`
- Deferred account-pool branch: `feat/sub2api-account-pool-deferred-2026-07`
- Reference: `/root/work/sub2api` at `d4b9797f` (`v0.1.161`)
- Comparison baseline: the 2026-06-27 migration summarized in `docs/superpowers/specs/2026-06-27-account-pool-migration-summary.md`
- Scope: account-pool behavior only. Wynth's channel/relay architecture remains authoritative.

## Baseline

The target started clean apart from the user's untracked `.codegraph/` index. Before implementation, these packages passed:

```text
go test ./model ./service ./controller ./relay ./middleware ./types
```

Wynth already supports OpenAI, Anthropic, Gemini, Vertex service accounts, xAI, and `grok_web` account pools. It already has encrypted credential and token-state storage, proxy-aware OAuth refresh, single-query scheduling, in-process runtime blocks, per-model rate limits, request quotas, expiry pausing, affinity, pool retry, and React account-pool administration.

## sub2api delta inventory since 2026-06-27

### Grok/xAI OAuth administration

Relevant behavior and representative commits:

- PKCE authorization URL, state/session validation, code exchange, refresh-token validation, account refresh, and manual callback parsing are represented by the current `backend/internal/pkg/xai/oauth.go`, `grok_oauth_service.go`, `grok_oauth_client.go`, and `grok_oauth_handler.go`.
- `ad4bf5c6`: converts Grok Web SSO cookies to Build OAuth tokens with the xAI device flow and supports bounded batch import.
- `81b8b783`, `34339005`, `b32b815e`, `3a3b962d`: preserve imported upstream OAuth tokens, normalize refresh failures, and make credential recovery/failover safer.
- `a13a6113`, `6b259004`: add explicit account refresh and proactive reconciliation.
- `7f5d067a`, `22158140`, `e8e360c8`, `eb2b8632`: add custom xAI upstream endpoints/header overrides and validate unsafe URL components.

Current Wynth mapping:

- Refresh exists in `service/xai_oauth.go` and is selected by `service/account_pool_token_provider.go`.
- Encryption-at-rest and optimistic token updates exist in `service/account_pool_config.go`, `service/account_pool_service.go`, and `service/account_pool_token_provider.go`.
- Account CRUD/import routes exist in `controller/account_pool.go` and `router/api-router.go`.
- The React form in `web/default/src/features/account-pools/index.tsx` only supports pasted xAI tokens.
- Missing: PKCE session/auth URL, code exchange, account refresh API, SSO-to-OAuth conversion/import, and the guided React flow.

### Grok quota readiness and Free-plan behavior

Relevant commits:

- `1a0a6ea9`, `c896cacf`, `30d4301b`: stabilize the probe model, combine usage/billing observations, and estimate rolling 24-hour Free usage.
- `a1b5c75c`: probe newly imported OAuth accounts asynchronously.
- `1dedb209`: persist exhausted quota as a scheduling cooldown.
- `08ea2942`, `dd7a2b22`: recognize probed Free accounts and align Free probes with health checks.

Current Wynth mapping:

- Generic capability probing exists in `service/account_pool_capability*.go` and the account-pool admin UI.
- Request quota and upstream failure cooldowns already affect `model.AccountPoolAccount.IsSchedulableAt` and `service/account_pool_scheduler.go`.
- Wynth has no xAI billing/quota snapshot model or admin quota probe endpoint.

### Grok media eligibility and quarantine

Relevant commits:

- `e8606315`: allow OAuth media only when paid eligibility is positively known.
- `8bc0a277`, `d2ac6b2c`: quarantine ineligible accounts and harden unknown/error transitions.
- `eaf06917`: preserve media eligibility in scheduler cache.
- `335edde9` and later video-content commits: apply mapping and proxy protected video output through the chosen upstream account.

Current Wynth mapping:

- `relay/channel/xai` supports xAI image generation and account-pool runtime selection.
- Wynth does not have sub2api's Grok composer/video gateway, signed-video content proxy, or scheduler snapshot architecture.

### Scheduling and temporary unschedulability

Relevant commits:

- `1f7b7b91`: honor temporary unschedulable rules in pool mode.
- `ef2a22be`, `40b8f04a`: scope transient cooldowns by model.
- `2b462b07`: preserve configured Grok OAuth concurrency.
- `2a3dcb49`, `9b75c7b7`, `d2b080e8`: correct OpenAI OAuth/API-key model scheduling.

Current Wynth mapping:

- Wynth already filters status, global cooldown, temporary disable, overload, expiry, request quota, runtime block, supported models, model mapping, and per-model cooldown before every normal or lease-based pool selection in `service/account_pool_scheduler.go`.
- Explicit zero concurrency is preserved by pointer-backed DTOs and `MaxConcurrencySet` through create/import.
- These sub2api fixes are already behaviorally covered by Wynth's independent scheduler design; no gateway changes are required.

### Import/export shapes

Relevant commits:

- `ad4bf5c6`: Web SSO -> Build OAuth batch import.
- `83455a3f`: harden batch import input.
- `6bd248fd`, `a5638a4e`: avoid destructive Codex access-only merges and match identity more precisely.

Current Wynth mapping:

- `service/account_pool_import.go` and `service/account_pool_export.go` already support sub2api/CPA shapes, proxy deduplication, dry run, redaction, and encrypted persistence.
- The newer Grok shapes need aliases for `grok`/`xai`, `rt`, `refresh_token`, `access_token`, `id_token`, `client_id`, token expiry, and SSO cookie input.

### Other platform fixes considered

- Anthropic Fable 7-day model cooldown (`b3f79697`) is already represented more generally by Wynth's per-model pool cooldown support.
- Gemini/Antigravity normalization and OAuth 401 recovery (`df2cedee`, `d0a1443a`) are sub2api gateway-specific; Wynth already has separate Gemini OAuth refresh and normalized model policies.
- OpenAI session-import identity fixes (`6bd248fd`, `a5638a4e`) do not map directly to Wynth's export/import contract and must not introduce sub2api account merging semantics.

## Adopt now

### Phase 1: Grok OAuth login, refresh, and import

1. Extend `service/xai_oauth.go` with fixed, validated xAI endpoints, PKCE helpers, callback parsing, a 30-minute in-memory single-use session store, authorization generation, code exchange, refresh-token rotation preservation, JWT claim extraction, and a response adapter that produces Wynth's `AccountPoolCredentialConfig` and `AccountPoolTokenState`.
2. Bind each OAuth session to its redirect URI and resolved account-pool proxy. A code exchange may not replace those values, preventing a caller from switching transport or redirect context after authorization starts.
3. Add account-pool admin routes:
   - `POST /api/account_pools/:id/xai/oauth/authorize`
   - `POST /api/account_pools/:id/xai/oauth/exchange`
   - `POST /api/account_pools/:id/accounts/:account_id/xai/oauth/refresh`
   - `POST /api/account_pools/:id/accounts/xai/sso_import`
4. The exchange response returns profile metadata plus exact Wynth `credential` and `token_state` payloads. The existing create/update account APIs remain the sole CRUD and encryption boundary.
5. The account refresh endpoint decrypts the stored account, refreshes through the resolved account/default proxy, preserves a non-rotated refresh token, and uses the existing encrypted update path.
6. The SSO import endpoint normalizes a raw token or Cookie header, performs the trusted xAI device flow through the selected proxy, and creates accounts through `AccountPoolService.CreateAccount`. Imports are bounded and report per-item success/failure without returning SSO secrets.
7. Extend the existing sub2api importer with Grok OAuth aliases without changing OpenAI/Anthropic/Gemini/Vertex/grok_web behavior.
8. Add a guided xAI-only OAuth panel to the existing React account form: start authorization, open the URL, paste the callback URL or code, exchange it, prefill account name/identifier/token fields, and submit through the existing create/update mutation. Keep pasted-token inputs available.
9. Add an explicit refresh action for existing xAI OAuth accounts. Add all new UI strings to all six locale files.

### Phase 2 candidates that are safe after Phase 1

- Reuse existing capability detection after OAuth import instead of introducing sub2api's async scheduler snapshot worker.
- Improve xAI OAuth credential error classification only where Wynth's existing failure state machine lacks a concrete distinction.
- Add media eligibility only if a small, testable xAI billing probe can feed Wynth scheduling without importing the Grok gateway/video model.

## Adapt

- Ent repositories become GORM service/controller operations compatible with SQLite, MySQL, and PostgreSQL.
- Vue workflows become React 19/Base UI interactions inside `web/default/src/features/account-pools`.
- sub2api account objects become Wynth `AccountPoolCredentialConfig` and `AccountPoolTokenState`; secrets remain encrypted by existing helpers.
- sub2api's global Grok admin routes become pool-scoped routes so proxy defaults and platform validation are unambiguous.
- sub2api's account creation endpoint becomes an exchange payload compatible with existing Wynth create/update endpoints, avoiding duplicate validation and encryption code.
- Scheduler cache fixes map to Wynth's DB-backed selection and in-process runtime block; they are verified rather than copied.

## Defer after the account-pool Phase 2 slice

- Automatic import-time quota probes, a periodic OAuth reconciliation worker, and a multi-replica background sweep remain deferred pending lifecycle/config/HA design. The administrator-triggered probe and reconciler are the first operational slice.
- A locally estimated rolling 24-hour Free usage window and a dedicated quota-reset editor remain deferred. Wynth now stores authoritative billing/usage observations, but does not invent quota from token claims or incomplete history.
- Account outbound overrides are intentionally xAI-only. Extending them to OpenAI, Anthropic, Gemini, Vertex, or `grok_web` requires platform-specific review instead of assuming identical authorization/header semantics.
- SSO import concurrency above one: the upstream flow is sensitive and long-running. The first Wynth slice uses a bounded sequential batch; parallelism can follow measured need.

## Out of scope

- sub2api reverse-proxy/gateway architecture, Ent schema, Vue frontend, payments, generic billing, or scheduler snapshot/outbox.
- Grok composer/video generation, edits/extensions, signed-video same-origin proxying, media pricing, or request-owner content authorization.
- Wholesale OpenAI/Claude/Gemini gateway changes, upstream-source sync, channel auto-priority, or channel foundation replacement.

## Implementation status

Phase 1 is complete on `feat/sub2api-account-pool-sync-2026-07`:

- `service/xai_oauth.go` now provides validated loopback/HTTPS redirects, PKCE authorization, a 30-minute single-use session store, callback URL/query/bare-code parsing, proxy-bound code exchange, claim extraction, client-aware refresh, and Wynth credential/token-state conversion.
- `service/xai_sso_oauth.go` implements the trusted, redirect-capped, body-limited xAI device flow used by bounded SSO-to-OAuth import.
- Pool-scoped Gin routes expose authorize, exchange, saved-account refresh, and SSO import operations. Existing account create/update remains the only CRUD and encryption boundary.
- The sub2api importer accepts the current `platform: grok` OAuth export shape, the `rt` alias, RFC3339 expiry, and xAI identity/entitlement metadata. xAI exports use sub2api's `grok` platform name, preserve that metadata, and continue to redact ID/access/refresh tokens by default.
- The React account editor contains an xAI-only OAuth panel. It opens the authorization page, accepts a complete callback URL or bare code, fills the existing account form without browser persistence, retains manual token entry as a fallback, and refreshes saved accounts in place.
- Account list responses expose only the non-secret credential type, allowing refresh-token-only legacy xAI accounts to reopen in the OAuth editor while credential decryption failures remain visible for administrative recovery.
- All new administrator copy is translated in `en`, `zh`, `fr`, `ja`, `ru`, and `vi` using the repository i18n scripts.

The Phase 2 audit produced two narrow fixes:

- Runtime and administrator xAI refresh use the client ID stored with the encrypted credential. Administrator refresh prefers the latest token-state refresh token, so a prior runtime rotation cannot leave it using an invalid stale token.
- Administrator refresh persists rotations with a credential/token-state compare-and-swap guard. A concurrent runtime or administrator winner cannot be overwritten by an older response.
- An OAuth account whose access token is expired and whose latest database state has no refresh token is marked `expired`, so it cannot fail every request indefinitely. The expiration update matches the loaded encrypted credential and token state, so a concurrent rotation wins safely; channel-test requests remain mutation-free.

The deferred account-pool Phase 2 slice is complete on `feat/sub2api-account-pool-deferred-2026-07`:

- **Quota readiness and Free-tier probe:** pool-scoped manual probe/read APIs use the existing token provider and proxy resolution, store a sanitized snapshot under the account's existing `runtime_options.xai_quota`, and map observed exhaustion into `rate_limited_until`. Recovery clears only that quota cooldown. The React account table shows the last observation and exposes a probe action for xAI OAuth accounts.
- **Paid-media eligibility:** the billing probe stores a tri-state `media_eligible` observation. xAI image generation/edit selection skips only accounts with known-false eligibility; unknown remains schedulable and chat selection is unchanged. This reuses the shared selection context rather than adding a second scheduler or global account quarantine.
- **Safe per-account outbound overrides:** encrypted xAI credential config may contain an optional `base_url`, `header_override_enabled`, and bounded header map. A validated account value wins over the channel base URL/header of the same name; otherwise the channel remains authoritative. HTTPS/public-address validation, trusted `x.ai` handling, the explicit `ACCOUNT_POOL_XAI_ALLOW_UNSAFE_BASE_URL=true` escape hatch, header count/size limits, and credential/hop-by-hop header deny rules are enforced both on admin writes and at runtime.
- **Typed credential rejection and reconciliation:** xAI token failures carry only status/code metadata. `invalid_grant`, `invalid_refresh_token`, `token_expired`, and `session_terminated` expire the account with an encrypted credential/token-state CAS; network/5xx failures retain the existing temporary cooldown behavior. `POST /api/account_pools/:id/xai/oauth/reconcile` defaults to dry-run, scans missing/expired/near-expiry/rejected OAuth state, and applies refresh-or-expire actions only if the encrypted snapshot is still current. The React UI always previews the dry-run result before apply.

The following boundaries remain intentional:

- Normal selection and lease acquisition continue to share `loadAccountPoolSelectionContext`; no sub2api scheduler cache/outbox was introduced.
- No Grok composer/video generation, signed-video content proxy, or request-owner media gateway was ported.
- No JWT-claim-only paid eligibility inference, automatic import-time quota probe, rolling local Free estimate, or background reconciliation worker was added.
- OpenAI, Anthropic, Gemini, Vertex, and `grok_web` outbound behavior does not consume xAI account overrides or media eligibility.

## Error handling and security

- Authorization and token endpoints are fixed to `auth.x.ai`; no request may supply an outbound OAuth endpoint.
- Redirect URIs reject credentials, fragments, and non-HTTP(S) schemes; plain HTTP is limited to loopback hosts.
- Session IDs, state, verifier, tokens, and cookies are generated/handled server-side and never logged or included in audit metadata.
- Session state is constant-time compared and consumed on exchange success or failure.
- Token and SSO HTTP bodies are size-limited. xAI OAuth token failures expose only typed status/code metadata, never response descriptions, bodies, or submitted credentials.
- xAI account base URLs require HTTPS and a public resolved address unless the explicit unsafe environment override is enabled; credentials, fragments, dangerous transport/authentication headers, and oversized header maps are rejected.
- Proxy selection uses existing pool/account proxy resolution and validation.
- SSO redirects stay on `x.ai` subdomains and are capped.

## Testing and acceptance

- Service tests cover PKCE parameters, callback parsing, session expiry/single use, state mismatch, proxy binding, token form contracts, refresh rotation, claims, redirect validation, SSO normalization/device flow, and import aliases.
- Service tests cover invalid-state rejection and encrypted account refresh persistence; controller tests cover pool platform validation, success envelopes, and SSO import secret redaction with fakes/httptest only.
- Service/controller tests additionally cover billing and active-probe snapshots, eligibility scheduling, outbound override validation/precedence, typed OAuth failure classification, dry-run/apply reconciliation, and concurrent CAS winners using httptest fakes only.
- Frontend tests cover form-state application, xAI override serialization, and quota presentation; reconciler API/UI paths are covered by TypeScript checking, focused lint, and six-locale i18n validation.
- Required verification: focused Go packages, account-pool test repeats, frontend typecheck/targeted tests/lint/build, secret scan, and changelog review.

## Remaining deferred account-pool slice

The final deferred slice keeps the existing account-pool architecture and does not add a second scheduler, a new log schema, or provider behavior outside xAI OAuth.

### Automatic and periodic quota observation

- `AccountPoolService.CreateAccount` remains the only account persistence and encryption boundary. After it successfully builds an xAI OAuth account view, it submits a best-effort probe to `gopool`; the probe is enabled during normal worker startup, never delays or changes the create result, and therefore covers normal OAuth creation, the post-exchange create request, and every successful SSO import item without duplicate call-site hooks.
- A master-node worker performs an immediate sweep and periodic sweeps. It loads enabled xAI pools and enabled accounts, decrypts only enough credential metadata to retain OAuth accounts, orders missing/old snapshots by age, and probes at most the configured number per tick through the existing proxy-aware `ProbeXAIQuota` path.
- Defaults are a 15-minute interval, a 60-minute stale age, and 10 accounts per tick. `ACCOUNT_POOL_XAI_QUOTA_PROBE_INTERVAL_MINUTES`, `ACCOUNT_POOL_XAI_QUOTA_PROBE_STALE_MINUTES`, and `ACCOUNT_POOL_XAI_QUOTA_PROBE_MAX_PER_TICK` override positive values. Exhaustion and recovery continue to use the existing `rate_limited_until` persistence logic.

### Background OAuth reconciliation

- A separate master-node worker performs an immediate sweep and then runs every five minutes by default. `ACCOUNT_POOL_XAI_OAUTH_RECONCILE_INTERVAL_MINUTES` overrides the interval with a positive value.
- Each sweep lists enabled xAI pools and invokes the existing reconciler with `dry_run=false`. Missing or near-expiry access is refreshed only when a refresh token exists. A missing refresh token expires an account only when access is also missing or expired; a still-valid access-only account remains usable until expiry. Typed permanent credential rejection still expires through the existing encrypted credential/token-state compare-and-swap.
- `sync.Once` controls lifecycle and `atomic.Bool` skips overlapping ticks in one process. `NODE_TYPE=slave` nodes do not start periodic sweeps. Across instances, the reconciler's credential/token-state CAS makes duplicate refresh/expire writers safe: one update wins and stale snapshots cannot overwrite it. The repository has no reusable distributed worker lease; deployments should designate one master to avoid duplicate upstream refresh calls, while CAS preserves database correctness if multiple masters are configured.
- The administrator endpoint and React preview remain dry-run-first; only the background worker defaults to apply.

### Read-only rolling 24-hour Free usage estimate

- Wynth's consume logs do not store account-pool account IDs, while `account_pool_accounts` already stores cumulative successful request and token counters. Adding an indexed account ID to the large log table would be a new cross-database migration and write-path contract, so it is intentionally excluded from this slice.
- Known Free-tier quota snapshots are enriched at read/probe time with `free_usage_24h_estimate`. Accounts younger than 24 hours report the locally observed cumulative counters since account creation. Older accounts with recent activity report a 24-hour lifetime-average projection; accounts whose last successful use predates the window report zero. The object includes the source, window/coverage seconds, request and token counts, and an `estimated` flag.
- The estimate is never used for scheduling, cooldowns, billing, settlement, or quota resets, and it is not persisted into the durable upstream snapshot. It measures Wynth-observed account-pool traffic only and cannot reconstruct requests made outside Wynth or exact historical bursts before per-request account-linked logs exist.

### Alternatives considered

- Adding `account_pool_account_id` to every consume log would permit exact rolling aggregation but requires a large-table migration, new indexes, ClickHouse parity, and all text/media/task log writers to preserve the identifier.
- Persisting hourly buckets inside `runtime_options` avoids a schema change but turns every request into a JSON read/modify/write that can contend with quota snapshots and lose concurrent buckets without a new transactional data model.
- A system-task or Redis lease could avoid duplicate work across multiple masters, but no reusable lease exists in this repository. The selected master-node/atomic/CAS design keeps persistence safe and does not invent a one-off distributed lock protocol.
