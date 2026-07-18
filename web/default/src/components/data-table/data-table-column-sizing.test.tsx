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

import type { ColumnDef, ColumnSizingState, Header, Table } from '@tanstack/react-table'
import { createElement, type ReactElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'

import {
  createColumnResizeHandleProps,
  shouldRenderColumnResizer,
} from './core/column-resizing'
import { getTableSizeStyle } from './core/table-sizing'
import {
  COLUMN_SIZING_PERSIST_DELAY_MS,
  createColumnSizingPersistenceController,
  parseColumnSizing,
  readColumnSizing,
  resolveColumnSizingHydration,
} from './hooks/column-sizing'
import { useDataTable } from './hooks/use-data-table'

type FakeColumn = {
  id: string
  columnDef: ColumnDef<unknown, unknown>
  getSize: () => number
  getCanResize: () => boolean
}

function createFakeTable(columnSizing: ColumnSizingState = {}) {
  let sizing = columnSizing
  const table = {
    getState: () => ({ columnSizing: sizing }),
    setColumnSizing(updater: ColumnSizingState | ((old: ColumnSizingState) => ColumnSizingState)) {
      sizing = typeof updater === 'function' ? updater(sizing) : updater
    },
  }

  return {
    table: table as unknown as Table<unknown>,
    getSizing: () => sizing,
  }
}

function createFakeHeader(
  table: Table<unknown>,
  options: { minSize?: number; maxSize?: number } = {}
) {
  const resizeEvents: unknown[] = []
  const column: FakeColumn = {
    id: 'name',
    columnDef: { id: 'name', ...options },
    getSize: () => table.getState().columnSizing.name ?? 200,
    getCanResize: () => true,
  }
  const header = {
    column,
    getResizeHandler: () => (event: unknown) => resizeEvents.push(event),
  }

  return {
    header: header as unknown as Header<unknown, unknown>,
    resizeEvents,
  }
}

describe('data table column sizing', () => {
  test('keeps initial sizing for uncontrolled state and prefers controlled sizing', () => {
    let captured: ColumnSizingState | undefined
    function Probe(props: { controlled?: ColumnSizingState }): ReactElement {
      const { table } = useDataTable({
        data: [{ name: 'alpha' }],
        columns: [{ accessorKey: 'name', size: 160 }],
        initialColumnSizing: { name: 120 },
        columnSizing: props.controlled,
      })
      captured = table.getState().columnSizing
      return createElement('div')
    }

    renderToStaticMarkup(createElement(Probe, {}))
    assert.deepEqual(captured, { name: 120 })

    renderToStaticMarkup(
      createElement(Probe, { controlled: { name: 260 } })
    )
    assert.deepEqual(captured, { name: 260 })
  })

  test('validates persisted sizing values and clamps configured bounds', () => {
    const columns: ColumnDef<unknown, unknown>[] = [
      { accessorKey: 'name', minSize: 100, maxSize: 300 },
      { accessorKey: 'unbounded' },
      {
        id: 'details',
        columns: [{ accessorKey: 'meta.value', minSize: 40, maxSize: 180 }],
      },
    ]

    assert.deepEqual(
      parseColumnSizing(
        JSON.stringify({
          name: 20,
          unbounded: 175,
          meta_value: 220,
          invalid: '200',
          stale: 160,
          zero: 0,
          negative: -4,
          nan: null,
        }),
        columns
      ),
      { name: 100, unbounded: 175, meta_value: 180 }
    )
    assert.deepEqual(parseColumnSizing('[]', columns), {})
    assert.deepEqual(parseColumnSizing('{"name":"bad"}', columns), {})
  })

  test('coalesces persistence writes behind a 250ms debounce', () => {
    const scheduled: Array<{
      callback: () => void
      delay: number
      handle: number
    }> = []
    const cancelled: number[] = []
    const writes: Array<{ key: string; value: ColumnSizingState }> = []
    let nextHandle = 1
    const persistence = createColumnSizingPersistenceController({
      schedule: (callback, delay) => {
        const handle = nextHandle++
        scheduled.push({ callback, delay, handle })
        return handle
      },
      cancel: (handle) => cancelled.push(handle as number),
      write: (key, value) => writes.push({ key, value }),
    })

    persistence.schedule('channels:column-sizing', { name: 120 })
    persistence.schedule('channels:column-sizing', { name: 180 })

    assert.deepEqual(cancelled, [1])
    assert.equal(scheduled[1].delay, COLUMN_SIZING_PERSIST_DELAY_MS)
    scheduled[1].callback()
    assert.deepEqual(writes, [
      { key: 'channels:column-sizing', value: { name: 180 } },
    ])

    persistence.cancel()
    assert.deepEqual(cancelled, [1])
  })

  test('hydrates a new storage key only for uncontrolled sizing', () => {
    const columns: ColumnDef<unknown, unknown>[] = [
      { accessorKey: 'name', minSize: 100, maxSize: 300 },
    ]
    const storage = {
      getItem(key: string) {
        return key === 'table-a' ? '{"name":140}' : '{"name":260}'
      },
    }

    assert.deepEqual(
      resolveColumnSizingHydration(
        undefined,
        'table-a',
        'table-b',
        readColumnSizing('table-b', columns, storage)
      ),
      { storageKey: 'table-b', value: { name: 260 } }
    )
    assert.equal(
      resolveColumnSizingHydration(
        { name: 180 },
        'table-a',
        'table-b',
        { name: 260 }
      ),
      undefined
    )
  })

  test('exposes accessible mouse, touch, keyboard, and double-click resize behavior', () => {
    const { table, getSizing } = createFakeTable()
    const { header, resizeEvents } = createFakeHeader(table, {
      minSize: 100,
      maxSize: 300,
    })
    const prevented: string[] = []
    const props = createColumnResizeHandleProps(
      'Resize column',
      table,
      header,
      () => 420
    )

    assert.equal(props.role, 'separator')
    assert.equal(props['aria-orientation'], 'vertical')
    assert.equal(props['aria-label'], 'Resize column')
    assert.equal(props.tabIndex, 0)

    props.onMouseDown('mouse' as never)
    props.onTouchStart('touch' as never)
    assert.deepEqual(resizeEvents, ['mouse', 'touch'])

    props.onKeyDown({
      key: 'ArrowRight',
      shiftKey: false,
      preventDefault: () => prevented.push('right'),
    } as never)
    assert.deepEqual(getSizing(), { name: 210 })

    props.onKeyDown({
      key: 'ArrowLeft',
      shiftKey: true,
      preventDefault: () => prevented.push('left'),
    } as never)
    assert.deepEqual(getSizing(), { name: 160 })

    props.onDoubleClick({ preventDefault: () => prevented.push('double') } as never)
    assert.deepEqual(getSizing(), { name: 300 })

    props.onKeyDown({
      key: 'Enter',
      shiftKey: false,
      preventDefault: () => prevented.push('enter'),
    } as never)
    assert.deepEqual(getSizing(), { name: 300 })

    props.onKeyDown({
      key: ' ',
      shiftKey: false,
      preventDefault: () => prevented.push('space'),
    } as never)
    assert.deepEqual(getSizing(), { name: 300 })
    assert.deepEqual(prevented, ['right', 'left', 'double', 'enter', 'space'])
  })

  test('hides resize handles for disabled tables and content-sized columns', () => {
    const createHeader = (id: string) =>
      ({
        isPlaceholder: false,
        column: {
          id,
          getCanResize: () => true,
        },
      }) as unknown as Header<unknown, unknown>
    const enabledTable = {
      options: { enableColumnResizing: true },
    } as unknown as Table<unknown>
    const disabledTable = {
      options: { enableColumnResizing: false },
    } as unknown as Table<unknown>

    assert.equal(
      shouldRenderColumnResizer(enabledTable, createHeader('name')),
      true
    )
    assert.equal(
      shouldRenderColumnResizer(enabledTable, createHeader('actions')),
      false
    )
    assert.equal(
      shouldRenderColumnResizer(disabledTable, createHeader('name')),
      false
    )
  })

  test('uses a full-width auto-layout table with a pixel minimum', () => {
    const table = {
      getVisibleLeafColumns: () => [
        { id: 'name', getSize: () => 220 },
        { id: 'actions', getSize: () => 96 },
      ],
    } as unknown as Table<unknown>

    assert.deepEqual(getTableSizeStyle(table), {
      minWidth: 'max(100%, 220px)',
      tableLayout: 'auto',
      width: '100%',
    })
  })
})
