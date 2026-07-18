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
  ISOLATED_CONTENT_SANDBOX,
  ISOLATED_SANITIZE_OPTIONS,
  cloneApplicationStyleNodes,
  hardenEmbeddedFrame,
  hardenExternalLink,
  syncIsolatedTheme,
} from './html-content-policy'

class FakeElement {
  readonly attributes = new Map<string, string>()

  constructor(attributes: Record<string, string> = {}) {
    for (const [name, value] of Object.entries(attributes)) {
      this.attributes.set(name, value)
    }
  }

  getAttribute(name: string) {
    return this.attributes.get(name) ?? null
  }

  hasAttribute(name: string) {
    return this.attributes.has(name)
  }

  removeAttribute(name: string) {
    this.attributes.delete(name)
  }

  setAttribute(name: string, value: string) {
    this.attributes.set(name, value)
  }
}

describe('isolated HTML content policy', () => {
  test('preserves custom styles and media while excluding active document primitives', () => {
    assert.ok(ISOLATED_SANITIZE_OPTIONS.ADD_TAGS?.includes('style'))
    assert.ok(ISOLATED_SANITIZE_OPTIONS.ADD_TAGS?.includes('iframe'))
    assert.ok(ISOLATED_SANITIZE_OPTIONS.ADD_TAGS?.includes('video'))
    assert.ok(ISOLATED_SANITIZE_OPTIONS.ADD_ATTR?.includes('style'))
    assert.ok(ISOLATED_SANITIZE_OPTIONS.FORBID_ATTR?.includes('srcdoc'))

    for (const tag of ['base', 'embed', 'link', 'meta', 'object', 'script']) {
      assert.ok(ISOLATED_SANITIZE_OPTIONS.FORBID_TAGS?.includes(tag))
    }

    assert.equal(ISOLATED_SANITIZE_OPTIONS.FORCE_BODY, true)
  })

  test('hardens blank-target links without discarding existing rel values', () => {
    const link = new FakeElement({ rel: 'nofollow' })

    hardenExternalLink(link)

    assert.deepEqual(link.getAttribute('rel')?.split(' ').sort(), [
      'nofollow',
      'noopener',
      'noreferrer',
    ])
  })

  test('sandboxes embedded frames and preserves an explicit loading policy', () => {
    const lazyFrame = new FakeElement({ srcdoc: '<script>bad()</script>' })
    hardenEmbeddedFrame(lazyFrame)

    assert.equal(lazyFrame.hasAttribute('srcdoc'), false)
    assert.equal(lazyFrame.getAttribute('sandbox'), ISOLATED_CONTENT_SANDBOX)
    assert.equal(lazyFrame.getAttribute('referrerpolicy'), 'no-referrer')
    assert.equal(lazyFrame.getAttribute('loading'), 'lazy')

    const eagerFrame = new FakeElement({ loading: 'eager' })
    hardenEmbeddedFrame(eagerFrame)
    assert.equal(eagerFrame.getAttribute('loading'), 'eager')
  })

  test('clones only loaded application style nodes into the isolated root', () => {
    const clones: Array<{ source: string; deep: boolean }> = []
    const nodes = ['style', 'stylesheet'].map((source) => ({
      cloneNode(deep: boolean) {
        const clone = { source, deep }
        clones.push(clone)
        return clone
      },
    }))
    let selector = ''
    const documentLike = {
      head: {
        querySelectorAll(receivedSelector: string) {
          selector = receivedSelector
          return nodes
        },
      },
    }

    const result = cloneApplicationStyleNodes(documentLike)

    assert.equal(selector, 'style, link[rel="stylesheet"]')
    assert.deepEqual(result, clones)
    assert.deepEqual(clones, [
      { source: 'style', deep: true },
      { source: 'stylesheet', deep: true },
    ])
  })

  test('mirrors the application dark class onto isolated content', () => {
    let wrapperIsDark = false
    const wrapper = {
      classList: {
        toggle(name: string, enabled: boolean) {
          assert.equal(name, 'dark')
          wrapperIsDark = enabled
        },
      },
    }

    syncIsolatedTheme(wrapper, {
      classList: { contains: (name: string) => name === 'dark' },
    })
    assert.equal(wrapperIsDark, true)

    syncIsolatedTheme(wrapper, {
      classList: { contains: () => false },
    })
    assert.equal(wrapperIsDark, false)
  })
})
