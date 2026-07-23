# Upstream Rate Source and Label Consistency Design

## Problem

Upstream-generated channel names embed the mapping's effective rate, for
example `source-a / 0.080x`. Their remarks also embed the rate as
`Rate: 0.080x`, and the Channels UI shows that remark in the upstream-group
hover. Auto-priority stores its most recent nominal rate in the channel score
snapshot shown by the priority tooltip.

rc.55 made manual discovery and monitor rate collection refresh the generated
channel name, but it left two gaps:

- `refreshGeneratedChannelRateLabelsTx` updates `channels.name` but not
  `channels.remark`;
- normal/automatic source synchronization rebuilds the canonical remark, but
  `generatedChannelUpdateMap` omits `remark` for existing channels.

The name, hover, and last-score tooltip can therefore represent three
different moments. For example, a channel created at `0.050x`, later synced
at `0.040x`, and then scored will show name `0.040x`, hover `0.050x`, and
score `0.040x`.

The multiplier is not inverted: lower values still mean lower effective cost.
The defect is that the persisted representations can become stale relative
to each other.

## Constraints

- Keep nominal price and cache scoring separate, the 75/10/8/3/4 weights, the
  8x nominal hard-dominance threshold, the cache default of 95, and own-sample
  blending unchanged.
- Continue using the mapping's `effective_rate_multiplier` for generated
  channel scoring. Never parse a multiplier from the generated channel name.
- Continue using `channel_auto_priority_rate_multiplier` for manual channels.
- Do not change enabled, auto-disabled, or manually-disabled semantics.
- Do not run a full upstream channel synchronization from the monitor path,
  because that would fetch keys/models and could change status or abilities.
- Do not change routing, affinity, retry, model, path, or priority behavior.
- Never rename a channel that is not owned by the source/mapping.
- Preserve an administrator-authored custom channel remark. Only missing
  remarks and remarks in the generated `Upstream group: ...` format are
  upstream-managed.

## Considered Approaches

1. **Refresh both persisted labels at the existing write boundaries
   (selected).** In the shared discovery/monitor transaction, update both the
   canonical name and canonical remark after verifying ownership. Include the
   canonical remark in the existing generated-channel update map used by
   manual sync, auto-sync, and rule reapplication. Reconcile even when the
   mapping rate did not change so the first post-fix collection repairs
   historical drift. Preserve custom operator remarks at both write
   boundaries.
2. **Trigger a full source sync after every rate change.** Rejected because it
   performs unrelated network calls and can change keys, models, abilities,
   status, and other channel settings.
3. **Join mappings into every channel-list response.** Rejected as a larger
   API/UI change that leaves other consumers of persisted names and remarks
   inconsistent.

## Data Flow

1. The adapter returns an `UpstreamGroup` with a direct effective multiplier.
2. `applyUpstreamSourceGroupsTx` loads the previous mappings and upserts the
   current mappings.
3. For each existing group with a linked channel:
   - load the linked channel in the same transaction;
   - confirm it is owned by the source/mapping;
   - compute the canonical name with
     `upstreamSourceGeneratedChannelName(source, currentMapping)`;
   - compute the canonical remark with
     `upstreamSourceGeneratedChannelRemark(currentMapping)`;
   - retain the existing remark instead when it is not in the generated
     `Upstream group: ...` format;
   - update `channels.name`, `channels.remark`, and `channels.updated_time`
     only when either label differs.
4. Return whether any channel label changed.
5. `applyUpstreamSourceRateGroupSnapshot` refreshes the channel cache when a
   label changed or a mapping became stale.
6. Normal and automatic channel synchronization persists the same canonical
   name and remark through `generatedChannelUpdateMap`.
7. Auto-priority independently loads
   `UpstreamSourceChannelMapping.EffectiveRateMultiplier` for generated
   channels. Manual channels continue to use their own configured multiplier.

## Failure and Concurrency Behavior

Any discovery/monitor label update failure rolls back the same transaction
that updates the mapping, so the database cannot commit a new rate with an
old generated name or hover. The ownership check prevents an incorrect
`local_channel_id` from rewriting a manual or unrelated channel. Missing
upstream groups remain stale rather than deleted. Auto-priority's own
transactional priority persistence remains unchanged.

## Test Strategy

Add a screenshot-shaped regression that changes a mapping from `0.040x` to
`0.050x`, runs auto-priority, and asserts the mapping, generated name, hover
remark, and stored nominal score all use `0.050x`. Extend the historical-drift
test so it repairs both name and remark, and keep an ownership-negative test.
Add an owned-channel test proving a custom operator remark is preserved.
Add a normal-sync regression proving `generatedChannelUpdateMap` includes the
remark. Add explicit score-source tests proving a generated channel ignores
rate-like name text while a manual channel uses its configured multiplier.
Run the remark assertions before implementation to demonstrate RED, then run
the same focused tests after the two production changes to demonstrate GREEN.
