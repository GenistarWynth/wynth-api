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
import {
  ADVANCED_CUSTOM_INCOMING_PATH_OPTIONS,
  getAdvancedCustomConverterOptions,
  getDefaultAdvancedCustomIncomingPath,
  isAdvancedCustomIncomingPathAllowed,
  normalizeAdvancedCustomConfig,
} from '../../lib/advanced-custom'
import type { AdvancedCustomConfig, AdvancedCustomRoute } from '../../types'

export type AdvancedCustomRouteRow = {
  key: string
  route: AdvancedCustomRoute
}

type AdvancedCustomRouteKeyFactory = () => string

export function createAdvancedCustomRouteRows(
  config: AdvancedCustomConfig,
  createKey: AdvancedCustomRouteKeyFactory
): AdvancedCustomRouteRow[] {
  const normalized = normalizeAdvancedCustomConfig(config)
  return (normalized.advanced_routes || []).map((route) => ({
    key: createKey(),
    route,
  }))
}

export function replaceAdvancedCustomRouteRows(
  config: AdvancedCustomConfig,
  createKey: AdvancedCustomRouteKeyFactory
): AdvancedCustomRouteRow[] {
  return createAdvancedCustomRouteRows(config, createKey)
}

export function appendAdvancedCustomRouteRows(
  rows: AdvancedCustomRouteRow[],
  config: AdvancedCustomConfig,
  createKey: AdvancedCustomRouteKeyFactory
): AdvancedCustomRouteRow[] {
  return [...rows, ...createAdvancedCustomRouteRows(config, createKey)]
}

export function removeAdvancedCustomRouteRow(
  rows: AdvancedCustomRouteRow[],
  routeKey: string
): AdvancedCustomRouteRow[] {
  return rows.filter((row) => row.key !== routeKey)
}

export function updateAdvancedCustomRouteRow(
  rows: AdvancedCustomRouteRow[],
  routeKey: string,
  patch: Partial<AdvancedCustomRoute>
): AdvancedCustomRouteRow[] {
  return rows.map((row) =>
    row.key === routeKey ? { ...row, route: { ...row.route, ...patch } } : row
  )
}

export function advancedCustomRouteRowsToConfig(
  rows: AdvancedCustomRouteRow[]
): AdvancedCustomConfig {
  return normalizeAdvancedCustomConfig({
    advanced_routes: rows.map((row) => row.route),
  })
}

export function getAdvancedCustomRouteEditorOptions(
  route: AdvancedCustomRoute
) {
  const converter = route.converter || 'none'
  const incomingPath =
    route.incoming_path || getDefaultAdvancedCustomIncomingPath(converter)
  return {
    incomingPathOptions: ADVANCED_CUSTOM_INCOMING_PATH_OPTIONS,
    converterOptions: getAdvancedCustomConverterOptions(incomingPath),
  }
}

export function getAdvancedCustomIncomingPathChange(
  route: AdvancedCustomRoute,
  nextIncomingPath: string | null
): Partial<AdvancedCustomRoute> {
  const converter = route.converter || 'none'
  const incomingPath =
    nextIncomingPath || getDefaultAdvancedCustomIncomingPath(converter)
  const patch: Partial<AdvancedCustomRoute> = {
    incoming_path: incomingPath,
  }
  if (!isAdvancedCustomIncomingPathAllowed(incomingPath, converter)) {
    patch.converter = 'none'
  }
  return patch
}
