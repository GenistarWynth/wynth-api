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

import type { PricingModel } from '../types'
import { getDynamicDisplayGroupRatio } from './dynamic-price'
import {
  formatFixedPrice,
  formatGroupPrice,
  formatPrice,
  formatRequestPrice,
} from './price'

function createPricingModel(
  overrides: Partial<PricingModel> = {}
): PricingModel {
  return {
    id: 1,
    model_name: 'group-priced-model',
    quota_type: 0,
    model_ratio: 1,
    completion_ratio: 2,
    enable_groups: ['standard', 'pro', 'zero'],
    group_ratio: {
      standard: 0.5,
      pro: 2,
      zero: 0,
    },
    ...overrides,
  }
}

describe('pricing group selection', () => {
  test('uses the selected supported group and otherwise keeps minimum-price fallback', () => {
    const model = createPricingModel()

    assert.equal(getDynamicDisplayGroupRatio(model, 'pro'), 2)
    assert.equal(getDynamicDisplayGroupRatio(model, 'zero'), 0)
    assert.equal(getDynamicDisplayGroupRatio(model, 'missing'), 0)
    assert.equal(getDynamicDisplayGroupRatio(model, 'all'), 0)
    assert.equal(getDynamicDisplayGroupRatio(model), 0)
  })

  test('applies the selected group to token and request summary prices', () => {
    const tokenModel = createPricingModel()
    const requestModel = createPricingModel({
      quota_type: 1,
      model_price: 3,
    })

    assert.equal(
      formatPrice(tokenModel, 'input', 'M', false, 1, 1, 'pro'),
      formatGroupPrice(
        tokenModel,
        'pro',
        'input',
        'M',
        false,
        1,
        1,
        tokenModel.group_ratio || {}
      )
    )
    assert.equal(
      formatRequestPrice(requestModel, false, 1, 1, 'pro'),
      formatFixedPrice(
        requestModel,
        'pro',
        false,
        1,
        1,
        requestModel.group_ratio || {}
      )
    )
  })

  test('preserves explicitly configured zero ratios for group-specific prices', () => {
    const tokenModel = createPricingModel()
    const requestModel = createPricingModel({
      quota_type: 1,
      model_price: 3,
    })

    assert.notEqual(
      formatGroupPrice(
        tokenModel,
        'zero',
        'input',
        'M',
        false,
        1,
        1,
        tokenModel.group_ratio || {}
      ),
      formatGroupPrice(
        tokenModel,
        'missing',
        'input',
        'M',
        false,
        1,
        1,
        tokenModel.group_ratio || {}
      )
    )
    assert.notEqual(
      formatFixedPrice(
        requestModel,
        'zero',
        false,
        1,
        1,
        requestModel.group_ratio || {}
      ),
      formatFixedPrice(
        requestModel,
        'missing',
        false,
        1,
        1,
        requestModel.group_ratio || {}
      )
    )
  })
})
