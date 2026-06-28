# Design: Account-Pool Anthropic (Claude) Platform Support

- **Date:** 2026-06-27
- **Branch:** `sub2api-account-pool`
- **Status:** Approved (autonomous; user waived per-slice approval — goal: full multi-platform pool migration)
- **Goal context:** Add Anthropic (Claude) as a second pool platform alongside OpenAI/Codex. sub2api as behavioral reference only.

## 1. Background (from the multi-platform mapping)

Wynth's pool is OpenAI/Codex-only by five concrete couplings; everything else (scheduler, failure state machine, concurrency, affinity, quota, expiry) is already platform-neutral. Wynth **natively** forwards to Claude (`relay/channel/claude`, channel type `ChannelTypeAnthropic=14`, `https://api.anthropic.com/v1/messages`, `x-api-key`), but `ClaudeHelper` calls `rejectUnsupportedAccountPoolRuntime` → 503 when the channel has an active pool binding, and `validateAccountPoolRuntimeChannel` only allows OpenAI/Codex.

sub2api Claude reference: OAuth via `platform.claude.com/v1/oauth/token` (refresh: `grant_type=refresh_token`, `client_id=9d1c250a-e61b-44d9-88ed-5944d1962f5e`, UA `axios/1.13.6`); OAuth accounts forward with `Authorization: Bearer <access_token>` + a **claude-code mimicry bundle** (`anthropic-beta: claude-code-20250219,oauth-2025-04-20,interleaved-thinking-2025-05-14,...`, `User-Agent: claude-cli/...`, `X-Stainless-*`, `Anthropic-Dangerous-Direct-Browser-Access: true`); API-key accounts use `x-api-key` + a simpler beta header. 429 windows via `anthropic-ratelimit-unified-5h-reset`/`7d-reset` (unix; `>1e11`→/1000), `…-surpassed-threshold`/`…-utilization>=1.0`, `…-5h-status==rejected`; 7d wins if both exhausted; max-age guards (5h≤6h, 7d≤8d future); 429 with no reset header is **not** marked.

## 2. Scope & autonomous decisions

In scope (this design): pool **Claude OAuth and API-key accounts** and forward through Wynth's native claude adaptor, with Anthropic-correct auth headers, 429-window cooldowns, failure phrases, and session affinity.

Decisions (resolving the map's open questions, no user prompt):
- **Onboarding via import.** Accounts are added by importing credentials (OAuth `refresh_token`+`access_token`, or `api_key`) — the existing import path. The interactive claude.ai 3-step **login flow UI is deferred** (not needed at runtime).
- **Inject mimicry headers** (anthropic-beta/UA/X-Stainless) for OAuth accounts; **defer TLS-fingerprint impersonation** (heavy subsystem; headers are the primary requirement).
- **Skip the `antigravity` platform** (separate Google OAuth; not core Anthropic).
- **Reuse `rate_limited_until`** for the 5h/7d reset time; **defer** dedicated `session_window` pre-scheduling columns.
- Credential model unchanged (`AccountPoolCredentialConfig`/`AccountPoolTokenState` are already generic).

## 3. Architecture: a per-platform strategy seam

Introduce a small platform dispatch so each coupling becomes per-platform instead of forking the pool:
- **Platform constant:** add `model.AccountPoolPlatformAnthropic = "anthropic"`; `normalizeAccountPoolPlatform` accepts it.
- **Channel allowlist:** `validateAccountPoolRuntimeChannel` allows `ChannelTypeAnthropic` (14) (mapped to platform anthropic).
- **OAuth refresh dispatch:** the platform of the account's pool selects the refresh function. Today `accountPoolOAuthRefresh` is hardwired to `RefreshCodexOAuthTokenWithProxy`. Generalize to dispatch by platform: anthropic → a new `RefreshClaudeOAuthTokenWithProxy` (POST `platform.claude.com/v1/oauth/token`, `grant_type=refresh_token`, `client_id=9d1c…`). Thread the pool's `Platform` into `AccountPoolRuntimeCredentialRequest` and into `refreshAccountPoolRuntimeOAuthToken`.
- **Account identifier:** `accountPoolRuntimeAccountIdentifier` — for non-Codex platforms, use `selection.AccountIdentifier` (populated from the credential `Email`/stored uuid at import); do NOT call `ExtractCodexAccountIDFromJWT` for anthropic.
- **Failure classification (platform-aware):** thread the platform into `RecordAccountPoolRuntimeAttemptFailure` → `classifyAccountPoolFailure`. For anthropic: parse the `anthropic-ratelimit-unified-*` 5h/7d window headers (own reset logic, max-age guards, skip-if-absent); add Anthropic 400/permission phrases (`credit balance is too low`, `account is not active`). OpenAI/Codex behavior unchanged.
- **Relay injection (auth headers):** when the selected account is anthropic+OAuth, set `Authorization: Bearer <access_token>` + the mimicry bundle via `RuntimeHeadersOverride` (UseRuntimeHeadersOverride=true) and **suppress `x-api-key`**; anthropic+api-key uses the adaptor's existing `x-api-key` path. (Verify the override pipeline can delete/blank `x-api-key`; if not, add a minimal OAuth branch in the claude adaptor keyed off a runtime flag.)
- **Relay handler wrapping:** wrap `ClaudeHelper`'s core attempt in `runAccountPoolRuntimeAttempts` (mirror `TextHelper`); replace the `rejectUnsupportedAccountPoolRuntime("claude")` with the pool loop for bound channels.
- **Session affinity:** extend `BuildAccountPoolRuntimeAffinityKey` to derive a Claude session signal (metadata.user_id session id → ephemeral cache_control content hash → message digest), so stateful Claude sessions stick.
- **Capability probe (optional/deferred):** probe via `/v1/messages`; lower priority — capability detection can be disabled for anthropic initially.

## 4. Sub-slices (phased, each TDD + cross-model review)

- **A1 — Foundation/seams (backend, no relay-handler change):** platform constant + normalizer + channel allowlist; per-platform OAuth-refresh dispatch + `RefreshClaudeOAuthTokenWithProxy`; per-platform account-id; thread `Platform` through credential request + failure recorder (default openai → unchanged behavior). Tests: dispatch picks claude refresh for anthropic; normalizer/allowlist accept anthropic; account-id from email; openai path unchanged.
- **A2 — Anthropic failure classification:** 5h/7d window parsing + 400 phrases, platform-gated. Tests: classifier with anthropic headers → cooldown to the reset; no-header 429 → no mark; 7d-wins; phrase→expire.
- **A3 — Relay wrapping + OAuth header injection:** wrap ClaudeHelper; OAuth Bearer + mimicry + suppress x-api-key; api-key path intact. Tests: pooled claude request runs through the pool loop with correct auth headers (OAuth vs api-key).
- **A4 — Claude session affinity:** session-hash signal for Claude. Tests: same session → same account.
- **A5 — Frontend:** platform selector (anthropic) + Claude account fields (OAuth refresh_token / api_key) in the pool UI.
- **A6 — Capability probe for anthropic (optional):** /v1/messages probe.

## 5. Risks
- **OAuth header injection** (Bearer + suppress x-api-key + mimicry) is the key integration risk — verify the `RuntimeHeadersOverride`/DoApiRequest pipeline can override/remove the adaptor's `x-api-key`; otherwise add a small OAuth branch in the claude adaptor.
- **Anthropic third-party classification:** without TLS-fingerprint mimicry, OAuth accounts may be flagged by Anthropic. Headers cover most of it; TLS fingerprinting is a documented follow-up if needed.
- Threading `Platform` through the credential/failure paths touches shared signatures — keep openai default behavior byte-identical (regression net: existing pool tests).
