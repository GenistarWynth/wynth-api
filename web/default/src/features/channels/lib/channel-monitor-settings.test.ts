import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import type { Channel } from '../types'
import {
  buildChannelAutoPrioritySettingsPayload,
  buildChannelMonitorSettingsPayload,
  isChannelAutoPriorityManagedByUpstream,
  readChannelAutoPrioritySettings,
  readChannelMonitorSettings,
} from './channel-monitor'

// Minimal channel factory — the monitor settings helpers only read
// `settings` and `test_model`, so the rest is cast away for the tests.
function channel(settings: Record<string, unknown>, testModel = ''): Channel {
  return {
    settings: JSON.stringify(settings),
    test_model: testModel,
  } as unknown as Channel
}

describe('channel monitor settings persistence', () => {
  test('identifies generated channels whose auto priority is rule-managed', () => {
    assert.equal(
      isChannelAutoPriorityManagedByUpstream(
        channel({ generated_by_upstream_source_id: 42 })
      ),
      true
    )
    assert.equal(isChannelAutoPriorityManagedByUpstream(channel({})), false)
  })

  test('reads probe settings from the channel settings JSON', () => {
    const draft = readChannelMonitorSettings(
      channel({
        channel_monitor_enabled: true,
        channel_monitor_interval_minutes: 1,
        channel_monitor_model: 'gpt-4o',
      })
    )
    assert.equal(draft.enabled, true)
    assert.equal(draft.intervalMinutes, 1)
    assert.equal(draft.monitorModel, 'gpt-4o')
  })

  test('reads auto-priority settings from the channel settings JSON', () => {
    const draft = readChannelAutoPrioritySettings(
      channel({
        channel_auto_priority_enabled: true,
        channel_auto_priority_interval_minutes: 15,
        channel_auto_priority_window_hours: 12,
        channel_auto_priority_availability_window_hours: 48,
        channel_auto_priority_rate_multiplier: 0.75,
      })
    )
    assert.equal(draft.autoPriorityEnabled, true)
    assert.equal(draft.autoPriorityIntervalMinutes, 15)
    assert.equal(draft.autoPriorityWindowHours, 12)
    assert.equal(draft.autoPriorityAvailabilityWindowHours, 48)
    assert.equal(draft.autoPriorityRateMultiplier, 0.75)
  })

  test('prefers channel_monitor_model over the legacy test_model', () => {
    const draft = readChannelMonitorSettings(
      channel({ channel_monitor_model: 'from-settings' }, 'from-test-model')
    )
    assert.equal(draft.monitorModel, 'from-settings')
  })

  test('falls back to test_model for display only on legacy channels with no monitor config', () => {
    const draft = readChannelMonitorSettings(channel({}, 'legacy-model'))
    assert.equal(draft.monitorModel, 'legacy-model')
    assert.equal(draft.intervalMinutes, 10) // DEFAULT_MONITOR_INTERVAL_MINUTES
  })

  test('does not fall back to test_model once monitor config exists (clearing sticks)', () => {
    const draft = readChannelMonitorSettings(
      channel({ channel_monitor_enabled: true }, 'legacy-model')
    )
    assert.equal(draft.enabled, true)
    assert.equal(draft.monitorModel, '')
  })

  test('writes the monitor model into channel_monitor_model and never emits test_model', () => {
    const payload = buildChannelMonitorSettingsPayload(
      channel({ some_other_setting: 1 }, 'unchanged'),
      {
        enabled: true,
        intervalMinutes: 5,
        monitorModel: 'claude-3',
      }
    )
    // Regression guard: the dialog must not send test_model (which the monitor
    // probe ignores) — only `settings`. See channel-test.go resolveChannelMonitorProbeModel.
    assert.deepEqual(Object.keys(payload), ['settings'])
    const settings = JSON.parse(payload.settings as string)
    assert.equal(settings.channel_monitor_enabled, true)
    assert.equal(settings.channel_monitor_interval_minutes, 5)
    assert.equal(settings.channel_monitor_model, 'claude-3')
    assert.equal(settings.some_other_setting, 1) // preserves unrelated settings
  })

  test('drops interval and monitor model when disabled / cleared', () => {
    const payload = buildChannelMonitorSettingsPayload(
      channel({
        channel_monitor_interval_minutes: 5,
        channel_monitor_model: 'old',
      }),
      {
        enabled: false,
        intervalMinutes: 5,
        monitorModel: '',
      }
    )
    const settings = JSON.parse(payload.settings as string)
    assert.equal(settings.channel_monitor_enabled, false)
    assert.equal('channel_monitor_interval_minutes' in settings, false)
    assert.equal('channel_monitor_model' in settings, false)
  })

  test('writes auto-priority fields while preserving unrelated settings', () => {
    const payload = buildChannelAutoPrioritySettingsPayload(
      channel({
        channel_monitor_enabled: true,
        channel_monitor_interval_minutes: 5,
        channel_monitor_model: 'gpt-4o-mini',
        some_other_setting: 'keep-me',
      }),
      {
        autoPriorityEnabled: true,
        autoPriorityIntervalMinutes: 0,
        autoPriorityWindowHours: 12,
        autoPriorityAvailabilityWindowHours: 48,
        autoPriorityRateMultiplier: 0.8,
      }
    )

    const settings = JSON.parse(payload.settings as string)
    assert.equal(settings.channel_auto_priority_enabled, true)
    assert.equal(settings.channel_auto_priority_interval_minutes, 0)
    assert.equal(settings.channel_auto_priority_window_hours, 12)
    assert.equal(settings.channel_auto_priority_availability_window_hours, 48)
    assert.equal(settings.channel_auto_priority_rate_multiplier, 0.8)
    assert.equal(settings.channel_monitor_enabled, true)
    assert.equal(settings.channel_monitor_interval_minutes, 5)
    assert.equal(settings.channel_monitor_model, 'gpt-4o-mini')
    assert.equal(settings.some_other_setting, 'keep-me')
  })

  test('monitor writes preserve auto-priority settings', () => {
    const payload = buildChannelMonitorSettingsPayload(
      channel({
        channel_auto_priority_enabled: true,
        channel_auto_priority_interval_minutes: 15,
        channel_auto_priority_window_hours: 48,
        channel_auto_priority_availability_window_hours: 12,
        channel_auto_priority_rate_multiplier: 0.75,
      }),
      {
        enabled: true,
        intervalMinutes: 3,
        monitorModel: 'gemini-2.5',
      }
    )

    const settings = JSON.parse(payload.settings as string)
    assert.equal(settings.channel_auto_priority_enabled, true)
    assert.equal(settings.channel_auto_priority_interval_minutes, 15)
    assert.equal(settings.channel_auto_priority_window_hours, 48)
    assert.equal(settings.channel_auto_priority_availability_window_hours, 12)
    assert.equal(settings.channel_auto_priority_rate_multiplier, 0.75)
  })

  test('round-trips an enabled monitor draft', () => {
    const payload = buildChannelMonitorSettingsPayload(channel({}), {
      enabled: true,
      intervalMinutes: 3,
      monitorModel: 'gemini-2.5',
    })
    const draft = readChannelMonitorSettings(
      channel(JSON.parse(payload.settings as string))
    )
    assert.deepEqual(draft, {
      enabled: true,
      intervalMinutes: 3,
      monitorModel: 'gemini-2.5',
    })
  })

  test('round-trips enabled auto-priority settings independently', () => {
    const payload = buildChannelAutoPrioritySettingsPayload(channel({}), {
      autoPriorityEnabled: true,
      autoPriorityIntervalMinutes: 0,
      autoPriorityWindowHours: 48,
      autoPriorityAvailabilityWindowHours: 12,
      autoPriorityRateMultiplier: 0.6,
    })
    const draft = readChannelAutoPrioritySettings(
      channel(JSON.parse(payload.settings as string))
    )
    assert.deepEqual(draft, {
      autoPriorityEnabled: true,
      autoPriorityIntervalMinutes: 0,
      autoPriorityWindowHours: 48,
      autoPriorityAvailabilityWindowHours: 12,
      autoPriorityRateMultiplier: 0.6,
    })
  })
})
