import assert from 'node:assert/strict'
import { describe, test } from 'node:test'
import {
  buildLocalGroupRuleTemplate,
  createLocalGroupRuleUserTemplate,
  DEFAULT_LOCAL_GROUP_RULE_STRATEGY_DEFAULTS,
  hasLocalGroupRuleMatcher,
  normalizeKeywordList,
  normalizeModelList,
  parseLocalGroupRuleUserTemplates,
  resolveLocalGroupRuleStrategy,
  normalizeSyncRules,
  serializeLocalGroupRuleUserTemplates,
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

  test('preserves explicit follow image bridge policy in sync rules', () => {
    assert.deepEqual(
      normalizeSyncRules([
        {
          name: 'OpenAI',
          local_group: 'default',
          platforms: ['openai'],
          name_contains: [],
          description_contains: [],
          exclude_keywords: [],
          codex_image_generation_bridge_policy: 'follow',
          model_strategy: 'all_upstream',
          fixed_models: [],
        },
      ]),
      [
        {
          name: 'OpenAI',
          local_group: 'default',
          platforms: ['openai'],
          name_contains: [],
          description_contains: [],
          exclude_keywords: [],
          codex_image_generation_bridge_policy: 'follow',
          model_strategy: 'all_upstream',
          fixed_models: [],
        },
      ]
    )
  })

  test('detects rules that can be matched and saved as templates', () => {
    assert.equal(
      hasLocalGroupRuleMatcher({
        platforms: [],
        name_contains: [],
        description_contains: [],
      }),
      false
    )
    assert.equal(
      hasLocalGroupRuleMatcher({
        platforms: ['OpenAI'],
        name_contains: [],
        description_contains: [],
      }),
      true
    )
    assert.equal(
      hasLocalGroupRuleMatcher({
        platforms: [],
        name_contains: [' pro '],
        description_contains: [],
      }),
      true
    )
  })

  test('builds local group rule templates with inherited scheduling defaults', () => {
    assert.deepEqual(
      buildLocalGroupRuleTemplate('openai-pro', {
        defaultLocalGroup: 'OpenAI',
        proLocalGroup: 'OpenAI-Pro',
        monitor: { enabled: true, interval_minutes: 10 },
        autoSync: { enabled: true, interval_minutes: 0 },
        autoPriority: {
          enabled: true,
          interval_minutes: 0,
          window_hours: 48,
        },
        codexImageGenerationBridgePolicy: 'disabled',
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
        auto_priority: {
          enabled: true,
          interval_minutes: 0,
          window_hours: 48,
        },
        codex_image_generation_bridge_policy: 'disabled',
        model_strategy: 'fixed',
        fixed_models: ['gpt-5', 'gpt-4o'],
      }
    )
  })

  test('serializes user templates with normalized rule snapshots', () => {
    const template = createLocalGroupRuleUserTemplate(
      ' Cheap GPT ',
      {
        name: ' Cheap GPT ',
        local_group: ' OpenAI ',
        platforms: ['OpenAI', 'openai'],
        name_contains: [' GPT ', 'gpt'],
        description_contains: [],
        exclude_keywords: [' pro '],
        monitor: { enabled: true, interval_minutes: 10 },
        auto_sync: { enabled: true, interval_minutes: 0 },
        auto_priority: {
          enabled: false,
          interval_minutes: 15,
          window_hours: 24,
        },
        codex_image_generation_bridge_policy: 'enabled',
        model_strategy: 'fixed',
        fixed_models: [' gpt-4o ', 'gpt-5', 'gpt-4o'],
      },
      1234
    )

    assert.deepEqual(template, {
      id: 'cheap-gpt',
      name: 'Cheap GPT',
      created_at: 1234,
      rule: {
        name: 'Cheap GPT',
        local_group: 'OpenAI',
        platforms: ['openai'],
        name_contains: ['gpt'],
        description_contains: [],
        exclude_keywords: ['pro'],
        monitor: { enabled: true, interval_minutes: 10 },
        auto_sync: { enabled: true, interval_minutes: 0 },
        auto_priority: {
          enabled: false,
          interval_minutes: 15,
          window_hours: 24,
        },
        codex_image_generation_bridge_policy: 'enabled',
        model_strategy: 'fixed',
        fixed_models: ['gpt-4o', 'gpt-5'],
      },
    })

    assert.deepEqual(
      parseLocalGroupRuleUserTemplates(
        serializeLocalGroupRuleUserTemplates([template])
      ),
      [template]
    )
  })

  test('skips invalid user templates from storage', () => {
    assert.deepEqual(
      parseLocalGroupRuleUserTemplates(
        JSON.stringify([
          {
            id: 'valid',
            name: 'Valid',
            created_at: 10,
            rule: {
              name: 'Valid',
              local_group: 'OpenAI',
              platforms: ['openai'],
              name_contains: [],
              description_contains: [],
              exclude_keywords: [],
              model_strategy: 'all_upstream',
              fixed_models: [],
            },
          },
          {
            id: '',
            name: '',
            created_at: 'bad',
            rule: null,
          },
        ])
      ),
      [
        {
          id: 'valid',
          name: 'Valid',
          created_at: 10,
          rule: {
            name: 'Valid',
            local_group: 'OpenAI',
            platforms: ['openai'],
            name_contains: [],
            description_contains: [],
            exclude_keywords: [],
            model_strategy: 'all_upstream',
            fixed_models: [],
          },
        },
      ]
    )

    assert.deepEqual(parseLocalGroupRuleUserTemplates('{bad json'), [])
  })

  test('resolves rule strategy inheritance and custom overrides for display', () => {
    const inherited = resolveLocalGroupRuleStrategy(
      {
        name: 'OpenAI',
        local_group: 'OpenAI',
        platforms: ['openai'],
        name_contains: [],
        description_contains: [],
        exclude_keywords: [],
        model_strategy: 'all_upstream',
        fixed_models: [],
      },
      DEFAULT_LOCAL_GROUP_RULE_STRATEGY_DEFAULTS
    )

    assert.equal(inherited.has_overrides, false)
    assert.deepEqual(inherited.override_keys, [])
    assert.equal(inherited.monitor.origin, 'inherit')
    assert.equal(inherited.monitor.enabled, false)
    assert.equal(inherited.auto_sync.interval_minutes, 30)

    const customized = resolveLocalGroupRuleStrategy(
      {
        name: 'OpenAI Pro',
        local_group: 'OpenAI-Pro',
        platforms: ['openai'],
        name_contains: ['pro'],
        description_contains: [],
        exclude_keywords: [],
        auto_sync: { enabled: false, interval_minutes: 5 },
        auto_priority: {
          enabled: true,
          interval_minutes: 15,
          window_hours: 48,
        },
        codex_image_generation_bridge_policy: 'disabled',
        model_strategy: 'fixed',
        fixed_models: ['gpt-5', 'gpt-4o'],
      },
      DEFAULT_LOCAL_GROUP_RULE_STRATEGY_DEFAULTS
    )

    assert.equal(customized.has_overrides, true)
    assert.deepEqual(customized.override_keys, [
      'auto_sync',
      'auto_priority',
      'codex_image_generation_bridge',
      'model_strategy',
    ])
    assert.equal(customized.auto_sync.origin, 'override')
    assert.equal(customized.auto_sync.enabled, false)
    assert.equal(customized.auto_priority.window_hours, 48)
    assert.equal(
      customized.codex_image_generation_bridge_policy.value,
      'disabled'
    )
    assert.deepEqual(customized.model.fixed_models, ['gpt-5', 'gpt-4o'])
  })
})
