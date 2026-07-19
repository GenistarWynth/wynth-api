# Account-Pool Meaningful Remaining Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship all five remaining meaningful account-pool enhancements with portable persistence, safe outbound behavior, admin controls, and regression tests.

**Architecture:** Extend the existing account-pool service/controller/relay path rather than adding another scheduler or gateway. Shared validation and lease primitives sit below provider-specific behavior; request context connects selection to consume logs; the React admin UI consumes the new generic APIs.

**Tech Stack:** Go 1.22+, Gin, GORM v2, SQLite/MySQL/PostgreSQL-compatible persistence, React 19, TypeScript, Base UI/shadcn composition, TanStack Query, Vitest/Node test runner, i18next.

---

### Task 1: Local quota reset API and admin control

**Files:**
- Create: `service/account_pool_quota_reset.go`
- Create: `service/account_pool_quota_reset_test.go`
- Modify: `dto/account_pool.go`
- Modify: `controller/account_pool.go`
- Modify: `controller/account_pool_test.go`
- Modify: `router/api-router.go`
- Modify: `web/default/src/features/account-pools/api.ts`
- Modify: `web/default/src/features/account-pools/types.ts`
- Modify: `web/default/src/features/account-pools/index.tsx`
- Modify: `web/default/src/features/account-pools/lib/xai-quota.ts`
- Modify: `web/default/src/features/account-pools/lib/xai-quota.test.ts`

- [x] Write service tests proving cooldown/runtime-block clearing, optional request counter reset, xAI snapshot clearing, force-probe validation, and committed reset plus sanitized probe failure.
- [x] Run the focused Go tests and confirm they fail because the reset API does not exist.
- [x] Implement the service method with an atomic DB update and optional post-commit probe.
- [x] Write controller contract tests for request defaults, response shape, and audit-safe behavior; confirm RED, then add DTO, route, and handler.
- [x] Write frontend helper tests for force-probe eligibility and reset payload defaults; confirm RED, then add API types and the account-menu dialog.
- [x] Run focused Go/frontend tests and commit `feat(account-pools): add local quota reset controls`.

### Task 2: Shared cross-platform outbound overrides

**Files:**
- Rename/Modify: `service/account_pool_xai_overrides.go` to `service/account_pool_outbound_overrides.go`
- Rename/Modify: `service/account_pool_xai_overrides_test.go` to `service/account_pool_outbound_overrides_test.go`
- Modify: `service/account_pool_service.go`
- Modify: `service/account_pool_runtime.go`
- Modify: `service/account_pool_runtime_test.go`
- Modify: `relay/common/relay_info.go`
- Modify: `relay/channel/openai/adaptor.go`
- Modify: `relay/channel/openai/adaptor_test.go`
- Modify: `relay/channel/claude/adaptor.go`
- Modify: `relay/channel/claude/adaptor_test.go`
- Modify: `relay/channel/gemini/adaptor.go`
- Modify: `relay/channel/gemini/vertex.go`
- Modify: `relay/channel/gemini/vertex_test.go`
- Modify: `relay/channel/xai/adaptor.go`
- Modify: `web/default/src/features/account-pools/lib/account-pool-form.ts`
- Modify: `web/default/src/features/account-pools/lib/account-pool-form.test.ts`
- Modify: `web/default/src/features/account-pools/index.tsx`

- [x] Add failing table tests for OpenAI, Anthropic, Gemini, Vertex, xAI, `grok_web` exclusion, SSRF rejection, and dangerous/oversized headers.
- [x] Implement the shared platform policy and switch create/update/runtime validation to it.
- [x] Add failing runtime tests proving account base/header values override channel values for every supported platform.
- [x] Apply generic runtime headers and provider-specific runtime base URL selection, including Vertex and Code Assist defaults.
- [x] Add failing frontend payload tests for every supported platform and `grok_web`; update the form visibility/copy and pass the tests.
- [x] Run provider/service/frontend focused tests and commit `feat(account-pools): generalize outbound overrides`.

### Task 3: Account-linked consume logs and rolling usage

**Files:**
- Modify: `model/log.go`
- Modify: `model/main.go`
- Modify: `model/clickhouse_log_test.go`
- Create: `model/log_account_pool_test.go`
- Modify: `service/account_pool_runtime.go`
- Modify: `service/account_pool_xai_quota.go`
- Modify: `service/account_pool_xai_quota_test.go`
- Modify: `service/account_pool_service.go`
- Modify: `dto/account_pool.go`
- Modify: `controller/account_pool.go`
- Modify: `web/default/src/features/account-pools/types.ts`
- Modify: `web/default/src/features/account-pools/lib/xai-quota.ts`
- Modify: `web/default/src/features/account-pools/lib/xai-quota.test.ts`
- Modify: `web/default/src/features/account-pools/index.tsx`

- [x] Add a failing model test that records a consume log under a selected account-pool context and asserts both persisted IDs.
- [x] Add indexed log fields, context fallback, and portable migrations; update ClickHouse create/upgrade SQL.
- [x] Add failing service tests where linked recent logs win and missing linked rows fall back to `counter_estimate`.
- [x] Implement bounded aggregate queries on `LOG_DB` and expose `logs_24h`/`counter_estimate` through DTO and UI.
- [x] Run model/service/frontend tests and commit `feat(account-pools): link consume logs to accounts`.

### Task 4: Distributed worker leases

**Files:**
- Create: `model/account_pool_worker_lease.go`
- Create: `model/account_pool_worker_lease_test.go`
- Modify: `model/main.go`
- Create: `service/account_pool_worker_lease.go`
- Create: `service/account_pool_worker_lease_test.go`
- Modify: `service/account_pool_xai_quota_worker.go`
- Modify: `service/account_pool_xai_quota_worker_test.go`
- Modify: `service/account_pool_xai_reconcile_worker.go`
- Modify: `service/account_pool_xai_reconcile_worker_test.go`

- [x] Write failing model tests for owner contention, same-owner reacquire, renewal, release, and expiry takeover.
- [x] Implement portable GORM lease acquisition/renewal/release and add both normal/fast migrations.
- [x] Write failing service tests that two owners cannot run the same tick and expired/lost leases cancel safely.
- [x] Add heartbeat orchestration and wrap both xAI maintenance tick functions.
- [x] Run model/service worker tests repeatedly and commit `feat(account-pools): coordinate workers with db leases`.

### Task 5: Bounded parallel SSO import

**Files:**
- Modify: `service/account_pool_xai_oauth.go`
- Modify: `service/account_pool_xai_oauth_test.go`

- [x] Add failing deterministic tests that measure maximum in-flight conversions, preserve input-ordered results, pass the resolved proxy, and redact failing token values.
- [x] Implement the default-three/max-eight converter pool with per-item and total deadlines, then create accounts sequentially from indexed outcomes.
- [x] Run SSO/OAuth tests repeatedly and commit `feat(account-pools): parallelize xai sso imports safely`.

### Task 6: Documentation, i18n, and release verification

**Files:**
- Modify via script: `web/default/src/i18n/locales/{en,zh,fr,ja,ru,vi}.json`
- Modify: `CHANGELOG.md`
- Modify: this design and plan only if implementation constraints require an explicit correction.

- [x] Populate all new UI strings in `web/default/scripts/add-missing-keys.mjs`, run it, delete it, and run `bun run i18n:sync`.
- [x] Update CHANGELOG Unreleased with the five shipped slices and their limitations.
- [ ] Run `gofmt`, focused Go tests, `go test ./...`, frontend unit tests, typecheck, lint on touched files, format check, and production build.
- [ ] Inspect `git diff --check`, status, commit history, and secret patterns; fix every in-scope issue and rerun failed verification.
- [x] Commit `docs(account-pools): document remaining enhancements` if documentation/i18n changes are not already included.
- [ ] Merge feature branch into current `main`, run `gh auth setup-git`, push the feature branch and `main`, and report both SHAs/push status.
