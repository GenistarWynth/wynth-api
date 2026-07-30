import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import { AxiosError, type AxiosResponse } from 'axios'

import { resolveSessionVerification } from './session-verification'

function axiosErrorWithStatus(status?: number): AxiosError {
  const error = new AxiosError('session verification failed')
  if (status !== undefined) {
    error.response = {
      status,
      statusText: String(status),
      headers: {},
      config: error.config ?? { headers: {} },
      data: null,
    } as AxiosResponse
  }
  return error
}

describe('authenticated session verification policy', () => {
  test('accepts a successful self response and returns the refreshed user', () => {
    const user = { id: 7, username: 'verified-user', role: 1 }

    assert.deepEqual(
      resolveSessionVerification({ success: true, data: user }),
      { status: 'verified', user }
    )
  })

  test('retries unexpected successful HTTP payloads without expiring the session', () => {
    assert.deepEqual(resolveSessionVerification(), { status: 'retry' })
    assert.deepEqual(resolveSessionVerification({}), { status: 'retry' })
    assert.deepEqual(
      resolveSessionVerification({ success: true, data: null }),
      { status: 'retry' }
    )
  })

  test('expires the session for an HTTP 200 response that rejects authentication', () => {
    assert.deepEqual(resolveSessionVerification({ success: false }), {
      status: 'expired',
    })
  })

  test('expires the session only for an HTTP 401 verification error', () => {
    assert.deepEqual(
      resolveSessionVerification(undefined, axiosErrorWithStatus(401)),
      { status: 'expired' }
    )
    assert.deepEqual(
      resolveSessionVerification(undefined, axiosErrorWithStatus(500)),
      { status: 'retry' }
    )
    assert.deepEqual(
      resolveSessionVerification(undefined, axiosErrorWithStatus()),
      { status: 'retry' }
    )
    assert.deepEqual(
      resolveSessionVerification(undefined, new Error('network unavailable')),
      { status: 'retry' }
    )
  })
})
