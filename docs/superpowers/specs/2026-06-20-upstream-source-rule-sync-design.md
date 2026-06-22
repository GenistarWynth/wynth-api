# Upstream Source Rule Sync Design

## Goal

Change upstream source synchronization from manual mapping selection to rule-driven synchronization.

The system should discover upstream groups, match them against configured sync rules, create or update generated local channels only for matched groups, and leave unmatched groups visible but unsynced. This keeps expensive or unwanted upstream groups from being created accidentally while still showing what the upstream source exposes.

This design extends the existing upstream source work. Price-to-priority adjustment, availability scoring, cache-rate scoring, and account-pool integration remain out of scope for this step.

## Current Behavior

An upstream source currently has source-level sync settings:

- default local group;
- generated channel type;
- default priority and weight;
- generated channel monitoring defaults;
- source-level auto-sync interval;
- source-level auto model sync toggle;
- optional local group routing rules.

Discovery imports all valid upstream groups into `upstream_source_channel_mappings`.

Synchronization then creates or updates generated channels for mappings where `sync_enabled=true`. Local group rules only choose the generated channel's local group. They do not decide whether a mapping should be synced, how the mapping should be monitored, how often it should sync, or which models should be written.

## Target Behavior

Upstream source synchronization becomes rule-driven.

Each upstream source can define ordered group sync rules. A discovered upstream group is eligible for generated channel sync only when it matches at least one rule. If no rule matches, the mapping stays visible in the upstream source mapping list, but it is not selected for automatic sync and no generated channel is created for it.

Matching rules:

1. Rules are evaluated in order.
2. A rule may match by upstream platform, group name keywords, and group description keywords.
3. Platform matching uses structured upstream metadata when available. For sub2api, this means `UpstreamGroup.Platform`, such as `openai` or `anthropic`.
4. Include keywords match upstream group name or description with case-insensitive substring matching.
5. Exclude keywords also match group name or description. If any exclude keyword matches, the rule does not match even if include/platform conditions match.
6. If a group matches multiple rules, the first matching rule wins.
7. If no rule matches, the group is discoverable but unsynced.

Rule outputs:

- target local group;
- generated channel monitor setting;
- generated channel monitor interval;
- rule auto-sync enabled setting;
- rule auto-sync interval;
- model sync strategy;
- fixed model list when the strategy is fixed.

Source-level settings remain as fallback defaults. If a rule does not set monitoring, monitor interval, auto-sync enabled, auto-sync interval, or model sync strategy, the source-level value is used.

## Rule Shape

The existing `local_group_rules` concept should evolve into sync rules. The JSON config can remain backward compatible by accepting old fields, but new rule fields should carry sync semantics.

Proposed rule fields:

```json
{
  "name": "OpenAI cheap",
  "platforms": ["openai"],
  "name_contains": ["gpt"],
  "description_contains": ["cheap"],
  "exclude_keywords": ["pro", "premium"],
  "local_group": "default",
  "monitor": {
    "enabled": true,
    "interval_minutes": 5
  },
  "auto_sync": {
    "enabled": true,
    "interval_minutes": 30
  },
  "model_strategy": "all_upstream",
  "fixed_models": []
}
```

Allowed `model_strategy` values:

- `all_upstream`: fetch models from the generated upstream key and write all normalized upstream models to the generated channel;
- `fixed`: fetch models from the generated upstream key, intersect them with `fixed_models`, and write only the intersection.

For fixed model rules, if the configured list contains four models and the upstream key exposes three of them, the generated local channel gets the three available models. If the intersection is empty, the generated channel is saved disabled and the mapping records a sync error explaining that no configured models are available upstream.

## Source Fallbacks

Source-level values remain useful as defaults:

- source monitor enabled;
- source monitor interval;
- source auto-sync enabled;
- source auto-sync interval;
- source model sync strategy;
- source fixed models;
- source generated channel type;
- source default priority and weight;
- source default local group.

Fallback rules:

1. If a matched rule sets a field, use the rule field.
2. If a matched rule leaves a field unset, use the source fallback.
3. If a group matches no rule, do not use fallback values to sync it. It remains discovered only.
4. If old configs have no rules but have existing selected mappings, keep manual selected mappings working for backward compatibility in this migration step.

The final point prevents existing users from losing generated channels immediately after upgrading. New rule-driven behavior should be used for sources that have at least one sync rule configured.

## Discovery Flow

Discovery should still call the upstream adapter and persist all valid upstream groups as mappings.

For each discovered group, store:

- upstream group ID;
- upstream group name;
- upstream group description;
- upstream platform;
- upstream status;
- upstream rate multiplier;
- effective upstream rate multiplier;
- last discovered time.

Mappings should make match status visible to the frontend:

- matched rule name or ID;
- whether the mapping is currently sync-eligible;
- reason when not eligible, such as `no matching rule` or `excluded by keyword`.

The match status should be computed dynamically from the source config when listing mappings. Sync and UI must use the same matcher so that what the admin sees matches what the worker will sync.

## Sync Flow

Manual sync and scheduled auto sync should use the same eligibility function.

For each mapping:

1. Resolve the first matching rule.
2. If no rule matches and the source is in rule-driven mode, skip the mapping.
3. If the upstream group disappeared, mark the mapping disabled and disable its generated local channel.
4. Create or recover the upstream key for the upstream group.
5. Build or update the generated local channel.
6. Resolve the local group from the matched rule, falling back to source default only when the rule omits it.
7. Resolve monitoring settings from the matched rule, falling back to source settings.
8. Resolve auto-sync settings from the matched rule, falling back to source settings.
9. Resolve model strategy from the matched rule, falling back to source settings.
10. Fetch upstream models through the generated channel key.
11. Write all upstream models or the fixed-list intersection depending on the resolved model strategy.
12. Save the generated channel enabled when models are non-empty, otherwise disabled with a mapping error.

Manual per-mapping toggles may continue to exist for emergency disablement. They should act as an additional gate: a mapping must be rule-eligible and `sync_enabled=true` to sync. Rule matching should set the default `sync_enabled` for newly discovered mappings, but user overrides should not be silently overwritten unless the group disappears upstream.

## Auto Sync Scheduling

Auto sync scheduling should become rule-aware.

The source-level auto-sync setting remains the fallback. A matched rule may explicitly enable or disable auto sync and may set its own interval. A mapping is due only when its resolved auto-sync setting is enabled and its resolved interval has elapsed since the mapping's last sync time.

The worker can still run at source granularity for simplicity:

1. Find enabled sources that have source-level auto sync enabled or at least one rule with auto sync explicitly enabled.
2. Use the shortest resolved auto-sync interval for that source as the coarse source wake-up interval.
3. For each due source, discover groups first.
4. Sync only mappings whose own resolved auto-sync interval is due.
5. Update each synced mapping's `last_synced_at`; source `last_sync_time` remains a coarse record of the worker run.

## Frontend

The upstream source form should replace free-text local group fields with searchable selection from existing groups. The user should still be able to type to filter options, but the saved value should be one of the backend groups.

The rule editor should support:

- rule name;
- platforms multi-select, including OpenAI and Anthropic when upstream metadata supports them;
- include keywords for group name or description;
- exclude keywords for group name or description;
- target local group select;
- monitor enabled override;
- monitor interval override;
- auto sync enabled override;
- auto sync interval override;
- model strategy select;
- fixed model multi-select when model strategy is fixed.

The mapping list should show why a group is or is not sync-eligible. Unmatched groups should be visible with a neutral status like `not matched` instead of looking like an error.

## Backend Compatibility

The config parser should preserve explicit false values and should continue using project JSON wrappers.

Database changes should remain portable across SQLite, MySQL, and PostgreSQL. Prefer storing new rule configuration in the existing source sync config JSON rather than adding many nullable columns. If persisted mapping match status is added, use simple text, integer, and boolean columns only.

Existing generated channel ownership protections stay unchanged. Rule-driven sync must not claim a local channel unless the generated ownership metadata proves it belongs to the upstream source mapping, or the existing legacy long-name fallback applies.

## Testing

Backend tests should cover:

- platform matching for sub2api groups;
- include keyword matching on name and description;
- exclude keyword overriding include matches;
- first matching rule wins;
- unmatched groups are discovered but not synced;
- rule-level local group, monitor settings, and sync interval override source fallback;
- fixed model intersection keeps only models available upstream;
- fixed model empty intersection disables the channel and records a useful mapping error;
- deleted upstream groups disable existing generated channels;
- backward compatibility for existing selected mappings without rules.

Frontend tests should cover:

- local group selection uses existing group options;
- keyword normalization for include and exclude fields;
- fixed model selector payload;
- mapping list displays matched and unmatched states.

## Out of Scope

- Price-to-priority or price-to-weight automation.
- Availability-based scheduling.
- Cache-rate-based scheduling.
- Account-pool migration.
- Support for upstream providers beyond the current adapter interface unless required to expose group platform and models.
- Removing manual mapping controls entirely.
