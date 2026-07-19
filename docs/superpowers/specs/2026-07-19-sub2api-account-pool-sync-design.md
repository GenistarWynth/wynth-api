# sub2api Account-Pool Updates -> Wynth Design

- Date: 2026-07-19
- Target branch: `feat/sub2api-account-pool-sync-2026-07`
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

## Defer

- Proactive Grok OAuth reconciliation/background refresh: Wynth refreshes on demand with singleflight and encrypted CAS persistence. A new background reconciler needs lifecycle/config/HA design.
- Full xAI billing snapshot, rolling Free estimate, and quota-reset UI: valuable but larger than OAuth login and needs a Wynth-native durable schema and probe policy.
- Paid-media quarantine until that probe exists. Inferring paid eligibility from absent JWT claims would incorrectly exclude valid accounts.
- Per-account xAI base URL and header overrides: Wynth is channel-first, and its channel already owns base URL/header policy. Adding account overrides needs an explicit precedence and SSRF/header-denylist design.
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

The remaining Phase 2 candidates were deliberately not force-fitted:

- Normal selection and lease acquisition already share `loadAccountPoolSelectionContext`, so status, request quota, global/temp/overload cooldowns, runtime blocks, supported models, mapping, and per-model cooldown are applied consistently.
- xAI quota readiness and Free-tier rolling estimates need an authoritative Wynth quota probe and durable snapshot schema; generic capability detection is not an equivalent data source.
- Paid-media eligibility cannot be inferred safely from token claims. Wynth's xAI image relay has no equivalent of sub2api's billing-backed eligibility snapshot, and the sub2api video/cache architecture remains out of scope.
- Per-account base URLs and header overrides conflict with Wynth's channel-owned upstream policy and require an explicit precedence, SSRF, redirect, and denied-header design.
- Permanent `invalid_grant` classification and proactive reconciliation need a typed, secret-safe OAuth error contract plus multi-worker refresh-race recovery; the shipped missing-refresh invariant is deterministic without introducing that larger lifecycle.

## Error handling and security

- Authorization and token endpoints are fixed to `auth.x.ai`; no request may supply an outbound OAuth endpoint.
- Redirect URIs reject credentials, fragments, and non-HTTP(S) schemes; plain HTTP is limited to loopback hosts.
- Session IDs, state, verifier, tokens, and cookies are generated/handled server-side and never logged or included in audit metadata.
- Session state is constant-time compared and consumed on exchange success or failure.
- Token and SSO HTTP bodies are size-limited. Non-2xx errors expose status and a bounded sanitized message, never submitted credentials.
- Proxy selection uses existing pool/account proxy resolution and validation.
- SSO redirects stay on `x.ai` subdomains and are capped.

## Testing and acceptance

- Service tests cover PKCE parameters, callback parsing, session expiry/single use, state mismatch, proxy binding, token form contracts, refresh rotation, claims, redirect validation, SSO normalization/device flow, and import aliases.
- Service tests cover invalid-state rejection and encrypted account refresh persistence; controller tests cover pool platform validation, success envelopes, and SSO import secret redaction with fakes/httptest only.
- Frontend tests cover form-state application and serialization of exchanged credentials; the typed API paths are covered by TypeScript checking and the production build.
- Required verification: focused Go packages, account-pool test repeats, frontend typecheck/targeted tests/lint/build, secret scan, and changelog review.
