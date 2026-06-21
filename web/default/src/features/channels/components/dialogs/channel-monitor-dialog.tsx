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
import { useEffect, useMemo, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import {
  Bolt,
  Clock,
  Globe2,
  Hash,
  Loader2,
  RefreshCw,
  type LucideIcon,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { getLobeIcon } from '@/lib/lobe-icon'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { Combobox } from '@/components/ui/combobox'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyTitle,
} from '@/components/ui/empty'
import {
  Field,
  FieldContent,
  FieldDescription,
  FieldGroup,
  FieldLabel,
} from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Separator } from '@/components/ui/separator'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'
import { StatusBadge } from '@/components/status-badge'
import { getChannelMonitorDetail, updateChannel } from '../../api'
import {
  formatRelativeTime,
  formatResponseTime,
  getChannelTypeIcon,
  getChannelTypeLabel,
  channelsQueryKeys,
  parseModelsString,
} from '../../lib'
import {
  buildMonitorHistoryBars,
  monitorStatusText,
  type MonitorHistoryBar,
  type MonitorVisualStatus,
} from '../../lib/channel-monitor'
import type { Channel, ChannelMonitorInfo } from '../../types'

interface ChannelMonitorDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  channel: Channel | null
}

type TFn = (key: string, options?: { value?: number | string }) => string

interface CompactMetricValue {
  value: string
  unit: string
  empty: boolean
}

interface ChannelMonitorSettingsDraft {
  enabled: boolean
  intervalMinutes: number
  testModel: string
}

const DEFAULT_MONITOR_INTERVAL_MINUTES = 10

function monitorStatusPillClass(status: MonitorVisualStatus | undefined) {
  if (status === 'success')
    return 'border-success/35 bg-success/10 text-success'
  if (status === 'degraded')
    return 'border-warning/35 bg-warning/10 text-warning'
  if (status === 'failed' || status === 'error') {
    return 'border-destructive/35 bg-destructive/10 text-destructive'
  }
  return 'border-border bg-muted/70 text-muted-foreground'
}

function monitorHistoryToneClass(tone: MonitorHistoryBar['tone']) {
  if (tone === 'success') return 'bg-success'
  if (tone === 'warning') return 'bg-warning'
  if (tone === 'danger') return 'bg-destructive'
  return 'bg-muted'
}

function availabilityToneClass(value: number | null | undefined) {
  if (typeof value !== 'number' || !Number.isFinite(value))
    return 'text-muted-foreground'
  if (value < 0.5) return 'text-destructive'
  if (value < 0.8) return 'text-warning'
  return 'text-success'
}

function formatAvailability(
  value: number | null | undefined,
  fallback: string
) {
  if (typeof value !== 'number' || !Number.isFinite(value)) return fallback
  return `${(value * 100).toFixed(2)}%`
}

function parseChannelSettings(settings: string | null | undefined) {
  if (!settings?.trim()) return {}
  try {
    const parsed = JSON.parse(settings)
    if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
      return parsed as Record<string, unknown>
    }
  } catch {
    return {}
  }
  return {}
}

function normalizeMonitorInterval(value: unknown, fallback: number) {
  const interval = Number(value)
  if (Number.isInteger(interval) && interval >= 1) return interval
  return fallback
}

function readChannelMonitorSettings(
  channel: Channel | null
): ChannelMonitorSettingsDraft {
  const settings = parseChannelSettings(channel?.settings)
  return {
    enabled: settings.channel_monitor_enabled === true,
    intervalMinutes: normalizeMonitorInterval(
      settings.channel_monitor_interval_minutes,
      DEFAULT_MONITOR_INTERVAL_MINUTES
    ),
    testModel: channel?.test_model?.trim() ?? '',
  }
}

function buildChannelMonitorSettingsPayload(
  channel: Channel,
  draft: ChannelMonitorSettingsDraft
): Pick<Channel, 'settings' | 'test_model'> {
  const settings = parseChannelSettings(channel.settings)
  settings.channel_monitor_enabled = draft.enabled
  if (draft.enabled) {
    settings.channel_monitor_interval_minutes = normalizeMonitorInterval(
      draft.intervalMinutes,
      DEFAULT_MONITOR_INTERVAL_MINUTES
    )
  } else {
    delete settings.channel_monitor_interval_minutes
  }

  return {
    settings: JSON.stringify(settings),
    test_model: draft.testModel.trim(),
  }
}

function metricText(value: number | undefined, t: TFn) {
  if (typeof value !== 'number' || value <= 0) return t('No data')
  return formatResponseTime(value, t)
}

function compactMetric(value: number | undefined, t: TFn): CompactMetricValue {
  if (typeof value !== 'number' || value <= 0) {
    return { value: t('No data'), unit: '', empty: true }
  }
  return { value: String(Math.round(value)), unit: 'ms', empty: false }
}

function tokenText(input?: number, output?: number) {
  const prompt = input && input > 0 ? String(input) : '-'
  const completion = output && output > 0 ? String(output) : '-'
  return `${prompt} / ${completion}`
}

function refreshText(info: ChannelMonitorInfo | undefined, t: TFn) {
  if (!info?.enabled) return t('Disabled')
  if (!info.latest_checked_at) return t('No data')
  if (info.seconds_until_next_check && info.seconds_until_next_check > 0) {
    const seconds = info.seconds_until_next_check
    const value =
      seconds >= 60
        ? `${Math.ceil(seconds / 60)} ${t('minutes')}`
        : `${seconds} ${t('seconds')}`
    return t('Refresh in {{value}}', { value })
  }
  return t('Due now')
}

function historyTitle(bar: MonitorHistoryBar, t: TFn) {
  if (bar.status === 'empty') return t('No data')
  const checkedAt =
    bar.checkedAt > 0 ? formatRelativeTime(bar.checkedAt) : t('No data')
  const status = monitorStatusText(bar.status, t)
  const conversation = metricText(bar.latencyMS, t)
  const firstToken = metricText(bar.firstTokenLatencyMS, t)
  const endpoint = metricText(bar.endpointLatencyMS, t)
  const tokens = tokenText(bar.promptTokens, bar.completionTokens)
  const parts = [
    checkedAt,
    status,
    `${t('Conversation latency')}: ${conversation}`,
    `${t('First token')}: ${firstToken}`,
    `${t('Endpoint ping')}: ${endpoint}`,
    `${t('Input tokens')} / ${t('Output tokens')}: ${tokens}`,
  ]
  if (bar.message) parts.push(bar.message)
  return parts.join(' · ')
}

function MetricTile({
  icon: Icon,
  label,
  value,
}: {
  icon: LucideIcon
  label: string
  value: CompactMetricValue
}) {
  return (
    <div className='border-border/70 bg-background/70 flex min-h-24 flex-col justify-between gap-3 rounded-xl border p-4 shadow-sm'>
      <div className='text-muted-foreground flex items-center gap-2 text-xs font-semibold tracking-normal'>
        <Icon className='size-3.5' />
        {label}
      </div>
      <div
        className={cn(
          'flex items-end gap-1 leading-none font-semibold tracking-normal',
          value.empty
            ? 'text-muted-foreground text-base'
            : 'text-foreground text-4xl'
        )}
      >
        <span>{value.value}</span>
        {value.unit && (
          <span className='text-muted-foreground pb-1 text-sm font-medium'>
            {value.unit}
          </span>
        )}
      </div>
    </div>
  )
}

function SecondaryMetric({
  icon: Icon,
  label,
  value,
}: {
  icon: LucideIcon
  label: string
  value: string
}) {
  return (
    <div className='border-border/60 bg-background/40 flex min-w-0 items-center gap-2 rounded-lg border px-3 py-2'>
      <Icon className='text-muted-foreground size-3.5 shrink-0' />
      <span className='text-muted-foreground min-w-0 truncate text-xs font-medium'>
        {label}
      </span>
      <span className='text-foreground ml-auto max-w-[55%] truncate text-right text-xs font-semibold'>
        {value}
      </span>
    </div>
  )
}

function MonitorHistory({ bars }: { bars: MonitorHistoryBar[] }) {
  const { t } = useTranslation()

  return (
    <div className='flex flex-col gap-2'>
      <div className='border-border/50 bg-background/45 flex h-10 items-center gap-1 rounded-lg border px-2 py-2'>
        {bars.map((bar) => (
          <div
            key={bar.id}
            className={cn(
              'h-full min-w-0 flex-1 rounded-full transition-opacity hover:opacity-80',
              monitorHistoryToneClass(bar.tone),
              bar.tone === 'empty' && 'opacity-25'
            )}
            title={historyTitle(bar, t)}
          />
        ))}
      </div>
      <div className='text-muted-foreground flex items-center justify-between text-[11px] font-semibold tracking-normal uppercase'>
        <span>{t('Past')}</span>
        <span>{t('Now')}</span>
      </div>
    </div>
  )
}

function ChannelMonitorSkeleton() {
  return (
    <div className='border-border bg-card text-card-foreground flex flex-col gap-5 rounded-2xl border p-5 shadow-2xl'>
      <div className='flex items-start justify-between gap-3'>
        <div className='flex items-start gap-3'>
          <Skeleton className='size-12 rounded-lg' />
          <div className='flex flex-col gap-2'>
            <Skeleton className='h-5 w-56' />
            <Skeleton className='h-5 w-32' />
          </div>
        </div>
        <Skeleton className='h-7 w-16 rounded-full' />
      </div>
      <div className='grid gap-3 sm:grid-cols-2'>
        <Skeleton className='h-24 rounded-lg' />
        <Skeleton className='h-24 rounded-lg' />
      </div>
      <Skeleton className='h-28 rounded-lg' />
    </div>
  )
}

export function ChannelMonitorDialog({
  open,
  onOpenChange,
  channel,
}: ChannelMonitorDialogProps) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const channelId = channel?.id ?? 0
  const monitorDefaults = useMemo(
    () => readChannelMonitorSettings(channel),
    [channel?.id, channel?.settings, channel?.test_model]
  )
  const [monitorEnabled, setMonitorEnabled] = useState(monitorDefaults.enabled)
  const [monitorIntervalInput, setMonitorIntervalInput] = useState(
    String(monitorDefaults.intervalMinutes)
  )
  const [monitorTestModel, setMonitorTestModel] = useState(
    monitorDefaults.testModel
  )
  const [savedMonitorSettings, setSavedMonitorSettings] =
    useState<ChannelMonitorSettingsDraft>(monitorDefaults)
  const [isSavingMonitorSettings, setIsSavingMonitorSettings] = useState(false)
  const query = useQuery({
    queryKey: ['channel-monitor-detail', channelId],
    queryFn: async () => getChannelMonitorDetail(channelId),
    enabled: open && channelId > 0,
  })

  useEffect(() => {
    if (!open) return
    setMonitorEnabled(monitorDefaults.enabled)
    setMonitorIntervalInput(String(monitorDefaults.intervalMinutes))
    setMonitorTestModel(monitorDefaults.testModel)
    setSavedMonitorSettings(monitorDefaults)
  }, [
    monitorDefaults.enabled,
    monitorDefaults.intervalMinutes,
    monitorDefaults.testModel,
    open,
  ])

  const detail = query.data?.data
  const info = detail?.info ?? channel?.monitor_info ?? undefined
  const records = detail?.recent_records ?? []
  const latestRecord = records.length > 0 ? records[records.length - 1] : null
  const latestStatus = info?.latest_status
  const availabilityValue = info?.seven_day_availability
  const availability = formatAvailability(
    info?.seven_day_availability,
    t('No data')
  )
  const historyBars = useMemo(
    () => buildMonitorHistoryBars(records, 60),
    [records]
  )
  const testModelOptions = useMemo(() => {
    const models = parseModelsString(channel?.models ?? '')
    const selectedModel = monitorTestModel.trim()
    const allModels = new Set([
      ...models,
      ...(selectedModel ? [selectedModel] : []),
    ])
    return Array.from(allModels).map((model) => ({
      value: model,
      label: model,
    }))
  }, [channel?.models, monitorTestModel])
  const channelType = channel?.type ?? 1
  const typeLabel = t(getChannelTypeLabel(channelType))
  const icon = getLobeIcon(`${getChannelTypeIcon(channelType)}.Color`, 28)
  const latestModel = latestRecord?.model?.trim() || channel?.test_model || ''
  const latestTime =
    info?.latest_checked_at && info.latest_checked_at > 0
      ? formatRelativeTime(info.latest_checked_at)
      : t('No data')
  const monitorIntervalValue = Number(monitorIntervalInput)
  const isMonitorIntervalValid =
    !monitorEnabled ||
    (Number.isInteger(monitorIntervalValue) && monitorIntervalValue >= 1)
  const currentMonitorDraft = {
    enabled: monitorEnabled,
    intervalMinutes: normalizeMonitorInterval(
      monitorIntervalInput,
      savedMonitorSettings.intervalMinutes
    ),
    testModel: monitorTestModel.trim(),
  }
  const monitorSettingsDirty =
    currentMonitorDraft.enabled !== savedMonitorSettings.enabled ||
    currentMonitorDraft.intervalMinutes !==
      savedMonitorSettings.intervalMinutes ||
    currentMonitorDraft.testModel !== savedMonitorSettings.testModel

  const handleSaveMonitorSettings = async () => {
    if (!channel) return
    if (!isMonitorIntervalValid) {
      toast.error(t('Monitoring interval must be at least 1 minute'))
      return
    }

    setIsSavingMonitorSettings(true)
    try {
      const response = await updateChannel(
        channel.id,
        buildChannelMonitorSettingsPayload(channel, currentMonitorDraft)
      )
      if (response.success) {
        setSavedMonitorSettings(currentMonitorDraft)
        toast.success(t('Monitor settings saved'))
        queryClient.invalidateQueries({ queryKey: channelsQueryKeys.lists() })
        queryClient.invalidateQueries({
          queryKey: ['channel-monitor-detail', channel.id],
        })
      } else {
        toast.error(response.message || t('Failed to save monitor settings'))
      }
    } catch (error: unknown) {
      toast.error(
        error instanceof Error
          ? error.message
          : t('Failed to save monitor settings')
      )
    } finally {
      setIsSavingMonitorSettings(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className='text-card-foreground max-h-[calc(100vh-2rem)] overflow-y-auto p-0 sm:max-w-[520px]'>
        <DialogHeader className='sr-only'>
          <DialogTitle>{t('Channel Monitor')}</DialogTitle>
          <DialogDescription>{channel?.name ?? t('No data')}</DialogDescription>
        </DialogHeader>

        {query.isLoading ? (
          <div className='p-1'>
            <ChannelMonitorSkeleton />
          </div>
        ) : query.isError ? (
          <div className='border-border bg-card text-card-foreground rounded-2xl border p-4 shadow-2xl'>
            <Empty className='border-border/60 rounded-lg border py-8'>
              <EmptyHeader>
                <EmptyTitle>{t('Failed to load monitor data')}</EmptyTitle>
                <EmptyDescription>{query.error.message}</EmptyDescription>
              </EmptyHeader>
            </Empty>
          </div>
        ) : (
          <div className='border-border bg-card flex flex-col gap-5 rounded-2xl border p-5 shadow-2xl'>
            <div className='flex items-start justify-between gap-3'>
              <div className='flex min-w-0 items-start gap-3'>
                <div className='border-border/70 bg-background flex size-12 shrink-0 items-center justify-center rounded-xl border'>
                  {icon}
                </div>
                <div className='min-w-0'>
                  <div className='text-foreground truncate text-2xl leading-tight font-semibold tracking-normal'>
                    {channel?.name ?? t('Channel')}
                  </div>
                  <div className='mt-2 flex flex-wrap items-center gap-2'>
                    <StatusBadge
                      label={typeLabel}
                      variant='success'
                      copyable={false}
                      className='border-border/60 bg-background/70 border px-2'
                    />
                    {latestModel && (
                      <span className='text-muted-foreground max-w-56 truncate text-sm'>
                        {latestModel}
                      </span>
                    )}
                  </div>
                </div>
              </div>
              <span
                className={cn(
                  'inline-flex h-7 shrink-0 items-center rounded-full border px-3 text-sm font-semibold tracking-normal',
                  monitorStatusPillClass(latestStatus)
                )}
              >
                {monitorStatusText(latestStatus, t)}
              </span>
            </div>

            <div className='border-border/60 bg-background/45 flex flex-col gap-3 rounded-xl border p-3'>
              <Field
                orientation='horizontal'
                className='items-start justify-between gap-3'
              >
                <FieldContent>
                  <FieldLabel htmlFor={`channel-monitor-enabled-${channelId}`}>
                    {t('Monitor Settings')}
                  </FieldLabel>
                  <FieldDescription>
                    {t(
                      'Probe this channel on its own schedule and record availability.'
                    )}
                  </FieldDescription>
                </FieldContent>
                <Switch
                  id={`channel-monitor-enabled-${channelId}`}
                  checked={monitorEnabled}
                  onCheckedChange={(checked) => {
                    setMonitorEnabled(checked)
                    if (
                      checked &&
                      !(
                        Number.isInteger(Number(monitorIntervalInput)) &&
                        Number(monitorIntervalInput) >= 1
                      )
                    ) {
                      setMonitorIntervalInput(
                        String(savedMonitorSettings.intervalMinutes)
                      )
                    }
                  }}
                />
              </Field>

              <FieldGroup className='gap-3 sm:grid sm:grid-cols-2'>
                <Field data-invalid={!isMonitorIntervalValid || undefined}>
                  <FieldLabel htmlFor={`channel-monitor-interval-${channelId}`}>
                    {t('Monitoring interval')}
                  </FieldLabel>
                  <Input
                    id={`channel-monitor-interval-${channelId}`}
                    type='number'
                    min={1}
                    step={1}
                    value={monitorIntervalInput}
                    disabled={!monitorEnabled}
                    aria-invalid={!isMonitorIntervalValid}
                    onChange={(event) =>
                      setMonitorIntervalInput(event.target.value)
                    }
                    onBlur={() => {
                      if (monitorEnabled && !isMonitorIntervalValid) {
                        setMonitorIntervalInput('1')
                      }
                    }}
                  />
                  <FieldDescription>
                    {t('Interval in minutes for automatic probes.')}
                  </FieldDescription>
                </Field>

                <Field>
                  <FieldLabel
                    htmlFor={`channel-monitor-test-model-${channelId}`}
                  >
                    {t('Test model')}
                  </FieldLabel>
                  <Combobox
                    id={`channel-monitor-test-model-${channelId}`}
                    options={testModelOptions}
                    value={monitorTestModel}
                    placeholder={t('Test model')}
                    searchPlaceholder={t('Search models...')}
                    emptyText={t('No models found')}
                    allowCustomValue
                    onValueChange={(value) => setMonitorTestModel(value ?? '')}
                  />
                </Field>
              </FieldGroup>

              <div className='flex justify-end'>
                <Button
                  type='button'
                  size='sm'
                  onClick={handleSaveMonitorSettings}
                  disabled={
                    !channel ||
                    !monitorSettingsDirty ||
                    !isMonitorIntervalValid ||
                    isSavingMonitorSettings
                  }
                >
                  {isSavingMonitorSettings && (
                    <Loader2
                      className='animate-spin'
                      data-icon='inline-start'
                    />
                  )}
                  {isSavingMonitorSettings
                    ? t('Saving...')
                    : t('Save Settings')}
                </Button>
              </div>
            </div>

            <div className='grid gap-3 sm:grid-cols-2'>
              <MetricTile
                icon={Bolt}
                label={t('Conversation latency')}
                value={compactMetric(info?.latest_latency_ms, t)}
              />
              <MetricTile
                icon={Globe2}
                label={t('Endpoint ping')}
                value={compactMetric(info?.latest_endpoint_latency_ms, t)}
              />
            </div>

            <div className='grid gap-2 sm:grid-cols-2'>
              <SecondaryMetric
                icon={Clock}
                label={t('First token latency')}
                value={metricText(info?.latest_first_token_latency_ms, t)}
              />
              <SecondaryMetric
                icon={Hash}
                label={`${t('Input tokens')} / ${t('Output tokens')}`}
                value={tokenText(
                  info?.latest_prompt_tokens,
                  info?.latest_completion_tokens
                )}
              />
            </div>

            <Separator />

            <div className='flex items-end justify-between gap-4'>
              <div className='min-w-0'>
                <div className='text-muted-foreground text-sm'>
                  {t('Availability')} · {t('7-day')}
                </div>
                <div
                  className={cn(
                    'mt-1 text-5xl leading-none font-semibold tracking-normal',
                    availabilityToneClass(availabilityValue)
                  )}
                >
                  {availability}
                </div>
              </div>
              <div className='text-muted-foreground flex shrink-0 flex-col items-end gap-1 text-xs'>
                <div className='flex items-center gap-1.5'>
                  <RefreshCw className='size-3.5' />
                  {refreshText(info, t)}
                </div>
                <div>{latestTime}</div>
              </div>
            </div>

            <div className='flex flex-col gap-3'>
              <div className='text-muted-foreground flex items-center justify-between gap-3 text-sm font-semibold'>
                <span>{t('Recent {{value}} records', { value: 60 })}</span>
                <span>{records.length}/60</span>
              </div>
              <MonitorHistory bars={historyBars} />
            </div>

            {info?.latest_message && (
              <div className='border-destructive/25 bg-destructive/10 text-destructive max-h-20 overflow-y-auto rounded-lg border p-3 text-xs break-words whitespace-pre-wrap'>
                {info.latest_message}
              </div>
            )}
          </div>
        )}
      </DialogContent>
    </Dialog>
  )
}
