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
import type {
  Header,
  Table as TanstackTable,
} from '@tanstack/react-table'
import type { KeyboardEvent, MouseEvent } from 'react'

import { isContentSizedColumn } from './content-sized-columns'

type ColumnContentMeasure = (
  resizerElement: HTMLElement,
  columnId: string
) => number | undefined

export function createColumnResizeHandleProps<TData>(
  label: string,
  table: TanstackTable<TData>,
  header: Header<TData, unknown>,
  measure: ColumnContentMeasure = measureColumnContentWidth
) {
  const resizeHandler = header.getResizeHandler()
  const { minSize, maxSize } = header.column.columnDef

  return {
    role: 'separator' as const,
    'aria-orientation': 'vertical' as const,
    'aria-label': label,
    'aria-valuenow': Math.round(header.column.getSize()),
    'aria-valuemin': minSize,
    'aria-valuemax': maxSize,
    tabIndex: 0,
    onDoubleClick: (event: MouseEvent<HTMLDivElement>) => {
      event.preventDefault()
      autoSizeColumn(event.currentTarget, table, header, measure)
    },
    onMouseDown: resizeHandler,
    onTouchStart: resizeHandler,
    onKeyDown: (event: KeyboardEvent<HTMLDivElement>) =>
      handleColumnResizeKeyDown(event, table, header, measure),
  }
}

function handleColumnResizeKeyDown<TData>(
  event: KeyboardEvent<HTMLDivElement>,
  table: TanstackTable<TData>,
  header: Header<TData, unknown>,
  measure: ColumnContentMeasure
) {
  const step = event.shiftKey ? 50 : 10

  if (event.key === 'ArrowLeft') {
    event.preventDefault()
    resizeColumnByKeyboard(table, header, -step)
    return
  }

  if (event.key === 'ArrowRight') {
    event.preventDefault()
    resizeColumnByKeyboard(table, header, step)
    return
  }

  if (event.key === 'Enter' || event.key === ' ') {
    event.preventDefault()
    autoSizeColumn(event.currentTarget, table, header, measure)
  }
}

function resizeColumnByKeyboard<TData>(
  table: TanstackTable<TData>,
  header: Header<TData, unknown>,
  delta: number
) {
  table.setColumnSizing((previous) => ({
    ...previous,
    [header.column.id]: getClampedColumnSize(
      header,
      header.column.getSize() + delta
    ),
  }))
}

function autoSizeColumn<TData>(
  resizerElement: HTMLElement,
  table: TanstackTable<TData>,
  header: Header<TData, unknown>,
  measure: ColumnContentMeasure
) {
  const measuredSize = measure(resizerElement, header.column.id)
  if (measuredSize === undefined) return

  table.setColumnSizing((previous) => ({
    ...previous,
    [header.column.id]: getClampedColumnSize(header, measuredSize),
  }))
}

function getClampedColumnSize<TData>(
  header: Header<TData, unknown>,
  nextSize: number
) {
  const { minSize, maxSize } = header.column.columnDef
  if (typeof minSize === 'number' && nextSize < minSize) return minSize
  if (typeof maxSize === 'number' && nextSize > maxSize) return maxSize
  return nextSize
}

function measureColumnContentWidth(
  resizerElement: HTMLElement,
  columnId: string
) {
  const tableElement = resizerElement.closest('table')
  if (!tableElement) return undefined

  const cells = tableElement.querySelectorAll<HTMLElement>(
    getColumnElementSelector(columnId)
  )
  if (cells.length === 0) return undefined

  const measuredWidth = [...cells].reduce(
    (maxWidth, cell) => Math.max(maxWidth, measureElementWidth(cell)),
    0
  )
  return measuredWidth > 0 ? Math.ceil(measuredWidth) : undefined
}

function measureElementWidth(element: HTMLElement) {
  const clone = element.cloneNode(true) as HTMLElement
  clone.querySelectorAll('[data-column-resizer]').forEach((resizer) => {
    resizer.remove()
  })

  clone.style.position = 'absolute'
  clone.style.visibility = 'hidden'
  clone.style.pointerEvents = 'none'
  clone.style.left = '-10000px'
  clone.style.top = '0'
  clone.style.width = 'max-content'
  clone.style.minWidth = '0'
  clone.style.maxWidth = 'none'
  clone.style.height = 'auto'
  clone.style.whiteSpace = 'nowrap'

  document.body.append(clone)
  const width = clone.scrollWidth
  clone.remove()
  return width
}

function getColumnElementSelector(columnId: string) {
  const escapedColumnId =
    typeof CSS !== 'undefined' && typeof CSS.escape === 'function'
      ? CSS.escape(columnId)
      : columnId.replaceAll('\\', '\\\\').replaceAll('"', '\\"')
  return `[data-column-id="${escapedColumnId}"]`
}

export function shouldRenderColumnResizer<TData>(
  table: TanstackTable<TData>,
  header: Header<TData, unknown>
) {
  return (
    table.options.enableColumnResizing === true &&
    !header.isPlaceholder &&
    header.column.getCanResize() &&
    !isContentSizedColumn(header.column.id)
  )
}
