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
import type {
  AccountPoolAccount,
  AccountPoolXAIQuotaSnapshot,
} from '../types'

export type XAIQuotaDisplayState = {
  source: string
  remaining?: string
  media: 'eligible' | 'ineligible' | 'unknown'
  fetchedAt: number
}

export function canProbeXAIQuota(
  platform: string | undefined,
  account: Pick<AccountPoolAccount, 'credential_type'>
) {
  return platform === 'xai' && account.credential_type === 'oauth'
}

export function xaiQuotaDisplayState(
  snapshot: AccountPoolXAIQuotaSnapshot | undefined
): XAIQuotaDisplayState | undefined {
  if (!snapshot) return undefined

  const remaining = snapshot.requests?.remaining
  const limit = snapshot.requests?.limit
  let remainingLabel: string | undefined
  if (remaining !== undefined) {
    remainingLabel =
      limit === undefined ? String(remaining) : `${remaining} / ${limit}`
  }

  let media: XAIQuotaDisplayState['media'] = 'unknown'
  if (snapshot.media_eligible !== undefined) {
    media = snapshot.media_eligible ? 'eligible' : 'ineligible'
  }

  return {
    source: snapshot.source,
    remaining: remainingLabel,
    media,
    fetchedAt: snapshot.fetched_at,
  }
}
