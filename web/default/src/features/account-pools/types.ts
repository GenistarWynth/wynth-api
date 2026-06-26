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
export type ApiResponse<T = unknown> = {
  success: boolean
  message?: string
  data?: T
}

export type AccountPoolPlatform = 'openai'

export type AccountPoolStatus = 'enabled' | 'disabled' | 'deleted'

export type AccountPoolAccountStatus =
  | 'enabled'
  | 'disabled'
  | 'expired'
  | 'deleted'

export type AccountPoolProxyStatus = 'enabled' | 'disabled' | 'deleted'

export type AccountPoolBindingStatus = 'draft' | 'enabled' | 'disabled'

export type AccountPoolCredentialType = 'api_key' | 'oauth' | string

export type AccountPoolProxyProtocol =
  | 'http'
  | 'https'
  | 'socks5'
  | 'socks5h'
  | string

export type AccountPoolModelStrategy = 'all' | 'fixed' | string

export type AccountPoolSchedulePolicy = 'round_robin' | 'random'

export type AccountPoolCapabilityMode =
  | 'auto'
  | 'models_endpoint'
  | 'probe_models'
  | string

export type AccountPool = {
  id: number
  name: string
  platform: AccountPoolPlatform | string
  status: AccountPoolStatus | string
  default_proxy_id: number
  default_monitor_enabled: boolean
  default_schedule_policy: string
  capability_check_enabled: boolean
  capability_check_interval_minutes: number
  capability_check_mode: AccountPoolCapabilityMode | ''
  capability_check_channel_id: number
  capability_check_models: string[]
  capability_check_timeout_seconds: number
  capability_check_merge: boolean
  remark: string
  created_time: number
  updated_time: number
}

export type AccountPoolCreateRequest = {
  name: string
  platform: AccountPoolPlatform | string
  default_proxy_id: number
  default_monitor_enabled: boolean
  default_schedule_policy: AccountPoolSchedulePolicy | ''
  capability_check_enabled: boolean
  capability_check_interval_minutes: number
  capability_check_mode: AccountPoolCapabilityMode | ''
  capability_check_channel_id: number
  capability_check_models: string[]
  capability_check_timeout_seconds: number
  capability_check_merge: boolean
  remark: string
}

export type AccountPoolUpdateRequest = AccountPoolCreateRequest

export type AccountPoolCredentialConfigRequest = {
  type: AccountPoolCredentialType
  api_key: string
  email: string
  refresh_token: string
}

export type AccountPoolTokenStateRequest = {
  access_token: string
  refresh_token: string
  expires_at: number
  version: number
}

export type AccountPoolAccount = {
  id: number
  pool_id: number
  name: string
  account_identifier: string
  status: AccountPoolAccountStatus | string
  priority: number
  weight: number
  max_concurrency: number
  request_quota: number
  request_quota_window_seconds: number
  expires_at: number
  auto_pause_on_expired: boolean
  proxy_id: number
  supported_models: string[]
  model_mapping: Record<string, string>
  last_used_at: number
  last_success_at: number
  last_failure_at: number
  success_count: number
  failure_count: number
  total_prompt_tokens: number
  total_completion_tokens: number
  total_cached_tokens: number
  total_cache_write_tokens: number
  last_prompt_tokens: number
  last_completion_tokens: number
  last_cached_tokens: number
  last_cache_write_tokens: number
  total_latency_ms: number
  latency_sample_count: number
  last_latency_ms: number
  total_first_token_latency_ms: number
  first_token_latency_sample_count: number
  last_first_token_latency_ms: number
  rate_limited_until: number
  temp_disabled_until: number
  temp_disabled_reason: string
  last_error: string
  last_capability_check_at: number
  last_capability_check_status: string
  last_capability_check_error: string
  last_capability_check_models: string[]
  has_credential: boolean
  has_token: boolean
  created_time: number
  updated_time: number
}

export type AccountPoolAccountCreateRequest = {
  name: string
  account_identifier: string
  credential: AccountPoolCredentialConfigRequest
  token_state: AccountPoolTokenStateRequest
  status: AccountPoolAccountStatus | string
  priority: number
  weight: number
  max_concurrency: number
  request_quota: number
  request_quota_window_seconds: number
  expires_at: number
  auto_pause_on_expired: boolean
  proxy_id: number
  supported_models: string[]
  model_mapping: Record<string, string>
  last_used_at: number
  rate_limited_until: number
  temp_disabled_until: number
  temp_disabled_reason: string
  last_error: string
}

export type AccountPoolAccountImportDefaultsRequest = {
  status: AccountPoolAccountStatus | string
  priority: number
  weight: number
  max_concurrency: number
  proxy_id: number
  supported_models: string[]
  model_mapping: Record<string, string>
}

export type AccountPoolAccountImportRequest = {
  format: 'sub2api' | 'cpa' | string
  content: string
  defaults: AccountPoolAccountImportDefaultsRequest
  dry_run: boolean
}

export type AccountPoolAccountImportError = {
  index?: number
  name?: string
  message: string
}

export type AccountPoolAccountImportResponse = {
  imported: number
  skipped: number
  failed: number
  proxy_created: number
  proxy_reused: number
  accounts?: AccountPoolAccount[]
  errors?: AccountPoolAccountImportError[]
}

export type AccountPoolCapabilityDetectRequest = {
  mode: AccountPoolCapabilityMode
  channel_id: number
  account_ids?: number[]
  candidate_models: string[]
  apply: boolean
  merge: boolean
  model_mapping: Record<string, string>
  timeout_seconds: number
}

export type AccountPoolCapabilityDetectResult = {
  account_id: number
  status: string
  mode: string
  detected_models: string[]
  applied_models: string[]
  model_mapping: Record<string, string>
  errors: string[]
}

export type AccountPoolCapabilityPoolResult = {
  total: number
  succeeded: number
  failed: number
  results: AccountPoolCapabilityDetectResult[]
}

export type AccountPoolBinding = {
  id: number
  pool_id: number
  channel_id: number
  channel_name: string
  channel_status: number
  account_filter_config: string
  model_policy: string
  schedule_policy: string
  account_retry_times: number
  max_user_concurrency: number
  status: AccountPoolBindingStatus | string
  runtime_enabled: boolean
  created_time: number
  updated_time: number
}

export type AccountPoolBindingCreateRequest = {
  channel_id: number
  account_ids: number[]
  model_strategy: AccountPoolModelStrategy
  fixed_models: string[]
  schedule_policy: AccountPoolSchedulePolicy
  account_retry_times: number
  max_user_concurrency: number
}

export type AccountPoolBoundChannelCreateRequest = {
  name: string
  type?: number
  account_ids?: number[]
  model_strategy?: AccountPoolModelStrategy
  fixed_models?: string[]
  schedule_policy?: AccountPoolSchedulePolicy | ''
  account_retry_times?: number
  max_user_concurrency?: number
}

export type AccountPoolProxy = {
  id: number
  name: string
  protocol: AccountPoolProxyProtocol
  host: string
  port: number
  username: string
  status: AccountPoolProxyStatus | string
  fallback_proxy_id: number
  has_password: boolean
  created_time: number
  updated_time: number
}

export type AccountPoolProxyCreateRequest = {
  name: string
  protocol: AccountPoolProxyProtocol
  host: string
  port: number
  username: string
  password: string
  status: AccountPoolProxyStatus | string
  fallback_proxy_id: number
}
