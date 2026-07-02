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
import { api } from '@/lib/api'
import type { PermissionCatalog } from '@/lib/admin-permissions'
import type {
  User,
  GetUsersParams,
  GetUsersResponse,
  SearchUsersParams,
  UserFormData,
  ManageUserAction,
  ManageUserQuotaPayload,
  ApiResponse,
} from './types'
import type {
  ApiKey,
  ApiKeyFormData,
  GetApiKeysResponse,
} from '@/features/keys/types'

// ============================================================================
// User Management APIs
// ============================================================================

/**
 * Get paginated users list
 */
export async function getUsers(
  params: GetUsersParams = {}
): Promise<GetUsersResponse> {
  const { p = 1, page_size = 10 } = params
  const res = await api.get(`/api/user/?p=${p}&page_size=${page_size}`)
  return res.data
}

/**
 * Search users by keyword or group
 */
export async function searchUsers(
  params: SearchUsersParams
): Promise<GetUsersResponse> {
  const {
    keyword = '',
    group = '',
    role = '',
    status = '',
    p = 1,
    page_size = 10,
  } = params
  const queryParams = new URLSearchParams()
  queryParams.set('keyword', keyword)
  queryParams.set('group', group)
  if (role) queryParams.set('role', role)
  if (status) queryParams.set('status', status)
  queryParams.set('p', String(p))
  queryParams.set('page_size', String(page_size))
  const res = await api.get(`/api/user/search?${queryParams.toString()}`)
  return res.data
}

/**
 * Get single user by ID
 */
export async function getUser(id: number): Promise<ApiResponse<User>> {
  const res = await api.get(`/api/user/${id}`)
  return res.data
}

/**
 * Create a new user
 */
export async function createUser(
  data: UserFormData
): Promise<ApiResponse<User>> {
  const res = await api.post('/api/user/', data)
  return res.data
}

/**
 * Update an existing user
 */
export async function updateUser(
  data: UserFormData & { id: number }
): Promise<ApiResponse<Partial<User>>> {
  const res = await api.put('/api/user/', data)
  return res.data
}

/**
 * Delete a single user (hard delete)
 */
export async function deleteUser(id: number): Promise<ApiResponse> {
  const res = await api.delete(`/api/user/${id}/`)
  return res.data
}

/**
 * Manage user (promote, demote, enable, disable, delete)
 */
export async function manageUser(
  id: number,
  action: ManageUserAction
): Promise<ApiResponse<Partial<User>>> {
  const res = await api.post('/api/user/manage', { id, action })
  return res.data
}

/**
 * Adjust user quota atomically (add/subtract/override)
 */
export async function adjustUserQuota(
  payload: ManageUserQuotaPayload
): Promise<ApiResponse<Partial<User>>> {
  const res = await api.post('/api/user/manage', payload)
  return res.data
}

/**
 * Reset user's Passkey registration
 */
export async function resetUserPasskey(id: number): Promise<ApiResponse> {
  const res = await api.delete(`/api/user/${id}/reset_passkey`)
  return res.data
}

/**
 * Reset user's Two-Factor Authentication setup
 */
export async function resetUserTwoFA(id: number): Promise<ApiResponse> {
  const res = await api.delete(`/api/user/${id}/2fa`)
  return res.data
}

/**
 * Get all available groups
 */
export async function getGroups(): Promise<ApiResponse<string[]>> {
  const res = await api.get('/api/group/')
  return res.data
}

/**
 * Get the permission catalog (resources, actions, and role baselines).
 * Source of truth lives in the backend authz package.
 */
export async function getPermissionCatalog(): Promise<PermissionCatalog> {
  const res = await api.get('/api/authz/catalog')
  return {
    resources: res.data?.data?.resources ?? [],
    roles: res.data?.data?.roles ?? [],
  }
}

// ============================================================================
// Admin Binding Management APIs
// ============================================================================

export interface OAuthBinding {
  provider_id: string
  provider_name: string
  user_id?: number
  external_id?: string
}

/**
 * Get user's custom OAuth bindings (admin)
 */
export async function getUserOAuthBindings(
  userId: number
): Promise<ApiResponse<OAuthBinding[]>> {
  const res = await api.get(`/api/user/${userId}/oauth/bindings`)
  return res.data
}

/**
 * Clear a user's built-in binding (admin)
 */
export async function adminClearUserBinding(
  userId: number,
  bindingType: string
): Promise<ApiResponse> {
  const res = await api.delete(`/api/user/${userId}/bindings/${bindingType}`)
  return res.data
}

/**
 * Unbind custom OAuth for a user (admin)
 */
export async function adminUnbindCustomOAuth(
  userId: number,
  providerId: string
): Promise<ApiResponse> {
  const res = await api.delete(
    `/api/user/${userId}/oauth/bindings/${providerId}`
  )
  return res.data
}

// ============================================================================
// Admin: manage another user's API tokens
// ============================================================================

export async function getUserTokens(
  userId: number,
  params: { p?: number; size?: number } = {}
): Promise<GetApiKeysResponse> {
  const { p = 1, size = 10 } = params
  const res = await api.get(`/api/user/${userId}/tokens?p=${p}&size=${size}`)
  return res.data
}

export async function searchUserTokens(
  userId: number,
  params: { keyword?: string; token?: string; p?: number; size?: number }
): Promise<GetApiKeysResponse> {
  const { keyword = '', token = '', p, size } = params
  const q = new URLSearchParams()
  if (keyword) q.set('keyword', keyword)
  if (token) q.set('token', token)
  if (p != null) q.set('p', String(p))
  if (size != null) q.set('size', String(size))
  const res = await api.get(`/api/user/${userId}/tokens/search?${q.toString()}`)
  return res.data
}

export async function createUserToken(
  userId: number,
  data: ApiKeyFormData
): Promise<ApiResponse<ApiKey>> {
  const res = await api.post(`/api/user/${userId}/tokens`, data)
  return res.data
}

export async function updateUserToken(
  userId: number,
  data: ApiKeyFormData & { id: number }
): Promise<ApiResponse<ApiKey>> {
  const res = await api.put(`/api/user/${userId}/tokens`, data)
  return res.data
}

export async function updateUserTokenStatus(
  userId: number,
  id: number,
  status: number
): Promise<ApiResponse<ApiKey>> {
  const res = await api.put(`/api/user/${userId}/tokens?status_only=true`, { id, status })
  return res.data
}

export async function deleteUserToken(
  userId: number,
  id: number
): Promise<ApiResponse> {
  const res = await api.delete(`/api/user/${userId}/tokens/${id}`)
  return res.data
}

export async function batchDeleteUserTokens(
  userId: number,
  ids: number[]
): Promise<ApiResponse<number>> {
  const res = await api.post(`/api/user/${userId}/tokens/batch`, { ids })
  return res.data
}

// Root-only: reveal the full plaintext key (requires prior step-up verification).
export async function fetchUserTokenKey(
  userId: number,
  id: number
): Promise<{ success: boolean; message?: string; code?: string; data?: { key: string } }> {
  const res = await api.post(`/api/user/${userId}/tokens/${id}/key`)
  return res.data
}

// ============================================================================
// Admin: runtime per-token channel override (force a token onto a same-group channel)
// ============================================================================

export interface TokenChannelOverride {
  active: boolean
  channel_id: number
  set_by_user_id: number
  created_at: number
}

// These handlers own their own success/error toasts, so opt out of the global
// business-error and HTTP-error toasts to avoid double notifications.
const forceChannelActionConfig = {
  skipBusinessError: true,
  skipErrorHandler: true,
} as const

// Current override for a token (active=false when none is in effect).
export async function getUserTokenForceChannel(
  userId: number,
  tokenId: number
): Promise<ApiResponse<TokenChannelOverride>> {
  const res = await api.get(
    `/api/user/${userId}/tokens/${tokenId}/force-channel`,
    forceChannelActionConfig
  )
  return res.data
}

// Force the token onto a channel. ttl_seconds is optional (server default 30m, max 24h).
export async function forceUserTokenChannel(
  userId: number,
  tokenId: number,
  data: { channel_id: number; ttl_seconds?: number }
): Promise<ApiResponse<{ token_id: number; channel_id: number }>> {
  const res = await api.post(
    `/api/user/${userId}/tokens/${tokenId}/force-channel`,
    data,
    forceChannelActionConfig
  )
  return res.data
}

// Remove any override, restoring normal channel selection immediately.
export async function clearUserTokenForceChannel(
  userId: number,
  tokenId: number
): Promise<ApiResponse> {
  const res = await api.delete(
    `/api/user/${userId}/tokens/${tokenId}/force-channel`,
    forceChannelActionConfig
  )
  return res.data
}
