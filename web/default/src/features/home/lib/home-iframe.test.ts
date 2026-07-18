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
import { describe, test } from 'node:test'

import {
  HOME_IFRAME_SANDBOX,
  postHomeIframePreferences,
} from './home-iframe'

describe('custom home iframe integration', () => {
  test('allows only user-activated top navigation without same-origin access', () => {
    const tokens = HOME_IFRAME_SANDBOX.split(' ')

    assert.ok(tokens.includes('allow-top-navigation-by-user-activation'))
    assert.equal(tokens.includes('allow-top-navigation'), false)
    assert.equal(tokens.includes('allow-same-origin'), false)
  })

  test('posts the current theme and language to the embedded page', () => {
    const messages: Array<{ message: unknown; targetOrigin: string }> = []
    const target = {
      postMessage(message: unknown, targetOrigin: string) {
        messages.push({ message, targetOrigin })
      },
    }

    postHomeIframePreferences(target, 'dark', 'zh-CN')

    assert.deepEqual(messages, [
      { message: { themeMode: 'dark' }, targetOrigin: '*' },
      { message: { lang: 'zh-CN' }, targetOrigin: '*' },
    ])
  })

  test('tolerates a missing or navigating iframe window', () => {
    assert.doesNotThrow(() =>
      postHomeIframePreferences(null, 'light', 'en')
    )
    assert.doesNotThrow(() =>
      postHomeIframePreferences(
        {
          postMessage() {
            throw new Error('frame navigated')
          },
        },
        'light',
        'en'
      )
    )
  })
})
