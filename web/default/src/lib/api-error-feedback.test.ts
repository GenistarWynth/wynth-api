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
import { test } from 'node:test'

import { AxiosHeaders, type AxiosAdapter } from 'axios'

import { getEnabledModels } from '@/features/channels/api'
import {
  getRedemptions,
  searchRedemptions,
} from '@/features/redemption-codes/api'
import {
  resetPlanSubscriptions,
  resetUserSubscriptionsByPlan,
} from '@/features/subscriptions/api'
import {
  deleteStaleSystemInstance,
  deleteStaleSystemInstances,
} from '@/features/system-info/api'

import { api } from './api'

test('feature-owned feedback requests opt out of both Axios error handlers', async () => {
  const originalAdapter = api.defaults.adapter
  const captured: Parameters<AxiosAdapter>[0][] = []
  const adapter: AxiosAdapter = async (config) => {
    captured.push(config)
    return {
      data: { success: true, data: [] },
      status: 200,
      statusText: 'OK',
      headers: new AxiosHeaders(),
      config,
    }
  }
  api.defaults.adapter = adapter

  try {
    await getRedemptions()
    await searchRedemptions({ keyword: 'alpha' })
    await resetUserSubscriptionsByPlan(11, {
      plan_id: 22,
      advance_reset_time: false,
    })
    await resetPlanSubscriptions(22, { advance_reset_time: true })
    await deleteStaleSystemInstances()
    await deleteStaleSystemInstance('node/one')
    await getEnabledModels()
  } finally {
    api.defaults.adapter = originalAdapter
  }

  assert.deepEqual(
    captured.map((config) => `${config.method} ${config.url}`),
    [
      'get /api/redemption/?p=1&page_size=10',
      'get /api/redemption/search?keyword=alpha&p=1&page_size=10',
      'post /api/subscription/admin/users/11/subscriptions/reset',
      'post /api/subscription/admin/plans/22/subscriptions/reset',
      'delete /api/system-info/stale-instances',
      'delete /api/system-info/instances/node%2Fone',
      'get /api/channel/models_enabled',
    ]
  )
  for (const config of captured) {
    assert.equal(config.skipErrorHandler, true, config.url)
    assert.equal(config.skipBusinessError, true, config.url)
  }
})
