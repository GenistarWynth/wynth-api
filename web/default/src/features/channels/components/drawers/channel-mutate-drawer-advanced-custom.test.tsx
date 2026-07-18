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

import { AdvancedCustomRouteTypeBadges } from './channel-mutate-drawer'

const i18n = createInstance()

before(async () => {
  await i18n.use(initReactI18next).init({
    lng: 'en',
    fallbackLng: 'en',
    resources: {
      en: {
        translation: {
          'OpenAI Chat': 'Localized OpenAI Chat',
        },
      },
    },
    interpolation: { escapeValue: false },
  })
})

function renderBadges(labels: string[]) {
  return renderToStaticMarkup(
    createElement(
      I18nextProvider,
      { i18n },
      createElement(AdvancedCustomRouteTypeBadges, { labels })
    )
  )
}

describe('advanced custom route type badges', () => {
  test('renders at most three route types and an overflow count with the full title', () => {
    const labels = [
      'OpenAI Chat',
      'OpenAI Responses',
      'Claude Messages',
      'OpenAI Embeddings',
      'OpenAI Image Generations',
    ]
    const markup = renderBadges(labels)

    assert.match(markup, />Localized OpenAI Chat</)
    assert.match(markup, />OpenAI Responses</)
    assert.match(markup, />Claude Messages</)
    assert.doesNotMatch(markup, />OpenAI Embeddings</)
    assert.doesNotMatch(markup, />OpenAI Image Generations</)
    assert.match(markup, />\+2</)
    assert.doesNotMatch(markup, /class="flex flex-wrap gap-2" title=/)
    assert.match(
      markup,
      /data-slot="badge"[^>]*title="Localized OpenAI Chat"[^>]*><span class="truncate">Localized OpenAI Chat<\/span>/
    )
    assert.match(
      markup,
      /data-slot="badge"[^>]*title="Localized OpenAI Chat, OpenAI Responses, Claude Messages, OpenAI Embeddings, OpenAI Image Generations"[^>]*>\+2<\/span>/
    )
  })

  test('keeps visible badge titles without rendering overflow metadata', () => {
    const markup = renderBadges(['OpenAI Responses', 'Claude Messages'])

    assert.match(markup, /title="OpenAI Responses"/)
    assert.match(markup, /title="Claude Messages"/)
    assert.doesNotMatch(markup, /title="OpenAI Responses, Claude Messages"/)
    assert.doesNotMatch(markup, />\+/)
  })
})
