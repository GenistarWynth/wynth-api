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
import { readFileSync } from 'node:fs'
import { describe, test } from 'node:test'
import { fileURLToPath } from 'node:url'

import { routingReliabilitySchema } from './routing-reliability-schema'

const sectionSource = readFileSync(
  fileURLToPath(new URL('./routing-reliability-section.tsx', import.meta.url)),
  'utf8'
)

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
  },
}

describe('routing reliability settings ownership', () => {
  test('drops leftover global dead-recovery values from parsed form data', () => {
    const parsed = routingReliabilitySchema.parse({
      ...validValues,
      monitor_setting: {
        ...validValues.monitor_setting,
        dead_channel_recovery_min_minutes: 15,
        dead_channel_recovery_max_minutes: 120,
        dead_channel_recovery_max_per_tick: 5,
      },
    })

    assert.equal(
      'dead_channel_recovery_min_minutes' in parsed.monitor_setting,
      false
    )
    assert.equal(
      'dead_channel_recovery_max_minutes' in parsed.monitor_setting,
      false
    )
    assert.equal(
      'dead_channel_recovery_max_per_tick' in parsed.monitor_setting,
      false
    )
  })

  test('does not render global dead-recovery controls', () => {
    assert.doesNotMatch(sectionSource, /dead_channel_recovery_min_minutes/)
    assert.doesNotMatch(sectionSource, /dead_channel_recovery_max_minutes/)
    assert.doesNotMatch(sectionSource, /dead_channel_recovery_max_per_tick/)
  })
})
