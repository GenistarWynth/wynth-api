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

export const UPSTREAM_SOURCE_TYPE_SUB2API = 'sub2api' as const
export const UPSTREAM_SOURCE_TYPE_NEW_API = 'new-api' as const

export type UpstreamSourceType =
  | typeof UPSTREAM_SOURCE_TYPE_SUB2API
  | typeof UPSTREAM_SOURCE_TYPE_NEW_API

export type UpstreamSourceStatus = 'enabled' | 'disabled' | 'deleted'

export type UpstreamDiscoveryStatus = 'never_run' | 'succeeded' | 'failed'

export type UpstreamSyncStatus =
  | 'never_run'
  | 'running'
  | 'succeeded'
  | 'failed'

export type UpstreamMappingDiscoveryStatus = 'active' | 'stale' | 'invalid'

export type UpstreamMappingSyncStatus =
  | 'never_synced'
  | 'synced'
  | 'failed'
  | 'skipped'
  | 'needs_attention'

export type UpstreamSourceModelStrategy = 'all_upstream' | 'fixed'

export type CodexImageGenerationBridgePolicy = 'follow' | 'enabled' | 'disabled'

export type UpstreamSourceRuleMonitor = {
  enabled?: boolean
  interval_minutes?: number
  model?: string
}

export type UpstreamSourceRuleAutoSync = {
  enabled?: boolean
  interval_minutes?: number
}

export type UpstreamSourceRuleAutoPriority = {
  enabled?: boolean
  window_hours?: number
  /** Legacy input only; rule normalization deliberately omits this field. */
  availability_window_hours?: number
}

export type UpstreamSource = {
  id: number
  name: string
  type: UpstreamSourceType
  status: UpstreamSourceStatus
  base_url: string
  admin_api_base_path: string
  relay_base_url: string
  local_group: string
  allow_private_ip: boolean
  local_group_rules: UpstreamSourceLocalGroupRule[]
  masked_email: string
  has_credentials: boolean
  session_source?: string
  turnstile_blocked?: boolean
  auth_status: string
  auth_last_validated_at: number
  auth_last_refreshed_at: number
  auth_expires_at: number
  last_auth_error: string
  monitor_enabled: boolean
  monitor_interval_minutes: number
  next_monitor_at: number
  last_monitor_time: number
  last_discovery_time: number
  last_discovery_status: UpstreamDiscoveryStatus | ''
  last_discovery_error: string
  last_sync_time: number
  last_sync_status: UpstreamSyncStatus | ''
  last_sync_error: string
  created_time: number
  updated_time: number
}

export type UpstreamSourceMonitorScan = {
  id: number
  source_id: number
  scan_type: string
  status: string
  baseline: boolean
  started_at: number
  finished_at: number
  error_summary?: string
}

export type UpstreamSourceGroupChange = {
  id: number
  source_id: number
  scan_id: number
  change_type: 'added' | 'removed' | 'restored' | 'rate_changed'
  upstream_group_id: string
  upstream_group_name: string
  old_effective_rate_multiplier?: number | null
  new_effective_rate_multiplier?: number | null
  created_at: number
}

export type UpstreamSourceAnnouncement = {
  id: number
  source_id: number
  scan_id: number
  source_key: string
  title: string
  content: string
  url: string
  published_at: number
  first_seen_at: number
  last_seen_at: number
  is_new: boolean
}

export type UpstreamSourceBalanceSnapshot = {
  source_id: number
  scan_id: number
  available: number
  currency: string
  collected_at: number
}

export type UpstreamSourceSubscriptionSnapshot = {
  id: number
  source_id: number
  scan_id: number
  subscription_key: string
  name: string
  window: string
  unit: string
  used: number
  limit?: number | null
  remaining?: number | null
  remaining_percent?: number | null
  period_start: number
  period_end: number
  expires_at: number
  collected_at: number
}

export type UpstreamSourceMonitoringOverview = {
  balance?: UpstreamSourceBalanceSnapshot | null
  subscription_usage: UpstreamSourceSubscriptionSnapshot[]
  scans: UpstreamSourceMonitorScan[]
  changes: UpstreamSourceGroupChange[]
  announcements: UpstreamSourceAnnouncement[]
}

export type UpstreamSourceLocalGroupRule = {
  name: string
  local_group: string
  platforms: string[]
  name_contains: string[]
  description_contains: string[]
  exclude_keywords: string[]
  channel_type?: number
  priority?: number
  weight?: number
  monitor?: UpstreamSourceRuleMonitor
  auto_sync?: UpstreamSourceRuleAutoSync
  auto_priority?: UpstreamSourceRuleAutoPriority
  codex_image_generation_bridge_policy?: CodexImageGenerationBridgePolicy
  model_strategy: UpstreamSourceModelStrategy
  fixed_models: string[]
}

export type UpstreamSourceMapping = {
  id: number
  source_id: number
  sync_enabled: boolean
  upstream_group_id: string
  upstream_group_name: string
  upstream_group_description: string
  upstream_platform: string
  discovery_status: UpstreamMappingDiscoveryStatus | ''
  upstream_status: string
  upstream_rate_multiplier?: number | null
  effective_rate_multiplier?: number | null
  upstream_key_id: string
  has_upstream_key: boolean
  local_channel_id: number
  sync_status: UpstreamMappingSyncStatus | ''
  sync_eligible: boolean
  matched_rule_name: string
  match_reason: string
  resolved_local_group: string
  resolved_channel_type?: number
  resolved_priority?: number
  resolved_weight?: number
  resolved_monitor_enabled: boolean
  resolved_monitor_interval_minutes: number
  resolved_monitor_model?: string
  resolved_auto_sync_enabled: boolean
  resolved_auto_sync_interval_minutes: number
  resolved_auto_priority_enabled: boolean
  resolved_auto_priority_window_hours: number
  resolved_auto_priority_availability_window_hours: number
  resolved_codex_image_generation_bridge_policy: CodexImageGenerationBridgePolicy
  resolved_model_strategy: UpstreamSourceModelStrategy
  resolved_fixed_models: string[]
  last_error: string
  last_discovered_at: number
  last_synced_at: number
}

export type UpstreamSourceFormValues = {
  name: string
  type: UpstreamSourceType
  status: Exclude<UpstreamSourceStatus, 'deleted'>
  base_url: string
  admin_api_base_path: string
  relay_base_url: string
  email: string
  password: string
  local_group: string
  allow_private_ip: boolean
  local_group_rules: UpstreamSourceLocalGroupRule[]
}

export type UpstreamSourceCreateRequest = Omit<
  UpstreamSourceFormValues,
  'status'
>

export type UpstreamSourceUpdateRequest = Omit<
  UpstreamSourceFormValues,
  'email' | 'password'
>

export type UpstreamSourceCredentialsUpdateRequest = {
  email: string
  password: string
}

export interface UpstreamSourceSessionImportRequest {
  session_cookie?: string
  access_token?: string
  user_id?: number
  refresh_token?: string
  expires_at?: number
}

export type UpstreamSourceDiscoveryResult = {
  source_id: number
  discovered: number
  active: number
  stale: number
  invalid: number
  mappings: UpstreamSourceMapping[]
  error?: string
}

export type UpstreamSourceMappingSyncResult = {
  mapping_id: number
  upstream_group_id: string
  local_channel_id: number
  status: UpstreamMappingSyncStatus | string
  error?: string
  created: boolean
  updated: boolean
}

export type UpstreamSourceSyncResult = {
  source_id: number
  status: UpstreamSyncStatus | string
  created: number
  updated: number
  skipped: number
  failed: number
  results: UpstreamSourceMappingSyncResult[]
  error?: string
}

export type UpstreamSourceAutoPriorityChannelResult = {
  mapping_id: number
  local_channel_id: number
  old_priority: number
  new_priority: number
  computed_priority: number
  applied: boolean
  reason?: string
  effective_rate_multiplier: number
  nominal_rate_multiplier: number
  cache_adjusted_cost_factor: number
  effective_cost_multiplier: number
  effective_price_score: number
  nominal_price_score: number
  cache_score: number
  availability_score: number
  first_token_score: number
  throughput_score: number
  final_score: number
}

export type UpstreamSourceAutoPriorityResult = {
  source_id: number
  updated: number
  skipped: number
  failed: number
  results: UpstreamSourceAutoPriorityChannelResult[]
  error?: string
}

export type UpstreamSourceRuleModelOptionsMatchedMapping = {
  mapping_id: number
  upstream_group_id: string
  upstream_group_name: string
  upstream_platform: string
  local_channel_id: number
}

export type UpstreamSourceRuleModelOptionsRequest = {
  local_group_rules: UpstreamSourceLocalGroupRule[]
  rule_index: number
}

export type UpstreamSourceRuleModelOptionsResponse = {
  models: string[]
  matched_mappings: UpstreamSourceRuleModelOptionsMatchedMapping[]
}
