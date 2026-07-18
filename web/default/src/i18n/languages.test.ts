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
  convertDetectedLanguage,
  normalizeInterfaceLanguage,
  toIntlLocale,
} from './languages'

describe('interface language normalization', () => {
  test('maps regional Chinese browser locales to the available Chinese resource', () => {
    assert.equal(convertDetectedLanguage('zh-CN'), 'zh')
    assert.equal(convertDetectedLanguage('zh_TW'), 'zh')
    assert.equal(normalizeInterfaceLanguage('zh-Hant-TW'), 'zh')
  })

  test('returns valid Intl locales and safely falls back for malformed values', () => {
    assert.equal(toIntlLocale('zh'), 'zh-CN')
    assert.equal(toIntlLocale('fr-FR'), 'fr-FR')
    assert.equal(toIntlLocale('en_US'), 'en-US')
    assert.equal(toIntlLocale('not a locale'), undefined)
    assert.equal(toIntlLocale(null), undefined)
  })
})
