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
import { safeJsonParse } from '../utils/json-parser'

export type SpecialUsableRule = {
  userGroup: string
  visible: boolean
  visibleKeyStyle: 'prefixed' | 'plain'
  targetGroup: string
  description: string
}

function isJsonObject(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function parseJsonObject(value: string): Record<string, unknown> {
  const parsed = safeJsonParse<unknown>(value, {
    fallback: null,
    silent: true,
  })
  return isJsonObject(parsed) ? parsed : {}
}

export function parseGroupRatioMap(value: string): Record<string, unknown> {
  return parseJsonObject(value)
}

export function parseGroupDescriptionMap(
  value: string
): Record<string, string> {
  const parsed = parseJsonObject(value)
  const result: Record<string, string> = {}
  for (const [name, description] of Object.entries(parsed)) {
    result[name] = String(description ?? '')
  }
  return result
}

export function parseNestedGroupRatioMap(
  value: string
): Record<string, Record<string, number>> {
  const parsed = parseJsonObject(value)
  const result: Record<string, Record<string, number>> = {}

  for (const [userGroup, overrides] of Object.entries(parsed)) {
    if (!isJsonObject(overrides)) continue
    const validOverrides: Record<string, number> = {}
    for (const [targetGroup, ratio] of Object.entries(overrides)) {
      if (typeof ratio === 'number' && Number.isFinite(ratio)) {
        validOverrides[targetGroup] = ratio
      }
    }
    result[userGroup] = validOverrides
  }

  return result
}

export function parseAutoGroups(value: string): string[] {
  const parsed = safeJsonParse<unknown>(value, {
    fallback: null,
    silent: true,
  })
  if (!Array.isArray(parsed)) return []
  return parsed.filter((group): group is string => typeof group === 'string')
}

export function collectConfiguredGroupNames(values: {
  groupRatio: string
  userUsableGroups: string
  topupGroupRatio: string
}): string[] {
  const ratioMap = parseGroupRatioMap(values.groupRatio)
  const usableMap = parseGroupDescriptionMap(values.userUsableGroups)
  const topupMap = parseGroupRatioMap(values.topupGroupRatio)
  return [
    ...new Set([
      ...Object.keys(ratioMap),
      ...Object.keys(usableMap),
      ...Object.keys(topupMap),
    ]),
  ]
}

export function parseSpecialUsableRules(value: string): SpecialUsableRule[] {
  const parsed = parseJsonObject(value)
  const rules: SpecialUsableRule[] = []

  for (const [userGroup, inner] of Object.entries(parsed)) {
    if (!isJsonObject(inner)) continue
    for (const [rawKey, rawDescription] of Object.entries(inner)) {
      let visible = true
      let visibleKeyStyle: SpecialUsableRule['visibleKeyStyle'] = 'plain'
      let targetGroup = rawKey
      if (rawKey.startsWith('-:')) {
        visible = false
        visibleKeyStyle = 'prefixed'
        targetGroup = rawKey.slice(2)
      } else if (rawKey.startsWith('+:')) {
        visibleKeyStyle = 'prefixed'
        targetGroup = rawKey.slice(2)
      }
      rules.push({
        userGroup,
        visible,
        visibleKeyStyle,
        targetGroup,
        description: typeof rawDescription === 'string' ? rawDescription : '',
      })
    }
  }

  return rules
}

export function serializeSpecialUsableRules(
  rules: SpecialUsableRule[]
): string {
  const result: Record<string, Record<string, string>> = {}

  for (const rule of rules) {
    if (!rule.userGroup || !rule.targetGroup) continue
    if (!result[rule.userGroup]) result[rule.userGroup] = {}
    let rawKey = `-:${rule.targetGroup}`
    if (rule.visible) {
      rawKey =
        rule.visibleKeyStyle === 'plain'
          ? rule.targetGroup
          : `+:${rule.targetGroup}`
    }
    result[rule.userGroup][rawKey] = rule.description
  }

  return Object.keys(result).length === 0
    ? '{}'
    : JSON.stringify(result, null, 2)
}

export function setSpecialUsableRuleVisibility(
  rule: SpecialUsableRule,
  visible: boolean
): SpecialUsableRule {
  if (rule.visible === visible) return rule
  let description = rule.description
  if (!visible) description = 'remove'
  else if (description === 'remove') description = ''
  return {
    ...rule,
    visible,
    description,
  }
}
