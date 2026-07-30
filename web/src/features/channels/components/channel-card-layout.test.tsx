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

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createInstance } from 'i18next'
import { createElement, useContext, type ReactElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { I18nextProvider, initReactI18next } from 'react-i18next'

import { useSystemConfigStore } from '@/stores/system-config-store'

import { ChannelCard } from './channel-card'
import {
  ChannelRowActionsLayoutContext,
  type ChannelRowActionsLayout,
} from './channel-row-actions-context'
import { BalanceCell } from './channels-columns'
import { ChannelsProvider } from './channels-provider'
import { DataTableRowActions } from './data-table-row-actions'

const i18n = createInstance()
const queryClient = new QueryClient()
const originalConfig = useSystemConfigStore.getState().config

before(async () => {
  const storage = new Map<string, string>()
  Object.defineProperty(globalThis, 'localStorage', {
    configurable: true,
    value: {
      getItem: (key: string) => storage.get(key) ?? null,
      setItem: (key: string, value: string) => storage.set(key, value),
      removeItem: (key: string) => storage.delete(key),
    },
  })
  await i18n.use(initReactI18next).init({
    lng: 'en',
    fallbackLng: 'en',
    resources: { en: { translation: {} } },
    interpolation: { escapeValue: false },
  })
})

function renderWithProviders(element: ReactElement) {
  return renderToStaticMarkup(
    createElement(
      QueryClientProvider,
      { client: queryClient },
      createElement(
        I18nextProvider,
        { i18n },
        createElement(ChannelsProvider, null, element)
      )
    )
  )
}

function renderBalance(layout: ChannelRowActionsLayout) {
  return renderWithProviders(
    createElement(
      ChannelRowActionsLayoutContext.Provider,
      { value: layout },
      createElement(BalanceCell, {
        channel: {
          id: 1,
          type: 1,
          key: 'key',
          status: 1,
          name: 'Channel',
          created_time: 0,
          test_time: 0,
          response_time: 0,
          balance: 12.5,
          balance_updated_time: 0,
          used_quota: 500_000,
          group: 'default',
          settings: '{}',
        } as never,
      })
    )
  )
}

function LayoutProbe({ name }: { name: string }) {
  const layout = useContext(ChannelRowActionsLayoutContext)
  return createElement('span', { [`data-layout-${name}`]: layout }, layout)
}

function makeRow() {
  const channel = {
    id: 1,
    type: 1,
    key: 'key',
    status: 1,
    name: 'Channel',
    created_time: 0,
    test_time: 0,
    response_time: 0,
    balance: 12.5,
    balance_updated_time: 0,
    used_quota: 500_000,
    group: 'default',
    settings: '{}',
  }
  const makeCell = (id: string) => ({
    column: {
      id,
      columnDef: {
        cell: () => createElement(LayoutProbe, { name: id }),
      },
    },
    getContext: () => ({}),
  })
  return {
    original: channel,
    getAllCells: () =>
      [
        'select',
        'type',
        'name',
        'status',
        'actions',
        'priority',
        'weight',
        'balance',
        'response_time',
        'test_time',
      ].map(makeCell),
  } as never
}

function makeActionRow() {
  return {
    original: {
      id: 1,
      type: 1,
      key: 'key',
      status: 1,
      name: 'Channel',
      created_time: 0,
      test_time: 0,
      response_time: 0,
      balance: 12.5,
      balance_updated_time: 0,
      used_quota: 500_000,
      group: 'default',
      settings: '{}',
      channel_info: { is_multi_key: false },
    },
  } as never
}

describe('channel card layout context', () => {
  test('hides currency symbols in cards while retaining them in tables', () => {
    useSystemConfigStore.setState((state) => ({
      config: {
        ...state.config,
        currency: {
          ...state.config.currency,
          quotaDisplayType: 'USD',
        },
      },
    }))

    const tableMarkup = renderBalance('table')
    const cardMarkup = renderBalance('card')
    assert.match(tableMarkup, /\$/)
    assert.doesNotMatch(cardMarkup, /\$/)
  })

  test('provides card layout to every cell renderer, not only actions', () => {
    const markup = renderWithProviders(
      createElement(ChannelCard, { row: makeRow(), isSelected: false })
    )

    assert.match(markup, /data-layout-balance="card"/)
    assert.match(markup, /data-layout-actions="card"/)
  })

  test('keeps inline Edit in tables but removes the redundant card action', () => {
    const tableMarkup = renderWithProviders(
      createElement(
        ChannelRowActionsLayoutContext.Provider,
        { value: 'table' },
        createElement(DataTableRowActions, { row: makeActionRow() })
      )
    )
    const cardMarkup = renderWithProviders(
      createElement(
        ChannelRowActionsLayoutContext.Provider,
        { value: 'card' },
        createElement(DataTableRowActions, { row: makeActionRow() })
      )
    )

    assert.match(tableMarkup, /aria-label="Edit"/)
    assert.doesNotMatch(cardMarkup, /aria-label="Edit"/)
  })
})

// Keep the test process isolated from the formatter's global config.
process.on('exit', () => {
  useSystemConfigStore.setState({ config: originalConfig })
})
