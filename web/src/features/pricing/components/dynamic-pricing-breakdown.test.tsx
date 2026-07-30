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
import { readFileSync } from 'node:fs'
import { before, describe, test } from 'node:test'
import { fileURLToPath } from 'node:url'

import { createInstance } from 'i18next'
import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { I18nextProvider, initReactI18next } from 'react-i18next'

import { DynamicPricingBreakdown } from './dynamic-pricing-breakdown'

const i18n = createInstance()
const billingExpr = 'tier("standard", p * 0.5 + c * 1.5)'
const detailsDialogSource = readFileSync(
  fileURLToPath(
    new URL(
      '../../usage-logs/components/dialogs/details-dialog.tsx',
      import.meta.url
    )
  ),
  'utf8'
)

before(async () => {
  await i18n.use(initReactI18next).init({
    lng: 'en',
    fallbackLng: 'en',
    resources: { en: { translation: {} } },
    interpolation: { escapeValue: false },
  })
})

function renderBreakdown(compact: boolean): string {
  return renderToStaticMarkup(
    createElement(
      I18nextProvider,
      { i18n },
      createElement(DynamicPricingBreakdown, { billingExpr, compact })
    )
  )
}

describe('dynamic pricing breakdown density', () => {
  test('keeps the standalone pricing heading in the default presentation', () => {
    const markup = renderBreakdown(false)

    assert.match(markup, />Dynamic Pricing</)
    assert.match(markup, />standard</)
  })

  test('removes the repeated heading while preserving tier prices in compact log details', () => {
    const markup = renderBreakdown(true)

    assert.doesNotMatch(markup, />Dynamic Pricing</)
    assert.match(markup, />Tiered price table</)
    assert.match(markup, />standard</)
    assert.match(markup, />\$0\.5000</)
    assert.match(markup, />\$1\.5000</)
  })

  test('uses the compact breakdown inside the standard log-detail section', () => {
    assert.match(
      detailsDialogSource,
      /<DetailSection label=\{t\('Dynamic Pricing'\)\}>[\s\S]*?<DynamicPricingBreakdown[\s\S]*?\bcompact\b[\s\S]*?<\/DetailSection>/
    )
  })
})
