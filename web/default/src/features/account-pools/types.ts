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

export type AccountPoolPlatform =
  | 'openai'
  | 'anthropic'
  | 'gemini'
  | 'xai'
  | 'grok_web'

export type AccountPoolStatus = 'enabled' | 'disabled' | 'deleted'

export type AccountPoolAccountStatus =
  | 'enabled'
  | 'disabled'
  | 'expired'
  | 'deleted'

export type AccountPoolProxyStatus = 'enabled' | 'disabled' | 'deleted'

export type AccountPoolBindingStatus = 'draft' | 'enabled' | 'disabled'

export type AccountPoolCredentialType =
  | 'api_key'
  | 'oauth'
  | 'grok_web_cookie'
  | string

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
  // Gemini OAuth sub-type ('' | 'code_assist' | 'ai_studio'); ignored by the
  // backend unless the platform is gemini and the credential type is oauth.
  oauth_type?: string
  api_key: string
  email: string
  refresh_token: string
  id_token?: string
  client_id?: string
  scope?: string
  token_type?: string
  sub?: string
  team_id?: string
  subscription_tier?: string
  entitlement_status?: string
  // grok.com web-cookie credential: optional Cloudflare clearance cookie sent
  // alongside the SSO token (carried in api_key). Mirrors the backend
  // dto.AccountPoolCredentialConfigRequest field; ignored by other credential types.
  cf_clearance?: string
  base_url?: string
  header_override_enabled?: boolean
  header_overrides?: Record<string, string>
}

export type AccountPoolTokenStateRequest = {
  access_token: string
  refresh_token: string
  expires_at: number
  version: number
}

export type AccountPoolXAIOAuthAuthorizationRequest = {
  proxy_id: number
  redirect_uri?: string
}

export type AccountPoolXAIOAuthAuthorization = {
  auth_url: string
  session_id: string
  state: string
}

export type AccountPoolXAIOAuthExchangeRequest = {
  session_id: string
  code: string
  state?: string
}

export type AccountPoolXAIOAuthTokenResult = {
  email?: string
  sub?: string
  team_id?: string
  subscription_tier?: string
  entitlement_status?: string
  expires_at: number
  credential: AccountPoolCredentialConfigRequest
  token_state: AccountPoolTokenStateRequest
}

export type AccountPoolXAIOAuthReconcileRequest = {
  dry_run?: boolean
  near_expiry_window_seconds?: number
}

export type AccountPoolXAIOAuthReconcileItem = {
  account_id: number
  name: string
  status: string
  reason: string
  action: string
  applied: boolean
  outcome: string
}

export type AccountPoolXAIOAuthReconcileResult = {
  pool_id: number
  dry_run: boolean
  near_expiry_window_seconds: number
  scanned: number
  candidates: number
  applied: number
  skipped: number
  items: AccountPoolXAIOAuthReconcileItem[]
}

export type AccountPoolXAIQuotaWindow = {
  limit?: number
  remaining?: number
  reset_unix?: number
  reset_at?: string
}

export type AccountPoolXAIBillingSnapshot = {
  usage_percent?: number
  monthly_limit_cents?: number
  used_cents?: number
  used_percent?: number
  plan?: string
  weekly_status_code?: number
  monthly_status_code?: number
  partial?: boolean
}

export type AccountPoolXAIQuotaSnapshot = {
  source: string
  model?: string
  billing?: AccountPoolXAIBillingSnapshot
  requests?: AccountPoolXAIQuotaWindow
  tokens?: AccountPoolXAIQuotaWindow
  retry_after_seconds?: number
  status_code?: number
  headers_observed: boolean
  media_eligible?: boolean
  media_eligibility_reason?: string
  fetched_at: number
  probe_error?: string
}

export type AccountPoolLocalQuotaResetRequest = {
  clear_cooldown: boolean
  reset_request_quota: boolean
  force_probe: boolean
}

export type AccountPoolLocalQuotaResetResponse = {
  account: AccountPoolAccount
  cooldown_cleared: boolean
  request_quota_reset: boolean
  probe?: AccountPoolXAIQuotaSnapshot
  probe_error?: string
  upstream_reset: boolean
}

export type AccountPoolAccount = {
  id: number
  pool_id: number
  name: string
  account_identifier: string
  credential_type?: AccountPoolCredentialType
  status: AccountPoolAccountStatus | string
  // Gemini OAuth sub-type ('' | 'code_assist' | 'ai_studio') exposed on the view.
  oauth_type: string
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
  xai_quota?: AccountPoolXAIQuotaSnapshot
  base_url?: string
  header_override_enabled?: boolean
  header_overrides?: Record<string, string>
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
