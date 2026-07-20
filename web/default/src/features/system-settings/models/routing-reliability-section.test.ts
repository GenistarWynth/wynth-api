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
import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import { routingReliabilitySchema } from './routing-reliability-schema'

const validValues = {
  RetryTimes: 1,
  ChannelDisableThreshold: '',
  AutomaticDisableChannelEnabled: true,
  AutomaticEnableChannelEnabled: true,
  AutomaticDisableKeywords: '',
  AutomaticDisableStatusCodes: '401',
  AutomaticRetryStatusCodes: '500-599',
  monitor_setting: {
    auto_test_channel_enabled: false,
    auto_test_channel_minutes: 10,
    channel_test_mode: 'scheduled_all' as const,
    dead_channel_recovery_min_minutes: 15,
    dead_channel_recovery_max_minutes: 120,
    dead_channel_recovery_max_per_tick: 5,
  },
}

describe('routing reliability recovery settings validation', () => {
  test('keeps valid recovery settings in the parsed form values', () => {
    const parsed = routingReliabilitySchema.parse(validValues)

    assert.equal(parsed.monitor_setting.dead_channel_recovery_min_minutes, 15)
    assert.equal(parsed.monitor_setting.dead_channel_recovery_max_minutes, 120)
    assert.equal(parsed.monitor_setting.dead_channel_recovery_max_per_tick, 5)
  })

  test('rejects a maximum below the minimum', () => {
    const result = routingReliabilitySchema.safeParse({
      ...validValues,
      monitor_setting: {
        ...validValues.monitor_setting,
        dead_channel_recovery_min_minutes: 30,
        dead_channel_recovery_max_minutes: 29,
      },
    })

    assert.equal(result.success, false)
  })

  test('rejects more than 50 probes per tick', () => {
    const result = routingReliabilitySchema.safeParse({
      ...validValues,
      monitor_setting: {
        ...validValues.monitor_setting,
        dead_channel_recovery_max_per_tick: 51,
      },
    })

    assert.equal(result.success, false)
  })
})
