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
import { useEffect, useMemo, useState, type FormEvent } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  type ColumnDef,
  type ColumnFiltersState,
  type PaginationState,
  type Row,
} from '@tanstack/react-table'
import {
  Eye,
  Link2,
  Loader2,
  MoreHorizontal,
  Plus,
  Save,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { useMediaQuery } from '@/hooks'
import { formatTimestamp } from '@/lib/format'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import {
  Field,
  FieldDescription,
  FieldGroup,
  FieldLabel,
  FieldLegend,
  FieldSet,
} from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import {
  Sheet,
  SheetClose,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { Switch } from '@/components/ui/switch'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Textarea } from '@/components/ui/textarea'
import {
  DISABLED_ROW_DESKTOP,
  DISABLED_ROW_MOBILE,
  DataTablePage,
  useDataTable,
} from '@/components/data-table'
import {
  SideDrawerSection,
  SideDrawerSectionHeader,
  sideDrawerContentClassName,
  sideDrawerFooterClassName,
  sideDrawerFormClassName,
  sideDrawerHeaderClassName,
  sideDrawerSwitchItemClassName,
} from '@/components/drawer-layout'
import { SectionPageLayout } from '@/components/layout'
import { LongText } from '@/components/long-text'
import { StatusBadge, type StatusVariant } from '@/components/status-badge'
import { TableId } from '@/components/table-id'
import { getChannels } from '@/features/channels/api'
import {
  CHANNEL_STATUS_CONFIG,
  CHANNEL_STATUS_LABELS,
} from '@/features/channels/constants'
import type { Channel } from '@/features/channels/types'
import {
  accountPoolsQueryKeys,
  createAccountPool,
  createAccountPoolAccount,
  createAccountPoolBinding,
  createAccountPoolProxy,
  listAccountPoolAccounts,
  listAccountPoolBindings,
  listAccountPoolProxies,
  listAccountPools,
} from './api'
import {
  buildAccountPayload,
  buildPoolPayload,
  buildProxyPayload,
  emptyAccountForm,
  emptyPoolForm,
  emptyProxyForm,
  type AccountPoolAccountFormValues,
  type AccountPoolFormValues,
  type AccountPoolProxyFormValues,
} from './lib/account-pool-form'
import type {
  AccountPool,
  AccountPoolAccount,
  AccountPoolBinding,
  AccountPoolBindingCreateRequest,
  AccountPoolProxy,
  ApiResponse,
} from './types'

type BindingFormValues = {
  channel_id: number
  account_ids: number[]
  model_strategy: string
  fixed_models_text: string
  schedule_policy: string
  account_retry_times: number
}

const EMPTY_ACCOUNTS: AccountPoolAccount[] = []
const EMPTY_BINDINGS: AccountPoolBinding[] = []
const EMPTY_PROXIES: AccountPoolProxy[] = []
const EMPTY_CHANNELS: Channel[] = []
const POOL_PLATFORM_OPTIONS = [{ value: 'openai', label: 'OpenAI' }]
const STATUS_OPTIONS = [
  { value: 'enabled', label: 'Enabled' },
  { value: 'disabled', label: 'Disabled' },
]
const ACCOUNT_STATUS_OPTIONS = [
  { value: 'enabled', label: 'Enabled' },
  { value: 'disabled', label: 'Disabled' },
  { value: 'expired', label: 'Expired' },
]
const PROXY_PROTOCOL_OPTIONS = [
  { value: 'http', label: 'HTTP' },
  { value: 'https', label: 'HTTPS' },
  { value: 'socks5', label: 'SOCKS5' },
  { value: 'socks5h', label: 'SOCKS5H' },
]
const MODEL_STRATEGY_OPTIONS = [
  { value: 'all', label: 'All models' },
  { value: 'fixed', label: 'Fixed models' },
]
const SCHEDULE_POLICY_OPTIONS = [
  { value: 'round_robin', label: 'Round robin' },
  { value: 'random', label: 'Random' },
]

function emptyBindingForm(): BindingFormValues {
  return {
    channel_id: 0,
    account_ids: [],
    model_strategy: 'all',
    fixed_models_text: '',
    schedule_policy: 'round_robin',
    account_retry_times: 0,
  }
}

function formatOptionalTimestamp(value: number) {
  return value > 0 ? formatTimestamp(value) : '-'
}

function apiErrorMessage<T>(result: ApiResponse<T>, fallback: string) {
  return result.message || fallback
}

function statusLabel(status?: string) {
  switch (status) {
    case 'enabled':
      return 'Enabled'
    case 'disabled':
      return 'Disabled'
    case 'deleted':
      return 'Deleted'
    case 'expired':
      return 'Expired'
    case 'draft':
      return 'Draft'
    default:
      return status || 'Unknown'
  }
}

function statusVariant(status?: string): StatusVariant {
  switch (status) {
    case 'enabled':
      return 'success'
    case 'draft':
      return 'info'
    case 'expired':
      return 'warning'
    case 'disabled':
      return 'neutral'
    case 'deleted':
      return 'danger'
    default:
      return 'neutral'
  }
}

function modelListFromText(value: string): string[] {
  return value
    .split(/[,，\n\r]+/)
    .map((item) => item.trim())
    .filter(Boolean)
}

function channelStatusLabel(status: number) {
  return (
    CHANNEL_STATUS_LABELS[status as keyof typeof CHANNEL_STATUS_LABELS] ??
    'Unknown'
  )
}

function channelStatusVariant(status: number): StatusVariant {
  return (
    CHANNEL_STATUS_CONFIG[status as keyof typeof CHANNEL_STATUS_CONFIG]
      ?.variant ?? 'neutral'
  )
}

function StatusPill(props: { status?: string }) {
  const { t } = useTranslation()

  return (
    <StatusBadge
      label={t(statusLabel(props.status))}
      variant={statusVariant(props.status)}
      copyable={false}
    />
  )
}

function FieldBlock(props: {
  children: React.ReactNode
  description?: React.ReactNode
  htmlFor: string
  label: React.ReactNode
}) {
  return (
    <Field>
      <FieldLabel htmlFor={props.htmlFor}>{props.label}</FieldLabel>
      {props.children}
      {props.description && (
        <FieldDescription>{props.description}</FieldDescription>
      )}
    </Field>
  )
}

function BooleanBadge(props: { active: boolean; falseLabel: string; trueLabel: string }) {
  const { t } = useTranslation()

  return (
    <StatusBadge
      label={props.active ? t(props.trueLabel) : t(props.falseLabel)}
      variant={props.active ? 'success' : 'neutral'}
      copyable={false}
    />
  )
}

function useAccountPoolColumns(props: {
  accountsByPool: Record<number, AccountPoolAccount[] | undefined>
  onDetails: (pool: AccountPool) => void
}): ColumnDef<AccountPool>[] {
  const { t } = useTranslation()

  return useMemo(
    () => [
      {
        accessorKey: 'id',
        header: t('ID'),
        cell: ({ row }) => (
          <TableId value={row.getValue('id')} className='w-[60px]' />
        ),
        size: 80,
        meta: { mobileHidden: true },
      },
      {
        accessorKey: 'name',
        header: t('Pool'),
        cell: ({ row }) => {
          const pool = row.original
          return (
            <div className='flex min-w-[220px] flex-col gap-1'>
              <div className='flex items-center gap-2'>
                <LongText className='max-w-[180px] font-medium'>
                  {pool.name}
                </LongText>
                <StatusPill status={pool.status} />
              </div>
              {pool.remark && (
                <LongText className='text-muted-foreground max-w-[240px] text-xs'>
                  {pool.remark}
                </LongText>
              )}
            </div>
          )
        },
        enableHiding: false,
        size: 280,
        meta: { mobileTitle: true },
      },
      {
        accessorKey: 'platform',
        header: t('Platform'),
        cell: ({ row }) => (
          <StatusBadge
            label={row.getValue('platform')}
            variant='info'
            copyable={false}
          />
        ),
        size: 120,
      },
      {
        accessorKey: 'status',
        header: t('Status'),
        cell: ({ row }) => <StatusPill status={row.getValue('status')} />,
        filterFn: (row, id, value) =>
          Array.isArray(value) && value.includes(String(row.getValue(id))),
        size: 120,
        meta: { mobileBadge: true },
      },
      {
        id: 'account_count',
        header: t('Accounts'),
        cell: ({ row }) => {
          const accounts = props.accountsByPool[row.original.id]
          return (
            <span className='text-sm tabular-nums'>
              {accounts ? accounts.length : '-'}
            </span>
          )
        },
        size: 110,
      },
      {
        accessorKey: 'updated_time',
        header: t('Updated At'),
        cell: ({ row }) => (
          <span className='text-muted-foreground text-sm'>
            {formatOptionalTimestamp(row.getValue('updated_time'))}
          </span>
        ),
        size: 180,
      },
      {
        id: 'actions',
        header: () => t('Actions'),
        cell: ({ row }) => (
          <PoolActions row={row} onDetails={props.onDetails} />
        ),
        meta: { pinned: 'right' as const },
      },
    ],
    [props, t]
  )
}

function PoolActions(props: {
  row: Row<AccountPool>
  onDetails: (pool: AccountPool) => void
}) {
  const { t } = useTranslation()
  const pool = props.row.original

  return (
    <div className='-ml-2'>
      <DropdownMenu>
        <DropdownMenuTrigger
          render={
            <Button
              variant='ghost'
              size='icon'
              className='data-popup-open:bg-muted'
            />
          }
        >
          <MoreHorizontal />
          <span className='sr-only'>{t('Open menu')}</span>
        </DropdownMenuTrigger>
        <DropdownMenuContent align='end'>
          <DropdownMenuItem onClick={() => props.onDetails(pool)}>
            <Eye />
            {t('Details')}
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
    </div>
  )
}

export function AccountPools() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const isMobile = useMediaQuery('(max-width: 640px)')
  const [globalFilter, setGlobalFilter] = useState('')
  const [columnFilters, setColumnFilters] = useState<ColumnFiltersState>([])
  const [pagination, setPagination] = useState<PaginationState>({
    pageIndex: 0,
    pageSize: isMobile ? 10 : 20,
  })
  const [poolSheetOpen, setPoolSheetOpen] = useState(false)
  const [selectedPool, setSelectedPool] = useState<AccountPool>()
  const [accountSheetOpen, setAccountSheetOpen] = useState(false)
  const [proxySheetOpen, setProxySheetOpen] = useState(false)
  const [loadedAccountsByPool, setLoadedAccountsByPool] = useState<
    Record<number, AccountPoolAccount[] | undefined>
  >({})

  const poolsQuery = useQuery({
    queryKey: accountPoolsQueryKeys.list(),
    queryFn: async () => {
      const result = await listAccountPools()
      if (!result.success) {
        throw new Error(result.message || 'Failed to load account pools')
      }
      return result.data ?? []
    },
  })

  const selectedPoolID = selectedPool?.id
  const accountsQuery = useQuery({
    queryKey: selectedPoolID
      ? accountPoolsQueryKeys.accounts(selectedPoolID)
      : accountPoolsQueryKeys.accounts(0),
    queryFn: async () => {
      if (!selectedPoolID) return EMPTY_ACCOUNTS
      const result = await listAccountPoolAccounts(selectedPoolID)
      if (!result.success) {
        throw new Error(result.message || 'Failed to load pool accounts')
      }
      return result.data ?? []
    },
    enabled: Boolean(selectedPoolID),
  })

  const bindingsQuery = useQuery({
    queryKey: selectedPoolID
      ? accountPoolsQueryKeys.bindings(selectedPoolID)
      : accountPoolsQueryKeys.bindings(0),
    queryFn: async () => {
      if (!selectedPoolID) return EMPTY_BINDINGS
      const result = await listAccountPoolBindings(selectedPoolID)
      if (!result.success) {
        throw new Error(result.message || 'Failed to load pool bindings')
      }
      return result.data ?? []
    },
    enabled: Boolean(selectedPoolID),
  })

  const proxiesQuery = useQuery({
    queryKey: accountPoolsQueryKeys.proxies(),
    queryFn: async () => {
      const result = await listAccountPoolProxies()
      if (!result.success) {
        throw new Error(result.message || 'Failed to load account pool proxies')
      }
      return result.data ?? []
    },
  })

  useEffect(() => {
    if (selectedPoolID && accountsQuery.data) {
      setLoadedAccountsByPool((previous) => ({
        ...previous,
        [selectedPoolID]: accountsQuery.data,
      }))
    }
  }, [accountsQuery.data, selectedPoolID])

  useEffect(() => {
    for (const error of [
      poolsQuery.error,
      accountsQuery.error,
      bindingsQuery.error,
      proxiesQuery.error,
    ]) {
      if (error) {
        toast.error(error.message)
      }
    }
  }, [
    accountsQuery.error,
    bindingsQuery.error,
    poolsQuery.error,
    proxiesQuery.error,
  ])

  const invalidatePools = () => {
    queryClient.invalidateQueries({ queryKey: accountPoolsQueryKeys.all })
  }

  const createPoolMutation = useMutation({
    mutationFn: async (values: AccountPoolFormValues) =>
      createAccountPool(buildPoolPayload(values)),
    onSuccess: (result) => {
      if (!result.success) {
        toast.error(apiErrorMessage(result, t('Failed to create account pool')))
        return
      }
      toast.success(t('Account pool created'))
      setPoolSheetOpen(false)
      invalidatePools()
    },
    onError: (error) => {
      toast.error(error instanceof Error ? error.message : t('Request failed'))
    },
  })

  const createAccountMutation = useMutation({
    mutationFn: async (values: AccountPoolAccountFormValues) => {
      if (!selectedPoolID) {
        throw new Error(t('Select an account pool first'))
      }
      return createAccountPoolAccount(selectedPoolID, buildAccountPayload(values))
    },
    onSuccess: (result) => {
      if (!result.success) {
        toast.error(apiErrorMessage(result, t('Failed to create account')))
        return
      }
      toast.success(t('Account created'))
      setAccountSheetOpen(false)
      invalidatePools()
    },
    onError: (error) => {
      toast.error(error instanceof Error ? error.message : t('Request failed'))
    },
  })

  const createProxyMutation = useMutation({
    mutationFn: async (values: AccountPoolProxyFormValues) =>
      createAccountPoolProxy(buildProxyPayload(values)),
    onSuccess: (result) => {
      if (!result.success) {
        toast.error(apiErrorMessage(result, t('Failed to create proxy')))
        return
      }
      toast.success(t('Proxy created'))
      setProxySheetOpen(false)
      invalidatePools()
    },
    onError: (error) => {
      toast.error(error instanceof Error ? error.message : t('Request failed'))
    },
  })

  const createBindingMutation = useMutation({
    mutationFn: async (values: BindingFormValues) => {
      if (!selectedPoolID) {
        throw new Error(t('Select an account pool first'))
      }
      const payload: AccountPoolBindingCreateRequest = {
        channel_id: values.channel_id,
        account_ids: values.account_ids,
        model_strategy: values.model_strategy,
        fixed_models: modelListFromText(values.fixed_models_text),
        schedule_policy: values.schedule_policy,
        account_retry_times: values.account_retry_times,
      }
      return createAccountPoolBinding(selectedPoolID, payload)
    },
    onSuccess: (result) => {
      if (!result.success) {
        toast.error(apiErrorMessage(result, t('Failed to create binding')))
        return
      }
      toast.success(t('Binding created'))
      invalidatePools()
    },
    onError: (error) => {
      toast.error(error instanceof Error ? error.message : t('Request failed'))
    },
  })

  const columns = useAccountPoolColumns({
    accountsByPool: loadedAccountsByPool,
    onDetails: (pool) => setSelectedPool(pool),
  })
  const pools = poolsQuery.data ?? []
  const { table } = useDataTable({
    data: pools,
    columns,
    enableRowSelection: false,
    columnFilters,
    globalFilter,
    pagination,
    onColumnFiltersChange: setColumnFilters,
    onGlobalFilterChange: setGlobalFilter,
    onPaginationChange: setPagination,
    globalFilterFn: (row, _columnId, filterValue) => {
      const searchValue = String(filterValue).toLowerCase()
      const pool = row.original
      return [pool.name, pool.platform, pool.status, pool.remark].some((value) =>
        String(value || '')
          .toLowerCase()
          .includes(searchValue)
      )
    },
    columnVisibilityStorageKey: 'account-pools-table-columns',
  })

  return (
    <>
      <SectionPageLayout fixedContent>
        <SectionPageLayout.Title>{t('Account Pools')}</SectionPageLayout.Title>
        <SectionPageLayout.Actions>
          <Button type='button' onClick={() => setPoolSheetOpen(true)}>
            <Plus data-icon='inline-start' />
            {t('Add Pool')}
          </Button>
        </SectionPageLayout.Actions>
        <SectionPageLayout.Content>
          <DataTablePage
            table={table}
            columns={columns}
            isLoading={poolsQuery.isLoading}
            isFetching={poolsQuery.isFetching}
            emptyTitle={t('No Account Pools Found')}
            emptyDescription={t('Create an account pool to group accounts.')}
            skeletonKeyPrefix='account-pools-skeleton'
            applyHeaderSize
            toolbarProps={{
              searchPlaceholder: t('Filter by pool, platform or status...'),
              filters: [
                {
                  columnId: 'status',
                  title: t('Status'),
                  singleSelect: true,
                  options: STATUS_OPTIONS.map((option) => ({
                    ...option,
                    label: t(option.label),
                  })),
                },
              ],
            }}
            getRowClassName={(row, { isMobile }) =>
              row.original.status === 'disabled'
                ? isMobile
                  ? DISABLED_ROW_MOBILE
                  : DISABLED_ROW_DESKTOP
                : undefined
            }
          />
        </SectionPageLayout.Content>
      </SectionPageLayout>

      <PoolFormSheet
        open={poolSheetOpen}
        isSubmitting={createPoolMutation.isPending}
        onOpenChange={setPoolSheetOpen}
        onSubmit={(values) => createPoolMutation.mutate(values)}
      />
      <PoolDetailsSheet
        open={Boolean(selectedPool)}
        pool={selectedPool}
        accounts={accountsQuery.data ?? EMPTY_ACCOUNTS}
        bindings={bindingsQuery.data ?? EMPTY_BINDINGS}
        proxies={proxiesQuery.data ?? EMPTY_PROXIES}
        accountsLoading={accountsQuery.isLoading}
        bindingsLoading={bindingsQuery.isLoading}
        proxiesLoading={proxiesQuery.isLoading}
        bindingSubmitting={createBindingMutation.isPending}
        onOpenChange={(open) => !open && setSelectedPool(undefined)}
        onCreateAccount={() => setAccountSheetOpen(true)}
        onCreateProxy={() => setProxySheetOpen(true)}
        onCreateBinding={(values) => createBindingMutation.mutate(values)}
      />
      <AccountFormSheet
        key={
          accountSheetOpen && selectedPool
            ? `account-${selectedPool.id}`
            : 'account-closed'
        }
        open={accountSheetOpen}
        pool={selectedPool}
        proxies={proxiesQuery.data ?? EMPTY_PROXIES}
        isSubmitting={createAccountMutation.isPending}
        onOpenChange={setAccountSheetOpen}
        onSubmit={(values) => {
          try {
            buildAccountPayload(values)
          } catch (error) {
            toast.error(
              error instanceof Error ? t(error.message) : t('Invalid account')
            )
            return
          }
          createAccountMutation.mutate(values)
        }}
      />
      <ProxyFormSheet
        open={proxySheetOpen}
        proxies={proxiesQuery.data ?? EMPTY_PROXIES}
        isSubmitting={createProxyMutation.isPending}
        onOpenChange={setProxySheetOpen}
        onSubmit={(values) => createProxyMutation.mutate(values)}
      />
    </>
  )
}

function PoolFormSheet(props: {
  open: boolean
  isSubmitting: boolean
  onOpenChange: (open: boolean) => void
  onSubmit: (values: AccountPoolFormValues) => void
}) {
  const { t } = useTranslation()
  const [form, setForm] = useState<AccountPoolFormValues>(emptyPoolForm())

  useEffect(() => {
    if (props.open) {
      setForm(emptyPoolForm())
    }
  }, [props.open])

  const setField = <K extends keyof AccountPoolFormValues>(
    key: K,
    value: AccountPoolFormValues[K]
  ) => setForm((previous) => ({ ...previous, [key]: value }))

  const handleSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!form.name.trim()) {
      toast.error(t('Pool name is required'))
      return
    }
    props.onSubmit(form)
  }

  return (
    <Sheet open={props.open} onOpenChange={props.onOpenChange}>
      <SheetContent className={sideDrawerContentClassName('sm:max-w-[520px]')}>
        <SheetHeader className={sideDrawerHeaderClassName()}>
          <SheetTitle>{t('Add Account Pool')}</SheetTitle>
          <SheetDescription>{t('Account Pools')}</SheetDescription>
        </SheetHeader>
        <form
          id='account-pool-form'
          className={sideDrawerFormClassName()}
          onSubmit={handleSubmit}
        >
          <FieldGroup>
            <FieldBlock label={t('Name')} htmlFor='account-pool-name'>
              <Input
                id='account-pool-name'
                value={form.name}
                onChange={(event) => setField('name', event.target.value)}
              />
            </FieldBlock>
            <FieldBlock label={t('Platform')} htmlFor='account-pool-platform'>
              <Select
                items={POOL_PLATFORM_OPTIONS}
                value={form.platform}
                onValueChange={(value) => value && setField('platform', value)}
              >
                <SelectTrigger id='account-pool-platform'>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent alignItemWithTrigger={false}>
                  <SelectGroup>
                    {POOL_PLATFORM_OPTIONS.map((option) => (
                      <SelectItem key={option.value} value={option.value}>
                        {option.label}
                      </SelectItem>
                    ))}
                  </SelectGroup>
                </SelectContent>
              </Select>
            </FieldBlock>
            <FieldBlock
              label={t('Default Proxy ID')}
              htmlFor='account-pool-default-proxy-id'
            >
              <Input
                id='account-pool-default-proxy-id'
                type='number'
                min={0}
                value={form.default_proxy_id}
                onChange={(event) =>
                  setField('default_proxy_id', Number(event.target.value))
                }
              />
            </FieldBlock>
            <FieldBlock
              label={t('Schedule Policy')}
              htmlFor='account-pool-schedule-policy'
            >
              <Select
                items={SCHEDULE_POLICY_OPTIONS}
                value={form.default_schedule_policy || 'round_robin'}
                onValueChange={(value) =>
                  value && setField('default_schedule_policy', value)
                }
              >
                <SelectTrigger id='account-pool-schedule-policy'>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent alignItemWithTrigger={false}>
                  <SelectGroup>
                    {SCHEDULE_POLICY_OPTIONS.map((option) => (
                      <SelectItem key={option.value} value={option.value}>
                        {t(option.label)}
                      </SelectItem>
                    ))}
                  </SelectGroup>
                </SelectContent>
              </Select>
            </FieldBlock>
            <div className={sideDrawerSwitchItemClassName()}>
              <div className='min-w-0'>
                <FieldLabel htmlFor='account-pool-monitor'>
                  {t('Default Monitor')}
                </FieldLabel>
                <p className='text-muted-foreground mt-1 text-xs'>
                  {t('Monitor accounts by default')}
                </p>
              </div>
              <Switch
                id='account-pool-monitor'
                checked={form.default_monitor_enabled}
                onCheckedChange={(checked) =>
                  setField('default_monitor_enabled', checked)
                }
              />
            </div>
            <FieldBlock label={t('Remark')} htmlFor='account-pool-remark'>
              <Textarea
                id='account-pool-remark'
                value={form.remark}
                onChange={(event) => setField('remark', event.target.value)}
              />
            </FieldBlock>
          </FieldGroup>
        </form>
        <SheetFooter className={sideDrawerFooterClassName()}>
          <SheetClose render={<Button variant='outline' />}>
            {t('Cancel')}
          </SheetClose>
          <Button
            type='submit'
            form='account-pool-form'
            disabled={props.isSubmitting}
          >
            {props.isSubmitting ? (
              <Loader2 data-icon='inline-start' className='animate-spin' />
            ) : (
              <Save data-icon='inline-start' />
            )}
            {t('Create')}
          </Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  )
}

function PoolDetailsSheet(props: {
  open: boolean
  pool?: AccountPool
  accounts: AccountPoolAccount[]
  bindings: AccountPoolBinding[]
  proxies: AccountPoolProxy[]
  accountsLoading: boolean
  bindingsLoading: boolean
  proxiesLoading: boolean
  bindingSubmitting: boolean
  onOpenChange: (open: boolean) => void
  onCreateAccount: () => void
  onCreateProxy: () => void
  onCreateBinding: (values: BindingFormValues) => void
}) {
  const { t } = useTranslation()
  const pool = props.pool

  return (
    <Sheet open={props.open} onOpenChange={props.onOpenChange}>
      <SheetContent className={sideDrawerContentClassName('sm:max-w-[960px]')}>
        <SheetHeader className={sideDrawerHeaderClassName()}>
          <SheetTitle>{pool?.name || t('Account Pool')}</SheetTitle>
          <SheetDescription>
            {pool ? `${pool.platform} / ${t(statusLabel(pool.status))}` : '-'}
          </SheetDescription>
        </SheetHeader>
        <div className={sideDrawerFormClassName('gap-4')}>
          {pool && (
            <SideDrawerSection>
              <SideDrawerSectionHeader title={t('Pool Summary')} />
              <div className='grid gap-3 text-sm sm:grid-cols-4'>
                <SummaryItem label={t('Platform')} value={pool.platform} />
                <SummaryItem
                  label={t('Status')}
                  value={<StatusPill status={pool.status} />}
                />
                <SummaryItem
                  label={t('Accounts')}
                  value={props.accounts.length}
                />
                <SummaryItem
                  label={t('Updated At')}
                  value={formatOptionalTimestamp(pool.updated_time)}
                />
              </div>
            </SideDrawerSection>
          )}

          <Tabs defaultValue='accounts'>
            <TabsList>
              <TabsTrigger value='accounts'>{t('Accounts')}</TabsTrigger>
              <TabsTrigger value='bindings'>{t('Bindings')}</TabsTrigger>
              <TabsTrigger value='proxies'>{t('Proxies')}</TabsTrigger>
            </TabsList>
            <TabsContent value='accounts' className='min-h-0'>
              <AccountListSection
                accounts={props.accounts}
                loading={props.accountsLoading}
                onCreateAccount={props.onCreateAccount}
              />
            </TabsContent>
            <TabsContent value='bindings' className='min-h-0'>
              <BindingSection
                accounts={props.accounts}
                bindings={props.bindings}
                loading={props.bindingsLoading}
                submitting={props.bindingSubmitting}
                onCreateBinding={props.onCreateBinding}
              />
            </TabsContent>
            <TabsContent value='proxies' className='min-h-0'>
              <ProxyListSection
                proxies={props.proxies}
                loading={props.proxiesLoading}
                onCreateProxy={props.onCreateProxy}
              />
            </TabsContent>
          </Tabs>
        </div>
        <SheetFooter className={sideDrawerFooterClassName()}>
          <SheetClose render={<Button variant='outline' />}>
            {t('Close')}
          </SheetClose>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  )
}

function SummaryItem(props: { label: React.ReactNode; value: React.ReactNode }) {
  return (
    <div className='flex min-w-0 flex-col gap-1'>
      <span className='text-muted-foreground text-xs'>{props.label}</span>
      <div className='min-w-0 font-medium'>{props.value}</div>
    </div>
  )
}

function AccountListSection(props: {
  accounts: AccountPoolAccount[]
  loading: boolean
  onCreateAccount: () => void
}) {
  const { t } = useTranslation()

  return (
    <SideDrawerSection className='pt-4'>
      <div className='flex items-center justify-between gap-3'>
        <SideDrawerSectionHeader title={t('Accounts')} />
        <Button type='button' size='sm' onClick={props.onCreateAccount}>
          <Plus data-icon='inline-start' />
          {t('Add Account')}
        </Button>
      </div>
      <div className='border-border rounded-lg border'>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t('Account')}</TableHead>
              <TableHead>{t('Status')}</TableHead>
              <TableHead>{t('Priority')}</TableHead>
              <TableHead>{t('Weight')}</TableHead>
              <TableHead>{t('Max Concurrency')}</TableHead>
              <TableHead>{t('Models')}</TableHead>
              <TableHead>{t('Credentials')}</TableHead>
              <TableHead>{t('Last Error')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {props.loading && <LoadingRow colSpan={8} />}
            {!props.loading && props.accounts.length === 0 && (
              <EmptyRow colSpan={8} label={t('No accounts found')} />
            )}
            {props.accounts.map((account) => (
              <TableRow
                key={account.id}
                className={cn(
                  account.status === 'disabled' && 'text-muted-foreground'
                )}
              >
                <TableCell>
                  <div className='flex min-w-[180px] flex-col gap-1'>
                    <LongText className='max-w-[200px] font-medium'>
                      {account.name}
                    </LongText>
                    <span className='text-muted-foreground text-xs'>
                      {account.account_identifier || '-'}
                    </span>
                  </div>
                </TableCell>
                <TableCell>
                  <StatusPill status={account.status} />
                </TableCell>
                <TableCell>{account.priority}</TableCell>
                <TableCell>{account.weight}</TableCell>
                <TableCell>{account.max_concurrency}</TableCell>
                <TableCell>
                  <ModelBadges models={account.supported_models} />
                </TableCell>
                <TableCell>
                  <div className='flex flex-wrap gap-1'>
                    <BooleanBadge
                      active={account.has_credential}
                      trueLabel='Credential'
                      falseLabel='No Credential'
                    />
                    <BooleanBadge
                      active={account.has_token}
                      trueLabel='Token'
                      falseLabel='No Token'
                    />
                  </div>
                </TableCell>
                <TableCell>
                  <LongText className='text-destructive max-w-[200px] text-xs'>
                    {account.last_error || '-'}
                  </LongText>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
    </SideDrawerSection>
  )
}

function BindingSection(props: {
  accounts: AccountPoolAccount[]
  bindings: AccountPoolBinding[]
  loading: boolean
  submitting: boolean
  onCreateBinding: (values: BindingFormValues) => void
}) {
  const { t } = useTranslation()

  return (
    <div className='flex flex-col gap-6 pt-4'>
      <SideDrawerSection>
        <SideDrawerSectionHeader title={t('Bindings')} />
        <div className='border-border rounded-lg border'>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t('Channel')}</TableHead>
                <TableHead>{t('Channel Status')}</TableHead>
                <TableHead>{t('Binding Status')}</TableHead>
                <TableHead>{t('Retry Count')}</TableHead>
                <TableHead>{t('Updated At')}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {props.loading && <LoadingRow colSpan={5} />}
              {!props.loading && props.bindings.length === 0 && (
                <EmptyRow colSpan={5} label={t('No bindings found')} />
              )}
              {props.bindings.map((binding) => (
                <TableRow key={binding.id}>
                  <TableCell>
                    <div className='flex min-w-[180px] flex-col gap-1'>
                      <LongText className='max-w-[200px] font-medium'>
                        {binding.channel_name || '-'}
                      </LongText>
                      <TableId value={binding.channel_id} />
                    </div>
                  </TableCell>
                  <TableCell>
                    <StatusBadge
                      label={t(channelStatusLabel(binding.channel_status))}
                      variant={channelStatusVariant(binding.channel_status)}
                      copyable={false}
                    />
                  </TableCell>
                  <TableCell>
                    <StatusPill status={binding.status} />
                  </TableCell>
                  <TableCell>{binding.account_retry_times}</TableCell>
                  <TableCell>
                    <span className='text-muted-foreground text-sm'>
                      {formatOptionalTimestamp(binding.updated_time)}
                    </span>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      </SideDrawerSection>
      <BindingForm
        accounts={props.accounts}
        submitting={props.submitting}
        onSubmit={props.onCreateBinding}
      />
    </div>
  )
}

function BindingForm(props: {
  accounts: AccountPoolAccount[]
  submitting: boolean
  onSubmit: (values: BindingFormValues) => void
}) {
  const { t } = useTranslation()
  const [form, setForm] = useState<BindingFormValues>(emptyBindingForm())
  const disabledChannelsQuery = useQuery({
    queryKey: ['channels', 'disabled', 'account-pool-bindings'],
    queryFn: async () => {
      const result = await getChannels({ status: 'disabled', page_size: 100 })
      if (!result.success) {
        throw new Error(result.message || 'Failed to load disabled channels')
      }
      return result.data?.items ?? []
    },
  })
  const disabledChannels = disabledChannelsQuery.data ?? EMPTY_CHANNELS

  useEffect(() => {
    if (disabledChannelsQuery.error) {
      toast.error(disabledChannelsQuery.error.message)
    }
  }, [disabledChannelsQuery.error])

  const setField = <K extends keyof BindingFormValues>(
    key: K,
    value: BindingFormValues[K]
  ) => setForm((previous) => ({ ...previous, [key]: value }))

  const toggleAccount = (accountID: number, checked: boolean) => {
    setForm((previous) => ({
      ...previous,
      account_ids: checked
        ? [...previous.account_ids, accountID]
        : previous.account_ids.filter((id) => id !== accountID),
    }))
  }

  const handleSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (form.channel_id <= 0) {
      toast.error(t('Disabled channel is required'))
      return
    }
    props.onSubmit(form)
  }

  return (
    <SideDrawerSection>
      <SideDrawerSectionHeader title={t('Draft Binding')} />
      <form className='flex flex-col gap-4' onSubmit={handleSubmit}>
        <FieldGroup className='gap-4 sm:grid sm:grid-cols-2'>
          <FieldBlock
            label={t('Disabled Channel')}
            htmlFor='account-pool-binding-channel'
            description={t('Only disabled channels are available for binding.')}
          >
            <Select
              items={disabledChannels.map((channel) => ({
                value: String(channel.id),
                label: channel.name,
              }))}
              value={form.channel_id ? String(form.channel_id) : ''}
              onValueChange={(value) =>
                setField('channel_id', value ? Number(value) : 0)
              }
              disabled={disabledChannelsQuery.isLoading}
            >
              <SelectTrigger id='account-pool-binding-channel'>
                <SelectValue placeholder={t('Select disabled channel')} />
              </SelectTrigger>
              <SelectContent alignItemWithTrigger={false}>
                <SelectGroup>
                  {disabledChannels.map((channel) => (
                    <SelectItem key={channel.id} value={String(channel.id)}>
                      #{channel.id} {channel.name}
                    </SelectItem>
                  ))}
                </SelectGroup>
              </SelectContent>
            </Select>
          </FieldBlock>
          <FieldBlock
            label={t('Model Strategy')}
            htmlFor='account-pool-binding-model-strategy'
          >
            <Select
              items={MODEL_STRATEGY_OPTIONS}
              value={form.model_strategy}
              onValueChange={(value) =>
                value && setField('model_strategy', value)
              }
            >
              <SelectTrigger id='account-pool-binding-model-strategy'>
                <SelectValue />
              </SelectTrigger>
              <SelectContent alignItemWithTrigger={false}>
                <SelectGroup>
                  {MODEL_STRATEGY_OPTIONS.map((option) => (
                    <SelectItem key={option.value} value={option.value}>
                      {t(option.label)}
                    </SelectItem>
                  ))}
                </SelectGroup>
              </SelectContent>
            </Select>
          </FieldBlock>
          <FieldBlock
            label={t('Fixed Models')}
            htmlFor='account-pool-binding-fixed-models'
          >
            <Input
              id='account-pool-binding-fixed-models'
              value={form.fixed_models_text}
              onChange={(event) =>
                setField('fixed_models_text', event.target.value)
              }
              placeholder={t('Comma-separated model names')}
            />
          </FieldBlock>
          <FieldBlock
            label={t('Schedule Policy')}
            htmlFor='account-pool-binding-schedule-policy'
          >
            <Select
              items={SCHEDULE_POLICY_OPTIONS}
              value={form.schedule_policy}
              onValueChange={(value) =>
                value && setField('schedule_policy', value)
              }
            >
              <SelectTrigger id='account-pool-binding-schedule-policy'>
                <SelectValue />
              </SelectTrigger>
              <SelectContent alignItemWithTrigger={false}>
                <SelectGroup>
                  {SCHEDULE_POLICY_OPTIONS.map((option) => (
                    <SelectItem key={option.value} value={option.value}>
                      {t(option.label)}
                    </SelectItem>
                  ))}
                </SelectGroup>
              </SelectContent>
            </Select>
          </FieldBlock>
          <FieldBlock
            label={t('Retry Count')}
            htmlFor='account-pool-binding-retry-count'
          >
            <Input
              id='account-pool-binding-retry-count'
              type='number'
              min={0}
              value={form.account_retry_times}
              onChange={(event) =>
                setField('account_retry_times', Number(event.target.value))
              }
            />
          </FieldBlock>
        </FieldGroup>
        <FieldSet>
          <FieldLegend variant='label'>{t('Accounts')}</FieldLegend>
          <div className='grid gap-2 sm:grid-cols-2'>
            {props.accounts.map((account) => (
              <Field key={account.id} orientation='horizontal'>
                <Checkbox
                  id={`binding-account-${account.id}`}
                  checked={form.account_ids.includes(account.id)}
                  onCheckedChange={(checked) =>
                    toggleAccount(account.id, Boolean(checked))
                  }
                />
                <FieldLabel htmlFor={`binding-account-${account.id}`}>
                  {account.name}
                </FieldLabel>
              </Field>
            ))}
            {props.accounts.length === 0 && (
              <span className='text-muted-foreground text-sm'>
                {t('No accounts found')}
              </span>
            )}
          </div>
        </FieldSet>
        <div className='flex justify-end'>
          <Button type='submit' disabled={props.submitting}>
            {props.submitting ? (
              <Loader2 data-icon='inline-start' className='animate-spin' />
            ) : (
              <Link2 data-icon='inline-start' />
            )}
            {t('Create Draft Binding')}
          </Button>
        </div>
      </form>
    </SideDrawerSection>
  )
}

function ProxyListSection(props: {
  proxies: AccountPoolProxy[]
  loading: boolean
  onCreateProxy: () => void
}) {
  const { t } = useTranslation()

  return (
    <SideDrawerSection className='pt-4'>
      <div className='flex items-center justify-between gap-3'>
        <SideDrawerSectionHeader title={t('Proxies')} />
        <Button type='button' size='sm' onClick={props.onCreateProxy}>
          <Plus data-icon='inline-start' />
          {t('Add Proxy')}
        </Button>
      </div>
      <div className='border-border rounded-lg border'>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t('Proxy')}</TableHead>
              <TableHead>{t('Protocol')}</TableHead>
              <TableHead>{t('Status')}</TableHead>
              <TableHead>{t('Password')}</TableHead>
              <TableHead>{t('Fallback Proxy ID')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {props.loading && <LoadingRow colSpan={5} />}
            {!props.loading && props.proxies.length === 0 && (
              <EmptyRow colSpan={5} label={t('No proxies found')} />
            )}
            {props.proxies.map((proxy) => (
              <TableRow key={proxy.id}>
                <TableCell>
                  <div className='flex min-w-[180px] flex-col gap-1'>
                    <LongText className='max-w-[200px] font-medium'>
                      {proxy.name}
                    </LongText>
                    <span className='text-muted-foreground text-xs'>
                      {proxy.host}:{proxy.port}
                    </span>
                  </div>
                </TableCell>
                <TableCell>{proxy.protocol}</TableCell>
                <TableCell>
                  <StatusPill status={proxy.status} />
                </TableCell>
                <TableCell>
                  <BooleanBadge
                    active={proxy.has_password}
                    trueLabel='Password Set'
                    falseLabel='No Password'
                  />
                </TableCell>
                <TableCell>{proxy.fallback_proxy_id || '-'}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
    </SideDrawerSection>
  )
}

function AccountFormSheet(props: {
  open: boolean
  pool?: AccountPool
  proxies: AccountPoolProxy[]
  isSubmitting: boolean
  onOpenChange: (open: boolean) => void
  onSubmit: (values: AccountPoolAccountFormValues) => void
}) {
  const { t } = useTranslation()
  const [form, setForm] =
    useState<AccountPoolAccountFormValues>(emptyAccountForm())

  useEffect(() => {
    if (props.open) {
      setForm(emptyAccountForm())
    }
  }, [props.open])

  const setField = <K extends keyof AccountPoolAccountFormValues>(
    key: K,
    value: AccountPoolAccountFormValues[K]
  ) => setForm((previous) => ({ ...previous, [key]: value }))

  const handleSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!form.name.trim()) {
      toast.error(t('Account name is required'))
      return
    }
    props.onSubmit(form)
  }

  return (
    <Sheet open={props.open} onOpenChange={props.onOpenChange}>
      <SheetContent className={sideDrawerContentClassName('sm:max-w-[760px]')}>
        <SheetHeader className={sideDrawerHeaderClassName()}>
          <SheetTitle>{t('Add Account')}</SheetTitle>
          <SheetDescription>{props.pool?.name || t('Account Pool')}</SheetDescription>
        </SheetHeader>
        <form
          id='account-pool-account-form'
          className={sideDrawerFormClassName()}
          onSubmit={handleSubmit}
        >
          <SideDrawerSection>
            <SideDrawerSectionHeader title={t('Account')} />
            <FieldGroup className='gap-4 sm:grid sm:grid-cols-2'>
              <FieldBlock label={t('Name')} htmlFor='account-pool-account-name'>
                <Input
                  id='account-pool-account-name'
                  value={form.name}
                  onChange={(event) => setField('name', event.target.value)}
                />
              </FieldBlock>
              <FieldBlock
                label={t('Identifier')}
                htmlFor='account-pool-account-identifier'
              >
                <Input
                  id='account-pool-account-identifier'
                  value={form.account_identifier}
                  onChange={(event) =>
                    setField('account_identifier', event.target.value)
                  }
                />
              </FieldBlock>
              <FieldBlock
                label={t('Status')}
                htmlFor='account-pool-account-status'
              >
                <Select
                  items={ACCOUNT_STATUS_OPTIONS}
                  value={form.status}
                  onValueChange={(value) => value && setField('status', value)}
                >
                  <SelectTrigger id='account-pool-account-status'>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent alignItemWithTrigger={false}>
                    <SelectGroup>
                      {ACCOUNT_STATUS_OPTIONS.map((option) => (
                        <SelectItem key={option.value} value={option.value}>
                          {t(option.label)}
                        </SelectItem>
                      ))}
                    </SelectGroup>
                  </SelectContent>
                </Select>
              </FieldBlock>
              <FieldBlock
                label={t('Proxy')}
                htmlFor='account-pool-account-proxy'
              >
                <Select
                  items={[
                    { value: '0', label: t('No Proxy') },
                    ...props.proxies.map((proxy) => ({
                      value: String(proxy.id),
                      label: proxy.name,
                    })),
                  ]}
                  value={String(form.proxy_id)}
                  onValueChange={(value) =>
                    setField('proxy_id', value ? Number(value) : 0)
                  }
                >
                  <SelectTrigger id='account-pool-account-proxy'>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent alignItemWithTrigger={false}>
                    <SelectGroup>
                      <SelectItem value='0'>{t('No Proxy')}</SelectItem>
                      {props.proxies.map((proxy) => (
                        <SelectItem key={proxy.id} value={String(proxy.id)}>
                          {proxy.name}
                        </SelectItem>
                      ))}
                    </SelectGroup>
                  </SelectContent>
                </Select>
              </FieldBlock>
              <NumericField
                id='account-pool-account-priority'
                label={t('Priority')}
                value={form.priority}
                onChange={(value) => setField('priority', value)}
              />
              <NumericField
                id='account-pool-account-weight'
                label={t('Weight')}
                value={form.weight}
                onChange={(value) => setField('weight', value)}
              />
              <NumericField
                id='account-pool-account-max-concurrency'
                label={t('Max Concurrency')}
                min={1}
                value={form.max_concurrency}
                onChange={(value) => setField('max_concurrency', value)}
              />
            </FieldGroup>
          </SideDrawerSection>
          <SideDrawerSection>
            <SideDrawerSectionHeader title={t('Credentials')} />
            <FieldGroup className='gap-4 sm:grid sm:grid-cols-2'>
              <FieldBlock
                label={t('Credential Type')}
                htmlFor='account-pool-account-credential-type'
              >
                <Select
                  items={[
                    { value: 'api_key', label: 'API Key' },
                    { value: 'oauth', label: 'OAuth' },
                  ]}
                  value={form.credential_type}
                  onValueChange={(value) =>
                    value && setField('credential_type', value)
                  }
                >
                  <SelectTrigger id='account-pool-account-credential-type'>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent alignItemWithTrigger={false}>
                    <SelectGroup>
                      <SelectItem value='api_key'>{t('API Key')}</SelectItem>
                      <SelectItem value='oauth'>{t('OAuth')}</SelectItem>
                    </SelectGroup>
                  </SelectContent>
                </Select>
              </FieldBlock>
              <FieldBlock label={t('API Key')} htmlFor='account-pool-account-api-key'>
                <Input
                  id='account-pool-account-api-key'
                  type='password'
                  value={form.api_key}
                  onChange={(event) => setField('api_key', event.target.value)}
                />
              </FieldBlock>
              <FieldBlock label={t('Email')} htmlFor='account-pool-account-email'>
                <Input
                  id='account-pool-account-email'
                  value={form.email}
                  onChange={(event) => setField('email', event.target.value)}
                />
              </FieldBlock>
              <FieldBlock
                label={t('Refresh Token')}
                htmlFor='account-pool-account-refresh-token'
              >
                <Input
                  id='account-pool-account-refresh-token'
                  type='password'
                  value={form.refresh_token}
                  onChange={(event) =>
                    setField('refresh_token', event.target.value)
                  }
                />
              </FieldBlock>
              <FieldBlock
                label={t('Access Token')}
                htmlFor='account-pool-account-access-token'
              >
                <Input
                  id='account-pool-account-access-token'
                  type='password'
                  value={form.access_token}
                  onChange={(event) =>
                    setField('access_token', event.target.value)
                  }
                />
              </FieldBlock>
              <FieldBlock
                label={t('Token Refresh Token')}
                htmlFor='account-pool-account-token-refresh-token'
              >
                <Input
                  id='account-pool-account-token-refresh-token'
                  type='password'
                  value={form.token_refresh_token}
                  onChange={(event) =>
                    setField('token_refresh_token', event.target.value)
                  }
                />
              </FieldBlock>
              <NumericField
                id='account-pool-account-token-expires-at'
                label={t('Token Expires At')}
                value={form.token_expires_at}
                onChange={(value) => setField('token_expires_at', value)}
              />
              <NumericField
                id='account-pool-account-token-version'
                label={t('Token Version')}
                value={form.token_version}
                onChange={(value) => setField('token_version', value)}
              />
            </FieldGroup>
          </SideDrawerSection>
          <SideDrawerSection>
            <SideDrawerSectionHeader title={t('Models')} />
            <FieldGroup>
              <FieldBlock
                label={t('Supported Models')}
                htmlFor='account-pool-account-supported-models'
              >
                <Textarea
                  id='account-pool-account-supported-models'
                  value={form.supported_models_text}
                  onChange={(event) =>
                    setField('supported_models_text', event.target.value)
                  }
                  placeholder={t('Comma-separated model names')}
                />
              </FieldBlock>
              <FieldBlock
                label={t('Model Mapping JSON')}
                htmlFor='account-pool-account-model-mapping'
              >
                <Textarea
                  id='account-pool-account-model-mapping'
                  value={form.model_mapping_text}
                  onChange={(event) =>
                    setField('model_mapping_text', event.target.value)
                  }
                  placeholder='{"gpt-5":"upstream-gpt-5"}'
                />
              </FieldBlock>
            </FieldGroup>
          </SideDrawerSection>
        </form>
        <SheetFooter className={sideDrawerFooterClassName()}>
          <SheetClose render={<Button variant='outline' />}>
            {t('Cancel')}
          </SheetClose>
          <Button
            type='submit'
            form='account-pool-account-form'
            disabled={props.isSubmitting}
          >
            {props.isSubmitting ? (
              <Loader2 data-icon='inline-start' className='animate-spin' />
            ) : (
              <Save data-icon='inline-start' />
            )}
            {t('Create')}
          </Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  )
}

function ProxyFormSheet(props: {
  open: boolean
  proxies: AccountPoolProxy[]
  isSubmitting: boolean
  onOpenChange: (open: boolean) => void
  onSubmit: (values: AccountPoolProxyFormValues) => void
}) {
  const { t } = useTranslation()
  const [form, setForm] =
    useState<AccountPoolProxyFormValues>(emptyProxyForm())

  useEffect(() => {
    if (props.open) {
      setForm(emptyProxyForm())
    }
  }, [props.open])

  const setField = <K extends keyof AccountPoolProxyFormValues>(
    key: K,
    value: AccountPoolProxyFormValues[K]
  ) => setForm((previous) => ({ ...previous, [key]: value }))

  const handleSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!form.name.trim()) {
      toast.error(t('Proxy name is required'))
      return
    }
    if (!form.host.trim()) {
      toast.error(t('Proxy host is required'))
      return
    }
    props.onSubmit(form)
  }

  return (
    <Sheet open={props.open} onOpenChange={props.onOpenChange}>
      <SheetContent className={sideDrawerContentClassName('sm:max-w-[560px]')}>
        <SheetHeader className={sideDrawerHeaderClassName()}>
          <SheetTitle>{t('Add Proxy')}</SheetTitle>
          <SheetDescription>{t('Proxies')}</SheetDescription>
        </SheetHeader>
        <form
          id='account-pool-proxy-form'
          className={sideDrawerFormClassName()}
          onSubmit={handleSubmit}
        >
          <FieldGroup>
            <FieldBlock label={t('Name')} htmlFor='account-pool-proxy-name'>
              <Input
                id='account-pool-proxy-name'
                value={form.name}
                onChange={(event) => setField('name', event.target.value)}
              />
            </FieldBlock>
            <FieldBlock
              label={t('Protocol')}
              htmlFor='account-pool-proxy-protocol'
            >
              <Select
                items={PROXY_PROTOCOL_OPTIONS}
                value={form.protocol}
                onValueChange={(value) => value && setField('protocol', value)}
              >
                <SelectTrigger id='account-pool-proxy-protocol'>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent alignItemWithTrigger={false}>
                  <SelectGroup>
                    {PROXY_PROTOCOL_OPTIONS.map((option) => (
                      <SelectItem key={option.value} value={option.value}>
                        {option.label}
                      </SelectItem>
                    ))}
                  </SelectGroup>
                </SelectContent>
              </Select>
            </FieldBlock>
            <FieldBlock label={t('Host')} htmlFor='account-pool-proxy-host'>
              <Input
                id='account-pool-proxy-host'
                value={form.host}
                onChange={(event) => setField('host', event.target.value)}
              />
            </FieldBlock>
            <NumericField
              id='account-pool-proxy-port'
              label={t('Port')}
              min={0}
              value={form.port}
              onChange={(value) => setField('port', value)}
            />
            <FieldBlock
              label={t('Username')}
              htmlFor='account-pool-proxy-username'
            >
              <Input
                id='account-pool-proxy-username'
                value={form.username}
                onChange={(event) => setField('username', event.target.value)}
              />
            </FieldBlock>
            <FieldBlock
              label={t('Password')}
              htmlFor='account-pool-proxy-password'
            >
              <Input
                id='account-pool-proxy-password'
                type='password'
                value={form.password}
                onChange={(event) => setField('password', event.target.value)}
              />
            </FieldBlock>
            <FieldBlock label={t('Status')} htmlFor='account-pool-proxy-status'>
              <Select
                items={STATUS_OPTIONS}
                value={form.status}
                onValueChange={(value) => value && setField('status', value)}
              >
                <SelectTrigger id='account-pool-proxy-status'>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent alignItemWithTrigger={false}>
                  <SelectGroup>
                    {STATUS_OPTIONS.map((option) => (
                      <SelectItem key={option.value} value={option.value}>
                        {t(option.label)}
                      </SelectItem>
                    ))}
                  </SelectGroup>
                </SelectContent>
              </Select>
            </FieldBlock>
            <FieldBlock
              label={t('Fallback Proxy')}
              htmlFor='account-pool-proxy-fallback'
            >
              <Select
                items={[
                  { value: '0', label: t('No Fallback Proxy') },
                  ...props.proxies.map((proxy) => ({
                    value: String(proxy.id),
                    label: proxy.name,
                  })),
                ]}
                value={String(form.fallback_proxy_id)}
                onValueChange={(value) =>
                  setField('fallback_proxy_id', value ? Number(value) : 0)
                }
              >
                <SelectTrigger id='account-pool-proxy-fallback'>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent alignItemWithTrigger={false}>
                  <SelectGroup>
                    <SelectItem value='0'>{t('No Fallback Proxy')}</SelectItem>
                    {props.proxies.map((proxy) => (
                      <SelectItem key={proxy.id} value={String(proxy.id)}>
                        {proxy.name}
                      </SelectItem>
                    ))}
                  </SelectGroup>
                </SelectContent>
              </Select>
            </FieldBlock>
          </FieldGroup>
        </form>
        <SheetFooter className={sideDrawerFooterClassName()}>
          <SheetClose render={<Button variant='outline' />}>
            {t('Cancel')}
          </SheetClose>
          <Button
            type='submit'
            form='account-pool-proxy-form'
            disabled={props.isSubmitting}
          >
            {props.isSubmitting ? (
              <Loader2 data-icon='inline-start' className='animate-spin' />
            ) : (
              <Save data-icon='inline-start' />
            )}
            {t('Create')}
          </Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  )
}

function NumericField(props: {
  id: string
  label: React.ReactNode
  min?: number
  value: number
  onChange: (value: number) => void
}) {
  return (
    <FieldBlock label={props.label} htmlFor={props.id}>
      <Input
        id={props.id}
        type='number'
        min={props.min}
        value={props.value}
        onChange={(event) => props.onChange(Number(event.target.value))}
      />
    </FieldBlock>
  )
}

function ModelBadges(props: { models: string[] }) {
  const { t } = useTranslation()
  const visibleModels = props.models.slice(0, 3)
  const remaining = props.models.length - visibleModels.length

  if (props.models.length === 0) {
    return <span className='text-muted-foreground text-sm'>-</span>
  }

  return (
    <div className='flex max-w-[220px] flex-wrap gap-1'>
      {visibleModels.map((model) => (
        <StatusBadge
          key={model}
          label={model}
          variant='neutral'
          copyable={false}
        />
      ))}
      {remaining > 0 && (
        <StatusBadge
          label={t('+{{count}} more', { count: remaining })}
          variant='neutral'
          copyable={false}
        />
      )}
    </div>
  )
}

function LoadingRow(props: { colSpan: number }) {
  const { t } = useTranslation()
  return (
    <TableRow>
      <TableCell colSpan={props.colSpan}>
        <div className='text-muted-foreground flex items-center justify-center gap-2 py-8 text-sm'>
          <Loader2 className='animate-spin' />
          {t('Loading...')}
        </div>
      </TableCell>
    </TableRow>
  )
}

function EmptyRow(props: { colSpan: number; label: string }) {
  return (
    <TableRow>
      <TableCell colSpan={props.colSpan}>
        <div className='text-muted-foreground py-8 text-center text-sm'>
          {props.label}
        </div>
      </TableCell>
    </TableRow>
  )
}
