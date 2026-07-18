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

import type { ReactElement } from 'react'

import { Combobox } from './combobox'
import { shouldOpenCombobox } from './combobox-input'

describe('ComboboxInput open policy', () => {
  test('keeps non-pointer focus closed when openOnFocus is disabled', () => {
    assert.equal(shouldOpenCombobox('focus', false), false)
    assert.equal(shouldOpenCombobox('focus', true), true)
  })

  test('still opens for pointer interaction and navigation keys', () => {
    assert.equal(shouldOpenCombobox('pointer', false), true)
    assert.equal(shouldOpenCombobox('ArrowDown', false), true)
    assert.equal(shouldOpenCombobox('ArrowUp', false), true)
    assert.equal(shouldOpenCombobox('Enter', false), false)
  })

  test('passes openOnFocus through the legacy Combobox adapter', () => {
    const element = Combobox({
      options: [{ value: '1', label: 'OpenAI' }],
      value: '1',
      onValueChange: () => undefined,
      openOnFocus: false,
    }) as ReactElement<{ openOnFocus?: boolean }>

    assert.equal(element.props.openOnFocus, false)
  })
})
