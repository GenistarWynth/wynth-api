import { describe, expect, it } from 'vitest'
import {
  deriveUnsetPriceModels,
  type ModelPricingSnapshot,
} from './model-pricing-snapshots'

const snapshot = (name: string, values: Partial<ModelPricingSnapshot> = {}) => ({
  name,
  hasConflict: false,
  ...values,
})

describe('deriveUnsetPriceModels', () => {
  it('uses only unique enabled-channel models and excludes configured base prices', () => {
    expect(
      deriveUnsetPriceModels(
        [' unset ', 'free', 'priced', 'unset'],
        [
          snapshot('disabled'),
          snapshot('free', { ratio: '0' }),
          snapshot('priced', { price: '1' }),
        ]
      ).map((row) => row.name)
    ).toEqual(['unset'])
  })

  it('treats expression pricing as configured', () => {
    expect(
      deriveUnsetPriceModels(['expr'], [
        snapshot('expr', { billingMode: 'tiered_expr', billingExpr: '1' }),
      ])
    ).toEqual([])
  })
})
