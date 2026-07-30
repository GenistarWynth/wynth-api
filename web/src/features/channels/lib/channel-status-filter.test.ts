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

import {
  persistChannelStatusFilter,
  resolveChannelStatusFilter,
} from './channel-status-filter'

describe('channel status filter persistence policy', () => {
  test('uses a valid URL status without reading persisted state', () => {
    let storageReads = 0
    const storage = {
      getItem() {
        storageReads += 1
        return 'disabled'
      },
      setItem() {},
    }

    assert.deepEqual(resolveChannelStatusFilter(['enabled'], storage), [
      'enabled',
    ])
    assert.equal(storageReads, 0)
  })

  test('treats invalid, empty, and all URL statuses as no filter', () => {
    let storageReads = 0
    const storage = {
      getItem() {
        storageReads += 1
        return 'enabled'
      },
      setItem() {},
    }

    for (const urlStatus of [[], ['all'], ['unknown'], '', 'all', null]) {
      assert.deepEqual(resolveChannelStatusFilter(urlStatus, storage), [])
    }
    assert.equal(storageReads, 0)
  })

  test('falls back to persisted enabled or disabled only when URL status is absent', () => {
    for (const persisted of ['enabled', 'disabled'] as const) {
      const storage = {
        getItem() {
          return persisted
        },
        setItem() {},
      }

      assert.deepEqual(resolveChannelStatusFilter(undefined, storage), [
        persisted,
      ])
    }

    for (const persisted of [null, '', 'all', 'unknown']) {
      const storage = {
        getItem() {
          return persisted
        },
        setItem() {},
      }

      assert.deepEqual(resolveChannelStatusFilter(undefined, storage), [])
    }
  })

  test('silently falls back to no filter when persisted state is unavailable', () => {
    const storage = {
      getItem(): string | null {
        throw new Error('storage disabled')
      },
      setItem() {},
    }

    assert.doesNotThrow(() => resolveChannelStatusFilter(undefined, storage))
    assert.deepEqual(resolveChannelStatusFilter(undefined, storage), [])
  })

  test('persists enabled and disabled selections and uses all when cleared', () => {
    const writtenValues: string[] = []
    const storage = {
      getItem() {
        return null
      },
      setItem(_key: string, value: string) {
        writtenValues.push(value)
      },
    }

    persistChannelStatusFilter(['enabled'], storage)
    persistChannelStatusFilter(['disabled'], storage)
    persistChannelStatusFilter([], storage)
    persistChannelStatusFilter(['unknown'], storage)

    assert.deepEqual(writtenValues, ['enabled', 'disabled', 'all', 'all'])
  })

  test('silently ignores persistence failures', () => {
    const storage = {
      getItem() {
        return null
      },
      setItem(): void {
        throw new Error('storage disabled')
      },
    }

    assert.doesNotThrow(() => persistChannelStatusFilter(['enabled'], storage))
  })
})
