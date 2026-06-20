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
import {
  useEffect,
  useMemo,
  useState,
  type FormEvent,
  type ReactNode,
} from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  type ColumnDef,
  type ColumnFiltersState,
  type PaginationState,
  type Row,
} from '@tanstack/react-table'
import { useMediaQuery } from '@/hooks'
import {
  KeyRound,
  Loader2,
  MoreHorizontal,
  Pencil,
  Plus,
  Radar,
  RefreshCcw,
  Save,
  Settings2,
  Trash2,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { formatTimestamp } from '@/lib/format'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuShortcut,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
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
import { ConfirmDialog } from '@/components/confirm-dialog'
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
import {
  createUpstreamSource,
  deleteUpstreamSource,
  discoverUpstreamSource,
  listUpstreamSourceMappings,
  listUpstreamSources,
  syncUpstreamSource,
  updateUpstreamSource,
  updateUpstreamSourceCredentials,
  updateUpstreamSourceMappings,
  upstreamSourcesQueryKeys,
} from './api'
import {
  UPSTREAM_SOURCE_TYPE_SUB2API,
  type ApiResponse,
  type UpstreamDiscoveryStatus,
  type UpstreamMappingDiscoveryStatus,
  type UpstreamMappingSyncStatus,
  type UpstreamSource,
  type UpstreamSourceCreateRequest,
  type UpstreamSourceFormValues,
  type UpstreamSourceMapping,
  type UpstreamSourceStatus,
  type UpstreamSourceSyncResult,
  type UpstreamSourceType,
  type UpstreamSourceUpdateRequest,
  type UpstreamSyncStatus,
} from './types'

const CHANNEL_TYPE_OPENAI = 1
const DEFAULT_MONITOR_INTERVAL_MINUTES = 10
const EMPTY_UPSTREAM_SOURCE_MAPPINGS: UpstreamSourceMapping[] = []

type SourceSheetMode = 'create' | 'update'

type SourceSaveVariables = {
  source?: UpstreamSource
  values: UpstreamSourceFormValues
}

const SOURCE_TYPE_OPTIONS = [
  {
    label: 'sub2api',
    value: UPSTREAM_SOURCE_TYPE_SUB2API,
  },
]

function defaultSourceFormValues(
  source?: UpstreamSource
): UpstreamSourceFormValues {
  return {
    name: source?.name ?? '',
    type: source?.type ?? UPSTREAM_SOURCE_TYPE_SUB2API,
    status:
      source?.status === 'disabled' || source?.status === 'enabled'
        ? source.status
        : 'enabled',
    base_url: source?.base_url ?? '',
    admin_api_base_path: source?.admin_api_base_path || '/api/v1',
    relay_base_url: source?.relay_base_url ?? '',
    email: '',
    password: '',
    local_group: source?.local_group || 'default',
    channel_type: source?.channel_type || CHANNEL_TYPE_OPENAI,
    default_priority: source?.default_priority ?? 0,
    default_weight: source?.default_weight ?? 0,
    enable_monitor: source?.enable_monitor ?? false,
    monitor_interval_minutes:
      source?.monitor_interval_minutes || DEFAULT_MONITOR_INTERVAL_MINUTES,
    auto_sync_models: source?.auto_sync_models ?? true,
  }
}

function buildCreatePayload(
  values: UpstreamSourceFormValues
): UpstreamSourceCreateRequest {
  return {
    name: values.name.trim(),
    type: values.type,
    base_url: values.base_url.trim(),
    admin_api_base_path: values.admin_api_base_path.trim(),
    relay_base_url: values.relay_base_url.trim(),
    email: values.email.trim(),
    password: values.password,
    local_group: values.local_group.trim(),
    channel_type: values.channel_type,
    default_priority: values.default_priority,
    default_weight: Math.max(0, values.default_weight),
    enable_monitor: values.enable_monitor,
    monitor_interval_minutes: Math.max(0, values.monitor_interval_minutes),
    auto_sync_models: values.auto_sync_models,
  }
}

function buildUpdatePayload(
  values: UpstreamSourceFormValues
): UpstreamSourceUpdateRequest {
  return {
    name: values.name.trim(),
    type: values.type,
    status: values.status,
    base_url: values.base_url.trim(),
    admin_api_base_path: values.admin_api_base_path.trim(),
    relay_base_url: values.relay_base_url.trim(),
    local_group: values.local_group.trim(),
    channel_type: values.channel_type,
    default_priority: values.default_priority,
    default_weight: Math.max(0, values.default_weight),
    enable_monitor: values.enable_monitor,
    monitor_interval_minutes: Math.max(0, values.monitor_interval_minutes),
    auto_sync_models: values.auto_sync_models,
  }
}

function parseIntegerInput(value: string, fallback = 0) {
  const parsed = Number.parseInt(value, 10)
  return Number.isFinite(parsed) ? parsed : fallback
}

function statusLabel(status?: string) {
  switch (status) {
    case 'enabled':
      return 'Enabled'
    case 'disabled':
      return 'Disabled'
    case 'succeeded':
      return 'Succeeded'
    case 'failed':
      return 'Failed'
    case 'running':
      return 'Running'
    case 'active':
      return 'Active'
    case 'stale':
      return 'Stale'
    case 'invalid':
      return 'Invalid'
    case 'synced':
      return 'Synced'
    case 'skipped':
      return 'Skipped'
    case 'needs_attention':
      return 'Needs Attention'
    case 'never_synced':
    case 'never_run':
    case '':
    case undefined:
      return 'Never Run'
    default:
      return status
  }
}

function statusVariant(status?: string): StatusVariant {
  switch (status) {
    case 'enabled':
    case 'succeeded':
    case 'active':
    case 'synced':
      return 'success'
    case 'running':
      return 'info'
    case 'stale':
    case 'skipped':
    case 'needs_attention':
      return 'warning'
    case 'disabled':
    case 'never_synced':
    case 'never_run':
    case '':
    case undefined:
      return 'neutral'
    case 'failed':
    case 'invalid':
      return 'danger'
    default:
      return 'neutral'
  }
}

function formatRate(value?: number | null) {
  if (value === null || value === undefined) {
    return '-'
  }
  return `${value.toFixed(3)}x`
}

function formatOptionalTimestamp(value: number) {
  return value > 0 ? formatTimestamp(value) : '-'
}

function apiErrorMessage<T>(result: ApiResponse<T>, fallback: string) {
  return result.message || fallback
}

function UpstreamStatusBadge(props: { status?: string; label?: string }) {
  const { t } = useTranslation()
  const label = props.label ?? statusLabel(props.status)

  return (
    <StatusBadge
      label={t(label)}
      variant={statusVariant(props.status)}
      copyable={false}
    />
  )
}

function StatusWithTime(props: {
  status?: UpstreamDiscoveryStatus | UpstreamSyncStatus | ''
  timestamp: number
  error?: string
}) {
  const { t } = useTranslation()

  return (
    <div className='flex min-w-0 flex-col gap-1'>
      <UpstreamStatusBadge status={props.status} />
      <span className='text-muted-foreground text-xs'>
        {formatOptionalTimestamp(props.timestamp)}
      </span>
      {props.error && (
        <LongText className='text-destructive max-w-[220px] text-xs'>
          {t(props.error)}
        </LongText>
      )}
    </div>
  )
}

function SourceSettingBadges(props: { source: UpstreamSource }) {
  const { t } = useTranslation()
  const monitorLabel = props.source.enable_monitor
    ? `${t('Monitor On')} / ${props.source.monitor_interval_minutes || DEFAULT_MONITOR_INTERVAL_MINUTES}m`
    : t('Monitor Off')

  return (
    <div className='flex max-w-[220px] flex-wrap gap-1'>
      <StatusBadge
        label={`${t('Priority')}: ${props.source.default_priority}`}
        variant='neutral'
        copyable={false}
      />
      <StatusBadge
        label={`${t('Weight')}: ${props.source.default_weight}`}
        variant='neutral'
        copyable={false}
      />
      <StatusBadge
        label={monitorLabel}
        variant={props.source.enable_monitor ? 'success' : 'neutral'}
        copyable={false}
      />
    </div>
  )
}

function useUpstreamSourceColumns(props: {
  onEdit: (source: UpstreamSource) => void
  onCredentials: (source: UpstreamSource) => void
  onMappings: (source: UpstreamSource) => void
  onDiscover: (source: UpstreamSource) => void
  onSync: (source: UpstreamSource) => void
  onDelete: (source: UpstreamSource) => void
  discoveringID?: number
  syncingID?: number
}): ColumnDef<UpstreamSource>[] {
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
        header: t('Source'),
        cell: ({ row }) => {
          const source = row.original
          return (
            <div className='flex min-w-[220px] flex-col gap-1'>
              <div className='flex items-center gap-2'>
                <LongText className='max-w-[180px] font-medium'>
                  {source.name}
                </LongText>
                <UpstreamStatusBadge status={source.status} />
              </div>
              <LongText className='text-muted-foreground max-w-[260px] text-xs'>
                {source.base_url}
              </LongText>
              {source.relay_base_url &&
                source.relay_base_url !== source.base_url && (
                  <LongText className='text-muted-foreground max-w-[260px] text-xs'>
                    {source.relay_base_url}
                  </LongText>
                )}
            </div>
          )
        },
        enableHiding: false,
        size: 320,
        meta: { mobileTitle: true },
      },
      {
        accessorKey: 'type',
        header: t('Type'),
        cell: ({ row }) => (
          <StatusBadge
            label={row.getValue('type')}
            variant='info'
            copyable={false}
          />
        ),
        filterFn: (row, id, value) =>
          Array.isArray(value) && value.includes(String(row.getValue(id))),
        size: 120,
      },
      {
        accessorKey: 'status',
        header: t('Status'),
        cell: ({ row }) => (
          <UpstreamStatusBadge status={row.getValue('status')} />
        ),
        filterFn: (row, id, value) =>
          Array.isArray(value) && value.includes(String(row.getValue(id))),
        size: 120,
        meta: { mobileBadge: true },
      },
      {
        id: 'credentials',
        header: t('Credentials'),
        cell: ({ row }) => {
          const source = row.original
          return (
            <div className='flex min-w-[160px] flex-col gap-1'>
              <StatusBadge
                label={source.has_credentials ? t('Configured') : t('Missing')}
                variant={source.has_credentials ? 'success' : 'warning'}
                copyable={false}
              />
              <LongText className='text-muted-foreground max-w-[160px] text-xs'>
                {source.masked_email || '-'}
              </LongText>
            </div>
          )
        },
        size: 180,
      },
      {
        id: 'settings',
        header: t('Channel Settings'),
        cell: ({ row }) => <SourceSettingBadges source={row.original} />,
        size: 240,
      },
      {
        id: 'discovery',
        header: t('Discovery'),
        cell: ({ row }) => (
          <StatusWithTime
            status={row.original.last_discovery_status}
            timestamp={row.original.last_discovery_time}
            error={row.original.last_discovery_error}
          />
        ),
        size: 180,
      },
      {
        id: 'sync',
        header: t('Sync'),
        cell: ({ row }) => (
          <StatusWithTime
            status={row.original.last_sync_status}
            timestamp={row.original.last_sync_time}
            error={row.original.last_sync_error}
          />
        ),
        size: 180,
      },
      {
        accessorKey: 'created_time',
        header: t('Created At'),
        cell: ({ row }) => (
          <span className='text-muted-foreground text-sm'>
            {formatOptionalTimestamp(row.getValue('created_time'))}
          </span>
        ),
        size: 180,
        meta: { mobileHidden: true },
      },
      {
        id: 'actions',
        header: () => t('Actions'),
        cell: ({ row }) => (
          <SourceActions
            row={row}
            onEdit={props.onEdit}
            onCredentials={props.onCredentials}
            onMappings={props.onMappings}
            onDiscover={props.onDiscover}
            onSync={props.onSync}
            onDelete={props.onDelete}
            discovering={props.discoveringID === row.original.id}
            syncing={props.syncingID === row.original.id}
          />
        ),
        meta: { pinned: 'right' as const },
      },
    ],
    [props, t]
  )
}

function SourceActions(props: {
  row: Row<UpstreamSource>
  onEdit: (source: UpstreamSource) => void
  onCredentials: (source: UpstreamSource) => void
  onMappings: (source: UpstreamSource) => void
  onDiscover: (source: UpstreamSource) => void
  onSync: (source: UpstreamSource) => void
  onDelete: (source: UpstreamSource) => void
  discovering: boolean
  syncing: boolean
}) {
  const { t } = useTranslation()
  const source = props.row.original

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
        <DropdownMenuContent align='end' className='w-[190px]'>
          <DropdownMenuItem onClick={() => props.onEdit(source)}>
            {t('Edit')}
            <DropdownMenuShortcut>
              <Pencil size={16} />
            </DropdownMenuShortcut>
          </DropdownMenuItem>
          <DropdownMenuItem onClick={() => props.onCredentials(source)}>
            {t('Credentials')}
            <DropdownMenuShortcut>
              <KeyRound size={16} />
            </DropdownMenuShortcut>
          </DropdownMenuItem>
          <DropdownMenuItem onClick={() => props.onMappings(source)}>
            {t('Mappings')}
            <DropdownMenuShortcut>
              <Settings2 size={16} />
            </DropdownMenuShortcut>
          </DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuItem
            disabled={props.discovering}
            onClick={() => props.onDiscover(source)}
          >
            {props.discovering ? t('Discovering...') : t('Discover')}
            <DropdownMenuShortcut>
              {props.discovering ? (
                <Loader2 size={16} className='animate-spin' />
              ) : (
                <Radar size={16} />
              )}
            </DropdownMenuShortcut>
          </DropdownMenuItem>
          <DropdownMenuItem
            disabled={props.syncing}
            onClick={() => props.onSync(source)}
          >
            {props.syncing ? t('Syncing...') : t('Sync')}
            <DropdownMenuShortcut>
              {props.syncing ? (
                <Loader2 size={16} className='animate-spin' />
              ) : (
                <RefreshCcw size={16} />
              )}
            </DropdownMenuShortcut>
          </DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuItem
            className='text-destructive focus:text-destructive'
            onClick={() => props.onDelete(source)}
          >
            {t('Delete')}
            <DropdownMenuShortcut>
              <Trash2 size={16} />
            </DropdownMenuShortcut>
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
    </div>
  )
}

function FieldBlock(props: {
  label: string
  htmlFor?: string
  children: ReactNode
  description?: ReactNode
}) {
  return (
    <div className='flex flex-col gap-1.5'>
      <Label htmlFor={props.htmlFor}>{props.label}</Label>
      {props.children}
      {props.description && (
        <p className='text-muted-foreground text-xs leading-5'>
          {props.description}
        </p>
      )}
    </div>
  )
}

function SwitchRow(props: {
  label: string
  description?: ReactNode
  checked: boolean
  onCheckedChange: (checked: boolean) => void
}) {
  return (
    <div className={sideDrawerSwitchItemClassName()}>
      <div className='min-w-0 flex-1'>
        <Label>{props.label}</Label>
        {props.description && (
          <p className='text-muted-foreground mt-1 text-xs leading-5'>
            {props.description}
          </p>
        )}
      </div>
      <Switch
        checked={props.checked}
        onCheckedChange={(checked) => props.onCheckedChange(Boolean(checked))}
      />
    </div>
  )
}

function SourceFormSheet(props: {
  open: boolean
  mode: SourceSheetMode
  source?: UpstreamSource
  isSubmitting: boolean
  onOpenChange: (open: boolean) => void
  onSubmit: (values: UpstreamSourceFormValues) => void
}) {
  const { t } = useTranslation()
  const [form, setForm] = useState<UpstreamSourceFormValues>(
    defaultSourceFormValues(props.source)
  )
  const isUpdate = props.mode === 'update'

  const setField = <K extends keyof UpstreamSourceFormValues>(
    key: K,
    value: UpstreamSourceFormValues[K]
  ) => {
    setForm((previous) => ({ ...previous, [key]: value }))
  }

  const handleSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!form.name.trim()) {
      toast.error(t('Source name is required'))
      return
    }
    if (!form.base_url.trim()) {
      toast.error(t('Base URL is required'))
      return
    }
    props.onSubmit(form)
  }

  return (
    <Sheet open={props.open} onOpenChange={props.onOpenChange}>
      <SheetContent className={sideDrawerContentClassName('sm:max-w-[660px]')}>
        <SheetHeader className={sideDrawerHeaderClassName()}>
          <SheetTitle>
            {isUpdate ? t('Edit Upstream Source') : t('Add Upstream Source')}
          </SheetTitle>
          <SheetDescription>
            {isUpdate ? props.source?.name : t('sub2api')}
          </SheetDescription>
        </SheetHeader>
        <form
          id='upstream-source-form'
          className={sideDrawerFormClassName()}
          onSubmit={handleSubmit}
        >
          <SideDrawerSection>
            <SideDrawerSectionHeader title={t('Connection')} />
            <FieldBlock label={t('Name')} htmlFor='source-name'>
              <Input
                id='source-name'
                value={form.name}
                onChange={(event) => setField('name', event.target.value)}
              />
            </FieldBlock>
            <div className='grid gap-4 sm:grid-cols-2'>
              <FieldBlock label={t('Type')} htmlFor='source-type'>
                <Select
                  items={SOURCE_TYPE_OPTIONS}
                  value={form.type}
                  onValueChange={(value) =>
                    value && setField('type', value as UpstreamSourceType)
                  }
                >
                  <SelectTrigger id='source-type'>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent alignItemWithTrigger={false}>
                    <SelectGroup>
                      <SelectItem value={UPSTREAM_SOURCE_TYPE_SUB2API}>
                        sub2api
                      </SelectItem>
                    </SelectGroup>
                  </SelectContent>
                </Select>
              </FieldBlock>
              {isUpdate && (
                <FieldBlock label={t('Status')} htmlFor='source-status'>
                  <Select
                    items={[
                      { label: t('Enabled'), value: 'enabled' },
                      { label: t('Disabled'), value: 'disabled' },
                    ]}
                    value={form.status}
                    onValueChange={(value) =>
                      value &&
                      setField(
                        'status',
                        value as Exclude<UpstreamSourceStatus, 'deleted'>
                      )
                    }
                  >
                    <SelectTrigger id='source-status'>
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent alignItemWithTrigger={false}>
                      <SelectGroup>
                        <SelectItem value='enabled'>{t('Enabled')}</SelectItem>
                        <SelectItem value='disabled'>
                          {t('Disabled')}
                        </SelectItem>
                      </SelectGroup>
                    </SelectContent>
                  </Select>
                </FieldBlock>
              )}
            </div>
            <FieldBlock label={t('Base URL')} htmlFor='source-base-url'>
              <Input
                id='source-base-url'
                value={form.base_url}
                placeholder='https://example.com'
                onChange={(event) => setField('base_url', event.target.value)}
              />
            </FieldBlock>
            <div className='grid gap-4 sm:grid-cols-2'>
              <FieldBlock
                label={t('Admin API Base Path')}
                htmlFor='source-admin-api-base-path'
              >
                <Input
                  id='source-admin-api-base-path'
                  value={form.admin_api_base_path}
                  onChange={(event) =>
                    setField('admin_api_base_path', event.target.value)
                  }
                />
              </FieldBlock>
              <FieldBlock
                label={t('Relay Base URL')}
                htmlFor='source-relay-url'
              >
                <Input
                  id='source-relay-url'
                  value={form.relay_base_url}
                  placeholder='https://example.com'
                  onChange={(event) =>
                    setField('relay_base_url', event.target.value)
                  }
                />
              </FieldBlock>
            </div>
          </SideDrawerSection>

          {!isUpdate && (
            <SideDrawerSection>
              <SideDrawerSectionHeader title={t('Credentials')} />
              <FieldBlock label={t('Email')} htmlFor='source-email'>
                <Input
                  id='source-email'
                  value={form.email}
                  autoComplete='off'
                  onChange={(event) => setField('email', event.target.value)}
                />
              </FieldBlock>
              <FieldBlock label={t('Password')} htmlFor='source-password'>
                <Input
                  id='source-password'
                  type='password'
                  value={form.password}
                  autoComplete='new-password'
                  onChange={(event) => setField('password', event.target.value)}
                />
              </FieldBlock>
            </SideDrawerSection>
          )}

          <SideDrawerSection>
            <SideDrawerSectionHeader title={t('Generated Channels')} />
            <div className='grid gap-4 sm:grid-cols-2'>
              <FieldBlock label={t('Local Group')} htmlFor='source-local-group'>
                <Input
                  id='source-local-group'
                  value={form.local_group}
                  onChange={(event) =>
                    setField('local_group', event.target.value)
                  }
                />
              </FieldBlock>
              <FieldBlock
                label={t('Channel Type')}
                htmlFor='source-channel-type'
              >
                <Input
                  id='source-channel-type'
                  type='number'
                  min={1}
                  value={form.channel_type}
                  onChange={(event) =>
                    setField(
                      'channel_type',
                      parseIntegerInput(event.target.value, CHANNEL_TYPE_OPENAI)
                    )
                  }
                />
              </FieldBlock>
            </div>
            <div className='grid gap-4 sm:grid-cols-2'>
              <FieldBlock
                label={t('Default Priority')}
                htmlFor='source-default-priority'
              >
                <Input
                  id='source-default-priority'
                  type='number'
                  value={form.default_priority}
                  onChange={(event) =>
                    setField(
                      'default_priority',
                      parseIntegerInput(event.target.value)
                    )
                  }
                />
              </FieldBlock>
              <FieldBlock
                label={t('Default Weight')}
                htmlFor='source-default-weight'
              >
                <Input
                  id='source-default-weight'
                  type='number'
                  min={0}
                  value={form.default_weight}
                  onChange={(event) =>
                    setField(
                      'default_weight',
                      parseIntegerInput(event.target.value)
                    )
                  }
                />
              </FieldBlock>
            </div>
            <SwitchRow
              label={t('Enable Channel Monitor')}
              checked={form.enable_monitor}
              onCheckedChange={(checked) => setField('enable_monitor', checked)}
            />
            <FieldBlock
              label={t('Monitor Interval Minutes')}
              htmlFor='source-monitor-interval'
            >
              <Input
                id='source-monitor-interval'
                type='number'
                min={0}
                value={form.monitor_interval_minutes}
                onChange={(event) =>
                  setField(
                    'monitor_interval_minutes',
                    parseIntegerInput(
                      event.target.value,
                      DEFAULT_MONITOR_INTERVAL_MINUTES
                    )
                  )
                }
              />
            </FieldBlock>
            <SwitchRow
              label={t('Auto Sync Models')}
              checked={form.auto_sync_models}
              onCheckedChange={(checked) =>
                setField('auto_sync_models', checked)
              }
            />
          </SideDrawerSection>
        </form>
        <SheetFooter className={sideDrawerFooterClassName()}>
          <SheetClose render={<Button variant='outline' />}>
            {t('Close')}
          </SheetClose>
          <Button
            form='upstream-source-form'
            type='submit'
            disabled={props.isSubmitting}
          >
            {props.isSubmitting && (
              <Loader2 data-icon='inline-start' className='animate-spin' />
            )}
            {props.isSubmitting ? t('Saving...') : t('Save changes')}
          </Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  )
}

function CredentialsSheet(props: {
  open: boolean
  source?: UpstreamSource
  isSubmitting: boolean
  onOpenChange: (open: boolean) => void
  onSubmit: (values: { email: string; password: string }) => void
}) {
  const { t } = useTranslation()
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')

  const handleSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    props.onSubmit({ email, password })
  }

  return (
    <Sheet open={props.open} onOpenChange={props.onOpenChange}>
      <SheetContent className={sideDrawerContentClassName('sm:max-w-[520px]')}>
        <SheetHeader className={sideDrawerHeaderClassName()}>
          <SheetTitle>{t('Update Credentials')}</SheetTitle>
          <SheetDescription>{props.source?.name}</SheetDescription>
        </SheetHeader>
        <form
          id='upstream-source-credentials-form'
          className={sideDrawerFormClassName()}
          onSubmit={handleSubmit}
        >
          <SideDrawerSection>
            <FieldBlock label={t('Email')} htmlFor='credentials-email'>
              <Input
                id='credentials-email'
                value={email}
                placeholder={props.source?.masked_email || ''}
                autoComplete='off'
                onChange={(event) => setEmail(event.target.value)}
              />
            </FieldBlock>
            <FieldBlock label={t('Password')} htmlFor='credentials-password'>
              <Input
                id='credentials-password'
                type='password'
                value={password}
                autoComplete='new-password'
                onChange={(event) => setPassword(event.target.value)}
              />
            </FieldBlock>
          </SideDrawerSection>
        </form>
        <SheetFooter className={sideDrawerFooterClassName()}>
          <SheetClose render={<Button variant='outline' />}>
            {t('Close')}
          </SheetClose>
          <Button
            form='upstream-source-credentials-form'
            type='submit'
            disabled={props.isSubmitting}
          >
            {props.isSubmitting && (
              <Loader2 data-icon='inline-start' className='animate-spin' />
            )}
            {t('Save credentials')}
          </Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  )
}

function MappingsSheet(props: {
  open: boolean
  source?: UpstreamSource
  onOpenChange: (open: boolean) => void
  onDiscover: (source: UpstreamSource) => void
  onSync: (source: UpstreamSource) => void
  discovering: boolean
  syncing: boolean
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const sourceID = props.source?.id ?? 0
  const [mappingSelectionOverrides, setMappingSelectionOverrides] = useState<
    Record<number, boolean>
  >({})

  const mappingsQuery = useQuery({
    queryKey: upstreamSourcesQueryKeys.mappings(sourceID),
    enabled: props.open && sourceID > 0,
    queryFn: async () => {
      const result = await listUpstreamSourceMappings(sourceID)
      if (!result.success) {
        throw new Error(result.message || 'Failed to load mappings')
      }
      return result.data ?? []
    },
  })

  useEffect(() => {
    if (mappingsQuery.error) {
      toast.error(mappingsQuery.error.message)
    }
  }, [mappingsQuery.error])

  const mappings = mappingsQuery.data ?? EMPTY_UPSTREAM_SOURCE_MAPPINGS
  const selectedMappingIDs = useMemo(
    () =>
      mappings
        .filter(
          (mapping) =>
            mappingSelectionOverrides[mapping.id] ?? mapping.sync_enabled
        )
        .map((mapping) => mapping.id),
    [mappingSelectionOverrides, mappings]
  )
  const selectedMappingIDSet = useMemo(
    () => new Set(selectedMappingIDs),
    [selectedMappingIDs]
  )

  const saveMutation = useMutation({
    mutationFn: async (mappingIDs: number[]) =>
      updateUpstreamSourceMappings(sourceID, mappingIDs),
    onSuccess: (result) => {
      if (!result.success) {
        toast.error(apiErrorMessage(result, t('Failed to save mappings')))
        return
      }
      toast.success(t('Mappings saved'))
      setMappingSelectionOverrides({})
      queryClient.invalidateQueries({
        queryKey: upstreamSourcesQueryKeys.mappings(sourceID),
      })
      queryClient.invalidateQueries({
        queryKey: upstreamSourcesQueryKeys.list(),
      })
    },
    onError: (error) => {
      toast.error(error instanceof Error ? error.message : t('Request failed'))
    },
  })

  const toggleMapping = (mappingID: number, checked: boolean) => {
    setMappingSelectionOverrides((previous) => ({
      ...previous,
      [mappingID]: checked,
    }))
  }

  const selectedCount = selectedMappingIDs.length

  return (
    <Sheet open={props.open} onOpenChange={props.onOpenChange}>
      <SheetContent className={sideDrawerContentClassName('sm:max-w-[920px]')}>
        <SheetHeader className={sideDrawerHeaderClassName()}>
          <div className='flex items-start justify-between gap-3'>
            <div className='min-w-0'>
              <SheetTitle>{t('Upstream Mappings')}</SheetTitle>
              <SheetDescription>{props.source?.name}</SheetDescription>
            </div>
            {props.source && (
              <div className='flex shrink-0 gap-2'>
                <Button
                  type='button'
                  variant='outline'
                  disabled={props.discovering}
                  onClick={() => props.source && props.onDiscover(props.source)}
                >
                  {props.discovering ? (
                    <Loader2
                      data-icon='inline-start'
                      className='animate-spin'
                    />
                  ) : (
                    <Radar data-icon='inline-start' />
                  )}
                  {t('Discover')}
                </Button>
                <Button
                  type='button'
                  variant='outline'
                  disabled={props.syncing}
                  onClick={() => props.source && props.onSync(props.source)}
                >
                  {props.syncing ? (
                    <Loader2
                      data-icon='inline-start'
                      className='animate-spin'
                    />
                  ) : (
                    <RefreshCcw data-icon='inline-start' />
                  )}
                  {t('Sync')}
                </Button>
              </div>
            )}
          </div>
        </SheetHeader>
        <div className={sideDrawerFormClassName('gap-3')}>
          <div className='flex items-center justify-between gap-3'>
            <span className='text-muted-foreground text-sm'>
              {t('{{count}} selected', { count: selectedCount })}
            </span>
            {mappingsQuery.isFetching && (
              <span className='text-muted-foreground flex items-center gap-1 text-xs'>
                <Loader2 className='animate-spin' />
                {t('Refreshing...')}
              </span>
            )}
          </div>
          <div className='border-border rounded-lg border'>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead className='w-[44px]' />
                  <TableHead>{t('Group')}</TableHead>
                  <TableHead>{t('Platform')}</TableHead>
                  <TableHead>{t('Rate')}</TableHead>
                  <TableHead>{t('Discovery')}</TableHead>
                  <TableHead>{t('Sync')}</TableHead>
                  <TableHead>{t('Channel')}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {mappingsQuery.isLoading && (
                  <TableRow>
                    <TableCell colSpan={7}>
                      <div className='text-muted-foreground flex items-center justify-center gap-2 py-8 text-sm'>
                        <Loader2 className='animate-spin' />
                        {t('Loading...')}
                      </div>
                    </TableCell>
                  </TableRow>
                )}
                {!mappingsQuery.isLoading && mappings.length === 0 && (
                  <TableRow>
                    <TableCell colSpan={7}>
                      <div className='text-muted-foreground py-8 text-center text-sm'>
                        {t('No mappings found')}
                      </div>
                    </TableCell>
                  </TableRow>
                )}
                {mappings.map((mapping) => (
                  <MappingRow
                    key={mapping.id}
                    mapping={mapping}
                    checked={selectedMappingIDSet.has(mapping.id)}
                    onCheckedChange={(checked) =>
                      toggleMapping(mapping.id, checked)
                    }
                  />
                ))}
              </TableBody>
            </Table>
          </div>
        </div>
        <SheetFooter className={sideDrawerFooterClassName()}>
          <SheetClose render={<Button variant='outline' />}>
            {t('Close')}
          </SheetClose>
          <Button
            type='button'
            disabled={saveMutation.isPending}
            onClick={() => saveMutation.mutate(selectedMappingIDs)}
          >
            {saveMutation.isPending ? (
              <Loader2 data-icon='inline-start' className='animate-spin' />
            ) : (
              <Save data-icon='inline-start' />
            )}
            {t('Save mappings')}
          </Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  )
}

function MappingRow(props: {
  mapping: UpstreamSourceMapping
  checked: boolean
  onCheckedChange: (checked: boolean) => void
}) {
  const { t } = useTranslation()
  const mapping = props.mapping
  const groupName = mapping.upstream_group_name || mapping.upstream_group_id
  const rowMuted =
    mapping.discovery_status === 'invalid' || !mapping.has_upstream_key

  return (
    <TableRow className={cn(rowMuted && 'text-muted-foreground')}>
      <TableCell>
        <Checkbox
          checked={props.checked}
          onCheckedChange={(checked) => props.onCheckedChange(Boolean(checked))}
          aria-label={t('Select mapping')}
        />
      </TableCell>
      <TableCell>
        <div className='flex min-w-[180px] flex-col gap-1'>
          <LongText className='max-w-[220px] font-medium'>{groupName}</LongText>
          <span className='text-muted-foreground text-xs'>
            {mapping.upstream_group_id}
          </span>
        </div>
      </TableCell>
      <TableCell>
        <StatusBadge
          label={mapping.upstream_platform || '-'}
          variant='neutral'
          copyable={false}
        />
      </TableCell>
      <TableCell>
        <div className='flex flex-col gap-1'>
          <span className='text-sm font-medium'>
            {formatRate(mapping.effective_rate_multiplier)}
          </span>
          <span className='text-muted-foreground text-xs'>
            {formatRate(mapping.upstream_rate_multiplier)}
          </span>
        </div>
      </TableCell>
      <TableCell>
        <UpstreamStatusBadge
          status={mapping.discovery_status as UpstreamMappingDiscoveryStatus}
        />
      </TableCell>
      <TableCell>
        <div className='flex flex-col gap-1'>
          <UpstreamStatusBadge
            status={mapping.sync_status as UpstreamMappingSyncStatus}
          />
          {mapping.last_error && (
            <LongText className='text-destructive max-w-[180px] text-xs'>
              {mapping.last_error}
            </LongText>
          )}
        </div>
      </TableCell>
      <TableCell>
        {mapping.local_channel_id > 0 ? (
          <TableId value={mapping.local_channel_id} />
        ) : (
          <StatusBadge
            label={mapping.has_upstream_key ? t('Pending') : t('No Key')}
            variant={mapping.has_upstream_key ? 'warning' : 'danger'}
            copyable={false}
          />
        )}
      </TableCell>
    </TableRow>
  )
}

export function UpstreamSources() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const isMobile = useMediaQuery('(max-width: 640px)')
  const [globalFilter, setGlobalFilter] = useState('')
  const [columnFilters, setColumnFilters] = useState<ColumnFiltersState>([])
  const [pagination, setPagination] = useState<PaginationState>({
    pageIndex: 0,
    pageSize: isMobile ? 10 : 20,
  })
  const [sourceSheetMode, setSourceSheetMode] =
    useState<SourceSheetMode | null>(null)
  const [currentSource, setCurrentSource] = useState<UpstreamSource>()
  const [credentialSource, setCredentialSource] = useState<UpstreamSource>()
  const [mappingSource, setMappingSource] = useState<UpstreamSource>()
  const [deleteSource, setDeleteSource] = useState<UpstreamSource>()

  const sourcesQuery = useQuery({
    queryKey: upstreamSourcesQueryKeys.list(),
    queryFn: async () => {
      const result = await listUpstreamSources()
      if (!result.success) {
        throw new Error(result.message || 'Failed to load upstream sources')
      }
      return result.data ?? []
    },
  })

  useEffect(() => {
    if (sourcesQuery.error) {
      toast.error(sourcesQuery.error.message)
    }
  }, [sourcesQuery.error])

  const invalidateSources = () => {
    queryClient.invalidateQueries({
      queryKey: upstreamSourcesQueryKeys.all,
    })
  }

  const sourceSaveMutation = useMutation({
    mutationFn: async ({ source, values }: SourceSaveVariables) => {
      return source
        ? updateUpstreamSource(source.id, buildUpdatePayload(values))
        : createUpstreamSource(buildCreatePayload(values))
    },
    onSuccess: (result, variables) => {
      if (!result.success) {
        toast.error(
          apiErrorMessage(
            result,
            variables.source
              ? t('Failed to update upstream source')
              : t('Failed to create upstream source')
          )
        )
        return
      }
      toast.success(
        variables.source
          ? t('Upstream source updated')
          : t('Upstream source created')
      )
      setSourceSheetMode(null)
      setCurrentSource(undefined)
      invalidateSources()
    },
    onError: (error) => {
      toast.error(error instanceof Error ? error.message : t('Request failed'))
    },
  })

  const credentialsMutation = useMutation({
    mutationFn: async (variables: {
      source: UpstreamSource
      email: string
      password: string
    }) =>
      updateUpstreamSourceCredentials(variables.source.id, {
        email: variables.email,
        password: variables.password,
      }),
    onSuccess: (result) => {
      if (!result.success) {
        toast.error(apiErrorMessage(result, t('Failed to update credentials')))
        return
      }
      toast.success(t('Credentials updated'))
      setCredentialSource(undefined)
      invalidateSources()
    },
    onError: (error) => {
      toast.error(error instanceof Error ? error.message : t('Request failed'))
    },
  })

  const discoverMutation = useMutation({
    mutationFn: async (source: UpstreamSource) =>
      discoverUpstreamSource(source.id),
    onSuccess: (result, source) => {
      if (!result.success) {
        toast.error(apiErrorMessage(result, t('Discovery failed')))
        return
      }
      toast.success(
        t('Discovered {{count}} upstream groups', {
          count: result.data?.discovered ?? 0,
        })
      )
      invalidateSources()
      queryClient.invalidateQueries({
        queryKey: upstreamSourcesQueryKeys.mappings(source.id),
      })
    },
    onError: (error) => {
      toast.error(error instanceof Error ? error.message : t('Request failed'))
    },
  })

  const syncMutation = useMutation({
    mutationFn: async (source: UpstreamSource) => syncUpstreamSource(source.id),
    onSuccess: (result, source) => {
      if (!result.success) {
        toast.error(apiErrorMessage(result, t('Sync failed')))
        return
      }
      const data: UpstreamSourceSyncResult | undefined = result.data
      toast.success(
        t(
          'Sync finished: {{created}} created, {{updated}} updated, {{failed}} failed',
          {
            created: data?.created ?? 0,
            updated: data?.updated ?? 0,
            failed: data?.failed ?? 0,
          }
        )
      )
      invalidateSources()
      queryClient.invalidateQueries({
        queryKey: upstreamSourcesQueryKeys.mappings(source.id),
      })
      queryClient.invalidateQueries({
        queryKey: upstreamSourcesQueryKeys.syncResult(source.id),
      })
    },
    onError: (error) => {
      toast.error(error instanceof Error ? error.message : t('Request failed'))
    },
  })

  const deleteMutation = useMutation({
    mutationFn: async (source: UpstreamSource) =>
      deleteUpstreamSource(source.id),
    onSuccess: (result) => {
      if (!result.success) {
        toast.error(
          apiErrorMessage(result, t('Failed to delete upstream source'))
        )
        return
      }
      toast.success(t('Upstream source deleted'))
      setDeleteSource(undefined)
      invalidateSources()
    },
    onError: (error) => {
      toast.error(error instanceof Error ? error.message : t('Request failed'))
    },
  })

  const openCreateSheet = () => {
    setCurrentSource(undefined)
    setSourceSheetMode('create')
  }

  const openUpdateSheet = (source: UpstreamSource) => {
    setCurrentSource(source)
    setSourceSheetMode('update')
  }

  const columns = useUpstreamSourceColumns({
    onEdit: openUpdateSheet,
    onCredentials: setCredentialSource,
    onMappings: setMappingSource,
    onDiscover: (source) => discoverMutation.mutate(source),
    onSync: (source) => syncMutation.mutate(source),
    onDelete: setDeleteSource,
    discoveringID: discoverMutation.variables?.id,
    syncingID: syncMutation.variables?.id,
  })

  const sources = sourcesQuery.data ?? []
  const { table } = useDataTable({
    data: sources,
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
      const source = row.original
      return [
        source.name,
        source.type,
        source.base_url,
        source.relay_base_url,
        source.masked_email,
        source.local_group,
      ].some((value) =>
        String(value || '')
          .toLowerCase()
          .includes(searchValue)
      )
    },
    initialColumnVisibility: {
      created_time: false,
    },
    columnVisibilityStorageKey: 'upstream-sources-table-columns',
  })

  return (
    <>
      <SectionPageLayout fixedContent>
        <SectionPageLayout.Title>
          {t('Upstream Sources')}
        </SectionPageLayout.Title>
        <SectionPageLayout.Actions>
          <Button type='button' onClick={openCreateSheet}>
            <Plus data-icon='inline-start' />
            {t('Add Source')}
          </Button>
        </SectionPageLayout.Actions>
        <SectionPageLayout.Content>
          <DataTablePage
            table={table}
            columns={columns}
            isLoading={sourcesQuery.isLoading}
            isFetching={sourcesQuery.isFetching}
            emptyTitle={t('No Upstream Sources Found')}
            emptyDescription={t(
              'Create an upstream source to start syncing channels.'
            )}
            skeletonKeyPrefix='upstream-sources-skeleton'
            applyHeaderSize
            toolbarProps={{
              searchPlaceholder: t('Filter by source, URL, email or group...'),
              filters: [
                {
                  columnId: 'status',
                  title: t('Status'),
                  singleSelect: true,
                  options: [
                    { label: t('Enabled'), value: 'enabled' },
                    { label: t('Disabled'), value: 'disabled' },
                  ],
                },
                {
                  columnId: 'type',
                  title: t('Type'),
                  singleSelect: true,
                  options: [{ label: 'sub2api', value: 'sub2api' }],
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

      <SourceFormSheet
        key={
          sourceSheetMode
            ? `${sourceSheetMode}-${currentSource?.id ?? 'new'}`
            : 'source-sheet-closed'
        }
        open={sourceSheetMode !== null}
        mode={sourceSheetMode ?? 'create'}
        source={currentSource}
        isSubmitting={sourceSaveMutation.isPending}
        onOpenChange={(open) => {
          if (!open) {
            setSourceSheetMode(null)
            setCurrentSource(undefined)
          }
        }}
        onSubmit={(values) =>
          sourceSaveMutation.mutate({
            source: sourceSheetMode === 'update' ? currentSource : undefined,
            values,
          })
        }
      />
      <CredentialsSheet
        key={
          credentialSource
            ? `credentials-${credentialSource.id}`
            : 'credentials-closed'
        }
        open={Boolean(credentialSource)}
        source={credentialSource}
        isSubmitting={credentialsMutation.isPending}
        onOpenChange={(open) => !open && setCredentialSource(undefined)}
        onSubmit={(values) => {
          if (!credentialSource) {
            return
          }
          credentialsMutation.mutate({
            source: credentialSource,
            email: values.email,
            password: values.password,
          })
        }}
      />
      <MappingsSheet
        key={mappingSource ? `mappings-${mappingSource.id}` : 'mappings-closed'}
        open={Boolean(mappingSource)}
        source={mappingSource}
        onOpenChange={(open) => !open && setMappingSource(undefined)}
        onDiscover={(source) => discoverMutation.mutate(source)}
        onSync={(source) => syncMutation.mutate(source)}
        discovering={
          discoverMutation.isPending &&
          discoverMutation.variables?.id === mappingSource?.id
        }
        syncing={
          syncMutation.isPending &&
          syncMutation.variables?.id === mappingSource?.id
        }
      />
      <ConfirmDialog
        open={Boolean(deleteSource)}
        onOpenChange={(open) => !open && setDeleteSource(undefined)}
        title={t('Delete Upstream Source')}
        desc={
          deleteSource
            ? t('Delete {{name}}?', { name: deleteSource.name })
            : t('Delete upstream source?')
        }
        destructive
        isLoading={deleteMutation.isPending}
        handleConfirm={() =>
          deleteSource && deleteMutation.mutate(deleteSource)
        }
      />
    </>
  )
}
