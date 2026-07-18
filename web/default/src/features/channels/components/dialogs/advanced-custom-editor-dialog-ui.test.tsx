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

import { RouteEditor } from './advanced-custom-editor-dialog'

const i18n = createInstance()

before(async () => {
  await i18n.use(initReactI18next).init({
    lng: 'en',
    fallbackLng: 'en',
    resources: {
      en: {
        translation: {
          'Claude Messages': 'Translated Claude Messages',
          'Native forwarding': 'Translated Native Forwarding',
          'OpenAI Chat to Anthropic Messages': 'Translated OpenAI to Anthropic',
        },
      },
    },
    interpolation: { escapeValue: false },
  })
})

function renderRouteEditor(route: Parameters<typeof RouteEditor>[0]['route']) {
  return renderToStaticMarkup(
    createElement(
      I18nextProvider,
      { i18n },
      createElement(RouteEditor, {
        route,
        index: 0,
        onChange: () => undefined,
        onRemove: () => undefined,
      })
    )
  )
}

describe('advanced custom route editor UI', () => {
  test('renders the translated selected label, native visual, responsive delete actions, and aligned auth row', () => {
    const markup = renderRouteEditor({
      incoming_path: '/v1/messages',
      upstream_path: '/v1/messages',
      converter: 'none',
      auth: {
        type: 'header',
        name: 'x-api-key',
        value: '{api_key}',
      },
    })

    assert.match(markup, />Translated Claude Messages</)
    assert.doesNotMatch(markup, /Translated Claude Messages · \/v1\/messages/)
    assert.match(markup, /data-icon="advanced-custom-native"/)
    assert.match(markup, /data-slot="tooltip-trigger"/)
    assert.match(markup, /title="Translated Native Forwarding"/)
    assert.match(markup, /class="sr-only">Translated Native Forwarding<\/span>/)

    assert.equal(
      (markup.match(/class="[^"]*lg:hidden[^"]*"/g) || []).length >= 2,
      true
    )
    assert.match(markup, /class="[^"]*hidden lg:inline-flex[^"]*"/)
    assert.equal((markup.match(/>Delete<\/span>/g) || []).length, 2)

    assert.equal((markup.match(/lg:h-8/g) || []).length >= 3, true)
    assert.match(markup, /lg:gap-1/)
    assert.match(markup, /lg:border-t/)
    assert.match(markup, /lg:pt-2/)
    assert.equal(
      (markup.match(/lg:grid-cols-\[7rem_minmax/g) || []).length >= 2,
      true
    )
  })

  test('renders a distinct Hugeicons conversion visual with translated tooltip semantics', () => {
    const markup = renderRouteEditor({
      incoming_path: '/v1/chat/completions',
      upstream_path: '/v1/messages',
      converter: 'openai_chat_completions_to_anthropic_messages',
    })

    assert.match(markup, /data-icon="advanced-custom-conversion"/)
    assert.match(markup, /title="Translated OpenAI to Anthropic"/)
    assert.match(
      markup,
      /class="sr-only">Translated OpenAI to Anthropic<\/span>/
    )
  })
})
