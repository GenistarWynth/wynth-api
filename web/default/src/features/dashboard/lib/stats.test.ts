import assert from 'node:assert/strict'
import { describe, test } from 'node:test'
import {
  calculateCacheHitRate,
  calculateDashboardStats,
} from './stats'

describe('dashboard stats', () => {
  test('calculates cache hit rate against all prompt-side tokens', () => {
    assert.equal(
      calculateCacheHitRate({
        totalInputTokens: 500,
        totalCacheReadTokens: 1500,
        totalCacheCreationTokens: 0,
      }),
      75
    )
  })

  test('includes cache creation tokens in cache hit rate denominator', () => {
    assert.equal(
      calculateCacheHitRate({
        totalInputTokens: 200,
        totalCacheReadTokens: 500,
        totalCacheCreationTokens: 300,
      }),
      50
    )
  })

  test('returns zero cache hit rate when prompt-side tokens are zero', () => {
    assert.equal(
      calculateCacheHitRate({
        totalInputTokens: 0,
        totalCacheReadTokens: 0,
        totalCacheCreationTokens: 0,
      }),
      0
    )
  })

  test('aggregates cache tokens from dashboard quota data', () => {
    assert.deepEqual(
      calculateDashboardStats([
        {
          created_at: 1,
          quota: 10,
          count: 2,
          token_used: 100,
          input_tokens: 70,
          cache_read_tokens: 40,
          cache_creation_tokens: 10,
        },
        {
          created_at: 2,
          quota: 15,
          count: 3,
          token_used: 200,
          input_tokens: 130,
          cache_read_tokens: 60,
          cache_creation_tokens: 20,
        },
      ]),
      {
        totalQuota: 25,
        totalCount: 5,
        totalTokens: 300,
        totalInputTokens: 200,
        totalCacheReadTokens: 100,
        totalCacheCreationTokens: 30,
        cacheHitDenominator: 330,
        cacheHitRate: 30.303,
      }
    )
  })
})
