# Configurable Dead-Channel Recovery and Next-Check UI Design

## Goal

Make post-mortem recovery for auto-disabled, unmonitored channels configurable at runtime and expose the next scheduled recovery probe in the existing channel monitor UI.

## Architecture

The existing `monitor_setting` configuration gains three integer fields: minimum delay minutes, maximum delay minutes, and maximum probes per one-minute worker tick. `operation_setting` owns defaults and normalization so every consumer receives safe values: 15/120/5 when unset, minimum delay reset to 15 below 1, maximum raised to the normalized minimum when smaller, and per-tick count reset to 5 below 1 or capped at 50 above the supported limit.

Pure eligibility and deterministic timing functions move from `controller` into `model`, which is already allowed to depend on `operation_setting`. Both the one-minute worker and `AttachChannelMonitorInfo` call those helpers with the same normalized settings. The deterministic seed remains channel ID plus `status_time`, preserving the current scattering behavior.

`ChannelMonitorInfo` gains `dead_recovery_eligible`, `dead_recovery_next_check_at`, and `dead_recovery_seconds_until_next_check`. Eligibility remains exactly auto-disabled plus per-channel monitor disabled. Enabled, manually disabled, and monitor-enabled channels expose `false` and no recovery timestamp.

The existing Routing Reliability settings section gets three compact numeric fields. The existing Channel Monitor dialog reuses its relative-time formatter and shows “Next post-mortem recovery: …” only for eligible channels. All six frontend locales receive translations.

## Data Flow

1. Administrators save `monitor_setting.dead_channel_recovery_*` through the existing option API.
2. `GetMonitorSetting` normalizes loaded values and environment-overridden monitor settings.
3. Each one-minute recovery tick reads normalized settings, filters eligible due channels using the shared next-check calculation, and caps probes with `max_per_tick`.
4. Channel list/detail hydration reads the same settings and shared timing helper to attach the next-check API fields.
5. The monitor dialog parses the fields and renders the relative next-check time only when `dead_recovery_eligible` is true.

## Validation and Tests

Backend normalization tests cover unset/default and invalid values. Model tests cover deterministic configured bounds, eligibility, next-check calculation, and API exposure. Controller filter tests continue to protect status/monitor exclusions and configured batch caps. Frontend schema and display-text tests protect the payload contract and eligible-only label. Verification includes touched Go packages, frontend tests, typecheck, lint on changed files, formatting, and production build.
