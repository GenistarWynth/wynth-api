# Per-Channel Dead-Recovery Settings Design

## Goal

Make post-mortem recovery opt-in per channel, remove its global System Settings controls, and keep the next scheduled probe visible in Channel Monitor only when that channel is eligible.

## Architecture

`dto.ChannelOtherSettings` owns `channel_dead_recovery_enabled`, `channel_dead_recovery_min_minutes`, and `channel_dead_recovery_max_minutes`. Missing settings mean disabled with display/runtime defaults of 15 and 120 minutes. Model-level normalization makes malformed stored values safe, while the dedicated update scope validates newly submitted values and merges only these three keys.

The worker no longer reads recovery parameters from `operation_setting.MonitorSetting`. Eligibility requires auto-disabled status, monitoring off, and per-channel recovery enabled. Deterministic scatter continues to use channel ID plus `status_time`, with the normalized bounds from that channel. The one-minute worker retains a fixed internal cap of five probes to limit stampedes; no cap is exposed through product settings.

The existing Channel Monitor dialog shows a “Post-mortem recovery” button only while its monitor switch is off. The button opens a secondary dialog containing the enable switch, minimum and maximum delays, and helper text explaining that the feature applies only to auto-disabled channels with monitoring off. The secondary dialog saves through the existing channel settings endpoint using the dedicated `dead-recovery` scope so monitor and auto-priority fields remain untouched.

## Data Flow

1. Channel Monitor reads recovery values from the selected channel’s `settings` JSON and applies 15/120 defaults without enabling the feature.
2. The secondary dialog validates integer minutes (`min >= 1`, `max >= min`) and sends only channel settings plus scope `dead-recovery`.
3. The backend merges only the three dead-recovery keys and rejects invalid enabled ranges.
4. Each worker tick filters channels using per-channel eligibility and next-check calculations, shuffles due candidates, and starts at most five probes.
5. Channel list/detail hydration emits next-check fields only for eligible channels, so the existing relative-time presentation stays hidden for every other state.

## Migration and Compatibility

Existing channels remain opted out because the enable key is absent. Leftover `monitor_setting.dead_channel_recovery_*` JSON may still be accepted by generic configuration loading, but the typed admin surface and runtime do not expose or consume it.

## Validation and Tests

Backend tests cover default/invalid normalization, exact eligibility, deterministic channel-specific delay bounds, fixed-cap filtering, eligible-only monitor info, and isolated `dead-recovery` persistence. Frontend tests cover read/write defaults, range validation, scope typing, removal of global fields, monitor-off button visibility, and the nested dialog contract. Verification includes all Go tests, all frontend tests, i18n checks, typecheck, changed-file lint and whitespace checks, and a production build.
