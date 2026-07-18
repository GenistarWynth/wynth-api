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
import { describe, test } from 'node:test'

import type { ColumnDef } from '@tanstack/react-table'
import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'

import { useDataTable } from '../hooks/use-data-table'
import { CardRowContent } from './card-row-content'

type TestRow = {
  title: string
  unordered: string
  second: string
  first: string
}

const columns: ColumnDef<TestRow>[] = [
  {
    accessorKey: 'title',
    header: 'Title',
    meta: { mobileTitle: true },
  },
  { accessorKey: 'unordered', header: 'Unordered' },
  {
    accessorKey: 'second',
    header: 'Second',
    meta: { mobileOrder: 20 },
  },
  {
    accessorKey: 'first',
    header: 'First',
    meta: { mobileOrder: 10 },
  },
]

function renderCard(compact: boolean) {
  function Probe() {
    const { table } = useDataTable({
      data: [
        {
          title: 'title-value',
          unordered: 'unordered-value',
          second: 'second-value',
          first: 'first-value',
        },
      ],
      columns,
    })

    return (
      <CardRowContent row={table.getRowModel().rows[0]} compact={compact} />
    )
  }

  return renderToStaticMarkup(createElement(Probe))
}

function assertAppearsInOrder(markup: string, labels: string[]) {
  const positions = labels.map((label) => markup.indexOf(label))
  assert.ok(positions.every((position) => position >= 0))
  assert.deepEqual(
    positions,
    [...positions].sort((a, b) => a - b)
  )
}

describe('data table card row content', () => {
  test('orders compact fields by mobileOrder and leaves unspecified fields last', () => {
    assertAppearsInOrder(renderCard(true), ['First', 'Second', 'Unordered'])
  })

  test('applies mobileOrder to fallback cards without reordering equal priorities', () => {
    assertAppearsInOrder(renderCard(false), [
      'First',
      'Second',
      'Title',
      'Unordered',
    ])
  })
})
