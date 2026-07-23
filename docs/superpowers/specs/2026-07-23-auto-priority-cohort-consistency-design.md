# Auto-Priority Cohort Consistency Design

## Problem

The repository contains two auto-priority execution paths. The legacy
upstream-source worker selects due mapping IDs, scores only that subset, and
persists candidates independently. The rc.56 channel worker selects a whole
local group and persists it transactionally, but it only loads enabled and
manually disabled channels. Both the legacy cohort-bounds query and the rc.56
worker therefore omit temporarily auto-disabled channels.

That omission makes cohort membership depend on transient channel status.
When a cheap channel becomes auto-disabled, the cohort floor rises; when it
recovers, the floor falls again. The same omission leaves auto-disabled rows
with old snapshots indefinitely. In the legacy path, they can be selected as
due but fail the optimistic update because persistence requires
`status = enabled`.

## Considered Approaches

1. **Evaluate enabled plus temporarily auto-disabled members atomically
   (selected).** Treat both statuses as eligible cohort membership, but mark
   auto-disabled inputs hard-unavailable. Persist their fresh diagnostic
   snapshots without changing their channel or ability priority. Keep manually
   disabled and all other statuses outside normalization. Route scheduled work
   through the group worker.
2. **Keep excluding auto-disabled rows and cache the last floor.** This would
   stabilize price scores temporarily, but the cached floor would become wrong
   when channels are permanently reconfigured or removed and would need a new
   invalidation protocol.
3. **Read the full cohort but update only due rows.** This fixes the immediate
   floor error but preserves mixed snapshot versions and timestamps, so the UI
   still cannot tell which rows were evaluated together.

Approach 1 directly matches the status lifecycle: auto-disable is temporary,
manual disable is an operator decision, and unknown/deleted states are not
eligible.

## Evaluation and Persistence

The channel worker loads statuses `enabled`, `auto-disabled`, and
`manually-disabled`. Manual-disabled members retain the existing sink flow and
do not contribute to scoring. Enabled and auto-disabled members are grouped by
the existing local-group schedule; scoring cohorts remain keyed by
`localGroup#channelType`, never by channel name.

If any eligible member is due, every eligible member in that local group is
evaluated. Price floor, ceiling, member count, and all scoring inputs come from
that complete in-memory set. Auto-disabled members participate in nominal
price normalization but receive a zero availability gate, a computed priority
of zero, and a `channel_auto_disabled` reason. Their current priority remains
unchanged because the result is diagnostic-only.

The existing per-local-group database transaction remains the atomic write
boundary. Each optimistic update also matches the status observed during
evaluation, so an enable/disable race aborts the whole group. A concurrent
worker that loses the settings comparison rolls back without partial or
duplicate writes.

The compatibility upstream-source scheduler delegates due execution to the
same group worker instead of running its mapping subset scorer. It unions the
due groups and evaluates them once, without touching unrelated groups. Direct
source-triggered runs also use source mappings only to select groups, then
evaluate the complete groups.

## Diagnostics

Snapshot version `v4` adds:

- `cohort_floor`
- `cohort_ceil`
- `cohort_member_count`

Together with the existing cohort and computed timestamp, these fields show
which normalization set produced a score without exposing credentials or
channel keys.

## Compatibility

The rc.56 scoring formula is unchanged:

- nominal price remains separate from cache behavior;
- weights remain 75/10/8/3/4;
- a zero-sample cache score remains 95 and blends to own data at 20 samples;
- nominal 8x dominance remains enforced.

Rate multiplier resolution continues to use channel settings or upstream
mapping data. Channel names are never parsed. Manual-disabled sink behavior,
unrelated settings preservation, generated-channel optimistic locking, and
rate-label/remark synchronization remain unchanged.

Manual-disabled channels are sunk below enabled scoring members only.
Temporarily auto-disabled members retain their prior priority and therefore do
not influence the manual sink value.

## Tests

Regression coverage creates one OpenAI cohort with rates
`.02/.04/.06/.05/.08`, where the first three are temporarily auto-disabled and
only `.05/.08` would have formed the old due subset. The run must refresh all
five members at one timestamp/version, report a `.02` floor and five members,
and produce monotonic nominal price scores. A second test starts an
auto-disabled member with a five-hour-old v2 snapshot and verifies it is
refreshed without changing its priority. Existing atomic rollback tests cover
partial-write prevention; focused score tests cover hard-unavailable behavior
and v4 diagnostic round trips.
