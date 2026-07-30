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
import { test } from 'node:test'

import type { ColumnDef } from '@tanstack/react-table'
import i18next from 'i18next'
import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { I18nextProvider } from 'react-i18next'

import type { User } from '../types'
import { useUsersColumns } from './users-columns'

function getColumnId(column: ColumnDef<User>) {
  if (column.id) return column.id
  if ('accessorKey' in column) return String(column.accessorKey)
  return undefined
}

test('users mobile cards show identity fields in a deliberate order', async () => {
  const i18n = i18next.createInstance()
  await i18n.init({ lng: 'en', resources: { en: { translation: {} } } })

  let columns: ColumnDef<User>[] = []
  function Probe() {
    columns = useUsersColumns()
    return null
  }

  renderToStaticMarkup(
    createElement(I18nextProvider, { i18n }, createElement(Probe))
  )

  const mobileMetadata = Object.fromEntries(
    columns.map((column) => [getColumnId(column), column.meta])
  )

  assert.deepEqual(mobileMetadata.id, { mobileOrder: 10 })
  assert.deepEqual(mobileMetadata.role, { mobileOrder: 20 })
  assert.deepEqual(mobileMetadata.group, { mobileOrder: 30 })
  assert.deepEqual(mobileMetadata.quota, { mobileOrder: 40 })
})
