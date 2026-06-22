import assert from 'node:assert/strict'
import { describe, test } from 'node:test'
import {
  CHANNEL_FORM_DEFAULT_VALUES,
  transformChannelToFormDefaults,
  transformFormDataToUpdatePayload,
} from './channel-form'
import type { Channel } from '../types'

function baseChannel(overrides: Partial<Channel> = {}): Channel {
  return {
    id: 1,
    type: 1,
    key: '',
    openai_organization: null,
    test_model: null,
    status: 1,
    name: 'Test channel',
    weight: null,
    created_time: 0,
    updated_time: 0,
    last_sync_time: 0,
    test_time: 0,
    response_time: 0,
    base_url: null,
    other: '',
    balance: 0,
    balance_updated_time: 0,
    models: 'gpt-test',
    group: 'default',
    used_quota: 0,
    model_mapping: null,
    status_code_mapping: null,
    priority: null,
    auto_ban: 1,
    other_info: '',
    tag: null,
    setting: null,
    param_override: null,
    header_override: null,
    remark: '',
    max_input_tokens: 0,
    channel_info: {
      is_multi_key: false,
      multi_key_size: 0,
      multi_key_polling_index: 0,
      multi_key_mode: 'random',
    },
    settings: '{}',
    ...overrides,
  }
}

describe('channel form auto retry settings', () => {
  test('reads channel auto retry override from settings json', () => {
    const defaults = transformChannelToFormDefaults(
      baseChannel({ settings: '{"auto_retry_times":2}' })
    )

    assert.equal(defaults.auto_retry_times, 2)
  })

  test('writes explicit zero auto retry override to settings json', () => {
    const payload = transformFormDataToUpdatePayload(
      {
        ...CHANNEL_FORM_DEFAULT_VALUES,
        name: 'Test channel',
        models: 'gpt-test',
        group: ['default'],
        auto_retry_times: 0,
      },
      1
    )

    assert.equal(JSON.parse(String(payload.settings)).auto_retry_times, 0)
  })

  test('removes auto retry override when following global setting', () => {
    const payload = transformFormDataToUpdatePayload(
      {
        ...CHANNEL_FORM_DEFAULT_VALUES,
        name: 'Test channel',
        models: 'gpt-test',
        group: ['default'],
        settings: '{"auto_retry_times":3,"custom_existing":true}',
        auto_retry_times: undefined,
      },
      1
    )

    const settings = JSON.parse(String(payload.settings))
    assert.equal(settings.auto_retry_times, undefined)
    assert.equal(settings.custom_existing, true)
  })
})
