# Channel Monitor Metrics Display Design

## Goal

Improve the existing channel-bound monitor so an administrator can judge a channel's recent quality at a glance. The monitor should show the same practical signals used when comparing upstream relay stations: first-token latency, endpoint latency, total request latency, input and output tokens, availability, latest status, and recent failures.

This is an incremental design on top of `2026-06-19-channel-monitoring-design.md`. It does not replace the existing per-channel monitor table, scheduler, or retention window.

## Product Decisions

- Keep the monitor entry inside the channel operation flow.
- Improve the single-channel monitor dialog first, not a separate global monitor dashboard.
- Continue to retain raw monitor history for at most 7 days.
- Use the latest 60 monitor records for the compact history strip.
- Treat first-token latency as a comparison signal, not a perfect upstream-provider truth. Some upstreams or non-stream tests may not expose it.
- Continue to configure monitor enablement, interval, and test model inside the monitor dialog.
- Do not implement price-to-priority, cache-rate scoring, automatic weight adjustment, or availability-based routing in this step.

## Current State

The backend already records these fields in `ChannelMonitorLog`:

```text
latency_ms
endpoint_latency_ms
first_token_latency_ms
prompt_tokens
completion_tokens
message
checked_at
```

Automatic probes already call the channel test path with consumption logging disabled. For stream-capable channels, the probe records first-token latency from relay response timing. Prompt and completion token counts are copied from the test usage data when available.

The frontend monitor dialog already has a card-style display, but it is still too sparse for operational comparison. It shows only a small set of metrics and the history strip does not make individual records easy enough to inspect.

## Target Behavior

Opening a channel's monitor dialog should answer these questions without leaving the page:

1. Is this channel currently usable?
2. How often has it been available in the last 7 days?
3. What was the latest first-token latency?
4. What was the latest endpoint latency and total request latency?
5. How many prompt and completion tokens were used by the latest probe?
6. Which model was tested?
7. What happened in recent checks, including errors?

The dialog should preserve the current theme and should not use a visually separate dark/black monitor panel inside a light theme.

## Backend Contract

Use the existing `ChannelMonitorLog` fields. Do not add a new monitor metrics table for this iteration.

The monitor detail endpoint should return:

- current channel monitor settings;
- latest monitor status;
- latest checked time;
- latest tested model;
- latest total latency;
- latest endpoint latency;
- latest first-token latency;
- latest prompt tokens;
- latest completion tokens;
- latest message;
- 7-day total check count;
- 7-day success count;
- 7-day availability;
- average total latency over rows with positive latency;
- average endpoint latency over rows with positive endpoint latency;
- average first-token latency over rows with positive first-token latency;
- recent monitor records in chronological order.

Zeros must be interpreted carefully:

- `0` latency means no usable latency sample, not a successful zero-millisecond request.
- `0` token count means unavailable or not reported unless the corresponding response explicitly proved zero usage.
- frontend display should use "no data" text for unavailable metrics.

## Probe Semantics

Automatic probes should continue to prefer stream mode when the channel type supports streaming. First-token latency is available only when:

1. the request is stream-capable;
2. the upstream sends a response chunk;
3. the relay timing fields were populated.

For non-stream probes or failed responses, first-token latency should stay empty rather than being presented as `0 ms`.

Manual channel tests remain separate from monitor history. A manual test can show timing in its own response, but it should not automatically write a monitor record.

## Frontend Dialog Design

The monitor dialog should be reorganized into four clear areas.

### Header

Show:

- channel icon and name;
- channel type;
- latest tested model;
- latest status pill;
- latest checked time.

The status pill should use the app's existing semantic colors and should work in light and dark themes.

### Monitor Settings

Keep editable settings in the dialog:

- enabled toggle;
- interval in minutes;
- test model selector.

The test model selector should continue to use the channel's configured model list with fuzzy search and custom input fallback.

### Metric Summary

Show compact metric tiles:

- first-token latency;
- endpoint latency;
- total request latency;
- prompt tokens and completion tokens;
- 7-day availability;
- success count and total check count.

Metric labels must be Chinese-translated through the frontend i18n files.

### Recent History

Keep the 60-record history strip, but make each record inspectable. Hovering or focusing a record should expose:

- status;
- checked time;
- tested model;
- total latency;
- endpoint latency;
- first-token latency;
- prompt tokens;
- completion tokens;
- error or monitor message.

The strip height should be based on first-token latency when available, then total latency, then endpoint latency. Error records should remain visible even when no latency sample exists.

## Channel List Display

The channel list should stay compact.

Do not show long inline monitor text in the channel name. The row should expose a monitor action and a concise latest state, while detailed metrics live in the dialog. Future dashboard work can add sortable global monitor columns, but this step should not crowd the channel table.

## Error Handling

If monitor detail loading fails, show a themed empty/error state in the dialog with the backend message.

If the latest monitor record failed, show the error message inside the dialog and inside the record tooltip/details. Do not overwrite the latest useful latency metrics with invented values.

If no records exist, show no-data states for metrics and an empty history strip.

## Testing

Backend tests should cover:

- monitor detail returns latest timing and token fields;
- averages ignore unavailable zero timing samples where appropriate;
- recent records stay chronological;
- no-data channels return empty metrics without errors.

Frontend tests should cover:

- monitor settings remain in the monitor dialog;
- test model selector uses channel models and accepts custom input;
- history records expose timing, token, model, and message data;
- no-data and failed states render translated labels;
- light/dark theme classes do not hard-code an incompatible monitor surface.

## Out Of Scope

- Global monitor dashboard.
- Price synchronization.
- Price-to-priority adjustment.
- Cache-rate monitoring.
- Routing changes based on monitor results.
- Long-term 15-day or 30-day reports.
- Provider-specific monitor adapters beyond the existing channel test path.
