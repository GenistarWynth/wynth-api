# Account Pool Binding Selection Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make activated account-pool bindings participate in normal channel selection without enabling the underlying channel row.

**Architecture:** Keep account-pool runtime activation on the binding, not the channel. Binding state changes update ability eligibility, and the memory cache includes a manually disabled channel only when an enabled account-pool binding makes it runtime-selectable.

**Tech Stack:** Go, Gin, GORM, existing `model` and `service` account-pool packages, `testify/require` and `testify/assert`.

---

## File Structure

- Modify `service/account_pool_service_test.go`: service-level routing tests for activate/disable/delete behavior.
- Modify `model/channel_cache.go`: account-pool-aware selectable-channel predicate for memory cache initialization.
- Modify `model/account_pool.go`: expose enabled account-pool channel lookup for cache initialization.
- Modify `service/account_pool_service.go`: synchronize ability status on binding activation, disable, delete, and pool delete.

---

### Task 1: Red Tests For Binding Selection

- [ ] Add `TestAccountPoolServiceActivateBindingEnablesRoutingButNotChannel` to `service/account_pool_service_test.go`.

Expected test shape:

```go
binding, err := service.CreateBoundChannel(AccountPoolBoundChannelCreateParams{
    PoolID: pool.Id,
    Name: "pool-channel",
    ModelPolicy: AccountPoolModelPolicy{
        Strategy: "fixed",
        FixedModels: []string{"gpt-5"},
    },
})
require.NoError(t, err)

_, err = service.ActivateBinding(pool.Id, binding.Id)
require.NoError(t, err)

channel, err := model.GetChannel("default", "gpt-5", 0, "/v1/chat/completions")
require.NoError(t, err)
require.NotNil(t, channel)
assert.Equal(t, binding.ChannelID, channel.Id)
assert.Equal(t, common.ChannelStatusManuallyDisabled, channel.Status)
```

- [ ] Add disabled and deleted binding assertions in the same test: after `DisableBinding` and `DeleteBinding`, `model.GetChannel` returns nil.
- [ ] Add a memory-cache variant that enables `common.MemoryCacheEnabled`, calls `model.InitChannelCache`, and asserts `model.GetRandomSatisfiedChannel` returns the account-pool channel only while the binding is enabled.
- [ ] Run `go test ./service -run "TestAccountPoolServiceActivateBindingEnablesRouting" -count=1` and verify failure.

---

### Task 2: Implement Ability Synchronization

- [ ] Add a service helper that updates all ability rows for a binding channel:

```go
func setAccountPoolBindingAbilityEnabled(channelID int, enabled bool) error {
    return model.DB.Model(&model.Ability{}).
        Where("channel_id = ?", channelID).
        Update("enabled", enabled).Error
}
```

- [ ] In `setBindingStatus`, after the binding status update, call the helper with `enabled = status == model.AccountPoolBindingStatusEnabled`.
- [ ] In `DeleteBinding`, disable the channel's abilities before or during deletion.
- [ ] In `DeletePool`, disable abilities for all bindings belonging to the pool.
- [ ] After each state change, call `model.InitChannelCache()` when `common.MemoryCacheEnabled` is true.
- [ ] Run the focused service test and verify it passes.

---

### Task 3: Implement Memory-Cache Inclusion

- [ ] Add a model helper that returns enabled account-pool channel IDs:

```go
func EnabledAccountPoolRuntimeChannelIDs() (map[int]struct{}, error)
```

It should return an empty map when `DB` is nil or the binding table does not exist.

- [ ] In `model.InitChannelCache`, load enabled account-pool channel IDs before building `group2model2channels`.
- [ ] Include a channel in the cache when:

```go
channel.Status == common.ChannelStatusEnabled || enabledAccountPoolChannelIDs[channel.Id] exists
```

- [ ] Do not change `channelsIDM`; it should still store the actual disabled channel status for relay/runtime logic.
- [ ] Run service memory-cache test and `go test ./model ./service -count=1`.

---

### Task 4: Review And Final Verification

- [ ] Run `gofmt` on changed Go files.
- [ ] Run `go test ./model ./service ./middleware ./controller -count=1`.
- [ ] Ask Claude for read-only review of the diff with focus on cache/DB routing consistency and ability state leakage.
- [ ] Fix verified findings with tests.
- [ ] Commit implementation with `feat: route activated account pool bindings`.

---

## Self-Review

- Spec coverage: activation, deactivation, deletion, cache parity, and disabled channel invariant are covered.
- Placeholder scan: no TODO/TBD placeholders remain.
- Type consistency: helpers use existing `model.Ability`, `model.AccountPoolChannelBinding`, and binding status constants.
