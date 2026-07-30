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
  modelGroupSelectorLayoutClasses,
  scrollSelectedOptionIntoView,
} from './model-group-selector-layout'

describe('model and group selector desktop layout', () => {
  test('keeps both option columns inside a bounded shared scroll region', () => {
    assert.match(modelGroupSelectorLayoutClasses.desktopPanel, /max-h-/)
    assert.match(modelGroupSelectorLayoutClasses.desktopContent, /min-h-0/)
    assert.match(modelGroupSelectorLayoutClasses.groupScroll, /overflow-y-auto/)
    assert.match(modelGroupSelectorLayoutClasses.modelList, /min-h-0/)
  })

  test('centers a selected group inside its scroll container', () => {
    const calls: ScrollToOptions[] = []
    const selected = {
      offsetHeight: 32,
      offsetTop: 900,
      scrollIntoView: () => undefined,
    }
    const container = {
      clientHeight: 200,
      scrollTop: 0,
      scrollTo: (options: ScrollToOptions) => calls.push(options),
    }

    scrollSelectedOptionIntoView(selected, container)

    assert.deepEqual(calls, [{ top: 816, behavior: 'auto' }])
    assert.equal(container.scrollTop, 0)
  })

  test('falls back to native centering when no scroll container is available', () => {
    const calls: ScrollIntoViewOptions[] = []
    const selected = {
      scrollIntoView: (options?: ScrollIntoViewOptions) => {
        if (options) calls.push(options)
      },
    }

    scrollSelectedOptionIntoView(selected)

    assert.deepEqual(calls, [{ block: 'center', inline: 'nearest' }])
  })
})
