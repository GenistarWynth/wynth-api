# new-api upstream synchronization completion handoff

> Claude handoff: this file is the completed, current-state handoff for the
> isolated synchronization worktree. It supersedes the historical
> `docs/wynth/NEW_API_UPSTREAM_SYNC_HANDOFF.md` for execution status without
> modifying that untracked file in the protected original checkout. Use this
> report together with the untracked `SYNC_PROGRESS.md`; do not resume Batch 6B
> or start another batch against the frozen `7c28993f` audit unless the user
> explicitly opens a separate follow-up.

STATUS: DONE_WITH_CONCERNS

## Workspace

- Repo root: E:/Documents/Projects/wynth-api
- Worktree: E:/Documents/Projects/wynth-api/.claude/worktrees/new-api-upstream-sync-2026-07
- Branch: feat/new-api-upstream-sync-2026-07
- HEAD: documentation handoff commit (implementation tip: 91c529316)
- Base origin/main: 24ce34e7d961e14be4891b5c2bba9f47ac1d29ca
- Audited upstream tip: 7c28993f6bd9e92616f3f578212577f8b7c40b45
- Current refs after final fetch:
  - origin/main: 24ce34e7d961e14be4891b5c2bba9f47ac1d29ca
  - upstream/main: 5a6c53d4966b2e34690ab49f3dd19be01c88fdbe
  - origin/upstream merge base: 1d166532fe954a45207dffd2924697796a984159
  - audited tip is an ancestor of current upstream/main: yes
  - upstream...origin: 130 upstream-only commits, 461 origin-only commits
- Dirty state at the final verification checkpoint: the tracked tree was clean.
  Before the documentation commit, the only untracked entries were the
  intentionally preserved `.codegraph/`, `CODEX_SYNC_CONTINUATION.md`,
  `SYNC_PROGRESS.md`, and this completion report. This report is the one
  explicitly staged documentation artifact; keep the other three untracked and
  do not auto-stage them while consuming this handoff. Ignored build artifacts
  are separate and are not staged.
- The original checkout remains outside this worktree and must not be touched:
  its existing dirty files are `.gitignore`, `docker-compose.yml`, the
  `.codex/*.png` screenshots, and the historical untracked
  `docs/wynth/NEW_API_UPSTREAM_SYNC_HANDOFF.md`. Do not copy changes back there,
  clean it, or use it as the implementation checkout.

The audited milestone was kept at 7c28993f. The newer upstream range contains 10 commits. It includes the later Codex model-discovery successor 57746fc9726f190ee7948af7b2034e51e5551f45 and other unrelated changes; that range is recorded for a separate audit and was not expanded into this 95-item synchronization.

## Implemented batches

| Batch | Local commit(s) | Upstream coverage and result |
|---|---|---|
| 1 | 985053d37, 56e7ac671 | Codex request schema, headers, and sticky state. |
| 2 | 8c5348752, 64283bef9, 840331f39 | GPT-5.6 model and cache-write billing contracts. |
| 3 | d83b4d421 | Compact request/response compatibility and cache semantics. Live compact field decision remains deferred. |
| 4 | 74df947b4, 34136fca9, ed56f94c8, ace345003, f57574b63 | Disconnect-safe streams, image billing reconciliation, and worker lifecycle. |
| 5 | 06d942f31 | Codex field passthrough controls. |
| 6A | 6ac57635b, 0ce6069af, e641466ff, be0dc7630, ab47f094b, 92b32bfb4 | Session, token, email, OAuth, cookie, and authorization hardening. |
| 6B | 244c48ddb | Outbound destination validation at dial time, DNS/address-range checks, and redirect safety. |
| 6C | 2d395ba9b | 401-only session expiry, sign-up redirect, and OAuth callback copy/copy action. |
| 6D | d2ddd679e | Graceful shutdown and user/accounting cache hardening. |
| 7A | 97705fef9 | Wan 2.7 media mapping and task quota persistence. |
| 7B | 12a06794f | Seedance 2.0 request fields and resolution/video billing. |
| 7C | 6df32f895 | Ollama non-stream tool calls. |
| 7D | 04e03452e | Tiered estimates, filtered ratios, pre-consume saturation, and error reporting. |
| 7E | e57047a49 | Synchronous quota logging and startup ordering. |
| 7F | c8c5ed92c | Configured quota units for reward transfers. |
| 7G | 07f2a8467 | Redemption, subscription, stale-instance, model, and admin management. |
| 8 | no code commit | c36418c deferred with evidence; see the disposition table and Batch 8 section below. |
| 9A | c538b045f | Saturation boundaries, unset-price locale copy, and surviving FaSlack change. |
| 9B | 74de97dab | Channel management, advanced route editing, table sizing, pricing sync, and dialog behavior. |
| 9C | 23434a377 | Isolated HTML, Shadow DOM theme propagation, iframe preferences, route continuity, and browser-translation guards. |
| 9 pricing | bc3023749 | Selected-group pricing, minimum-ratio fallback, and explicit zero ratio handling. |
| 9E | c73603717 | Locale normalization, decimal ratio drafts, referral copy, and usage-log badge alignment. |
| 9F | d6771bdf2 | Playground model/group selector behavior and request parameter controls. |
| 9 mobile | a740940b2 | Stable mobile user-card field order. |
| 9D | 29b7bb6d6 | Make/Rsbuild/Tailwind/dependency/workflow/docs synchronization with Wynth registry preservation. |
| 9 classic | da20d613f | Classic maintenance banner, default-theme switch, shared switch helper, and audit-copy continuity. |
| 7 nested usage | 2cade116a | Nested reasoning/cache usage-token details with legacy fallback. |
| Codex model follow-up | 8767b0830 | Completes audited Codex model advertisement and adds evidence-backed GPT-5.4-mini/GPT-5.5 options. |
| 9 group editor | bdcd620d5 | Wynth adaptation of group visibility/JSON parsing with six-locale coverage. |
| 9 timing | 91c529316 | Dynamic pricing density and stream/timing metrics across desktop and mobile. |

All implemented commits were locally reviewed. No push, pull request, merge, or deployment was performed.

## 95-item disposition

The table below is keyed directly from Appendix A and Appendix B of the audited handoff. Trailer ancestry is only a cross-check; each ported row also has the batch behavior/tests/review evidence recorded in SYNC_PROGRESS.md.

| # | Upstream | Update | Disposition | Evidence |
|---:|---|---|---|---|
| 1 | 25f998595d2d | feat: refine channel management UI | PORTED_AND_VERIFIED | local 74de97dab; batch tests/review |
| 2 | 43591fba7762 | feat: improve advanced custom route editor | PORTED_AND_VERIFIED | local 74de97dab; batch tests/review |
| 3 | fda8177864d7 | fix(web): custom HTML style filtering and layout spacing | PORTED_AND_VERIFIED | local 23434a377; batch tests/review |
| 4 | 1f4d8d2b2681 | fix(web): inject app styles into isolated HTML | PORTED_AND_VERIFIED | local 23434a377; batch tests/review |
| 5 | 759ab6bbca57 | fix: keep page state when switching tabs within the same route | PORTED_AND_VERIFIED | local 23434a377; batch tests/review |
| 6 | c1903607d5c1 | fix: persist channel status filter across page navigation | PORTED_AND_VERIFIED | local 74de97dab; batch tests/review |
| 7 | b35dfa32efad | perf(channels): streamline channel test dialog layout | PORTED_AND_VERIFIED | local 74de97dab; batch tests/review |
| 8 | c5600f9b11b9 | perf(channels): compact model test row actions | PORTED_AND_VERIFIED | local 74de97dab; batch tests/review |
| 9 | 86021d8ed23f | refine default web UI and backend sync handling | PORTED_AND_VERIFIED | local da20d613f; batch tests/review |
| 10 | 4ae341756ebf | fix(channels): show field passthrough controls for Codex | PORTED_AND_VERIFIED | local 06d942f31; batch tests/review |
| 11 | f52b52b16779 | fix: align dynamic pricing style with log details sections | PORTED_AND_VERIFIED | local 91c529316; focused tests/review |
| 12 | 2281c9e3d878 | fix(web): refine mobile user cards | PORTED_AND_VERIFIED | local a740940b2; focused tests/review |
| 13 | 2f91d8ccb329 | fix(web): sync home iframe theme and language | PORTED_AND_VERIFIED | local 23434a377; focused tests/review |
| 14 | 17465b855fae | fix(html): Shadow DOM light/dark-mode behavior | PORTED_AND_VERIFIED | local 23434a377; focused tests/review |
| 15 | 1e80ce03e859 | feat: optimize legacy top-up warning copy | PORTED_AND_VERIFIED | local da20d613f; frontend checks/review |
| 16 | fc26b88fd131 | feat(group): improve group ratio editor visibility and JSON parsing | PORTED_AND_VERIFIED | local bdcd620d5; 6/6 focused tests, six locales, review |
| 17 | d1abf78ec09a | localize the new UI to zh-TW | INTENTIONAL_OMISSION | Wynth owns six locales en/zh/fr/ru/ja/vi; zh-TW and zh-Hant-TW normalize to zh; adding a seventh resource would expand the explicit maintenance contract. |
| 18 | 8f31b30598dd | fix(i18n): standardize locale formatting for Intl APIs | PORTED_AND_VERIFIED | local c73603717; focused tests/review |
| 19 | becc18e3007e | fix(i18n): add Chinese locale detection mapping | PORTED_AND_VERIFIED | local c73603717; focused tests/review |
| 20 | 394b023dbf98 | fix: keep group ratio input as a string draft for decimal typing | PORTED_AND_VERIFIED | local c73603717; focused tests/review |
| 21 | 57865fc1f85a | fix: restore default channel connection paste | PORTED_AND_VERIFIED | local 74de97dab; focused tests/review |
| 22 | 28e0115a08f6 | fix(web): prevent browser translation from mutating React roots | PORTED_AND_VERIFIED | local 23434a377; focused tests/review |
| 23 | 97bbb7c8cd71 | feat(pricing): dynamic pricing group selection | PORTED_AND_VERIFIED | local bc3023749; 3/3 TDD and focused checks |
| 24 | 8739c05c0e2a | feat(web): manually resizable channel-list columns | PORTED_AND_VERIFIED | local 74de97dab; focused tests/review |
| 25 | df01273b94d7 | fix(web): let resized tables fill available width | PORTED_AND_VERIFIED | local 74de97dab; focused tests/review |
| 26 | a79f96919ee4 | fix(affiliate): update referral message | PORTED_AND_VERIFIED | local c73603717; six-locale checks/review |
| 27 | 4645ad9df51d | fix(playground): keep model selector lists in sync | PORTED_AND_VERIFIED | local d6771bdf2; focused tests/review |
| 28 | 928b4750753d | feat(playground): add chat parameter settings panel | PORTED_AND_VERIFIED | local d6771bdf2; focused tests/review |
| 29 | e8596cab7e56 | fix: allow custom model names that differ only by case | PORTED_AND_VERIFIED | local 74de97dab; focused tests/review |
| 30 | 489c045842a3 | perf(model-pricing): optimize upstream price sync table | PORTED_AND_VERIFIED | local 74de97dab; focused tests/review |
| 31 | 43783286e576 | fix(model-pricing): polish sync-channel dialog layout | PORTED_AND_VERIFIED | local 74de97dab; focused tests/review |
| 32 | ca971413e9a6 | fix(web): user-activated top navigation for custom home iframe | PORTED_AND_VERIFIED | local 23434a377; focused tests/review |
| 33 | 6bbddb104637 | feat(timing): stream timing metrics display and localization | PORTED_AND_VERIFIED | local 91c529316; 7/7 focused tests and review |
| 34 | 162f87925cc7 | feat: update theme colors | SUPERSEDED | Wynth's neutral default theme, ten persisted color presets, cookie/DOM theme provider, and config drawer provide a different maintained theme contract; theme.css and theme-presets.css were intentionally untouched. |
| 35 | 1250fb2eb514 | fix: StatusBadge margin in log columns | PORTED_AND_VERIFIED | local c73603717; focused checks/review |
| 36 | bde9b2f44887 | fix: harden unset-price-models batch copy, feedback, and memo equality | PORTED_AND_VERIFIED | local c538b045f; focused checks/review |
| 37 | c8491b41bc44 | feat: bill Doubao Seedance 2.0 by output resolution and video input | PORTED_AND_VERIFIED | local 12a06794f; focused tests/review |
| 38 | e514db20f762 | feat: Seedance 2.0 safety_identifier, priority, and 4K billing | PORTED_AND_VERIFIED | local 12a06794f; focused tests/review |
| 39 | 52858ad1e617 | feat: support Wan2.7 i2v media mapping | PORTED_AND_VERIFIED | local 97705fef9; focused tests/review |
| 40 | 8874d1929f97 | make quota logging synchronous and delay startup log | PORTED_AND_VERIFIED | local e57047a49; focused tests/review |
| 41 | 0977965d933f | fix: handle Ollama non-stream tool calls | PORTED_AND_VERIFIED | local 6df32f895; focused tests/review |
| 42 | aa334c0850b1 | fix(ai-elements): read nested usage-token details | PORTED_AND_VERIFIED | local 2cade116a; focused tests/review |
| 43 | 043720f9bebf | fix: task delta settlement quota and Ali video duration | PORTED_AND_VERIFIED | local 97705fef9; focused tests/review |
| 44 | 153d7f01a27c | fix: avoid stale stream writes after client disconnect | PORTED_AND_VERIFIED | local f57574b63 and stream follow-ups; focused tests/review |
| 45 | 3fbad6a72f77 | fix(price): default token estimate for tiered-expression pre-consume | PORTED_AND_VERIFIED | local 04e03452e; billing tests/review |
| 46 | fc1259f58366 | refactor(price): improve other-ratio handling in PriceData | PORTED_AND_VERIFIED | local 04e03452e; billing tests/review |
| 47 | 90fa6fe6b645 | fix(wallet): honor configured quota units for reward transfers | PORTED_AND_VERIFIED | local c8c5ed92c; focused tests/review |
| 48 | 6ce7305cd36f | feat(price): add token ratios for GPT-5.6 models | PORTED_AND_VERIFIED | local 64283bef9; billing tests/review |
| 49 | 4e570389dd43 | fix: use GORM v2 row locking for subscription resets | PORTED_AND_VERIFIED | local 07f2a8467; database/package tests/review |
| 50 | 621927f710b4 | fix(billing): reject saturated pre-consume quota | PORTED_AND_VERIFIED | local 04e03452e; billing tests/review |
| 51 | d9595831bf05 | fix(billing): improve pre-consume quota and error reporting | PORTED_AND_VERIFIED | local 04e03452e; billing tests/review |
| 52 | dad57a6bb85b | fix: sync Codex field | PORTED_AND_VERIFIED | local 8767b0830; model-list TDD and Codex package tests |
| 53 | 269e4ff39059 | feat(image): image stream disconnect and billing handling | PORTED_AND_VERIFIED | local 34136fca9; stream/image tests/review |
| 54 | c36418c86329 | feat: text protocol conversion and advanced custom routing | DEFERRED_WITH_EVIDENCE | The registry rewrite changes about 115 files and would remove Wynth account-pool runtime ownership, current Claude/Gemini OAuth behavior, and namespace/tool_search metadata restoration. Baseline converter suite passed; no partial rewrite was made. |
| 55 | 48068ce9236e | feat: bill OpenAI cache_write_tokens at cache-creation price | PORTED_AND_VERIFIED | local d83b4d421 and cache-write follow-ups; focused tests/review |
| 56 | 92d3c9d18fc6 | fix: uncached remainder and compact prompt_cache_key | PORTED_AND_VERIFIED | local d83b4d421; compact/cache tests/review |
| 57 | 0565e626793d | fix: only 401 expires a session in the auth guard | PORTED_AND_VERIFIED | local 2d395ba9b; frontend tests/review |
| 58 | bfddc5fea0ba | fix: omit access_token from user queries | PORTED_AND_VERIFIED | local 6ac57635b; backend tests/review |
| 59 | dfc0d6324b40 | user, subscription, cache, and router hardening despite its misleading title | PORTED_AND_VERIFIED | local d2ddd679e; user/cache/shutdown tests/review |
| 60 | bed4a3f91612 | fix(user): trim whitespace from username and validate input | PORTED_AND_VERIFIED | local 6ac57635b; backend tests/review |
| 61 | 5fc35e28a253 | fix(user): harden account email and password handling | PORTED_AND_VERIFIED | local 6ac57635b plus review remediations; backend tests/review |
| 62 | 0d5995eb63f8 | fix(auth): allow read-only access for non-disabled tokens | PORTED_AND_VERIFIED | local 6ac57635b; auth tests/review |
| 63 | 56dbaab1d479 | feat(session): support opt-in Secure session cookies | PORTED_AND_VERIFIED | local 6ac57635b; auth tests/review |
| 64 | 4a64b87072ca | test(user): self-service password update guard | PORTED_AND_VERIFIED | local 6ac57635b; auth tests/review |
| 65 | 1e11dfcfb574 | feat(user): better redeem failure messages | PORTED_AND_VERIFIED | local 6ac57635b; auth tests/review |
| 66 | df087b022dba | feat(ssrf): SSRF protection in HTTP clients and validation | PORTED_AND_VERIFIED | local 244c48ddb; deterministic destination/redirect tests/review |
| 67 | 3a876d6f31b5 | fix(web): redirect authenticated users away from sign-up | PORTED_AND_VERIFIED | local 2d395ba9b; frontend tests/review |
| 68 | 6a437a337ded | feat(oauth): OAuth callback URL display and copy | PORTED_AND_VERIFIED | local 2d395ba9b; frontend tests/review |
| 69 | 986d90ae046f | graceful shutdown | PORTED_AND_VERIFIED | local d2ddd679e; shutdown/accounting tests/review |
| 70 | 12603a7765cf | fix(redemption): status filtering and cleanup action | PORTED_AND_VERIFIED | local 07f2a8467; admin tests/review |
| 71 | 81808d2410fb | remove sample special groups leaking into pricing | PORTED_AND_VERIFIED | local 07f2a8467; admin/pricing tests/review |
| 72 | 9b93d61b7fce | feat(subscription): admin quota-reset actions | PORTED_AND_VERIFIED | local 07f2a8467; admin tests/review |
| 73 | a72e5082e9f3 | feat(system-info): stale-instance cleanup actions | PORTED_AND_VERIFIED | local 07f2a8467; admin tests/review |
| 74 | 246d62aa5ed3 | remove dead files resurrected by the launch commit | PORTED_AND_VERIFIED | local 07f2a8467; tree/admin review |
| 75 | e40061965697 | stale-instance handling and theme update | PORTED_AND_VERIFIED | local 07f2a8467; stale-instance/theme behavior review |
| 76 | 7a2b9d86e8d9 | model search with status and sync filters | PORTED_AND_VERIFIED | local 07f2a8467; model/admin tests/review |
| 77 | f9165e7bfaab | fix(dev): run only the default frontend in dev-web | PORTED_AND_VERIFIED | local 29b7bb6d6; build/dependency review |
| 78 | e1fd9cc282c2 | chore(build): align make targets with web naming | PORTED_AND_VERIFIED | local 29b7bb6d6; build/dependency review |
| 79 | 12fc01006023 | bump Electron lockfile dependencies | PORTED_AND_VERIFIED | local 29b7bb6d6; frozen Bun install/build |
| 80 | 5bf346836273 | chore: run Bun format over frontend code | INTENTIONAL_OMISSION | Formatting-only repository churn; scoped changed-file oxfmt checks passed and no broad rewrite was needed. |
| 81 | 95e8c5eecff5 | perf(web): optimize Rsbuild and Tailwind build pipeline | PORTED_AND_VERIFIED | local 29b7bb6d6; default build passed |
| 82 | bff701b0cda2 | docs: update AGENTS.md | INTENTIONAL_OMISSION | The only product-tree change is an agent commentary instruction, not behavior, and it conflicts with the active execution contract. |
| 83 | 69c4d83df403 | chore(deps): bump golang.org/x/net from 0.50.0 to 0.55.0 | PORTED_AND_VERIFIED | local 29b7bb6d6; go mod verify/full tests |
| 84 | 1dcb389d008e | chore(deps): bump golang.org/x/image from 0.38.0 to 0.41.0 | PORTED_AND_VERIFIED | local 29b7bb6d6; go mod verify/full tests |
| 85 | a6c020125716 | chore(deps): update default-web dependencies | PORTED_AND_VERIFIED | local 29b7bb6d6; frozen Bun install/typecheck/build |
| 86 | 70c0b37eec6e | chore(deps): remove unused date-fns dependency | PORTED_AND_VERIFIED | local 29b7bb6d6; dependency review |
| 87 | 55858f353c95 | feat: manual Docker image publishing workflow | PORTED_AND_VERIFIED | local 29b7bb6d6; dual-registry workflow review |
| 88 | a13010394810 | chore(makefile): rename backend targets to api | PORTED_AND_VERIFIED | local 29b7bb6d6; Make/build checks |
| 89 | 5cbb7b0be17c | docs: update system architecture requirements | PORTED_AND_VERIFIED | local 29b7bb6d6; docs review |
| 90 | 8bc4bf1d6b1f | feat(docker): cosign manifest signing and permissions | PORTED_AND_VERIFIED | local 29b7bb6d6; workflow/package checks |
| 91 | 2f5f6ba84f1a | feat: prepare for 5.6 | PORTED_AND_VERIFIED | local 8c5348752; GPT-5.6 cache/model tests |
| 92 | 00f1cbb6df2c | chore(deps): bump golang.org/x/crypto from 0.51.0 to 0.52.0 | PORTED_AND_VERIFIED | local 29b7bb6d6; go mod verify/full tests |
| 93 | bae799ccb147 | billing/audit boundary and locale omissions remain | PORTED_AND_VERIFIED | local c538b045f; billing/i18n tests |
| 94 | 8283df169ebf | feature code exists, locale additions remain missing | PORTED_AND_VERIFIED | local c538b045f; i18n check |
| 95 | 997926bbe1cc | only the surviving FaSlack change remains missing; do not restore the reverted date-fns alias | PORTED_AND_VERIFIED | local c538b045f; classic source check and frontend review |

Disposition count: 90 ported and verified, 1 superseded, 4 intentionally omitted or deferred with evidence, 0 open.

## Batch 8 deferral evidence

Upstream c36418c8632912377010a903bdcc9672dde1c22d is intentionally deferred, not forgotten. The change rewrites roughly 115 files and would remove Wynth account-pool runtime ownership in the Claude/Gemini handlers, current provider OAuth/Bearer/Code Assist paths, namespace/tool_search flattening/restoration, response metadata, and SetActualResponseModel plumbing. The existing converter suite passed at the decision checkpoint. The continuation stop condition explicitly permits this evidence-backed deferral; a partial registry migration was not attempted.

## Carried-over notes

### Codex model advertisement

The Codex channel now advertises gpt-5.4-mini, gpt-5.5, gpt-5.6-sol, gpt-5.6-terra, and gpt-5.6-luna, plus their existing compact variants. The audited dad57a6bb85b diff requires the three GPT-5.6 models. Official current Codex model documentation confirms GPT-5.5 and GPT-5.4-mini are available to Codex CLI/IDE. The public gpt-5.6 alias is intentionally not in the specialized channel fallback: the audited and later Codex discovery upstream lists do not establish direct ChatGPT-backend alias acceptance, and no live credential was available to test it.

### Compact decision gate

Status: DEFERRED_WITHOUT_LIVE_CREDENTIALS. Wynth retains the current field superset, including Metadata, Tools, ParallelToolCalls, Reasoning, ServiceTier, PromptCacheKey, Text, PromptCacheOptions, and lossless legacy prompt_cache_retention. No public Responses API versus ChatGPT Codex backend filtering policy was guessed.

## Latest Codex/OpenAI provider status

- Installed Codex CLI: codex-cli 0.144.5.
- openai/codex main observed at 56395bddaf26eb2829387ca6a417bf9128e5b239; the pinned interpretation checkpoint in the original handoff is 2f7d89b1419bf7064346855b0acde23514b1ebc5. No source-level parity claim is made from the remote tip alone.
- Responses WebSocket: NOT IMPLEMENTED/NOT VERIFIED. Wynth exposes HTTP POST /v1/responses and /v1/responses/compact plus GET /v1/realtime; it has no /v1/responses upgrade path. Sticky account-pool ownership, response.create warmup, previous_response_id continuation, reconnect boundary, and session HTTP fallback therefore have no E2E evidence.
- Programmatic Tool Calling: PARTIAL/UNVERIFIED. Native HTTP request fields in raw Input and Tools and the raw response body can pass through direct Responses paths, but Responses-to-Chat conversion does not implement program/program_output/caller semantics. No live or fixture E2E was run, so full preservation is not claimed.
- Multi-agent beta: NOT SUPPORTED. The OpenAIResponsesRequest DTO has no multi_agent field, the general header setup does not forward OpenAI-Beta: responses_multi_agent=v1, and the WebSocket upgrade path is absent. This is a known compatibility gap, not a silently green status.
- Explicit prompt-cache breakpoints, Responses retrieve/delete/cancel/input_items/input_tokens, and background responses: OUT OF INITIAL SCOPE and NOT E2E VERIFIED.
- Official references checked: https://developers.openai.com/api/docs/guides/latest-model, https://developers.openai.com/api/docs/models/gpt-5.4-mini, https://developers.openai.com/api/docs/models/gpt-5.5, https://developers.openai.com/api/docs/guides/tools-programmatic-tool-calling, https://developers.openai.com/api/docs/guides/responses-multi-agent, https://developers.openai.com/api/docs/guides/websocket-mode, and https://learn.chatgpt.com/docs/models.

## Codex E2E matrix

NOT RUN. No live authorized credentials or local gateway session were supplied. No credentials, authorization headers, or external state were written to the repo. The custom Responses provider, built-in OpenAI provider, compact, cache, GPT-5.6, disconnect, WebSocket, PTC, and multi-agent scenarios remain an explicit follow-up requiring an isolated user-level CODEX_HOME.

## Validation

- Go:
  - go test ./... -count=1: PASS.
  - go test -race ./service ./relay -run 'AccountPool|Codex|Responses|Cache|Tiered' -count=1: PASS.
  - focused converter suite: PASS.
  - focused model/service/controller/middleware/common suite: PASS.
  - go mod verify: PASS.
  - go mod tidy -diff: PASS.
  - gofmt on 131 existing changed Go files: PASS.
  - go vet ./...: FAIL on pre-existing diagnostics only: CustomEvent copies sync.Mutex in common/custom-event.go; IPv6 test address formatting in common/email_test.go; unreachable code in legacy relay/channel/{cohere,dify,xunfei,baidu,zhipu,mokaai,tencent,palm,cloudflare,mistral,jina}/adaptor.go.
- Default frontend:
  - bun install --frozen-lockfile --offline --ignore-scripts: PASS, no changes.
  - bun run typecheck: PASS.
  - bun run i18n:check: PASS, 5442 keys.
  - bun run build: PASS, Rsbuild 2.1.6; routeTree.gen.ts generated ordering churn was restored.
  - scoped oxlint over all 12 changed TS/TSX files: PASS.
  - scoped oxfmt over all 18 changed source/locale files: PASS.
  - combined focused suite: PASS, 24 tests across 7 files.
  - full bun run lint: FAIL on many unrelated pre-existing files; no changed-file errors remain.
  - full bun run format:check: FAIL on the repository's existing broad formatting baseline; changed-file scoped check is clean.
  - bun run i18n:sync: executed; generated _reports churn and protected footer.newapi key escaping were restored, leaving no diff.
- Classic frontend:
  - bun run build: FAIL on the known baseline date-fns/date-fns-tz package-subpath exports (date-fns/_lib/cloneObject, date-fns/format, date-fns/_lib/toInteger, date-fns/_lib/getTimezoneOffsetInMilliseconds).
- Completion greps for ClientMetadata, PromptCacheOptions, cache_write_tokens, X-Codex-Turn-State, and gpt-5.6-sol: all hit.
- git cherry cross-check against audited tip: 110 lines (108 plus, 2 minus); it was used only as a cross-check, not as disposition evidence.

## Current-state refresh (2026-07-18)

This section records the final recheck performed after the earlier batch
entries were written:

- `origin/main` remains `24ce34e7d961e14be4891b5c2bba9f47ac1d29ca`;
  `upstream/main` is `5a6c53d4966b2e34690ab49f3dd19be01c88fdbe`; merge-base is
  `1d166532fe954a45207dffd2924697796a984159`.
- `7c28993f6bd9e92616f3f578212577f8b7c40b45` is still an ancestor of
  `upstream/main`; the post-audit range is still exactly 10 commits. The
  current graph counts remain 130 upstream-only and 461 origin-only.
- The 95 disposition rows were independently counted and matched against
  Appendix A (92) plus Appendix B partials (3): 90 ported, 1 superseded, 3
  intentional omissions, 1 evidence-backed deferral, and 0 open.
- All local commit IDs cited by the implemented-batch table resolve as commit
  objects. The audited-tip `git cherry` cross-check remains 108 plus and 2
  minus; it is not used as the behavior evidence.
- `go test ./... -count=1`: PASS. The ignored `web/classic/dist` required by
  the root `embed` was restored in this isolated worktree from the already
  generated artifact in the protected original checkout; the original checkout
  was read-only and unchanged.
- `go vet ./...`: BASELINE FAIL with the previously recorded 12 legacy
  unreachable-adaptor diagnostics, `common.CustomEvent` lock-copy diagnostics,
  and the IPv6 formatting diagnostic in `common/email_test.go`.
- `go mod verify` and `go mod tidy -diff`: PASS.
- In `web/default`: frozen offline Bun install, `bun run typecheck`,
  `bun run i18n:check` (5442 keys), and `bun run build`: PASS. `bun run
  i18n:sync` also exited 0; its generated report/locale/route-tree churn was
  reviewed and reverse-applied, leaving no tracked generated diff.
- The final timing/pricing focused Bun command passed 7/7 tests; scoped
  `oxlint` and `oxfmt --check` over the seven files in `91c529316`: PASS.
- Full `bun run lint`, `bun run format:check`, and `bun run copyright:check`
  remain non-zero on the repository's existing broad baseline; no changed-file
  error was introduced by the final timing commit. A full `bunx vitest run` is
  not a valid aggregate gate here because the repository intentionally mixes
  Bun `node:test` suites with Vitest files and has existing alias/empty-suite
  failures; the batch-specific Bun suites are the authoritative frontend
  evidence.
- `web/classic bun run build`: BASELINE FAIL on the existing `date-fns` /
  `date-fns-tz` package-subpath exports (`_lib/cloneObject`, `format`,
  `_lib/toInteger`, `_lib/getTimezoneOffsetInMilliseconds`). The failed build
  removes ignored `dist`, so the isolated artifact must be restored before any
  later root Go test.
- `git diff --check` is clean. At the verification checkpoint before this
  documentation commit, there was no source or configuration diff pending; after
  the commit, only `.codegraph/`, `CODEX_SYNC_CONTINUATION.md`, and
  `SYNC_PROGRESS.md` remain intentionally untracked.

## External actions

- Pushed: no.
- Pull request: no.
- Merged/deployed: no.
