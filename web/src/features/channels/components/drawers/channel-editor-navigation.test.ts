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
  ADVANCED_SETTINGS_SECTION_IDS,
  CHANNEL_EDITOR_SECTION_IDS,
  hasConfiguredOverrideValue,
  isAdvancedNavigationTarget,
} from './channel-editor-navigation'

describe('channel editor navigation state', () => {
  test('treats empty JSON containers as unconfigured and invalid JSON as configured', () => {
    assert.equal(hasConfiguredOverrideValue(''), false)
    assert.equal(hasConfiguredOverrideValue('null'), false)
    assert.equal(hasConfiguredOverrideValue('{}'), false)
    assert.equal(hasConfiguredOverrideValue('[]'), false)
    assert.equal(hasConfiguredOverrideValue('{"status": 500}'), true)
    assert.equal(hasConfiguredOverrideValue('{'), true)
  })

  test('recognizes advanced parent and child anchors without treating main sections as advanced', () => {
    assert.equal(
      isAdvancedNavigationTarget(CHANNEL_EDITOR_SECTION_IDS.advanced),
      true
    )
    assert.equal(
      isAdvancedNavigationTarget(ADVANCED_SETTINGS_SECTION_IDS.overrideRules),
      true
    )
    assert.equal(
      isAdvancedNavigationTarget(CHANNEL_EDITOR_SECTION_IDS.models),
      false
    )
  })
})
