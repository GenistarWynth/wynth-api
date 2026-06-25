# Design: Admin/Root Management of Other Users' API Keys (Tokens)

- **Date:** 2026-06-25
- **Status:** Approved design (pending spec review) → implementation plan next
- **Reviewers:** User (product decisions), Codex (read-only design review, session `019eff02-8c16-7fc2-be41-cf6fed629416`)

## 1. Summary

Today, API key (token) management is strictly self-scoped: every handler in `controller/token.go`
binds `userId := c.GetInt("id")`, and every model function enforces `WHERE user_id = ?`. There is no
path for an administrator to view or manage another user's tokens (`model/token.go:363` even comments
*"Why we need userId here? In case user want to delete other's token."*).

This feature adds an **admin dashboard capability** for administrators to manage the API tokens of the
users they are responsible for — mirroring the existing admin features that already manage users, OAuth
bindings, subscriptions, passkeys, and 2FA. Revealing a token's **full plaintext value** is restricted
to **root only**, behind step-up verification, matching the existing channel-key-reveal pattern.

## 2. Goals / Non-Goals

### Goals
- Admins (role ≥ 10) can, for users they may manage: list, view (masked), search, create-on-behalf,
  enable/disable, edit (quota/unlimited, group, expiry, model limits, IP allowlist, name), delete
  (single + batch) another user's tokens.
- **Root only** can reveal another user's full plaintext token value, behind step-up verification.
- Full reuse of existing, already-`userId`-parameterized model functions (no rewrite of the
  security-sensitive query layer).
- React admin UI integrated into the existing Users feature.

### Non-Goals
- No change to self-service token endpoints (`/api/token/*`) or their semantics.
- No new billing/quota mechanics; admin quota edits are token-level only (see §6).
- No change to the role model or `canManageTargetRole` semantics.

## 3. Permission Model

Roles (`common/constants.go:187`): Guest=0, Common=1, Admin=10, Root=100.
Hierarchy helper (`controller/user.go:317`): `canManageTargetRole(myRole, targetRole) = (myRole == RoleRootUser || myRole > targetRole)`.

Every admin token handler performs, **before any token access**:
1. `targetUserId := <:id route param>`; `myRole := c.GetInt("role")`.
2. `targetUser, err := model.GetUserById(targetUserId, false)` (404 if missing).
3. Guard: `if !canManageTargetRole(myRole, targetUser.Role) { 403 }`
   (admin manages strictly-lower roles; root manages anyone). This matches the existing per-user admin
   handlers for users / OAuth bindings / 2FA / passkeys.
4. **Reveal-plaintext only:** additionally require `myRole == common.RoleRootUser` (enforced by the
   `RootAuth()` middleware on that route; see §5). Admins can manage but never see plaintext.

## 4. Model Layer — Reuse, No Rewrite

All required model functions are **already parameterized by `userId`** and enforce `WHERE user_id = ?`
(`model/token.go`): `GetAllUserTokens` (81), `SearchUserTokens` (127), `GetTokenByIds` (228),
`Token.Update` (286), `DeleteTokenById` (362), `CountUserTokens` (436), `BatchDeleteTokens` (443),
`GetTokenKeysByIds` (476). Admin handlers call these with **`targetUserId`** instead of the session id.
No changes to these functions.

`UpdateToken`'s field validation (name length, quota range / negative checks, expired/exhausted
re-enable guards) is factored into a shared helper used by both the self and admin update handlers to
avoid duplication.

## 5. Backend API

New handlers in the `controller` package (e.g. `controller/token_admin.go`). All management endpoints
are mounted under the existing **admin group** `adminRoute := userRoute.Group("/")` + `AdminAuth()`
(`router/api-router.go:127`). The reveal endpoint is registered **directly on `userRoute`** with its own
`RootAuth()` chain (single auth layer — avoids nesting `RootAuth` inside `AdminAuth`, which would
double-wrap the admin audit).

| Method & Path | Handler | Middleware / Auth |
|---|---|---|
| `GET /api/user/:id/tokens` | `AdminGetUserTokens` (paginated, **masked**) | AdminAuth |
| `GET /api/user/:id/tokens/search` | `AdminSearchUserTokens` | AdminAuth + `SearchRateLimit` |
| `GET /api/user/:id/tokens/:tid` | `AdminGetUserToken` (single, **masked**) | AdminAuth |
| `POST /api/user/:id/tokens` | `AdminCreateUserToken` (respects target's max-token limit; **returns no plaintext**) | AdminAuth |
| `PUT /api/user/:id/tokens` | `AdminUpdateUserToken` (full-object; `?status_only=true` for toggle) | AdminAuth |
| `DELETE /api/user/:id/tokens/:tid` | `AdminDeleteUserToken` | AdminAuth |
| `POST /api/user/:id/tokens/batch` | `AdminBatchDeleteUserTokens` | AdminAuth |
| `POST /api/user/:id/tokens/:tid/key` | `AdminGetUserTokenKey` (**full plaintext**) | **RootAuth** + `CriticalRateLimit` + `DisableCache` + `SecureVerificationRequired` |

**Routing order** (gin): register `/:id/tokens/search` before `/:id/tokens/:tid`, and all `/:id/tokens*`
subroutes before the bare `/:id` route. This coexists cleanly with existing static routes
(`/search`, `/topup`) — same mix already works in the channel and token groups.

### Reveal handler (root-only) — mirrors channel-key reveal
Pattern from `router/api-router.go:253` + `controller/channel.go:411` (`GetChannelKey`):
1. `RootAuth` + `CriticalRateLimit` + `DisableCache` + `SecureVerificationRequired` on the route.
2. Resolve `targetUserId`, `tid`; load target user; `canManageTargetRole` (root passes).
3. `token, err := model.GetTokenByIds(tid, targetUserId)` (scoped — cannot fetch another user's token).
4. Explicit audit: `recordManageAuditFor(c, targetUserId, "user.token.key_view", {id, name})`
   (also marks audit logged, preventing the auto-audit double-write).
5. Return `token.GetFullKey()`.

### Step-up verification (root reveal) — `SecureVerificationRequired`
`SecureVerificationRequired` (`middleware/secure_verification.go`) is **session-based step-up with a
5-minute sliding window**. The middleware only gates on a `secure_verified_at` session timestamp
(401 if not logged in; 403 `VERIFICATION_REQUIRED` if absent; 403 `VERIFICATION_EXPIRED` if older than
`SecureVerificationTimeout` = 300s). The actual verification is performed out-of-band at
`POST /api/verify` → `UniversalVerify` (`controller/secure_verification.go`), which supports **2FA (TOTP)
or Passkey (WebAuthn)** — there is **no password option** — and on success writes the 5-minute session
marker. Implications:
- **Prerequisite:** the acting root must have **2FA or Passkey enabled**; otherwise `/api/verify` returns
  *"用户未启用2FA或Passkey"* and reveal is unavailable. This is identical to the existing channel-key
  reveal constraint — not new to this feature.
- **Sliding window:** verify once, then reveal multiple keys within 5 minutes without re-verifying.
- This is the **only** new operation requiring step-up; all management operations (list/edit/delete/
  create) need just `AdminAuth` + `canManageTargetRole`.

## 6. Correctness & Safety Decisions (from review)

### 6.1 Full-object update — no partial-wipe (P1)
Self-service `UpdateToken` is a **full-object replacement**: missing JSON fields bind to zero/false/nil
and are persisted (`Token.Update` at `model/token.go:286` selects zero-capable columns:
`name, expired_time, remain_quota, unlimited_quota, model_limits_enabled, model_limits, allow_ips,
group, cross_group_retry`). A *partial* admin body would therefore silently clear expiry/quota/limits/
group.
- **Decision:** Admin full edit uses **full-object replacement** — the UI drawer prefills the token's
  current values and submits the complete object; the handler does `GetTokenByIds(tid, targetUserId)`
  then overwrites from the request (same shape as self-service). Quick enable/disable uses
  `?status_only=true`.
- **Documented alternative (future):** a pointer-typed partial-patch DTO per project Rule 6, if true
  partial PATCH is wanted later. Not in v1.

### 6.2 Group validation (P2)
Token `Group` is **not** validated at write time today; it is validated **lazily at request time**
(`middleware/auth.go:392-410`): a non-admin owner's token whose group ∉ `service.GetUserUsableGroups(userGroup)`
is rejected; **admin-owned tokens bypass** this check.
- **Decision:** When an admin sets/changes a token's group, validate the new group against the
  **target user's** usable groups **only if the target is a common user**. For admin/root targets, skip
  validation (mirrors the runtime bypass). This prevents an admin from creating a *silently dead* token
  for a common user (a group they can't actually use), while staying consistent with runtime semantics.
- Cross-DB: validation is pure Go map lookups over settings; no DB-specific SQL. The reused model
  functions already follow project DB-compat rules (GORM + `commonKeyCol`).

### 6.3 Token quota is token-level (P2)
`Token.RemainQuota / UnlimitedQuota / UsedQuota` are independent of `User.Quota`
(`model/token.go:14`, `model/user.go:24`). Runtime billing (`service/pre_consume_quota.go`,
`service/quota.go`) adjusts both, but **admin quota edits must set only `RemainQuota` / `UnlimitedQuota`**
and must **not** mutate `User.Quota` or the token's `UsedQuota` as a side effect.

### 6.4 Cache (confirmed adequate)
Reused functions invalidate the Redis token cache: `Token.Update` refreshes, `Delete` removes,
`BatchDeleteTokens` removes deleted keys after commit. Disable/delete take effect immediately.
`BatchDeleteTokens` returns the count actually found/deleted and silently ignores non-existent or
non-target ids — surface this count in the UI toast.

## 7. Audit

`AdminAuth`/`RootAuth` auto-wrap admin write methods (`middleware/auth.go:156`, `middleware/audit.go`),
but the **fallback logs against the operator**, not the target user. Therefore each token operation also
calls **explicit** `recordManageAuditFor(c, targetUserId, action, params)` (`controller/audit.go:106`),
so target user + token id(s) + action are explicit and the target sees it in their own log. Actions:
`user.token.create`, `user.token.update`, `user.token.status`, `user.token.delete`,
`user.token.batch_delete`, `user.token.key_view`. Explicit calls `markAuditLogged` to avoid double-write
with the auto-audit backstop.

## 8. Frontend (`web/default/`, React 19)

- **Entry point:** add a **"Manage API Keys"** item to the Users row-actions dropdown
  (`features/users/components/data-table-row-actions.tsx`), beside the existing "Manage Bindings" /
  "Manage Subscriptions" items, opening a new `UserApiKeysDialog` (drawer) — same pattern as
  `UserBindingDialog` / `UserSubscriptionsDialog`.
- **Panel:** a self-contained admin keys table (own thin API client targeting the new endpoints,
  parameterized by `userId`), reusing generic presentational components already in the repo
  (`status-badge`, `masked-value-display`, `group-badge`). Edit uses a **prefilled** drawer (full-object
  submit) to avoid the §6.1 partial-wipe footgun. Supports enable/disable, edit, delete, batch delete,
  create-on-behalf.
- **Reveal full key button:** shown **only when the current user is root**. Clicking reuses the existing
  universal step-up verification flow (`POST /api/verify`, 2FA/Passkey) that channel-key reveal already
  drives — on a `VERIFICATION_REQUIRED`/`VERIFICATION_EXPIRED` response, prompt verification (2FA code or
  Passkey), then retry the reveal. If the root has neither 2FA nor Passkey enabled, surface the backend
  message guiding them to enable one. (Backend enforces root + step-up regardless — defense in depth.)
- **i18n:** add new strings to `web/default/src/i18n/locales/*.json` (en base, zh, …) and backend
  `i18n/` (en, zh) for any new server messages.

## 9. Testing (project Rule 9 — testify, table-driven, contract-focused)

Backend contract tests (initialize DB/context/role/settings explicitly in fixtures):
1. **Access control:** admin can manage a lower-role target; admin **cannot** manage a same-level or
   higher target (403); root can manage anyone. Reveal-plaintext: non-root (admin) is rejected; root
   succeeds.
2. **Cross-user scoping:** an admin operation on user A's tokens never reads/mutates user B's tokens
   (e.g. delete by id that belongs to B returns not-found / affects nothing).
3. **Group validation:** setting a common target's token to a group not in their usable groups is
   rejected; an admin/root target is allowed (bypass parity with runtime).
4. **Full-object update safety:** a full-object update preserves untouched fields (regression guard for
   §6.1); `status_only` changes only status.
5. **Quota isolation:** editing a token's `RemainQuota` does not change `User.Quota` or token `UsedQuota`.
6. **Create-on-behalf:** response contains **no plaintext** key.

## 10. Out of Scope
- True partial PATCH semantics (pointer DTO) — documented as a future option (§6.1).
- Bulk cross-user operations, token transfer between users, scheduled key rotation.
