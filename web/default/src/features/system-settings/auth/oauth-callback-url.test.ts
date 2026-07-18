import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import {
  buildOAuthCallbackUrl,
  resolveOAuthSiteUrl,
} from './oauth-callback-url'

describe('OAuth callback URL helpers', () => {
  test('normalizes configured site addresses and falls back for blank values', () => {
    const cases = [
      {
        name: 'trims surrounding whitespace',
        serverAddress: '  https://auth.example.com  ',
        fallback: 'Site URL',
        expected: 'https://auth.example.com',
      },
      {
        name: 'removes every trailing slash',
        serverAddress: 'https://auth.example.com///',
        fallback: 'Site URL',
        expected: 'https://auth.example.com',
      },
      {
        name: 'uses the localized fallback for whitespace-only input',
        serverAddress: '   ',
        fallback: 'Site URL',
        expected: 'Site URL',
      },
    ]

    for (const testCase of cases) {
      assert.equal(
        resolveOAuthSiteUrl(testCase.serverAddress, testCase.fallback),
        testCase.expected,
        testCase.name
      )
    }
  })

  test('builds provider and custom-slug callbacks from normalized paths', () => {
    const cases = [
      {
        serverAddress: 'https://auth.example.com///',
        callbackPath: 'github',
        expected: 'https://auth.example.com/oauth/github',
      },
      {
        serverAddress: 'https://auth.example.com/',
        callbackPath: '///oidc',
        expected: 'https://auth.example.com/oauth/oidc',
      },
      {
        serverAddress: 'https://auth.example.com',
        callbackPath: '{slug}',
        expected: 'https://auth.example.com/oauth/{slug}',
      },
    ]

    for (const testCase of cases) {
      assert.equal(
        buildOAuthCallbackUrl(
          testCase.serverAddress,
          testCase.callbackPath,
          'Site URL'
        ),
        testCase.expected
      )
    }
  })
})
