# Upstream Source Login Through Cloudflare Turnstile — Design

- **Date:** 2026-07-02
- **Status:** Approved (design), pending implementation plan
- **Scope:** Issue #3 of the three-issue batch (CF login). Issues #2 (rules refactor) and #1 (i18n) are separate specs.
- **Branch:** `feat/upstream-source-cf-login`

## Problem

Some upstream sources are themselves new-api / sub2api gateways that put a **Cloudflare Turnstile** ("请验证您是真人") challenge on their `/login` page. The upstream-source sync subsystem logs into those gateways with an admin **email + password** over plain `net/http` to discover groups and mint API tokens. That automated login cannot pass Turnstile, so discover/sync fails with an opaque error.

Note: the **account pool** itself never does username/password gateway login (it uses API keys / OAuth refresh / Vertex SA), so this is exclusively an **upstream-source-sync** concern.

## Grounding facts (verified in code)

1. **`UpstreamSource.AuthConfig`** (`model/upstream_source.go:49`) is `gorm:"type:text"`, `json:"-"`, stored as **plaintext JSON**. No encryption hook.
2. Credentials are written by `marshalUpstreamSourceAuthConfig(email, password)` (`controller/upstream_source.go:418`) — stores only `{email, password}` and **intentionally drops** any cached login tokens on rotation.
3. **The adapters short-circuit login when a cached session is present:**
   - new-api `ensureManagementAuth` (`service/upstream_source_newapi.go:203`): if `access_token != "" && user_id > 0`, reuse; else `loginManagementAuth` (POST `/user/login` `{username,password}` → cookies → GET `/user/token` → `access_token`).
   - sub2api `ensureAccessToken` (`service/upstream_source_sub2api.go:200`): if `access_token != "" && expires_at > now`, reuse; else POST `/auth/login` `{email,password}` (Bearer). `requires_2fa` → `ErrUpstreamSource2FARequired`.
4. **The refreshed `source.AuthConfig` set in memory after login is NEVER persisted to the DB.** `service/upstream_source.go` has no write-back of `AuthConfig`. Therefore every discover/sync re-authenticates with email+password → hits Turnstile **every run**.
5. new-api login expects the Turnstile token as a **URL query param `turnstile`** on `/user/login` (proven by this fork's own `middleware/turnstile-check.go:26` `c.Query("turnstile")` and frontend `features/auth/api.ts:41` `/api/user/login?turnstile=${token}`). Upstream validates it server-side against `challenges.cloudflare.com/turnstile/v0/siteverify` and then caches `turnstile=true` in the session cookie. Turnstile tokens are single-use and short-lived (~300s).
6. When new-api's own Turnstile is enabled, a tokenless login returns **HTTP 200** with `{success:false, message:"Turnstile token 为空"}`. `isNewAPIAuthError` (`upstream_source_newapi.go:443`) does **not** match this, so there is no retry and the error is recorded verbatim in `LastDiscoveryError`/`LastSyncError`.
7. A CF **edge managed-challenge** returns an HTML interstitial; `decodeNewAPIResponseBody` → `common.Unmarshal` fails → `"decode upstream response failed: ..."`.
8. **`common.EncryptSecretString` / `DecryptSecretString`** (`common/secret_envelope.go`) — AES-256-GCM keyed on `CRYPTO_SECRET`/`SESSION_SECRET`, gated on `CryptoSecretStable`; returns `ErrUnstableSecretEnvelopeKey` when unset. Already used by the account pool.
9. **CF detection markers** already exist in `relay/channel/grokweb/failure.go`: `cloudflareMarkers = {cf_clearance, cf-ray, just a moment, cloudflare, attention required, challenge-platform}`, `isCloudflareChallenge(status, body)` matches 403/503 + marker.
10. Error strings are sanitized/truncated by `SanitizeUpstreamSourceError` (`upstream_source_sub2api.go:436`) before being stored/returned.

## Goals

- Let admins sync from upstream sources that gate login behind Cloudflare Turnstile, for **both new-api and sub2api** types.
- Two acquisition paths that converge on the same reusable session:
  1. **Headless browser (primary, best-effort):** drive the upstream's real web login through a sidecar Chrome via CDP, capture the session, extract the reusable token.
  2. **Manual session import (fallback, always available):** admin logs into the upstream in their own browser and imports the session; system replays it.
- **Persist** the acquired session (encrypted) so account/sync runs reuse it and only re-authenticate on expiry — cutting Turnstile hits from "every run" to "rarely."
- **Encrypt `AuthConfig` at rest** (reusing `secret_envelope`), fixing the current plaintext storage, with backward-compatible reads of existing plaintext rows.
- **Detect** Turnstile/CF blocks and surface a distinct, actionable status instead of an opaque decode/`Turnstile token 为空` error.

## Non-goals

- Solving Turnstile programmatically without a browser (no third-party CAPTCHA solver in this iteration).
- Bundling Chromium into the main application image (sidecar or BYO CDP only).
- Changing the account-pool runtime credential path (API key / OAuth / Vertex).
- Guaranteeing headless success against Cloudflare — it is explicitly best-effort; the manual import path is the reliable backstop.

## Architecture

### Core model: session acquisition chain

The reusable artifact is a cached upstream session:
- **new-api:** `access_token` + `user_id` (management API uses `Authorization: <token>` + `New-Api-User: <id>` headers).
- **sub2api:** `access_token` (JWT, Bearer) + `expires_at`.

`ensureManagementAuth` / `ensureAccessToken` are extended to resolve a session in this order, persisting the first success:

1. **Valid cached session** → use it (existing behavior).
2. **Headless browser** (only if a CDP endpoint is configured) → drive web login, capture session, extract token.
3. **Imported manual session** (only if the admin has imported one and it is still valid) → use it.
4. **Password direct login** (existing `net/http` flow) → succeeds only when the upstream has no Turnstile.

If all applicable steps fail and the failure is classified as Turnstile/CF, return a new sentinel `ErrUpstreamSourceTurnstileRequired`; the orchestration records a distinct status ("blocked by Cloudflare — import a session") and stops that source's sync, mirroring the existing `ErrUpstreamSource2FARequired` treatment.

> Ordering note: steps 2 and 3 both target Turnstile-gated sources. The headless attempt runs first (per the chosen strategy); a stored manual session is used when headless is unavailable/failed and the session is still valid. When neither a CDP endpoint is configured nor a manual session exists, behavior is exactly today's password login.

### 1. Session persistence (backbone)

Add `model.PersistUpstreamSourceAuthConfig(sourceID int, encrypted string) error` (a scoped `Updates` on `auth_config` + `updated_time`, dialect-safe GORM). After any adapter acquires or refreshes a session and the in-memory `source.AuthConfig` differs from what was loaded, the discover/sync orchestration writes it back (encrypted). This makes password login happen only on first use / expiry.

### 2. Centralized auth read/write + encryption

Introduce two helpers used by **all** `AuthConfig` access points (`controller/upstream_source.go`, `service/upstream_source_newapi.go`, `service/upstream_source_sub2api.go`):

- `readUpstreamSourceAuthRaw(stored string) (string, error)`: if `stored` parses as a `SecretEnvelope` (`v==1 && alg=="AES-256-GCM"`), `DecryptSecretString`; otherwise treat `stored` as legacy plaintext JSON and return as-is. This makes reads backward-compatible with existing plaintext rows.
- `writeUpstreamSourceAuthRaw(plaintextJSON string) (string, error)`: if `CryptoSecretStable`, `EncryptSecretString`; else return plaintext unchanged (so the feature still works when `CRYPTO_SECRET`/`SESSION_SECRET` is unset) and log a one-time warning. Existing plaintext rows are transparently upgraded to ciphertext the next time they are written (credential update or session persistence).

Adapters change from `common.UnmarshalJsonStr(source.AuthConfig, &cfg)` to unmarshaling `readUpstreamSourceAuthRaw(source.AuthConfig)`; write sites go through `writeUpstreamSourceAuthRaw`.

### 3. Headless browser component (sidecar + CDP)

New file `service/upstream_source_browser.go`:

- Config: `UPSTREAM_BROWSER_CDP_URL` (env / system setting), e.g. `ws://browserless:3000`. Empty ⇒ headless disabled (chain skips step 2).
- Uses `chromedp.NewRemoteAllocator(ctx, cdpURL)` to connect to the sidecar (no local Chromium in the app image).
- **SSRF guard:** validate `source.BaseURL` (and the login URL) with the existing `validateUpstreamSourceURL` before navigating.
- **Concurrency limit:** a small semaphore (browser sessions are heavy); default max 1–2 concurrent, configurable.
- **Timeout:** bounded per attempt (e.g. 60s).
- Flow: new tab → navigate to `{BaseURL}/login` → fill email/password → wait for the Turnstile widget to auto-solve (managed challenge may pass for a sufficiently real browser) → submit → wait for post-login state → capture cookies via CDP.
- **Per-type session extraction:**
  - new-api: replay captured cookies through the existing `net/http` client → `GET {AdminAPIBasePath}/user/token` → obtain `access_token`; obtain `user_id` from the login response / `/user/self`. Store `{access_token, user_id}`.
  - sub2api: evaluate page JS to read the JWT from `localStorage` (token key discovered from the running SPA); store `{access_token, expires_at}`.
- Result is persisted via the centralized write helper.
- **Explicit limitation, documented in code and UI:** Cloudflare frequently flags headless/browserless traffic; this path is best-effort and may fail, in which case the chain falls through to manual import.

### 4. Manual session import (reliable backstop, first-class)

New endpoint `POST /api/upstream_source/:id/session` with a type-aware request DTO:

- **new-api:** either a **session cookie string** (primary; backend replays it → `/user/token` → stores `access_token`+`user_id`) **or** an **access token + user id** pair (new-api exposes the access token in its user settings page, no devtools needed). Both supported; cookie is the primary/default affordance.
- **sub2api:** an **access token (JWT)**, with optional refresh token / expiry.

The handler **validates the imported session with a probe** (`DiscoverGroups`) before persisting; a failing probe returns an actionable error and does not overwrite stored credentials. On success it persists the session (encrypted) and clears any Turnstile-blocked status.

Frontend: in the upstream-source create/edit drawer's **Credentials** section, add an "Import session" affordance with type-specific helper text (how to copy the cookie / token from the browser). All new UI strings go through `t()` **and** are added to `en.json` + translated to `zh.json` (see issue #1 discipline; do not repeat the missing-key bug here).

### 5. CF / Turnstile detection and status

Add a classifier shared by both adapters (reuse the grokweb marker approach; keep the list local to the upstream-source package to avoid coupling):

- **new-api own Turnstile:** HTTP 200, `success:false`, message contains `turnstile` (case-insensitive) → `ErrUpstreamSourceTurnstileRequired`.
- **CF edge challenge:** decode failure or HTML body containing `cf-ray` / `just a moment` / `challenge-platform` / `cf_clearance` with status 403/503 → `ErrUpstreamSourceTurnstileRequired`.

The orchestration maps this sentinel to a distinct, sanitized status message (e.g. `"blocked by Cloudflare Turnstile; import a session"`), surfaced in `LastDiscoveryError` / `LastSyncError` and rendered in the frontend as a highlighted state with an "Import session" call to action — not the current opaque `decode failed` / `Turnstile token 为空`.

### 6. Config & deployment

- `docker-compose.yml`: add an **optional** `browserless/chrome` sidecar service (documented, commented, or profile-gated) exposing a CDP endpoint on the internal network.
- `UPSTREAM_BROWSER_CDP_URL` defaults to **empty** ⇒ zero extra deployment; the feature works via manual import + password login out of the box. Setting it enables the headless path.
- Main image stays lean (no Chromium).

## Data model / storage

No schema migration. `AuthConfig` remains the single `text` column; its **value** becomes either a `SecretEnvelope` (new) or legacy plaintext JSON (read-compatible). The JSON payload shape gains fields already partially present in the adapter structs:

- new-api: `{email, password, access_token, user_id, session_cookie?, session_source?, session_expires_at?}`
- sub2api: `{email, password, access_token, refresh_token, expires_at, session_source?}`

`session_source` records how the session was obtained (`password` | `browser` | `manual`) for diagnostics/UI. Password remains optional: a source may have only an imported session and no stored password.

## API changes

- **New:** `POST /api/upstream_source/:id/session` — import a manual session (type-aware DTO, probe-validated). Admin-guarded like the other upstream-source management routes.
- **Response:** `UpstreamSourceResponse` gains `session_source` and a boolean like `turnstile_blocked` (derived) so the UI can render state. `HasCredentials` stays but now also reflects an imported session (already true when `access_token != ""`).
- Existing credential-update route unchanged in shape; its write path now goes through the encryption helper.

## Security

- Encrypt `AuthConfig` at rest via `secret_envelope` (gated on `CryptoSecretStable`; graceful plaintext fallback with warning when unset).
- Continue masking email and never returning password/token in responses.
- Continue sanitizing errors via `SanitizeUpstreamSourceError` (its patterns already redact `cookie`, `token`, `authorization`, `password`).
- Headless navigation is SSRF-validated with the same fetch settings as `net/http`.

## Implementation phases (single PR, sequenced low-risk → high-risk)

1. **Backbone:** centralized encrypted read/write helpers + backward-compatible plaintext read + session persistence write-back. (Feature-invisible; reduces Turnstile hits immediately by caching tokens across runs.)
2. **Detection + sentinel + status:** classifier, `ErrUpstreamSourceTurnstileRequired`, distinct status, frontend state rendering.
3. **Manual import:** endpoint + probe validation + frontend "Import session" UI. **At this point the feature is fully usable** for Turnstile-gated sources.
4. **Headless browser:** sidecar config + `chromedp` acquisition + per-type extraction + docker-compose sidecar. (Enhancement; most failure-prone; last.)

## Testing

Backend table-driven tests (`testify`), state initialized in-fixture:
- Envelope round-trip and **legacy-plaintext read compatibility**; write falls back to plaintext when crypto unstable.
- CF/Turnstile classifier: new-api `{success:false,"Turnstile..."}` (200), CF edge HTML (403/503), normal error, normal success — exact sentinel/no-sentinel outcomes.
- Session persistence: after a simulated login, the encrypted `AuthConfig` is written back and reused on the next call (no second login).
- Manual import: per-type session parse + probe-validation success/failure paths (adapter HTTP stubbed).
- Headless: extractor/orchestration logic behind an interface, stubbed — no real browser in unit tests.

No fake fuzz/stress/timing tests; each test protects a real contract (encryption compatibility, block classification, token reuse, import validation).

## Decisions made

- **Strategy:** headless (primary, best-effort) + manual import (reliable fallback); both persist a reusable session. *(chosen)*
- **Coverage:** both new-api and sub2api. *(chosen)*
- **Encryption:** encrypt `AuthConfig` at rest, reusing `secret_envelope`, with plaintext read-compat and crypto-unset fallback. *(chosen)*
- **Browser deployment:** sidecar `browserless/chrome` via configurable CDP URL; nothing bundled. *(chosen)*
- **new-api manual artifact:** support **both** session cookie (primary) and access-token+user-id; sub2api uses JWT. *(chosen)*

## Risks & mitigations

- **Headless vs Cloudflare reliability (high):** likely to fail on strict CF configs. Mitigation: manual import is first-class and always available; headless is an optional accelerator.
- **sub2api JWT extraction is SPA-specific:** the localStorage key may vary between sub2api builds. Mitigation: make the extractor tolerant (probe several likely keys) and fall back to manual import; log clearly on failure.
- **`CRYPTO_SECRET` unset:** encryption unavailable. Mitigation: plaintext fallback preserves current behavior with a warning; no hard failure.
- **Sidecar operational surface:** an extra container. Mitigation: fully optional and off by default.
