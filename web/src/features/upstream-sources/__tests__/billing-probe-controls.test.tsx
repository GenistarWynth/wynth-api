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
import { before, describe, test } from 'node:test'

import { createInstance } from 'i18next'
import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { I18nextProvider, initReactI18next } from 'react-i18next'

import { BillingProbeControls } from '../billing-probe-controls'
import { empiricalBillingProbeStatusLabel } from '../billing-probe-status'
import type { UpstreamSourceBillingProbe } from '../types'

const i18n = createInstance()

before(async () => {
  await i18n.use(initReactI18next).init({
    lng: 'en',
    fallbackLng: 'en',
    resources: { en: { translation: {} } },
    interpolation: { escapeValue: false },
  })
})

function createProbe(
  overrides: Partial<UpstreamSourceBillingProbe> = {}
): UpstreamSourceBillingProbe {
  return {
    source_id: 7,
    mapping_id: 11,
    enabled: false,
    interval_minutes: 30,
    status: 'idle',
    unsupported: false,
    auto_priority_cost_source: 'advertised',
    empirical_status: 'missing',
    advertised_effective_rate_multiplier: 0.42,
    has_last_good: false,
    fresh: false,
    stale: false,
    ...overrides,
  }
}

function renderControls(probe: UpstreamSourceBillingProbe) {
  return renderToStaticMarkup(
    createElement(
      I18nextProvider,
      { i18n },
      createElement(BillingProbeControls, {
        probe,
        saving: false,
        running: false,
        onSave: () => undefined,
        onRun: () => undefined,
      })
    )
  )
}

describe('billing probe controls', () => {
  test('keeps advertised selection and probe collection as independent controls', () => {
    const markup = renderControls(createProbe())

    assert.match(markup, /Auto-priority cost source/)
    assert.match(markup, />Advertised</)
    assert.match(markup, />Empirical probe</)
    assert.match(markup, /Probe collection/)
    assert.match(markup, /Probe interval \(minutes\)/)
    assert.match(markup, /Save probe settings/)
    assert.match(markup, /Run probe/)
    assert.match(markup, /Advertised rate/)
    assert.match(markup, /0\.420x/)
    assert.match(markup, /Empirical probe rate/)
    assert.match(markup, /No empirical snapshot/)
  })

  test('shows only sanitized last-good diagnostics and distinct empirical value', () => {
    const probe = {
      ...createProbe({
        enabled: true,
        interval_minutes: 5,
        status: 'failed',
        auto_priority_cost_source: 'empirical_probe',
        empirical_status: 'temporary_failed',
        empirical_nominal_rate_multiplier: 0.8,
        group_rate_multiplier: 0.8,
        user_rate_multiplier: 1,
        resolved_rate_multiplier: 0.8,
        peak_rate_enabled: true,
        peak_start: '09:00',
        peak_end: '17:00',
        peak_rate_multiplier: 1.5,
        applied_peak_multiplier: 1,
        effective_rate_multiplier: 0.8,
        timezone: 'Asia/Tokyo',
        observed_at: '2026-08-01T12:00:00Z',
        last_attempt_at: 1_754_046_000,
        received_at: 1_754_046_000,
        fresh_until: 1_754_049_600,
        has_last_good: true,
        fresh: true,
      }),
      identity_fingerprint: 'fingerprint-secret',
      key: 'sk-secret',
      proxy_url: 'https://proxy-secret.example',
      request_query: 'tenant-secret',
      raw_body: 'raw-secret',
    } as UpstreamSourceBillingProbe

    const markup = renderControls(probe)

    assert.match(markup, /Temporary failure — using last good/)
    assert.match(markup, /Empirical probe rate/)
    assert.match(markup, /0\.800x/)
    assert.match(markup, /Last attempt/)
    assert.match(markup, /Last good received/)
    assert.match(markup, /Fresh until/)
    assert.match(markup, /Resolved rate/)
    assert.match(markup, /Peak window/)
    assert.match(markup, /09:00–17:00/)
    assert.match(markup, /Asia\/Tokyo/)
    for (const secret of [
      'fingerprint-secret',
      'sk-secret',
      'proxy-secret',
      'tenant-secret',
      'raw-secret',
    ]) {
      assert.doesNotMatch(markup, new RegExp(secret))
    }
  })

  test('maps every backend-safe empirical state to explicit copy', () => {
    assert.deepEqual(
      [
        'disabled',
        'missing',
        'ready',
        'temporary_failed',
        'stale',
        'unsupported',
        'identity_mismatch',
        'malformed',
      ].map(empiricalBillingProbeStatusLabel),
      [
        'Disabled',
        'No empirical snapshot',
        'Ready',
        'Temporary failure — using last good',
        'Stale',
        'Unsupported',
        'Identity mismatch',
        'Malformed snapshot',
      ]
    )
  })
})
