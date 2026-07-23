# Upstream Rate Label Consistency Design

## Problem

Upstream-generated channel names embed the mapping's effective rate, for
example `source-a / 0.080x`. The rc.54 upstream monitor refreshes rate groups
through `applyUpstreamSourceGroupsTx`, which updates
`upstream_source_channel_mappings.effective_rate_multiplier`. That path does
not update an existing generated channel's name. Auto-priority therefore reads
the new mapping rate while the Channels table can continue showing the old
embedded rate.

The multiplier is not inverted: lower values still mean lower effective cost.
The defect is that two persisted representations can become stale relative to
each other.

## Constraints

- Keep the 8x extreme-cost dominance threshold and every scoring weight
  unchanged.
- Continue using the mapping's `effective_rate_multiplier` for generated
  channel scoring; never allow the manual channel rate setting to override it.
- Do not change enabled, auto-disabled, or manually-disabled semantics.
- Do not run a full upstream channel synchronization from the monitor path,
  because that would fetch keys/models and could change status or abilities.
- Do not change routing, affinity, retry, model, path, or priority behavior.
- Never rename a channel that is not owned by the source/mapping.

## Considered Approaches

1. **Refresh the generated label in the shared group-application transaction
   (selected).** For every existing mapping with a linked channel, verify
   ownership using the existing generated-channel metadata guard, then update
   only the channel name when it differs from the current canonical name.
   Reconciling even when the mapping rate itself did not change lets the first
   post-fix collection repair drift already persisted by rc.54. This keeps
   mapping and label atomic and covers both manual discovery and monitor
   collection.
2. **Trigger a full source sync after every rate change.** Rejected because it
   performs unrelated network calls and can change keys, models, abilities,
   status, and other channel settings.
3. **Add a separate rate column backed directly by mappings.** Rejected as a
   larger API/UI change that leaves the stale embedded name in other views and
   notifications.

## Data Flow

1. The adapter returns an `UpstreamGroup` with a direct effective multiplier.
2. `applyUpstreamSourceGroupsTx` loads the previous mappings and upserts the
   current mappings.
3. For each existing group with a linked channel:
   - load the linked channel in the same transaction;
   - confirm it is owned by the source/mapping;
   - compute the canonical name with
     `upstreamSourceGeneratedChannelName(source, currentMapping)`;
   - update only `channels.name` and `channels.updated_time` when needed.
4. Return whether any channel label changed.
5. `applyUpstreamSourceRateGroupSnapshot` refreshes the channel cache when a
   label changed or a mapping became stale.

## Failure and Concurrency Behavior

Any channel-label update failure rolls back the same transaction that updates
the mapping, so the database cannot commit a new rate with an old generated
label. The ownership check prevents an incorrect `local_channel_id` from
renaming a manual or unrelated channel. Auto-priority's own transactional
priority persistence remains unchanged.

## Test Strategy

Add a regression test to `service/upstream_source_collection_test.go` that
creates an owned generated channel named with `0.080x`, applies an rc.54-style
rate snapshot changing the mapping to `0.020x`, and asserts both the mapping
and channel name are `0.020x`. Add a second regression in which the mapping is
already `0.020x` but the channel name is still `0.080x`, proving that a later
unchanged snapshot repairs historical drift. An ownership-negative test
ensures a mismatched channel is never renamed. Run both defect cases before
their implementation changes to demonstrate the stale-label failures, then
after the minimal implementation. Existing auto-priority tests continue to
prove 8x dominance, current-unavailability override, mapping-owned rates,
hysteresis, and auto-disabled preservation.
