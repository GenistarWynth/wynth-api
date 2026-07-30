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
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { beginPasskeyVerification } from '@/features/auth/passkey/api'
import { api } from '@/lib/api'

import { exportAccountPoolAccounts } from '../api'

describe('account-pool credential export proof calls', () => {
  beforeEach(() => {
    vi.spyOn(api, 'get').mockResolvedValue({ data: { success: true } })
    vi.spyOn(api, 'post').mockResolvedValue({ data: { success: true } })
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('binds passkey proof issuance to the exact pool resource', async () => {
    await beginPasskeyVerification('account_pool.credentials.export', '42')

    expect(api.post).toHaveBeenCalledWith('/api/user/passkey/verify/begin', {
      scope: 'account_pool.credentials.export',
      resource: '42',
    })
  })

  it('sends the one-time proof only on secret export', async () => {
    await exportAccountPoolAccounts(42, true, 'one-time-proof')

    expect(api.get).toHaveBeenCalledWith(
      '/api/account_pools/42/accounts/export?include_secrets=true',
      {
        headers: { 'X-Security-Proof': 'one-time-proof' },
        skipBusinessError: true,
        skipErrorHandler: true,
      }
    )

    await exportAccountPoolAccounts(42, false, 'must-not-be-forwarded')
    expect(api.get).toHaveBeenNthCalledWith(
      2,
      '/api/account_pools/42/accounts/export',
      {
        skipBusinessError: true,
        skipErrorHandler: true,
      }
    )
  })
})
