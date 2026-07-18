import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import { formatQuota } from '@/lib/format'

import {
  formatQuotaInputValue,
  normalizeQuotaInputValue,
} from './quota-settings'

describe('quota settings input policy', () => {
  test('normalizes an invalid valueAsNumber to an empty controlled value', () => {
    assert.equal(normalizeQuotaInputValue(Number.NaN), '')
    assert.equal(normalizeQuotaInputValue(Number.POSITIVE_INFINITY), '')
    assert.equal(normalizeQuotaInputValue(Number.NEGATIVE_INFINITY), '')
    assert.equal(normalizeQuotaInputValue(0), 0)
    assert.equal(normalizeQuotaInputValue(12.5), 12.5)
  })

  test('formats current and empty quota values with the active quota formatter', () => {
    assert.equal(formatQuotaInputValue(250_000), formatQuota(250_000))
    assert.equal(formatQuotaInputValue(''), formatQuota(0))
  })
})
