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
import { createElement, type ReactElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { I18nextProvider, initReactI18next } from 'react-i18next'

import { Input } from '@/components/ui/input'

import {
  getChannelStatusDisplay,
  OFFICIAL_CHANNEL_ID,
  OFFICIAL_CHANNEL_NAME,
} from './constants'
import { UpstreamRatioSyncTable } from './upstream-ratio-sync-table'

const i18n = createInstance()

before(async () => {
  await i18n.use(initReactI18next).init({
    lng: 'en',
    fallbackLng: 'en',
    resources: {
      en: {
        translation: {
          'Auto Disabled': 'Translated Auto Disabled',
        },
      },
    },
    interpolation: { escapeValue: false },
  })
})

function renderWithI18n(element: ReactElement) {
  return renderToStaticMarkup(createElement(I18nextProvider, { i18n }, element))
}

describe('upstream ratio sync layout', () => {
  test('keeps the sync header fixed with an internal body scroller and footer pager', () => {
    const sourceName = `${OFFICIAL_CHANNEL_NAME}(${OFFICIAL_CHANNEL_ID})`
    const markup = renderWithI18n(
      createElement(UpstreamRatioSyncTable, {
        differences: {
          'model-a': {
            model_ratio: {
              current: 1,
              upstreams: { [sourceName]: 1.25 },
              confidence: { [sourceName]: true },
            },
          },
        },
        resolutions: {},
        isDisabled: false,
        isSyncing: false,
        onSelectValue: () => undefined,
        onSelectValues: () => undefined,
        onUnselectValue: () => undefined,
        onUnselectValues: () => undefined,
      })
    )

    assert.match(markup, /min-h-\[520px\]/)
    assert.match(markup, /sticky top-0 z-10/)
    assert.match(markup, /scrollbar-gutter:stable/)
    assert.match(markup, />0\/1</)
    assert.match(markup, new RegExp(`>${OFFICIAL_CHANNEL_NAME}</span>`))
    assert.doesNotMatch(
      markup,
      new RegExp(`>${OFFICIAL_CHANNEL_NAME}\\(${OFFICIAL_CHANNEL_ID}\\)</span>`)
    )
  })

  test('uses inset input rings and translates selector status labels', () => {
    const inputMarkup = renderToStaticMarkup(createElement(Input))
    assert.match(inputMarkup, /focus-visible:ring-inset/)
    assert.match(inputMarkup, /aria-invalid:ring-inset/)

    assert.deepEqual(getChannelStatusDisplay(3, i18n.t), {
      label: 'Translated Auto Disabled',
      variant: 'warning',
    })
  })
})
