# Batch 3 Protocol and Administration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Complete request-scoped Responses compatibility, verified Codex field forwarding, correct Anthropic cache billing, an enabled-channel unset-price editor, and observational API-token concurrency statistics.

**Architecture:** Keep protocol conversion metadata in explicit per-request result/state values passed through request and response conversion. Extend the existing billing normalization and token response boundaries rather than adding parallel paths. Redis concurrency is a best-effort lease metric attached once around the authenticated client request; the default React administration UI derives unset-price rows from current channel/model and pricing drafts using pure functions.

**Tech Stack:** Go 1.22+, Gin, Redis ZSETs, Testify, React 19, TypeScript, Base UI, Tailwind CSS, Vitest, Bun.

## Global Constraints

- Start from the Batch 2 merged `main`; preserve current JSON wrappers, streaming conversion, Codex identity, account-pool retries, Redis namespace conventions, and the default frontend design system.
- Responses namespace and `tool_search` metadata is request-scoped only: no package globals and no persisted conversion metadata.
- Reject final-name ambiguity before relay; omit a missing/dropped tool choice.
- Port only demonstrated cache gaps; implement aggregate Anthropic cache-creation remainder and cache-only nonzero settlement without duplicate prompt/cache charges.
- Codex prompt-cache forwarding already exists; synchronize only fields proven dropped by `ResponsesHelper`.
- Redis concurrency is observational: errors never fail traffic, affect billing, or expose raw token strings.
- No backend/database change is permitted for unset-price administration.
- Use `common` JSON wrappers and checked quota conversion/clamp auditing; use Testify in new Go tests and Bun for frontend commands.
- Exclude account header overrides, heartbeat/SSE synthesis, quota probes, unverified Codex manifests, Grok video, and direct Vue/framework ports.

---

## File Map

- `service/relayconvert/responses_request_to_chat.go`: return converted chat request plus exact emitted-name/reverse-name metadata.
- `service/relayconvert/chat_to_responses_response.go`, `service/relayconvert/chat_to_responses.go`: consume request-scoped metadata when restoring non-stream and stream outputs.
- `relay/channel/advancedcustom/adaptor.go`: retain conversion metadata on `RelayInfo` for the current request only.
- `relay/common/relay_info.go`: request-owned conversion metadata field.
- `relay/responses_handler.go`: preserve every confirmed compact/Responses field when normalizing request types.
- `relay/channel/codex/adaptor_test.go`: prove Codex forwarding remains intact.
- `service/tiered_settle.go`: derive aggregate Anthropic cache-creation remainder and preserve cache-only settlement.
- `service/tiered_settle_test.go`, `service/text_quota_test.go`: billing regressions.
- `service/token_concurrency.go`: best-effort Redis ZSET lease acquire/release/count API.
- `middleware/auth.go`: acquire one lease after token authentication and release after the complete client request.
- `controller/token.go`, `controller/token_admin.go`, `dto/token.go`: response DTO and batched count enrichment.
- `web/default/src/features/system-settings/models/model-ratio-visual-editor.tsx`: unset-price tab and current draft behavior.
- `web/default/src/features/system-settings/models/model-pricing-snapshots.ts`: pure enabled-channel candidate derivation.
- `web/default/src/features/system-settings/models/model-ratio-table-columns.tsx`: unset-mode controls.
- `web/default/src/features/system-settings/models/ratio-settings-card.tsx`: current-design-system tab wiring.

### Task 1: Request-scoped namespace and tool_search request conversion

**Files:**
- Modify: `service/relayconvert/responses_request_to_chat.go:18-88,308-373`
- Modify: `service/openai_chat_responses_compat.go:12-14`
- Modify: `relay/channel/advancedcustom/adaptor.go:102-120`
- Modify: `relay/common/relay_info.go`
- Test: `service/relayconvert/responses_request_to_chat_test.go`
- Test: `relay/channel/advancedcustom/adaptor_test.go`

**Interfaces:**
- Produces: `type ResponsesChatConversion struct { Request *dto.GeneralOpenAIRequest; EmittedToolNames map[string]struct{}; ReverseToolNames map[string]string; ToolSearchProxyName string }`.
- Produces: `ResponsesRequestToChatCompletionsRequest(req *dto.OpenAIResponsesRequest) (*ResponsesChatConversion, error)`.
- Consumes: Responses tools and `tool_choice`; stores only the returned metadata on the current `RelayInfo`.

- [ ] **Step 1: Write failing table tests** covering nested namespace flattening, normalization collision rejection, safe synthetic `tool_search`, unsafe proxy omission, and omission of a `tool_choice` whose target was not emitted. Assert exact emitted names and reverse mappings with `require.NoError`/`assert.Equal`.
- [ ] **Step 2: Verify failure** with `go test ./service/relayconvert -run 'TestResponsesRequestToChat_(Namespace|ToolSearch|ToolChoice)' -count=1`; expect compile failures for `ResponsesChatConversion` and behavioral failures for flattened tools.
- [ ] **Step 3: Implement minimal conversion result**. Parse namespace children, normalize the final callable name once, reject duplicate final names, add only collision-free callable tools, conditionally append the synthetic proxy, and filter choice against `EmittedToolNames`. Use `common.Unmarshal`/`common.Marshal`; do not add global maps.
- [ ] **Step 4: Thread metadata through the advanced-custom adaptor** by updating the service wrapper and assigning the conversion result to a request-owned `RelayInfo` field immediately before returning `conversion.Request`.
- [ ] **Step 5: Verify focused and package tests** with `go test ./service/relayconvert ./relay/channel/advancedcustom -count=1`; expect PASS.
- [ ] **Step 6: Commit** with `git add service/relayconvert/responses_request_to_chat.go service/relayconvert/responses_request_to_chat_test.go service/openai_chat_responses_compat.go relay/channel/advancedcustom/adaptor.go relay/channel/advancedcustom/adaptor_test.go relay/common/relay_info.go && git commit -m "feat: bridge responses namespace tools through chat"`.

### Task 2: Restore namespace and tool_search metadata in Responses output

**Files:**
- Modify: `service/relayconvert/chat_to_responses_response.go`
- Modify: `service/relayconvert/chat_to_responses.go`
- Modify: `relay/channel/openai/responses_via_chat.go:19-130`
- Test: `service/relayconvert/chat_responses_compat_test.go`
- Test: `relay/channel/openai/chat_via_responses_test.go`

**Interfaces:**
- Consumes: Task 1 `ReverseToolNames` and `ToolSearchProxyName` from the current request.
- Produces: response conversion functions/state constructors accepting immutable conversion metadata; restored namespace/tool-search items in buffered and SSE output.

- [ ] **Step 1: Add failing non-stream and stream tests** with two independent conversion metadata values executed in parallel. Assert each function call is restored to its own namespace and synthetic proxy calls become exact Responses `tool_search` metadata without cross-request leakage.
- [ ] **Step 2: Verify failure** with `go test ./service/relayconvert ./relay/channel/openai -run 'Test.*(Namespace|ToolSearch|RequestScoped)' -count=1`; expect wrong flat names or missing metadata.
- [ ] **Step 3: Extend response/state signatures** to accept a copied immutable metadata value. Apply reverse mapping when constructing function-call output items and convert only the recorded proxy name to `tool_search`; unknown names remain ordinary functions.
- [ ] **Step 4: Pass metadata from `RelayInfo`** in both `OaiChatToResponsesHandler` and `OaiChatToResponsesStreamHandler`; never persist it on an adaptor.
- [ ] **Step 5: Run race and regression tests**: `go test -race ./service/relayconvert ./relay/channel/openai -count=1`; expect PASS.
- [ ] **Step 6: Commit** with `git add service/relayconvert/chat_to_responses_response.go service/relayconvert/chat_to_responses.go service/relayconvert/chat_responses_compat_test.go relay/channel/openai/responses_via_chat.go relay/channel/openai/chat_via_responses_test.go && git commit -m "feat: restore responses tool namespace metadata"`.

### Task 3: Synchronize only demonstrably dropped Responses/Codex fields

**Files:**
- Modify: `relay/responses_handler.go:37-48`
- Test: `relay/responses_handler_test.go`
- Test: `relay/channel/codex/adaptor_test.go`

**Interfaces:**
- Consumes/produces: existing `dto.OpenAIResponsesRequest`; no new protocol mapping.
- Preserves: metadata, reasoning mode/context, tools, parallel tool calls, reasoning, service tier, prompt cache key, and text across compact normalization; existing Codex prompt-cache forwarding stays authoritative.

- [ ] **Step 1: Add a failing compact-normalization test** whose `OpenAIResponsesCompactionRequest` populates every field present in both DTOs and asserts the adaptor receives exact raw JSON/pointer presence, including explicit `false`. Add a Codex regression asserting prompt-cache key is unchanged.
- [ ] **Step 2: Verify failure** with `go test ./relay ./relay/channel/codex -run 'Test.*(Compact.*Fields|PromptCache)' -count=1`; expect the fields omitted by the four-field literal at `responses_handler.go:42-47` to be empty.
- [ ] **Step 3: Replace the lossy literal** with explicit assignments for only confirmed shared fields (or a tested common conversion method). Do not invent cache mappings and do not alter Codex identity/header policy.
- [ ] **Step 4: Run tests**: `go test ./relay ./relay/channel/codex -count=1`; expect PASS.
- [ ] **Step 5: Commit** with `git add relay/responses_handler.go relay/responses_handler_test.go relay/channel/codex/adaptor_test.go && git commit -m "fix: preserve confirmed responses compact fields"`.

### Task 4: Correct Anthropic aggregate cache creation and cache-only settlement

**Files:**
- Modify: `service/tiered_settle.go:20-87`
- Test: `service/tiered_settle_test.go:414-680`
- Test: `service/text_quota_test.go`

**Interfaces:**
- Consumes: `dto.Usage.PromptTokensDetails.CachedCreationTokens`, `ClaudeCacheCreation5mTokens`, and `ClaudeCacheCreation1hTokens`.
- Produces: `billingexpr.TokenParams` where `CC1h` is explicit 1h, `CC` is explicit 5m plus `max(0, aggregate-explicit5m-explicit1h)`, and `Len` includes cache creation exactly once.

- [ ] **Step 1: Add failing deterministic tests** for aggregate-only usage, aggregate greater than tier details (remainder charged as `cc`), aggregate equal to details (no remainder), aggregate smaller than details (zero remainder), and a cache-only response (`P=0`, `C=0`) yielding nonzero actual quota. Assert exact `TokenParams` and settlement quota.
- [ ] **Step 2: Verify failure** with `go test ./service -run 'TestBuildTieredTokenParams_Claude_(Aggregate|Remainder)|TestTryTieredSettle_CacheOnly' -count=1`; expect `CC == 0` or zero settlement for aggregate-only/cache-only cases.
- [ ] **Step 3: Implement the remainder inline** in `BuildTieredTokenParams`: start from aggregate `CachedCreationTokens`, subtract nonnegative explicit 5m/1h details with a floor at zero, add the remainder to 5m `CC`, and compute `Len` from the reconciled values. Keep all categories separate so prompt and cache creation are never both charged.
- [ ] **Step 4: Ensure text settlement does not short-circuit solely on zero prompt/completion tokens**; tiered settlement must run when any `CR`, `CC`, or `CC1h` token is nonzero. Preserve `TryTieredSettle` checked quota/clamp flow.
- [ ] **Step 5: Run focused and regression tests**: `go test ./service ./pkg/billingexpr -count=1`; expect PASS.
- [ ] **Step 6: Commit** with `git add service/tiered_settle.go service/tiered_settle_test.go service/text_quota_test.go && git commit -m "fix: reconcile anthropic cache creation billing"`.

### Task 5: Add observational API-token Redis leases and response DTO counts

**Files:**
- Create: `service/token_concurrency.go`
- Create: `service/token_concurrency_test.go`
- Modify: `middleware/auth.go:303-445`
- Modify: `controller/token.go:38-99`
- Modify: `controller/token_admin.go:40-90`
- Create: `dto/token.go`
- Test: `middleware/auth_test.go`
- Test: `controller/token_test.go`

**Interfaces:**
- Produces: `AcquireTokenConcurrencyLease(ctx context.Context, tokenID int) func()`; returned release is idempotent and always safe.
- Produces: `GetTokenConcurrencyCounts(ctx context.Context, tokenIDs []int) map[int]int`; missing/error entries are zero.
- Produces: `dto.TokenResponse` embedding/copying safe token fields plus `Concurrency int json:"concurrency"`.

- [ ] **Step 1: Write failing service tests** using the project Redis test fixture: acquire increments, idempotent release removes once, expiry removes abandoned leases, batched counts span tokens, and Redis failure returns a no-op release/zero counts.
- [ ] **Step 2: Verify failure** with `go test ./service -run TestTokenConcurrency -count=1`; expect undefined APIs.
- [ ] **Step 3: Implement ZSET leases** under a namespaced key containing token ID only. Use a random lease member, prune expired scores before count, pipeline batched reads, and make every Redis error observational.
- [ ] **Step 4: Add middleware lifecycle tests** for success, handler error, panic cleanup, cancellation, stream EOF/error, and WebSocket close. Assert one acquire per authenticated client request and no reacquire from upstream retry execution.
- [ ] **Step 5: Wrap authenticated request lifetime** after `token_id` is established, using `defer release()` around `c.Next()`/panic propagation. Keep account-pool retry code untouched.
- [ ] **Step 6: Add DTO/controller tests**, replace model-return masking with `dto.TokenResponse`, batch-load counts once for list/search/admin endpoints, and prove keys remain masked and Redis failures still return HTTP success.
- [ ] **Step 7: Run race/regression tests**: `go test -race ./service ./middleware ./controller -run 'Token|Concurrency' -count=1`; expect PASS.
- [ ] **Step 8: Commit** with `git add service/token_concurrency.go service/token_concurrency_test.go middleware/auth.go middleware/auth_test.go dto/token.go controller/token.go controller/token_admin.go controller/token_test.go && git commit -m "feat: report api token live concurrency"`.

### Task 6: Add enabled-channel unset-price administration tab

**Files:**
- Modify: `web/default/src/features/system-settings/models/model-pricing-snapshots.ts`
- Create: `web/default/src/features/system-settings/models/model-pricing-snapshots.test.ts`
- Modify: `web/default/src/features/system-settings/models/model-ratio-visual-editor.tsx:97-776`
- Modify: `web/default/src/features/system-settings/models/model-ratio-table-columns.tsx:41-120`
- Modify: `web/default/src/features/system-settings/models/ratio-settings-card.tsx:131-410`
- Test: `web/default/src/features/system-settings/models/model-ratio-visual-editor.test.tsx`
- Modify: `web/default/src/i18n/locales/{en,zh,fr,ru,ja,vi}.json`

**Interfaces:**
- Produces: `deriveUnsetPriceModels(enabledChannelModels: string[], snapshot: ModelPricingSnapshot): ModelRow[]` using the existing base-price predicate.
- Consumes: current saved snapshot plus unsaved drafts; no API/schema/backend changes.

- [ ] **Step 1: Add failing pure-function tests** proving disabled-channel-only models are excluded, the existing base-price predicate determines unset state, explicit zero/free pricing is set (not unset), and duplicate channel models collapse to one row.
- [ ] **Step 2: Verify failure** from `web/default` with `bun test src/features/system-settings/models/model-pricing-snapshots.test.ts`; expect undefined `deriveUnsetPriceModels`.
- [ ] **Step 3: Implement derivation** by intersecting normalized enabled-channel model names with the current pricing snapshot and filtering through the existing base-price predicate; preserve explicit zero.
- [ ] **Step 4: Add failing component tests** for unset tab controls: destructive/raw controls hidden, edit and batch copy enabled, save removes newly priced rows, drafts survive tab switches, memo-equal props do not reset drafts, and page index clamps after filtering/save.
- [ ] **Step 5: Wire the tab with current components** (`Tabs`, existing data table, sheet, buttons, pagination). Add an explicit `mode: 'configured' | 'unset'` prop to columns/editor; do not port classic/Semi UI.
- [ ] **Step 6: Add translated user-facing strings** to all six locale files and run `bun run i18n:sync`.
- [ ] **Step 7: Run frontend verification**: `bun test src/features/system-settings/models/model-pricing-snapshots.test.ts src/features/system-settings/models/model-ratio-visual-editor.test.tsx && bun run typecheck && bun run build`; expect all PASS.
- [ ] **Step 8: Commit** with `git add web/default/src/features/system-settings/models/model-pricing-snapshots.ts web/default/src/features/system-settings/models/model-pricing-snapshots.test.ts web/default/src/features/system-settings/models/model-ratio-visual-editor.tsx web/default/src/features/system-settings/models/model-ratio-visual-editor.test.tsx web/default/src/features/system-settings/models/model-ratio-table-columns.tsx web/default/src/features/system-settings/models/ratio-settings-card.tsx web/default/src/i18n/locales && git commit -m "feat: add unset model pricing administration"`.

### Task 7: Integrated verification

**Files:**
- Modify only files required by failures attributable to Tasks 1-6.

**Interfaces:**
- Consumes all prior task contracts; produces a release-ready Batch 3 implementation.

- [ ] **Step 1: Run backend tests**: `go test ./service/relayconvert ./relay/channel/advancedcustom ./relay/channel/openai ./relay/channel/codex ./relay ./service ./middleware ./controller -count=1`; expect PASS.
- [ ] **Step 2: Run targeted race tests**: `go test -race ./service/relayconvert ./relay/channel/openai ./service ./middleware -run 'Namespace|ToolSearch|RequestScoped|TokenConcurrency|CacheOnly|Aggregate' -count=1`; expect PASS with no race report.
- [ ] **Step 3: Run frontend checks** from `web/default`: `bun test && bun run typecheck && bun run build`; expect PASS.
- [ ] **Step 4: Audit exclusions and state**: `git diff main...HEAD --name-only`; confirm no migration/database file, account-header override, speculative cache mapping, model manifest, Grok video, classic frontend, or protected identity change.
- [ ] **Step 5: Commit only if verification required a fix**, staging the exact corrected files and using `git commit -m "test: verify batch 3 protocol administration"`; otherwise leave the independently reviewable commits from Tasks 1-6 unchanged.
