# Strict Channel Priority Scheduling Design

## Goal

Change channel selection so retry fallback strictly exhausts all available channels in the current priority tier before moving to a lower priority tier.

This is the first scheduling change for Wynth API. It must stay scoped to existing `new-api` channel selection behavior and must not introduce account-pool logic yet.

## Current Behavior

The current scheduler already sorts priorities from high to low and selects the first attempt from the highest priority tier.

For a group and model with these channels:

| Channel | Priority | Weight |
| --- | ---: | ---: |
| 1 | 100 | 100 |
| 2 | 100 | 20 |
| 3 | 50 | 100 |

The first attempt uses `priority=100` and selects channel 1 or 2 by weight. If channel 1 fails, the retry index increments. Current selection treats that retry index as the next priority tier, so retry can jump directly to `priority=50` and select channel 3, even though channel 2 has the same higher priority and has not been tried.

This violates strict priority semantics.

## Target Behavior

For each request:

1. Select from the highest available priority tier first.
2. Inside a priority tier, keep weighted random selection for load balancing.
3. Track channels already attempted during the current request.
4. On retry, exclude channels already attempted for this request.
5. Stay in the current priority tier while it still has untried available channels.
6. Move to the next lower priority tier only after the current tier has no untried available channels.
7. Keep this attempted-channel state request-local. It must not affect other concurrent requests.

Using the same example:

| Attempt | Eligible Priority | Eligible Channels | Result |
| --- | ---: | --- | --- |
| 1 | 100 | 1, 2 | weighted random picks 1 |
| 2 | 100 | 2 | tries 2 |
| 3 | 50 | 3 | tries 3 only after priority 100 is exhausted |

## Scope

In scope:

- Channel selection for normal groups.
- Channel selection for `auto` groups.
- Memory-cache selection path.
- Database selection path when memory cache is disabled.
- Retry behavior inside a single relay request.
- Tests that prove lower-priority channels are not selected while higher-priority untried channels remain.

Out of scope:

- Account-pool channels.
- UI changes.
- New scheduling configuration.
- Channel health check redesign.
- Changes to how weights are stored or edited.
- Changes to retryable status code policy.

## Architecture

Add request-local attempted-channel awareness to the channel selection path.

The relay loop already records used channel IDs in Gin context through `addUsedChannel`. The scheduler should receive or read those attempted IDs and filter them out before choosing candidates. Selection should then work over priority tiers rather than interpreting retry index as a direct priority-tier index.

The core selection unit should be:

```text
group + model + requestPath + attemptedChannelIDs -> next channel
```

The selected channel is the weighted random result from the highest priority tier that has at least one untried candidate.

## Component Design

### RetryParam

Extend `service.RetryParam` with request-local attempted channel IDs.

The relay layer already knows which channels have been used during this request. `RetryParam` should expose those IDs to the model selection layer so both normal group and `auto` group selection can make the same decision.

### Memory Cache Selection

Update `model.GetRandomSatisfiedChannel` or introduce a focused helper beneath it so the memory-cache path can:

1. collect candidate channels for group, model, and request path;
2. remove attempted channel IDs;
3. group remaining candidates by priority;
4. choose the highest priority group;
5. select one channel from that group using existing weight behavior.

The existing path-aware filtering for Advanced Custom channels must continue to apply before priority selection.

### Database Selection

Update the database path in `model.GetChannel` so it follows the same semantics when `common.MemoryCacheEnabled` is false.

The database path should not continue to use retry as "priority tier index". It should query all enabled matching abilities, apply path filtering, remove attempted channel IDs, then select from the highest remaining priority tier by existing weighted behavior.

### Auto Group Selection

For `TokenGroup == "auto"`, `service.CacheGetRandomSatisfiedChannel` should keep trying the current auto group while it still has untried channels for the requested model and path.

It should only move to the next auto group when the current group has no untried eligible channels left. This preserves the existing cross-group intent while making priority fallback strict inside each group.

## Error Handling

If no eligible channel remains after filtering attempted channels:

- normal group selection returns `nil` so the existing relay error path can report no available channel;
- auto group selection may proceed to the next auto group;
- no new user-facing error type is needed for the first implementation.

Database consistency errors, missing cached channels, and Advanced Custom route filtering behavior should preserve the existing error behavior.

## Testing Strategy

Add focused unit tests around the scheduler behavior.

Required cases:

1. With channels `(100, 100)`, `(100, 20)`, `(50, 100)`, when channel 1 is attempted, the next channel must be channel 2, not channel 3.
2. When channels 1 and 2 are both attempted, the next channel may select channel 3.
3. When two unattempted channels share the highest priority, selection remains weight-aware.
4. When memory cache is disabled, database selection follows the same strict priority behavior.
5. For `auto` group, lower-priority channels in the current group are considered only after higher-priority channels in that same group are exhausted; the next auto group is considered only after the current group has no remaining eligible channels.

Tests should be deterministic where possible by using attempted-channel filtering to leave a single valid candidate. Weighted randomness should only be tested as membership within the highest eligible priority tier, not as an exact distribution.

## Compatibility

This changes retry behavior intentionally. Operators who use multiple channels with the same priority should now see all same-priority fallback candidates attempted before lower-priority channels receive traffic.

Existing `priority` and `weight` fields remain valid:

- `priority` controls strict fallback tiers.
- `weight` controls load distribution inside the currently selected tier.

No migration is required.
