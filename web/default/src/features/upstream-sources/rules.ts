import type {
  CodexImageGenerationBridgePolicy,
  UpstreamSourceLocalGroupRule,
  UpstreamSourceModelStrategy,
} from './types'

export const UPSTREAM_SOURCE_MODEL_STRATEGY_ALL = 'all_upstream' as const
export const UPSTREAM_SOURCE_MODEL_STRATEGY_FIXED = 'fixed' as const

export type LocalGroupRuleTemplateKey =
  | 'openai'
  | 'openai-pro'
  | 'anthropic'
  | 'anthropic-pro'

type LocalGroupRuleTemplateDefaults = {
  defaultLocalGroup: string
  proLocalGroup: string
  monitor?: UpstreamSourceLocalGroupRule['monitor']
  autoSync?: UpstreamSourceLocalGroupRule['auto_sync']
  autoPriority?: UpstreamSourceLocalGroupRule['auto_priority']
  codexImageGenerationBridgePolicy?: CodexImageGenerationBridgePolicy
  modelStrategy?: UpstreamSourceModelStrategy
  fixedModels?: string[]
}

export type LocalGroupRuleStrategyOrigin = 'inherit' | 'override'

export type LocalGroupRuleStrategyOverrideKey =
  | 'monitor'
  | 'auto_sync'
  | 'auto_priority'
  | 'codex_image_generation_bridge'
  | 'model_strategy'

export type LocalGroupRuleStrategyDefaults = {
  monitor: {
    enabled: boolean
    interval_minutes: number
  }
  autoSync: {
    enabled: boolean
    interval_minutes: number
  }
  autoPriority: {
    enabled: boolean
    interval_minutes: number
    window_hours: number
  }
  codexImageGenerationBridgePolicy: CodexImageGenerationBridgePolicy
  modelStrategy: UpstreamSourceModelStrategy
  fixedModels: string[]
}

// DEFAULT_LOCAL_GROUP_RULE_STRATEGY_DEFAULTS is the constant inherit baseline
// every per-rule strategy override falls back to now that upstream sources no
// longer carry source-level "Default *" settings — everything is per-rule.
export const DEFAULT_LOCAL_GROUP_RULE_STRATEGY_DEFAULTS: LocalGroupRuleStrategyDefaults =
  {
    monitor: {
      enabled: false,
      interval_minutes: 10,
    },
    autoSync: {
      enabled: false,
      interval_minutes: 30,
    },
    autoPriority: {
      enabled: false,
      interval_minutes: 30,
      window_hours: 24,
    },
    codexImageGenerationBridgePolicy: 'follow',
    modelStrategy: UPSTREAM_SOURCE_MODEL_STRATEGY_ALL,
    fixedModels: [],
  }

type ResolvedMonitorStrategy = {
  origin: LocalGroupRuleStrategyOrigin
  enabled: boolean
  interval_minutes: number
}

type ResolvedAutoSyncStrategy = {
  origin: LocalGroupRuleStrategyOrigin
  enabled: boolean
  interval_minutes: number
}

type ResolvedAutoPriorityStrategy = {
  origin: LocalGroupRuleStrategyOrigin
  enabled: boolean
  interval_minutes: number
  window_hours: number
}

type ResolvedCodexImageGenerationBridgeStrategy = {
  origin: LocalGroupRuleStrategyOrigin
  value: CodexImageGenerationBridgePolicy
}

type ResolvedModelStrategy = {
  origin: LocalGroupRuleStrategyOrigin
  strategy: UpstreamSourceModelStrategy
  fixed_models: string[]
}

export type LocalGroupRuleStrategyResolution = {
  has_overrides: boolean
  override_keys: LocalGroupRuleStrategyOverrideKey[]
  monitor: ResolvedMonitorStrategy
  auto_sync: ResolvedAutoSyncStrategy
  auto_priority: ResolvedAutoPriorityStrategy
  codex_image_generation_bridge_policy: ResolvedCodexImageGenerationBridgeStrategy
  model: ResolvedModelStrategy
}

export type LocalGroupRuleUserTemplate = {
  id: string
  name: string
  created_at: number
  rule: UpstreamSourceLocalGroupRule
}

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

export function hasLocalGroupRuleMatcher(
  rule: Pick<
    UpstreamSourceLocalGroupRule,
    'platforms' | 'name_contains' | 'description_contains'
  >
) {
  return (
    normalizeKeywordList(rule.platforms ?? []).length > 0 ||
    normalizeKeywordList(rule.name_contains ?? []).length > 0 ||
    normalizeKeywordList(rule.description_contains ?? []).length > 0
  )
}

function toStringArray(value: unknown): string[] {
  return Array.isArray(value)
    ? value.map((item) => String(item))
    : typeof value === 'string'
      ? [value]
      : []
}

function normalizeTemplateID(value: string, createdAt: number) {
  const normalized = value
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
  return normalized || `template-${createdAt}`
}

export function normalizeModelStrategy(
  value?: string | null
): UpstreamSourceModelStrategy {
  return value === UPSTREAM_SOURCE_MODEL_STRATEGY_FIXED
    ? UPSTREAM_SOURCE_MODEL_STRATEGY_FIXED
    : UPSTREAM_SOURCE_MODEL_STRATEGY_ALL
}

export function normalizeCodexImageGenerationBridgePolicy(
  value?: string | null
): CodexImageGenerationBridgePolicy {
  return value === 'enabled' || value === 'disabled' ? value : 'follow'
}

function hasCodexImageGenerationBridgePolicy(value?: string | null): boolean {
  return typeof value === 'string' && value.trim() !== ''
}

function sameStringList(left: string[], right: string[]) {
  if (left.length !== right.length) {
    return false
  }
  return left.every((value, index) => value === right[index])
}

function strategyOrigin(isOverride: boolean): LocalGroupRuleStrategyOrigin {
  return isOverride ? 'override' : 'inherit'
}

export function resolveLocalGroupRuleStrategy(
  rule: UpstreamSourceLocalGroupRule,
  defaults: LocalGroupRuleStrategyDefaults
): LocalGroupRuleStrategyResolution {
  const defaultModelStrategy = normalizeModelStrategy(defaults.modelStrategy)
  const defaultFixedModels =
    defaultModelStrategy === UPSTREAM_SOURCE_MODEL_STRATEGY_FIXED
      ? normalizeModelList(defaults.fixedModels)
      : []
  const monitorEnabled = rule.monitor?.enabled ?? defaults.monitor.enabled
  const monitorInterval =
    typeof rule.monitor?.interval_minutes === 'number'
      ? rule.monitor.interval_minutes
      : defaults.monitor.interval_minutes
  const autoSyncEnabled = rule.auto_sync?.enabled ?? defaults.autoSync.enabled
  const autoSyncInterval =
    typeof rule.auto_sync?.interval_minutes === 'number'
      ? rule.auto_sync.interval_minutes
      : defaults.autoSync.interval_minutes
  const autoPriorityEnabled =
    rule.auto_priority?.enabled ?? defaults.autoPriority.enabled
  const autoPriorityInterval =
    typeof rule.auto_priority?.interval_minutes === 'number'
      ? rule.auto_priority.interval_minutes
      : defaults.autoPriority.interval_minutes
  const autoPriorityWindow =
    typeof rule.auto_priority?.window_hours === 'number'
      ? rule.auto_priority.window_hours
      : defaults.autoPriority.window_hours
  const defaultBridgePolicy = normalizeCodexImageGenerationBridgePolicy(
    defaults.codexImageGenerationBridgePolicy
  )
  const hasBridgePolicy = hasCodexImageGenerationBridgePolicy(
    rule.codex_image_generation_bridge_policy
  )
  const bridgePolicy = hasBridgePolicy
    ? normalizeCodexImageGenerationBridgePolicy(
        rule.codex_image_generation_bridge_policy
      )
    : defaultBridgePolicy
  const hasRuleModelStrategy =
    typeof rule.model_strategy === 'string' && rule.model_strategy.trim() !== ''
  const modelStrategy = hasRuleModelStrategy
    ? normalizeModelStrategy(rule.model_strategy)
    : defaultModelStrategy
  const fixedModels =
    modelStrategy === UPSTREAM_SOURCE_MODEL_STRATEGY_FIXED
      ? normalizeModelList(rule.fixed_models ?? [])
      : []

  const monitorOverride =
    monitorEnabled !== defaults.monitor.enabled ||
    monitorInterval !== defaults.monitor.interval_minutes
  const autoSyncOverride =
    autoSyncEnabled !== defaults.autoSync.enabled ||
    autoSyncInterval !== defaults.autoSync.interval_minutes
  const autoPriorityOverride =
    autoPriorityEnabled !== defaults.autoPriority.enabled ||
    autoPriorityInterval !== defaults.autoPriority.interval_minutes ||
    autoPriorityWindow !== defaults.autoPriority.window_hours
  const bridgeOverride = hasBridgePolicy && bridgePolicy !== defaultBridgePolicy
  const modelOverride =
    modelStrategy !== defaultModelStrategy ||
    !sameStringList(fixedModels, defaultFixedModels)
  const overrideKeys: LocalGroupRuleStrategyOverrideKey[] = []

  if (monitorOverride) {
    overrideKeys.push('monitor')
  }
  if (autoSyncOverride) {
    overrideKeys.push('auto_sync')
  }
  if (autoPriorityOverride) {
    overrideKeys.push('auto_priority')
  }
  if (bridgeOverride) {
    overrideKeys.push('codex_image_generation_bridge')
  }
  if (modelOverride) {
    overrideKeys.push('model_strategy')
  }

  return {
    has_overrides: overrideKeys.length > 0,
    override_keys: overrideKeys,
    monitor: {
      origin: strategyOrigin(monitorOverride),
      enabled: monitorEnabled,
      interval_minutes: monitorInterval,
    },
    auto_sync: {
      origin: strategyOrigin(autoSyncOverride),
      enabled: autoSyncEnabled,
      interval_minutes: autoSyncInterval,
    },
    auto_priority: {
      origin: strategyOrigin(autoPriorityOverride),
      enabled: autoPriorityEnabled,
      interval_minutes: autoPriorityInterval,
      window_hours: autoPriorityWindow,
    },
    codex_image_generation_bridge_policy: {
      origin: strategyOrigin(bridgeOverride),
      value: bridgePolicy,
    },
    model: {
      origin: strategyOrigin(modelOverride),
      strategy: modelStrategy,
      fixed_models: fixedModels,
    },
  }
}

export function buildLocalGroupRuleTemplate(
  key: LocalGroupRuleTemplateKey,
  defaults: LocalGroupRuleTemplateDefaults
): UpstreamSourceLocalGroupRule {
  const modelStrategy = normalizeModelStrategy(defaults.modelStrategy)
  const baseRule: UpstreamSourceLocalGroupRule = {
    name: '',
    local_group:
      key.endsWith('-pro') && defaults.proLocalGroup
        ? defaults.proLocalGroup
        : defaults.defaultLocalGroup,
    platforms: [],
    name_contains: [],
    description_contains: [],
    exclude_keywords: [],
    ...(defaults.monitor ? { monitor: defaults.monitor } : {}),
    ...(defaults.autoSync ? { auto_sync: defaults.autoSync } : {}),
    ...(defaults.autoPriority ? { auto_priority: defaults.autoPriority } : {}),
    ...(defaults.codexImageGenerationBridgePolicy &&
    normalizeCodexImageGenerationBridgePolicy(
      defaults.codexImageGenerationBridgePolicy
    ) !== 'follow'
      ? {
          codex_image_generation_bridge_policy:
            normalizeCodexImageGenerationBridgePolicy(
              defaults.codexImageGenerationBridgePolicy
            ),
        }
      : {}),
    model_strategy: modelStrategy,
    fixed_models:
      modelStrategy === UPSTREAM_SOURCE_MODEL_STRATEGY_FIXED
        ? normalizeModelList(defaults.fixedModels ?? [])
        : [],
  }

  switch (key) {
    case 'openai':
      return {
        ...baseRule,
        name: 'OpenAI',
        platforms: ['openai'],
        name_contains: ['gpt'],
        exclude_keywords: ['pro'],
      }
    case 'openai-pro':
      return {
        ...baseRule,
        name: 'OpenAI Pro',
        platforms: ['openai'],
        name_contains: ['pro'],
        description_contains: ['pro'],
      }
    case 'anthropic':
      return {
        ...baseRule,
        name: 'Anthropic',
        platforms: ['anthropic'],
        exclude_keywords: ['pro'],
      }
    case 'anthropic-pro':
      return {
        ...baseRule,
        name: 'Anthropic Pro',
        platforms: ['anthropic'],
        name_contains: ['pro'],
        description_contains: ['pro'],
      }
  }
}

function normalizeTemplateRule(
  rule: Partial<UpstreamSourceLocalGroupRule>
): UpstreamSourceLocalGroupRule {
  const modelStrategy = normalizeModelStrategy(rule.model_strategy)
  const autoPriority = normalizeRuleAutoPriority(rule.auto_priority)
  const hasBridgePolicy = hasCodexImageGenerationBridgePolicy(
    rule.codex_image_generation_bridge_policy
  )
  const bridgePolicy = normalizeCodexImageGenerationBridgePolicy(
    rule.codex_image_generation_bridge_policy
  )
  return {
    name: String(rule.name ?? '').trim(),
    local_group: String(rule.local_group ?? '').trim(),
    platforms: normalizeKeywordList(toStringArray(rule.platforms)),
    name_contains: normalizeKeywordList(toStringArray(rule.name_contains)),
    description_contains: normalizeKeywordList(
      toStringArray(rule.description_contains)
    ),
    exclude_keywords: normalizeKeywordList(
      toStringArray(rule.exclude_keywords)
    ),
    ...(rule.monitor ? { monitor: rule.monitor } : {}),
    ...(rule.auto_sync ? { auto_sync: rule.auto_sync } : {}),
    ...(autoPriority ? { auto_priority: autoPriority } : {}),
    ...(hasBridgePolicy
      ? { codex_image_generation_bridge_policy: bridgePolicy }
      : {}),
    model_strategy: modelStrategy,
    fixed_models:
      modelStrategy === UPSTREAM_SOURCE_MODEL_STRATEGY_FIXED
        ? normalizeModelList(toStringArray(rule.fixed_models))
        : [],
  }
}

function normalizeRuleAutoPriority(
  value: UpstreamSourceLocalGroupRule['auto_priority'] | undefined
): UpstreamSourceLocalGroupRule['auto_priority'] | undefined {
  if (!value) {
    return undefined
  }
  const normalized: UpstreamSourceLocalGroupRule['auto_priority'] = {}
  if (typeof value.enabled === 'boolean') {
    normalized.enabled = value.enabled
  }
  if (
    typeof value.interval_minutes === 'number' &&
    Number.isFinite(value.interval_minutes)
  ) {
    normalized.interval_minutes = Math.max(
      0,
      Math.trunc(value.interval_minutes)
    )
  }
  if (
    typeof value.window_hours === 'number' &&
    Number.isFinite(value.window_hours)
  ) {
    normalized.window_hours = Math.max(1, Math.trunc(value.window_hours))
  }
  return Object.keys(normalized).length > 0 ? normalized : undefined
}

export function createLocalGroupRuleUserTemplate(
  name: string,
  rule: UpstreamSourceLocalGroupRule,
  createdAt = Date.now()
): LocalGroupRuleUserTemplate {
  const trimmedName = name.trim() || rule.name.trim() || `Template ${createdAt}`
  return {
    id: normalizeTemplateID(trimmedName, createdAt),
    name: trimmedName,
    created_at: createdAt,
    rule: normalizeTemplateRule({ ...rule, name: rule.name || trimmedName }),
  }
}

export function serializeLocalGroupRuleUserTemplates(
  templates: LocalGroupRuleUserTemplate[]
) {
  return JSON.stringify(templates)
}

function parseUserTemplate(value: unknown): LocalGroupRuleUserTemplate | null {
  if (!value || typeof value !== 'object') {
    return null
  }
  const item = value as Partial<LocalGroupRuleUserTemplate>
  if (
    typeof item.id !== 'string' ||
    item.id.trim() === '' ||
    typeof item.name !== 'string' ||
    item.name.trim() === '' ||
    typeof item.created_at !== 'number' ||
    !Number.isFinite(item.created_at) ||
    !item.rule ||
    typeof item.rule !== 'object'
  ) {
    return null
  }
  const rule = normalizeTemplateRule(
    item.rule as Partial<UpstreamSourceLocalGroupRule>
  )
  if (!hasLocalGroupRuleMatcher(rule)) {
    return null
  }
  return {
    id: item.id.trim(),
    name: item.name.trim(),
    created_at: item.created_at,
    rule,
  }
}

export function parseLocalGroupRuleUserTemplates(
  raw: string | null | undefined
): LocalGroupRuleUserTemplate[] {
  if (!raw) {
    return []
  }
  try {
    const parsed = JSON.parse(raw)
    if (!Array.isArray(parsed)) {
      return []
    }
    return parsed
      .map(parseUserTemplate)
      .filter((item): item is LocalGroupRuleUserTemplate => item !== null)
  } catch {
    return []
  }
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
      const autoPriority = normalizeRuleAutoPriority(rule.auto_priority)
      const hasBridgePolicy = hasCodexImageGenerationBridgePolicy(
        rule.codex_image_generation_bridge_policy
      )
      const bridgePolicy = normalizeCodexImageGenerationBridgePolicy(
        rule.codex_image_generation_bridge_policy
      )

      return {
        name: rule.name.trim(),
        local_group: rule.local_group.trim(),
        platforms,
        name_contains: nameContains,
        description_contains: descriptionContains,
        exclude_keywords: excludeKeywords,
        ...(rule.monitor ? { monitor: rule.monitor } : {}),
        ...(rule.auto_sync ? { auto_sync: rule.auto_sync } : {}),
        ...(autoPriority ? { auto_priority: autoPriority } : {}),
        ...(hasBridgePolicy
          ? { codex_image_generation_bridge_policy: bridgePolicy }
          : {}),
        model_strategy: modelStrategy,
        fixed_models: fixedModels,
      }
    })
    .filter(hasLocalGroupRuleMatcher)
}
