import assert from 'node:assert/strict'
import { afterEach, describe, test } from 'node:test'

import { isRedirect } from '@tanstack/react-router'

import { useAuthStore } from '@/stores/auth-store'

import { redirectAuthenticatedUserFromSignUp } from './sign-up-guard'

afterEach(() => {
  useAuthStore.getState().auth.setUser(null)
})

describe('sign-up route guard', () => {
  test('redirects an authenticated user to the dashboard', () => {
    useAuthStore.getState().auth.setUser({
      id: 7,
      username: 'signed-in-user',
      role: 1,
    })

    assert.throws(redirectAuthenticatedUserFromSignUp, (error: unknown) => {
      if (!isRedirect(error)) return false
      return error.options.to === '/dashboard'
    })
  })

  test('allows an anonymous user to continue to sign-up', () => {
    useAuthStore.getState().auth.setUser(null)

    assert.doesNotThrow(redirectAuthenticatedUserFromSignUp)
  })
})
