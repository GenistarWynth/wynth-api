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
import type { ChannelMonitorRecord } from '../types'

export type MonitorVisualStatus =
  | ChannelMonitorRecord['status']
  | 'empty'

export interface MonitorHistoryBar {
  id: string
  status: MonitorVisualStatus
  heightPercent: number
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
    return {
      id: String(record.id ?? `${record.checked_at ?? 'record'}-${index}`),
      status: record.status ?? 'error',
      heightPercent,
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
      heightPercent: 25,
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
