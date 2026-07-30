import { ROLE } from '@/lib/roles'

type SubscriptionResetTarget =
  | { scope: 'plan'; planId: number }
  | { scope: 'user'; userId: number; planId: number }

export function resolveSubscriptionResetPermissions(
  role: number,
  canResetQuota: boolean
) {
  return {
    canResetPlan: role >= ROLE.SUPER_ADMIN,
    canResetUser: canResetQuota,
  }
}

export function resolveSubscriptionResetRequest(
  target: SubscriptionResetTarget,
  advanceResetTime = true
) {
  if (target.scope === 'user') {
    return {
      url: `/api/subscription/admin/users/${target.userId}/subscriptions/reset`,
      body: {
        plan_id: target.planId,
        advance_reset_time: advanceResetTime,
      },
    }
  }

  return {
    url: `/api/subscription/admin/plans/${target.planId}/subscriptions/reset`,
    body: { advance_reset_time: advanceResetTime },
  }
}
