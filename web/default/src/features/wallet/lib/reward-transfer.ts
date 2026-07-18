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
import { getCurrencyDisplay } from '@/lib/currency'
import { parseQuotaFromDollars, quotaUnitsToDollars } from '@/lib/format'
import type { CurrencyConfig } from '@/stores/system-config-store'

type RewardTransferStateInput = {
  amount: number
  availableQuota: number
  currencyConfig: CurrencyConfig
}

export type RewardTransferState = {
  minimumQuota: number
  minimumAmount: number
  maximumAmount: number
  transferQuota: number
  canTransfer: boolean
}

export function deriveRewardTransferState(
  input: RewardTransferStateInput
): RewardTransferState {
  const currencyDisplay = getCurrencyDisplay(input.currencyConfig)
  const quotaPerUnit = currencyDisplay.config.quotaPerUnit
  const minimumQuota = Math.ceil(quotaPerUnit)
  const transferQuota = parseQuotaFromDollars(input.amount, currencyDisplay)
  const minimumAmount = quotaUnitsToDollars(minimumQuota, currencyDisplay)
  const maximumAmount = quotaUnitsToDollars(
    input.availableQuota,
    currencyDisplay
  )
  const roundTripAmount = quotaUnitsToDollars(transferQuota, currencyDisplay)
  // Allow only accumulated IEEE-754 noise, not a distinct display amount.
  const roundTripTolerance =
    Number.EPSILON *
    Math.max(1, Math.abs(input.amount), Math.abs(roundTripAmount)) *
    4
  const isRepresentable =
    Math.abs(input.amount - roundTripAmount) <= roundTripTolerance

  return {
    minimumQuota,
    minimumAmount,
    maximumAmount,
    transferQuota,
    canTransfer:
      Number.isFinite(input.amount) &&
      Number.isFinite(input.availableQuota) &&
      input.amount >= minimumAmount &&
      input.amount <= maximumAmount &&
      isRepresentable &&
      Number.isInteger(transferQuota) &&
      transferQuota >= minimumQuota &&
      transferQuota <= input.availableQuota,
  }
}
