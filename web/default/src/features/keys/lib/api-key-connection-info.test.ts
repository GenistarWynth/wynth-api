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

import { parseChannelConnectionInfo } from '@/lib/channel-connection-info'

import {
  encodeApiKeyConnectionInfo,
  getServerAddress,
} from './api-key-connection-info'

describe('API key connection info producer', () => {
  test('prefers the configured status server address over window origin', () => {
    const environment = {
      storage: {
        getItem() {
          return JSON.stringify({
            server_address: 'https://configured.example.com',
          })
        },
      },
      origin: 'https://window.example.com',
    }

    assert.equal(
      getServerAddress(environment),
      'https://configured.example.com'
    )
    assert.deepEqual(
      parseChannelConnectionInfo(
        encodeApiKeyConnectionInfo('sk-real-key', environment)
      ),
      {
        key: 'sk-real-key',
        url: 'https://configured.example.com',
      }
    )
  })

  test('falls back to window origin when status has no server address', () => {
    const environment = {
      storage: {
        getItem() {
          return JSON.stringify({ server_address: '' })
        },
      },
      origin: 'https://window.example.com',
    }

    assert.deepEqual(
      parseChannelConnectionInfo(
        encodeApiKeyConnectionInfo('sk-real-key', environment)
      ),
      {
        key: 'sk-real-key',
        url: 'https://window.example.com',
      }
    )
  })

  test('falls back to window origin when status storage is malformed or unavailable', () => {
    const malformed = {
      storage: {
        getItem() {
          return '{not-json'
        },
      },
      origin: 'https://window.example.com',
    }
    const unavailable = {
      storage: {
        getItem(): string | null {
          throw new Error('storage disabled')
        },
      },
      origin: 'https://window.example.com',
    }

    assert.equal(getServerAddress(malformed), 'https://window.example.com')
    assert.equal(getServerAddress(unavailable), 'https://window.example.com')
  })
})
