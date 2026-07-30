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
  getCacheReadTokenCount,
  getReasoningTokenCount,
} from './usage-token-details'

describe('usage token details', () => {
  test('prefers nested AI SDK usage details while retaining legacy fallback', () => {
    const usage = {
      reasoningTokens: 3,
      cachedInputTokens: 5,
      outputTokenDetails: { reasoningTokens: 7 },
      inputTokenDetails: { cacheReadTokens: 11 },
    }

    assert.equal(getReasoningTokenCount(usage), 7)
    assert.equal(getCacheReadTokenCount(usage), 11)
    assert.equal(getReasoningTokenCount({ reasoningTokens: 3 }), 3)
    assert.equal(getCacheReadTokenCount({ cachedInputTokens: 5 }), 5)
  })

  test('preserves explicit zero and defaults absent usage to zero', () => {
    assert.equal(
      getReasoningTokenCount({
        reasoningTokens: 9,
        outputTokenDetails: { reasoningTokens: 0 },
      }),
      0
    )
    assert.equal(getCacheReadTokenCount(undefined), 0)
  })
})
