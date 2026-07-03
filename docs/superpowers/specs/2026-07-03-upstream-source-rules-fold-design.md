# Upstream Source Rules Fold-Down — Design

- **Date:** 2026-07-03
- **Status:** Design presented; two open decisions resolved by best-judgment while the user was away (flagged below) — pending user spec review.
- **Scope:** Issue #2 of the three-issue batch. Depends on #3 being merged (this branch is off merged `main`, which contains #3).
- **Branch:** `feat/upstream-source-rules`

## Problem

The upstream-source create/edit UI has a confusing two-layer model: source-level **"Default \*"** settings (Default Strategy / Default Sync Schedule / Default Priority Adjustment) *and* per-group **Sync Rules** that override them. The user wants the global/source-level layer **removed** — everything should follow the per-group sync rules, with fixed constant fallbacks instead of source-level defaults. Two additional asks: the monitor minimum interval must drop to **1 minute**, and the monitor settings must let an admin pick a **monitoring model** (the model the health-check probe uses).

## Grounding facts (verified against merged `main`)

1. **Most settings are already rule-driven.** `dto.UpstreamSourceLocalGroupRule` already overrides `local_group`, `monitor{enabled,interval}`, `auto_sync{enabled,interval}`, `auto_priority{enabled,interval,window}`, `codex bridge`, `model_strategy`, `fixed_models` (`dto/upstream_source.go:114-143`). Resolution overlays a matched rule onto a source-level fallback (`service/upstream_source_rule.go:415-461`).
2. **The only gap:** `channel_type`, `default_priority`, `default_weight` are **not** per-rule. `buildGeneratedChannel` reads them straight off the source config: `Type: config.ChannelType` (`service/upstream_source.go:839`), `Priority: config.DefaultPriority` (:843), `Weight: config.DefaultWeight` (:844). Everything else on the generated channel already comes from `resolution`.
3. **`allow_private_ip` is fetch-time, not per-channel** — passed to `FetchChannelUpstreamModelIDs` at discovery (`service/upstream_source.go:120`). It has no per-generated-channel application point, so it stays source-level.
4. **No monitor model exists today.** The auto-monitor probe `runChannelMonitorProbe` (`controller/channel-test.go:992`) calls `testChannelWithOptions(..., testModel="")`; `resolveChannelTestModel` (`controller/channel-test.go:153-171`) resolves: passed model → `channel.TestModel` (`model/channel.go:28`) → first of `channel.GetModels()` → hardcoded `"gpt-4o-mini"`. `dto.ChannelOtherSettings` has only `ChannelMonitorEnabled`/`ChannelMonitorIntervalMinutes` (`dto/channel_settings.go:51-52`), no model field.
5. **`generatedChannelUpdateMap` persists the `"settings"` (OtherSettings) column but NOT `test_model`** (`service/upstream_source.go:943-959`) — so a monitor model stored in `ChannelOtherSettings` persists on both create and re-sync-update, whereas reusing `channel.TestModel` would silently drop on update-sync and clobber admin manual edits.
6. **Monitor min-interval clamps to 5** in three backend places (`service/upstream_source_rule.go:106-108`, `256-261`, `322-326`) and the frontend (`web/default/src/features/upstream-sources/index.tsx:2028` `min={5}`). The channel-monitor runtime floor is already 1 min (`model/channel_monitor.go:20,77-87`) and the scheduler ticks every 60s (`controller/channel-test.go:1476`), so 1-min is honorable once the upstream clamps relax. Auto-sync has a controller-only min-5 (`controller/upstream_source.go:540-546`); auto-priority has no floor.
7. **No-rules today = sync everything with source defaults** (`service/upstream_source_rule.go:294-299` forces `SyncEligible=true`; discovery sets `mapping.SyncEnabled=true` at `service/upstream_source.go:1066-1067`).
8. **Empty-matcher rules are dropped today** (`service/upstream_source_rule.go:176-178`).
9. **`sync_config` is a single `text` JSON column** (`model/upstream_source.go:50`) — no DB/schema migration needed; only read-time JSON reshaping.
10. **Frontend:** all three "Default \*" subsections live in one `SideDrawerSection` (`index.tsx:1542-1769`); the inherit baseline is built from `form.*` in the `ruleStrategyDefaults` memo (`index.tsx:1181-1213`) and per-rule `rule.X?.Y ?? form.Z` fallbacks; constants `DEFAULT_*` already exist (`index.tsx:182-185`). A single-select `Combobox` with `allowCustomValue` (`components/ui/combobox.tsx`) is already used for local-group pickers and can back the monitor-model selector, fed by `modelSelectOptions` (`index.tsx:1177-1180`).

## Goals

- Remove the source-level "Default \*" configuration; make `channel_type`, `default_priority`, `default_weight` per-rule alongside the already-per-rule settings; source-level fallback becomes hardcoded constants.
- Add a per-rule **monitor model** that the auto-monitor probe uses for generated channels.
- Drop the monitor minimum interval to 1 minute, end-to-end.
- Do not break existing sources: read-time migration folds legacy source-level defaults into a synthesized catch-all rule.

## Non-goals

- No DB schema/column migration (JSON-only reshaping).
- `allow_private_ip` does not become per-rule (fetch-time concern; stays source-level).
- Monitor model does not change manual channel tests (auto-monitor only).
- Auto-sync minimum interval is NOT lowered in this change (stays 5; see Decisions).

## Design

### 1. Rule shape (new fields)
Add to `dto.UpstreamSourceLocalGroupRule`: `Priority *int64` (pointer+omitempty), `Weight *uint` (pointer+omitempty), `ChannelType int`. Add `Model string` to `dto.UpstreamSourceRuleMonitor`. (Pointers for priority/weight so an explicit 0 is distinguishable from "unset" per the project's DTO rule, though the UI always sends a value — see §7.)

### 2. Resolution fold-down (backend)
- Add `ChannelType int`, `Priority int64`, `Weight uint`, `MonitorModel string` to `upstreamSourceRuleResolution` (`service/upstream_source_rule.go:51-67`).
- Populate them in `upstreamSourceRuleFallbackResolution` from **constants** (channel_type=`constant.ChannelTypeOpenAI`, priority=0, weight=1, monitor-model="") and in `resolveUpstreamSourceMatchedRule` from the matched rule (when set), inside the existing overlay logic.
- `buildGeneratedChannel` (`service/upstream_source.go:839/843/844`): read `resolution.ChannelType` / `resolution.Priority` / `resolution.Weight` instead of `config.*`. Preserve the `channel_type==0 → OpenAI` fallback in the rule-normalize path.

### 3. Monitor-model plumbing
- `rule.Monitor.Model` → `resolution.MonitorModel` (set in the matched-rule Monitor block + carried through `normalizeUpstreamSourceRuleMonitor`).
- Add `ChannelMonitorModel string json:"channel_monitor_model,omitempty"` to `dto.ChannelOtherSettings`.
- `buildGeneratedChannel` / `mergeGeneratedChannelOtherSettings` write `resolution.MonitorModel` into the generated channel's `ChannelOtherSettings` (persisted via the existing `"settings"` update key).
- `runChannelMonitorProbe` (`controller/channel-test.go:992`): read `NormalizeChannelMonitorSettings(GetChannelMonitorSettingsReadOnly(channel)).ChannelMonitorModel`; **if non-empty AND present in `channel.GetModels()` (after `channel.ModelMapping`), pass it as the `testModel`; otherwise pass `""`** (fall back to the existing default resolution — never force a guaranteed-failing probe). Thread the same into `testAccountPoolChannelMonitorSchedulability` (`controller/channel-test.go:850`). `resolveChannelTestModel` itself needs no change (a non-empty passed model already wins).

### 4. Monitor min-interval → 1
Change the three backend monitor clamps (`service/upstream_source_rule.go:106-108`, `256-261`, `322-326`) from `>0 && <5 → 5` to `>0 && <1 → 1` (i.e. floor 1, keep `0 = disabled/inherit`). Frontend rule-monitor input `min={5}` → `min={1}` (`index.tsx:2028`). Auto-sync and auto-priority clamps unchanged.

### 5. No-rules semantics + empty-matcher = match-all + migration
- **Empty-matcher rule = match-all**: change `normalizeUpstreamSourceLocalGroupRules` to keep (not drop) a rule with no platforms/name_contains/description_contains and treat it as matching every discovered group. This is both a useful fallback-rule feature and the vehicle for migration.
- **No rules = nothing eligible**: change the backward-compat branch (`service/upstream_source_rule.go:294-299`) to fall through to the not-eligible fallback (`Reason="no matching rule"`); align `resolveUpstreamSourceRuleForManualSync` (`service/upstream_source.go:331-343`) and the `eligibleCount==0` messaging (`:207-243`).
- **Read-time migration** (in `parseUpstreamSourceSyncConfig`, both `service/upstream_source_rule.go:69-87` and the controller twin `controller/upstream_source.go:512-521`): if a parsed config has **no** `local_group_rules` but carries legacy source-level defaults, synthesize a single catch-all rule (empty matchers) carrying the former source-level `channel_type/priority/weight/monitor{enabled,interval}/auto_sync/auto_priority/model_strategy/fixed_models/local_group`. Existing sources thus keep syncing exactly as before, now expressed as one rule.

### 6. Source-level field consolidation
- Remove the source-level `Default *` fields from the request/response DTOs (`UpstreamSourceCreateRequest`/`UpdateRequest`/`Response`): `channel_type`, `default_priority`, `default_weight`, `enable_monitor`, `monitor_interval_minutes`, `auto_sync_*`, `auto_priority_*`, `model_strategy`, `fixed_models`, `auto_sync_models`, `default_local_group`.
- **Keep a single source-level `local_group`** as the base target group (relocated in the UI to the Connection section, §7). Each rule's `local_group` inherits this base when left blank; the base itself defaults to the constant `"default"`. This preserves the current backend expectation that a source has a base group and gives rules a sensible inherited target.
- **Keep** these fields on the internal `upstreamSourceSyncConfig` / `upstreamSourceControllerSyncConfig` structs so old stored JSON still parses and feeds the read-time migration.
- Keep `allow_private_ip` at source level (connection/fetch concern).

### 7. Frontend
- Delete the three "Default \*" subsections (`index.tsx:1542-1769`).
- Re-point every inherit baseline (`ruleStrategyDefaults` memo `:1181-1213`, per-rule `?? form.*` fallbacks, `addLocalGroupRuleTemplates` `:1296-1323`, and `resolveLocalGroupRuleStrategy` callers) to the module-level `DEFAULT_*` constants; centralize as an exported `DEFAULT_LOCAL_GROUP_RULE_STRATEGY_DEFAULTS` in `rules.ts`.
- Extend the rule type (`types.ts:114-127`) + `UpstreamSourceRuleMonitor` (`:58-61`) with `priority`/`weight`/`channel_type`/monitor `model`; extend `normalizeSyncRules`, `normalizeTemplateRule`, `buildLocalGroupRuleTemplate`, `emptyLocalGroupRule`, `normalizeRuleForForm`.
- Render **always-visible** priority/weight/channel_type inputs in each rule's body (pre-filled: channel_type=OpenAI, priority=0, weight=1), and a **monitor-model** single-select `Combobox` (`allowCustomValue`, fed by `modelSelectOptions`) in the rule's monitor override area.
- Relocate the source's base **local group** input to the Connection section (it stays a single source-level field per §6; rules inherit it when their `local_group` is blank).
- Rule-monitor interval `min={1}`. New i18n keys ("Monitor Model", "Select monitor model", per-rule "Priority"/"Weight"/"Channel Type" reuse existing keys) in en.json + zh.json.

### 8. Testing
Table-driven (`testify`), state in-fixture:
- Resolution: priority/weight/channel_type now come from the matched rule; fallback = constants; matched-rule overlay of the three new fields.
- `buildGeneratedChannel` reads the three from resolution (update the existing tests that assert source-level flow-through: `service/upstream_source_test.go:869-931`, `:1169-1209`).
- No-rules → all mappings skipped ("no matching rule"); empty-matcher rule → matches all.
- **Read-time migration**: a legacy no-rules JSON blob → synthesized catch-all rule preserving prior behavior (priority/weight/channel_type/monitor/etc.).
- Monitor model: written into `ChannelOtherSettings`; `runChannelMonitorProbe` passes it when in the channel's models, falls back to default when not.
- Monitor interval 1-min end-to-end (upstream clamp → generated channel setting → runtime due-check).
- Controller round-trip of the new rule fields; migration mirrored in the controller parse.
- Frontend `rules.test.ts` updated for the constant baseline + new fields; `bun run typecheck` + lint green.

## Decisions

- **Full fold-down** of channel_type/priority/weight into per-rule; source-level defaults → hardcoded constants. *(user)*
- **Monitor min interval → 1 minute.** *(user)*
- **Selectable monitor model**, per-rule, auto-monitor only, single model, with fall-back-to-default when not in the channel's models. *(chosen — minimal, robust)*
- **allow_private_ip stays source-level** (fetch-time concern, no per-channel application point). *(chosen)*
- **Non-breaking migration**: read-time synthesis of a catch-all rule from legacy source-level defaults; empty-matcher rule = match-all. *(chosen)*
- **[Away-decision] Rule priority/weight/channel_type = always-visible inputs** with constant defaults pre-filled (not optional overrides). *(best-judgment; flip at review if optional-override preferred)*
- **[Away-decision] Auto-sync minimum interval stays 5 minutes** (only monitor drops to 1). *(best-judgment; a 1-min auto-sync re-pulls upstream groups/models every minute and risks account flags — flip at review if you want parity)*

## Risks & mitigations
- **Silent stop-syncing for legacy no-rule sources** → read-time catch-all migration (§5) preserves behavior; covered by a migration test.
- **Monitor model not in a channel's model list** → probe would always fail; mitigated by the in-models guard with fallback (§3).
- **Empty-matcher semantic change** (drop → match-all) could surprise someone who left a rule blank; mitigated by making it explicit in the rule editor (a blank-matcher rule is labeled "matches all") and documented.
- **Removing source-level DTO fields** could break external admin-API callers → mitigated by keeping the fields on the internal config struct (old stored JSON still parses) and the read-time migration; note the API change in the PR body.
