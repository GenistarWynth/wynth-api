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
import { readFileSync } from 'node:fs'
import { before, describe, test } from 'node:test'
import { fileURLToPath } from 'node:url'

import type { ColumnDef } from '@tanstack/react-table'
import { createInstance } from 'i18next'
import { createElement, type ReactElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { I18nextProvider, initReactI18next } from 'react-i18next'

import type { UsageLog } from '../../data/schema'
import { useCommonLogsColumns } from './common-logs-columns'

const i18n = createInstance()
const mobileCardSource = readFileSync(
  fileURLToPath(new URL('../usage-logs-mobile-card.tsx', import.meta.url)),
  'utf8'
)

before(async () => {
  await i18n.use(initReactI18next).init({
    lng: 'en',
    fallbackLng: 'en',
    resources: { en: { translation: {} } },
    interpolation: { escapeValue: false },
  })
})

function getAccessorKey(column: ColumnDef<UsageLog>): string | undefined {
  if ('accessorKey' in column && typeof column.accessorKey === 'string') {
    return column.accessorKey
  }
  return undefined
}

function renderTimingColumns(log: UsageLog): string {
  function Probe(): ReactElement {
    const columns = useCommonLogsColumns(false)
    const timingColumns = columns.filter((column) =>
      ['is_stream', 'use_time'].includes(getAccessorKey(column) ?? '')
    )

    const content = timingColumns.map((column) => {
      if (typeof column.cell !== 'function') return null
      const accessorKey = getAccessorKey(column)
      return createElement(
        'div',
        { key: accessorKey, 'data-column': accessorKey },
        column.cell({
          row: {
            original: log,
            getValue: (key: string) => log[key as keyof UsageLog],
          },
        } as never)
      )
    })

    return createElement('div', null, content)
  }

  return renderToStaticMarkup(
    createElement(I18nextProvider, { i18n }, createElement(Probe))
  )
}

function makeLog(overrides: Partial<UsageLog> = {}): UsageLog {
  return {
    id: 1,
    user_id: 2,
    created_at: 1,
    type: 2,
    content: '',
    username: 'tester',
    token_name: 'key',
    model_name: 'gpt-test',
    quota: 0,
    prompt_tokens: 100,
    completion_tokens: 120,
    use_time: 6,
    is_stream: true,
    channel: 3,
    channel_name: 'channel',
    token_id: 4,
    group: 'default',
    ip: '',
    other: JSON.stringify({ frt: 1250, stream_status: { status: 'ok' } }),
    request_id: '',
    upstream_request_id: '',
    ...overrides,
  }
}

describe('usage log timing columns', () => {
  test('separates stream throughput from timing metrics in a stable order', () => {
    function Probe(): ReactElement {
      const keys = useCommonLogsColumns(false)
        .map(getAccessorKey)
        .filter((key): key is string => key != null)
      return createElement('span', null, keys.join(','))
    }

    const markup = renderToStaticMarkup(
      createElement(I18nextProvider, { i18n }, createElement(Probe))
    )
    const keys = markup.replaceAll(/^<span>|<\/span>$/g, '').split(',')

    assert.ok(keys.indexOf('model_name') < keys.indexOf('is_stream'))
    assert.ok(keys.indexOf('is_stream') < keys.indexOf('prompt_tokens'))
    assert.ok(keys.indexOf('prompt_tokens') < keys.indexOf('use_time'))
  })

  test('labels stream first-token latency, total duration, and throughput', () => {
    const markup = renderTimingColumns(makeLog())

    assert.match(markup, /data-column="is_stream"/)
    assert.match(markup, />Stream</)
    assert.match(markup, />20 t\/s</)
    assert.match(markup, />First token</)
    assert.match(markup, />1\.3s</)
    assert.match(markup, />Duration</)
    assert.match(markup, />6\.0s</)
  })

  test('omits first-token content for non-stream logs without hiding total duration', () => {
    const markup = renderTimingColumns(
      makeLog({ is_stream: false, other: JSON.stringify({}) })
    )

    assert.match(markup, />Non-stream</)
    assert.doesNotMatch(markup, />First token</)
    assert.match(markup, />Duration</)
    assert.match(markup, />6\.0s</)
  })

  test('keeps all stream and timing rows visible on mobile cards', () => {
    assert.match(
      mobileCardSource,
      /label=\{t\('Stream'\)\}\s+cell=\{cells\.get\('is_stream'\)\}\s+\/>/
    )
    assert.match(
      mobileCardSource,
      /label=\{t\('Timing'\)\}\s+cell=\{cells\.get\('use_time'\)\}\s+\/>/
    )
  })
})
