import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import { isVerificationRequiredError } from '@/lib/secure-verification'

import type { SystemInstance } from '../types'
import {
  buildSystemInstanceDeletePath,
  resolveSystemInstanceCleanup,
  runSystemInstanceCleanup,
} from './instance-cleanup'

function instance(
  nodeName: string,
  status: 'online' | 'stale'
): SystemInstance {
  return {
    node_name: nodeName,
    status,
    stale_after_seconds: 90,
    started_at: 1,
    last_seen_at: status === 'stale' ? 909 : 910,
  }
}

describe('system instance cleanup policy', () => {
  test('exposes row and bulk cleanup only for stale instances', () => {
    const cleanup = resolveSystemInstanceCleanup([
      instance('online', 'online'),
      instance('stale-a', 'stale'),
      instance('stale-b', 'stale'),
    ])

    assert.equal(cleanup.hasStaleInstances, true)
    assert.deepEqual(
      cleanup.staleInstances.map((item) => item.node_name),
      ['stale-a', 'stale-b']
    )
  })

  test('fully hides bulk cleanup when no stale rows exist', () => {
    const cleanup = resolveSystemInstanceCleanup([
      instance('boundary-now-minus-90', 'online'),
    ])

    assert.equal(cleanup.hasStaleInstances, false)
    assert.deepEqual(cleanup.staleInstances, [])
  })

  test('encodes the node name in the single-delete API path', () => {
    assert.equal(
      buildSystemInstanceDeletePath('北京/master 1'),
      '/api/system-info/instances/%E5%8C%97%E4%BA%AC%2Fmaster%201'
    )
  })

  test('assigns initial ordinary failures to the confirm handler', async () => {
    const errors = [
      new Error('Business failure'),
      new Error('Network Error'),
      Object.assign(new Error('Forbidden'), {
        response: { status: 403, data: { code: 'FORBIDDEN' } },
      }),
    ]

    for (const error of errors) {
      const handled: unknown[] = []
      await runSystemInstanceCleanup(
        async () => {
          throw error
        },
        (caught) => handled.push(caught)
      )
      assert.deepEqual(handled, [error])
    }
  })

  test('keeps verification handoff and retry failures outside the initial handler', async () => {
    const verificationRequired = {
      response: {
        status: 403,
        data: { code: 'VERIFICATION_REQUIRED' },
      },
    }
    const retryError = new Error('Retry failed')
    const handled: unknown[] = []
    let attempt = 0
    let retry: (() => Promise<unknown>) | undefined

    const action = async () => {
      attempt += 1
      if (attempt === 1) throw verificationRequired
      throw retryError
    }
    const withVerification = async () => {
      try {
        return await action()
      } catch (error) {
        assert.equal(isVerificationRequiredError(error), true)
        retry = action
        return null
      }
    }

    await runSystemInstanceCleanup(withVerification, (error) => {
      handled.push(error)
    })

    assert.deepEqual(handled, [])
    assert.ok(retry)
    await assert.rejects(retry, retryError)
    assert.deepEqual(handled, [])
  })
})
