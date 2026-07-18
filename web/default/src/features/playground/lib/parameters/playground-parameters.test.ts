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

import { DEFAULT_CONFIG, DEFAULT_PARAMETER_ENABLED } from '../../constants'
import { buildChatCompletionPayload } from '../streaming/payload-builder'
import {
  getParameterControlValueText,
  normalizeParameterNumberValue,
  PLAYGROUND_PARAMETER_CONTROLS,
} from './playground-parameters'

describe('playground parameter controls', () => {
  test('exposes the six persisted request parameters in a stable order', () => {
    assert.deepEqual(
      PLAYGROUND_PARAMETER_CONTROLS.map((control) => control.key),
      [
        'temperature',
        'top_p',
        'frequency_penalty',
        'presence_penalty',
        'max_tokens',
        'seed',
      ]
    )
  })

  test('clamps decimals and integer inputs to each control contract', () => {
    assert.equal(normalizeParameterNumberValue('temperature', 2), 1)
    assert.equal(normalizeParameterNumberValue('temperature', '0.36'), 0.4)
    assert.equal(normalizeParameterNumberValue('frequency_penalty', -3), -2)
    assert.equal(normalizeParameterNumberValue('max_tokens', 12.9), 12)
    assert.equal(normalizeParameterNumberValue('seed', ''), null)
    assert.equal(normalizeParameterNumberValue('seed', 'not-a-number'), null)
  })

  test('labels an unset seed without changing persisted numeric values', () => {
    assert.equal(getParameterControlValueText('seed', null), 'Not set')
    assert.equal(getParameterControlValueText('temperature', 0), '0')
  })

  test('sends only enabled parameters while preserving explicit zero values', () => {
    const config = {
      ...DEFAULT_CONFIG,
      temperature: 0,
      max_tokens: 0,
      seed: 0,
    }
    const payload = buildChatCompletionPayload([], config, {
      ...DEFAULT_PARAMETER_ENABLED,
      temperature: true,
      frequency_penalty: false,
      max_tokens: true,
      seed: true,
    })

    assert.equal(payload.temperature, 0)
    assert.equal(payload.max_tokens, 0)
    assert.equal(payload.seed, 0)
    assert.equal('frequency_penalty' in payload, false)
    assert.equal('presence_penalty' in payload, true)
  })
})
