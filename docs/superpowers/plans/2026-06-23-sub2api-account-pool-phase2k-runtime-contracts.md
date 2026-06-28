# Phase 2K: Account Pool Runtime Contracts

## Goal

Close logical gaps that can be verified without a real upstream account:

1. Provide a read-only service contract that answers whether an account-pool-bound channel currently has any schedulable runtime account for a model.
2. Preserve the selected account identifier through runtime selection so provider adaptors can build protocol-specific headers.
3. Allow the Codex adaptor to use account-pool runtime OAuth access tokens without requiring the persisted channel key to be a Codex OAuth JSON object.

## Non-Goals

- Do not add UI.
- Do not implement sub2api reverse-proxy flows.
- Do not call real upstream services.
- Do not change billing, priority, or channel retry behavior.

## Expected Behavior

- An unbound or disabled binding reports `runtime_enabled=false` and no error.
- A bound channel with no usable account reports `runtime_enabled=true`, `schedulable=false`.
- A bound channel with at least one usable account reports `runtime_enabled=true`, `schedulable=true`.
- Runtime selection records the selected account identifier on `RelayInfo`.
- Codex request headers accept either:
  - legacy persisted OAuth JSON key with `access_token` and `account_id`; or
  - runtime raw access token plus runtime account id from account-pool selection.

## Verification

- Focused service tests for schedulability and runtime account identifier propagation.
- Focused Codex adaptor tests for legacy JSON keys and runtime raw tokens.
- Existing account-pool runtime retry/proxy/token tests must remain passing.
- Claude review after implementation.
