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
  ADVANCED_CUSTOM_INCOMING_PATH_OPTIONS,
  getAdvancedCustomConverterOptions,
  getAdvancedCustomStats,
  isAdvancedCustomIncomingPathAllowed,
} from './advanced-custom'

describe('advanced custom route metadata', () => {
  test('filters converters by the selected incoming path while keeping native forwarding', () => {
    assert.deepEqual(
      getAdvancedCustomConverterOptions('/v1/messages').map(
        (option) => option.value
      ),
      ['none', 'anthropic_messages_to_openai_chat_completions']
    )
    assert.deepEqual(
      getAdvancedCustomConverterOptions('/v1/embeddings').map(
        (option) => option.value
      ),
      ['none']
    )
    assert.deepEqual(
      getAdvancedCustomConverterOptions('/v1/chat/completions').map(
        (option) => option.value
      ),
      [
        'none',
        'openai_chat_completions_to_anthropic_messages',
        'openai_chat_completions_to_openai_responses',
        'openai_chat_completions_to_gemini_generate_content',
      ]
    )
  })

  test('normalizes incoming path whitespace consistently for options and allowed checks', () => {
    const incomingPath = '  /v1/messages  '
    const converter = 'anthropic_messages_to_openai_chat_completions'

    assert.equal(
      getAdvancedCustomConverterOptions(incomingPath).some(
        (option) => option.value === converter
      ),
      true
    )
    assert.equal(
      isAdvancedCustomIncomingPathAllowed(incomingPath, converter),
      true
    )
  })

  test('orders legacy completions after both image routes', () => {
    const paths = ADVANCED_CUSTOM_INCOMING_PATH_OPTIONS.map(
      (option) => option.value
    )

    assert.equal(
      paths.indexOf('/v1/completions') >
        paths.indexOf('/v1/images/generations'),
      true
    )
    assert.equal(
      paths.indexOf('/v1/completions') > paths.indexOf('/v1/images/edits'),
      true
    )
  })

  test('reports unique route type labels in first-seen order', () => {
    const stats = getAdvancedCustomStats(
      JSON.stringify({
        advanced_routes: [
          {
            incoming_path: '/v1/messages',
            upstream_path: '/v1/messages',
            converter: 'none',
          },
          {
            incoming_path: '/v1/chat/completions',
            upstream_path: '/v1/chat/completions',
            converter: 'none',
          },
          {
            incoming_path: '/v1/messages',
            upstream_path: 'https://example.com/v1/messages',
            converter: 'none',
          },
          {
            incoming_path: '/custom/route',
            upstream_path: '/custom/route',
            converter: 'none',
          },
        ],
      })
    )

    assert.deepEqual(stats.routeTypeLabels, [
      'Claude Messages',
      'OpenAI Chat',
      '/custom/route',
    ])
  })

  test('returns no route type labels for invalid JSON', () => {
    assert.deepEqual(getAdvancedCustomStats('{').routeTypeLabels, [])
  })
})
