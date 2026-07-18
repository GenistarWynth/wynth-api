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

const CHANNEL_STATUS_FILTER_STORAGE_KEY = 'channel-status-filter'

type ChannelStatusFilterStorage = {
  getItem: (key: string) => string | null
  setItem: (key: string, value: string) => void
}

type ChannelStatusFilter = 'enabled' | 'disabled'

function parseChannelStatusFilter(value: unknown): ChannelStatusFilter | null {
  if (value === 'enabled' || value === 'disabled') return value
  if (
    Array.isArray(value) &&
    value.length === 1 &&
    (value[0] === 'enabled' || value[0] === 'disabled')
  ) {
    return value[0]
  }
  return null
}

function getBrowserStorage(): ChannelStatusFilterStorage | null {
  try {
    if (typeof window === 'undefined') return null
    return window.localStorage
  } catch {
    return null
  }
}

export function resolveChannelStatusFilter(
  urlValue: unknown,
  storage?: ChannelStatusFilterStorage | null
): ChannelStatusFilter[] {
  if (urlValue !== undefined) {
    const status = parseChannelStatusFilter(urlValue)
    return status ? [status] : []
  }

  const resolvedStorage = storage === undefined ? getBrowserStorage() : storage
  if (!resolvedStorage) return []

  try {
    const status = parseChannelStatusFilter(
      resolvedStorage.getItem(CHANNEL_STATUS_FILTER_STORAGE_KEY)
    )
    return status ? [status] : []
  } catch {
    return []
  }
}

export function persistChannelStatusFilter(
  value: unknown,
  storage?: ChannelStatusFilterStorage | null
): void {
  const resolvedStorage = storage === undefined ? getBrowserStorage() : storage
  if (!resolvedStorage) return

  const status = parseChannelStatusFilter(value)
  try {
    resolvedStorage.setItem(CHANNEL_STATUS_FILTER_STORAGE_KEY, status ?? 'all')
  } catch {
    /* localStorage can be unavailable; URL state remains authoritative. */
  }
}
