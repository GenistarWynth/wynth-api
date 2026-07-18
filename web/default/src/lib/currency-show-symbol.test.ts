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
import { afterEach, describe, test } from 'node:test'

import {
  DEFAULT_CURRENCY_CONFIG,
  useSystemConfigStore,
  type CurrencyConfig,
} from '@/stores/system-config-store'

import {
  formatBillingCurrencyFromUSD,
  formatCurrencyFromUSD,
  formatQuotaWithCurrency,
} from './currency'

const originalConfig = useSystemConfigStore.getState().config

function setCurrency(overrides: Partial<CurrencyConfig>) {
  useSystemConfigStore.setState((state) => ({
    config: {
      ...state.config,
      currency: {
        ...DEFAULT_CURRENCY_CONFIG,
        ...overrides,
      },
    },
  }))
}

afterEach(() => {
  useSystemConfigStore.setState({ config: originalConfig })
})

describe('currency showSymbol option', () => {
  test('removes USD and CNY symbols without changing converted values', () => {
    setCurrency({ quotaDisplayType: 'USD' })
    assert.equal(
      formatCurrencyFromUSD(12.5, {
        abbreviate: false,
        locale: 'en-US',
        showSymbol: false,
      }),
      '12.5'
    )
    assert.equal(
      formatCurrencyFromUSD(12.5, {
        abbreviate: false,
        locale: 'en-US',
      }),
      '$12.5'
    )

    setCurrency({ quotaDisplayType: 'CNY', usdExchangeRate: 7 })
    assert.equal(
      formatCurrencyFromUSD(10, {
        abbreviate: false,
        locale: 'en-US',
        showSymbol: false,
      }),
      '70'
    )
    assert.equal(
      formatCurrencyFromUSD(10, {
        abbreviate: false,
        locale: 'en-US',
      }),
      '¥70'
    )
  })

  test('removes custom symbols and applies to billing/quota formatters', () => {
    setCurrency({
      quotaDisplayType: 'CUSTOM',
      customCurrencySymbol: 'credits',
      customCurrencyExchangeRate: 2,
      quotaPerUnit: 500_000,
    })

    assert.equal(
      formatBillingCurrencyFromUSD(3, {
        abbreviate: false,
        locale: 'en-US',
        showSymbol: false,
      }),
      '6'
    )
    assert.equal(
      formatQuotaWithCurrency(1_500_000, {
        abbreviate: false,
        locale: 'en-US',
        showSymbol: false,
      }),
      '6'
    )
    assert.equal(
      formatCurrencyFromUSD(3, {
        abbreviate: false,
        locale: 'en-US',
      }),
      'credits 6'
    )
  })

  test('keeps token displays unchanged and still supports compact numbers', () => {
    setCurrency({ quotaDisplayType: 'TOKENS', quotaPerUnit: 500_000 })
    const withSymbol = formatCurrencyFromUSD(10, {
      abbreviate: false,
      locale: 'en-US',
    })
    const withoutSymbol = formatCurrencyFromUSD(10, {
      abbreviate: false,
      locale: 'en-US',
      showSymbol: false,
    })
    assert.equal(withoutSymbol, withSymbol)
    assert.equal(withoutSymbol, '5000000')

    setCurrency({ quotaDisplayType: 'USD' })
    assert.equal(
      formatCurrencyFromUSD(280_000, {
        compact: true,
        locale: 'en-US',
        showSymbol: false,
      }),
      '280K'
    )
    assert.equal(formatCurrencyFromUSD(Number.NaN, { showSymbol: false }), '-')
  })
})
