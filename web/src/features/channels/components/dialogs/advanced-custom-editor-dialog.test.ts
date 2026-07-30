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

import { ADVANCED_CUSTOM_INCOMING_PATH_OPTIONS } from '../../lib/advanced-custom'
import {
  appendAdvancedCustomRouteRows,
  createAdvancedCustomRouteRows,
  getAdvancedCustomIncomingPathChange,
  getAdvancedCustomRouteEditorOptions,
  removeAdvancedCustomRouteRow,
  replaceAdvancedCustomRouteRows,
  updateAdvancedCustomRouteRow,
} from './advanced-custom-editor-state'

function createRouteKeyFactory() {
  let nextId = 0
  return () => {
    nextId += 1
    return `route-${nextId}`
  }
}

describe('advanced custom route editor state', () => {
  test('keeps later route state attached to its stable key after a middle removal', () => {
    const nextKey = createRouteKeyFactory()
    let rows = createAdvancedCustomRouteRows(
      {
        advanced_routes: [
          {
            incoming_path: '/first',
            upstream_path: '/first',
            converter: 'none',
          },
          {
            incoming_path: '/second',
            upstream_path: '/second',
            converter: 'none',
          },
          {
            incoming_path: '/third',
            upstream_path: '/third',
            converter: 'none',
          },
        ],
      },
      nextKey
    )
    const secondKey = rows[1].key
    const thirdKey = rows[2].key

    rows = updateAdvancedCustomRouteRow(rows, thirdKey, {
      upstream_path: 'https://example.com/third',
    })
    rows = removeAdvancedCustomRouteRow(rows, secondKey)

    assert.deepEqual(
      rows.map((row) => row.key),
      ['route-1', 'route-3']
    )
    assert.equal(rows[1].route.incoming_path, '/third')
    assert.equal(rows[1].route.upstream_path, 'https://example.com/third')
  })

  test('keeps keys synchronized and unique through add, JSON replacement, and template append', () => {
    const nextKey = createRouteKeyFactory()
    let rows = createAdvancedCustomRouteRows(
      {
        advanced_routes: [
          {
            incoming_path: '/v1/chat/completions',
            upstream_path: '/v1/chat/completions',
            converter: 'none',
          },
        ],
      },
      nextKey
    )

    rows = appendAdvancedCustomRouteRows(
      rows,
      {
        advanced_routes: [
          {
            incoming_path: '/v1/messages',
            upstream_path: '/v1/messages',
            converter: 'none',
          },
        ],
      },
      nextKey
    )
    assert.equal(new Set(rows.map((row) => row.key)).size, rows.length)

    rows = replaceAdvancedCustomRouteRows(
      {
        advanced_routes: [
          {
            incoming_path: '/v1/responses',
            upstream_path: '/v1/responses',
            converter: 'none',
          },
          {
            incoming_path: '/v1/embeddings',
            upstream_path: '/v1/embeddings',
            converter: 'none',
          },
        ],
      },
      nextKey
    )
    assert.deepEqual(
      rows.map((row) => row.key),
      ['route-3', 'route-4']
    )

    rows = appendAdvancedCustomRouteRows(
      rows,
      {
        advanced_routes: [
          {
            incoming_path: '/v1/images/generations',
            upstream_path: '/v1/images/generations',
            converter: 'none',
          },
          {
            incoming_path: '/v1/images/edits',
            upstream_path: '/v1/images/edits',
            converter: 'none',
          },
        ],
      },
      nextKey
    )
    assert.equal(new Set(rows.map((row) => row.key)).size, rows.length)
    assert.deepEqual(
      rows.map((row) => row.route.incoming_path),
      [
        '/v1/responses',
        '/v1/embeddings',
        '/v1/images/generations',
        '/v1/images/edits',
      ]
    )
  })

  test('offers every incoming path and filters converters for the active path', () => {
    const options = getAdvancedCustomRouteEditorOptions({
      incoming_path: '/v1/messages',
      converter: 'anthropic_messages_to_openai_chat_completions',
    })

    assert.deepEqual(
      options.incomingPathOptions.map((option) => option.value),
      ADVANCED_CUSTOM_INCOMING_PATH_OPTIONS.map((option) => option.value)
    )
    assert.deepEqual(
      options.converterOptions.map((option) => option.value),
      ['none', 'anthropic_messages_to_openai_chat_completions']
    )
  })

  test('resets an incompatible converter when the incoming path changes', () => {
    assert.deepEqual(
      getAdvancedCustomIncomingPathChange(
        {
          incoming_path: '/v1/messages',
          converter: 'anthropic_messages_to_openai_chat_completions',
        },
        '/v1/embeddings'
      ),
      { incoming_path: '/v1/embeddings', converter: 'none' }
    )
    assert.deepEqual(
      getAdvancedCustomIncomingPathChange(
        {
          incoming_path: '/v1/messages',
          converter: 'anthropic_messages_to_openai_chat_completions',
        },
        '/v1/messages'
      ),
      { incoming_path: '/v1/messages' }
    )
  })
})
