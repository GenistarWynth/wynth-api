# Account Pool Model Capability Detection Design

## Goal

Add an account-pool capability detection flow that can discover or verify which models each account can use, then write those results back to account-pool account configuration.

This is Phase 3A of the account-pool migration. It should make account scheduling safer and easier to configure without taking on the deeper sub2api-specific reverse-proxy protocol work yet.

## Background

The current account-pool runtime can select an account, inject its credential, refresh OAuth tokens, apply account-level model mapping, retry another account before a response starts, and record runtime metrics. Account selection already filters by `AccountPoolAccount.SupportedModels` and applies `AccountPoolAccount.ModelMapping`.

The missing piece is how those account model fields get populated and maintained. Today they are manual or imported from external config. That is enough for a first runtime, but it is fragile:

- imported account data may be incomplete;
- an upstream account may lose access to a model;
- new models may become available;
- a binding can route a request to an account that looks configured but is not actually capable.

This design adds a controlled detection path for account model capability. It is separate from the existing upstream actual model audit design, which records the model an upstream actually returned in usage logs. Capability detection is account configuration and scheduling input, not request log decoration.

## Non-Goals

- Do not implement a dedicated ChatGPT web/session reverse proxy protocol in this phase.
- Do not change billing, channel priority, or channel weight.
- Do not use detected capability to auto-create local channels.
- Do not consume account-pool usage metrics, cache-rate metrics, or health metrics for scoring.
- Do not store raw upstream responses, access tokens, refresh tokens, or prompt text in detection logs.
- Do not require browser automation.

## Recommended Approach

Use a two-mode detector:

1. `models_endpoint`
   - Use the account runtime credential and proxy to call an OpenAI-compatible `/v1/models` endpoint where possible.
   - Parse model ids from the response.
   - This is cheap and safe when the upstream supports it, but not every account/upstream will expose the complete usable list.

2. `probe_models`
   - Given an administrator-selected candidate model list, issue minimal probe requests for those models.
   - Record which candidates work and which fail with model-not-found or authorization-like errors.
   - This costs more than `/v1/models`, so it should be explicit and bounded.

The service should support both modes behind one account capability detection API. The UI can default to `models_endpoint` and allow `probe_models` when the administrator wants stricter verification.

## Data Model

Avoid new database tables in the first implementation. Reuse account fields that already exist and add only small metadata fields if needed.

The detector may update:

- `SupportedModels`: JSON array of local/upstream model names the scheduler may use for this account.
- `ModelMapping`: JSON object from channel upstream model to account actual model.
- `LastError`: sanitized detection error when detection fails.
- `UpdatedTime`: normal model update timestamp.

If the implementation needs persistent detection metadata, add fields to `AccountPoolAccount` rather than a separate table:

- `LastCapabilityCheckAt int64`
- `LastCapabilityCheckStatus string`
- `LastCapabilityCheckError string`
- `LastCapabilityCheckModels string` as JSON text

These fields must be optional and cross-database safe. JSON remains `TEXT`; no JSON query operators.

Do not overwrite health fields such as `RateLimitedUntil`, `TempDisabledUntil`, `SuccessCount`, `FailureCount`, token usage, cache tokens, latency, or first-token latency from detection. Detection may report errors to the capability metadata and `LastError`, but it must not make test/probe traffic look like production traffic.

## API Design

Add account-pool admin endpoints:

- `POST /api/account_pools/:id/accounts/:account_id/capabilities/detect`
- `POST /api/account_pools/:id/capabilities/detect`

The single-account endpoint returns one result. The pool endpoint runs over selected accounts or all enabled accounts in the pool with bounded concurrency.

Request shape:

```json
{
  "mode": "models_endpoint",
  "candidate_models": ["gpt-5", "gpt-5-mini"],
  "apply": true,
  "merge": true,
  "model_mapping": {},
  "timeout_seconds": 30
}
```

Fields:

- `mode`: `models_endpoint`, `probe_models`, or `auto`.
- `candidate_models`: required for `probe_models`; optional filter for `models_endpoint`.
- `apply`: when false, return a dry-run result without writing account fields.
- `merge`: when true, merge detected models into existing `SupportedModels`; when false, replace `SupportedModels` with detected models.
- `model_mapping`: optional explicit mapping to apply after detection. This lets administrators map public model aliases to actual account models without guessing.
- `timeout_seconds`: bounded per-account timeout, clamped to a safe range.

Response shape:

```json
{
  "account_id": 12,
  "status": "success",
  "mode": "models_endpoint",
  "detected_models": ["gpt-5", "gpt-5-mini"],
  "applied_models": ["gpt-5", "gpt-5-mini"],
  "model_mapping": {},
  "errors": []
}
```

Pool response should include aggregate counts and per-account results. Partial failure is allowed: one failed account must not abort the entire pool detection.

## Runtime Integration

Detection should reuse existing account-pool runtime pieces:

- decrypt account credential and token state;
- refresh OAuth token if needed;
- resolve account proxy and pool default proxy;
- use the same HTTP proxy behavior as relay requests;
- respect channel-test isolation semantics so detection traffic does not update production health or usage metrics.

The token provider already has a `SkipFailureRecord` path for channel-test OAuth refresh. Capability detection should use the same semantic: refresh errors should be returned and sanitized, but they should not write production health penalties unless the user explicitly runs a separate health check feature.

Detection must not acquire long-lived runtime leases unless the probe path can otherwise overload the same account. For `probe_models`, use the same per-account concurrency lease with a short timeout so manual probes do not race real traffic uncontrollably. For `models_endpoint`, a lease is optional because it is a metadata call, but using one is safer and simpler.

## Detection Semantics

### `models_endpoint`

For OpenAI-compatible accounts:

1. Build the account base URL from the bound channel or configured runtime defaults.
2. Call `GET /v1/models` with the account runtime credential.
3. Decode `data[].id`.
4. Filter empty ids and deduplicate while preserving upstream order.
5. If `candidate_models` is non-empty, intersect with candidates while preserving candidate order.

If the endpoint returns 401 or 403, report an auth error but do not mark the account expired in this phase.

If the endpoint returns 404 or not implemented, return a clear `unsupported` result so the UI can suggest `probe_models`.

### `probe_models`

For each candidate model:

1. Use a minimal request appropriate for the channel type.
2. Mark the model as supported on successful upstream response.
3. Mark the model as unsupported for model-not-found, not-allowed, or deployment-not-found errors.
4. Mark the account result as failed for auth errors, network errors, and malformed upstream responses.

The first implementation should support OpenAI-compatible chat or responses text probes only. It should not probe image generation, audio, embeddings, or Anthropic-native routes in this phase.

The probe prompt must be fixed, tiny, and non-user-supplied. Probe usage must not write account-pool usage metrics or cache-rate metrics.

## Mapping Behavior

Detection returns detected account-side model names. It should not invent aliases automatically.

When the administrator supplies `model_mapping`, validate it before writing:

- keys and values must be non-empty strings;
- values should be present in detected models unless `apply` is false;
- duplicate or whitespace-only entries are rejected or cleaned deterministically.

If no mapping is supplied, detection should update only `SupportedModels`.

Later UI work can add convenience actions like “map selected local model to detected account model”, but this backend phase should keep the mapping contract explicit.

## UI Scope

Keep the first UI pass small:

- Add a “检测模型” action on each account row.
- Add a pool-level “批量检测模型” action.
- Show detection status, detected model count, and sanitized last detection error.
- Let the admin choose:
  - mode: auto / models endpoint / probe models;
  - candidate model list for probe mode;
  - dry run vs apply;
  - merge vs replace.

Do not add a complex visual model-mapping editor in this phase. Existing account edit fields can continue to manage `SupportedModels` and `ModelMapping`.

## Error Handling And Secret Safety

Detection errors must be sanitized before storage or API return:

- mask bearer tokens, `sk-` keys, access tokens, refresh tokens, proxy passwords, and authorization headers;
- truncate long upstream bodies;
- do not include raw request/response bodies in persisted fields;
- do not log full credentials.

Detection should classify results:

- `success`
- `partial`
- `unsupported`
- `auth_error`
- `network_error`
- `upstream_error`
- `config_error`

Classification is for display and follow-up action, not for automatic account disabling in this phase.

## Testing

Backend tests should cover:

- `/v1/models` success updates `SupportedModels`.
- `/v1/models` dry run returns detected models without writing.
- candidate filtering preserves candidate order.
- merge mode preserves existing models and adds new ones deterministically.
- replace mode removes models absent from detection.
- unsupported `/v1/models` returns `unsupported` and does not write.
- auth failure returns sanitized error and does not set account `Status`, `RateLimitedUntil`, or `TempDisabledUntil`.
- OAuth refresh failure during detection uses the skip-health-record path.
- probe mode marks supported and unsupported candidate models with deterministic fake upstream responses.
- pool detection continues after one account fails.
- all JSON marshal/unmarshal calls use `common` wrappers.

Frontend tests should cover:

- account row action calls the single-account detection API;
- pool action calls the pool detection API;
- dry-run results are displayed without assuming persistence;
- errors are shown in Chinese UI text through i18n keys.

## Rollout

1. Implement backend service and tests first.
2. Add controller endpoints and route tests.
3. Add minimal UI entry points.
4. Run backend and frontend verification.
5. Ask Claude for focused review on HTTP/proxy/secret-handling paths.

## Out Of Scope For This Phase

- Automatic priority or weight adjustment from detected capabilities.
- Combining capability detection with channel monitoring availability scores.
- Inferred model aliases from ClaudeCodeHub or done-hub heuristics.
- Anthropic account pool support.
- Dedicated ChatGPT browser-session reverse proxy.
- Background scheduled capability detection.

## Open Follow-Up

After this phase, the next logical step is either:

- add scheduled/background capability checks for account pools; or
- start the dedicated ChatGPT/sub2api reverse-proxy protocol migration once real-account behavior is understood from detection results.
