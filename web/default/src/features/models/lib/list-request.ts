import type { GetModelsParams, SearchModelsParams } from '../types'

type ModelListRequestInput = {
  keyword?: string
  vendor?: string
  status?: string
  syncOfficial?: string
  page: number
  pageSize: number
}

type ModelListRequest =
  | { mode: 'list'; params: GetModelsParams }
  | { mode: 'search'; params: SearchModelsParams }

export function resolveModelListRequest(
  input: ModelListRequestInput
): ModelListRequest {
  const keyword = input.keyword?.trim() || undefined
  const vendorValue = input.vendor?.trim()
  const statusValue = input.status?.trim()
  const syncValue = input.syncOfficial?.trim()
  const vendor = vendorValue && vendorValue !== 'all' ? vendorValue : undefined
  const status = statusValue && statusValue !== 'all' ? statusValue : undefined
  const syncOfficial = syncValue && syncValue !== 'all' ? syncValue : undefined
  const pagination = { p: input.page, page_size: input.pageSize }

  if (!keyword && !vendor && !status && !syncOfficial) {
    return { mode: 'list', params: pagination }
  }

  return {
    mode: 'search',
    params: {
      ...(keyword ? { keyword } : {}),
      ...(vendor ? { vendor } : {}),
      ...(status ? { status } : {}),
      ...(syncOfficial ? { sync_official: syncOfficial } : {}),
      ...pagination,
    },
  }
}
