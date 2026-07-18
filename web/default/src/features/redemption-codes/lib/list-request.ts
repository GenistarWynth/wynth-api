import type {
  GetRedemptionsParams,
  GetRedemptionsResponse,
  SearchRedemptionsParams,
} from '../types'

type RedemptionListRequestInput = {
  keyword?: string
  status?: readonly string[]
  page: number
  pageSize: number
}

type RedemptionListRequest =
  | { mode: 'list'; params: GetRedemptionsParams }
  | { mode: 'search'; params: SearchRedemptionsParams }

type RedemptionListDependencies = {
  list: (params: GetRedemptionsParams) => Promise<GetRedemptionsResponse>
  search: (params: SearchRedemptionsParams) => Promise<GetRedemptionsResponse>
  onRejected: (error: unknown, mode: RedemptionListRequest['mode']) => void
}

export function resolveRedemptionListRequest(
  input: RedemptionListRequestInput
): RedemptionListRequest {
  const keyword = input.keyword?.trim() ?? ''
  const status = input.status?.[0] ?? ''
  const pagination = { p: input.page, page_size: input.pageSize }

  if (!keyword && !status) {
    return { mode: 'list', params: pagination }
  }

  return {
    mode: 'search',
    params: { keyword, status, ...pagination },
  }
}

export function serializeRedemptionListParams(
  params: GetRedemptionsParams | SearchRedemptionsParams
): string {
  const query = new URLSearchParams()
  if ('keyword' in params && params.keyword !== undefined) {
    query.set('keyword', params.keyword)
  }
  if ('status' in params && params.status !== undefined) {
    query.set('status', params.status)
  }
  query.set('p', String(params.p ?? 1))
  query.set('page_size', String(params.page_size ?? 10))
  return query.toString()
}

export async function loadRedemptionList(
  request: RedemptionListRequest,
  dependencies: RedemptionListDependencies
): Promise<GetRedemptionsResponse> {
  try {
    if (request.mode === 'search') {
      return await dependencies.search(request.params)
    }
    return await dependencies.list(request.params)
  } catch (error) {
    dependencies.onRejected(error, request.mode)
    throw error
  }
}
