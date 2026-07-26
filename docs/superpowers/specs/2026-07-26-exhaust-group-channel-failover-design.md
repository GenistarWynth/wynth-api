# Exhaust Same-Group Channel Failover Design

## Goal

For a resolved relay group and requested model, try every eligible channel at
most once in the existing scheduler order before returning a retryable
upstream/channel error downstream.

This design supersedes the retry-budget and auto-group fallback decisions in
`docs/superpowers/specs/2026-06-19-strict-channel-priority-design.md`. The
strict-priority selector remains the scheduling authority, but
`common.RetryTimes` no longer caps inter-channel exhaustion and retry must not
leave the already resolved group.

## Current Failure

`middleware.Distribute` resolves the request model, group, affinity preference,
and first channel. `controller.Relay` then owns the relay lifecycle: request
validation, one pre-consume session, request-body replay, per-attempt channel
setup, relay dispatch, health/error accounting, success settlement, and final
error output.

The selector below that loop already:

- filters enabled group/model abilities and special request-path constraints;
- excludes request-local channel IDs from `use_channel`;
- chooses from the highest remaining priority tier;
- preserves weighted random selection inside a tier; and
- returns `(nil, nil)` when no eligible candidate remains.

However, `controller.Relay` stops after `common.RetryTimes + 1` attempts. The
selector therefore never gets a chance to report exhaustion when a group has
more candidates than that fixed budget.

## Selected Architecture

Keep the existing relay loop as the only lifecycle owner and make candidate
exhaustion its termination condition.

1. Freeze retry selection to the concrete group resolved by the distributor.
   For an `auto` token group, use `ContextKeyAutoGroup`; never advance to
   another auto group during relay retry.
2. Record a selected channel ID before any operation that can fail for that
   attempt. This guarantees setup failures cannot cause the same channel to be
   selected forever.
3. Let the existing selector choose one unattempted candidate per iteration.
   A nil channel with no selector error means same-group exhaustion and retains
   the last upstream error as the final error.
4. Reuse the existing error retryability policy without a numeric retry
   budget. Fixed-channel routing, skip-retry errors, configured status/code
   rules, and obvious request-semantic failures remain terminal. An affinity
   hit still controls the first candidate, but its legacy `skip_retry` flag
   cannot terminate same-group exhaustion after a retryable channel failure.
5. Stop after a response has been committed or the request context is canceled.
6. Keep pre-consume, request transforms, request body storage, settlement,
   usage logging, health/error processing, and final response serialization in
   their current single lifecycle.

The same exhaustion rule applies to task submission retries, except a task
locked to its origin channel remains single-channel and cannot fan out.

## Ordering and Eligibility

The next candidate is always obtained through
`service.CacheGetRandomSatisfiedChannel` and
`model.GetRandomSatisfiedChannel`.

- An affinity-selected channel remains the first attempt because it was
  selected by the distributor and is read from context on the first loop.
- Its ID is added to `use_channel`, so subsequent selection cannot repeat it.
- The selector exhausts all unattempted channels at the highest remaining
  priority before considering a lower tier.
- Equal-priority selection retains the current weighted random algorithm.
- Memory-cache eligibility continues to come from enabled channel/ability
  cache entries. Database selection continues to use enabled ability rows.
- Disabled channels/abilities, incompatible models, other groups, and special
  path-incompatible channels never enter the candidate set.
- Complexity is linear in the number of selected candidates plus the existing
  per-selection scheduler cost, and the loop is bounded because every selected
  channel ID is inserted into the attempted set exactly once.

## Error, Stream, Cancellation, and Accounting Boundaries

- A retryable pre-commit upstream/channel failure records channel health/error
  state once and advances to the next candidate.
- A non-retryable request-semantic failure records the failed attempt as today
  and returns immediately.
- Once `RelayInfo.HasSendResponse()` is true, no outer channel retry is allowed.
- A canceled/deadline-exceeded request cannot start another channel attempt.
- If every candidate fails, the last normalized upstream error is retained and
  serialized by the existing final error path; selector exhaustion does not
  overwrite it with a synthetic “no channel” error.
- Billing is pre-consumed once outside the loop. Failed pre-commit attempts do
  not settle usage; the one successful attempt settles once. Total failure
  refunds the one billing session once.
- `processChannelError` remains once per failed channel attempt, preserving
  auto-disable and error-log behavior.

## Verification

Controller-level regression fixtures will use real selection and local HTTP
upstreams to prove:

- success beyond `RetryTimes`, total exhaustion, exclusions, strict lower-tier
  traversal, affinity-first continuation, and group isolation;
- non-retryable, pre-commit retryable, and post-commit stream boundaries;
- exact once-only attempt/accounting behavior, no duplicated usage, and prompt
  cancellation;
- task retry classification and locked-channel behavior where applicable.

Existing model/service/middleware strict-priority, weighted-selection,
affinity, retry classification, streaming, relay, and billing tests will be
rerun before the full repository suite.
