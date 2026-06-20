import assert from 'node:assert/strict'
import { describe, test } from 'node:test'
import {
  normalizeKeywordList,
  normalizeModelList,
  normalizeSyncRules,
} from './rules'

describe('upstream source rule normalization', () => {
  test('normalizes comma newline and chinese-comma separated keywords', () => {
    assert.deepEqual(normalizeKeywordList(' GPT,pro， Claude\nGPT '), [
      'gpt',
      'pro',
      'claude',
    ])
  })

  test('keeps platform-only rules as valid sync rules', () => {
    assert.deepEqual(
      normalizeSyncRules([
        {
          name: 'OpenAI',
          local_group: '',
          platforms: ['OpenAI'],
          name_contains: [],
          description_contains: [],
          exclude_keywords: [],
          model_strategy: 'all_upstream',
          fixed_models: [],
        },
      ]),
      [
        {
          name: 'OpenAI',
          local_group: '',
          platforms: ['openai'],
          name_contains: [],
          description_contains: [],
          exclude_keywords: [],
          model_strategy: 'all_upstream',
          fixed_models: [],
        },
      ]
    )
  })

  test('normalizes fixed model rules with de-duplicated model order', () => {
    assert.deepEqual(normalizeModelList([' gpt-4o ', 'claude', 'gpt-4o']), [
      'gpt-4o',
      'claude',
    ])
  })
})
