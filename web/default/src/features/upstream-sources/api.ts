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
import { api, type ApiRequestConfig } from '@/lib/api'

import type {
  ApiResponse,
  UpstreamSource,
  UpstreamSourceCreateRequest,
  UpstreamSourceAutoPriorityResult,
  UpstreamSourceCredentialsUpdateRequest,
  UpstreamSourceDiscoveryResult,
  UpstreamSourceMapping,
  UpstreamSourceMonitoringOverview,
  UpstreamSourceMonitorScan,
  UpstreamSourceGroupChange,
  UpstreamSourceAnnouncement,
  UpstreamSourceBalanceSnapshot,
  UpstreamSourceSubscriptionSnapshot,
  UpstreamSourceRuleModelOptionsRequest,
  UpstreamSourceRuleModelOptionsResponse,
  UpstreamSourceSessionImportRequest,
  UpstreamSourceSyncResult,
  UpstreamSourceUpdateRequest,
} from './types'

const upstreamSourceActionConfig = (
  config: ApiRequestConfig = {}
): ApiRequestConfig => ({
  ...config,
  skipBusinessError: true,
  skipErrorHandler: true,
})

export const upstreamSourcesQueryKeys = {
  all: ['upstream-sources'] as const,
  list: () => [...upstreamSourcesQueryKeys.all, 'list'] as const,
  detail: (id: number) =>
    [...upstreamSourcesQueryKeys.all, 'detail', id] as const,
  mappings: (id: number) =>
    [...upstreamSourcesQueryKeys.all, 'mappings', id] as const,
  syncResult: (id: number) =>
    [...upstreamSourcesQueryKeys.all, 'sync-result', id] as const,
  monitoring: (id: number) =>
    [...upstreamSourcesQueryKeys.all, 'monitoring', id] as const,
}

export async function listUpstreamSources(): Promise<
  ApiResponse<UpstreamSource[]>
> {
  const res = await api.get('/api/upstream_sources')
  return res.data
}

export async function createUpstreamSource(
  data: UpstreamSourceCreateRequest
): Promise<ApiResponse<UpstreamSource>> {
  const res = await api.post(
    '/api/upstream_sources',
    data,
    upstreamSourceActionConfig()
  )
  return res.data
}

export async function updateUpstreamSource(
  id: number,
  data: UpstreamSourceUpdateRequest
): Promise<ApiResponse<UpstreamSource>> {
  const res = await api.put(
    `/api/upstream_sources/${id}`,
    data,
    upstreamSourceActionConfig()
  )
  return res.data
}

export async function updateUpstreamSourceCredentials(
  id: number,
  data: UpstreamSourceCredentialsUpdateRequest
): Promise<ApiResponse<UpstreamSource>> {
  const res = await api.put(
    `/api/upstream_sources/${id}/credentials`,
    data,
    upstreamSourceActionConfig()
  )
  return res.data
}

export async function importUpstreamSourceSession(
  id: number,
  data: UpstreamSourceSessionImportRequest
): Promise<ApiResponse<UpstreamSource>> {
  const res = await api.post(
    `/api/upstream_sources/${id}/session`,
    data,
    upstreamSourceActionConfig()
  )
  return res.data
}

export async function clearUpstreamSourceSession(
  id: number
): Promise<ApiResponse<UpstreamSource>> {
  const res = await api.delete(
    `/api/upstream_sources/${id}/session`,
    upstreamSourceActionConfig()
  )
  return res.data
}

export async function updateUpstreamSourceMonitor(
  id: number,
  enabled: boolean,
  intervalMinutes: number
): Promise<ApiResponse<UpstreamSource>> {
  const res = await api.put(
    `/api/upstream_sources/${id}/monitor`,
    { enabled, interval_minutes: intervalMinutes },
    upstreamSourceActionConfig()
  )
  return res.data
}

export async function getUpstreamSourceMonitoringOverview(
  id: number
): Promise<ApiResponse<UpstreamSourceMonitoringOverview>> {
  const [snapshots, scans, changes, announcements] = await Promise.all([
    api.get<
      ApiResponse<{
        balance?: UpstreamSourceBalanceSnapshot | null
        subscription_usage: UpstreamSourceSubscriptionSnapshot[]
      }>
    >(`/api/upstream_sources/${id}/monitor/snapshots`),
    api.get<ApiResponse<UpstreamSourceMonitorScan[]>>(
      `/api/upstream_sources/${id}/monitor/runs?limit=10`
    ),
    api.get<ApiResponse<UpstreamSourceGroupChange[]>>(
      `/api/upstream_sources/${id}/changes?limit=10`
    ),
    api.get<ApiResponse<UpstreamSourceAnnouncement[]>>(
      `/api/upstream_sources/${id}/announcements?limit=10`
    ),
  ])
  const responses = [
    snapshots.data,
    scans.data,
    changes.data,
    announcements.data,
  ]
  const failed = responses.find((response) => !response.success)
  if (failed) {
    return { success: false, message: failed.message }
  }
  return {
    success: true,
    data: {
      balance: snapshots.data.data?.balance,
      subscription_usage: snapshots.data.data?.subscription_usage ?? [],
      scans: scans.data.data ?? [],
      changes: changes.data.data ?? [],
      announcements: announcements.data.data ?? [],
    },
  }
}

export async function deleteUpstreamSource(
  id: number
): Promise<ApiResponse<null>> {
  const res = await api.delete(
    `/api/upstream_sources/${id}`,
    upstreamSourceActionConfig()
  )
  return res.data
}

export async function discoverUpstreamSource(
  id: number
): Promise<ApiResponse<UpstreamSourceDiscoveryResult>> {
  const res = await api.post(
    `/api/upstream_sources/${id}/discover`,
    undefined,
    upstreamSourceActionConfig()
  )
  return res.data
}

export async function listUpstreamSourceMappings(
  id: number
): Promise<ApiResponse<UpstreamSourceMapping[]>> {
  const res = await api.get(`/api/upstream_sources/${id}/mappings`)
  return res.data
}

export async function updateUpstreamSourceMappings(
  id: number,
  mappingIDs: number[]
): Promise<ApiResponse<UpstreamSourceMapping[]>> {
  const res = await api.put(
    `/api/upstream_sources/${id}/mappings`,
    { mapping_ids: mappingIDs },
    upstreamSourceActionConfig()
  )
  return res.data
}

export async function syncUpstreamSource(
  id: number
): Promise<ApiResponse<UpstreamSourceSyncResult>> {
  const res = await api.post(
    `/api/upstream_sources/${id}/sync`,
    undefined,
    upstreamSourceActionConfig()
  )
  return res.data
}

export async function runUpstreamSourceAutoPriority(
  id: number
): Promise<ApiResponse<UpstreamSourceAutoPriorityResult>> {
  const res = await api.post(
    `/api/upstream_sources/${id}/auto_priority/run`,
    undefined,
    upstreamSourceActionConfig()
  )
  return res.data
}

export async function getUpstreamSourceRuleModelOptions(
  id: number,
  data: UpstreamSourceRuleModelOptionsRequest
): Promise<ApiResponse<UpstreamSourceRuleModelOptionsResponse>> {
  const res = await api.post(
    `/api/upstream_sources/${id}/rule_model_options`,
    data,
    upstreamSourceActionConfig()
  )
  return res.data
}
