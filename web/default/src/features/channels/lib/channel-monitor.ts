/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import type { Channel, ChannelMonitorRecord } from '../types'

export type MonitorVisualStatus =
  | ChannelMonitorRecord['status']
  | 'empty'

export type MonitorHistoryTone = 'success' | 'warning' | 'danger' | 'empty'

export interface MonitorHistoryBar {
  id: string
  status: MonitorVisualStatus
  tone: MonitorHistoryTone
  heightPercent: number
  model: string
  latencyMS: number
  endpointLatencyMS: number
  firstTokenLatencyMS: number
  promptTokens: number
  completionTokens: number
  checkedAt: number
  message: string
}

export function monitorStatusText(
  status: MonitorVisualStatus | undefined,
  t: (key: string) => string
) {
  if (status === 'success') return t('Normal')
  if (status === 'degraded') return t('Degraded')
  if (status === 'failed') return t('Failed')
  if (status === 'error') return t('Error')
  return t('No data')
}

export function monitorHistoryTone(status: MonitorVisualStatus): MonitorHistoryTone {
  if (status === 'success') return 'success'
  if (status === 'degraded') return 'warning'
  if (status === 'failed' || status === 'error') return 'danger'
  return 'empty'
}

function metricValue(record: Partial<ChannelMonitorRecord>) {
  if (typeof record.first_token_latency_ms === 'number' && record.first_token_latency_ms > 0) {
    return record.first_token_latency_ms
  }
  if (typeof record.latency_ms === 'number' && record.latency_ms > 0) {
    return record.latency_ms
  }
  if (typeof record.endpoint_latency_ms === 'number' && record.endpoint_latency_ms > 0) {
    return record.endpoint_latency_ms
  }
  return 0
}

export function buildMonitorHistoryBars(
  records: Array<Partial<ChannelMonitorRecord>>,
  count = 60
): MonitorHistoryBar[] {
  const safeCount = Math.max(1, Math.trunc(count))
  const recentRecords = records.slice(-safeCount)
  const maxMetric = Math.max(1, ...recentRecords.map(metricValue))
  const bars = recentRecords.map((record, index): MonitorHistoryBar => {
    const value = metricValue(record)
    const heightPercent = value > 0 ? Math.max(25, Math.round((value / maxMetric) * 100)) : 25
    const status = record.status ?? 'error'
    return {
      id: String(record.id ?? `${record.checked_at ?? 'record'}-${index}`),
      status,
      tone: monitorHistoryTone(status),
      heightPercent,
      model: record.model ?? '',
      latencyMS: record.latency_ms ?? 0,
      endpointLatencyMS: record.endpoint_latency_ms ?? 0,
      firstTokenLatencyMS: record.first_token_latency_ms ?? 0,
      promptTokens: record.prompt_tokens ?? 0,
      completionTokens: record.completion_tokens ?? 0,
      checkedAt: record.checked_at ?? 0,
      message: record.message ?? '',
    }
  })

  const emptyCount = safeCount - bars.length
  if (emptyCount <= 0) return bars

  return [
    ...Array.from({ length: emptyCount }, (_, index): MonitorHistoryBar => ({
      id: `empty-${index}`,
      status: 'empty',
      tone: 'empty',
      heightPercent: 25,
      model: '',
      latencyMS: 0,
      endpointLatencyMS: 0,
      firstTokenLatencyMS: 0,
      promptTokens: 0,
      completionTokens: 0,
      checkedAt: 0,
      message: '',
    })),
    ...bars,
  ]
}

export interface ChannelMonitorSettingsDraft {
  enabled: boolean
  intervalMinutes: number
  monitorModel: string
}

export const DEFAULT_MONITOR_INTERVAL_MINUTES = 10

function parseChannelSettings(settings: string | null | undefined) {
  if (!settings?.trim()) return {}
  try {
    const parsed = JSON.parse(settings)
    if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
      return parsed as Record<string, unknown>
    }
  } catch {
    return {}
  }
  return {}
}

export function normalizeMonitorInterval(value: unknown, fallback: number) {
  const interval = Number(value)
  if (Number.isInteger(interval) && interval >= 1) return interval
  return fallback
}

// readChannelMonitorSettings maps a channel's persisted OtherSettings onto the
// monitor dialog draft. The monitor probe reads channel_monitor_model (also set
// by upstream-source rules), so the dialog must bind to that same field — with a
// fallback to the legacy top-level test_model for display only.
export function readChannelMonitorSettings(
  channel: Channel | null
): ChannelMonitorSettingsDraft {
  const settings = parseChannelSettings(channel?.settings)
  const monitorModel =
    typeof settings.channel_monitor_model === 'string'
      ? settings.channel_monitor_model.trim()
      : ''
  // Fall back to the legacy top-level test_model for display ONLY on channels
  // that have no monitor configuration yet. Once any monitor setting exists,
  // channel_monitor_model is authoritative, so clearing it in the dialog sticks
  // instead of visibly reverting to test_model on the next read.
  const hasMonitorConfig =
    'channel_monitor_model' in settings ||
    'channel_monitor_enabled' in settings ||
    'channel_monitor_interval_minutes' in settings
  return {
    enabled: settings.channel_monitor_enabled === true,
    intervalMinutes: normalizeMonitorInterval(
      settings.channel_monitor_interval_minutes,
      DEFAULT_MONITOR_INTERVAL_MINUTES
    ),
    monitorModel:
      monitorModel ||
      (hasMonitorConfig ? '' : (channel?.test_model?.trim() ?? '')),
  }
}

// buildChannelMonitorSettingsPayload serializes the draft back into a partial
// channel update. Only `settings` is returned; channel.Update() persists via
// GORM Updates (field-aware), so omitted fields (e.g. test_model) are untouched.
export function buildChannelMonitorSettingsPayload(
  channel: Channel,
  draft: ChannelMonitorSettingsDraft
): Pick<Channel, 'settings'> {
  const settings = parseChannelSettings(channel.settings)
  settings.channel_monitor_enabled = draft.enabled
  if (draft.enabled) {
    settings.channel_monitor_interval_minutes = normalizeMonitorInterval(
      draft.intervalMinutes,
      DEFAULT_MONITOR_INTERVAL_MINUTES
    )
  } else {
    delete settings.channel_monitor_interval_minutes
  }
  const monitorModel = draft.monitorModel.trim()
  if (monitorModel) {
    settings.channel_monitor_model = monitorModel
  } else {
    delete settings.channel_monitor_model
  }
  return {
    settings: JSON.stringify(settings),
  }
}
