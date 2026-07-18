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
  MODELS_DEV_PRESET_ID,
  MODELS_DEV_PRESET_NAME,
  OFFICIAL_CHANNEL_ID,
  OFFICIAL_CHANNEL_NAME,
} from './constants'
import {
  applyResolutionRemovalPlan,
  applyResolutionSelections,
  getAlignedRatioTypes,
  getEffectiveResolutionSelections,
  getUpstreamDisplayName,
  isSelectedResolutionValue,
  type RatioDifferenceEntry,
  type ResolutionRemovalPlan,
  type ResolutionSelection,
} from './upstream-ratio-sync-helpers'

function difference(
  upstreams: RatioDifferenceEntry['upstreams']
): RatioDifferenceEntry {
  return { current: null, upstreams, confidence: {} }
}

describe('upstream ratio sync helpers', () => {
  test('aligns visible fields after tiered billing preference is applied', () => {
    const ratioTypes = {
      model_ratio: difference({ source: 1.2 }),
      completion_ratio: difference({ source: 2 }),
      billing_mode: difference({ source: 'tiered_expr' }),
      billing_expr: difference({ source: 'input_tokens * 2' }),
    }

    assert.deepEqual(getAlignedRatioTypes(ratioTypes, ['source']), [
      'billing_expr',
    ])
  })

  test('applies a bulk selection atomically and pairs tiered fields', () => {
    const differences = {
      'model-a': {
        model_ratio: difference({ source: 1.2 }),
        completion_ratio: difference({ source: 2 }),
      },
      'model-b': {
        model_ratio: difference({ source: 1.4 }),
        billing_mode: difference({ source: 'tiered_expr' }),
        billing_expr: difference({ source: 'input_tokens * 3' }),
      },
    }
    const selections: ResolutionSelection[] = [
      {
        model: 'model-a',
        ratioType: 'model_ratio',
        value: 1.2,
        sourceName: 'source',
      },
      {
        model: 'model-a',
        ratioType: 'completion_ratio',
        value: 2,
        sourceName: 'source',
      },
      {
        model: 'model-b',
        ratioType: 'model_ratio',
        value: 1.4,
        sourceName: 'source',
      },
    ]

    assert.deepEqual(applyResolutionSelections({}, differences, selections), {
      'model-a': { model_ratio: 1.2, completion_ratio: 2 },
      'model-b': {
        billing_mode: 'tiered_expr',
        billing_expr: 'input_tokens * 3',
      },
    })
    assert.deepEqual(
      getEffectiveResolutionSelections(differences, selections),
      [
        selections[0],
        selections[1],
        {
          ...selections[2],
          ratioType: 'billing_expr',
          value: 'input_tokens * 3',
        },
      ]
    )
  })

  test('removes paired tiered fields in one removal plan', () => {
    const plan: ResolutionRemovalPlan = new Map([
      ['model-a', new Set(['billing_expr'])],
    ])

    assert.deepEqual(
      applyResolutionRemovalPlan(
        {
          'model-a': {
            billing_mode: 'tiered_expr',
            billing_expr: 'input_tokens * 2',
            cache_ratio: 0.5,
          },
        },
        plan
      ),
      { 'model-a': { cache_ratio: 0.5 } }
    )
  })

  test('normalizes numeric selections and strips synthetic preset IDs', () => {
    assert.equal(
      isSelectedResolutionValue(
        { 'model-a': { model_ratio: '1.25' } },
        'model-a',
        'model_ratio',
        1.25
      ),
      true
    )
    assert.equal(
      getUpstreamDisplayName(`${OFFICIAL_CHANNEL_NAME}(${OFFICIAL_CHANNEL_ID})`),
      OFFICIAL_CHANNEL_NAME
    )
    assert.equal(
      getUpstreamDisplayName(
        `${MODELS_DEV_PRESET_NAME}(${MODELS_DEV_PRESET_ID})`
      ),
      MODELS_DEV_PRESET_NAME
    )
    assert.equal(getUpstreamDisplayName('Custom source(42)'), 'Custom source(42)')
  })
})
