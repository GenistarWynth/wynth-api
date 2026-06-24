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
  AccountPoolAccount,
  AccountPoolAccountCreateRequest,
  AccountPoolAccountStatus,
  AccountPoolCreateRequest,
  AccountPoolCredentialType,
  AccountPoolPlatform,
  AccountPoolProxy,
  AccountPoolProxyCreateRequest,
  AccountPoolProxyProtocol,
  AccountPoolProxyStatus,
} from '../types'

const MODEL_MAPPING_ERROR =
  'Model mapping must be a JSON object with string values'

export type AccountPoolFormValues = {
  name: string
  platform: AccountPoolPlatform | string
  default_proxy_id: number
  default_monitor_enabled: boolean
  default_schedule_policy: string
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

export function emptyPoolForm(): AccountPoolFormValues {
  return {
    name: '',
    platform: 'openai',
    default_proxy_id: 0,
    default_monitor_enabled: false,
    default_schedule_policy: '',
    remark: '',
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
    default_schedule_policy: values.default_schedule_policy.trim(),
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
    max_concurrency: Math.max(1, toInteger(values.max_concurrency)),
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
