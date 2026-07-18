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
  isMultiSelectValueCreatable,
  transitionMultiSelectCreationInput,
} from './multi-select-creation'

describe('MultiSelect custom value creation', () => {
  test('allows a value that differs from an option value only by case', () => {
    assert.equal(
      isMultiSelectValueCreatable(
        ' GPT-4O ',
        [{ value: 'gpt-4o', label: 'OpenAI model' }],
        []
      ),
      true
    )
  })

  test('allows a value that differs from an option label only by case', () => {
    assert.equal(
      isMultiSelectValueCreatable(
        'GPT-4O',
        [{ value: 'openai-model', label: 'GPT-4o' }],
        []
      ),
      true
    )
  })

  test('rejects exact option value, option label, and selected duplicates', () => {
    const options = [{ value: 'gpt-4o', label: 'GPT-4o' }]

    assert.equal(isMultiSelectValueCreatable('gpt-4o', options, []), false)
    assert.equal(isMultiSelectValueCreatable('GPT-4o', options, []), false)
    assert.equal(
      isMultiSelectValueCreatable('custom-model', options, ['custom-model']),
      false
    )
  })

  test('keeps selected-value deduplication exact and case-sensitive', () => {
    assert.equal(isMultiSelectValueCreatable('GPT-4O', [], ['gpt-4o']), true)
    const transition = transitionMultiSelectCreationInput({
      input: 'GPT-4O,gpt-4o, GPT-4O ,',
      options: [],
      selected: ['gpt-4o'],
    })

    assert.equal(transition.selectionChanged, true)
    assert.deepEqual(transition.nextSelected, ['gpt-4o', 'GPT-4O'])
    assert.equal(transition.draft, '')
  })

  test('preserves comma and newline batch creation with a trailing draft', () => {
    const transition = transitionMultiSelectCreationInput({
      input: ' alpha, beta\nGamma，existing, tail ',
      options: [],
      selected: ['existing'],
    })

    assert.equal(transition.selectionChanged, true)
    assert.deepEqual(transition.nextSelected, [
      'existing',
      'alpha',
      'beta',
      'Gamma',
    ])
    assert.equal(transition.draft, ' tail ')
  })

  test('batch creation rejects exact option and selected conflicts while preserving case variants and draft', () => {
    const selected = ['selected-model']
    const options = [{ value: 'gpt-4o', label: 'OpenAI GPT' }]
    const transition = transitionMultiSelectCreationInput({
      input: 'GPT-4O,gpt-4o,OpenAI GPT,selected-model,Custom，Next\n tail',
      options,
      selected,
    })

    assert.equal(transition.selectionChanged, true)
    assert.deepEqual(transition.nextSelected, [
      'selected-model',
      'GPT-4O',
      'Custom',
      'Next',
    ])
    assert.equal(transition.draft, ' tail')
  })

  test('exact option and selected-only batches do not request onChange', () => {
    const selected = ['selected-model']
    const transition = transitionMultiSelectCreationInput({
      input: 'gpt-4o,OpenAI GPT,selected-model,',
      options: [{ value: 'gpt-4o', label: 'OpenAI GPT' }],
      selected,
    })

    assert.equal(transition.selectionChanged, false)
    assert.equal(transition.nextSelected, selected)
    assert.equal(transition.draft, '')
  })
})
