# Account Pool Binding Selection Design

## Goal

When an account-pool binding is activated, its local channel must participate in normal model routing even though the channel row remains manually disabled. Deactivating or deleting the binding must remove it from routing again.

## Current Problem

The account-pool runtime already replaces the selected channel credential with a scheduled account credential after a channel is selected. The missing part is selection: normal routing is driven by `abilities.enabled = true` and by the memory cache's enabled-channel map. Account-pool channels intentionally keep `channels.status = ChannelStatusManuallyDisabled`, so activated bindings can exist without their channels being eligible for routing.

## Behavior

- `CreateBoundChannel` may create ability rows for fixed models, but those abilities stay disabled while the binding is draft.
- `ActivateBinding` enables ability rows for the binding's channel without changing the channel status.
- `DisableBinding`, `DeleteBinding`, and `DeletePool` disable or remove the binding's routing effect.
- The database selection path and memory-cache selection path must agree.
- The channel row remains manually disabled so the normal key cannot be used directly and the account-pool runtime remains the only execution path.

## Cache Rules

`InitChannelCache` should treat a manually disabled channel as selectable only when it has an enabled account-pool binding. Ordinary manually disabled channels remain excluded. This keeps the memory-cache route aligned with the database ability route.

## Out Of Scope

- Changing account scheduling policy.
- Changing relay runtime account retry behavior.
- Adding UI settings.
- Supporting non-OpenAI-compatible account-pool channels.

## Testing

Tests must cover:

- Activating a binding enables routing while the channel status stays disabled.
- Disabling and deleting the binding removes routing eligibility.
- Memory-cache and database selection return the same active account-pool channel behavior.
- Existing account-pool runtime activation guard still blocks direct channel enabling.
