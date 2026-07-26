# Exhaust Same-Group Channel Failover Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Exhaust every eligible channel in the resolved group for the requested model, once each, before returning a retryable relay error.

**Architecture:** Preserve the existing distributor, scheduler, and single relay lifecycle. Remove the numeric inter-channel cap at the controller seam, freeze retry selection to the resolved group, treat selector nil as exhaustion while retaining the last upstream error, and guard response commitment/cancellation.

**Tech Stack:** Go 1.22+, Gin, GORM, `httptest`, Testify

---

### Task 1: Add controller-level RED fixtures

**Files:**
- Create: `controller/relay_failover_test.go`

- [ ] **Step 1: Build an isolated relay fixture**

Create a fixture that installs an in-memory SQLite `channels`/`abilities`
database, enables the memory cache, uses free-model group pricing, starts local
OpenAI-compatible upstream servers, initializes the first channel into Gin
context through `middleware.SetupContextForSelectedChannel`, and calls
`Relay(c, types.RelayFormatOpenAI)`.

The fixture must expose:

```go
type relayFailoverResult struct {
    StatusCode  int
    Body        string
    Attempts    []int
    UsedChannel []string
}
```

Each upstream handler appends its channel ID under a mutex and returns either a
retryable OpenAI error, a non-retryable request error, a valid completion with
usage, or an SSE response.

- [ ] **Step 2: Add exact exhaustion tests**

Add deterministic unique-priority candidates and assertions for:

```go
func TestRelayExhaustsEligibleChannelsBeyondRetryTimes(t *testing.T)
func TestRelayReturnsFinalErrorOnlyAfterTotalExhaustion(t *testing.T)
func TestRelayExcludesIneligibleChannelsAndReachesLowerPriority(t *testing.T)
func TestRelayAffinityFirstFailureExhaustsRemainingCandidates(t *testing.T)
func TestRelayStaysInResolvedAutoGroup(t *testing.T)
```

The beyond-budget case sets `common.RetryTimes = 2`, makes channels 1–4 return
retryable pre-commit errors, makes channel 5 succeed, and asserts attempts
`[1,2,3,4,5]`, `use_channel == ["1","2","3","4","5"]`, status 200, the fifth
response body, and no duplicate IDs. The total-failure case asserts all
candidates exactly once and the last sanitized error. The exclusion case
creates manually disabled, auto-disabled, disabled-ability,
model-incompatible, path-incompatible, and other-group rows and asserts only
eligible same-group IDs are attempted in descending priority order.

- [ ] **Step 3: Add boundary and accounting tests**

Add:

```go
func TestRelayNonRetryableErrorDoesNotFanOut(t *testing.T)
func TestRelayRetriesPreCommitStreamFailure(t *testing.T)
func TestRelayDoesNotRetryAfterStreamCommit(t *testing.T)
func TestRelayCancellationStopsCandidateTraversal(t *testing.T)
```

Use an upstream 400 for the semantic error, an upstream 500 before SSE headers
for pre-commit retry, a stream that emits one valid event before an abrupt
transport failure for post-commit behavior, and a canceled request context
after the first upstream failure. Assert exact upstream attempts, unchanged
downstream prefix for the committed stream, one failed-attempt record per
failed channel, and one success usage/settlement record. Success accounting
assertions are included directly in the fallback fixtures.

- [ ] **Step 4: Capture RED**

Run:

```bash
go test ./controller -run 'TestRelay(Exhausts|ReturnsFinal|Excludes|Affinity|Stays|NonRetryable|RetriesPreCommit|DoesNotRetryAfter|Cancellation)' -count=1 -v
```

Expected: the beyond-budget, total-exhaustion, affinity, resolved-auto-group,
and applicable boundary tests fail because the base loop stops at
`RetryTimes + 1` or advances out of the resolved auto group. Confirm failures
are assertion failures caused by base behavior, not fixture errors.

### Task 2: Implement candidate-driven relay exhaustion

**Files:**
- Modify: `controller/relay.go:181-251`
- Modify: `controller/relay.go:295-367`

- [ ] **Step 1: Freeze the retry group and remove the numeric loop cap**

Construct `RetryParam.TokenGroup` from the concrete `relayInfo.UsingGroup`
after pricing/group resolution:

```go
resolvedGroup := relayInfo.UsingGroup
if autoGroup := common.GetContextKeyString(c, constant.ContextKeyAutoGroup); autoGroup != "" {
    resolvedGroup = autoGroup
}
retryParam := &service.RetryParam{
    Ctx: c, TokenGroup: resolvedGroup, ModelName: relayInfo.OriginModelName,
    RequestPath: c.Request.URL.Path, Retry: common.GetPointer(0),
}
for ; ; retryParam.IncreaseRetry() {
    // one selected channel per iteration
}
```

- [ ] **Step 2: Make selector exhaustion non-exceptional**

Change `getChannel` so an exhausted selector returns `(nil, nil)`, while a
database/cache selection error still returns `ErrorCodeGetChannelFailed`.
When channel setup fails, return the selected channel together with the error:

```go
if channel == nil {
    return nil, nil
}
if setupErr := middleware.SetupContextForSelectedChannel(c, channel, info.OriginModelName); setupErr != nil {
    return channel, setupErr
}
```

At the call site, record any non-nil selected channel before handling setup or
relay errors. A nil channel ends the loop; retain `relayInfo.LastError` when
present and create the existing no-channel error only if no attempt error
exists.

- [ ] **Step 3: Separate retryability from attempt budget**

Remove the `retryTimes` argument and its `<= 0` branch from `shouldRetry`.
Keep fixed-channel, channel-error, skip-retry, error-code, and status-code
rules. Check fixed-channel routing before the unconditional channel-error
branch so an unbounded loop cannot escape a fixed channel. Affinity still
chooses the first candidate, but its legacy skip flag must not terminate
same-group exhaustion.

Before advancing, stop when:

```go
if relayInfo.HasSendResponse() ||
    c.Request.Context().Err() != nil ||
    !shouldRetry(c, newAPIError) {
    break
}
```

Keep `processChannelError` exactly once before this decision.

- [ ] **Step 4: Run focused GREEN tests**

Run the Task 1 command. Expected: all focused controller failover tests pass.

### Task 3: Apply the same bounded semantics to task submission

**Files:**
- Modify: `controller/relay.go:523-581`
- Modify: `controller/relay.go:631-670`
- Test: `controller/relay_failover_test.go`

- [ ] **Step 1: Add a failing task retry-classification test**

Add a table test proving retryable task errors are not rejected only because
the numeric budget is zero, while local/400/fixed-channel errors remain
terminal and an affinity-selected retryable failure still permits same-group
exhaustion:

```go
func TestShouldRetryTaskRelayUsesSemanticsNotGlobalBudget(t *testing.T)
```

Run:

```bash
go test ./controller -run TestShouldRetryTaskRelayUsesSemanticsNotGlobalBudget -count=1 -v
```

Expected: FAIL because `shouldRetryTaskRelay(..., 0)` currently returns false.

- [ ] **Step 2: Use selector exhaustion for unlocked task channels**

Freeze `RetryParam.TokenGroup` to the resolved group and make the unlocked task
loop unbounded but candidate-bounded, using the same `getChannel` nil
exhaustion and once-only `addUsedChannel` behavior. Keep
`relayInfo.LockedChannel` single-channel: after its first failed submit, stop
without selecting or reattempting another channel.

Remove numeric budget from `shouldRetryTaskRelay` while preserving its
status/local-error rules and checking fixed-channel constraints first. Stop
traversal when the request context is canceled.

- [ ] **Step 3: Run controller GREEN**

Run:

```bash
go test ./controller -run 'TestRelay|TestShouldRetry' -count=1 -v
```

Expected: PASS.

### Task 4: Regression verification and review

**Files:**
- Verify all modified files

- [ ] **Step 1: Run scheduler and boundary regressions**

```bash
go test ./model -run 'StrictPriority|RandomSatisfiedChannel|Retry' -count=1 -v
go test ./service -run 'ChannelSelect|StrictPriority|Affinity|Retry|Billing' -count=1 -v
go test ./middleware -run 'Distribute|Affinity|StrictPriority' -count=1 -v
go test ./relay/... -count=1
go test ./controller -count=1
```

Expected: PASS.

- [ ] **Step 2: Run the full suite**

```bash
go test ./... -count=1
```

Expected: PASS. If an unrelated SQLite in-memory failure occurs, rerun the
exact test, its package, and this full command before classifying it.

- [ ] **Step 3: Review invariants**

Inspect `git diff origin/main...HEAD` and verify:

- every loop iteration records a new positive channel ID or terminates;
- fixed/locked channels cannot enter an unbounded retry path;
- auto group retry cannot cross the resolved group;
- no retry occurs after downstream commitment or cancellation;
- health/error accounting occurs once per failed attempt;
- pre-consume/refund/settlement and request-body replay remain single-lifecycle;
- final selector exhaustion retains the last sanitized upstream error.

- [ ] **Step 4: Run final quality checks**

```bash
gofmt -w controller/relay.go controller/relay_failover_test.go
go test ./... -count=1
git diff --check origin/main..HEAD
git status --short
```

Expected: all commands succeed and only intended tracked files plus preserved
untracked `.codegraph/` and `.local/` appear.

### Task 5: Commit and push

**Files:**
- Commit all intended tracked changes

- [ ] **Step 1: Commit**

```bash
git add controller/relay.go controller/relay_failover_test.go \
  controller/relay_retry_rules_test.go model/channel_cache.go \
  docs/superpowers/specs/2026-07-26-exhaust-group-channel-failover-design.md \
  docs/superpowers/plans/2026-07-26-exhaust-group-channel-failover.md
git commit -m "feat: exhaust same-group channel failover"
```

- [ ] **Step 2: Push the normal branch**

```bash
git push -u origin feat/exhaust-group-channel-failover
```

- [ ] **Step 3: Verify push identity**

```bash
git rev-parse HEAD
git rev-parse @{upstream}
git ls-remote origin refs/heads/feat/exhaust-group-channel-failover
git status --short --branch
```

Expected: all three SHAs match; the tracked worktree is clean; `.codegraph/`
and `.local/` remain untracked.
