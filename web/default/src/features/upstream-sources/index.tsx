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
  ChevronDown,
  Cookie,
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
  TrendingUp,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { getUserGroups, getUserModels } from '@/lib/api'
import { formatTimestamp } from '@/lib/format'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'
import { Combobox } from '@/components/ui/combobox'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
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
import { Textarea } from '@/components/ui/textarea'
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
import { MultiSelect } from '@/components/multi-select'
import { StatusBadge, type StatusVariant } from '@/components/status-badge'
import { TableId } from '@/components/table-id'
import { CHANNEL_TYPE_OPTIONS } from '@/features/channels/constants'
import { channelsQueryKeys } from '@/features/channels/lib/channel-actions'
import {
  createUpstreamSource,
  deleteUpstreamSource,
  discoverUpstreamSource,
  importUpstreamSourceSession,
  listUpstreamSourceMappings,
  listUpstreamSources,
  runUpstreamSourceAutoPriority,
  syncUpstreamSource,
  updateUpstreamSource,
  updateUpstreamSourceCredentials,
  updateUpstreamSourceMappings,
  upstreamSourcesQueryKeys,
} from './api'
import {
  buildLocalGroupRuleTemplate,
  createLocalGroupRuleUserTemplate,
  DEFAULT_LOCAL_GROUP_RULE_STRATEGY_DEFAULTS,
  formatKeywordList,
  hasLocalGroupRuleMatcher,
  parseLocalGroupRuleUserTemplates,
  serializeLocalGroupRuleUserTemplates,
  type LocalGroupRuleTemplateKey,
  type LocalGroupRuleUserTemplate,
  normalizeKeywordList,
  normalizeCodexImageGenerationBridgePolicy,
  normalizeModelList,
  normalizeModelStrategy,
  resolveLocalGroupRuleStrategy,
  normalizeSyncRules,
  type LocalGroupRuleStrategyResolution,
  UPSTREAM_SOURCE_MODEL_STRATEGY_ALL,
  UPSTREAM_SOURCE_MODEL_STRATEGY_FIXED,
} from './rules'
import {
  hasMappingSelectionChanges,
  resolveSelectedMappingIDs,
} from './selection'
import {
  UPSTREAM_SOURCE_TYPE_NEW_API,
  UPSTREAM_SOURCE_TYPE_SUB2API,
  type ApiResponse,
  type CodexImageGenerationBridgePolicy,
  type UpstreamDiscoveryStatus,
  type UpstreamMappingDiscoveryStatus,
  type UpstreamMappingSyncStatus,
  type UpstreamSource,
  type UpstreamSourceAutoPriorityResult,
  type UpstreamSourceCreateRequest,
  type UpstreamSourceFormValues,
  type UpstreamSourceLocalGroupRule,
  type UpstreamSourceMapping,
  type UpstreamSourceSessionImportRequest,
  type UpstreamSourceStatus,
  type UpstreamSourceSyncResult,
  type UpstreamSourceType,
  type UpstreamSourceUpdateRequest,
  type UpstreamSyncStatus,
} from './types'

const DEFAULT_AUTO_PRIORITY_INTERVAL_MINUTES = 30
const DEFAULT_AUTO_PRIORITY_WINDOW_HOURS = 24
const EMPTY_UPSTREAM_SOURCE_MAPPINGS: UpstreamSourceMapping[] = []
const UPSTREAM_SOURCE_PLATFORM_OPTIONS = [
  { value: 'openai', label: 'OpenAI' },
  { value: 'anthropic', label: 'Anthropic' },
]
const LOCAL_GROUP_RULE_TEMPLATE_SETS: {
  label: string
  keys: LocalGroupRuleTemplateKey[]
}[] = [
  { label: 'OpenAI / Pro', keys: ['openai', 'openai-pro'] },
  { label: 'Anthropic / Pro', keys: ['anthropic', 'anthropic-pro'] },
  {
    label: 'OpenAI + Anthropic',
    keys: ['openai', 'openai-pro', 'anthropic', 'anthropic-pro'],
  },
]
const USER_RULE_TEMPLATES_STORAGE_KEY =
  'wynth.upstreamSource.localGroupRuleTemplates'

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
  {
    label: 'new-api',
    value: UPSTREAM_SOURCE_TYPE_NEW_API,
  },
]

function defaultAdminAPIBasePath(sourceType: UpstreamSourceType) {
  return sourceType === UPSTREAM_SOURCE_TYPE_NEW_API ? '/api' : '/api/v1'
}

function sourceTypeLabel(sourceType: UpstreamSourceType) {
  return sourceType === UPSTREAM_SOURCE_TYPE_NEW_API ? 'new-api' : 'sub2api'
}

function codexImageGenerationBridgePolicyLabel(
  policy: CodexImageGenerationBridgePolicy
) {
  switch (policy) {
    case 'enabled':
      return 'Force enable'
    case 'disabled':
      return 'Force disable'
    default:
      return 'Follow channel'
  }
}

const DEFAULT_RULE_CHANNEL_TYPE = 1 // OpenAI
const DEFAULT_RULE_PRIORITY = 0
const DEFAULT_RULE_WEIGHT = 1

function emptyLocalGroupRule(): UpstreamSourceLocalGroupRule {
  return {
    name: '',
    local_group: '',
    platforms: [],
    name_contains: [],
    description_contains: [],
    exclude_keywords: [],
    channel_type: DEFAULT_RULE_CHANNEL_TYPE,
    priority: DEFAULT_RULE_PRIORITY,
    weight: DEFAULT_RULE_WEIGHT,
    monitor: { model: '' },
    model_strategy: UPSTREAM_SOURCE_MODEL_STRATEGY_ALL,
    fixed_models: [],
  }
}

function intervalDisplayValue(value: number | undefined, fallback: number) {
  return value !== undefined && value >= 0 ? value : fallback
}

function monitorIntervalDisplayValue(
  value: number | undefined,
  fallback: number
) {
  return value && value > 0 ? value : fallback
}

function normalizeRuleForForm(
  rule: Partial<UpstreamSourceLocalGroupRule>
): UpstreamSourceLocalGroupRule {
  const base = emptyLocalGroupRule()
  return {
    ...base,
    ...rule,
    platforms: normalizeKeywordList(rule.platforms ?? []),
    name_contains: normalizeKeywordList(rule.name_contains ?? []),
    description_contains: normalizeKeywordList(rule.description_contains ?? []),
    exclude_keywords: normalizeKeywordList(rule.exclude_keywords ?? []),
    channel_type:
      typeof rule.channel_type === 'number' &&
      Number.isFinite(rule.channel_type)
        ? rule.channel_type
        : base.channel_type,
    priority:
      typeof rule.priority === 'number' && Number.isFinite(rule.priority)
        ? rule.priority
        : base.priority,
    weight:
      typeof rule.weight === 'number' && Number.isFinite(rule.weight)
        ? rule.weight
        : base.weight,
    monitor: {
      ...rule.monitor,
      model: rule.monitor?.model ?? '',
    },
    codex_image_generation_bridge_policy:
      rule.codex_image_generation_bridge_policy
        ? normalizeCodexImageGenerationBridgePolicy(
            rule.codex_image_generation_bridge_policy
          )
        : undefined,
    model_strategy: normalizeModelStrategy(rule.model_strategy),
    fixed_models: normalizeModelList(rule.fixed_models ?? []),
  }
}

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
    admin_api_base_path:
      source?.admin_api_base_path ||
      defaultAdminAPIBasePath(source?.type ?? UPSTREAM_SOURCE_TYPE_SUB2API),
    relay_base_url: source?.relay_base_url ?? '',
    email: '',
    password: '',
    local_group: source?.local_group || 'default',
    local_group_rules: (source?.local_group_rules ?? []).map(
      normalizeRuleForForm
    ),
    allow_private_ip: source?.allow_private_ip ?? false,
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
    local_group_rules: normalizeSyncRules(values.local_group_rules),
    allow_private_ip: values.allow_private_ip,
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
    local_group_rules: normalizeSyncRules(values.local_group_rules),
    allow_private_ip: values.allow_private_ip,
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

function modelStrategyDisplayLabel(strategy: string) {
  return normalizeModelStrategy(strategy) ===
    UPSTREAM_SOURCE_MODEL_STRATEGY_FIXED
    ? 'Fixed models'
    : 'All upstream models'
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

// SourceSettingBadges summarizes per-source settings that still live on the
// source itself. Monitor/auto-sync/auto-priority/priority/weight moved to
// per-rule overrides (see UpstreamSourceLocalGroupRule), so this only shows
// how many sync rules are configured plus the still-source-level private IP
// policy.
function SourceSettingBadges(props: { source: UpstreamSource }) {
  const { t } = useTranslation()
  const ruleCount = (props.source.local_group_rules ?? []).length

  return (
    <div className='flex max-w-[260px] flex-wrap gap-1'>
      <StatusBadge
        label={t('{{count}} sync rules', { count: ruleCount })}
        variant={ruleCount > 0 ? 'success' : 'neutral'}
        copyable={false}
      />
      <StatusBadge
        label={
          props.source.allow_private_ip
            ? t('Private IP Allowed')
            : t('Private IP Blocked')
        }
        variant={props.source.allow_private_ip ? 'warning' : 'neutral'}
        copyable={false}
      />
    </div>
  )
}

function ruleStrategyOverrideLabel(
  key: LocalGroupRuleStrategyResolution['override_keys'][number]
) {
  switch (key) {
    case 'monitor':
      return 'Monitor'
    case 'auto_sync':
      return 'Auto Sync'
    case 'auto_priority':
      return 'Auto Priority'
    case 'codex_image_generation_bridge':
      return 'Codex image generation bridge'
    case 'model_strategy':
      return 'Model strategy'
  }
  const exhaustive: never = key
  return exhaustive
}

function RuleStrategySummary(props: {
  resolution: LocalGroupRuleStrategyResolution
}) {
  const { t } = useTranslation()
  const resolution = props.resolution
  const modelLabel =
    resolution.model.strategy === UPSTREAM_SOURCE_MODEL_STRATEGY_FIXED
      ? `${t('Fixed models')} / ${resolution.model.fixed_models.length}`
      : t('All upstream models')
  const overrideLabel = resolution.override_keys
    .map((key) => t(ruleStrategyOverrideLabel(key)))
    .join(', ')

  return (
    <div className='flex flex-col gap-2'>
      <div className='flex flex-wrap gap-1'>
        <StatusBadge
          label={
            resolution.has_overrides
              ? t('Custom strategy')
              : t('Inherit defaults')
          }
          variant={resolution.has_overrides ? 'warning' : 'neutral'}
          copyable={false}
        />
        {resolution.has_overrides && (
          <StatusBadge
            label={`${t('Overrides')}: ${overrideLabel}`}
            variant='warning'
            copyable={false}
          />
        )}
      </div>
      <div className='flex flex-wrap gap-1'>
        <StatusBadge
          label={`${t('Monitor')}: ${
            resolution.monitor.enabled ? t('Enabled') : t('Disabled')
          } / ${resolution.monitor.interval_minutes}m`}
          variant={resolution.monitor.enabled ? 'success' : 'neutral'}
          copyable={false}
        />
        <StatusBadge
          label={`${t('Auto Sync')}: ${
            resolution.auto_sync.enabled ? t('Enabled') : t('Disabled')
          } / ${resolution.auto_sync.interval_minutes}m`}
          variant={resolution.auto_sync.enabled ? 'success' : 'neutral'}
          copyable={false}
        />
        <StatusBadge
          label={`${t('Auto Priority')}: ${
            resolution.auto_priority.enabled ? t('Enabled') : t('Disabled')
          } / ${resolution.auto_priority.interval_minutes}m / ${
            resolution.auto_priority.window_hours
          }h`}
          variant={resolution.auto_priority.enabled ? 'success' : 'neutral'}
          copyable={false}
        />
        <StatusBadge label={modelLabel} variant='neutral' copyable={false} />
        <StatusBadge
          label={`${t('Codex image generation bridge')}: ${t(
            codexImageGenerationBridgePolicyLabel(
              resolution.codex_image_generation_bridge_policy.value
            )
          )}`}
          variant={
            resolution.codex_image_generation_bridge_policy.value === 'disabled'
              ? 'warning'
              : 'neutral'
          }
          copyable={false}
        />
      </div>
    </div>
  )
}

function useUpstreamSourceColumns(props: {
  onEdit: (source: UpstreamSource) => void
  onCredentials: (source: UpstreamSource) => void
  onImportSession: (source: UpstreamSource) => void
  onMappings: (source: UpstreamSource) => void
  onDiscover: (source: UpstreamSource) => void
  onSync: (source: UpstreamSource) => void
  onAutoPriority: (source: UpstreamSource) => void
  onDelete: (source: UpstreamSource) => void
  discoveringID?: number
  syncingID?: number
  autoPriorityID?: number
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
          <div className='flex min-w-0 flex-col gap-1'>
            <StatusWithTime
              status={row.original.last_sync_status}
              timestamp={row.original.last_sync_time}
              error={row.original.last_sync_error}
            />
            {row.original.turnstile_blocked && (
              <StatusBadge
                label={t('Blocked by Cloudflare — import a session')}
                variant='danger'
                copyable={false}
              />
            )}
          </div>
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
            onImportSession={props.onImportSession}
            onMappings={props.onMappings}
            onDiscover={props.onDiscover}
            onSync={props.onSync}
            onAutoPriority={props.onAutoPriority}
            onDelete={props.onDelete}
            discovering={props.discoveringID === row.original.id}
            syncing={props.syncingID === row.original.id}
            autoPrioritizing={props.autoPriorityID === row.original.id}
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
  onImportSession: (source: UpstreamSource) => void
  onMappings: (source: UpstreamSource) => void
  onDiscover: (source: UpstreamSource) => void
  onSync: (source: UpstreamSource) => void
  onAutoPriority: (source: UpstreamSource) => void
  onDelete: (source: UpstreamSource) => void
  discovering: boolean
  syncing: boolean
  autoPrioritizing: boolean
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
          <DropdownMenuItem onClick={() => props.onImportSession(source)}>
            {t('Import session')}
            <DropdownMenuShortcut>
              <Cookie size={16} />
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
          <DropdownMenuItem
            disabled={props.autoPrioritizing}
            onClick={() => props.onAutoPriority(source)}
          >
            {props.autoPrioritizing
              ? t('Adjusting priority...')
              : t('Adjust priority')}
            <DropdownMenuShortcut>
              {props.autoPrioritizing ? (
                <Loader2 size={16} className='animate-spin' />
              ) : (
                <TrendingUp size={16} />
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

function CheckboxList(props: {
  values: string[]
  options: { value: string; label: string }[]
  onChange: (values: string[]) => void
}) {
  const selected = new Set(props.values)
  const toggle = (value: string, checked: boolean) => {
    props.onChange(
      checked
        ? Array.from(new Set([...props.values, value]))
        : props.values.filter((item) => item !== value)
    )
  }
  return (
    <div className='border-border grid gap-2 rounded-md border p-2'>
      {props.options.map((option) => (
        <label
          key={option.value}
          className='flex min-h-8 items-center gap-2 text-sm'
        >
          <Checkbox
            checked={selected.has(option.value)}
            onCheckedChange={(checked) =>
              toggle(option.value, Boolean(checked))
            }
          />
          <span>{option.label}</span>
        </label>
      ))}
    </div>
  )
}

function FixedModelSelect(props: {
  values: string[]
  options: { value: string; label: string }[]
  onChange: (values: string[]) => void
}) {
  const { t } = useTranslation()
  return (
    <div className='grid gap-2'>
      <MultiSelect
        options={props.options}
        selected={props.values}
        onChange={props.onChange}
        placeholder={t('Select models or add custom ones')}
        allowCreate
        createLabel='Add custom model "{{value}}"'
        emptyText={t('No models found')}
        maxVisibleChips={8}
        copyChipOnClick
      />
    </div>
  )
}

function CodexImageGenerationBridgePolicySelect(props: {
  value: CodexImageGenerationBridgePolicy | 'inherit'
  includeInherit?: boolean
  triggerId?: string
  onChange: (value: CodexImageGenerationBridgePolicy | 'inherit') => void
}) {
  const { t } = useTranslation()
  return (
    <Select
      value={props.value}
      onValueChange={(value) => {
        if (value) {
          props.onChange(value as CodexImageGenerationBridgePolicy | 'inherit')
        }
      }}
    >
      <SelectTrigger id={props.triggerId}>
        <SelectValue>
          {props.value === 'inherit'
            ? t('Inherit upstream source')
            : t(codexImageGenerationBridgePolicyLabel(props.value))}
        </SelectValue>
      </SelectTrigger>
      <SelectContent alignItemWithTrigger={false}>
        <SelectGroup>
          {props.includeInherit && (
            <SelectItem value='inherit'>
              {t('Inherit upstream source')}
            </SelectItem>
          )}
          <SelectItem value='follow'>{t('Follow channel')}</SelectItem>
          <SelectItem value='enabled'>{t('Force enable')}</SelectItem>
          <SelectItem value='disabled'>{t('Force disable')}</SelectItem>
        </SelectGroup>
      </SelectContent>
    </Select>
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
  const [userRuleTemplates, setUserRuleTemplates] = useState<
    LocalGroupRuleUserTemplate[]
  >([])
  const isUpdate = props.mode === 'update'
  const groupsQuery = useQuery({
    queryKey: ['user-groups'],
    queryFn: getUserGroups,
    enabled: props.open,
  })
  const modelsQuery = useQuery({
    queryKey: ['user-models'],
    queryFn: getUserModels,
    enabled: props.open,
  })
  const groupOptions = useMemo(
    () =>
      Object.entries(groupsQuery.data?.data ?? {}).map(([value, info]) => ({
        value,
        label:
          info.desc && info.desc !== value ? `${value} (${info.desc})` : value,
      })),
    [groupsQuery.data]
  )
  const modelOptions = useMemo(
    () => modelsQuery.data?.data ?? [],
    [modelsQuery.data]
  )
  const modelSelectOptions = useMemo(
    () => modelOptions.map((model) => ({ value: model, label: model })),
    [modelOptions]
  )
  const channelTypeSelectOptions = useMemo(
    () =>
      CHANNEL_TYPE_OPTIONS.map((option) => ({
        value: String(option.value),
        label: t(option.label),
      })),
    [t]
  )
  const ruleStrategyDefaults = DEFAULT_LOCAL_GROUP_RULE_STRATEGY_DEFAULTS

  useEffect(() => {
    if (!props.open || typeof window === 'undefined') {
      return
    }
    setUserRuleTemplates(
      parseLocalGroupRuleUserTemplates(
        window.localStorage.getItem(USER_RULE_TEMPLATES_STORAGE_KEY)
      )
    )
  }, [props.open])

  const persistUserRuleTemplates = (
    templates: LocalGroupRuleUserTemplate[]
  ) => {
    setUserRuleTemplates(templates)
    if (typeof window !== 'undefined') {
      window.localStorage.setItem(
        USER_RULE_TEMPLATES_STORAGE_KEY,
        serializeLocalGroupRuleUserTemplates(templates)
      )
    }
  }

  const setField = <K extends keyof UpstreamSourceFormValues>(
    key: K,
    value: UpstreamSourceFormValues[K]
  ) => {
    setForm((previous) => ({ ...previous, [key]: value }))
  }

  const setSourceType = (value: UpstreamSourceType) => {
    setForm((previous) => {
      const previousDefaultPath = defaultAdminAPIBasePath(previous.type)
      const shouldReplacePath =
        previous.admin_api_base_path.trim() === '' ||
        previous.admin_api_base_path === previousDefaultPath
      return {
        ...previous,
        type: value,
        admin_api_base_path: shouldReplacePath
          ? defaultAdminAPIBasePath(value)
          : previous.admin_api_base_path,
      }
    })
  }

  const setLocalGroupRule = (
    index: number,
    value: UpstreamSourceLocalGroupRule
  ) => {
    setForm((previous) => ({
      ...previous,
      local_group_rules: previous.local_group_rules.map((rule, ruleIndex) =>
        ruleIndex === index ? value : rule
      ),
    }))
  }

  const addLocalGroupRule = () => {
    setForm((previous) => ({
      ...previous,
      local_group_rules: [...previous.local_group_rules, emptyLocalGroupRule()],
    }))
  }

  const inferProLocalGroup = (defaultLocalGroup: string) => {
    const normalizedDefault = defaultLocalGroup.trim().toLowerCase()
    const exactProGroup = groupOptions.find(
      (option) =>
        option.value.toLowerCase() === `${normalizedDefault}-pro` ||
        option.value.toLowerCase() === `${normalizedDefault}_pro`
    )
    if (exactProGroup) {
      return exactProGroup.value
    }
    return (
      groupOptions.find((option) => option.value.toLowerCase().includes('pro'))
        ?.value || defaultLocalGroup
    )
  }

  const addLocalGroupRuleTemplates = (keys: LocalGroupRuleTemplateKey[]) => {
    setForm((previous) => {
      const defaultLocalGroup = previous.local_group.trim() || 'default'
      const proLocalGroup = inferProLocalGroup(defaultLocalGroup)
      const defaults = {
        defaultLocalGroup,
        proLocalGroup,
        monitor: ruleStrategyDefaults.monitor,
        autoSync: ruleStrategyDefaults.autoSync,
        autoPriority: ruleStrategyDefaults.autoPriority,
        codexImageGenerationBridgePolicy:
          ruleStrategyDefaults.codexImageGenerationBridgePolicy,
        modelStrategy: ruleStrategyDefaults.modelStrategy,
        fixedModels: ruleStrategyDefaults.fixedModels,
      }

      return {
        ...previous,
        local_group_rules: [
          ...previous.local_group_rules,
          ...keys.map((key) =>
            normalizeRuleForForm(buildLocalGroupRuleTemplate(key, defaults))
          ),
        ],
      }
    })
  }

  const addUserLocalGroupRuleTemplate = (
    template: LocalGroupRuleUserTemplate
  ) => {
    setForm((previous) => ({
      ...previous,
      local_group_rules: [
        ...previous.local_group_rules,
        normalizeRuleForForm(template.rule),
      ],
    }))
  }

  const saveLocalGroupRuleAsTemplate = (
    index: number,
    rule: UpstreamSourceLocalGroupRule
  ) => {
    if (!hasLocalGroupRuleMatcher(rule)) {
      toast.error(t('Rule template needs at least one platform or keyword'))
      return
    }
    const fallbackName = t('Rule {{index}}', { index: index + 1 })
    const template = createLocalGroupRuleUserTemplate(
      rule.name.trim() || fallbackName,
      rule
    )
    const next = [
      template,
      ...userRuleTemplates.filter((item) => item.id !== template.id),
    ]
    persistUserRuleTemplates(next)
    toast.success(t('Rule template saved'))
  }

  const removeLocalGroupRule = (index: number) => {
    setForm((previous) => ({
      ...previous,
      local_group_rules: previous.local_group_rules.filter(
        (_, ruleIndex) => ruleIndex !== index
      ),
    }))
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
  const credentialLabel =
    form.type === UPSTREAM_SOURCE_TYPE_NEW_API
      ? t('Username or Email')
      : t('Email')

  return (
    <Sheet open={props.open} onOpenChange={props.onOpenChange}>
      <SheetContent className={sideDrawerContentClassName('sm:max-w-[760px]')}>
        <SheetHeader className={sideDrawerHeaderClassName()}>
          <SheetTitle>
            {isUpdate ? t('Edit Upstream Source') : t('Add Upstream Source')}
          </SheetTitle>
          <SheetDescription>
            {isUpdate ? props.source?.name : sourceTypeLabel(form.type)}
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
                    value && setSourceType(value as UpstreamSourceType)
                  }
                >
                  <SelectTrigger id='source-type'>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent alignItemWithTrigger={false}>
                    <SelectGroup>
                      {SOURCE_TYPE_OPTIONS.map((option) => (
                        <SelectItem key={option.value} value={option.value}>
                          {option.label}
                        </SelectItem>
                      ))}
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
            <FieldBlock label={t('Local Group')} htmlFor='source-local-group'>
              <Combobox
                id='source-local-group'
                options={groupOptions}
                value={form.local_group}
                onValueChange={(value) => value && setField('local_group', value)}
                placeholder={t('Select local group')}
                emptyText={t('No local group found')}
              />
            </FieldBlock>
            <SwitchRow
              label={t('Allow Private IP / Fake IP')}
              checked={form.allow_private_ip}
              onCheckedChange={(checked) =>
                setField('allow_private_ip', checked)
              }
            />
          </SideDrawerSection>

          {!isUpdate && (
            <SideDrawerSection>
              <SideDrawerSectionHeader title={t('Credentials')} />
              <FieldBlock label={credentialLabel} htmlFor='source-email'>
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
            <div className='flex items-center justify-between gap-3'>
              <SideDrawerSectionHeader title={t('Sync Rules')} />
              <div className='flex shrink-0 flex-wrap justify-end gap-2'>
                <DropdownMenu>
                  <DropdownMenuTrigger
                    render={
                      <Button type='button' variant='outline' size='sm' />
                    }
                  >
                    <Settings2 data-icon='inline-start' />
                    {t('Rule templates')}
                  </DropdownMenuTrigger>
                  <DropdownMenuContent align='end' className='w-60'>
                    <DropdownMenuLabel>
                      {t('Built-in templates')}
                    </DropdownMenuLabel>
                    {LOCAL_GROUP_RULE_TEMPLATE_SETS.map((templateSet) => (
                      <DropdownMenuItem
                        key={templateSet.label}
                        onClick={() =>
                          addLocalGroupRuleTemplates(templateSet.keys)
                        }
                      >
                        {t(templateSet.label)}
                      </DropdownMenuItem>
                    ))}
                    <DropdownMenuSeparator />
                    <DropdownMenuLabel>
                      {t('Saved templates')}
                    </DropdownMenuLabel>
                    {userRuleTemplates.length === 0 ? (
                      <DropdownMenuItem disabled>
                        {t('No saved templates')}
                      </DropdownMenuItem>
                    ) : (
                      userRuleTemplates.map((template) => (
                        <DropdownMenuItem
                          key={template.id}
                          onClick={() =>
                            addUserLocalGroupRuleTemplate(template)
                          }
                        >
                          <span className='truncate'>{template.name}</span>
                        </DropdownMenuItem>
                      ))
                    )}
                  </DropdownMenuContent>
                </DropdownMenu>
                <Button
                  type='button'
                  variant='outline'
                  size='sm'
                  onClick={addLocalGroupRule}
                >
                  <Plus data-icon='inline-start' />
                  {t('Add rule')}
                </Button>
              </div>
            </div>
            {form.local_group_rules.length === 0 && (
              <p className='text-muted-foreground text-sm'>
                {t('No sync rules')}
              </p>
            )}
            <div className='space-y-3'>
              {form.local_group_rules.map((rule, index) => (
                <div
                  key={index}
                  className='border-border grid gap-3 rounded-lg border p-3'
                >
                  <div className='flex items-center justify-between gap-3'>
                    <span className='text-sm font-medium'>
                      {rule.name.trim() ||
                        t('Rule {{index}}', {
                          index: index + 1,
                        })}
                    </span>
                    <div className='flex shrink-0 items-center gap-1'>
                      <Button
                        type='button'
                        variant='outline'
                        size='sm'
                        onClick={() =>
                          saveLocalGroupRuleAsTemplate(index, rule)
                        }
                      >
                        <Save data-icon='inline-start' />
                        {t('Save as template')}
                      </Button>
                      <Button
                        type='button'
                        variant='ghost'
                        size='icon'
                        onClick={() => removeLocalGroupRule(index)}
                      >
                        <Trash2 />
                        <span className='sr-only'>{t('Remove rule')}</span>
                      </Button>
                    </div>
                  </div>
                  <RuleStrategySummary
                    resolution={resolveLocalGroupRuleStrategy(
                      rule,
                      ruleStrategyDefaults
                    )}
                  />
                  <div className='grid gap-3 sm:grid-cols-2'>
                    <FieldBlock
                      label={t('Rule Name')}
                      htmlFor={`source-rule-name-${index}`}
                    >
                      <Input
                        id={`source-rule-name-${index}`}
                        value={rule.name}
                        onChange={(event) =>
                          setLocalGroupRule(index, {
                            ...rule,
                            name: event.target.value,
                          })
                        }
                      />
                    </FieldBlock>
                    <FieldBlock
                      label={t('Target Local Group')}
                      htmlFor={`source-rule-local-group-${index}`}
                    >
                      <Combobox
                        id={`source-rule-local-group-${index}`}
                        options={groupOptions}
                        value={rule.local_group}
                        onValueChange={(value) =>
                          setLocalGroupRule(index, {
                            ...rule,
                            local_group: value ?? '',
                          })
                        }
                        placeholder={t('Select local group')}
                        emptyText={t('No local group found')}
                      />
                    </FieldBlock>
                  </div>
                  <div className='grid gap-3 sm:grid-cols-3'>
                    <FieldBlock
                      label={t('Channel Type')}
                      htmlFor={`source-rule-channel-type-${index}`}
                    >
                      <Combobox
                        id={`source-rule-channel-type-${index}`}
                        options={channelTypeSelectOptions}
                        value={String(
                          rule.channel_type ?? DEFAULT_RULE_CHANNEL_TYPE
                        )}
                        onValueChange={(value) =>
                          setLocalGroupRule(index, {
                            ...rule,
                            channel_type: value
                              ? Number.parseInt(value, 10)
                              : DEFAULT_RULE_CHANNEL_TYPE,
                          })
                        }
                        placeholder={t('Channel Type')}
                        emptyText={t('No channel type found.')}
                      />
                    </FieldBlock>
                    <FieldBlock
                      label={t('Priority')}
                      htmlFor={`source-rule-priority-${index}`}
                    >
                      <Input
                        id={`source-rule-priority-${index}`}
                        type='number'
                        value={rule.priority ?? DEFAULT_RULE_PRIORITY}
                        onChange={(event) =>
                          setLocalGroupRule(index, {
                            ...rule,
                            priority: parseIntegerInput(
                              event.target.value,
                              DEFAULT_RULE_PRIORITY
                            ),
                          })
                        }
                      />
                    </FieldBlock>
                    <FieldBlock
                      label={t('Weight')}
                      htmlFor={`source-rule-weight-${index}`}
                    >
                      <Input
                        id={`source-rule-weight-${index}`}
                        type='number'
                        min={0}
                        value={rule.weight ?? DEFAULT_RULE_WEIGHT}
                        onChange={(event) =>
                          setLocalGroupRule(index, {
                            ...rule,
                            weight: Math.max(
                              0,
                              parseIntegerInput(
                                event.target.value,
                                DEFAULT_RULE_WEIGHT
                              )
                            ),
                          })
                        }
                      />
                    </FieldBlock>
                  </div>
                  <FieldBlock label={t('Platforms')}>
                    <CheckboxList
                      values={rule.platforms}
                      options={UPSTREAM_SOURCE_PLATFORM_OPTIONS.map(
                        (option) => ({
                          value: option.value,
                          label: t(option.label),
                        })
                      )}
                      onChange={(values) =>
                        setLocalGroupRule(index, {
                          ...rule,
                          platforms: values,
                        })
                      }
                    />
                  </FieldBlock>
                  <div className='grid gap-3 sm:grid-cols-2'>
                    <FieldBlock
                      label={t('Name Keywords')}
                      htmlFor={`source-rule-name-keywords-${index}`}
                    >
                      <Input
                        id={`source-rule-name-keywords-${index}`}
                        value={formatKeywordList(rule.name_contains)}
                        onChange={(event) =>
                          setLocalGroupRule(index, {
                            ...rule,
                            name_contains: normalizeKeywordList(
                              event.target.value
                            ),
                          })
                        }
                      />
                    </FieldBlock>
                    <FieldBlock
                      label={t('Description Keywords')}
                      htmlFor={`source-rule-description-keywords-${index}`}
                    >
                      <Input
                        id={`source-rule-description-keywords-${index}`}
                        value={formatKeywordList(rule.description_contains)}
                        onChange={(event) =>
                          setLocalGroupRule(index, {
                            ...rule,
                            description_contains: normalizeKeywordList(
                              event.target.value
                            ),
                          })
                        }
                      />
                    </FieldBlock>
                  </div>
                  {!hasLocalGroupRuleMatcher(rule) && (
                    <p className='text-muted-foreground text-xs leading-5'>
                      {t('Matches all groups')}
                    </p>
                  )}
                  <FieldBlock
                    label={t('Exclude Keywords')}
                    htmlFor={`source-rule-exclude-keywords-${index}`}
                  >
                    <Input
                      id={`source-rule-exclude-keywords-${index}`}
                      value={formatKeywordList(rule.exclude_keywords)}
                      onChange={(event) =>
                        setLocalGroupRule(index, {
                          ...rule,
                          exclude_keywords: normalizeKeywordList(
                            event.target.value
                          ),
                        })
                      }
                    />
                  </FieldBlock>
                  <Collapsible defaultOpen={false} className='grid gap-3'>
                    <div className='flex items-center justify-between gap-3'>
                      <div className='min-w-0'>
                        <p className='text-sm font-medium'>
                          {t('Strategy overrides')}
                        </p>
                      </div>
                      <CollapsibleTrigger
                        render={
                          <Button type='button' variant='outline' size='sm' />
                        }
                      >
                        <Settings2 data-icon='inline-start' />
                        {t('Advanced strategy overrides')}
                        <ChevronDown data-icon='inline-end' />
                      </CollapsibleTrigger>
                    </div>
                    <CollapsibleContent className='border-border bg-muted/20 grid gap-3 rounded-md border p-3'>
                      <div className='grid gap-3 lg:grid-cols-3'>
                        <div className='grid gap-3'>
                          <SwitchRow
                            label={t('Monitor')}
                            checked={
                              rule.monitor?.enabled ??
                              ruleStrategyDefaults.monitor.enabled
                            }
                            onCheckedChange={(checked) =>
                              setLocalGroupRule(index, {
                                ...rule,
                                monitor: {
                                  ...rule.monitor,
                                  enabled: checked,
                                  interval_minutes: monitorIntervalDisplayValue(
                                    rule.monitor?.interval_minutes,
                                    ruleStrategyDefaults.monitor
                                      .interval_minutes
                                  ),
                                },
                              })
                            }
                          />
                          <FieldBlock
                            label={t('Monitor Interval Minutes')}
                            htmlFor={`source-rule-monitor-interval-${index}`}
                          >
                            <Input
                              id={`source-rule-monitor-interval-${index}`}
                              type='number'
                              min={1}
                              value={monitorIntervalDisplayValue(
                                rule.monitor?.interval_minutes,
                                ruleStrategyDefaults.monitor.interval_minutes
                              )}
                              onChange={(event) =>
                                setLocalGroupRule(index, {
                                  ...rule,
                                  monitor: {
                                    ...rule.monitor,
                                    enabled:
                                      rule.monitor?.enabled ??
                                      ruleStrategyDefaults.monitor.enabled,
                                    interval_minutes: parseIntegerInput(
                                      event.target.value,
                                      ruleStrategyDefaults.monitor
                                        .interval_minutes
                                    ),
                                  },
                                })
                              }
                            />
                          </FieldBlock>
                          <FieldBlock
                            label={t('Monitor Model')}
                            htmlFor={`source-rule-monitor-model-${index}`}
                          >
                            <Combobox
                              id={`source-rule-monitor-model-${index}`}
                              options={modelSelectOptions}
                              value={rule.monitor?.model ?? ''}
                              onValueChange={(value) => {
                                const model = value ?? ''
                                setLocalGroupRule(index, {
                                  ...rule,
                                  monitor: {
                                    ...rule.monitor,
                                    // Choosing a monitor model implies the admin
                                    // wants this rule monitored; auto-enable so it
                                    // does not silently stay off. Clearing the model
                                    // leaves the existing enabled state untouched.
                                    enabled: model
                                      ? true
                                      : (rule.monitor?.enabled ??
                                        ruleStrategyDefaults.monitor.enabled),
                                    model,
                                  },
                                })
                              }}
                              placeholder={t('Select monitor model')}
                              emptyText={t('No models found')}
                              allowCustomValue
                            />
                          </FieldBlock>
                        </div>
                        <div className='grid gap-3'>
                          <SwitchRow
                            label={t('Auto Sync')}
                            checked={
                              rule.auto_sync?.enabled ??
                              ruleStrategyDefaults.autoSync.enabled
                            }
                            onCheckedChange={(checked) =>
                              setLocalGroupRule(index, {
                                ...rule,
                                auto_sync: {
                                  enabled: checked,
                                  interval_minutes:
                                    rule.auto_sync?.interval_minutes ??
                                    ruleStrategyDefaults.autoSync
                                      .interval_minutes,
                                },
                              })
                            }
                          />
                          <FieldBlock
                            label={t('Auto Sync Interval Minutes')}
                            htmlFor={`source-rule-auto-sync-interval-${index}`}
                          >
                            <Input
                              id={`source-rule-auto-sync-interval-${index}`}
                              type='number'
                              min={0}
                              value={
                                rule.auto_sync?.interval_minutes ??
                                ruleStrategyDefaults.autoSync.interval_minutes
                              }
                              onChange={(event) =>
                                setLocalGroupRule(index, {
                                  ...rule,
                                  auto_sync: {
                                    enabled:
                                      rule.auto_sync?.enabled ??
                                      ruleStrategyDefaults.autoSync.enabled,
                                    interval_minutes: parseIntegerInput(
                                      event.target.value,
                                      ruleStrategyDefaults.autoSync
                                        .interval_minutes
                                    ),
                                  },
                                })
                              }
                            />
                          </FieldBlock>
                        </div>
                        <div className='grid gap-3'>
                          <SwitchRow
                            label={t('Auto Priority')}
                            checked={
                              rule.auto_priority?.enabled ??
                              ruleStrategyDefaults.autoPriority.enabled
                            }
                            onCheckedChange={(checked) =>
                              setLocalGroupRule(index, {
                                ...rule,
                                auto_priority: {
                                  enabled: checked,
                                  interval_minutes: intervalDisplayValue(
                                    rule.auto_priority?.interval_minutes,
                                    ruleStrategyDefaults.autoPriority
                                      .interval_minutes
                                  ),
                                  window_hours:
                                    rule.auto_priority?.window_hours ??
                                    ruleStrategyDefaults.autoPriority
                                      .window_hours,
                                },
                              })
                            }
                          />
                          <div className='grid gap-3 sm:grid-cols-2 lg:grid-cols-1'>
                            <FieldBlock
                              label={t('Auto Priority Interval Minutes')}
                              htmlFor={`source-rule-auto-priority-interval-${index}`}
                            >
                              <Input
                                id={`source-rule-auto-priority-interval-${index}`}
                                type='number'
                                min={0}
                                value={intervalDisplayValue(
                                  rule.auto_priority?.interval_minutes,
                                  ruleStrategyDefaults.autoPriority
                                    .interval_minutes
                                )}
                                onChange={(event) =>
                                  setLocalGroupRule(index, {
                                    ...rule,
                                    auto_priority: {
                                      enabled:
                                        rule.auto_priority?.enabled ??
                                        ruleStrategyDefaults.autoPriority
                                          .enabled,
                                      interval_minutes: parseIntegerInput(
                                        event.target.value,
                                        ruleStrategyDefaults.autoPriority
                                          .interval_minutes
                                      ),
                                      window_hours:
                                        rule.auto_priority?.window_hours ??
                                        ruleStrategyDefaults.autoPriority
                                          .window_hours,
                                    },
                                  })
                                }
                              />
                            </FieldBlock>
                            <FieldBlock
                              label={t('Metrics Window Hours')}
                              htmlFor={`source-rule-auto-priority-window-${index}`}
                            >
                              <Input
                                id={`source-rule-auto-priority-window-${index}`}
                                type='number'
                                min={1}
                                max={168}
                                value={
                                  rule.auto_priority?.window_hours ??
                                  ruleStrategyDefaults.autoPriority
                                    .window_hours
                                }
                                onChange={(event) =>
                                  setLocalGroupRule(index, {
                                    ...rule,
                                    auto_priority: {
                                      enabled:
                                        rule.auto_priority?.enabled ??
                                        ruleStrategyDefaults.autoPriority
                                          .enabled,
                                      interval_minutes: intervalDisplayValue(
                                        rule.auto_priority?.interval_minutes,
                                        ruleStrategyDefaults.autoPriority
                                          .interval_minutes
                                      ),
                                      window_hours: parseIntegerInput(
                                        event.target.value,
                                        ruleStrategyDefaults.autoPriority
                                          .window_hours
                                      ),
                                    },
                                  })
                                }
                              />
                            </FieldBlock>
                          </div>
                        </div>
                      </div>
                      <FieldBlock
                        label={t('Codex image generation bridge')}
                        htmlFor={`source-rule-codex-image-generation-bridge-${index}`}
                      >
                        <CodexImageGenerationBridgePolicySelect
                          triggerId={`source-rule-codex-image-generation-bridge-${index}`}
                          includeInherit
                          value={
                            rule.codex_image_generation_bridge_policy ??
                            'inherit'
                          }
                          onChange={(value) => {
                            const nextRule = { ...rule }
                            if (value === 'inherit') {
                              delete nextRule.codex_image_generation_bridge_policy
                            } else {
                              nextRule.codex_image_generation_bridge_policy =
                                value
                            }
                            setLocalGroupRule(index, nextRule)
                          }}
                        />
                      </FieldBlock>
                      <FieldBlock
                        label={t('Model strategy')}
                        htmlFor={`source-rule-model-strategy-${index}`}
                      >
                        <Select
                          value={rule.model_strategy}
                          onValueChange={(value) =>
                            setLocalGroupRule(index, {
                              ...rule,
                              model_strategy: normalizeModelStrategy(value),
                            })
                          }
                        >
                          <SelectTrigger
                            id={`source-rule-model-strategy-${index}`}
                          >
                            <SelectValue>
                              {t(
                                modelStrategyDisplayLabel(rule.model_strategy)
                              )}
                            </SelectValue>
                          </SelectTrigger>
                          <SelectContent alignItemWithTrigger={false}>
                            <SelectGroup>
                              <SelectItem
                                value={UPSTREAM_SOURCE_MODEL_STRATEGY_ALL}
                              >
                                {t('All upstream models')}
                              </SelectItem>
                              <SelectItem
                                value={UPSTREAM_SOURCE_MODEL_STRATEGY_FIXED}
                              >
                                {t('Fixed models')}
                              </SelectItem>
                            </SelectGroup>
                          </SelectContent>
                        </Select>
                      </FieldBlock>
                      {rule.model_strategy ===
                        UPSTREAM_SOURCE_MODEL_STRATEGY_FIXED && (
                        <FieldBlock label={t('Fixed Models')}>
                          <FixedModelSelect
                            values={rule.fixed_models}
                            options={modelSelectOptions}
                            onChange={(values) =>
                              setLocalGroupRule(index, {
                                ...rule,
                                fixed_models: values,
                              })
                            }
                          />
                        </FieldBlock>
                      )}
                    </CollapsibleContent>
                  </Collapsible>
                </div>
              ))}
            </div>
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
  const credentialLabel =
    props.source?.type === UPSTREAM_SOURCE_TYPE_NEW_API
      ? t('Username or Email')
      : t('Email')

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
            <FieldBlock label={credentialLabel} htmlFor='credentials-email'>
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

function ImportSessionSheet(props: {
  open: boolean
  source?: UpstreamSource
  isSubmitting: boolean
  onOpenChange: (open: boolean) => void
  onSubmit: (values: UpstreamSourceSessionImportRequest) => void
}) {
  const { t } = useTranslation()
  const [sessionCookie, setSessionCookie] = useState('')
  const [accessToken, setAccessToken] = useState('')
  const [userID, setUserID] = useState('')
  const [refreshToken, setRefreshToken] = useState('')
  const [expiresAt, setExpiresAt] = useState('')
  const isNewAPI = props.source?.type === UPSTREAM_SOURCE_TYPE_NEW_API

  const handleSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const values: UpstreamSourceSessionImportRequest = {}
    if (sessionCookie.trim()) {
      values.session_cookie = sessionCookie.trim()
    }
    if (accessToken.trim()) {
      values.access_token = accessToken.trim()
    }
    if (isNewAPI) {
      const parsedUserID = Number.parseInt(userID, 10)
      if (userID.trim() && Number.isFinite(parsedUserID)) {
        values.user_id = parsedUserID
      }
    } else {
      if (refreshToken.trim()) {
        values.refresh_token = refreshToken.trim()
      }
      const parsedExpiresAt = Number.parseInt(expiresAt, 10)
      if (expiresAt.trim() && Number.isFinite(parsedExpiresAt)) {
        values.expires_at = parsedExpiresAt
      }
    }
    props.onSubmit(values)
  }

  return (
    <Sheet open={props.open} onOpenChange={props.onOpenChange}>
      <SheetContent className={sideDrawerContentClassName('sm:max-w-[520px]')}>
        <SheetHeader className={sideDrawerHeaderClassName()}>
          <SheetTitle>{t('Import session')}</SheetTitle>
          <SheetDescription>{props.source?.name}</SheetDescription>
        </SheetHeader>
        <form
          id='upstream-source-session-form'
          className={sideDrawerFormClassName()}
          onSubmit={handleSubmit}
        >
          <SideDrawerSection>
            {isNewAPI ? (
              <>
                <FieldBlock
                  label={t('Session cookie')}
                  htmlFor='session-cookie'
                  description={t(
                    'Copy the session cookie from the upstream browser session (DevTools → Application → Cookies), or paste the access token from the upstream user settings page.'
                  )}
                >
                  <Textarea
                    id='session-cookie'
                    value={sessionCookie}
                    autoComplete='off'
                    onChange={(event) => setSessionCookie(event.target.value)}
                  />
                </FieldBlock>
                <FieldBlock label={t('Access token')} htmlFor='session-access-token'>
                  <Input
                    id='session-access-token'
                    value={accessToken}
                    autoComplete='off'
                    onChange={(event) => setAccessToken(event.target.value)}
                  />
                </FieldBlock>
                <FieldBlock label={t('User ID')} htmlFor='session-user-id'>
                  <Input
                    id='session-user-id'
                    type='number'
                    value={userID}
                    onChange={(event) => setUserID(event.target.value)}
                  />
                </FieldBlock>
              </>
            ) : (
              <>
                <FieldBlock
                  label={t('Access token (JWT)')}
                  htmlFor='session-access-token-jwt'
                  description={t(
                    'Paste the access token (JWT) from the upstream browser session.'
                  )}
                >
                  <Input
                    id='session-access-token-jwt'
                    value={accessToken}
                    autoComplete='off'
                    onChange={(event) => setAccessToken(event.target.value)}
                  />
                </FieldBlock>
                <FieldBlock
                  label={t('Refresh token (optional)')}
                  htmlFor='session-refresh-token'
                  description={t(
                    'Paste the refresh token so the access token auto-renews when it expires.'
                  )}
                >
                  <Input
                    id='session-refresh-token'
                    value={refreshToken}
                    autoComplete='off'
                    onChange={(event) => setRefreshToken(event.target.value)}
                  />
                </FieldBlock>
                <FieldBlock
                  label={t('Expires At (unix seconds, 0 = never)')}
                  htmlFor='session-expires-at'
                  description={t(
                    'Leave blank to auto-read expiry from the JWT; tokens without an exp claim never expire.'
                  )}
                >
                  <Input
                    id='session-expires-at'
                    type='number'
                    value={expiresAt}
                    onChange={(event) => setExpiresAt(event.target.value)}
                  />
                </FieldBlock>
              </>
            )}
          </SideDrawerSection>
        </form>
        <SheetFooter className={sideDrawerFooterClassName()}>
          <SheetClose render={<Button variant='outline' />}>
            {t('Close')}
          </SheetClose>
          <Button
            form='upstream-source-session-form'
            type='submit'
            disabled={props.isSubmitting}
          >
            {props.isSubmitting && (
              <Loader2 data-icon='inline-start' className='animate-spin' />
            )}
            {t('Import session')}
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
    () => resolveSelectedMappingIDs(mappings, mappingSelectionOverrides),
    [mappingSelectionOverrides, mappings]
  )
  const selectedMappingIDSet = useMemo(
    () => new Set(selectedMappingIDs),
    [selectedMappingIDs]
  )
  const hasUnsavedSelectionChanges = useMemo(
    () => hasMappingSelectionChanges(mappings, mappingSelectionOverrides),
    [mappingSelectionOverrides, mappings]
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

  const handleSync = async () => {
    if (!props.source) {
      return
    }
    if (hasUnsavedSelectionChanges) {
      const result = await saveMutation
        .mutateAsync(selectedMappingIDs)
        .catch(() => undefined)
      if (!result?.success) {
        return
      }
    }
    props.onSync(props.source)
  }

  const selectedCount = selectedMappingIDs.length
  const isSavingSelection = saveMutation.isPending
  const isSyncButtonBusy = props.syncing || isSavingSelection

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
                  disabled={isSyncButtonBusy}
                  onClick={handleSync}
                >
                  {isSyncButtonBusy ? (
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
              {t('{{count}} sync gates enabled', { count: selectedCount })}
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
            disabled={isSavingSelection}
            onClick={() => saveMutation.mutate(selectedMappingIDs)}
          >
            {isSavingSelection ? (
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
  const matchLabel = mapping.sync_eligible
    ? mapping.matched_rule_name || t('Matched')
    : t(mapping.match_reason || 'Not matched')
  const modelStrategyLabel =
    mapping.resolved_model_strategy === UPSTREAM_SOURCE_MODEL_STRATEGY_FIXED
      ? t('Fixed models')
      : t('All upstream models')
  const autoPriorityLabel = mapping.resolved_auto_priority_enabled
    ? `${t('Auto priority')}: ${intervalDisplayValue(
        mapping.resolved_auto_priority_interval_minutes,
        DEFAULT_AUTO_PRIORITY_INTERVAL_MINUTES
      )}m / ${mapping.resolved_auto_priority_window_hours || DEFAULT_AUTO_PRIORITY_WINDOW_HOURS}h`
    : `${t('Auto priority')}: ${t('Disabled')}`
  const codexImageBridgePolicy = normalizeCodexImageGenerationBridgePolicy(
    mapping.resolved_codex_image_generation_bridge_policy
  )
  const rowMuted =
    mapping.discovery_status === 'invalid' ||
    mapping.discovery_status === 'stale' ||
    !mapping.has_upstream_key

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
          {mapping.upstream_group_description && (
            <LongText className='text-muted-foreground max-w-[220px] text-xs'>
              {mapping.upstream_group_description}
            </LongText>
          )}
          <span className='text-muted-foreground text-xs'>
            {mapping.upstream_group_id}
          </span>
          <StatusBadge
            label={matchLabel}
            variant={mapping.sync_eligible ? 'success' : 'neutral'}
            copyable={false}
          />
          <span className='text-muted-foreground text-xs'>
            {t('Local group')}: {mapping.resolved_local_group || '-'}
          </span>
          <span className='text-muted-foreground text-xs'>
            {t('Model strategy')}: {modelStrategyLabel}
          </span>
          <span className='text-muted-foreground text-xs'>
            {autoPriorityLabel}
          </span>
          <span className='text-muted-foreground text-xs'>
            {t('Codex image generation bridge')}:{' '}
            {t(codexImageGenerationBridgePolicyLabel(codexImageBridgePolicy))}
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
  const [importSessionSource, setImportSessionSource] =
    useState<UpstreamSource>()
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

  const importSessionMutation = useMutation({
    mutationFn: async (variables: {
      source: UpstreamSource
      values: UpstreamSourceSessionImportRequest
    }) => importUpstreamSourceSession(variables.source.id, variables.values),
    onSuccess: (result) => {
      if (!result.success) {
        toast.error(apiErrorMessage(result, t('Failed to import session')))
        return
      }
      toast.success(t('Session imported'))
      setImportSessionSource(undefined)
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

  const autoPriorityMutation = useMutation({
    mutationFn: async (source: UpstreamSource) =>
      runUpstreamSourceAutoPriority(source.id),
    onSuccess: (result, source) => {
      if (!result.success) {
        toast.error(apiErrorMessage(result, t('Priority adjustment failed')))
        return
      }
      const data: UpstreamSourceAutoPriorityResult | undefined = result.data
      toast.success(
        t(
          'Priority adjustment finished: {{updated}} updated, {{skipped}} skipped, {{failed}} failed',
          {
            updated: data?.updated ?? 0,
            skipped: data?.skipped ?? 0,
            failed: data?.failed ?? 0,
          }
        )
      )
      invalidateSources()
      queryClient.invalidateQueries({
        queryKey: upstreamSourcesQueryKeys.mappings(source.id),
      })
      queryClient.invalidateQueries({
        queryKey: channelsQueryKeys.lists(),
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
    onImportSession: setImportSessionSource,
    onMappings: setMappingSource,
    onDiscover: (source) => discoverMutation.mutate(source),
    onSync: (source) => syncMutation.mutate(source),
    onAutoPriority: (source) => autoPriorityMutation.mutate(source),
    onDelete: setDeleteSource,
    discoveringID: discoverMutation.isPending
      ? discoverMutation.variables?.id
      : undefined,
    syncingID: syncMutation.isPending ? syncMutation.variables?.id : undefined,
    autoPriorityID: autoPriorityMutation.isPending
      ? autoPriorityMutation.variables?.id
      : undefined,
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
                  options: SOURCE_TYPE_OPTIONS,
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
      <ImportSessionSheet
        key={
          importSessionSource
            ? `import-session-${importSessionSource.id}`
            : 'import-session-closed'
        }
        open={Boolean(importSessionSource)}
        source={importSessionSource}
        isSubmitting={importSessionMutation.isPending}
        onOpenChange={(open) => !open && setImportSessionSource(undefined)}
        onSubmit={(values) => {
          if (!importSessionSource) {
            return
          }
          importSessionMutation.mutate({
            source: importSessionSource,
            values,
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
