import type {
  UpstreamSourceLocalGroupRule,
  UpstreamSourceModelStrategy,
} from './types'

export const UPSTREAM_SOURCE_MODEL_STRATEGY_ALL = 'all_upstream' as const
export const UPSTREAM_SOURCE_MODEL_STRATEGY_FIXED = 'fixed' as const

export function normalizeKeywordList(value: string | string[]): string[] {
  const raw = Array.isArray(value) ? value.join(',') : value
  const seen = new Set<string>()
  return raw
    .split(/[\n,，]+/)
    .map((item) => item.trim().toLowerCase())
    .filter((item) => {
      if (!item || seen.has(item)) {
        return false
      }
      seen.add(item)
      return true
    })
}

export function normalizeModelList(values: string[]): string[] {
  const seen = new Set<string>()
  return values
    .map((item) => item.trim())
    .filter((item) => {
      if (!item || seen.has(item)) {
        return false
      }
      seen.add(item)
      return true
    })
}

export function formatKeywordList(values: string[]) {
  return values.join(', ')
}

export function normalizeModelStrategy(
  value?: string | null
): UpstreamSourceModelStrategy {
  return value === UPSTREAM_SOURCE_MODEL_STRATEGY_FIXED
    ? UPSTREAM_SOURCE_MODEL_STRATEGY_FIXED
    : UPSTREAM_SOURCE_MODEL_STRATEGY_ALL
}

export function normalizeSyncRules(
  rules: UpstreamSourceLocalGroupRule[]
): UpstreamSourceLocalGroupRule[] {
  return rules
    .map((rule) => {
      const platforms = normalizeKeywordList(rule.platforms ?? [])
      const nameContains = normalizeKeywordList(rule.name_contains ?? [])
      const descriptionContains = normalizeKeywordList(
        rule.description_contains ?? []
      )
      const excludeKeywords = normalizeKeywordList(rule.exclude_keywords ?? [])
      const modelStrategy = normalizeModelStrategy(rule.model_strategy)
      const fixedModels =
        modelStrategy === UPSTREAM_SOURCE_MODEL_STRATEGY_FIXED
          ? normalizeModelList(rule.fixed_models ?? [])
          : []

      return {
        name: rule.name.trim(),
        local_group: rule.local_group.trim(),
        platforms,
        name_contains: nameContains,
        description_contains: descriptionContains,
        exclude_keywords: excludeKeywords,
        ...(rule.monitor ? { monitor: rule.monitor } : {}),
        ...(rule.auto_sync ? { auto_sync: rule.auto_sync } : {}),
        model_strategy: modelStrategy,
        fixed_models: fixedModels,
      }
    })
    .filter(
      (rule) =>
        rule.platforms.length > 0 ||
        rule.name_contains.length > 0 ||
        rule.description_contains.length > 0
    )
}
