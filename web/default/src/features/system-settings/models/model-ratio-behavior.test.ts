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
import { describe, expect, it } from 'vitest'

import {
  areModelRatioVisualEditorPropsEqual,
  buildBatchPricingCopyTargets,
  type ModelRatioVisualEditorProps,
} from './model-ratio-behavior'

const noop = () => undefined

const baseProps: ModelRatioVisualEditorProps = {
  savedModelPrice: '{}',
  savedModelRatio: '{}',
  savedCacheRatio: '{}',
  savedCreateCacheRatio: '{}',
  savedCompletionRatio: '{}',
  savedImageRatio: '{}',
  savedAudioRatio: '{}',
  savedAudioCompletionRatio: '{}',
  savedBillingMode: '{}',
  savedBillingExpr: '{}',
  modelPrice: '{}',
  modelRatio: '{}',
  cacheRatio: '{}',
  createCacheRatio: '{}',
  completionRatio: '{}',
  imageRatio: '{}',
  audioRatio: '{}',
  audioCompletionRatio: '{}',
  billingMode: '{}',
  billingExpr: '{}',
  candidateModelNames: ['source', 'target'],
  candidateModelsLoading: false,
  filterMode: 'unset',
  onChange: noop,
  onSave: noop,
  isSaving: false,
}

describe('unset price model editor behavior', () => {
  it('persists a committed batch-copy draft to the source and unique targets', () => {
    expect(
      buildBatchPricingCopyTargets('source', [
        'target-a',
        'source',
        'target-a',
        'target-b',
      ])
    ).toEqual(['source', 'target-a', 'target-b'])
  })

  it('skips equal props and re-renders when saved pricing or candidate loading state changes', () => {
    expect(
      areModelRatioVisualEditorPropsEqual(baseProps, { ...baseProps })
    ).toBe(true)

    const savedKeys = [
      'savedModelPrice',
      'savedModelRatio',
      'savedCacheRatio',
      'savedCreateCacheRatio',
      'savedCompletionRatio',
      'savedImageRatio',
      'savedAudioRatio',
      'savedAudioCompletionRatio',
      'savedBillingMode',
      'savedBillingExpr',
    ] as const

    for (const key of savedKeys) {
      expect(
        areModelRatioVisualEditorPropsEqual(baseProps, {
          ...baseProps,
          [key]: '{"changed":1}',
        })
      ).toBe(false)
    }
    expect(
      areModelRatioVisualEditorPropsEqual(baseProps, {
        ...baseProps,
        candidateModelsLoading: true,
      })
    ).toBe(false)
  })
})
