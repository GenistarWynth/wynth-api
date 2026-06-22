import assert from 'node:assert/strict'
import { describe, test } from 'node:test'
import { formatModelName } from './format'
import type { UsageLog } from '../data/schema'

function buildUsageLog(other: Record<string, unknown>): UsageLog {
  return {
    id: 1,
    user_id: 1,
    created_at: 1,
    type: 2,
    content: '',
    username: '',
    token_name: '',
    model_name: 'gpt-4o-mini',
    quota: 0,
    prompt_tokens: 0,
    completion_tokens: 0,
    use_time: 0,
    is_stream: false,
    channel: 0,
    channel_name: '',
    token_id: 0,
    group: '',
    ip: '',
    other: JSON.stringify(other),
    request_id: '',
    upstream_request_id: '',
  }
}

describe('formatModelName model audit', () => {
  test('does not include a secondary actual model when actual matches the requested baseline', () => {
    const result = formatModelName(
      buildUsageLog({
        actual_response_model: 'gpt-4o-mini',
      })
    )

    assert.equal(result.name, 'gpt-4o-mini')
    assert.equal(result.isMapped, false)
    assert.equal(result.upstreamModel, undefined)
    assert.equal(result.actualResponseModel, 'gpt-4o-mini')
    assert.equal(result.secondaryActualModel, undefined)
  })

  test('includes a secondary actual model when actual differs from the requested baseline', () => {
    const result = formatModelName(
      buildUsageLog({
        actual_response_model: 'gpt-4.1-mini',
      })
    )

    assert.equal(result.isMapped, false)
    assert.equal(result.upstreamModel, undefined)
    assert.equal(result.actualResponseModel, 'gpt-4.1-mini')
    assert.equal(result.secondaryActualModel, 'gpt-4.1-mini')
  })

  test('keeps upstream audit baseline separate from model mapping when mapping is disabled', () => {
    const result = formatModelName(
      buildUsageLog({
        is_model_mapped: false,
        upstream_model_name: 'gpt-4.1-mini',
        actual_response_model: 'gpt-4.1-mini',
      })
    )

    assert.equal(result.isMapped, false)
    assert.equal(result.actualModel, undefined)
    assert.equal(result.upstreamModel, 'gpt-4.1-mini')
    assert.equal(result.secondaryActualModel, undefined)
  })

  test('does not include a secondary actual model when actual matches the mapped upstream model', () => {
    const result = formatModelName(
      buildUsageLog({
        is_model_mapped: true,
        upstream_model_name: 'claude-3-5-sonnet',
        actual_response_model: 'claude-3-5-sonnet',
      })
    )

    assert.equal(result.isMapped, true)
    assert.equal(result.upstreamModel, 'claude-3-5-sonnet')
    assert.equal(result.actualModel, 'claude-3-5-sonnet')
    assert.equal(result.actualResponseModel, 'claude-3-5-sonnet')
    assert.equal(result.secondaryActualModel, undefined)
  })

  test('includes a secondary actual model when actual differs from the mapped upstream model', () => {
    const result = formatModelName(
      buildUsageLog({
        is_model_mapped: true,
        upstream_model_name: 'claude-3-5-sonnet',
        actual_response_model: 'claude-3-7-sonnet',
        actual_response_model_source: 'response.body.model',
      })
    )

    assert.equal(result.isMapped, true)
    assert.equal(result.upstreamModel, 'claude-3-5-sonnet')
    assert.equal(result.actualResponseModel, 'claude-3-7-sonnet')
    assert.equal(result.actualResponseModelSource, 'response.body.model')
    assert.equal(result.secondaryActualModel, 'claude-3-7-sonnet')
  })
})
