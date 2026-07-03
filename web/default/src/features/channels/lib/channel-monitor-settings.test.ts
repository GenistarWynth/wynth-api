import assert from 'node:assert/strict'
import { describe, test } from 'node:test'
import {
  buildChannelMonitorSettingsPayload,
  readChannelMonitorSettings,
} from './channel-monitor'
import type { Channel } from '../types'

// Minimal channel factory — the monitor settings helpers only read
// `settings` and `test_model`, so the rest is cast away for the tests.
function channel(
  settings: Record<string, unknown>,
  testModel = ''
): Channel {
  return {
    settings: JSON.stringify(settings),
    test_model: testModel,
  } as unknown as Channel
}

describe('channel monitor settings <-> channel_monitor_model binding', () => {
  test('reads the monitor model from channel_monitor_model (rule/probe field)', () => {
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

  test('prefers channel_monitor_model over the legacy test_model', () => {
    const draft = readChannelMonitorSettings(
      channel({ channel_monitor_model: 'from-settings' }, 'from-test-model')
    )
    assert.equal(draft.monitorModel, 'from-settings')
  })

  test('falls back to test_model for display when channel_monitor_model is absent', () => {
    const draft = readChannelMonitorSettings(
      channel({ channel_monitor_enabled: true }, 'legacy-model')
    )
    assert.equal(draft.monitorModel, 'legacy-model')
    assert.equal(draft.intervalMinutes, 10) // DEFAULT_MONITOR_INTERVAL_MINUTES
  })

  test('writes the monitor model into channel_monitor_model and never emits test_model', () => {
    const payload = buildChannelMonitorSettingsPayload(
      channel({ some_other_setting: 1 }, 'unchanged'),
      { enabled: true, intervalMinutes: 5, monitorModel: 'claude-3' }
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
      { enabled: false, intervalMinutes: 5, monitorModel: '' }
    )
    const settings = JSON.parse(payload.settings as string)
    assert.equal(settings.channel_monitor_enabled, false)
    assert.equal('channel_monitor_interval_minutes' in settings, false)
    assert.equal('channel_monitor_model' in settings, false)
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
})
