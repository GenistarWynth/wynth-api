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

export const INTERFACE_LANGUAGE_OPTIONS = [
  { code: 'zh', label: '简体中文' },
  { code: 'en', label: 'English' },
  { code: 'fr', label: 'Français' },
  { code: 'ru', label: 'Русский' },
  { code: 'ja', label: '日本語' },
  { code: 'vi', label: 'Tiếng Việt' },
] as const

export type InterfaceLanguageCode =
  (typeof INTERFACE_LANGUAGE_OPTIONS)[number]['code']

export function normalizeInterfaceLanguage(value?: string | null): string {
  if (!value) return 'en'

  const normalized = value.trim().replaceAll('_', '-').toLowerCase()
  if (normalized.startsWith('zh')) return 'zh'

  return INTERFACE_LANGUAGE_OPTIONS.some((lang) => lang.code === normalized)
    ? normalized
    : 'en'
}

/**
 * Map browser locale detection onto the interface language codes supported by
 * Wynth. The app has one Chinese resource, so regional Chinese tags share it.
 */
export function convertDetectedLanguage(value: string): string {
  const normalized = value.trim().replaceAll('_', '-').toLowerCase()
  return normalized.startsWith('zh') ? 'zh' : value
}

/**
 * Convert an i18next language code into a valid BCP-47 tag for Intl APIs.
 * Invalid values return undefined so Intl falls back to the runtime locale.
 */
export function toIntlLocale(value?: string | null): string | undefined {
  if (!value) return undefined

  const normalized = value.trim().replaceAll('_', '-')
  if (normalized === 'zh' || normalized === 'zhCN') return 'zh-CN'
  if (normalized === 'zhTW') return 'zh-TW'

  try {
    return Intl.getCanonicalLocales(normalized)[0]
  } catch {
    return undefined
  }
}
