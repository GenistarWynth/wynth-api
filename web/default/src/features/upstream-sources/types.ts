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

export type UpstreamSourceType = typeof UPSTREAM_SOURCE_TYPE_SUB2API

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

export type UpstreamSource = {
  id: number
  name: string
  type: UpstreamSourceType
  status: UpstreamSourceStatus
  base_url: string
  admin_api_base_path: string
  relay_base_url: string
  local_group: string
  channel_type: number
  default_priority: number
  default_weight: number
  enable_monitor: boolean
  monitor_interval_minutes: number
  auto_sync_models: boolean
  allow_private_ip: boolean
  auto_sync_enabled: boolean
  auto_sync_interval_minutes: number
  default_local_group: string
  local_group_rules: UpstreamSourceLocalGroupRule[]
  masked_email: string
  has_credentials: boolean
  last_discovery_time: number
  last_discovery_status: UpstreamDiscoveryStatus | ''
  last_discovery_error: string
  last_sync_time: number
  last_sync_status: UpstreamSyncStatus | ''
  last_sync_error: string
  created_time: number
  updated_time: number
}

export type UpstreamSourceLocalGroupRule = {
  name: string
  local_group: string
  name_contains: string[]
  description_contains: string[]
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
  channel_type: number
  default_priority: number
  default_weight: number
  enable_monitor: boolean
  monitor_interval_minutes: number
  auto_sync_models: boolean
  allow_private_ip: boolean
  auto_sync_enabled: boolean
  auto_sync_interval_minutes: number
  default_local_group: string
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
