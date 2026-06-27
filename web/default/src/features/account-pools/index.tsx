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
import { useEffect, useMemo, useRef, useState, type FormEvent } from 'react'
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
  Pencil,
  Plus,
  Power,
  PowerOff,
  Radar,
  Save,
  Trash2,
  Upload,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { useMediaQuery } from '@/hooks'
import { getUserModels } from '@/lib/api'
import { formatTimestamp } from '@/lib/format'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { ConfirmDialog } from '@/components/confirm-dialog'
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
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
import { MultiSelect } from '@/components/multi-select'
import { StatusBadge, type StatusVariant } from '@/components/status-badge'
import { TableId } from '@/components/table-id'
import { getChannels } from '@/features/channels/api'
import {
  CHANNEL_TYPES,
  CHANNEL_STATUS_CONFIG,
  CHANNEL_STATUS_LABELS,
} from '@/features/channels/constants'
import type { Channel } from '@/features/channels/types'
import {
  activateAccountPoolBinding,
  accountPoolsQueryKeys,
  createAccountPool,
  createAccountPoolAccount,
  createAccountPoolBinding,
  createAccountPoolBoundChannel,
  createAccountPoolProxy,
  deleteAccountPool,
  deleteAccountPoolAccount,
  deleteAccountPoolBinding,
  deleteAccountPoolProxy,
  detectAccountPoolAccountCapability,
  detectAccountPoolCapabilities,
  disableAccountPoolBinding,
  importAccountPoolAccounts,
  listAccountPoolAccounts,
  listAccountPoolBindings,
  listAccountPoolProxies,
  listAccountPools,
  updateAccountPool,
  updateAccountPoolAccount,
  updateAccountPoolBinding,
  updateAccountPoolProxy,
} from './api'
import {
  accountToFormValues,
  allowedChannelTypesForPlatform,
  buildAccountPayload,
  buildAccountImportPayload,
  buildAccountPoolProxyOptions,
  buildBoundChannelPayload,
  buildPoolPayload,
  buildProxyPayload,
  defaultChannelTypeForPlatform,
  emptyAccountForm,
  emptyAccountImportForm,
  emptyPoolForm,
  emptyProxyForm,
  normalizeAccountPoolSchedulePolicy,
  platformSupportsOAuthCredential,
  poolToFormValues,
  proxyToFormValues,
  type AccountPoolAccountFormValues,
  type AccountPoolBoundChannelFormValues,
  type AccountImportFormValues,
  type AccountPoolFormValues,
  type AccountPoolProxyFormValues,
} from './lib/account-pool-form'
import type {
  AccountPool,
  AccountPoolAccount,
  AccountPoolBinding,
  AccountPoolCapabilityDetectRequest,
  AccountPoolCapabilityDetectResult,
  AccountPoolCapabilityMode,
  AccountPoolCapabilityPoolResult,
  AccountPoolBindingCreateRequest,
  AccountPoolProxy,
  AccountPoolSchedulePolicy,
  ApiResponse,
} from './types'

type BindingFormValues = {
  channel_id: number
  account_ids: number[]
  model_strategy: string
  fixed_models_text: string
  schedule_policy: AccountPoolSchedulePolicy
  account_retry_times: number
  max_user_concurrency: number
}

type CapabilityDetectFormValues = {
  mode: AccountPoolCapabilityMode
  channel_id: number
  candidate_models_text: string
  apply: boolean
  merge: boolean
  timeout_seconds: number
}

const EMPTY_ACCOUNTS: AccountPoolAccount[] = []
const EMPTY_BINDINGS: AccountPoolBinding[] = []
const EMPTY_PROXIES: AccountPoolProxy[] = []
const EMPTY_CHANNELS: Channel[] = []
const POOL_PLATFORM_OPTIONS = [
  { value: 'openai', label: 'OpenAI / Codex' },
  { value: 'anthropic', label: 'Anthropic (Claude)' },
  { value: 'gemini', label: 'Google Gemini' },
]
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
const ACCOUNT_POOL_CAPABILITY_MODE_OPTIONS = [
  { value: 'auto', label: 'Auto' },
  { value: 'models_endpoint', label: 'Models Endpoint' },
  { value: 'probe_models', label: 'Probe Models' },
]

function translateOptions(
  options: Array<{ value: string; label: string }>,
  t: (key: string) => string
) {
  return options.map((option) => ({
    ...option,
    label: t(option.label),
  }))
}

function emptyBindingForm(defaultSchedulePolicy = ''): BindingFormValues {
  return {
    channel_id: 0,
    account_ids: [],
    model_strategy: 'all',
    fixed_models_text: '',
    schedule_policy: normalizeAccountPoolSchedulePolicy(defaultSchedulePolicy),
    account_retry_times: 0,
    max_user_concurrency: 0,
  }
}

function emptyCapabilityDetectForm(
  defaultChannelID = 0
): CapabilityDetectFormValues {
  return {
    mode: 'auto',
    channel_id: defaultChannelID,
    candidate_models_text: '',
    apply: false,
    merge: true,
    timeout_seconds: 30,
  }
}

function buildBindingPayload(
  values: BindingFormValues
): AccountPoolBindingCreateRequest {
  return {
    channel_id: values.channel_id,
    account_ids: values.account_ids,
    model_strategy: values.model_strategy,
    fixed_models: modelListFromText(values.fixed_models_text),
    schedule_policy: normalizeAccountPoolSchedulePolicy(values.schedule_policy),
    account_retry_times: values.account_retry_times,
    max_user_concurrency: Math.max(0, values.max_user_concurrency),
  }
}

function formatOptionalTimestamp(value: number) {
  return value > 0 ? formatTimestamp(value) : '-'
}

function formatOptionalCount(value: number) {
  if (value <= 0) return '-'
  if (value >= 1_000_000) return `${(value / 1_000_000).toFixed(2)}M`
  if (value >= 1_000) return `${(value / 1_000).toFixed(2)}K`
  return String(value)
}

function formatOptionalMilliseconds(value: number) {
  return value > 0 ? `${Math.round(value)} ms` : '-'
}

function formatOptionalPercent(numerator: number, denominator: number) {
  if (numerator <= 0 || denominator <= 0) return '-'
  return `${((numerator / denominator) * 100).toFixed(1)}%`
}

function averageMetric(total: number, count: number) {
  return count > 0 ? total / count : 0
}

function apiErrorMessage<T>(result: ApiResponse<T>, fallback: string) {
  return result.message || fallback
}

function queryErrorMessage(error: unknown) {
  return error instanceof Error ? error.message : String(error)
}

function useQueryErrorToast(error: unknown) {
  useEffect(() => {
    if (error) {
      toast.error(queryErrorMessage(error))
    }
  }, [error])
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

function schedulePolicyLabel(policy?: string) {
  const normalized = policy || 'round_robin'
  return (
    SCHEDULE_POLICY_OPTIONS.find((option) => option.value === normalized)
      ?.label ?? normalized
  )
}

function proxyReferenceLabel(proxyID: number, proxyNames: Map<number, string>) {
  if (proxyID <= 0) return '-'
  const name = proxyNames.get(proxyID)
  return name ? `${name} (#${proxyID})` : `#${proxyID}`
}

function modelListFromText(value: string): string[] {
  return value
    .split(/[,，\n\r]+/)
    .map((item) => item.trim())
    .filter(Boolean)
}

function modelListToText(values: string[]) {
  return values.join(', ')
}

function capabilityStatusVariant(status?: string): StatusVariant {
  switch (status) {
    case 'success':
      return 'success'
    case 'partial':
      return 'warning'
    case 'auth_error':
    case 'network_error':
    case 'upstream_error':
    case 'config_error':
      return 'danger'
    case 'unsupported':
      return 'neutral'
    default:
      return 'neutral'
  }
}

function capabilityStatusLabel(status?: string) {
  switch (status) {
    case 'success':
      return 'Success'
    case 'partial':
      return 'Partial'
    case 'unsupported':
      return 'Unsupported'
    case 'auth_error':
      return 'Authentication Error'
    case 'network_error':
      return 'Network Error'
    case 'upstream_error':
      return 'Upstream Error'
    case 'config_error':
      return 'Configuration Error'
    default:
      return status ? status.replace(/_/g, ' ') : 'Unknown'
  }
}

type CapabilityDetectScope = 'account' | 'pool'

type CapabilityDetectRequestContext = {
  scope: CapabilityDetectScope
  poolID: number
  accountID?: number
  channelID: number
  dialogNonce: number
  requestNonce: number
}

type CapabilityDetectMutationInput = {
  values: CapabilityDetectFormValues
  context: CapabilityDetectRequestContext
}

type CapabilityDetectMutationResult =
  | {
      context: CapabilityDetectRequestContext & {
        scope: 'account'
        accountID: number
      }
      response: ApiResponse<AccountPoolCapabilityDetectResult>
    }
  | {
      context: CapabilityDetectRequestContext & {
        scope: 'pool'
      }
      response: ApiResponse<AccountPoolCapabilityPoolResult>
    }

function isActiveCapabilityDetectContext(
  active: CapabilityDetectRequestContext | null,
  target: CapabilityDetectRequestContext
) {
  return (
    active?.scope === target.scope &&
    active.poolID === target.poolID &&
    active.accountID === target.accountID &&
    active.channelID === target.channelID &&
    active.dialogNonce === target.dialogNonce &&
    active.requestNonce === target.requestNonce
  )
}

function useAccountPoolModelSelect(enabled = true) {
  const { t } = useTranslation()
  const modelsQuery = useQuery({
    queryKey: ['user-models', 'account-pools'],
    queryFn: getUserModels,
    enabled,
  })
  const modelSelectOptions = useMemo(
    () =>
      (modelsQuery.data?.data ?? []).map((model) => ({
        value: model,
        label: model,
      })),
    [modelsQuery.data]
  )

  useEffect(() => {
    if (modelsQuery.error) {
      toast.error(t('Failed to load models'))
    }
  }, [modelsQuery.error, t])

  return {
    modelSelectOptions,
    modelsLoading: modelsQuery.isLoading,
  }
}

function parseAccountPoolJSON<T>(value: string, fallback: T): T {
  if (!value.trim()) {
    return fallback
  }
  try {
    return JSON.parse(value) as T
  } catch {
    return fallback
  }
}

function bindingToFormValues(binding: AccountPoolBinding): BindingFormValues {
  const filter = parseAccountPoolJSON<{ account_ids?: number[] }>(
    binding.account_filter_config,
    {}
  )
  const policy = parseAccountPoolJSON<{
    strategy?: string
    fixed_models?: string[]
  }>(binding.model_policy, {})

  return {
    channel_id: binding.channel_id,
    account_ids: Array.isArray(filter.account_ids) ? filter.account_ids : [],
    model_strategy: policy.strategy || 'all',
    fixed_models_text: Array.isArray(policy.fixed_models)
      ? policy.fixed_models.join(', ')
      : '',
    schedule_policy: normalizeAccountPoolSchedulePolicy(binding.schedule_policy),
    account_retry_times: binding.account_retry_times,
    max_user_concurrency: binding.max_user_concurrency,
  }
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
  onEdit: (pool: AccountPool) => void
  onDelete: (pool: AccountPool) => void
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
          <PoolActions
            row={row}
            onDetails={props.onDetails}
            onEdit={props.onEdit}
            onDelete={props.onDelete}
          />
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
  onEdit: (pool: AccountPool) => void
  onDelete: (pool: AccountPool) => void
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
          <DropdownMenuItem onClick={() => props.onEdit(pool)}>
            <Pencil />
            {t('Edit')}
          </DropdownMenuItem>
          <DropdownMenuItem onClick={() => props.onDelete(pool)}>
            <Trash2 />
            {t('Delete')}
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
  const [editingPool, setEditingPool] = useState<AccountPool>()
  const [deletingPool, setDeletingPool] = useState<AccountPool>()
  const [selectedPool, setSelectedPool] = useState<AccountPool>()
  const [accountSheetOpen, setAccountSheetOpen] = useState(false)
  const [editingAccount, setEditingAccount] = useState<AccountPoolAccount>()
  const [deletingAccount, setDeletingAccount] = useState<AccountPoolAccount>()
  const [capabilityDialogOpen, setCapabilityDialogOpen] = useState(false)
  const [detectingAccount, setDetectingAccount] = useState<AccountPoolAccount>()
  const [accountImportOpen, setAccountImportOpen] = useState(false)
  const [proxySheetOpen, setProxySheetOpen] = useState(false)
  const [editingProxy, setEditingProxy] = useState<AccountPoolProxy>()
  const [deletingProxy, setDeletingProxy] = useState<AccountPoolProxy>()
  const [editingBinding, setEditingBinding] = useState<AccountPoolBinding>()
  const [deletingBinding, setDeletingBinding] = useState<AccountPoolBinding>()
  const [boundChannelDialogOpen, setBoundChannelDialogOpen] = useState(false)
  const [loadedAccountsByPool, setLoadedAccountsByPool] = useState<
    Record<number, AccountPoolAccount[] | undefined>
  >({})
  const [bindingFormResetVersion, setBindingFormResetVersion] = useState(0)

  const poolsQuery = useQuery({
    queryKey: accountPoolsQueryKeys.list(),
    queryFn: async () => {
      const result = await listAccountPools()
      if (!result.success) {
        throw new Error(result.message || t('Failed to load account pools'))
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
        throw new Error(result.message || t('Failed to load pool accounts'))
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
        throw new Error(result.message || t('Failed to load pool bindings'))
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
        throw new Error(
          result.message || t('Failed to load account pool proxies')
        )
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

  useQueryErrorToast(poolsQuery.error)
  useQueryErrorToast(accountsQuery.error)
  useQueryErrorToast(bindingsQuery.error)
  useQueryErrorToast(proxiesQuery.error)

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
      setEditingPool(undefined)
      invalidatePools()
    },
    onError: (error) => {
      toast.error(error instanceof Error ? error.message : t('Request failed'))
    },
  })

  const updatePoolMutation = useMutation({
    mutationFn: async (values: AccountPoolFormValues) => {
      if (!editingPool) {
        throw new Error(t('Select an account pool first'))
      }
      return updateAccountPool(editingPool.id, buildPoolPayload(values))
    },
    onSuccess: (result) => {
      if (!result.success) {
        toast.error(apiErrorMessage(result, t('Failed to update account pool')))
        return
      }
      toast.success(t('Account pool updated'))
      setPoolSheetOpen(false)
      setEditingPool(undefined)
      invalidatePools()
    },
    onError: (error) => {
      toast.error(error instanceof Error ? error.message : t('Request failed'))
    },
  })

  const deletePoolMutation = useMutation({
    mutationFn: async (pool: AccountPool) => deleteAccountPool(pool.id),
    onSuccess: (result) => {
      if (!result.success) {
        toast.error(apiErrorMessage(result, t('Failed to delete account pool')))
        return
      }
      toast.success(t('Account pool deleted'))
      setDeletingPool(undefined)
      setSelectedPool(undefined)
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

  const updateAccountMutation = useMutation({
    mutationFn: async (values: AccountPoolAccountFormValues) => {
      if (!selectedPoolID || !editingAccount) {
        throw new Error(t('Select an account first'))
      }
      return updateAccountPoolAccount(
        selectedPoolID,
        editingAccount.id,
        buildAccountPayload(values)
      )
    },
    onSuccess: (result) => {
      if (!result.success) {
        toast.error(apiErrorMessage(result, t('Failed to update account')))
        return
      }
      toast.success(t('Account updated'))
      setAccountSheetOpen(false)
      setEditingAccount(undefined)
      invalidatePools()
    },
    onError: (error) => {
      toast.error(error instanceof Error ? error.message : t('Request failed'))
    },
  })

  const updateAccountStatusMutation = useMutation({
    mutationFn: async (params: {
      account: AccountPoolAccount
      status: string
    }) => {
      if (!selectedPoolID) {
        throw new Error(t('Select an account pool first'))
      }
      return updateAccountPoolAccount(
        selectedPoolID,
        params.account.id,
        buildAccountPayload({
          ...accountToFormValues(params.account),
          status: params.status,
        })
      )
    },
    onSuccess: (result) => {
      if (!result.success) {
        toast.error(apiErrorMessage(result, t('Failed to update account')))
        return
      }
      toast.success(t('Account updated'))
      invalidatePools()
    },
    onError: (error) => {
      toast.error(error instanceof Error ? error.message : t('Request failed'))
    },
  })

  const deleteAccountMutation = useMutation({
    mutationFn: async (account: AccountPoolAccount) => {
      if (!selectedPoolID) {
        throw new Error(t('Select an account pool first'))
      }
      return deleteAccountPoolAccount(selectedPoolID, account.id)
    },
    onSuccess: (result) => {
      if (!result.success) {
        toast.error(apiErrorMessage(result, t('Failed to delete account')))
        return
      }
      toast.success(t('Account deleted'))
      setDeletingAccount(undefined)
      invalidatePools()
    },
    onError: (error) => {
      toast.error(error instanceof Error ? error.message : t('Request failed'))
    },
  })

  const importAccountsMutation = useMutation({
    mutationFn: async (values: AccountImportFormValues) => {
      if (!selectedPoolID) {
        throw new Error(t('Select an account pool first'))
      }
      return importAccountPoolAccounts(
        selectedPoolID,
        buildAccountImportPayload(values)
      )
    },
    onSuccess: (result) => {
      if (!result.success) {
        toast.error(apiErrorMessage(result, t('Failed to import accounts')))
        return
      }
      const data = result.data
      toast.success(
        t('Imported {{count}} account(s)', { count: data?.imported ?? 0 })
      )
      if (data?.skipped || data?.failed) {
        toast.warning(
          t('Skipped {{skipped}} / failed {{failed}}', {
            skipped: data.skipped,
            failed: data.failed,
          })
        )
      }
      setAccountImportOpen(false)
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
      setEditingProxy(undefined)
      invalidatePools()
    },
    onError: (error) => {
      toast.error(error instanceof Error ? error.message : t('Request failed'))
    },
  })

  const updateProxyMutation = useMutation({
    mutationFn: async (values: AccountPoolProxyFormValues) => {
      if (!editingProxy) {
        throw new Error(t('Select a proxy first'))
      }
      return updateAccountPoolProxy(editingProxy.id, buildProxyPayload(values))
    },
    onSuccess: (result) => {
      if (!result.success) {
        toast.error(apiErrorMessage(result, t('Failed to update proxy')))
        return
      }
      toast.success(t('Proxy updated'))
      setProxySheetOpen(false)
      setEditingProxy(undefined)
      invalidatePools()
    },
    onError: (error) => {
      toast.error(error instanceof Error ? error.message : t('Request failed'))
    },
  })

  const deleteProxyMutation = useMutation({
    mutationFn: async (proxy: AccountPoolProxy) =>
      deleteAccountPoolProxy(proxy.id),
    onSuccess: (result) => {
      if (!result.success) {
        toast.error(apiErrorMessage(result, t('Failed to delete proxy')))
        return
      }
      toast.success(t('Proxy deleted'))
      setDeletingProxy(undefined)
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
      return createAccountPoolBinding(selectedPoolID, buildBindingPayload(values))
    },
    onSuccess: (result) => {
      if (!result.success) {
        toast.error(apiErrorMessage(result, t('Failed to create binding')))
        return
      }
      toast.success(t('Binding created'))
      setEditingBinding(undefined)
      setBindingFormResetVersion((version) => version + 1)
      invalidatePools()
    },
    onError: (error) => {
      toast.error(error instanceof Error ? error.message : t('Request failed'))
    },
  })

  const createBoundChannelMutation = useMutation({
    mutationFn: async (values: AccountPoolBoundChannelFormValues) => {
      if (!selectedPoolID) {
        throw new Error(t('Select an account pool first'))
      }
      return createAccountPoolBoundChannel(
        selectedPoolID,
        buildBoundChannelPayload(values)
      )
    },
    onSuccess: (result) => {
      if (!result.success) {
        toast.error(
          apiErrorMessage(result, t('Failed to create account pool channel'))
        )
        return
      }
      toast.success(t('Account pool channel created'))
      setBoundChannelDialogOpen(false)
      invalidatePools()
      queryClient.invalidateQueries({ queryKey: ['channels'] })
    },
    onError: (error) => {
      toast.error(error instanceof Error ? error.message : t('Request failed'))
    },
  })

  const updateBindingMutation = useMutation({
    mutationFn: async (values: BindingFormValues) => {
      if (!selectedPoolID || !editingBinding) {
        throw new Error(t('Select a binding first'))
      }
      return updateAccountPoolBinding(
        selectedPoolID,
        editingBinding.id,
        buildBindingPayload(values)
      )
    },
    onSuccess: (result) => {
      if (!result.success) {
        toast.error(apiErrorMessage(result, t('Failed to update binding')))
        return
      }
      toast.success(t('Binding updated'))
      setEditingBinding(undefined)
      setBindingFormResetVersion((version) => version + 1)
      invalidatePools()
    },
    onError: (error) => {
      toast.error(error instanceof Error ? error.message : t('Request failed'))
    },
  })

  const setBindingStatusMutation = useMutation({
    mutationFn: async (params: {
      binding: AccountPoolBinding
      status: 'enabled' | 'disabled'
    }) => {
      if (!selectedPoolID) {
        throw new Error(t('Select an account pool first'))
      }
      if (params.status === 'enabled') {
        return activateAccountPoolBinding(selectedPoolID, params.binding.id)
      }
      return disableAccountPoolBinding(selectedPoolID, params.binding.id)
    },
    onSuccess: (result) => {
      if (!result.success) {
        toast.error(apiErrorMessage(result, t('Failed to update binding')))
        return
      }
      toast.success(t('Binding updated'))
      invalidatePools()
    },
    onError: (error) => {
      toast.error(error instanceof Error ? error.message : t('Request failed'))
    },
  })

  const deleteBindingMutation = useMutation({
    mutationFn: async (binding: AccountPoolBinding) => {
      if (!selectedPoolID) {
        throw new Error(t('Select an account pool first'))
      }
      return deleteAccountPoolBinding(selectedPoolID, binding.id)
    },
    onSuccess: (result) => {
      if (!result.success) {
        toast.error(apiErrorMessage(result, t('Failed to delete binding')))
        return
      }
      toast.success(t('Binding deleted'))
      setDeletingBinding(undefined)
      setEditingBinding(undefined)
      invalidatePools()
    },
    onError: (error) => {
      toast.error(error instanceof Error ? error.message : t('Request failed'))
    },
  })

  const columns = useAccountPoolColumns({
    accountsByPool: loadedAccountsByPool,
    onDetails: (pool) => setSelectedPool(pool),
    onEdit: (pool) => {
      setEditingPool(pool)
      setPoolSheetOpen(true)
    },
    onDelete: setDeletingPool,
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
          <Button
            type='button'
            onClick={() => {
              setEditingPool(undefined)
              setPoolSheetOpen(true)
            }}
          >
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
        pool={editingPool}
        proxies={proxiesQuery.data ?? EMPTY_PROXIES}
        isSubmitting={createPoolMutation.isPending || updatePoolMutation.isPending}
        onOpenChange={(open) => {
          setPoolSheetOpen(open)
          if (!open) setEditingPool(undefined)
        }}
        onSubmit={(values) => {
          if (editingPool) {
            updatePoolMutation.mutate(values)
          } else {
            createPoolMutation.mutate(values)
          }
        }}
      />
      <ConfirmDialog
        open={Boolean(deletingPool)}
        onOpenChange={(open) => !open && setDeletingPool(undefined)}
        title={t('Delete account pool?')}
        desc={
          deletingPool
            ? t('Delete account pool {{name}} and remove its bindings.', {
                name: deletingPool.name,
              })
            : ''
        }
        destructive
        confirmText={t('Delete')}
        isLoading={deletePoolMutation.isPending}
        handleConfirm={() => {
          if (deletingPool) deletePoolMutation.mutate(deletingPool)
        }}
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
        editingBinding={editingBinding}
        bindingFormResetVersion={bindingFormResetVersion}
        bindingSubmitting={
          createBindingMutation.isPending || updateBindingMutation.isPending
        }
        onOpenChange={(open) => {
          if (!open) {
            setSelectedPool(undefined)
            setEditingBinding(undefined)
            setBoundChannelDialogOpen(false)
            setCapabilityDialogOpen(false)
            setDetectingAccount(undefined)
          }
        }}
        onCreateAccount={() => {
          setEditingAccount(undefined)
          setAccountSheetOpen(true)
        }}
        onImportAccounts={() => setAccountImportOpen(true)}
        onEditAccount={(account) => {
          setEditingAccount(account)
          setAccountSheetOpen(true)
        }}
        onDetectAccount={(account) => {
          setDetectingAccount(account)
          setCapabilityDialogOpen(true)
        }}
        onBatchDetectModels={() => {
          setDetectingAccount(undefined)
          setCapabilityDialogOpen(true)
        }}
        onDeleteAccount={setDeletingAccount}
        onSetAccountStatus={(account, status) =>
          updateAccountStatusMutation.mutate({ account, status })
        }
        onCreateProxy={() => {
          setEditingProxy(undefined)
          setProxySheetOpen(true)
        }}
        onEditProxy={(proxy) => {
          setEditingProxy(proxy)
          setProxySheetOpen(true)
        }}
        onDeleteProxy={setDeletingProxy}
        onCreateBoundChannel={() => setBoundChannelDialogOpen(true)}
        onCreateBinding={(values) => createBindingMutation.mutate(values)}
        onUpdateBinding={(values) => updateBindingMutation.mutate(values)}
        onEditBinding={setEditingBinding}
        onDeleteBinding={setDeletingBinding}
        onCancelBindingEdit={() => setEditingBinding(undefined)}
        onSetBindingStatus={(binding, status) =>
          setBindingStatusMutation.mutate({ binding, status })
        }
      />
      <ConfirmDialog
        open={Boolean(deletingBinding)}
        onOpenChange={(open) => !open && setDeletingBinding(undefined)}
        title={t('Delete binding?')}
        desc={
          deletingBinding
            ? t('Delete binding for channel {{name}}. The channel itself will be kept.', {
                name: deletingBinding.channel_name || `#${deletingBinding.channel_id}`,
              })
            : ''
        }
        destructive
        confirmText={t('Delete')}
        isLoading={deleteBindingMutation.isPending}
        handleConfirm={() => {
          if (deletingBinding) deleteBindingMutation.mutate(deletingBinding)
        }}
      />
      <BoundChannelDialog
        open={boundChannelDialogOpen}
        pool={selectedPool}
        isSubmitting={createBoundChannelMutation.isPending}
        onOpenChange={setBoundChannelDialogOpen}
        onSubmit={(values) => createBoundChannelMutation.mutate(values)}
      />
      <AccountImportDialog
        open={accountImportOpen}
        pool={selectedPool}
        proxies={proxiesQuery.data ?? EMPTY_PROXIES}
        isSubmitting={importAccountsMutation.isPending}
        onOpenChange={setAccountImportOpen}
        onSubmit={(values) => importAccountsMutation.mutate(values)}
      />
      <CapabilityDetectDialog
        open={capabilityDialogOpen}
        pool={selectedPool}
        bindings={bindingsQuery.data ?? EMPTY_BINDINGS}
        accounts={accountsQuery.data ?? EMPTY_ACCOUNTS}
        account={detectingAccount}
        onOpenChange={(open) => {
          setCapabilityDialogOpen(open)
          if (!open) {
            setDetectingAccount(undefined)
          }
        }}
      />
      <AccountFormSheet
        key={
          accountSheetOpen && selectedPool
            ? `account-${selectedPool.id}-${editingAccount?.id ?? 'new'}`
            : 'account-closed'
        }
        open={accountSheetOpen}
        pool={selectedPool}
        account={editingAccount}
        proxies={proxiesQuery.data ?? EMPTY_PROXIES}
        isSubmitting={
          createAccountMutation.isPending || updateAccountMutation.isPending
        }
        onOpenChange={(open) => {
          setAccountSheetOpen(open)
          if (!open) setEditingAccount(undefined)
        }}
        onSubmit={(values) => {
          try {
            buildAccountPayload(values)
          } catch (error) {
            toast.error(
              error instanceof Error ? t(error.message) : t('Invalid account')
            )
            return
          }
          if (editingAccount) {
            updateAccountMutation.mutate(values)
          } else {
            createAccountMutation.mutate(values)
          }
        }}
      />
      <ConfirmDialog
        open={Boolean(deletingAccount)}
        onOpenChange={(open) => !open && setDeletingAccount(undefined)}
        title={t('Delete account?')}
        desc={
          deletingAccount
            ? t('Delete account {{name}} from this pool.', {
                name: deletingAccount.name,
              })
            : ''
        }
        destructive
        confirmText={t('Delete')}
        isLoading={deleteAccountMutation.isPending}
        handleConfirm={() => {
          if (deletingAccount) deleteAccountMutation.mutate(deletingAccount)
        }}
      />
      <ProxyFormSheet
        open={proxySheetOpen}
        proxy={editingProxy}
        proxies={proxiesQuery.data ?? EMPTY_PROXIES}
        isSubmitting={createProxyMutation.isPending || updateProxyMutation.isPending}
        onOpenChange={(open) => {
          setProxySheetOpen(open)
          if (!open) setEditingProxy(undefined)
        }}
        onSubmit={(values) => {
          if (editingProxy) {
            updateProxyMutation.mutate(values)
          } else {
            createProxyMutation.mutate(values)
          }
        }}
      />
      <ConfirmDialog
        open={Boolean(deletingProxy)}
        onOpenChange={(open) => !open && setDeletingProxy(undefined)}
        title={t('Delete proxy?')}
        desc={
          deletingProxy
            ? t('Delete proxy {{name}} and clear existing references.', {
                name: deletingProxy.name,
              })
            : ''
        }
        destructive
        confirmText={t('Delete')}
        isLoading={deleteProxyMutation.isPending}
        handleConfirm={() => {
          if (deletingProxy) deleteProxyMutation.mutate(deletingProxy)
        }}
      />
    </>
  )
}

function PoolFormSheet(props: {
  open: boolean
  pool?: AccountPool
  proxies: AccountPoolProxy[]
  isSubmitting: boolean
  onOpenChange: (open: boolean) => void
  onSubmit: (values: AccountPoolFormValues) => void
}) {
  const { t } = useTranslation()
  const [form, setForm] = useState<AccountPoolFormValues>(emptyPoolForm())
  const platformOptions = useMemo(
    () => translateOptions(POOL_PLATFORM_OPTIONS, t),
    [t]
  )
  const schedulePolicyOptions = useMemo(
    () => translateOptions(SCHEDULE_POLICY_OPTIONS, t),
    [t]
  )
  const capabilityModeOptions = useMemo(
    () =>
      translateOptions(
        ACCOUNT_POOL_CAPABILITY_MODE_OPTIONS.filter(
          (option) => option.value !== 'auto'
        ),
        t
      ),
    [t]
  )
  const proxyOptions = useMemo(
    () => buildAccountPoolProxyOptions(props.proxies, t('No Proxy')),
    [props.proxies, t]
  )

  useEffect(() => {
    if (props.open) {
      setForm(props.pool ? poolToFormValues(props.pool) : emptyPoolForm())
    }
  }, [props.open, props.pool])

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
          <SheetTitle>
            {props.pool ? t('Edit Account Pool') : t('Add Account Pool')}
          </SheetTitle>
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
                items={platformOptions}
                value={form.platform}
                onValueChange={(value) => value && setField('platform', value)}
              >
                <SelectTrigger id='account-pool-platform'>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent alignItemWithTrigger={false}>
                  <SelectGroup>
                    {platformOptions.map((option) => (
                      <SelectItem key={option.value} value={option.value}>
                        {option.label}
                      </SelectItem>
                    ))}
                  </SelectGroup>
                </SelectContent>
              </Select>
            </FieldBlock>
            <FieldBlock
              label={t('Default Proxy')}
              htmlFor='account-pool-default-proxy'
            >
              <Select
                items={proxyOptions}
                value={String(form.default_proxy_id)}
                onValueChange={(value) =>
                  setField('default_proxy_id', value ? Number(value) : 0)
                }
              >
                <SelectTrigger id='account-pool-default-proxy'>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent alignItemWithTrigger={false}>
                  <SelectGroup>
                    {proxyOptions.map((option) => (
                      <SelectItem key={option.value} value={option.value}>
                        {option.label}
                      </SelectItem>
                    ))}
                  </SelectGroup>
                </SelectContent>
              </Select>
            </FieldBlock>
            <FieldBlock
              label={t('Schedule Policy')}
              htmlFor='account-pool-schedule-policy'
            >
              <Select
                items={schedulePolicyOptions}
                value={form.default_schedule_policy || 'round_robin'}
                onValueChange={(value) =>
                  value &&
                  setField(
                    'default_schedule_policy',
                    normalizeAccountPoolSchedulePolicy(value)
                  )
                }
              >
                <SelectTrigger id='account-pool-schedule-policy'>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent alignItemWithTrigger={false}>
                  <SelectGroup>
                    {schedulePolicyOptions.map((option) => (
                      <SelectItem key={option.value} value={option.value}>
                        {option.label}
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
            <div className='space-y-4 border-t pt-4'>
              <div className={sideDrawerSwitchItemClassName()}>
                <div className='min-w-0'>
                  <FieldLabel htmlFor='account-pool-capability-check'>
                    {t('Scheduled Capability Detection')}
                  </FieldLabel>
                  <p className='text-muted-foreground mt-1 text-xs'>
                    {t('Refresh account supported models automatically')}
                  </p>
                </div>
                <Switch
                  id='account-pool-capability-check'
                  checked={form.capability_check_enabled}
                  onCheckedChange={(checked) =>
                    setField('capability_check_enabled', checked)
                  }
                />
              </div>
              {form.capability_check_enabled ? (
                <div className='grid gap-4 sm:grid-cols-2'>
                  <FieldBlock
                    label={t('Detection Interval Minutes')}
                    htmlFor='account-pool-capability-interval'
                  >
                    <Input
                      id='account-pool-capability-interval'
                      type='number'
                      min={5}
                      value={form.capability_check_interval_minutes}
                      onChange={(event) =>
                        setField(
                          'capability_check_interval_minutes',
                          Number(event.target.value)
                        )
                      }
                    />
                  </FieldBlock>
                  <FieldBlock
                    label={t('Detection Mode')}
                    htmlFor='account-pool-capability-mode'
                  >
                    <Select
                      items={capabilityModeOptions}
                      value={form.capability_check_mode || 'models_endpoint'}
                      onValueChange={(value) =>
                        value &&
                        setField(
                          'capability_check_mode',
                          value as AccountPoolCapabilityMode
                        )
                      }
                    >
                      <SelectTrigger id='account-pool-capability-mode'>
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent alignItemWithTrigger={false}>
                        <SelectGroup>
                          {capabilityModeOptions.map((option) => (
                            <SelectItem
                              key={option.value}
                              value={option.value}
                            >
                              {option.label}
                            </SelectItem>
                          ))}
                        </SelectGroup>
                      </SelectContent>
                    </Select>
                  </FieldBlock>
                  <FieldBlock
                    label={t('Detection Channel ID')}
                    htmlFor='account-pool-capability-channel'
                  >
                    <Input
                      id='account-pool-capability-channel'
                      type='number'
                      min={0}
                      value={form.capability_check_channel_id}
                      onChange={(event) =>
                        setField(
                          'capability_check_channel_id',
                          Number(event.target.value)
                        )
                      }
                    />
                  </FieldBlock>
                  <FieldBlock
                    label={t('Detection Timeout Seconds')}
                    htmlFor='account-pool-capability-timeout'
                  >
                    <Input
                      id='account-pool-capability-timeout'
                      type='number'
                      min={1}
                      max={300}
                      value={form.capability_check_timeout_seconds}
                      onChange={(event) =>
                        setField(
                          'capability_check_timeout_seconds',
                          Number(event.target.value)
                        )
                      }
                    />
                  </FieldBlock>
                  <div className='sm:col-span-2'>
                    <FieldBlock
                      label={t('Candidate Models')}
                      htmlFor='account-pool-capability-models'
                    >
                      <Textarea
                        id='account-pool-capability-models'
                        value={form.capability_check_models_text}
                        placeholder={'gpt-5\ngpt-5-mini'}
                        onChange={(event) =>
                          setField(
                            'capability_check_models_text',
                            event.target.value
                          )
                        }
                      />
                    </FieldBlock>
                  </div>
                  <div className='sm:col-span-2'>
                    <div className={sideDrawerSwitchItemClassName()}>
                      <div className='min-w-0'>
                        <FieldLabel htmlFor='account-pool-capability-merge'>
                          {t('Merge Detected Models')}
                        </FieldLabel>
                        <p className='text-muted-foreground mt-1 text-xs'>
                          {t('Keep existing supported models when applying')}
                        </p>
                      </div>
                      <Switch
                        id='account-pool-capability-merge'
                        checked={form.capability_check_merge}
                        onCheckedChange={(checked) =>
                          setField('capability_check_merge', checked)
                        }
                      />
                    </div>
                  </div>
                </div>
              ) : null}
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
            {props.pool ? t('Save Changes') : t('Create')}
          </Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  )
}

function BoundChannelDialog(props: {
  open: boolean
  pool?: AccountPool
  isSubmitting: boolean
  onOpenChange: (open: boolean) => void
  onSubmit: (values: AccountPoolBoundChannelFormValues) => void
}) {
  const { t } = useTranslation()
  const platform = props.pool?.platform ?? 'openai'
  const defaultChannelType = String(defaultChannelTypeForPlatform(platform))
  const channelTypeOptions = useMemo(
    () =>
      allowedChannelTypesForPlatform(platform).map((type) => ({
        value: String(type),
        label: CHANNEL_TYPES[type as keyof typeof CHANNEL_TYPES] ?? String(type),
      })),
    [platform]
  )
  const [name, setName] = useState('')
  const [channelType, setChannelType] = useState(defaultChannelType)
  const [fixedModelsText, setFixedModelsText] = useState('')
  const { modelSelectOptions, modelsLoading } = useAccountPoolModelSelect(
    props.open
  )

  useEffect(() => {
    if (props.open) {
      setName(props.pool ? `${props.pool.name} Channel` : '')
      setChannelType(defaultChannelType)
      setFixedModelsText('')
    }
  }, [props.open, props.pool, defaultChannelType])

  const handleSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const trimmedName = name.trim()
    if (!trimmedName) {
      toast.error(t('Channel name is required'))
      return
    }
    const fixedModels = modelListFromText(fixedModelsText)
    if (fixedModels.length === 0) {
      toast.error(t('Select at least one model'))
      return
    }
    props.onSubmit({
      name: trimmedName,
      type:
        Number.parseInt(channelType, 10) ||
        defaultChannelTypeForPlatform(platform),
      fixed_models_text: modelListToText(fixedModels),
    })
  }

  return (
    <Dialog open={props.open} onOpenChange={props.onOpenChange}>
      <DialogContent className='sm:max-w-[520px]'>
        <DialogHeader>
          <DialogTitle>{t('Create Account Pool Channel')}</DialogTitle>
          <DialogDescription>
            {t('Create a disabled local channel and bind it to this pool.')}
          </DialogDescription>
        </DialogHeader>
        <form
          id='account-pool-bound-channel-form'
          className='flex flex-col gap-4'
          onSubmit={handleSubmit}
        >
          <FieldBlock
            label={t('Channel Name')}
            htmlFor='account-pool-bound-channel-name'
          >
            <Input
              id='account-pool-bound-channel-name'
              value={name}
              onChange={(event) => setName(event.target.value)}
              autoFocus
            />
          </FieldBlock>
          <FieldBlock
            label={t('Channel Type')}
            htmlFor='account-pool-bound-channel-type'
          >
            <Select
              value={channelType}
              onValueChange={(value) =>
                setChannelType(value ?? defaultChannelType)
              }
            >
              <SelectTrigger id='account-pool-bound-channel-type'>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectGroup>
                  {channelTypeOptions.map((option) => (
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
            htmlFor='account-pool-bound-channel-fixed-models'
          >
            <MultiSelect
              id='account-pool-bound-channel-fixed-models'
              options={modelSelectOptions}
              selected={modelListFromText(fixedModelsText)}
              onChange={(values) => setFixedModelsText(modelListToText(values))}
              placeholder={t('Select models or add custom ones')}
              allowCreate
              createLabel='Add custom model "{{value}}"'
              emptyText={t('No models found')}
              maxVisibleChips={6}
              copyChipOnClick
              disabled={modelsLoading}
            />
          </FieldBlock>
        </form>
        <DialogFooter>
          <DialogClose render={<Button variant='outline' />}>
            {t('Cancel')}
          </DialogClose>
          <Button
            type='submit'
            form='account-pool-bound-channel-form'
            disabled={props.isSubmitting}
          >
            {props.isSubmitting ? (
              <Loader2 data-icon='inline-start' className='animate-spin' />
            ) : (
              <Plus data-icon='inline-start' />
            )}
            {t('Create Channel')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function CapabilityDetectDialog(props: {
  open: boolean
  pool?: AccountPool
  bindings: AccountPoolBinding[]
  accounts: AccountPoolAccount[]
  account?: AccountPoolAccount
  onOpenChange: (open: boolean) => void
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const defaultChannelID =
    props.bindings.length === 1 ? props.bindings[0].channel_id : 0
  const [form, setForm] = useState<CapabilityDetectFormValues>(
    emptyCapabilityDetectForm(defaultChannelID)
  )
  const [singleResult, setSingleResult] =
    useState<AccountPoolCapabilityDetectResult>()
  const [poolResult, setPoolResult] = useState<AccountPoolCapabilityPoolResult>()
  const [pendingContext, setPendingContext] =
    useState<CapabilityDetectRequestContext | null>(null)
  const activeContextRef = useRef<CapabilityDetectRequestContext | null>(null)
  const dialogNonceRef = useRef(0)
  const requestNonceRef = useRef(0)
  const hasBindings = props.bindings.length > 0
  const modeOptions = useMemo(
    () => translateOptions(ACCOUNT_POOL_CAPABILITY_MODE_OPTIONS, t),
    [t]
  )
  const bindingOptions = useMemo(
    () =>
      props.bindings.map((binding) => ({
        value: String(binding.channel_id),
        label:
          binding.channel_name || `${t('Channel')} #${binding.channel_id}`,
      })),
    [props.bindings, t]
  )
  const accountNameByID = useMemo(
    () =>
      new Map(
        props.accounts.map((account) => [account.id, account.name || `#${account.id}`])
      ),
    [props.accounts]
  )

  useEffect(() => {
    if (props.open) {
      dialogNonceRef.current += 1
      requestNonceRef.current = 0
      setForm(emptyCapabilityDetectForm(defaultChannelID))
      setSingleResult(undefined)
      setPoolResult(undefined)
      setPendingContext(null)
      activeContextRef.current = {
        scope: props.account ? 'account' : 'pool',
        poolID: props.pool?.id ?? 0,
        accountID: props.account?.id,
        channelID: defaultChannelID,
        dialogNonce: dialogNonceRef.current,
        requestNonce: requestNonceRef.current,
      }
    } else {
      setPendingContext(null)
      activeContextRef.current = null
    }
  }, [defaultChannelID, props.account?.id, props.open, props.pool?.id])

  const setField = <K extends keyof CapabilityDetectFormValues>(
    key: K,
    value: CapabilityDetectFormValues[K]
  ) => setForm((previous) => ({ ...previous, [key]: value }))

  const detectMutation = useMutation<
    CapabilityDetectMutationResult,
    Error,
    CapabilityDetectMutationInput
  >({
    mutationFn: async ({
      values,
      context,
    }: CapabilityDetectMutationInput) => {
      if (!context.poolID) {
        throw new Error(t('Select an account pool first'))
      }

      const request: AccountPoolCapabilityDetectRequest = {
        mode: values.mode,
        channel_id: values.channel_id,
        candidate_models: modelListFromText(values.candidate_models_text),
        apply: values.apply,
        merge: values.merge,
        model_mapping: {},
        timeout_seconds: values.timeout_seconds,
      }

      if (context.scope === 'account' && context.accountID) {
        const response = await detectAccountPoolAccountCapability(
          context.poolID,
          context.accountID,
          request
        )
        return {
          context: { ...context, scope: 'account', accountID: context.accountID },
          response,
        }
      }

      const response = await detectAccountPoolCapabilities(context.poolID, request)
      return {
        context: { ...context, scope: 'pool' },
        response,
      }
    },
    onMutate: ({ context }) => {
      if (isActiveCapabilityDetectContext(activeContextRef.current, context)) {
        setPendingContext(context)
        setSingleResult(undefined)
        setPoolResult(undefined)
      }
    },
    onSuccess: async (payload) => {
      await queryClient.invalidateQueries({
        queryKey: accountPoolsQueryKeys.accounts(payload.context.poolID),
      })

      if (
        !isActiveCapabilityDetectContext(activeContextRef.current, payload.context)
      ) {
        return
      }

      if (!payload.response.success) {
        toast.error(payload.response.message || t('Detection failed'))
        return
      }

      if (payload.context.scope === 'account') {
        setSingleResult(
          payload.response.data as AccountPoolCapabilityDetectResult | undefined
        )
        setPoolResult(undefined)
      } else {
        setPoolResult(
          payload.response.data as AccountPoolCapabilityPoolResult | undefined
        )
        setSingleResult(undefined)
      }

      toast.success(t('Detection completed'))
    },
    onError: (error, variables) => {
      if (
        !isActiveCapabilityDetectContext(
          activeContextRef.current,
          variables.context
        )
      ) {
        return
      }
      toast.error(error instanceof Error ? error.message : t('Request failed'))
    },
    onSettled: (_payload, _error, variables) => {
      setPendingContext((previous) =>
        previous &&
        isActiveCapabilityDetectContext(previous, variables.context)
          ? null
          : previous
      )
    },
  })

  const isCurrentDetectionPending = Boolean(
    pendingContext &&
      isActiveCapabilityDetectContext(activeContextRef.current, pendingContext)
  )

  const handleSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()

    if (!props.pool) {
      toast.error(t('Select an account pool first'))
      return
    }
    if (!hasBindings) {
      toast.error(t('No bindings found'))
      return
    }
    if (!form.channel_id && props.bindings.length > 1) {
      toast.error(t('Select a channel'))
      return
    }
    if (
      form.mode === 'probe_models' &&
      modelListFromText(form.candidate_models_text).length === 0
    ) {
      toast.error(t('Candidate models are required'))
      return
    }
    if (form.timeout_seconds <= 0) {
      toast.error(t('Timeout must be greater than 0'))
      return
    }

    const context: CapabilityDetectRequestContext = {
      scope: props.account ? 'account' : 'pool',
      poolID: props.pool.id,
      accountID: props.account?.id,
      channelID: form.channel_id,
      dialogNonce: activeContextRef.current?.dialogNonce ?? dialogNonceRef.current,
      requestNonce: requestNonceRef.current + 1,
    }

    requestNonceRef.current = context.requestNonce
    activeContextRef.current = context
    detectMutation.mutate({ values: form, context })
  }

  return (
    <Dialog open={props.open} onOpenChange={props.onOpenChange}>
      <DialogContent className='sm:max-w-[640px]'>
        <DialogHeader>
          <DialogTitle>{t('Capability Detection')}</DialogTitle>
          <DialogDescription>
            {props.pool
              ? props.account
                ? t('Detect models for {{account}}', {
                    account: props.account.name || `#${props.account.id}`,
                  })
                : t('Detect models for {{pool}}', {
                    pool: props.pool.name,
                  })
              : t('Account Pool')}
          </DialogDescription>
        </DialogHeader>
        <form
          id='account-pool-capability-detect-form'
          className='flex max-h-[70vh] flex-col gap-4 overflow-y-auto pr-1'
          onSubmit={handleSubmit}
        >
          <FieldGroup className='gap-4 sm:grid sm:grid-cols-2'>
            <FieldBlock
              label={t('Mode')}
              htmlFor='account-pool-capability-mode'
            >
              <Select
                items={modeOptions}
                value={String(form.mode)}
                onValueChange={(value) => value && setField('mode', value)}
                disabled={!hasBindings}
              >
                <SelectTrigger id='account-pool-capability-mode'>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent alignItemWithTrigger={false}>
                  <SelectGroup>
                    {modeOptions.map((option) => (
                      <SelectItem key={option.value} value={option.value}>
                        {option.label}
                      </SelectItem>
                    ))}
                  </SelectGroup>
                </SelectContent>
              </Select>
            </FieldBlock>
            <FieldBlock
              label={t('Channel')}
              htmlFor='account-pool-capability-channel'
            >
              <Select
                items={bindingOptions}
                value={form.channel_id > 0 ? String(form.channel_id) : ''}
                onValueChange={(value) =>
                  setField('channel_id', value ? Number(value) : 0)
                }
                disabled={!hasBindings}
              >
                <SelectTrigger id='account-pool-capability-channel'>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent alignItemWithTrigger={false}>
                  <SelectGroup>
                    {bindingOptions.map((option) => (
                      <SelectItem key={option.value} value={option.value}>
                        {option.label}
                      </SelectItem>
                    ))}
                  </SelectGroup>
                </SelectContent>
              </Select>
            </FieldBlock>
          </FieldGroup>

          {!hasBindings && (
            <div className='text-muted-foreground rounded-md border border-dashed px-3 py-2 text-sm'>
              {t('No bound channels')}
            </div>
          )}

          <FieldBlock
            label={t('Candidate Models')}
            htmlFor='account-pool-capability-candidate-models'
          >
            <Textarea
              id='account-pool-capability-candidate-models'
              className='min-h-[120px] font-mono text-xs'
              value={form.candidate_models_text}
              onChange={(event) =>
                setField('candidate_models_text', event.target.value)
              }
              required={form.mode === 'probe_models'}
              placeholder={'gpt-5\ngpt-5-mini'}
              disabled={!hasBindings}
            />
          </FieldBlock>

          <FieldGroup className='gap-3'>
            <div className={sideDrawerSwitchItemClassName()}>
              <div className='min-w-0'>
                <FieldLabel htmlFor='account-pool-capability-apply'>
                  {t('Apply detected models')}
                </FieldLabel>
                {!form.apply && (
                  <p className='text-muted-foreground mt-1 text-xs'>
                    {t('Dry Run')}
                  </p>
                )}
              </div>
              <Switch
                id='account-pool-capability-apply'
                checked={form.apply}
                onCheckedChange={(checked) => setField('apply', checked)}
                disabled={!hasBindings}
              />
            </div>
            <div className={sideDrawerSwitchItemClassName()}>
              <div className='min-w-0'>
                <FieldLabel htmlFor='account-pool-capability-merge'>
                  {t('Merge with existing models')}
                </FieldLabel>
              </div>
              <Switch
                id='account-pool-capability-merge'
                checked={form.merge}
                onCheckedChange={(checked) => setField('merge', checked)}
                disabled={!hasBindings}
              />
            </div>
            <NumericField
              id='account-pool-capability-timeout'
              label={t('Timeout')}
              min={1}
              value={form.timeout_seconds}
              onChange={(value) => setField('timeout_seconds', value)}
              disabled={!hasBindings}
            />
          </FieldGroup>

          {singleResult && (
            <div className='rounded-lg border p-3 text-sm'>
              <div className='flex flex-wrap items-center gap-2'>
                <StatusBadge
                  label={t(capabilityStatusLabel(singleResult.status))}
                  variant={capabilityStatusVariant(singleResult.status)}
                  copyable={false}
                />
                <span className='text-muted-foreground text-xs'>
                  #{singleResult.account_id}
                </span>
              </div>
              <div className='text-muted-foreground mt-3 grid gap-2 sm:grid-cols-3'>
                <div>
                  {t('Detected Models')}: {singleResult.detected_models.length}
                </div>
                <div>
                  {t('Applied Models')}: {singleResult.applied_models.length}
                </div>
                <div>
                  {t('Status')}: {t(capabilityStatusLabel(singleResult.status))}
                </div>
              </div>
              {singleResult.errors.length > 0 && (
                <div className='text-destructive mt-3 text-xs'>
                  {singleResult.errors.join('; ')}
                </div>
              )}
            </div>
          )}

          {poolResult && (
            <div className='rounded-lg border p-3 text-sm'>
              <div className='grid gap-2 sm:grid-cols-3'>
                <div>
                  {t('Total')}: {poolResult.total}
                </div>
                <div>
                  {t('Succeeded')}: {poolResult.succeeded}
                </div>
                <div>
                  {t('Failed')}: {poolResult.failed}
                </div>
              </div>
              <div className='mt-3 flex max-h-56 flex-col gap-2 overflow-y-auto'>
                {poolResult.results.map((result) => (
                  <div
                    key={String(result.account_id)}
                    className='flex flex-col gap-1 rounded-md border px-3 py-2 sm:flex-row sm:items-center sm:justify-between'
                  >
                    <div className='min-w-0'>
                      <div className='flex items-center gap-2'>
                        <span className='font-medium'>
                          {accountNameByID.get(result.account_id) ||
                            `#${result.account_id}`}
                        </span>
                        <span className='text-muted-foreground text-xs'>
                          #{result.account_id}
                        </span>
                      </div>
                      {result.errors.length > 0 && (
                        <div className='text-destructive text-xs'>
                          {result.errors.join('; ')}
                        </div>
                      )}
                    </div>
                    <div className='flex flex-wrap items-center gap-2 text-xs'>
                      <StatusBadge
                        label={t(capabilityStatusLabel(result.status))}
                        variant={capabilityStatusVariant(result.status)}
                        copyable={false}
                      />
                      <span className='text-muted-foreground'>
                        {t('Detected Models')}: {result.detected_models.length}
                      </span>
                      <span className='text-muted-foreground'>
                        {t('Applied Models')}: {result.applied_models.length}
                      </span>
                    </div>
                  </div>
                ))}
              </div>
            </div>
          )}
        </form>
        <DialogFooter>
          <DialogClose render={<Button type='button' variant='outline' />}>
            {t('Cancel')}
          </DialogClose>
          <Button
            type='submit'
            form='account-pool-capability-detect-form'
            disabled={isCurrentDetectionPending || !hasBindings}
          >
            {isCurrentDetectionPending ? (
              <Loader2 data-icon='inline-start' className='animate-spin' />
            ) : (
              <Radar data-icon='inline-start' />
            )}
            {!hasBindings
              ? t('No bound channels')
              : props.account
                ? t('Detect Models')
                : t('Batch Detect Models')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
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
  editingBinding?: AccountPoolBinding
  bindingFormResetVersion: number
  bindingSubmitting: boolean
  onOpenChange: (open: boolean) => void
  onCreateAccount: () => void
  onImportAccounts: () => void
  onEditAccount: (account: AccountPoolAccount) => void
  onDetectAccount: (account: AccountPoolAccount) => void
  onBatchDetectModels: () => void
  onDeleteAccount: (account: AccountPoolAccount) => void
  onSetAccountStatus: (account: AccountPoolAccount, status: string) => void
  onCreateProxy: () => void
  onEditProxy: (proxy: AccountPoolProxy) => void
  onDeleteProxy: (proxy: AccountPoolProxy) => void
  onCreateBoundChannel: () => void
  onCreateBinding: (values: BindingFormValues) => void
  onUpdateBinding: (values: BindingFormValues) => void
  onEditBinding: (binding: AccountPoolBinding) => void
  onDeleteBinding: (binding: AccountPoolBinding) => void
  onCancelBindingEdit: () => void
  onSetBindingStatus: (
    binding: AccountPoolBinding,
    status: 'enabled' | 'disabled'
  ) => void
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
                hasBindings={props.bindings.length > 0}
                loading={props.accountsLoading}
                onCreateAccount={props.onCreateAccount}
                onImportAccounts={props.onImportAccounts}
                onEditAccount={props.onEditAccount}
                onDetectAccount={props.onDetectAccount}
                onBatchDetectModels={props.onBatchDetectModels}
                onDeleteAccount={props.onDeleteAccount}
                onSetAccountStatus={props.onSetAccountStatus}
              />
            </TabsContent>
            <TabsContent value='bindings' className='min-h-0'>
              <BindingSection
                pool={pool}
                accounts={props.accounts}
                bindings={props.bindings}
                editingBinding={props.editingBinding}
                loading={props.bindingsLoading}
                resetVersion={props.bindingFormResetVersion}
                submitting={props.bindingSubmitting}
                onCreateBoundChannel={props.onCreateBoundChannel}
                onCreateBinding={props.onCreateBinding}
                onUpdateBinding={props.onUpdateBinding}
                onEditBinding={props.onEditBinding}
                onDeleteBinding={props.onDeleteBinding}
                onCancelBindingEdit={props.onCancelBindingEdit}
                onSetBindingStatus={props.onSetBindingStatus}
              />
            </TabsContent>
            <TabsContent value='proxies' className='min-h-0'>
              <ProxyListSection
                proxies={props.proxies}
                loading={props.proxiesLoading}
                onCreateProxy={props.onCreateProxy}
                onEditProxy={props.onEditProxy}
                onDeleteProxy={props.onDeleteProxy}
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
  hasBindings: boolean
  loading: boolean
  onCreateAccount: () => void
  onImportAccounts: () => void
  onEditAccount: (account: AccountPoolAccount) => void
  onDetectAccount: (account: AccountPoolAccount) => void
  onBatchDetectModels: () => void
  onDeleteAccount: (account: AccountPoolAccount) => void
  onSetAccountStatus: (account: AccountPoolAccount, status: string) => void
}) {
  const { t } = useTranslation()

  return (
    <SideDrawerSection className='pt-4'>
      <div className='flex items-center justify-between gap-3'>
        <SideDrawerSectionHeader title={t('Accounts')} />
        <div className='flex items-center gap-2'>
          <Button
            type='button'
            size='sm'
            variant='outline'
            onClick={props.onBatchDetectModels}
            disabled={!props.hasBindings}
          >
            <Radar data-icon='inline-start' />
            {props.hasBindings ? t('Batch Detect Models') : t('No bound channels')}
          </Button>
          <Button
            type='button'
            size='sm'
            variant='outline'
            onClick={props.onImportAccounts}
          >
            <Upload data-icon='inline-start' />
            {t('Import Accounts')}
          </Button>
          <Button type='button' size='sm' onClick={props.onCreateAccount}>
            <Plus data-icon='inline-start' />
            {t('Add Account')}
          </Button>
        </div>
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
              <TableHead>{t('Runtime Stats')}</TableHead>
              <TableHead>{t('Last Error')}</TableHead>
              <TableHead>{t('Actions')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {props.loading && <LoadingRow colSpan={10} />}
            {!props.loading && props.accounts.length === 0 && (
              <EmptyRow colSpan={10} label={t('No accounts found')} />
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
                  <AccountRuntimeStats account={account} />
                </TableCell>
                <TableCell>
                  <LongText className='text-destructive max-w-[200px] text-xs'>
                    {account.last_error || '-'}
                  </LongText>
                </TableCell>
                <TableCell>
                  <AccountRowActions
                    account={account}
                    onEdit={props.onEditAccount}
                    onDetect={props.onDetectAccount}
                    onDelete={props.onDeleteAccount}
                    onSetStatus={props.onSetAccountStatus}
                  />
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
    </SideDrawerSection>
  )
}

function AccountRuntimeStats(props: { account: AccountPoolAccount }) {
  const { t } = useTranslation()
  const account = props.account
  const averageFirstTokenLatency = averageMetric(
    account.total_first_token_latency_ms,
    account.first_token_latency_sample_count
  )

  return (
    <div className='flex min-w-[190px] flex-col gap-1 text-xs'>
      <div className='flex items-center gap-2'>
        <span className='text-muted-foreground'>{t('Success')}</span>
        <span className='font-medium'>{account.success_count}</span>
        <span className='text-muted-foreground'>{t('Failure')}</span>
        <span className='font-medium'>{account.failure_count}</span>
      </div>
      <div className='text-muted-foreground'>
        {t('Cache Rate')}:{' '}
        {formatOptionalPercent(
          account.total_cached_tokens,
          account.total_prompt_tokens
        )}
      </div>
      <div className='text-muted-foreground'>
        {t('TTFT')}: {formatOptionalMilliseconds(account.last_first_token_latency_ms)}
        {' / '}
        {t('Avg')} {formatOptionalMilliseconds(averageFirstTokenLatency)}
      </div>
      <div className='text-muted-foreground'>
        {t('Latency')}: {formatOptionalMilliseconds(account.last_latency_ms)}
      </div>
      <div className='text-muted-foreground'>
        {t('Tokens')}: {formatOptionalCount(account.last_prompt_tokens)}
        {' / '}
        {formatOptionalCount(account.last_completion_tokens)}
      </div>
      <div className='text-muted-foreground'>
        {t('Last Success')}: {formatOptionalTimestamp(account.last_success_at)}
      </div>
      <div className='text-muted-foreground'>
        {t('Last Failure')}: {formatOptionalTimestamp(account.last_failure_at)}
      </div>
      <div className='text-muted-foreground'>
        {t('Capability detection status')}:{' '}
        {account.last_capability_check_status
          ? `${t(capabilityStatusLabel(account.last_capability_check_status))}${
              account.last_capability_check_at > 0
                ? ` / ${formatOptionalTimestamp(account.last_capability_check_at)}`
                : ''
            }`
          : '-'}
      </div>
    </div>
  )
}

function AccountRowActions(props: {
  account: AccountPoolAccount
  onEdit: (account: AccountPoolAccount) => void
  onDetect: (account: AccountPoolAccount) => void
  onDelete: (account: AccountPoolAccount) => void
  onSetStatus: (account: AccountPoolAccount, status: string) => void
}) {
  const { t } = useTranslation()
  const disabled = props.account.status === 'disabled'
  const nextStatus = disabled ? 'enabled' : 'disabled'

  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        render={
          <Button
            variant='ghost'
            size='icon-sm'
            className='data-popup-open:bg-muted'
          />
        }
      >
        <MoreHorizontal />
        <span className='sr-only'>{t('Open menu')}</span>
      </DropdownMenuTrigger>
      <DropdownMenuContent align='end'>
        <DropdownMenuItem onClick={() => props.onEdit(props.account)}>
          <Pencil />
          {t('Edit')}
        </DropdownMenuItem>
        <DropdownMenuItem onClick={() => props.onDetect(props.account)}>
          <Radar />
          {t('Detect Models')}
        </DropdownMenuItem>
        <DropdownMenuItem
          onClick={() => props.onSetStatus(props.account, nextStatus)}
        >
          {disabled ? <Power /> : <PowerOff />}
          {disabled ? t('Enable') : t('Disable')}
        </DropdownMenuItem>
        <DropdownMenuItem
          className='text-destructive'
          onClick={() => props.onDelete(props.account)}
        >
          <Trash2 />
          {t('Delete')}
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

function BindingSection(props: {
  pool?: AccountPool
  accounts: AccountPoolAccount[]
  bindings: AccountPoolBinding[]
  editingBinding?: AccountPoolBinding
  loading: boolean
  resetVersion: number
  submitting: boolean
  onCreateBoundChannel: () => void
  onCreateBinding: (values: BindingFormValues) => void
  onUpdateBinding: (values: BindingFormValues) => void
  onEditBinding: (binding: AccountPoolBinding) => void
  onDeleteBinding: (binding: AccountPoolBinding) => void
  onCancelBindingEdit: () => void
  onSetBindingStatus: (
    binding: AccountPoolBinding,
    status: 'enabled' | 'disabled'
  ) => void
}) {
  const { t } = useTranslation()

  return (
    <div className='flex flex-col gap-6 pt-4'>
      <SideDrawerSection>
        <div className='flex items-center justify-between gap-3'>
          <SideDrawerSectionHeader title={t('Bindings')} />
          <Button size='sm' onClick={props.onCreateBoundChannel}>
            <Plus data-icon='inline-start' />
            {t('Create Channel')}
          </Button>
        </div>
        <div className='border-border rounded-lg border'>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t('Channel')}</TableHead>
                <TableHead>{t('Channel Status')}</TableHead>
                <TableHead>{t('Binding Status')}</TableHead>
                <TableHead>{t('Runtime')}</TableHead>
                <TableHead>{t('Schedule Policy')}</TableHead>
                <TableHead>{t('Retry Count')}</TableHead>
                <TableHead>{t('Updated At')}</TableHead>
                <TableHead>{t('Actions')}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {props.loading && <LoadingRow colSpan={8} />}
              {!props.loading && props.bindings.length === 0 && (
                <EmptyRow colSpan={8} label={t('No bindings found')} />
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
                  <TableCell>
                    <StatusBadge
                      label={binding.runtime_enabled ? t('Routed') : t('Not Routed')}
                      variant={binding.runtime_enabled ? 'success' : 'neutral'}
                      copyable={false}
                    />
                  </TableCell>
                  <TableCell>
                    {t(schedulePolicyLabel(binding.schedule_policy))}
                  </TableCell>
                  <TableCell>{binding.account_retry_times}</TableCell>
                  <TableCell>
                    <span className='text-muted-foreground text-sm'>
                      {formatOptionalTimestamp(binding.updated_time)}
                    </span>
                  </TableCell>
                  <TableCell>
                    <BindingRowActions
                      binding={binding}
                      onEdit={props.onEditBinding}
                      onDelete={props.onDeleteBinding}
                      onSetStatus={props.onSetBindingStatus}
                    />
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      </SideDrawerSection>
      <BindingForm
        pool={props.pool}
        accounts={props.accounts}
        binding={props.editingBinding}
        bindings={props.bindings}
        resetVersion={props.resetVersion}
        submitting={props.submitting}
        onCancelEdit={props.onCancelBindingEdit}
        onSubmit={
          props.editingBinding ? props.onUpdateBinding : props.onCreateBinding
        }
      />
    </div>
  )
}

function BindingRowActions(props: {
  binding: AccountPoolBinding
  onEdit: (binding: AccountPoolBinding) => void
  onDelete: (binding: AccountPoolBinding) => void
  onSetStatus: (
    binding: AccountPoolBinding,
    status: 'enabled' | 'disabled'
  ) => void
}) {
  const { t } = useTranslation()
  const enabled = props.binding.status === 'enabled'

  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        render={
          <Button
            variant='ghost'
            size='icon-sm'
            className='data-popup-open:bg-muted'
          />
        }
      >
        <MoreHorizontal />
        <span className='sr-only'>{t('Open menu')}</span>
      </DropdownMenuTrigger>
      <DropdownMenuContent align='end'>
        <DropdownMenuItem onClick={() => props.onEdit(props.binding)}>
          <Pencil />
          {t('Edit')}
        </DropdownMenuItem>
        <DropdownMenuItem
          onClick={() =>
            props.onSetStatus(props.binding, enabled ? 'disabled' : 'enabled')
          }
        >
          {enabled ? <PowerOff /> : <Power />}
          {enabled ? t('Disable') : t('Enable')}
        </DropdownMenuItem>
        <DropdownMenuItem
          className='text-destructive focus:text-destructive'
          onClick={() => props.onDelete(props.binding)}
        >
          <Trash2 />
          {t('Delete')}
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

function BindingForm(props: {
  pool?: AccountPool
  accounts: AccountPoolAccount[]
  binding?: AccountPoolBinding
  bindings: AccountPoolBinding[]
  resetVersion: number
  submitting: boolean
  onCancelEdit: () => void
  onSubmit: (values: BindingFormValues) => void
}) {
  const { t } = useTranslation()
  const [form, setForm] = useState<BindingFormValues>(emptyBindingForm())
  const modelStrategyOptions = useMemo(
    () => translateOptions(MODEL_STRATEGY_OPTIONS, t),
    [t]
  )
  const schedulePolicyOptions = useMemo(
    () => translateOptions(SCHEDULE_POLICY_OPTIONS, t),
    [t]
  )
  const disabledChannelsQuery = useQuery({
    queryKey: ['channels', 'disabled', 'account-pool-bindings'],
    queryFn: async () => {
      const result = await getChannels({ status: 'disabled', page_size: 100 })
      if (!result.success) {
        throw new Error(result.message || t('Failed to load disabled channels'))
      }
      return result.data?.items ?? []
    },
  })
  const { modelSelectOptions, modelsLoading } = useAccountPoolModelSelect()
  const disabledChannels = disabledChannelsQuery.data ?? EMPTY_CHANNELS
  const boundChannelIDs = useMemo(
    () =>
      new Set(
        props.bindings
          .filter((binding) => binding.id !== props.binding?.id)
          .map((binding) => binding.channel_id)
      ),
    [props.binding?.id, props.bindings]
  )
  const availableBindingChannels = useMemo(
    () => {
      const channels = disabledChannels
        .filter((channel) => !boundChannelIDs.has(channel.id))
        .map((channel) => ({
          id: channel.id,
          name: channel.name,
        }))
      if (
        props.binding &&
        !channels.some((channel) => channel.id === props.binding?.channel_id)
      ) {
        return [
          {
            id: props.binding.channel_id,
            name: props.binding.channel_name || `#${props.binding.channel_id}`,
          },
          ...channels,
        ]
      }
      return channels
    },
    [boundChannelIDs, disabledChannels, props.binding]
  )

  useEffect(() => {
    if (disabledChannelsQuery.error) {
      toast.error(disabledChannelsQuery.error.message)
    }
  }, [disabledChannelsQuery.error])

  useEffect(() => {
    setForm(
      props.binding
        ? bindingToFormValues(props.binding)
        : emptyBindingForm(props.pool?.default_schedule_policy)
    )
  }, [props.binding, props.pool?.default_schedule_policy, props.resetVersion])

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
      <SideDrawerSectionHeader
        title={props.binding ? t('Edit Binding') : t('Draft Binding')}
      />
      <form className='flex flex-col gap-4' onSubmit={handleSubmit}>
        <FieldGroup className='gap-4 sm:grid sm:grid-cols-2'>
          <FieldBlock
            label={t('Disabled Channel')}
            htmlFor='account-pool-binding-channel'
            description={t('Only disabled channels are available for binding.')}
          >
            <Select
              items={availableBindingChannels.map((channel) => ({
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
                  {availableBindingChannels.map((channel) => (
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
              items={modelStrategyOptions}
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
                  {modelStrategyOptions.map((option) => (
                    <SelectItem key={option.value} value={option.value}>
                      {option.label}
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
            <MultiSelect
              id='account-pool-binding-fixed-models'
              options={modelSelectOptions}
              selected={modelListFromText(form.fixed_models_text)}
              onChange={(values) =>
                setField('fixed_models_text', modelListToText(values))
              }
              placeholder={t('Select models or add custom ones')}
              allowCreate
              createLabel='Add custom model "{{value}}"'
              emptyText={t('No models found')}
              maxVisibleChips={8}
              copyChipOnClick
              disabled={modelsLoading}
            />
          </FieldBlock>
          <FieldBlock
            label={t('Schedule Policy')}
            htmlFor='account-pool-binding-schedule-policy'
          >
            <Select
              items={schedulePolicyOptions}
              value={form.schedule_policy}
              onValueChange={(value) =>
                value &&
                setField(
                  'schedule_policy',
                  normalizeAccountPoolSchedulePolicy(value)
                )
              }
            >
              <SelectTrigger id='account-pool-binding-schedule-policy'>
                <SelectValue />
              </SelectTrigger>
              <SelectContent alignItemWithTrigger={false}>
                <SelectGroup>
                  {schedulePolicyOptions.map((option) => (
                    <SelectItem key={option.value} value={option.value}>
                      {option.label}
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
          <FieldBlock
            label={t('Max Per-User Concurrency')}
            htmlFor='account-pool-binding-max-user-concurrency'
          >
            <Input
              id='account-pool-binding-max-user-concurrency'
              type='number'
              min={0}
              value={form.max_user_concurrency}
              onChange={(event) =>
                setField('max_user_concurrency', Number(event.target.value))
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
        <div className='flex justify-end gap-2'>
          {props.binding && (
            <Button
              type='button'
              variant='outline'
              disabled={props.submitting}
              onClick={props.onCancelEdit}
            >
              {t('Cancel Edit')}
            </Button>
          )}
          <Button type='submit' disabled={props.submitting}>
            {props.submitting ? (
              <Loader2 data-icon='inline-start' className='animate-spin' />
            ) : props.binding ? (
              <Save data-icon='inline-start' />
            ) : (
              <Link2 data-icon='inline-start' />
            )}
            {props.binding ? t('Save Changes') : t('Create Draft Binding')}
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
  onEditProxy: (proxy: AccountPoolProxy) => void
  onDeleteProxy: (proxy: AccountPoolProxy) => void
}) {
  const { t } = useTranslation()
  const proxyNames = useMemo(
    () => new Map(props.proxies.map((proxy) => [proxy.id, proxy.name])),
    [props.proxies]
  )

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
              <TableHead>{t('Fallback Proxy')}</TableHead>
              <TableHead>{t('Actions')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {props.loading && <LoadingRow colSpan={6} />}
            {!props.loading && props.proxies.length === 0 && (
              <EmptyRow colSpan={6} label={t('No proxies found')} />
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
                <TableCell>
                  <LongText className='max-w-[180px]'>
                    {proxyReferenceLabel(proxy.fallback_proxy_id, proxyNames)}
                  </LongText>
                </TableCell>
                <TableCell>
                  <ProxyRowActions
                    proxy={proxy}
                    onEdit={props.onEditProxy}
                    onDelete={props.onDeleteProxy}
                  />
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
    </SideDrawerSection>
  )
}

function ProxyRowActions(props: {
  proxy: AccountPoolProxy
  onEdit: (proxy: AccountPoolProxy) => void
  onDelete: (proxy: AccountPoolProxy) => void
}) {
  const { t } = useTranslation()

  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        render={
          <Button
            variant='ghost'
            size='icon-sm'
            className='data-popup-open:bg-muted'
          />
        }
      >
        <MoreHorizontal />
        <span className='sr-only'>{t('Open menu')}</span>
      </DropdownMenuTrigger>
      <DropdownMenuContent align='end'>
        <DropdownMenuItem onClick={() => props.onEdit(props.proxy)}>
          <Pencil />
          {t('Edit')}
        </DropdownMenuItem>
        <DropdownMenuItem onClick={() => props.onDelete(props.proxy)}>
          <Trash2 />
          {t('Delete')}
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

function AccountImportDialog(props: {
  open: boolean
  pool?: AccountPool
  proxies: AccountPoolProxy[]
  isSubmitting: boolean
  onOpenChange: (open: boolean) => void
  onSubmit: (values: AccountImportFormValues) => void
}) {
  const { t } = useTranslation()
  const [form, setForm] = useState<AccountImportFormValues>(
    emptyAccountImportForm()
  )
  const formatOptions = useMemo(
    () => [
      { value: 'sub2api', label: t('sub2api export') },
      { value: 'cpa', label: t('CPA config or auth') },
    ],
    [t]
  )
  const proxyOptions = useMemo(
    () => buildAccountPoolProxyOptions(props.proxies, t('No Proxy')),
    [props.proxies, t]
  )
  const { modelSelectOptions, modelsLoading } = useAccountPoolModelSelect(
    props.open
  )

  useEffect(() => {
    if (props.open) {
      setForm(emptyAccountImportForm())
    }
  }, [props.open])

  const setField = <K extends keyof AccountImportFormValues>(
    key: K,
    value: AccountImportFormValues[K]
  ) => setForm((previous) => ({ ...previous, [key]: value }))

  const handleSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!props.pool) {
      toast.error(t('Select an account pool first'))
      return
    }
    if (!form.content.trim()) {
      toast.error(t('Import content is required'))
      return
    }
    props.onSubmit(form)
  }

  return (
    <Dialog open={props.open} onOpenChange={props.onOpenChange}>
      <DialogContent className='sm:max-w-2xl'>
        <DialogHeader>
          <DialogTitle>{t('Import Accounts')}</DialogTitle>
          <DialogDescription>
            {props.pool
              ? t('Import accounts into {{name}}', { name: props.pool.name })
              : t('Account Pool')}
          </DialogDescription>
        </DialogHeader>
        <form
          id='account-pool-import-form'
          className='flex max-h-[70vh] flex-col gap-4 overflow-y-auto pr-1'
          onSubmit={handleSubmit}
        >
          <FieldGroup className='gap-4 sm:grid sm:grid-cols-2'>
            <FieldBlock
              label={t('Import Format')}
              htmlFor='account-pool-import-format'
            >
              <Select
                items={formatOptions}
                value={form.format}
                onValueChange={(value) =>
                  value &&
                  setField('format', value as AccountImportFormValues['format'])
                }
              >
                <SelectTrigger id='account-pool-import-format'>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent alignItemWithTrigger={false}>
                  <SelectGroup>
                    {formatOptions.map((option) => (
                      <SelectItem key={option.value} value={option.value}>
                        {option.label}
                      </SelectItem>
                    ))}
                  </SelectGroup>
                </SelectContent>
              </Select>
            </FieldBlock>
            <NumericField
              id='account-pool-import-default-priority'
              label={t('Default Priority')}
              value={form.default_priority}
              onChange={(value) => setField('default_priority', value)}
            />
            <NumericField
              id='account-pool-import-default-weight'
              label={t('Default Weight')}
              min={0}
              value={form.default_weight}
              onChange={(value) => setField('default_weight', value)}
            />
            <NumericField
              id='account-pool-import-default-concurrency'
              label={t('Default Max Concurrency')}
              min={0}
              value={form.default_max_concurrency}
              onChange={(value) => setField('default_max_concurrency', value)}
            />
            <FieldBlock
              label={t('Default Proxy')}
              htmlFor='account-pool-import-default-proxy'
            >
              <Select
                items={proxyOptions}
                value={String(form.default_proxy_id)}
                onValueChange={(value) =>
                  setField('default_proxy_id', value ? Number(value) : 0)
                }
              >
                <SelectTrigger id='account-pool-import-default-proxy'>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent alignItemWithTrigger={false}>
                  <SelectGroup>
                    {proxyOptions.map((option) => (
                      <SelectItem key={option.value} value={option.value}>
                        {option.label}
                      </SelectItem>
                    ))}
                  </SelectGroup>
                </SelectContent>
              </Select>
            </FieldBlock>
          </FieldGroup>
          <FieldBlock
            label={t('Default Supported Models')}
            htmlFor='account-pool-import-default-models'
            description={t('Used only when the imported account has no model list.')}
          >
            <MultiSelect
              id='account-pool-import-default-models'
              options={modelSelectOptions}
              selected={modelListFromText(form.default_supported_models_text)}
              onChange={(values) =>
                setField(
                  'default_supported_models_text',
                  modelListToText(values)
                )
              }
              placeholder={t('Select models or add custom ones')}
              allowCreate
              createLabel='Add custom model "{{value}}"'
              emptyText={t('No models found')}
              maxVisibleChips={8}
              copyChipOnClick
              disabled={modelsLoading}
            />
          </FieldBlock>
          <FieldBlock
            label={t('Import Content')}
            htmlFor='account-pool-import-content'
            description={t('Paste sub2api account export JSON or CPA YAML/JSON.')}
          >
            <Textarea
              id='account-pool-import-content'
              className='min-h-[260px] font-mono text-xs'
              value={form.content}
              onChange={(event) => setField('content', event.target.value)}
              placeholder={
                form.format === 'cpa'
                  ? 'codex-api-key:\n  - api-key: sk-...'
                  : '{\n  "type": "sub2api-data",\n  "accounts": []\n}'
              }
            />
          </FieldBlock>
        </form>
        <DialogFooter>
          <DialogClose render={<Button type='button' variant='outline' />}>
            {t('Cancel')}
          </DialogClose>
          <Button
            type='submit'
            form='account-pool-import-form'
            disabled={props.isSubmitting}
          >
            {props.isSubmitting ? (
              <Loader2 data-icon='inline-start' className='animate-spin' />
            ) : (
              <Upload data-icon='inline-start' />
            )}
            {t('Import')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function AccountFormSheet(props: {
  open: boolean
  pool?: AccountPool
  account?: AccountPoolAccount
  proxies: AccountPoolProxy[]
  isSubmitting: boolean
  onOpenChange: (open: boolean) => void
  onSubmit: (values: AccountPoolAccountFormValues) => void
}) {
  const { t } = useTranslation()
  const [form, setForm] =
    useState<AccountPoolAccountFormValues>(emptyAccountForm())
  const accountStatusOptions = useMemo(
    () => translateOptions(ACCOUNT_STATUS_OPTIONS, t),
    [t]
  )
  const oauthSupported = platformSupportsOAuthCredential(
    props.pool?.platform ?? 'openai'
  )
  const credentialTypeOptions = useMemo(
    () => [
      { value: 'api_key', label: t('API Key') },
      ...(oauthSupported ? [{ value: 'oauth', label: t('OAuth') }] : []),
    ],
    [t, oauthSupported]
  )
  const proxyOptions = useMemo(
    () => buildAccountPoolProxyOptions(props.proxies, t('No Proxy')),
    [props.proxies, t]
  )
  const { modelSelectOptions, modelsLoading } = useAccountPoolModelSelect(
    props.open
  )

  useEffect(() => {
    if (props.open) {
      const next = props.account
        ? accountToFormValues(props.account)
        : emptyAccountForm()
      // Gemini pools do not support OAuth credentials yet; fall back to api_key.
      if (!oauthSupported && next.credential_type === 'oauth') {
        next.credential_type = 'api_key'
      }
      setForm(next)
    }
  }, [props.account, props.open, oauthSupported])

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
          <SheetTitle>
            {props.account ? t('Edit Account') : t('Add Account')}
          </SheetTitle>
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
                  items={accountStatusOptions}
                  value={form.status}
                  onValueChange={(value) => value && setField('status', value)}
                >
                  <SelectTrigger id='account-pool-account-status'>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent alignItemWithTrigger={false}>
                    <SelectGroup>
                      {accountStatusOptions.map((option) => (
                        <SelectItem key={option.value} value={option.value}>
                          {option.label}
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
                  items={proxyOptions}
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
                      {proxyOptions.map((option) => (
                        <SelectItem key={option.value} value={option.value}>
                          {option.label}
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
                min={0}
                value={form.max_concurrency}
                onChange={(value) => setField('max_concurrency', value)}
              />
              <NumericField
                id='account-pool-account-request-quota'
                label={t('Request Quota (0 = unlimited)')}
                min={0}
                value={form.request_quota}
                onChange={(value) => setField('request_quota', value)}
              />
              <NumericField
                id='account-pool-account-request-quota-window'
                label={t('Request Quota Window (seconds, 0 = lifetime)')}
                min={0}
                value={form.request_quota_window_seconds}
                onChange={(value) =>
                  setField('request_quota_window_seconds', value)
                }
              />
              <NumericField
                id='account-pool-account-expires-at'
                label={t('Expires At (unix seconds, 0 = never)')}
                min={0}
                value={form.expires_at}
                onChange={(value) => setField('expires_at', value)}
              />
            </FieldGroup>
            <div className={sideDrawerSwitchItemClassName()}>
              <div className='min-w-0'>
                <FieldLabel htmlFor='account-pool-account-auto-pause-on-expired'>
                  {t('Auto Pause On Expired')}
                </FieldLabel>
                <p className='text-muted-foreground mt-1 text-xs'>
                  {t('Stop scheduling this account once it has expired')}
                </p>
              </div>
              <Switch
                id='account-pool-account-auto-pause-on-expired'
                checked={form.auto_pause_on_expired}
                onCheckedChange={(checked) =>
                  setField('auto_pause_on_expired', checked)
                }
              />
            </div>
          </SideDrawerSection>
          <SideDrawerSection>
            <SideDrawerSectionHeader title={t('Credentials')} />
            <FieldGroup className='gap-4 sm:grid sm:grid-cols-2'>
              <FieldBlock
                label={t('Credential Type')}
                htmlFor='account-pool-account-credential-type'
              >
                <Select
                  items={credentialTypeOptions}
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
                      {credentialTypeOptions.map((option) => (
                        <SelectItem key={option.value} value={option.value}>
                          {option.label}
                        </SelectItem>
                      ))}
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
                placeholder={
                  props.account?.has_credential ? t('Leave blank to keep') : ''
                }
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
                  placeholder={
                    props.account?.has_credential
                      ? t('Leave blank to keep')
                      : ''
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
                  placeholder={
                    props.account?.has_token ? t('Leave blank to keep') : ''
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
                  placeholder={
                    props.account?.has_token ? t('Leave blank to keep') : ''
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
                <MultiSelect
                  id='account-pool-account-supported-models'
                  options={modelSelectOptions}
                  selected={modelListFromText(form.supported_models_text)}
                  onChange={(values) =>
                    setField('supported_models_text', modelListToText(values))
                  }
                  placeholder={t('Select models or add custom ones')}
                  allowCreate
                  createLabel='Add custom model "{{value}}"'
                  emptyText={t('No models found')}
                  maxVisibleChips={8}
                  copyChipOnClick
                  disabled={modelsLoading}
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
            {props.account ? t('Save Changes') : t('Create')}
          </Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  )
}

function ProxyFormSheet(props: {
  open: boolean
  proxy?: AccountPoolProxy
  proxies: AccountPoolProxy[]
  isSubmitting: boolean
  onOpenChange: (open: boolean) => void
  onSubmit: (values: AccountPoolProxyFormValues) => void
}) {
  const { t } = useTranslation()
  const [form, setForm] =
    useState<AccountPoolProxyFormValues>(emptyProxyForm())
  const protocolOptions = useMemo(
    () => translateOptions(PROXY_PROTOCOL_OPTIONS, t),
    [t]
  )
  const statusOptions = useMemo(
    () => translateOptions(STATUS_OPTIONS, t),
    [t]
  )
  const fallbackProxyOptions = useMemo(
    () => [
      { value: '0', label: t('No Fallback Proxy') },
      ...props.proxies
        .filter((proxy) => proxy.id !== props.proxy?.id)
        .map((proxy) => ({
          value: String(proxy.id),
          label: proxy.name,
        })),
    ],
    [props.proxies, props.proxy?.id, t]
  )

  useEffect(() => {
    if (props.open) {
      setForm(props.proxy ? proxyToFormValues(props.proxy) : emptyProxyForm())
    }
  }, [props.open, props.proxy])

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
          <SheetTitle>{props.proxy ? t('Edit Proxy') : t('Add Proxy')}</SheetTitle>
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
                items={protocolOptions}
                value={form.protocol}
                onValueChange={(value) => value && setField('protocol', value)}
              >
                <SelectTrigger id='account-pool-proxy-protocol'>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent alignItemWithTrigger={false}>
                  <SelectGroup>
                    {protocolOptions.map((option) => (
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
                placeholder={
                  props.proxy?.has_password ? t('Leave blank to keep') : ''
                }
              />
            </FieldBlock>
            <FieldBlock label={t('Status')} htmlFor='account-pool-proxy-status'>
              <Select
                items={statusOptions}
                value={form.status}
                onValueChange={(value) => value && setField('status', value)}
              >
                <SelectTrigger id='account-pool-proxy-status'>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent alignItemWithTrigger={false}>
                  <SelectGroup>
                    {statusOptions.map((option) => (
                      <SelectItem key={option.value} value={option.value}>
                        {option.label}
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
                items={fallbackProxyOptions}
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
                    {fallbackProxyOptions.map((option) => (
                      <SelectItem key={option.value} value={option.value}>
                        {option.label}
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
            {props.proxy ? t('Save Changes') : t('Create')}
          </Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  )
}

function NumericField(props: {
  id: string
  label: React.ReactNode
  disabled?: boolean
  min?: number
  value: number
  onChange: (value: number) => void
}) {
  return (
    <FieldBlock label={props.label} htmlFor={props.id}>
      <Input
        id={props.id}
        type='number'
        disabled={props.disabled}
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
