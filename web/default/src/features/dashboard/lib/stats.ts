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
import type { QuotaDataItem } from '@/features/dashboard/types'

export interface CacheHitRateInput {
  totalInputTokens: number
  totalCacheReadTokens: number
  totalCacheCreationTokens: number
}

/**
 * Safe division: handles NaN and Infinity cases
 */
export function safeDivide(
  value: number,
  divisor: number,
  precision: number = 3
): number {
  const result = value / divisor
  if (isNaN(result) || !isFinite(result)) return 0
  const factor = Math.pow(10, precision)
  return Math.round(result * factor) / factor
}

export function calculateCacheHitRate(input: CacheHitRateInput): number {
  const denominator =
    (Number(input.totalInputTokens) || 0) +
    (Number(input.totalCacheReadTokens) || 0) +
    (Number(input.totalCacheCreationTokens) || 0)
  if (denominator <= 0) return 0
  return safeDivide((Number(input.totalCacheReadTokens) || 0) * 100, denominator)
}

/**
 * Calculate aggregated statistics from quota data
 */
export function calculateDashboardStats(data: QuotaDataItem[]) {
  const totals = data.reduce(
    (acc, item) => {
      const totalInputTokens =
        acc.totalInputTokens + (Number(item.input_tokens) || 0)
      const totalCacheReadTokens =
        acc.totalCacheReadTokens + (Number(item.cache_read_tokens) || 0)
      const totalCacheCreationTokens =
        acc.totalCacheCreationTokens +
        (Number(item.cache_creation_tokens) || 0)

      return {
        totalQuota: acc.totalQuota + (Number(item.quota) || 0),
        totalCount: acc.totalCount + (Number(item.count) || 0),
        totalTokens: acc.totalTokens + (Number(item.token_used) || 0),
        totalInputTokens,
        totalCacheReadTokens,
        totalCacheCreationTokens,
        cacheHitDenominator:
          totalInputTokens + totalCacheReadTokens + totalCacheCreationTokens,
        cacheHitRate: calculateCacheHitRate({
          totalInputTokens,
          totalCacheReadTokens,
          totalCacheCreationTokens,
        }),
      }
    },
    {
      totalQuota: 0,
      totalCount: 0,
      totalTokens: 0,
      totalInputTokens: 0,
      totalCacheReadTokens: 0,
      totalCacheCreationTokens: 0,
      cacheHitDenominator: 0,
      cacheHitRate: 0,
    }
  )

  return totals
}
