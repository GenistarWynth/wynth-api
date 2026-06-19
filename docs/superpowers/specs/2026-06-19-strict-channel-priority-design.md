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
8. Keep `common.RetryTimes` as the total retry budget. Strict priority controls which channel is chosen next; it does not expand the number of attempts.

Using the same example:

| Attempt | Eligible Priority | Eligible Channels | Result |
| --- | ---: | --- | --- |
| 1 | 100 | 1, 2 | weighted random picks 1 |
| 2 | 100 | 2 | tries 2 |
| 3 | 50 | 3 | tries 3 only after priority 100 is exhausted |

If the highest priority tier has more channels than the request's attempt budget, lower priority tiers may not be reached. That is intentional for this iteration: `RetryTimes` remains the cap on total retry work.

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

The first channel is selected before the relay handler retries: `middleware.Distribute` calls `service.CacheGetRandomSatisfiedChannel`, then the first relay attempt reuses that selected channel from context. The relay loop records the used channel ID through `addUsedChannel` before any retry selection happens.

The scheduler should receive attempted IDs from the service layer and filter them out before choosing candidates. Attempted IDs must be resolved fresh for each selection call, because `service.RetryParam` is constructed once before the retry loop and reused across attempts. A static attempted-channel slice captured when `RetryParam` is created would be stale.

Selection should work over priority tiers rather than interpreting retry index as a direct priority-tier index.

The core selection unit should be:

```text
group + model + requestPath + attemptedChannelIDs -> next channel
```

The selected channel is the weighted random result from the highest priority tier that has at least one untried candidate.

The selection contract is:

- candidates are filtered by group, model, request path, enabled state, and attempted channel IDs;
- exhaustion returns `(nil, nil)`, not an error;
- database consistency failures still return errors;
- cached candidate slices are never mutated in place.

## Component Design

### RetryParam

Use `service.RetryParam` as the request selection carrier, but do not store a one-time attempted-channel snapshot in it.

The service layer should read the current `use_channel` context value on every selection call, convert it to an attempted-channel set, and pass that set to the model selection layer. This keeps retry selection correct after each failed attempt while avoiding a Gin dependency in the model package.

`RetryParam.Retry` remains useful as the relay loop counter and attempt budget input. It should no longer mean "priority tier index".

### Distributor and First Attempt

The distributor path and retry path must use the same selection helper:

- initial selection has an empty attempted-channel set;
- the first relay attempt records the selected channel with `addUsedChannel`;
- retry selection then excludes that channel;
- channel affinity selections that bypass normal random selection are still recorded by `addUsedChannel` and should be excluded on retry.

### Memory Cache Selection

Update `model.GetRandomSatisfiedChannel` or introduce a focused helper beneath it so the memory-cache path can:

1. collect candidate channels for group, model, and request path;
2. remove attempted channel IDs;
3. group remaining candidates by priority;
4. choose the highest priority group;
5. select one channel from that group using existing weight behavior.

The existing path-aware filtering for Advanced Custom channels must continue to apply before priority selection.

Implementation constraints:

- the `len(channels) == 1` fast path must return `nil` when that only channel has already been attempted;
- attempted-channel filtering must allocate a new slice and must not mutate `group2model2channels` entries;
- filtering must apply to both exact model lookup and normalized model fallback lookup;
- zero-weight tiers must continue to select using the existing smoothing behavior.

### Database Selection

Update the database path in `model.GetChannel` so it follows the same semantics when `common.MemoryCacheEnabled` is false.

The database path should not continue to use retry as "priority tier index". It should query all enabled matching abilities, apply path filtering, remove attempted channel IDs, then select from the highest remaining priority tier by existing weighted behavior.

If filtering leaves no ability candidates, the function must return `(nil, nil)` before trying to load `channel.Id == 0`.

### Auto Group Selection

For `TokenGroup == "auto"`, `service.CacheGetRandomSatisfiedChannel` should keep trying the current auto group while it still has untried channels for the requested model and path.

It should only move to the next auto group when the current group has no untried eligible channels left. This preserves the existing cross-group intent while making priority fallback strict inside each group.

The current `priorityRetry >= common.RetryTimes` auto-group switch should no longer decide when to change groups. Under strict priority, group advancement is driven by selection exhaustion:

1. Try the current auto group with the current attempted-channel set.
2. If a channel is returned, use that group and channel.
3. If `(nil, nil)` is returned, advance to the next auto group.
4. Stop when a channel is found or all auto groups are exhausted.

`ContextKeyAutoGroupIndex` may still track the current group index across retries, but it should reflect exhaustion-based advancement. `ContextKeyAutoGroupRetryIndex` and `priorityRetry` should not drive priority-tier selection after this change.

## Error Handling

If no eligible channel remains after filtering attempted channels:

- normal group selection returns `nil` so the existing relay error path can report no available channel;
- auto group selection may proceed to the next auto group;
- no new user-facing error type is needed for the first implementation.

Database consistency errors, missing cached channels, and Advanced Custom route filtering behavior should preserve the existing error behavior.

Single-channel groups also follow the attempted-channel rule. If the only channel has already failed in the current request, retry should not use it again. This may reduce same-channel transient retries, but it is consistent with strict request-local fallback.

## Testing Strategy

Add focused unit tests around the scheduler behavior.

Required cases:

1. With channels `(100, 100)`, `(100, 20)`, `(50, 100)`, when channel 1 is attempted, the next channel must be channel 2, not channel 3.
2. When channels 1 and 2 are both attempted, the next channel may select channel 3.
3. When two unattempted channels share the highest priority, selection remains weight-aware.
4. When memory cache is disabled, database selection follows the same strict priority behavior.
5. For `auto` group, lower-priority channels in the current group are considered only after higher-priority channels in that same group are exhausted; the next auto group is considered only after the current group has no remaining eligible channels.
6. When all eligible channels are attempted, normal group selection returns `nil`.
7. When a single-channel group has already attempted its only channel, selection returns `nil`.
8. For `auto` group, a lower-priority channel in the current group is tried before switching to the next group, even when the attempt count is higher than the old priority-tier index.
9. For `auto` group, a channel already attempted in one group is not retried through another group if the same channel belongs to both groups.
10. With memory cache disabled, exhaustion returns `nil` and does not attempt to load `channel.Id == 0`.
11. Attempted-channel filtering does not mutate cached `group2model2channels` slices.
12. Zero-weight channels inside the selected priority tier remain selectable.

Tests should be deterministic where possible by using attempted-channel filtering to leave a single valid candidate. Weighted randomness should only be tested as membership within the highest eligible priority tier, not as an exact distribution.

## Compatibility

This changes retry behavior intentionally. Operators who use multiple channels with the same priority should now see all same-priority fallback candidates attempted before lower-priority channels receive traffic.

`RetryTimes` remains the total retry budget. If a high-priority tier contains more untried channels than the request has remaining attempts, lower-priority tiers will not be reached for that request.

Deployments with `RetryTimes = 0` will not see retry fallback behavior change, because only the first channel is attempted.

Existing `priority` and `weight` fields remain valid:

- `priority` controls strict fallback tiers.
- `weight` controls load distribution inside the currently selected tier.

No migration is required.
