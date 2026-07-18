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
import type { ColumnDef, ColumnSizingState } from '@tanstack/react-table'

export const COLUMN_SIZING_PERSIST_DELAY_MS = 250

type ColumnWithSizing<TData> = ColumnDef<TData, unknown> & {
  accessorKey?: string | number
  columns?: ColumnDef<TData, unknown>[]
}

export type ColumnSizingBounds = Record<
  string,
  {
    minSize?: number
    maxSize?: number
  }
>

function getColumnId<TData>(column: ColumnDef<TData, unknown>) {
  const columnWithSizing = column as ColumnWithSizing<TData>

  if (typeof columnWithSizing.id === 'string') {
    return columnWithSizing.id
  }

  if (typeof columnWithSizing.accessorKey === 'string') {
    return columnWithSizing.accessorKey.replaceAll('.', '_')
  }

  if (typeof columnWithSizing.accessorKey === 'number') {
    return String(columnWithSizing.accessorKey)
  }

  return undefined
}

export function buildColumnSizingBounds<TData>(
  columns: ColumnDef<TData, unknown>[]
): ColumnSizingBounds {
  return columns.reduce<ColumnSizingBounds>((bounds, column) => {
    const columnWithSizing = column as ColumnWithSizing<TData>
    const columnId = getColumnId(column)

    if (columnId) {
      const minSize =
        typeof columnWithSizing.minSize === 'number' &&
        Number.isFinite(columnWithSizing.minSize)
          ? columnWithSizing.minSize
          : undefined
      const maxSize =
        typeof columnWithSizing.maxSize === 'number' &&
        Number.isFinite(columnWithSizing.maxSize)
          ? columnWithSizing.maxSize
          : undefined
      bounds[columnId] = { minSize, maxSize }
    }

    if (Array.isArray(columnWithSizing.columns)) {
      Object.assign(bounds, buildColumnSizingBounds(columnWithSizing.columns))
    }

    return bounds
  }, {})
}

export function getBoundedColumnSize(
  columnId: string,
  value: unknown,
  bounds: ColumnSizingBounds
) {
  if (typeof value !== 'number' || !Number.isFinite(value) || value <= 0) {
    return undefined
  }

  const columnBounds = bounds[columnId]
  if (!columnBounds) return undefined

  let size = value
  if (columnBounds.minSize !== undefined && size < columnBounds.minSize) {
    size = columnBounds.minSize
  }
  if (columnBounds.maxSize !== undefined && size > columnBounds.maxSize) {
    size = columnBounds.maxSize
  }

  return size > 0 ? size : undefined
}

export function parseColumnSizing<TData>(
  raw: string | null | undefined,
  columns: ColumnDef<TData, unknown>[]
): ColumnSizingState {
  if (!raw) return {}

  let parsed: unknown
  try {
    parsed = JSON.parse(raw)
  } catch {
    return {}
  }

  if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
    return {}
  }

  const bounds = buildColumnSizingBounds(columns)
  return Object.entries(parsed).reduce<ColumnSizingState>(
    (sizing, [key, value]) => {
      const boundedSize = getBoundedColumnSize(key, value, bounds)
      if (boundedSize !== undefined) sizing[key] = boundedSize
      return sizing
    },
    {}
  )
}

export function readColumnSizing<TData>(
  storageKey: string | undefined,
  columns: ColumnDef<TData, unknown>[],
  storage?: { getItem: (key: string) => string | null }
): ColumnSizingState {
  if (!storageKey) return {}

  const resolvedStorage =
    storage ?? (typeof window === 'undefined' ? undefined : window.localStorage)
  if (!resolvedStorage) return {}

  try {
    return parseColumnSizing(resolvedStorage.getItem(storageKey), columns)
  } catch {
    return {}
  }
}

export function resolveColumnSizingHydration(
  controlledValue: ColumnSizingState | undefined,
  hydratedStorageKey: string | undefined,
  nextStorageKey: string | undefined,
  nextValue: ColumnSizingState
) {
  if (
    controlledValue !== undefined ||
    hydratedStorageKey === nextStorageKey
  ) {
    return undefined
  }

  return { storageKey: nextStorageKey, value: nextValue }
}

type PersistenceControllerOptions = {
  schedule: (callback: () => void, delay: number) => unknown
  cancel: (handle: unknown) => void
  write: (storageKey: string, value: ColumnSizingState) => void
}

export function createColumnSizingPersistenceController(
  options: PersistenceControllerOptions
) {
  let pendingHandle: unknown

  return {
    schedule(storageKey: string, value: ColumnSizingState) {
      if (pendingHandle !== undefined) {
        options.cancel(pendingHandle)
      }

      const snapshot = { ...value }
      pendingHandle = options.schedule(() => {
        pendingHandle = undefined
        options.write(storageKey, snapshot)
      }, COLUMN_SIZING_PERSIST_DELAY_MS)
    },
    cancel() {
      if (pendingHandle === undefined) return
      options.cancel(pendingHandle)
      pendingHandle = undefined
    },
  }
}
