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
  encodeChannelConnectionInfo,
  parseChannelConnectionInfo,
} from './channel-connection-info'

describe('channel connection info codec', () => {
  test('encodes and parses the shared clipboard contract', () => {
    const encoded = encodeChannelConnectionInfo(
      'sk-test',
      'https://api.example.com'
    )

    assert.deepEqual(JSON.parse(encoded), {
      _type: 'newapi_channel_conn',
      key: 'sk-test',
      url: 'https://api.example.com',
    })
    assert.deepEqual(parseChannelConnectionInfo(`  ${encoded}\n`), {
      key: 'sk-test',
      url: 'https://api.example.com',
    })
  })

  test('rejects invalid JSON and values outside the connection contract', () => {
    const invalidValues: unknown[] = [
      null,
      undefined,
      '',
      '{not-json',
      'null',
      '[]',
      '{}',
      JSON.stringify({
        _type: 'other_type',
        key: 'sk-test',
        url: 'https://api.example.com',
      }),
      JSON.stringify({
        _type: 'newapi_channel_conn',
        key: null,
        url: 'https://api.example.com',
      }),
      JSON.stringify({
        _type: 'newapi_channel_conn',
        key: 123,
        url: 'https://api.example.com',
      }),
      JSON.stringify({
        _type: 'newapi_channel_conn',
        key: 'sk-test',
        url: null,
      }),
      JSON.stringify({
        _type: 'newapi_channel_conn',
        key: 'sk-test',
        url: false,
      }),
    ]

    for (const value of invalidValues) {
      assert.equal(
        parseChannelConnectionInfo(value as string | null | undefined),
        null
      )
    }
  })
})
