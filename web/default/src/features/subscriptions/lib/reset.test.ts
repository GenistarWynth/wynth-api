import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import { ROLE } from '@/lib/roles'

import {
  resolveSubscriptionResetPermissions,
  resolveSubscriptionResetRequest,
} from './reset'

describe('subscription reset policy', () => {
  test('keeps plan-wide reset root-only and honors user reset permission', () => {
    assert.deepEqual(resolveSubscriptionResetPermissions(ROLE.ADMIN, true), {
      canResetPlan: false,
      canResetUser: true,
    })
    assert.deepEqual(
      resolveSubscriptionResetPermissions(ROLE.SUPER_ADMIN, false),
      { canResetPlan: true, canResetUser: false }
    )
  })

  test('builds user-scoped endpoint and preserves explicit false', () => {
    assert.deepEqual(
      resolveSubscriptionResetRequest(
        { scope: 'user', userId: 7, planId: 42 },
        false
      ),
      {
        url: '/api/subscription/admin/users/7/subscriptions/reset',
        body: { plan_id: 42, advance_reset_time: false },
      }
    )
  })

  test('builds plan-wide endpoint with advance enabled by default', () => {
    assert.deepEqual(
      resolveSubscriptionResetRequest({ scope: 'plan', planId: 42 }),
      {
        url: '/api/subscription/admin/plans/42/subscriptions/reset',
        body: { advance_reset_time: true },
      }
    )
  })
})
