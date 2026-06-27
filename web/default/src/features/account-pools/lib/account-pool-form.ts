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
import type {
  AccountPool,
  AccountPoolAccount,
  AccountPoolAccountCreateRequest,
  AccountPoolAccountImportRequest,
  AccountPoolAccountStatus,
  AccountPoolBoundChannelCreateRequest,
  AccountPoolCapabilityMode,
  AccountPoolCreateRequest,
  AccountPoolCredentialType,
  AccountPoolPlatform,
  AccountPoolProxy,
  AccountPoolProxyCreateRequest,
  AccountPoolProxyProtocol,
  AccountPoolProxyStatus,
  AccountPoolSchedulePolicy,
} from '../types'

const MODEL_MAPPING_ERROR =
  'Model mapping must be a JSON object with string values'

// Channel type numbers (mirror web/default/src/features/channels/constants.ts).
const CHANNEL_TYPE_OPENAI = 1
const CHANNEL_TYPE_ANTHROPIC = 14
const CHANNEL_TYPE_GEMINI = 24
const CHANNEL_TYPE_CODEX_VALUE = 57

// Allowed bound-channel types per pool platform. The backend validates that a
// binding's channel type matches the pool platform, so the UI mirrors that map
// to filter the selector and surface the rule before submitting.
export function allowedChannelTypesForPlatform(
  platform: AccountPoolPlatform | string
): number[] {
  switch (platform) {
    case 'anthropic':
      return [CHANNEL_TYPE_ANTHROPIC]
    case 'gemini':
      return [CHANNEL_TYPE_GEMINI]
    case 'openai':
    default:
      return [CHANNEL_TYPE_OPENAI, CHANNEL_TYPE_CODEX_VALUE]
  }
}

// Default channel type for a new bound channel, derived from the pool platform.
export function defaultChannelTypeForPlatform(
  platform: AccountPoolPlatform | string
): number {
  return allowedChannelTypesForPlatform(platform)[0]
}

// Whether the OAuth credential type is supported for the given pool platform.
// Gemini OAuth is not supported yet, so gemini pools only allow api_key.
export function platformSupportsOAuthCredential(
  platform: AccountPoolPlatform | string
): boolean {
  return platform !== 'gemini'
}

export function normalizeAccountPoolSchedulePolicy(
  value: string
): AccountPoolSchedulePolicy {
  // Legacy or externally edited values fall back to the runtime default.
  return value.trim() === 'random' ? 'random' : 'round_robin'
}

export function normalizeOptionalAccountPoolSchedulePolicy(
  value: string
): AccountPoolSchedulePolicy | '' {
  const trimmed = value.trim()
  return trimmed === '' ? '' : normalizeAccountPoolSchedulePolicy(trimmed)
}

export type AccountPoolFormValues = {
  name: string
  platform: AccountPoolPlatform | string
  default_proxy_id: number
  default_monitor_enabled: boolean
  default_schedule_policy: AccountPoolSchedulePolicy | ''
  capability_check_enabled: boolean
  capability_check_interval_minutes: number
  capability_check_mode: AccountPoolCapabilityMode
  capability_check_channel_id: number
  capability_check_models_text: string
  capability_check_timeout_seconds: number
  capability_check_merge: boolean
  remark: string
}

export type AccountPoolAccountFormValues = {
  name: string
  account_identifier: string
  credential_type: AccountPoolCredentialType
  api_key: string
  email: string
  refresh_token: string
  access_token: string
  token_refresh_token: string
  token_expires_at: number
  token_version: number
  status: AccountPoolAccountStatus | string
  priority: number
  weight: number
  max_concurrency: number
  request_quota: number
  request_quota_window_seconds: number
  expires_at: number
  auto_pause_on_expired: boolean
  proxy_id: number
  supported_models_text: string
  model_mapping_text: string
  last_used_at: number
  rate_limited_until: number
  temp_disabled_until: number
  temp_disabled_reason: string
  last_error: string
}

export type AccountPoolProxyFormValues = {
  name: string
  protocol: AccountPoolProxyProtocol
  host: string
  port: number
  username: string
  password: string
  status: AccountPoolProxyStatus | string
  fallback_proxy_id: number
}

export type AccountImportFormValues = {
  format: 'sub2api' | 'cpa'
  content: string
  default_priority: number
  default_weight: number
  default_max_concurrency: number
  default_proxy_id: number
  default_supported_models_text: string
}

export type AccountPoolBoundChannelFormValues = {
  name: string
  type: number
  fixed_models_text: string
}

export type AccountPoolProxyOption = {
  value: string
  label: string
}

export function buildAccountPoolProxyOptions(
  proxies: Pick<AccountPoolProxy, 'id' | 'name'>[],
  noProxyLabel: string
): AccountPoolProxyOption[] {
  return [
    { value: '0', label: noProxyLabel },
    ...proxies.map((proxy) => ({
      value: String(proxy.id),
      label: proxy.name,
    })),
  ]
}

export function emptyPoolForm(): AccountPoolFormValues {
  return {
    name: '',
    platform: 'openai',
    default_proxy_id: 0,
    default_monitor_enabled: false,
    default_schedule_policy: '',
    capability_check_enabled: false,
    capability_check_interval_minutes: 1440,
    capability_check_mode: 'models_endpoint',
    capability_check_channel_id: 0,
    capability_check_models_text: '',
    capability_check_timeout_seconds: 30,
    capability_check_merge: false,
    remark: '',
  }
}

export function poolToFormValues(pool: AccountPool): AccountPoolFormValues {
  return {
    name: pool.name,
    platform: pool.platform,
    default_proxy_id: pool.default_proxy_id,
    default_monitor_enabled: pool.default_monitor_enabled,
    default_schedule_policy: normalizeOptionalAccountPoolSchedulePolicy(
      pool.default_schedule_policy
    ),
    capability_check_enabled: pool.capability_check_enabled,
    capability_check_interval_minutes:
      pool.capability_check_interval_minutes || 1440,
    capability_check_mode: pool.capability_check_mode || 'models_endpoint',
    capability_check_channel_id: pool.capability_check_channel_id || 0,
    capability_check_models_text: pool.capability_check_models.join('\n'),
    capability_check_timeout_seconds:
      pool.capability_check_timeout_seconds || 30,
    capability_check_merge: pool.capability_check_merge === true,
    remark: pool.remark,
  }
}

export function buildPoolPayload(
  values: AccountPoolFormValues
): AccountPoolCreateRequest {
  return {
    name: values.name.trim(),
    platform: values.platform || 'openai',
    default_proxy_id: toInteger(values.default_proxy_id),
    default_monitor_enabled: values.default_monitor_enabled === true,
    default_schedule_policy: normalizeOptionalAccountPoolSchedulePolicy(
      values.default_schedule_policy
    ),
    capability_check_enabled: values.capability_check_enabled === true,
    capability_check_interval_minutes: toInteger(
      values.capability_check_interval_minutes
    ),
    capability_check_mode: values.capability_check_mode || 'models_endpoint',
    capability_check_channel_id: toInteger(values.capability_check_channel_id),
    capability_check_models: normalizeModelListText(
      values.capability_check_models_text
    ),
    capability_check_timeout_seconds: toInteger(
      values.capability_check_timeout_seconds
    ),
    capability_check_merge: values.capability_check_merge === true,
    remark: values.remark.trim(),
  }
}

export function emptyAccountForm(): AccountPoolAccountFormValues {
  return {
    name: '',
    account_identifier: '',
    credential_type: 'api_key',
    api_key: '',
    email: '',
    refresh_token: '',
    access_token: '',
    token_refresh_token: '',
    token_expires_at: 0,
    token_version: 0,
    status: 'enabled',
    priority: 0,
    weight: 1,
    max_concurrency: 1,
    request_quota: 0,
    request_quota_window_seconds: 0,
    expires_at: 0,
    auto_pause_on_expired: false,
    proxy_id: 0,
    supported_models_text: '',
    model_mapping_text: '',
    last_used_at: 0,
    rate_limited_until: 0,
    temp_disabled_until: 0,
    temp_disabled_reason: '',
    last_error: '',
  }
}

export function buildAccountPayload(
  values: AccountPoolAccountFormValues
): AccountPoolAccountCreateRequest {
  return {
    name: values.name.trim(),
    account_identifier: values.account_identifier.trim(),
    credential: {
      type: values.credential_type || 'api_key',
      api_key:
        values.credential_type === 'api_key' ? values.api_key.trim() : '',
      email: values.email.trim(),
      refresh_token: values.refresh_token.trim(),
    },
    token_state: {
      access_token: values.access_token.trim(),
      refresh_token: values.token_refresh_token.trim(),
      expires_at: toInteger(values.token_expires_at),
      version: toInteger(values.token_version),
    },
    status: values.status || 'enabled',
    priority: toInteger(values.priority),
    weight: toInteger(values.weight),
    max_concurrency: Math.max(0, toInteger(values.max_concurrency)),
    request_quota: Math.max(0, toInteger(values.request_quota)),
    request_quota_window_seconds: Math.max(
      0,
      toInteger(values.request_quota_window_seconds)
    ),
    expires_at: Math.max(0, toInteger(values.expires_at)),
    auto_pause_on_expired: values.auto_pause_on_expired === true,
    proxy_id: toInteger(values.proxy_id),
    supported_models: normalizeModelListText(values.supported_models_text),
    model_mapping: parseModelMapping(values.model_mapping_text),
    last_used_at: toInteger(values.last_used_at),
    rate_limited_until: toInteger(values.rate_limited_until),
    temp_disabled_until: toInteger(values.temp_disabled_until),
    temp_disabled_reason: values.temp_disabled_reason.trim(),
    last_error: values.last_error.trim(),
  }
}

export function accountToFormValues(
  account: AccountPoolAccount
): AccountPoolAccountFormValues {
  return {
    name: account.name,
    account_identifier: account.account_identifier,
    credential_type: 'api_key',
    api_key: '',
    email: '',
    refresh_token: '',
    access_token: '',
    token_refresh_token: '',
    token_expires_at: 0,
    token_version: 0,
    status: account.status,
    priority: account.priority,
    weight: account.weight,
    max_concurrency: account.max_concurrency,
    request_quota: account.request_quota,
    request_quota_window_seconds: account.request_quota_window_seconds,
    expires_at: account.expires_at,
    auto_pause_on_expired: account.auto_pause_on_expired === true,
    proxy_id: account.proxy_id,
    supported_models_text: account.supported_models.join('\n'),
    model_mapping_text:
      Object.keys(account.model_mapping || {}).length > 0
        ? JSON.stringify(account.model_mapping, null, 2)
        : '',
    last_used_at: account.last_used_at,
    rate_limited_until: account.rate_limited_until,
    temp_disabled_until: account.temp_disabled_until,
    temp_disabled_reason: account.temp_disabled_reason,
    last_error: account.last_error,
  }
}

export function normalizeModelListText(value: string): string[] {
  const seen = new Set<string>()
  const models: string[] = []

  for (const model of value.split(/[,，\n\r]+/)) {
    const normalized = model.trim()
    if (!normalized || seen.has(normalized)) continue
    seen.add(normalized)
    models.push(normalized)
  }

  return models
}

export function maskSecretPreview(value: string): string {
  const secret = value.trim()
  if (!secret) return ''
  if (secret.length < 8) return '***'
  return `${secret.slice(0, 4)}...${secret.slice(-4)}`
}

export function emptyProxyForm(): AccountPoolProxyFormValues {
  return {
    name: '',
    protocol: 'http',
    host: '',
    port: 0,
    username: '',
    password: '',
    status: 'enabled',
    fallback_proxy_id: 0,
  }
}

export function proxyToFormValues(
  proxy: AccountPoolProxy
): AccountPoolProxyFormValues {
  return {
    name: proxy.name,
    protocol: proxy.protocol,
    host: proxy.host,
    port: proxy.port,
    username: proxy.username,
    password: '',
    status: proxy.status,
    fallback_proxy_id: proxy.fallback_proxy_id,
  }
}

export function buildProxyPayload(
  values: AccountPoolProxyFormValues
): AccountPoolProxyCreateRequest {
  return {
    name: values.name.trim(),
    protocol: values.protocol || 'http',
    host: values.host.trim(),
    port: toInteger(values.port),
    username: values.username.trim(),
    password: values.password.trim(),
    status: values.status || 'enabled',
    fallback_proxy_id: toInteger(values.fallback_proxy_id),
  }
}

export function emptyAccountImportForm(): AccountImportFormValues {
  return {
    format: 'sub2api',
    content: '',
    default_priority: 0,
    default_weight: 0,
    default_max_concurrency: 1,
    default_proxy_id: 0,
    default_supported_models_text: '',
  }
}

export function buildAccountImportPayload(
  values: AccountImportFormValues
): AccountPoolAccountImportRequest {
  return {
    format: values.format,
    content: values.content,
    dry_run: false,
    defaults: {
      status: 'enabled',
      priority: toInteger(values.default_priority),
      weight: toInteger(values.default_weight),
      max_concurrency: Math.max(0, toInteger(values.default_max_concurrency)),
      proxy_id: toInteger(values.default_proxy_id),
      supported_models: normalizeModelListText(
        values.default_supported_models_text
      ),
      model_mapping: {},
    },
  }
}

export function buildBoundChannelPayload(
  values: AccountPoolBoundChannelFormValues
): AccountPoolBoundChannelCreateRequest {
  return {
    name: values.name.trim(),
    type: toInteger(values.type),
    model_strategy: 'fixed',
    fixed_models: normalizeModelListText(values.fixed_models_text),
  }
}

function parseModelMapping(value: string): Record<string, string> {
  const text = value.trim()
  if (!text) return {}

  try {
    const parsed: unknown = JSON.parse(text)
    if (!isStringRecord(parsed)) {
      throw new Error(MODEL_MAPPING_ERROR)
    }
    return parsed
  } catch {
    throw new Error(MODEL_MAPPING_ERROR)
  }
}

function isStringRecord(value: unknown): value is Record<string, string> {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) {
    return false
  }

  return Object.values(value).every((item) => typeof item === 'string')
}

function toInteger(value: number): number {
  if (!Number.isFinite(value)) return 0
  return Math.trunc(value)
}
