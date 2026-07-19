# Remaining Meaningful Account-Pool Enhancements Design

## Scope and decisions

This change completes the five explicitly requested account-pool items while preserving Wynth's channel-first relay architecture. The sub2api checkout is a behavioral reference only. No provider gateway, Vue, or Ent architecture is copied.

The implementation uses five incremental slices:

1. A generic local quota-state reset API and admin dialog.
2. Shared outbound override validation and runtime application across supported account-pool platforms.
3. Account-linked consume logs and a log-backed rolling xAI Free usage view.
4. Portable DB-backed leases for the xAI quota and OAuth maintenance workers.
5. Bounded parallel xAI SSO conversion with ordered result aggregation.

## Local quota reset

Add `POST /api/account_pools/:id/accounts/:account_id/quota/reset` with three explicit options:

- `clear_cooldown` defaults to true and clears the local rate-limit cooldown, the in-process/Redis runtime block, and quota-related temporary-disable metadata. It also clears the stored xAI quota observation so stale exhausted state is not presented as current.
- `reset_request_quota` clears `request_quota_used` and restarts the configured local request-quota window.
- `force_probe` performs a fresh xAI OAuth quota probe after the local reset. It is rejected for accounts that cannot be probed.

The response returns the refreshed account, optional probe snapshot, and a sanitized probe error. A probe failure does not roll back an already committed local reset. The UI exposes an account-menu action and an option dialog whose copy states that no upstream provider quota is reset. sub2api's xAI implementation explicitly reports upstream quota reset as unsupported, so Wynth does not imitate a nonexistent hard reset.

## Cross-platform outbound overrides

Replace the xAI-only validation entry point with a shared policy. Supported pool/account paths are OpenAI, Anthropic, Gemini API/OAuth, Gemini Vertex service accounts, and xAI. `grok_web` is rejected because arbitrary base/header changes can break Cookie, `cf_clearance`, and anti-bot session coupling.

The base URL policy requires an absolute HTTPS URL without userinfo, query, or fragment. Official platform hosts are trusted defaults; custom public hosts remain supported after DNS resolution and private/link-local/loopback/multicast rejection. A development-only HTTP escape hatch remains available through a renamed generic environment option, with the existing xAI option retained as a compatibility alias.

Header validation is shared across platforms, bounded by entry/name/value/total size, and blocks authentication/provider-billing, cookie, proxy/forwarding, connection, content framing, compression, and WebSocket handshake headers. Runtime merge order is channel headers first and account headers second, so account values win. Provider URL builders opt into `RuntimeBaseURL`; Vertex and Gemini Code Assist retain their provider defaults when no account override exists.

The React account form shows the same fields for every supported pool and keeps them hidden for `grok_web`.

## Account-linked usage logs

Add nullable-by-zero `account_pool_id` and `account_pool_account_id` integer columns to consume logs with indexes. `RecordConsumeLog` copies the selected IDs from the request context, so text, audio, realtime, task, and other existing consume-log callers inherit the association without duplicating plumbing. GORM migrations cover SQLite, MySQL, and PostgreSQL; ClickHouse create/upgrade SQL is kept compatible as an additional safeguard.

For xAI Free accounts, query consume logs over `[now-24h, now]` by account ID. If at least one linked row exists, the usage source is `logs_24h`, `estimated=false`, and requests/tokens are exact Wynth-observed rolling values. If no linked rows exist, preserve the existing counter algorithm but expose the normalized source `counter_estimate`. No backfill is attempted, and external provider traffic remains unknowable.

## Distributed worker leases

Add `account_pool_worker_leases` with a primary-key lease name, owner ID, expiry, and update timestamp. Acquisition is a portable insert-if-missing plus conditional update (`expired OR same owner`); renewal and release require owner match. Each maintenance tick obtains a lease, renews it on a heartbeat, cancels work if ownership is lost, and releases best-effort on completion. Process-local atomic guards remain as a cheap re-entry defense.

The initial leased workers are:

- `account_pool:xai_quota_probe`
- `account_pool:xai_oauth_reconcile`

TTL recovery guarantees that a crashed holder does not block future ticks indefinitely.

## Bounded SSO import

xAI SSO conversion uses three workers by default and accepts at most eight through `ACCOUNT_POOL_XAI_SSO_IMPORT_CONCURRENCY`. The existing 25-item request bound remains. Each conversion receives a 90-second per-item timeout, the batch receives a bounded total deadline (`ACCOUNT_POOL_XAI_SSO_IMPORT_TIMEOUT_SECONDS`, default 300 seconds), and the selected account/pool proxy URL is reused for every item.

Conversions fill an indexed outcome array. Account creation and response aggregation then run in input order, preserving stable names, indexes, successes, and failures. Returned errors remain static and never contain SSO tokens, proxy credentials, or provider response bodies.

## API and configuration reference

`POST /api/account_pools/:id/accounts/:account_id/quota/reset` accepts:

```json
{
  "clear_cooldown": true,
  "reset_request_quota": false,
  "force_probe": false
}
```

The response includes the refreshed account, the actions applied, an optional probe snapshot/error, and `upstream_reset: false` so clients cannot mistake a local reset for a provider-side reset.

| Setting | Default | Accepted behavior |
| --- | ---: | --- |
| `ACCOUNT_POOL_OUTBOUND_ALLOW_UNSAFE_BASE_URL` | `false` | Development-only opt-out of HTTPS/public-host enforcement. The legacy `ACCOUNT_POOL_XAI_ALLOW_UNSAFE_BASE_URL` remains an xAI-only alias. |
| `ACCOUNT_POOL_WORKER_LEASE_TTL_SECONDS` | `120` | 15–3600 seconds; invalid values use the default and heartbeats run at one third of the TTL. |
| `ACCOUNT_POOL_XAI_SSO_IMPORT_CONCURRENCY` | `3` | Positive values, capped at 8. |
| `ACCOUNT_POOL_XAI_SSO_IMPORT_TIMEOUT_SECONDS` | `300` | 30–1800 seconds for the whole conversion batch; each item also retains the 90-second provider-flow timeout. |

The quota and reconcile interval/staleness/max-per-tick settings remain unchanged. Distributed lease keys are fixed internal identities rather than configuration: `account_pool:xai_quota_probe` and `account_pool:xai_oauth_reconcile`.

## Verification and limitations

Tests cover local reset combinations, no-fake-upstream semantics, per-platform override validation and precedence, consume-log association, log-vs-counter estimate selection, lease contention/expiry/renewal, and bounded ordered SSO aggregation without live provider traffic. Frontend helper tests cover payload/platform behavior; all six locales receive the new copy through the repository i18n script.

Known limitations are intentional: xAI upstream quota cannot be hard-reset; log-backed usage begins only after the migration; DNS validation cannot by itself prevent a malicious public hostname from rebinding after save; and `grok_web` outbound overrides remain excluded for session safety.
