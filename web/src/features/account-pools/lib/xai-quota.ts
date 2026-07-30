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
  AccountPoolLocalQuotaResetRequest,
  AccountPoolXAIQuotaSnapshot,
} from '../types'

export type XAIQuotaDisplayState = {
  source: string
  remaining?: string
  media: 'eligible' | 'ineligible' | 'unknown'
  fetchedAt: number
  usage24h?: {
    source: string
    requests: number
    tokens: number
    estimated: boolean
  }
}

export function canProbeXAIQuota(
  platform: string | undefined,
  account: Pick<AccountPoolAccount, 'credential_type'>
) {
  return platform === 'xai' && account.credential_type === 'oauth'
}

export function canForceProbeAfterLocalQuotaReset(
  platform: string | undefined,
  account: Pick<AccountPoolAccount, 'credential_type'>
) {
  return canProbeXAIQuota(platform, account)
}

export function defaultLocalQuotaResetRequest(
  account: Pick<AccountPoolAccount, 'request_quota'>
): AccountPoolLocalQuotaResetRequest {
  return {
    clear_cooldown: true,
    reset_request_quota: account.request_quota > 0,
    force_probe: false,
  }
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
    usage24h: snapshot.free_usage_24h_estimate
      ? {
          source: snapshot.free_usage_24h_estimate.source,
          requests: snapshot.free_usage_24h_estimate.requests,
          tokens: snapshot.free_usage_24h_estimate.tokens,
          estimated: snapshot.free_usage_24h_estimate.estimated,
        }
      : undefined,
  }
}
