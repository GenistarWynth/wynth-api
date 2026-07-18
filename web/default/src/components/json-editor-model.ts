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

export type JsonEditorRow = {
  id: string
  key: string
  value: string
}

export type JsonEditorValueType = 'string' | 'number' | 'any'

export function parseJsonEditorRows(json: string): JsonEditorRow[] {
  try {
    if (!json.trim()) return []

    const parsed: unknown = JSON.parse(json)
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
      return []
    }

    return Object.entries(parsed).map(([key, value], index) => ({
      id: `${Date.now()}-${index}`,
      key,
      value: typeof value === 'object' ? JSON.stringify(value) : String(value),
    }))
  } catch {
    return []
  }
}

export function serializeJsonEditorRows(
  rows: JsonEditorRow[],
  valueType: JsonEditorValueType
): string {
  if (rows.length === 0) return ''

  const object: Record<string, unknown> = {}
  rows.forEach((row) => {
    if (!row.key.trim()) return

    let parsedValue: unknown = row.value.trim()
    if (valueType === 'number') {
      parsedValue = Number(parsedValue) || 0
    } else if (valueType === 'any') {
      try {
        parsedValue = JSON.parse(row.value)
      } catch {
        parsedValue = row.value.trim()
      }
    }

    object[row.key.trim()] = parsedValue
  })

  return JSON.stringify(object, null, 2)
}
