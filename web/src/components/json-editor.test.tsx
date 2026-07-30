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
import { before, describe, test } from 'node:test'

import { createInstance } from 'i18next'
import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { I18nextProvider, initReactI18next } from 'react-i18next'

import { JsonEditor } from './json-editor'
import {
  parseJsonEditorRows,
  serializeJsonEditorRows,
} from './json-editor-model'

const i18n = createInstance()

before(async () => {
  await i18n.use(initReactI18next).init({
    lng: 'en',
    fallbackLng: 'en',
    resources: { en: { translation: {} } },
    interpolation: { escapeValue: false },
  })
})

function renderEditor(value: string) {
  return renderToStaticMarkup(
    createElement(
      I18nextProvider,
      { i18n },
      createElement(JsonEditor, { value, onChange: () => undefined })
    )
  )
}

describe('JsonEditor value synchronization', () => {
  test('renders a non-empty initial object without waiting for an external change', () => {
    const markup = renderEditor(
      JSON.stringify({ endpoint: 'https://example.test', enabled: true })
    )

    assert.match(markup, /value="endpoint"/)
    assert.match(markup, /value="https:\/\/example\.test"/)
    assert.match(markup, /value="enabled"/)
    assert.match(markup, /value="true"/)
  })

  test('preserves nested object and array values as editable JSON strings', () => {
    const rows = parseJsonEditorRows(
      JSON.stringify({
        object: { enabled: true },
        array: ['alpha', 2],
      })
    )

    assert.deepEqual(
      rows.map(({ key, value }) => ({ key, value })),
      [
        { key: 'object', value: '{"enabled":true}' },
        { key: 'array', value: '["alpha",2]' },
      ]
    )
  })

  test('rejects invalid JSON and non-object roots instead of exposing stale rows', () => {
    for (const value of ['{invalid', 'null', '[]', '"text"', '42', 'true']) {
      assert.deepEqual(parseJsonEditorRows(value), [])
    }
  })

  test('round-trips object values in any-value mode', () => {
    const input = {
      text: 'value',
      zero: 0,
      disabled: false,
      nested: { models: ['alpha', 'beta'] },
    }

    const serialized = serializeJsonEditorRows(
      parseJsonEditorRows(JSON.stringify(input)),
      'any'
    )

    assert.deepEqual(JSON.parse(serialized), input)
  })
})
