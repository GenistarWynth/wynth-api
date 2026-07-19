# CLIProxyAPI Essence for Account-Pool Import

- Date: 2026-07-19
- Branch: `feat/account-pool-cpa-essence-sync`
- Target baseline: `main@097c8ecb`
- Reference: `/root/work/CLIProxyAPI` at `origin/main@e47ffda` (`v7.2.90`)
- Scope: account-pool credential/config import only; Wynth's channel and relay architecture remains authoritative.

## Goal

Absorb the useful CLIProxyAPI (CPA) credential file shapes and per-account operational fields into Wynth's existing account-pool importer. Imported secrets must continue through the existing encrypted `AccountPoolCredentialConfig` and `AccountPoolTokenState` boundaries, and imported accounts must behave like accounts created through Wynth's native administration APIs.

## Reference inventory

The current CPA example and auth implementations provide two relevant families of input:

1. `codex-api-key` configuration entries with `api-key`, `priority`, `prefix`, `base-url`, `headers`, `proxy-url`, model aliases, excluded-model patterns, and `disable-cooling`.
2. JSON auth files from CPA's auth directory. Codex, Claude, Antigravity/Gemini, Vertex, and xAI files use provider/type markers plus access tokens, refresh tokens, expiry, email/account/project identity, and optional operational metadata. CPA's management upload API accepts individual JSON files and multipart batches, but the on-disk payload remains one JSON object per file.

CPA also contains a proxy gateway, management API, plugin runtime, commercial mode, routing/session engines, panel integration, and provider-specific request transformations. Those systems are not credential import formats and are outside this design.

## Chosen architecture

Extend `service/account_pool_import.go` as a bounded translation layer. It will recognize CPA input, produce the existing `AccountPoolAccountCreateParams`, and then rely on `AccountPoolService.CreateAccount` for validation, encryption, and persistence. It will not add a CPA runtime, provider registry, management API, or alternate relay path.

The alternatives considered were:

- A generic CPA provider/plugin registry. This would make adding unrelated CPA providers easy, but it adds abstractions and lifecycle concepts that Wynth does not need for four already-supported pool platforms.
- Importing CPA's auth manager and file-store model. This would preserve more CPA behavior, but it would create a second credential/runtime authority and conflict with Wynth's channel-first account-pool design.

The direct translation approach keeps the security and runtime behavior at Wynth's existing boundaries and is therefore the smallest durable design.

## CPA config mapping

`codex-api-key` remains valid only for OpenAI pools. Each list entry maps as follows:

| CPA field | Wynth destination |
| --- | --- |
| `api-key` | encrypted credential `type=api_key`, `api_key` |
| `priority` | account priority |
| `prefix` | account display name and client-facing model prefix |
| `base-url` | encrypted per-account `base_url`, through existing HTTPS/SSRF validation |
| `headers` | encrypted `header_overrides` with overrides enabled, through existing header allowlist validation |
| normal proxy URL | existing account-pool proxy dedup/create path |
| `direct` / `none` | explicit account-level direct connection that bypasses the pool default proxy |
| `models[].name/alias` | `supported_models` and `model_mapping` |
| `excluded-models` | filters explicit `models` using CPA-style wildcard matching |

An exclusion list without an explicit model list cannot be represented by Wynth's positive `supported_models` policy without inventing a provider catalog snapshot. Such an exclusion-only entry therefore retains Wynth's unrestricted model policy and the limitation is documented rather than guessed.

`disable-cooling` is deliberately not mapped. CPA uses it to disable its own auth/model cooldown scheduler. Wynth's temporary disable and rate-limit timestamps are safety state produced by the shared failure classifier. Treating `disable-cooling` as an initial timestamp or disabling Wynth's classifier would not preserve semantics and could keep broken credentials hot.

## Auth JSON provider mapping

CPA auth input accepts a JSON array, one JSON object, or an object containing `auths`, `accounts`, `items`, or `data` arrays. A nested `metadata` object is merged over top-level values so both CPA's on-disk files and wrapper/export shapes are accepted.

| CPA provider/type | Required destination pool | Wynth credential behavior |
| --- | --- | --- |
| `codex`, `openai` (or legacy empty provider) | OpenAI | OAuth token state; Codex refresh path |
| `claude`, `anthropic` | Anthropic | OAuth token state; Claude refresh path |
| `gemini` | Gemini | OAuth with `ai_studio` subtype |
| `gemini-cli`, `geminicli` | Gemini | OAuth with `code_assist` subtype and project ID |
| `antigravity` | Gemini | OAuth with `antigravity` subtype and project ID |
| `google-one`, `google_one` | Gemini | OAuth with `google_one` subtype and project ID |
| `vertex`, `vertex-ai`, `vertex_ai` | Gemini | service-account JSON plus location/project identity; token-shaped Vertex entries are rejected |
| `xai` | xAI | OAuth token state and xAI identity/client metadata |

Provider entries that do not match the destination pool are skipped with an actionable per-entry error. Unsupported types are also skipped. The messages do not include the provider-supplied token, proxy URL, header value, or raw input.

OAuth imports retain access/refresh tokens, expiry (`expires_at` or CPA's RFC3339/numeric `expired`), email, account ID, ID token, client ID, scope, token type, subject, team/subscription/entitlement metadata, project ID, priority, disabled status, base URL, headers, and proxy selection where Wynth has an equivalent field. Access/refresh tokens stay in encrypted storage; account and project identifiers remain in their existing Wynth columns/token state.

## Explicit direct proxy semantics

Wynth currently interprets account `proxy_id=0` as “inherit the pool default proxy,” so mapping CPA `direct` or `none` to zero would silently change behavior. The import uses a reserved negative account proxy ID for explicit direct connection. Runtime proxy resolution returns no proxy for this sentinel before considering the pool default. Positive proxy IDs and zero/inherit behavior remain unchanged.

The account editor and import defaults expose separate “use pool default” and “direct connection” options so imported accounts can be reviewed and edited without losing the distinction.

## Errors and secret safety

- JSON/YAML shape errors identify the expected top-level `codex-api-key` spelling or auth object/array form.
- Missing credential errors name only the missing field family.
- Proxy URL parse errors are normalized and never return Go's raw URL parse error, which can contain proxy usernames or passwords.
- Header validation may identify a header name but never its value.
- Duplicate detection continues to hash credential/token values and returns only a generic duplicate message.

## UI and internationalization

The import format label names “CLIProxyAPI / CPA.” Help text distinguishes CPA config YAML/JSON from CPA auth JSON files. The file input accepts `.json`, `.yaml`, `.yml`, and the matching JSON/YAML/text MIME types. New copy is translated in `en`, `zh`, `fr`, `ja`, `ru`, and `vi` through the repository i18n workflow.

## Verification

Backend fixtures derived from CPA's current examples cover:

- a Codex config list with base URL, headers, direct proxy, prefix, aliases, and wildcard exclusions;
- Codex/OpenAI, Claude/Anthropic, Gemini CLI/Antigravity, Vertex, and xAI auth objects;
- JSON arrays and single objects;
- provider/pool mismatch and plural/mis-keyed config errors;
- expiry/identity/operational field preservation;
- proxy error redaction and explicit-direct runtime behavior.

Frontend tests cover the accepted file extensions/MIME contract and the distinct proxy option values. Final verification includes targeted Go tests, affected frontend tests, i18n checks, type checking, lint/format checks, the production frontend build, and the repository's account-pool backend package gate.

## Explicitly rejected CPA scope

- server host/port/TLS and management endpoints;
- plugin and commercial-mode systems;
- WebSocket authentication and gateway routing strategies;
- Claude cloak behavior and CPA request transformations;
- session-affinity/router engines;
- panel downloads and remote-management secrets;
- CPA's proxy server or whole-file auth runtime.

These remain rejected because they duplicate or conflict with Wynth's channel, relay, administration, and account-pool scheduling foundations.
