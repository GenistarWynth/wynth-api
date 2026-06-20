import assert from 'node:assert/strict'
import { describe, test } from 'node:test'
import {
  buildLocalGroupRuleTemplate,
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

  test('builds local group rule templates with inherited scheduling defaults', () => {
    assert.deepEqual(
      buildLocalGroupRuleTemplate('openai-pro', {
        defaultLocalGroup: 'OpenAI',
        proLocalGroup: 'OpenAI-Pro',
        monitor: { enabled: true, interval_minutes: 10 },
        autoSync: { enabled: true, interval_minutes: 0 },
        modelStrategy: 'fixed',
        fixedModels: ['gpt-5', 'gpt-4o'],
      }),
      {
        name: 'OpenAI Pro',
        local_group: 'OpenAI-Pro',
        platforms: ['openai'],
        name_contains: ['pro'],
        description_contains: ['pro'],
        exclude_keywords: [],
        monitor: { enabled: true, interval_minutes: 10 },
        auto_sync: { enabled: true, interval_minutes: 0 },
        model_strategy: 'fixed',
        fixed_models: ['gpt-5', 'gpt-4o'],
      }
    )
  })
})
