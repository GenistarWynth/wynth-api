import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import { renderAuditContent } from './format'

type AuditCase = {
  action: string
  params: Record<string, string | number | boolean | string[]>
  template: string
}

const cases: AuditCase[] = [
  {
    action: 'subscription.plan_reset',
    params: { plan_id: 42 },
    template: 'Reset active subscriptions for plan {{plan_id}}',
  },
  {
    action: 'subscription.user_plan_reset',
    params: { plan_id: 42, target_user_id: 17 },
    template:
      'Reset active plan {{plan_id}} subscriptions for user {{target_user_id}}',
  },
  {
    action: 'system_instance.delete_stale',
    params: { node_name: 'edge-node-一' },
    template: 'Deleted stale system instance {{node_name}}',
  },
  {
    action: 'system_instance.delete_stale_all',
    params: { deleted_count: 3 },
    template: 'Deleted {{deleted_count}} stale system instances',
  },
]

describe('renderAuditContent reset and stale-instance actions', () => {
  for (const auditCase of cases) {
    test(auditCase.action, () => {
      const localizedContent = `localized:${auditCase.action}`
      const result = renderAuditContent(
        {
          op: {
            action: auditCase.action,
            params: auditCase.params,
          },
        },
        (key, options) => {
          assert.equal(key, auditCase.template)
          assert.deepEqual(options, auditCase.params)
          return localizedContent
        }
      )

      assert.equal(result, localizedContent)
      assert.notEqual(result, auditCase.action)
      assert.notEqual(result, `fallback:${auditCase.action}`)
    })
  }
})
