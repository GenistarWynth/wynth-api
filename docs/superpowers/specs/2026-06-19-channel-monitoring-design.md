# Channel-Bound Monitoring Design

## Goal

Redesign channel monitoring so it is configured per local channel and produces useful availability history for later scheduling decisions.

This is the first monitoring step for Wynth API. It should not implement upstream account pools, upstream channel creation, price synchronization, cache-rate scoring, or automatic priority adjustment yet. It should build the data foundation those features will use.

## Product Decisions

- Monitoring is explicitly enabled per channel.
- New and existing channels default to monitoring disabled.
- There is no user-facing global "enable monitoring" switch.
- Each monitored channel has its own monitor interval.
- Monitoring history is bound directly to `Channel` records.
- The first availability window is at most 7 days.
- Manual channel tests remain available and do not require monitoring to be enabled.
- The first implementation may still use the existing channel test mechanism as the actual probe.

## Current Behavior

Current automatic monitoring is controlled by global operation settings:

- `auto_test_channel_enabled`
- `auto_test_channel_minutes`

When enabled, the scheduler scans all active channels through `testAllChannels()`. A channel can be auto-disabled or auto-enabled based on the existing test result logic, but the system does not keep a channel-level availability history that can later participate in priority or weight calculations.

This is too coarse for Wynth API because expensive upstream channels may not be worth frequent monitoring, while cheap channels need better health data.

## Target Behavior

Each channel controls whether it is monitored and how often it is monitored.

A monitored channel is eligible for an automatic probe when:

1. the channel is not manually disabled;
2. `channel_monitor_enabled` is true;
3. the channel's configured interval has elapsed since its last automatic monitor check.

When an automatic monitor check runs, the system records a history row for the channel and updates a small latest-status snapshot used by the channel list and edit UI.

The scheduler should be an internal background runner. It should not expose a global monitoring on/off product control. The runner can wake at a short fixed cadence, such as once per minute, and only process channels whose own monitor interval is due.

## Scope

In scope:

- Per-channel monitoring toggle.
- Per-channel monitoring interval.
- Automatic monitor scheduler based on per-channel due time.
- Channel-bound monitor history for up to 7 days.
- Latest monitor status fields for UI display.
- Availability statistics over the retained history window.
- Existing auto-disable and auto-enable behavior for automatic checks on monitored channels.
- Frontend changes in the channel create/edit drawer and channel list/detail surfaces.
- Backend tests for scheduler filtering, due selection, history recording, and retention behavior.

Out of scope:

- Upstream source registry.
- sub2api or new-api credential storage.
- Creating local channels from upstream groups.
- Price or multiplier synchronization.
- Automatic priority or weight adjustment.
- Cache-rate monitoring.
- Independent monitor objects that are not tied to a local channel.
- Long availability windows such as 15-day or 30-day reports.

## Channel Settings

Store the per-channel monitor configuration in `dto.ChannelOtherSettings`, following the existing pattern used by upstream model update settings.

Add fields with stable JSON keys:

```text
channel_monitor_enabled
channel_monitor_interval_minutes
channel_monitor_last_checked_at
channel_monitor_last_status
channel_monitor_last_latency_ms
channel_monitor_last_message
```

`channel_monitor_enabled` defaults to false.

`channel_monitor_interval_minutes` defaults to 10 when monitoring is enabled but no value is set. The UI should present sensible choices and the backend should clamp invalid values to a safe minimum of 1 minute.

The latest fields are display snapshots. The monitor history table remains the source of truth for availability calculations.

## Monitor History

Add a channel-bound monitor history table.

Required fields:

```text
id
channel_id
model
status
latency_ms
message
checked_at
created_at
updated_at
```

`channel_id` references the local `channels.id` record.

`status` should use a small controlled set:

```text
success
failed
degraded
error
```

For the first implementation, existing channel test results can map to `success` or `failed`. `degraded` is reserved for later partial-success or slow-success policy. `error` is reserved for probe execution failures that are not a normal upstream failure result.

The history table should have indexes for:

- `channel_id, checked_at`
- `checked_at`

All schema work must remain compatible with SQLite, MySQL, and PostgreSQL through GORM migrations.

## Retention

Keep monitor history for 7 days.

The retention job can run from the same monitor background task and delete rows older than 7 days. The first implementation should not add 15-day or 30-day rollups.

If future traffic volume makes raw 7-day history too large, a daily rollup table can be added later. It is not required for the first implementation because the requested product window is short.

## Availability Calculation

Availability is calculated from retained monitor history:

```text
availability = success_count / total_count
```

For the first implementation:

- `success` counts as available.
- `failed`, `degraded`, and `error` count as unavailable.
- Channels with no monitor history should display no availability percentage rather than `0%`.
- Statistics should be available for the retained 7-day window.

The backend should expose enough data for the frontend to display:

- latest monitor status;
- latest monitor time;
- latest monitor latency;
- latest monitor message;
- 7-day check count;
- 7-day availability percentage.

## Scheduler Design

The automatic monitor runner should:

1. wake on an internal fixed cadence;
2. load channels with monitoring enabled;
3. skip manually disabled channels;
4. skip channels whose interval has not elapsed;
5. run the existing single-channel test flow;
6. record monitor history;
7. update the latest monitor snapshot in channel settings;
8. apply the existing automatic disable or enable decision only for this monitored automatic check;
9. delete expired history rows older than 7 days.

The existing manual test endpoints should continue to test any channel regardless of the monitor setting. Manual tests should not have to write monitor history unless explicitly designed later as "record this manual result"; the first implementation should keep automatic monitor history separate and predictable.

## Auto Disable And Recovery

Existing automatic disable and recovery policy should continue to apply to automatic monitor checks when the channel has monitoring enabled.

Channels with monitoring disabled should not be auto-disabled or auto-enabled by the monitoring runner because they are not probed automatically.

Manual channel tests can still show current reachability but should not silently opt a channel into monitoring or change its configured monitor interval.

## Frontend Design

Add monitoring controls to the channel create/edit drawer:

- enable monitoring toggle;
- interval input or select, visible when monitoring is enabled;
- latest monitor status;
- latest monitor time;
- latest latency;
- 7-day availability.

Remove or hide the global automatic monitoring setting from the system settings UI because the product control moves to each channel.

The channel list should make monitored status easy to scan without crowding the table:

- monitored channels show their latest status and 7-day availability;
- unmonitored channels show a neutral "not monitored" state;
- manual test actions remain separate from monitor configuration.

All new frontend text must use the existing i18n pattern.

## Future Upstream Automation Fit

The monitoring design intentionally binds history to local channels because local channels are what the scheduler ultimately selects.

Later upstream automation can build on this:

1. create or update local channels from a `sub2api` upstream group or a `new-api` upstream source;
2. store upstream price or multiplier metadata on the local channel or a source-managed mapping table;
3. adjust local channel priority from price;
4. later combine price, 7-day availability, and cache rate into a more complete priority or weight policy.

This design does not require the first implementation to know whether the channel originally came from `sub2api`, `new-api`, a manual setup, or a future account pool. Every route goes through the same local channel monitoring history.

## Error Handling

Probe execution failures should still record history when the channel can be identified.

History recording failures should be logged and should not crash the monitor runner. They also should not hide the original probe result from existing auto-disable logic.

Invalid monitor intervals should be normalized by the backend:

- empty or zero interval with monitoring enabled becomes the default interval;
- values below 1 minute become 1 minute;
- monitoring disabled ignores the interval for due-check purposes.

## Migration And Compatibility

Add the new history table through GORM auto migration.

The `ChannelOtherSettings` additions do not require a database column migration because they are stored in the existing channel settings payload.

Existing deployments should behave conservatively after upgrade:

- all existing channels are unmonitored until explicitly enabled;
- the old global auto-monitor setting no longer causes all channels to be tested automatically;
- manual channel tests continue to work.

If legacy global monitor settings still exist in stored system configuration, they should be ignored by the new monitor runner and hidden from the current frontend.

## Testing Strategy

Backend tests should cover real behavior:

1. channels with monitoring disabled are not selected by the automatic monitor scheduler;
2. enabled channels are selected only when their interval is due;
3. manually disabled channels are skipped even if monitoring is enabled;
4. invalid intervals are normalized;
5. automatic monitor results write history rows;
6. latest monitor snapshot fields are updated after an automatic check;
7. 7-day availability excludes history older than the retention window;
8. channels with no history return no availability percentage;
9. retention deletes rows older than 7 days;
10. manual test behavior remains independent from monitor enablement.

Frontend tests or focused manual verification should cover:

- channel edit form can enable and disable monitoring;
- interval field is only meaningful when monitoring is enabled;
- global monitoring setting is no longer presented as the main product control;
- channel list or detail display handles monitored, failed, successful, and unmonitored states.

## Non-Goals For This Spec

This spec intentionally does not decide:

- how to log in to `sub2api` upstreams;
- where to store upstream account credentials;
- how to create keys from upstream groups;
- how to map upstream multipliers to local priorities;
- how cache hit rate should affect scheduling;
- how to merge price and availability into a final scoring function.

Those are separate specs after channel-bound monitoring history exists.
