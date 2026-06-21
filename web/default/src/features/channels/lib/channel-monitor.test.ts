import assert from 'node:assert/strict'
import { describe, test } from 'node:test'
import { buildMonitorHistoryBars, monitorStatusText } from './channel-monitor'

describe('channel monitor history helpers', () => {
  test('builds fixed-width visual bars from newest detail records', () => {
    const bars = buildMonitorHistoryBars(
      [
        {
          id: 1,
          status: 'success',
          latency_ms: 1000,
          first_token_latency_ms: 500,
          endpoint_latency_ms: 20,
          prompt_tokens: 92,
          completion_tokens: 156,
          checked_at: 100,
        },
        {
          id: 2,
          status: 'degraded',
          latency_ms: 2000,
          first_token_latency_ms: 0,
          endpoint_latency_ms: 40,
          prompt_tokens: 92,
          completion_tokens: 0,
          checked_at: 200,
        },
        {
          id: 3,
          status: 'failed',
          latency_ms: 0,
          first_token_latency_ms: 0,
          endpoint_latency_ms: 50,
          prompt_tokens: 0,
          completion_tokens: 0,
          message: 'upstream timeout',
          checked_at: 300,
        },
      ],
      5
    )

    assert.equal(bars.length, 5)
    assert.equal(bars[0].status, 'empty')
    assert.equal(bars[0].tone, 'empty')
    assert.equal(bars[2].status, 'success')
    assert.equal(bars[2].tone, 'success')
    assert.equal(bars[3].status, 'degraded')
    assert.equal(bars[3].tone, 'warning')
    assert.equal(bars[4].status, 'failed')
    assert.equal(bars[4].tone, 'danger')
    assert.ok(bars[2].heightPercent >= 25)
    assert.ok(bars[3].heightPercent > bars[2].heightPercent)
    assert.equal(bars[4].message, 'upstream timeout')
  })

  test('translates monitor statuses through the provided translator', () => {
    const t = (key: string) => `t:${key}`

    assert.equal(monitorStatusText('success', t), 't:Normal')
    assert.equal(monitorStatusText('degraded', t), 't:Degraded')
    assert.equal(monitorStatusText('failed', t), 't:Failed')
    assert.equal(monitorStatusText('error', t), 't:Error')
    assert.equal(monitorStatusText('empty', t), 't:No data')
  })
})
