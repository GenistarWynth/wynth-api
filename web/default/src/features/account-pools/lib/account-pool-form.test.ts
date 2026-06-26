import assert from 'node:assert/strict'
import { describe, test } from 'node:test'
import {
  buildAccountPayload,
  buildAccountImportPayload,
  buildAccountPoolProxyOptions,
  buildBoundChannelPayload,
  buildPoolPayload,
  buildProxyPayload,
  emptyAccountForm,
  emptyAccountImportForm,
  emptyPoolForm,
  emptyProxyForm,
  maskSecretPreview,
  normalizeAccountPoolSchedulePolicy,
  normalizeOptionalAccountPoolSchedulePolicy,
  normalizeModelListText,
} from './account-pool-form'

describe('account pool form helpers', () => {
  test('builds a trimmed pool payload with OpenAI defaults', () => {
    assert.deepEqual(
      buildPoolPayload({ ...emptyPoolForm(), name: '  自建号池  ' }),
      {
        name: '自建号池',
        platform: 'openai',
        default_proxy_id: 0,
        default_monitor_enabled: false,
        default_schedule_policy: '',
        capability_check_enabled: false,
        capability_check_interval_minutes: 1440,
        capability_check_mode: 'models_endpoint',
        capability_check_channel_id: 0,
        capability_check_models: [],
        capability_check_timeout_seconds: 30,
        capability_check_merge: false,
        remark: '',
      }
    )
  })

  test('normalizes account pool schedule policy values', () => {
    assert.equal(normalizeAccountPoolSchedulePolicy('random'), 'random')
    assert.equal(normalizeAccountPoolSchedulePolicy('round_robin'), 'round_robin')
    assert.equal(normalizeAccountPoolSchedulePolicy('priority'), 'round_robin')
    assert.equal(normalizeOptionalAccountPoolSchedulePolicy(''), '')
    assert.equal(normalizeOptionalAccountPoolSchedulePolicy(' priority '), 'round_robin')
  })

  test('serializes write-only api key credential and normalizes models in first-seen order', () => {
    assert.deepEqual(
      buildAccountPayload({
        ...emptyAccountForm(),
        name: '  primary account  ',
        account_identifier: '  acct-1  ',
        api_key: '  sk-test  ',
        supported_models_text: 'gpt-5, gpt-4\ngpt-5，gpt-4o',
      }),
      {
        name: 'primary account',
        account_identifier: 'acct-1',
        credential: {
          type: 'api_key',
          api_key: 'sk-test',
          email: '',
          refresh_token: '',
        },
        token_state: {
          access_token: '',
          refresh_token: '',
          expires_at: 0,
          version: 0,
        },
        status: 'enabled',
        priority: 0,
        weight: 1,
        max_concurrency: 1,
        request_quota: 0,
        request_quota_window_seconds: 0,
        expires_at: 0,
        auto_pause_on_expired: false,
        proxy_id: 0,
        supported_models: ['gpt-5', 'gpt-4', 'gpt-4o'],
        model_mapping: {},
        last_used_at: 0,
        rate_limited_until: 0,
        temp_disabled_until: 0,
        temp_disabled_reason: '',
        last_error: '',
      }
    )
  })

  test('serializes oauth credential payload', () => {
    assert.deepEqual(
      buildAccountPayload({
        ...emptyAccountForm(),
        name: 'OAuth account',
        credential_type: 'oauth',
        email: '  user@example.com  ',
        refresh_token: '  refresh-token  ',
      }).credential,
      {
        type: 'oauth',
        api_key: '',
        email: 'user@example.com',
        refresh_token: 'refresh-token',
      }
    )
  })

  test('preserves zero max concurrency as unlimited for account payloads', () => {
    assert.equal(
      buildAccountPayload({
        ...emptyAccountForm(),
        max_concurrency: 0,
      }).max_concurrency,
      0
    )
  })

  test('clamps negative quota and expiry account fields to zero and preserves auto pause flag', () => {
    const payload = buildAccountPayload({
      ...emptyAccountForm(),
      request_quota: -5,
      request_quota_window_seconds: -3600,
      expires_at: -1,
      auto_pause_on_expired: true,
    })
    assert.equal(payload.request_quota, 0)
    assert.equal(payload.request_quota_window_seconds, 0)
    assert.equal(payload.expires_at, 0)
    assert.equal(payload.auto_pause_on_expired, true)
  })

  test('preserves explicit positive quota window and expiry account fields', () => {
    const payload = buildAccountPayload({
      ...emptyAccountForm(),
      request_quota: 1000,
      request_quota_window_seconds: 3600,
      expires_at: 1893456000,
    })
    assert.equal(payload.request_quota, 1000)
    assert.equal(payload.request_quota_window_seconds, 3600)
    assert.equal(payload.expires_at, 1893456000)
    assert.equal(payload.auto_pause_on_expired, false)
  })

  test('rejects invalid model mapping text instead of silently dropping it', () => {
    assert.throws(
      () =>
        buildAccountPayload({
          ...emptyAccountForm(),
          name: 'Mapped account',
          model_mapping_text: '{"gpt-5":',
        }),
      /Model mapping must be a JSON object with string values/
    )

    assert.throws(
      () =>
        buildAccountPayload({
          ...emptyAccountForm(),
          name: 'Mapped account',
          model_mapping_text: '{"gpt-5": 5}',
        }),
      /Model mapping must be a JSON object with string values/
    )
  })

  test('preserves valid string-to-string model mappings', () => {
    assert.deepEqual(
      buildAccountPayload({
        ...emptyAccountForm(),
        name: 'Mapped account',
        model_mapping_text: '{"gpt-5":"upstream-gpt-5"}',
      }).model_mapping,
      {
        'gpt-5': 'upstream-gpt-5',
      }
    )
  })

  test('normalizes model list text with commas, newlines, and duplicates', () => {
    assert.deepEqual(normalizeModelListText('gpt-5, gpt-4\ngpt-5'), [
      'gpt-5',
      'gpt-4',
    ])
  })

  test('builds account pool bound channel payload with fixed models in first-seen order', () => {
    assert.deepEqual(
      buildBoundChannelPayload({
        name: '  Codex pool  ',
        type: 57,
        fixed_models_text: ' gpt-5, gpt-5-codex\ngpt-5 ',
      }),
      {
        name: 'Codex pool',
        type: 57,
        model_strategy: 'fixed',
        fixed_models: ['gpt-5', 'gpt-5-codex'],
      }
    )
  })

  test('masks local secret previews', () => {
    assert.equal(maskSecretPreview(''), '')
    assert.equal(maskSecretPreview('abc'), '***')
    assert.equal(maskSecretPreview('sk-1234567890'), 'sk-1...7890')
  })

  test('builds proxy payload with trimmed host credentials and numeric defaults', () => {
    assert.deepEqual(
      buildProxyPayload({
        ...emptyProxyForm(),
        name: '  local proxy  ',
        host: '  127.0.0.1  ',
        port: 8080,
        username: '  proxy-user  ',
        password: '  proxy-secret  ',
      }),
      {
        name: 'local proxy',
        protocol: 'http',
        host: '127.0.0.1',
        port: 8080,
        username: 'proxy-user',
        password: 'proxy-secret',
        status: 'enabled',
        fallback_proxy_id: 0,
      }
    )
  })

  test('builds default proxy select options from existing proxies', () => {
    assert.deepEqual(
      buildAccountPoolProxyOptions(
        [
          { id: 12, name: '香港代理' },
          { id: 18, name: '日本代理' },
        ],
        'No Proxy'
      ),
      [
        { value: '0', label: 'No Proxy' },
        { value: '12', label: '香港代理' },
        { value: '18', label: '日本代理' },
      ]
    )
  })

  test('preserves zero default max concurrency as unlimited for import payloads', () => {
    assert.equal(
      buildAccountImportPayload({
        ...emptyAccountImportForm(),
        default_max_concurrency: 0,
      }).defaults.max_concurrency,
      0
    )
  })

  test('builds import payload with selected default proxy', () => {
    assert.deepEqual(
      buildAccountImportPayload({
        ...emptyAccountImportForm(),
        content: 'accounts: []',
        default_proxy_id: 12,
        default_supported_models_text: 'gpt-5, gpt-4\ngpt-5',
      }),
      {
        format: 'sub2api',
        content: 'accounts: []',
        dry_run: false,
        defaults: {
          status: 'enabled',
          priority: 0,
          weight: 0,
          max_concurrency: 1,
          proxy_id: 12,
          supported_models: ['gpt-5', 'gpt-4'],
          model_mapping: {},
        },
      }
    )
  })
})
