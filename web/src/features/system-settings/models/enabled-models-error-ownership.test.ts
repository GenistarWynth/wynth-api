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

import { QueryCache, QueryClient } from '@tanstack/react-query'
import { AxiosError, AxiosHeaders, type AxiosAdapter } from 'axios'
import { test, vi } from 'vitest'

import {
  handleQueryError,
  type QueryErrorNavigation,
} from '@/lib/query-error-handler'

vi.mock('sonner', () => ({
  toast: {
    error: vi.fn(),
  },
}))

test('enabled-model loading assigns business, network, and 403 locally while QueryCache owns 401 and 500', async () => {
  const { getEnabledModels } = await import('@/features/channels/api')
  const { api } = await import('@/lib/api')
  const { toast } = await import('sonner')
  const { resolveEnabledModelsError } = await import('./model-ratio-behavior')
  const toastError = toast.error as typeof toast.error & {
    mock: { calls: unknown[][] }
  }
  const originalAdapter = api.defaults.adapter
  const captureRequestError = async (adapter: AxiosAdapter) => {
    api.defaults.adapter = adapter
    let requestError: unknown
    await assert.rejects(getEnabledModels(), (error: unknown) => {
      requestError = error
      return true
    })
    assert.notEqual(requestError, undefined)
    return requestError
  }
  const httpErrorAdapter = (status: number): AxiosAdapter => {
    return async (config) => {
      assert.equal(config.skipErrorHandler, true)
      assert.equal(config.skipBusinessError, true)
      const response = {
        data: { success: false, message: `HTTP ${status}` },
        status,
        statusText: 'Failure',
        headers: new AxiosHeaders(),
        config,
      }
      throw new AxiosError(
        `Request failed with status code ${status}`,
        AxiosError.ERR_BAD_RESPONSE,
        config,
        undefined,
        response
      )
    }
  }

  try {
    api.defaults.adapter = async (config) => {
      assert.equal(config.skipErrorHandler, true)
      assert.equal(config.skipBusinessError, true)
      return {
        data: { success: false, message: 'business detail' },
        status: 200,
        statusText: 'OK',
        headers: new AxiosHeaders(),
        config,
      }
    }

    const business = await getEnabledModels()
    assert.equal(toastError.mock.calls.length, 0)
    assert.equal(
      resolveEnabledModelsError({
        enabled: true,
        isError: false,
        data: business,
        fallback: 'fallback',
      }),
      'business detail'
    )

    const networkError = await captureRequestError(async (config) => {
      assert.equal(config.skipErrorHandler, true)
      assert.equal(config.skipBusinessError, true)
      throw new AxiosError('Network Error', AxiosError.ERR_NETWORK, config)
    })
    assert.equal(
      resolveEnabledModelsError({
        enabled: true,
        isError: true,
        error: networkError,
        fallback: 'Failed to load enabled models',
      }),
      'Failed to load enabled models',
      'feature owns network feedback'
    )

    const ordinaryHttpError = await captureRequestError(httpErrorAdapter(403))
    assert.equal(
      resolveEnabledModelsError({
        enabled: true,
        isError: true,
        error: ordinaryHttpError,
        fallback: 'Failed to load enabled models',
      }),
      'Failed to load enabled models',
      'feature owns ordinary HTTP feedback'
    )

    for (const status of [401, 500]) {
      const requestError = await captureRequestError(httpErrorAdapter(status))

      assert.equal(toastError.mock.calls.length, 0)
      assert.equal(
        resolveEnabledModelsError({
          enabled: true,
          isError: true,
          error: requestError,
          fallback: 'Failed to load enabled models',
        }),
        null,
        `QueryCache owns HTTP ${status}`
      )
    }

    const feedback: string[] = []
    const navigations: QueryErrorNavigation[] = []
    let authResetCount = 0
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false } },
      queryCache: new QueryCache({
        onError: (error) => {
          handleQueryError(error, {
            translate: (key) => key,
            toastError: (message) => feedback.push(message),
            resetAuth: () => {
              authResetCount += 1
            },
            getCurrentHref: () => '/settings/models?tab=pricing',
            navigate: (options) => navigations.push(options),
          })
        },
      }),
    })

    const unauthorizedError = await captureRequestError(httpErrorAdapter(401))
    await assert.rejects(
      queryClient.fetchQuery({
        queryKey: ['enabled-models-error-owner', 401],
        queryFn: () => Promise.reject(unauthorizedError),
      }),
      (error: unknown) => error === unauthorizedError
    )
    assert.deepEqual(feedback, ['Session expired!'])
    assert.equal(authResetCount, 1)
    assert.deepEqual(navigations, [
      {
        to: '/sign-in',
        search: { redirect: '/settings/models?tab=pricing' },
      },
    ])

    feedback.length = 0
    navigations.length = 0
    authResetCount = 0

    const internalServerError = await captureRequestError(httpErrorAdapter(500))
    await assert.rejects(
      queryClient.fetchQuery({
        queryKey: ['enabled-models-error-owner', 500],
        queryFn: () => Promise.reject(internalServerError),
      }),
      (error: unknown) => error === internalServerError
    )
    assert.deepEqual(feedback, ['Internal Server Error!'])
    assert.equal(authResetCount, 0)
    assert.deepEqual(navigations, [{ to: '/500' }])

    queryClient.clear()
  } finally {
    api.defaults.adapter = originalAdapter
  }
})
