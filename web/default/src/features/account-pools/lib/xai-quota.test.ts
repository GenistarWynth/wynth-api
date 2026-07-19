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
import { describe, expect, it } from 'vitest'

import type { AccountPoolAccount } from '../types'
import {
  canForceProbeAfterLocalQuotaReset,
  canProbeXAIQuota,
  defaultLocalQuotaResetRequest,
  xaiQuotaDisplayState,
} from './xai-quota'

const account = {
  credential_type: 'oauth',
} as AccountPoolAccount

describe('xAI quota presentation', () => {
  it('offers the probe only for xAI OAuth accounts', () => {
    expect(canProbeXAIQuota('xai', account)).toBe(true)
    expect(canProbeXAIQuota('openai', account)).toBe(false)
    expect(
      canProbeXAIQuota('xai', {
        ...account,
        credential_type: 'api_key',
      })
    ).toBe(false)
  })

  it('summarizes known remaining quota and media eligibility', () => {
    expect(
      xaiQuotaDisplayState({
        source: 'hybrid_probe',
        requests: { limit: 100, remaining: 42 },
        headers_observed: true,
        media_eligible: false,
        media_eligibility_reason: 'billing_free_tier',
        fetched_at: 1_721_347_200,
      })
    ).toEqual({
      source: 'hybrid_probe',
      remaining: '42 / 100',
      media: 'ineligible',
      fetchedAt: 1_721_347_200,
    })
  })

  it('keeps unknown eligibility distinct from ineligible', () => {
    const state = xaiQuotaDisplayState({
      source: 'billing_probe',
      headers_observed: false,
      fetched_at: 1,
    })
    expect(state?.media).toBe('unknown')
    expect(xaiQuotaDisplayState(undefined)).toBeUndefined()
  })

  it('builds an explicitly local reset request and gates force-probe', () => {
    expect(canForceProbeAfterLocalQuotaReset('xai', account)).toBe(true)
    expect(canForceProbeAfterLocalQuotaReset('openai', account)).toBe(false)
    expect(
      defaultLocalQuotaResetRequest({
        request_quota: 100,
        credential_type: 'oauth',
      } as AccountPoolAccount)
    ).toEqual({
      clear_cooldown: true,
      reset_request_quota: true,
      force_probe: false,
    })
    expect(
      defaultLocalQuotaResetRequest({
        request_quota: 0,
        credential_type: 'api_key',
      } as AccountPoolAccount)
    ).toEqual({
      clear_cooldown: true,
      reset_request_quota: false,
      force_probe: false,
    })
  })
})
