# sub2api Account-Pool Sync Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship a Wynth-native Grok/xAI OAuth login, refresh, and SSO-to-OAuth import flow while preserving the existing multi-platform account-pool and channel architecture.

**Architecture:** Add OAuth protocol/session primitives to `service/xai_oauth.go`, expose pool-scoped Gin endpoints, and translate successful tokens into existing encrypted account-pool credential/token-state types. The React account form consumes those endpoints and continues to use existing create/update mutations.

**Tech Stack:** Go 1.22, Gin, GORM v2, `common.*` JSON wrappers, React 19, TypeScript, TanStack Query, Base UI/shadcn composition, i18next, Bun/Vitest.

---

### Task 1: xAI OAuth protocol and session service

**Files:**
- Modify: `service/xai_oauth.go`
- Modify: `service/xai_oauth_test.go`

- [ ] Add failing table tests for callback URL/query/bare-code parsing, redirect URI validation, PKCE authorization parameters, constant-time state validation, session expiry/single use, exchange form fields, refresh-token preservation, JWT claims, and Wynth credential/token-state conversion.
- [ ] Run `go test ./service -run 'TestXAI(OAuth|Authorization)' -count=1` and confirm failures are caused by missing APIs.
- [ ] Implement `XAIOAuthSessionStore`, `GenerateXAIOAuthAuthorization`, `ExchangeXAIOAuthCodeWithProxy`, `ParseXAIOAuthAuthorizationInput`, and token-info conversion using `common.DecodeJson`/`common.Unmarshal`.
- [ ] Re-run the focused tests, then `go test ./service -run 'XAI|AccountPoolTokenProvider' -count=1`.
- [ ] Commit `feat(account-pool): add xai oauth pkce exchange`.

### Task 2: trusted SSO-to-Build OAuth conversion

**Files:**
- Create: `service/xai_sso_oauth.go`
- Create: `service/xai_sso_oauth_test.go`

- [ ] Add a fake-client failing test for SSO normalization, cookie carry-forward, device-code creation, verify/approve redirects, token polling, untrusted redirects, body limits, and cancellation.
- [ ] Run `go test ./service -run 'TestXAI.*SSO' -count=1` and confirm the new behavior is missing.
- [ ] Implement the bounded trusted-host device flow with injectable `Do(*http.Request)` and sleep seams; use only `common.*` for JSON decoding.
- [ ] Re-run the focused SSO tests and the full service package.
- [ ] Commit `feat(account-pool): convert grok sso to oauth`.

### Task 3: pool-scoped OAuth controller APIs

**Files:**
- Modify: `dto/account_pool.go`
- Modify: `controller/account_pool.go`
- Modify: `controller/account_pool_test.go`
- Modify: `router/api-router.go`

- [ ] Add failing handler tests for xAI pool validation, authorize response, exchange response shape, refresh of an encrypted existing account, and SSO import partial results.
- [ ] Run `go test ./controller -run 'TestAccountPoolXAI' -count=1` and confirm 404/missing-handler failures.
- [ ] Add pool-scoped DTOs and handlers. Resolve proxy context through existing account-pool services, return `credential`/`token_state`, and route refresh/import writes through `AccountPoolService`.
- [ ] Register the four routes under the existing admin-only `/api/account_pools` group and add audit events without secret fields.
- [ ] Re-run controller, service, and router contract tests.
- [ ] Commit `feat(account-pool): expose grok oauth admin APIs`.

### Task 4: newer Grok import aliases

**Files:**
- Modify: `service/account_pool_import.go`
- Modify: `service/account_pool_import_test.go`
- Modify: `service/account_pool_export.go` if round-trip metadata requires it
- Modify: `service/account_pool_export_test.go` if the export contract changes

- [ ] Add failing deterministic cases for `platform: grok|xai`, `type: oauth`, top-level/nested `rt`, refresh/access/id tokens, client ID, RFC3339/unix expiry, and SSO cookie aliases; assert unrelated platform imports remain unchanged.
- [ ] Run the focused import tests and confirm expected field mismatches.
- [ ] Normalize only recognized Grok OAuth shapes into Wynth credential/token state; never persist raw SSO cookies as OAuth refresh tokens.
- [ ] Re-run import/export tests with `-count=3`.
- [ ] Commit `fix(account-pool): accept current grok credential imports`.

### Task 5: React OAuth API and pure form behavior

**Files:**
- Modify: `web/default/src/features/account-pools/api.ts`
- Modify: `web/default/src/features/account-pools/types.ts`
- Modify: `web/default/src/features/account-pools/lib/account-pool-form.ts`
- Modify: `web/default/src/features/account-pools/lib/account-pool-form.test.ts`

- [ ] Add failing Vitest cases for applying an exchange result to account form values while preserving non-secret configuration and for API request/response types.
- [ ] Run `bun run test -- src/features/account-pools/lib/account-pool-form.test.ts` from `web/default`.
- [ ] Add typed authorize/exchange/refresh/SSO-import API functions and a pure `applyXAIOAuthResultToForm` function.
- [ ] Re-run targeted tests and typecheck.
- [ ] Commit `feat(account-pool): add grok oauth frontend client`.

### Task 6: guided React OAuth UI

**Files:**
- Create: `web/default/src/features/account-pools/components/xai-oauth-flow.tsx`
- Modify: `web/default/src/features/account-pools/index.tsx`

- [ ] Add a component interaction test if the existing test harness supports React Testing Library; otherwise protect the state transition in Task 5 and verify the rendered flow through build/typecheck.
- [ ] Implement xAI-only controls for start, external authorization, callback/code paste, exchange, fallback token paste, and existing-account refresh. Keep secrets out of browser storage.
- [ ] Integrate the component into `AccountFormSheet` without changing non-xAI forms.
- [ ] Run targeted tests, `bun run typecheck`, and lint for modified files.
- [ ] Commit `feat(account-pool): add grok oauth login ui`.

### Task 7: six-locale i18n

**Files:**
- Modify: `web/default/src/i18n/locales/en.json`
- Modify: `web/default/src/i18n/locales/zh.json`
- Modify: `web/default/src/i18n/locales/fr.json`
- Modify: `web/default/src/i18n/locales/ja.json`
- Modify: `web/default/src/i18n/locales/ru.json`
- Modify: `web/default/src/i18n/locales/vi.json`

- [ ] Add every literal key used by the OAuth component and refresh action, with natural translations in all six locales.
- [ ] Run `bun run i18n:sync` and the i18n validation script exposed by `package.json`.
- [ ] Commit `feat(i18n): translate grok oauth account flow`.

### Task 8: Phase 2 parity decision and narrow fixes

**Files:**
- Modify only account-pool service/model/relay files justified by a failing regression test
- Update: `docs/superpowers/specs/2026-07-19-sub2api-account-pool-sync-design.md`

- [ ] Add or confirm regression coverage that pool selection already honors temp disable, model cooldown, configured zero or positive concurrency, and xAI image model mapping.
- [ ] Compare Wynth failure classification with sub2api's concrete Grok revoked/entitlement/transient classes; add only missing classifications that fit the existing state machine.
- [ ] Attempt a Wynth-native media eligibility design only if an authoritative probe is available without gateway/video coupling; otherwise keep the documented defer decision.
- [ ] Run affected model/service/relay tests with `-count=3`.
- [ ] Commit any coherent narrow fix separately.

### Task 9: changelog and release verification

**Files:**
- Modify: `CHANGELOG.md`
- Update: `docs/superpowers/specs/2026-07-19-sub2api-account-pool-sync-design.md`

- [x] Add an Account Pool changelog entry covering OAuth login, refresh, SSO import, import aliases, and deliberate deferrals.
- [x] Run `gofmt` on changed Go files and targeted Go tests, followed by `go test ./model ./service ./controller ./relay ./middleware ./types`.
- [x] Run account-pool tests with `-count=3`, `go vet` on affected Go packages, frontend targeted tests, typecheck, lint, i18n validation, and production build.
- [x] Inspect `git diff --check`, staged secrets/URLs, and final diff scope.
- [x] Commit `chore(release): document grok oauth account pool sync`.
- [ ] Merge the feature branch into `main`, push the branch and main, and prepare Chinese-friendly release notes. Create a tag only if all verification is solid and a release version is explicitly selected.
