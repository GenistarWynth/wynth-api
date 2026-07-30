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
  normalizeGroupRatioDraft,
  serializeGroupRatioDraft,
} from './group-ratio-draft'

describe('group ratio draft values', () => {
  test('preserves decimal text until serialization', () => {
    const draft = normalizeGroupRatioDraft('0.05')

    assert.equal(draft, '0.05')
    assert.equal(serializeGroupRatioDraft(draft), 0.05)
  })

  test('normalizes existing numeric values and invalid persisted values', () => {
    assert.equal(normalizeGroupRatioDraft(1.25), '1.25')
    assert.equal(normalizeGroupRatioDraft('not-a-number'), '1')
    assert.equal(serializeGroupRatioDraft(''), 0)
  })
})
