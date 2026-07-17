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
import { useMemo } from 'react'
import { AxiosError } from 'axios'
import { useQuery } from '@tanstack/react-query'
import { getRouteApi } from '@tanstack/react-router'
import { useMediaQuery } from '@/hooks'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { useTableUrlState } from '@/hooks/use-table-url-state'
import {
  DISABLED_ROW_DESKTOP,
  DISABLED_ROW_MOBILE,
  DataTablePage,
  useDataTable,
} from '@/components/data-table'
import { getRedemptions, searchRedemptions } from '../api'
import {
  ERROR_MESSAGES,
  REDEMPTION_STATUS,
  getRedemptionStatusOptions,
} from '../constants'
import { isRedemptionExpired } from '../lib'
import {
  loadRedemptionList,
  resolveRedemptionListRequest,
} from '../lib/list-request'
import type { Redemption } from '../types'
import { DataTableBulkActions } from './data-table-bulk-actions'
import { useRedemptionsColumns } from './redemptions-columns'
import { useRedemptions } from './redemptions-provider'

const route = getRouteApi('/_authenticated/redemption-codes/')

function isDisabledRedemptionRow(redemption: Redemption) {
  return (
    redemption.status !== REDEMPTION_STATUS.ENABLED ||
    isRedemptionExpired(redemption.expired_time, redemption.status)
  )
}

export function RedemptionsTable() {
  const { t } = useTranslation()
  const columns = useRedemptionsColumns()
  const { refreshTrigger } = useRedemptions()
  const isMobile = useMediaQuery('(max-width: 640px)')

  const {
    globalFilter,
    onGlobalFilterChange,
    columnFilters,
    onColumnFiltersChange,
    pagination,
    onPaginationChange,
    ensurePageInRange,
  } = useTableUrlState({
    search: route.useSearch(),
    navigate: route.useNavigate(),
    pagination: { defaultPage: 1, defaultPageSize: isMobile ? 10 : 20 },
    globalFilter: { enabled: true, key: 'filter' },
    columnFilters: [{ columnId: 'status', searchKey: 'status', type: 'array' }],
  })

  const statusFilter =
    (columnFilters.find((filter) => filter.id === 'status')?.value as
      | string[]
      | undefined) ?? []
  const listRequest = resolveRedemptionListRequest({
    keyword: globalFilter,
    status: statusFilter,
    page: pagination.pageIndex + 1,
    pageSize: pagination.pageSize,
  })

  // Fetch data with React Query
  const { data, isLoading, isFetching } = useQuery({
    queryKey: [
      'redemptions',
      listRequest.mode,
      listRequest.params,
      refreshTrigger,
    ],
    queryFn: async () => {
      const result = await loadRedemptionList(listRequest, {
        list: getRedemptions,
        search: searchRedemptions,
        onRejected: (error, mode) => {
          const status =
            error instanceof AxiosError ? error.response?.status : undefined
          if (status === 401 || status === 500) return

          const messageKey =
            mode === 'search'
              ? ERROR_MESSAGES.SEARCH_FAILED
              : ERROR_MESSAGES.LOAD_FAILED
          toast.error(t(messageKey))
        },
      })

      if (!result.success) {
        const messageKey =
          listRequest.mode === 'search'
            ? ERROR_MESSAGES.SEARCH_FAILED
            : ERROR_MESSAGES.LOAD_FAILED
        toast.error(result.message || t(messageKey))
        return { items: [], total: 0 }
      }

      return {
        items: result.data?.items || [],
        total: result.data?.total || 0,
      }
    },
    placeholderData: (previousData) => previousData,
  })

  const redemptions = data?.items || []

  const { table } = useDataTable({
    data: redemptions,
    columns,
    enableRowSelection: true,
    columnFilters,
    globalFilter,
    pagination,
    onPaginationChange,
    onGlobalFilterChange,
    onColumnFiltersChange,
    manualPagination: true,
    manualFiltering: true,
    totalCount: data?.total || 0,
    ensurePageInRange,
  })

  const redemptionStatusOptions = useMemo(
    () => getRedemptionStatusOptions(t),
    [t]
  )

  return (
    <DataTablePage
      table={table}
      columns={columns}
      isLoading={isLoading}
      isFetching={isFetching}
      emptyTitle={t('No Redemption Codes Found')}
      emptyDescription={t(
        'No redemption codes available. Create your first redemption code to get started.'
      )}
      skeletonKeyPrefix='redemptions-skeleton'
      applyHeaderSize
      toolbarProps={{
        searchPlaceholder: t('Filter by name or ID...'),
        filters: [
          {
            columnId: 'status',
            title: t('Status'),
            options: redemptionStatusOptions,
            singleSelect: true,
          },
        ],
      }}
      getRowClassName={(row, { isMobile }) => {
        if (!isDisabledRedemptionRow(row.original)) return undefined
        return isMobile ? DISABLED_ROW_MOBILE : DISABLED_ROW_DESKTOP
      }}
      bulkActions={<DataTableBulkActions table={table} />}
    />
  )
}
