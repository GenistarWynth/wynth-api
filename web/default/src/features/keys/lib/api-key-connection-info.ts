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
import { encodeChannelConnectionInfo } from '@/lib/channel-connection-info'

type StatusStorage = {
  getItem: (key: string) => string | null
}

type ApiKeyConnectionInfoEnvironment = {
  storage?: StatusStorage | null
  origin?: string
}

function getBrowserStorage(): StatusStorage | null {
  try {
    if (typeof window === 'undefined') return null
    return window.localStorage
  } catch {
    return null
  }
}

function getBrowserOrigin(): string {
  try {
    return typeof window === 'undefined' ? '' : window.location.origin
  } catch {
    return ''
  }
}

export function getServerAddress(
  environment: ApiKeyConnectionInfoEnvironment = {}
): string {
  const storage =
    environment.storage === undefined
      ? getBrowserStorage()
      : environment.storage

  try {
    const raw = storage?.getItem('status')
    if (raw) {
      const status: unknown = JSON.parse(raw)
      if (
        status &&
        typeof status === 'object' &&
        'server_address' in status &&
        typeof status.server_address === 'string' &&
        status.server_address
      ) {
        return status.server_address
      }
    }
  } catch {
    /* Malformed or unavailable status storage falls back to the origin. */
  }

  return environment.origin ?? getBrowserOrigin()
}

export function encodeApiKeyConnectionInfo(
  key: string,
  environment: ApiKeyConnectionInfoEnvironment = {}
): string {
  return encodeChannelConnectionInfo(key, getServerAddress(environment))
}
