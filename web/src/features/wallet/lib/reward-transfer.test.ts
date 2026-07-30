import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import { getCurrencyDisplay } from '@/lib/currency'
import { parseQuotaFromDollars, quotaUnitsToDollars } from '@/lib/format'
import {
  DEFAULT_CURRENCY_CONFIG,
  type CurrencyConfig,
} from '@/stores/system-config-store'

import { deriveRewardTransferState } from './reward-transfer'

function createCurrencyConfig(
  overrides: Partial<CurrencyConfig>
): CurrencyConfig {
  return {
    ...DEFAULT_CURRENCY_CONFIG,
    ...overrides,
  }
}

function quotaAmount(units: number, currencyConfig: CurrencyConfig): number {
  return quotaUnitsToDollars(units, getCurrencyDisplay(currencyConfig))
}

describe('reward transfer policy', () => {
  test('derives an integer minimum quota from the live configured quota unit', () => {
    const currencyConfig = createCurrencyConfig({
      quotaPerUnit: 2.5,
      quotaDisplayType: 'USD',
    })

    const state = deriveRewardTransferState({
      amount: quotaAmount(3, currencyConfig),
      availableQuota: 9,
      currencyConfig,
    })

    assert.equal(state.minimumQuota, 3)
    assert.equal(state.minimumAmount, 1.2)
    assert.equal(state.maximumAmount, 3.6)
    assert.equal(state.transferQuota, 3)
    assert.equal(Number.isInteger(state.transferQuota), true)
    assert.equal(state.canTransfer, true)
  })

  test('uses the active display currency for input limits and quota conversion', () => {
    const currencyConfig = createCurrencyConfig({
      quotaPerUnit: 500,
      quotaDisplayType: 'CNY',
      usdExchangeRate: 7,
    })

    const state = deriveRewardTransferState({
      amount: 7,
      availableQuota: 1_000,
      currencyConfig,
    })

    assert.equal(state.minimumQuota, 500)
    assert.equal(state.minimumAmount, 7)
    assert.equal(state.maximumAmount, 14)
    assert.equal(state.transferQuota, 500)
    assert.equal(state.canTransfer, true)
  })

  test('accepts a representable decimal amount in CNY display mode', () => {
    const currencyConfig = createCurrencyConfig({
      quotaPerUnit: 5,
      quotaDisplayType: 'CNY',
      usdExchangeRate: 7.1,
    })

    const state = deriveRewardTransferState({
      amount: 8.52,
      availableQuota: 10,
      currencyConfig,
    })

    assert.equal(state.minimumAmount, 7.1)
    assert.equal(state.maximumAmount, 14.2)
    assert.equal(state.transferQuota, 6)
    assert.equal(state.canTransfer, true)
  })

  test('uses a custom currency exchange rate for transfer conversion', () => {
    const currencyConfig = createCurrencyConfig({
      quotaPerUnit: 500,
      quotaDisplayType: 'CUSTOM',
      customCurrencySymbol: '€',
      customCurrencyExchangeRate: 0.9,
    })

    const state = deriveRewardTransferState({
      amount: 0.9,
      availableQuota: 1_000,
      currencyConfig,
    })

    assert.equal(state.minimumAmount, 0.9)
    assert.equal(state.maximumAmount, 1.8)
    assert.equal(state.transferQuota, 500)
    assert.equal(state.canTransfer, true)
  })

  test('uses the requested quota unit instead of a mismatched currency snapshot', () => {
    const mismatchedCurrencyDisplay = getCurrencyDisplay(
      createCurrencyConfig({
        quotaPerUnit: 1_000,
        quotaDisplayType: 'USD',
      })
    )
    const currencyConfig = createCurrencyConfig({
      quotaPerUnit: 500,
      quotaDisplayType: 'USD',
    })

    assert.equal(parseQuotaFromDollars(1, mismatchedCurrencyDisplay), 1_000)

    const state = deriveRewardTransferState({
      amount: 1,
      availableQuota: 1_000,
      currencyConfig,
    })

    assert.equal(state.minimumQuota, 500)
    assert.equal(state.transferQuota, 500)
    assert.equal(state.canTransfer, true)
  })

  test('uses raw quota amounts when the display mode is tokens', () => {
    const currencyConfig = createCurrencyConfig({
      quotaPerUnit: 2.5,
      quotaDisplayType: 'TOKENS',
    })

    const state = deriveRewardTransferState({
      amount: 3,
      availableQuota: 9,
      currencyConfig,
    })

    assert.equal(state.minimumQuota, 3)
    assert.equal(state.minimumAmount, 3)
    assert.equal(state.maximumAmount, 9)
    assert.equal(state.transferQuota, 3)
    assert.equal(state.canTransfer, true)
  })

  test('falls back to the default quota unit when the configured value is invalid', () => {
    for (const quotaPerUnit of [
      0,
      -1,
      Number.NaN,
      Number.POSITIVE_INFINITY,
      Number.NEGATIVE_INFINITY,
    ]) {
      const currencyConfig = createCurrencyConfig({
        quotaPerUnit,
        quotaDisplayType: 'USD',
      })

      const state = deriveRewardTransferState({
        amount: 1,
        availableQuota: DEFAULT_CURRENCY_CONFIG.quotaPerUnit,
        currencyConfig,
      })

      assert.equal(state.minimumQuota, DEFAULT_CURRENCY_CONFIG.quotaPerUnit)
      assert.equal(state.minimumAmount, 1)
      assert.equal(state.transferQuota, DEFAULT_CURRENCY_CONFIG.quotaPerUnit)
      assert.equal(state.canTransfer, true)
    }
  })

  test('rejects NaN, below-minimum, and over-balance display amounts', () => {
    const currencyConfig = createCurrencyConfig({
      quotaPerUnit: 500,
      quotaDisplayType: 'USD',
    })

    const invalidAmounts = [
      Number.NaN,
      quotaAmount(499, currencyConfig),
      quotaAmount(1_001, currencyConfig),
    ]

    for (const amount of invalidAmounts) {
      const state = deriveRewardTransferState({
        amount,
        availableQuota: 1_000,
        currencyConfig,
      })
      assert.equal(state.canTransfer, false)
    }
  })

  test('rejects a fractional display amount just below the displayed minimum', () => {
    const currencyConfig = createCurrencyConfig({
      quotaPerUnit: 2.5,
      quotaDisplayType: 'USD',
    })

    const state = deriveRewardTransferState({
      amount: 1.1,
      availableQuota: 3,
      currencyConfig,
    })

    assert.equal(state.minimumAmount, 1.2)
    assert.equal(state.transferQuota, 3)
    assert.equal(state.canTransfer, false)
  })

  test('rejects a fractional display amount just above the displayed maximum', () => {
    const currencyConfig = createCurrencyConfig({
      quotaPerUnit: 2.5,
      quotaDisplayType: 'USD',
    })

    const state = deriveRewardTransferState({
      amount: 1.3,
      availableQuota: 3,
      currencyConfig,
    })

    assert.equal(state.maximumAmount, 1.2)
    assert.equal(state.transferQuota, 3)
    assert.equal(state.canTransfer, false)
  })

  test('rejects a display amount that cannot round-trip through integer quota', () => {
    const currencyConfig = createCurrencyConfig({
      quotaPerUnit: 2.5,
      quotaDisplayType: 'USD',
    })

    const state = deriveRewardTransferState({
      amount: 1.3,
      availableQuota: 10,
      currencyConfig,
    })

    assert.equal(state.minimumAmount, 1.2)
    assert.equal(state.maximumAmount, 4)
    assert.equal(state.transferQuota, 3)
    assert.equal(state.canTransfer, false)
  })

  test('re-derives the minimum amount when the live quota unit changes', () => {
    const firstCurrencyConfig = createCurrencyConfig({
      quotaPerUnit: 500.5,
      quotaDisplayType: 'USD',
    })
    const first = deriveRewardTransferState({
      amount: 0,
      availableQuota: 2_000,
      currencyConfig: firstCurrencyConfig,
    })

    const secondCurrencyConfig = createCurrencyConfig({
      quotaPerUnit: 1_000.5,
      quotaDisplayType: 'USD',
    })
    const second = deriveRewardTransferState({
      amount: 0,
      availableQuota: 2_000,
      currencyConfig: secondCurrencyConfig,
    })

    assert.equal(first.minimumQuota, 501)
    assert.equal(second.minimumQuota, 1_001)
    assert.notEqual(first.minimumAmount, second.minimumAmount)
  })
})
