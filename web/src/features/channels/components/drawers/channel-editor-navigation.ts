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

export const CHANNEL_EDITOR_SECTION_IDS = {
  identity: 'channel-section-identity',
  credentials: 'channel-section-credentials',
  models: 'channel-section-models',
  advanced: 'channel-section-advanced',
} as const

export const CHANNEL_EDITOR_MAIN_SECTION_IDS = Object.values(
  CHANNEL_EDITOR_SECTION_IDS
)

export const ADVANCED_SETTINGS_SECTION_IDS = {
  routingStrategy: 'channel-section-advanced-routing-strategy',
  internalNotes: 'channel-section-advanced-internal-notes',
  overrideRules: 'channel-section-advanced-override-rules',
  extraSettings: 'channel-section-advanced-extra-settings',
  fieldPassthrough: 'channel-section-advanced-field-passthrough',
  upstreamModelDetection: 'channel-section-advanced-upstream-model-detection',
} as const

export const ADVANCED_SETTINGS_CHILD_SECTION_IDS = Object.values(
  ADVANCED_SETTINGS_SECTION_IDS
)

export function hasConfiguredOverrideValue(value: unknown): boolean {
  if (typeof value !== 'string') return false
  const trimmed = value.trim()
  if (!trimmed || trimmed === 'null') return false
  try {
    const parsed = JSON.parse(trimmed) as unknown
    if (parsed === null) return false
    if (Array.isArray(parsed)) return parsed.length > 0
    if (typeof parsed === 'object') return Object.keys(parsed).length > 0
  } catch {
    return true
  }
  return true
}

export function isAdvancedNavigationTarget(targetId: string): boolean {
  return (
    targetId === CHANNEL_EDITOR_SECTION_IDS.advanced ||
    ADVANCED_SETTINGS_CHILD_SECTION_IDS.includes(
      targetId as (typeof ADVANCED_SETTINGS_CHILD_SECTION_IDS)[number]
    )
  )
}
