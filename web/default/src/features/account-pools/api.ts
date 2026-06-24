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
  AccountPool,
  AccountPoolAccount,
  AccountPoolAccountCreateRequest,
  AccountPoolAccountImportRequest,
  AccountPoolAccountImportResponse,
  AccountPoolBinding,
  AccountPoolBindingCreateRequest,
  AccountPoolCreateRequest,
  AccountPoolProxy,
  AccountPoolProxyCreateRequest,
  AccountPoolUpdateRequest,
  ApiResponse,
} from './types'

const accountPoolActionConfig = (
  config: ApiRequestConfig = {}
): ApiRequestConfig => ({
  ...config,
  skipBusinessError: true,
  skipErrorHandler: true,
})

export const accountPoolsQueryKeys = {
  all: ['account-pools'] as const,
  list: () => [...accountPoolsQueryKeys.all, 'list'] as const,
  accounts: (id: number) =>
    [...accountPoolsQueryKeys.all, 'accounts', id] as const,
  bindings: (id: number) =>
    [...accountPoolsQueryKeys.all, 'bindings', id] as const,
  proxies: () => [...accountPoolsQueryKeys.all, 'proxies'] as const,
}

export async function listAccountPools(): Promise<ApiResponse<AccountPool[]>> {
  const res = await api.get('/api/account_pools')
  return res.data
}

export async function createAccountPool(
  data: AccountPoolCreateRequest
): Promise<ApiResponse<AccountPool>> {
  const res = await api.post(
    '/api/account_pools',
    data,
    accountPoolActionConfig()
  )
  return res.data
}

export async function updateAccountPool(
  id: number,
  data: AccountPoolUpdateRequest
): Promise<ApiResponse<AccountPool>> {
  const res = await api.put(
    `/api/account_pools/${id}`,
    data,
    accountPoolActionConfig()
  )
  return res.data
}

export async function deleteAccountPool(
  id: number
): Promise<ApiResponse<null>> {
  const res = await api.delete(
    `/api/account_pools/${id}`,
    accountPoolActionConfig()
  )
  return res.data
}

export async function listAccountPoolAccounts(
  id: number
): Promise<ApiResponse<AccountPoolAccount[]>> {
  const res = await api.get(`/api/account_pools/${id}/accounts`)
  return res.data
}

export async function createAccountPoolAccount(
  id: number,
  data: AccountPoolAccountCreateRequest
): Promise<ApiResponse<AccountPoolAccount>> {
  const res = await api.post(
    `/api/account_pools/${id}/accounts`,
    data,
    accountPoolActionConfig()
  )
  return res.data
}

export async function updateAccountPoolAccount(
  poolID: number,
  accountID: number,
  data: AccountPoolAccountCreateRequest
): Promise<ApiResponse<AccountPoolAccount>> {
  const res = await api.put(
    `/api/account_pools/${poolID}/accounts/${accountID}`,
    data,
    accountPoolActionConfig()
  )
  return res.data
}

export async function deleteAccountPoolAccount(
  poolID: number,
  accountID: number
): Promise<ApiResponse<null>> {
  const res = await api.delete(
    `/api/account_pools/${poolID}/accounts/${accountID}`,
    accountPoolActionConfig()
  )
  return res.data
}

export async function importAccountPoolAccounts(
  id: number,
  data: AccountPoolAccountImportRequest
): Promise<ApiResponse<AccountPoolAccountImportResponse>> {
  const res = await api.post(
    `/api/account_pools/${id}/accounts/import`,
    data,
    accountPoolActionConfig()
  )
  return res.data
}

export async function listAccountPoolBindings(
  id: number
): Promise<ApiResponse<AccountPoolBinding[]>> {
  const res = await api.get(`/api/account_pools/${id}/bindings`)
  return res.data
}

export async function createAccountPoolBinding(
  id: number,
  data: AccountPoolBindingCreateRequest
): Promise<ApiResponse<AccountPoolBinding>> {
  const res = await api.post(
    `/api/account_pools/${id}/bindings`,
    data,
    accountPoolActionConfig()
  )
  return res.data
}

export async function updateAccountPoolBinding(
  poolID: number,
  bindingID: number,
  data: AccountPoolBindingCreateRequest
): Promise<ApiResponse<AccountPoolBinding>> {
  const res = await api.put(
    `/api/account_pools/${poolID}/bindings/${bindingID}`,
    data,
    accountPoolActionConfig()
  )
  return res.data
}

export async function activateAccountPoolBinding(
  poolID: number,
  bindingID: number
): Promise<ApiResponse<AccountPoolBinding>> {
  const res = await api.post(
    `/api/account_pools/${poolID}/bindings/${bindingID}/activate`,
    null,
    accountPoolActionConfig()
  )
  return res.data
}

export async function disableAccountPoolBinding(
  poolID: number,
  bindingID: number
): Promise<ApiResponse<AccountPoolBinding>> {
  const res = await api.post(
    `/api/account_pools/${poolID}/bindings/${bindingID}/disable`,
    null,
    accountPoolActionConfig()
  )
  return res.data
}

export async function listAccountPoolProxies(): Promise<
  ApiResponse<AccountPoolProxy[]>
> {
  const res = await api.get('/api/account_pools/proxies')
  return res.data
}

export async function createAccountPoolProxy(
  data: AccountPoolProxyCreateRequest
): Promise<ApiResponse<AccountPoolProxy>> {
  const res = await api.post(
    '/api/account_pools/proxies',
    data,
    accountPoolActionConfig()
  )
  return res.data
}

export async function updateAccountPoolProxy(
  id: number,
  data: AccountPoolProxyCreateRequest
): Promise<ApiResponse<AccountPoolProxy>> {
  const res = await api.put(
    `/api/account_pools/proxies/${id}`,
    data,
    accountPoolActionConfig()
  )
  return res.data
}

export async function deleteAccountPoolProxy(
  id: number
): Promise<ApiResponse<null>> {
  const res = await api.delete(
    `/api/account_pools/proxies/${id}`,
    accountPoolActionConfig()
  )
  return res.data
}
