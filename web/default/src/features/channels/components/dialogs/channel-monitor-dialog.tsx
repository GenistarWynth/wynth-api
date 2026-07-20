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
import { useQuery, useQueryClient } from '@tanstack/react-query'
import {
  Activity,
  Bolt,
  CheckCircle2,
  Clock,
  Globe2,
  Hash,
  Loader2,
  RefreshCw,
  type LucideIcon,
} from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { StatusBadge } from '@/components/status-badge'
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
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { getLobeIcon } from '@/lib/lobe-icon'
import { cn } from '@/lib/utils'

import {
  getChannelMonitorDetail,
  updateChannelMonitorSettings,
} from '../../api'
import {
  formatRelativeTime,
  formatResponseTime,
  getChannelTypeIcon,
  getChannelTypeLabel,
  channelsQueryKeys,
  parseModelsString,
} from '../../lib'
import {
  buildChannelMonitorSettingsPayload,
  buildMonitorHistoryBars,
  monitorRefreshText,
  monitorStatusText,
  normalizeMonitorInterval,
  readChannelMonitorSettings,
  type ChannelMonitorSettingsDraft,
  type MonitorHistoryBar,
  type MonitorVisualStatus,
} from '../../lib/channel-monitor'
import type { Channel, ChannelMonitorRecord } from '../../types'

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

type MonitorHistoryViewMode = 'availability' | 'first-token'

const EMPTY_MONITOR_RECORDS: ChannelMonitorRecord[] = []

function monitorStatusPillClass(status: MonitorVisualStatus | undefined) {
  if (status === 'success') {
    return 'border-success/35 bg-success/10 text-success'
  }
  if (status === 'degraded') {
    return 'border-warning/35 bg-warning/10 text-warning'
  }
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
  if (typeof value !== 'number' || !Number.isFinite(value)) {
    return 'text-muted-foreground'
  }
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

function historyAriaLabel(bar: MonitorHistoryBar, t: TFn) {
  if (bar.status === 'empty') return t('No data')
  return `${monitorStatusText(bar.status, t)} · ${bar.model || t('No data')}`
}

function buildFirstTokenWaveform(bars: MonitorHistoryBar[]) {
  const baselineY = 34
  const markerOffsetY = 5
  const maxFirstToken = Math.max(
    1,
    ...bars.map((bar) =>
      bar.firstTokenLatencyMS > 0 ? bar.firstTokenLatencyMS : 0
    )
  )
  const divisor = Math.max(1, bars.length - 1)
  const points = bars.map((bar, index) => {
    const hasValue = bar.firstTokenLatencyMS > 0
    const x = (index / divisor) * 100
    const y = hasValue
      ? 36 - (bar.firstTokenLatencyMS / maxFirstToken) * 30
      : baselineY
    const isAnomaly = bar.tone === 'danger'
    const markerY = isAnomaly
      ? Math.max(4, hasValue ? y - markerOffsetY : 8)
      : y
    return { bar, isAnomaly, x, y, markerY }
  })
  const linePoints = points
    .map((point) => `${point.x.toFixed(2)},${point.y.toFixed(2)}`)
    .join(' ')
  const anomalyPoints = points.filter((point) => point.isAnomaly)

  return { points, linePoints, anomalyPoints, baselineY }
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

function MonitorHistoryDetails({ bar }: { bar: MonitorHistoryBar }) {
  const { t } = useTranslation()

  if (bar.status === 'empty') return <span>{t('No data')}</span>

  const checkedAt =
    bar.checkedAt > 0 ? formatRelativeTime(bar.checkedAt) : t('No data')
  const rows = [
    [t('Status'), monitorStatusText(bar.status, t)],
    [t('Time'), checkedAt],
    [t('Model'), bar.model || t('No data')],
    [t('First token latency'), metricText(bar.firstTokenLatencyMS, t)],
    [t('Endpoint latency'), metricText(bar.endpointLatencyMS, t)],
    [t('Conversation latency'), metricText(bar.latencyMS, t)],
    [
      `${t('Input tokens')} / ${t('Output tokens')}`,
      tokenText(bar.promptTokens, bar.completionTokens),
    ],
  ] as const

  return (
    <div className='grid min-w-56 gap-1.5 text-left text-xs'>
      {rows.map(([label, value]) => (
        <div key={label} className='grid grid-cols-[auto_1fr] gap-3'>
          <span className='text-muted-foreground'>{label}</span>
          <span className='text-right font-medium'>{value}</span>
        </div>
      ))}
      {bar.message && (
        <div className='border-border/70 bg-muted/40 mt-1 max-w-64 rounded-md border px-2 py-1.5 break-words whitespace-pre-wrap'>
          {bar.message}
        </div>
      )}
    </div>
  )
}

function MonitorHistory({
  bars,
  viewMode,
}: {
  bars: MonitorHistoryBar[]
  viewMode: MonitorHistoryViewMode
}) {
  const { t } = useTranslation()
  const waveform = buildFirstTokenWaveform(bars)

  return (
    <div className='flex flex-col gap-2'>
      <TooltipProvider delay={120}>
        {viewMode === 'availability' ? (
          <div className='border-border/50 bg-background/45 flex h-10 items-center gap-1 rounded-lg border px-2 py-2'>
            {bars.map((bar) => (
              <Tooltip key={bar.id}>
                <TooltipTrigger
                  render={
                    <button
                      type='button'
                      className={cn(
                        'h-full min-w-0 flex-1 rounded-full transition-opacity hover:opacity-80 focus-visible:ring-ring focus-visible:ring-2 focus-visible:outline-none',
                        monitorHistoryToneClass(bar.tone),
                        bar.tone === 'empty' && 'opacity-25'
                      )}
                      aria-label={historyAriaLabel(bar, t)}
                    />
                  }
                />
                <TooltipContent className='border-border bg-popover text-popover-foreground max-w-sm rounded-lg border px-3 py-2 shadow-md'>
                  <MonitorHistoryDetails bar={bar} />
                </TooltipContent>
              </Tooltip>
            ))}
          </div>
        ) : (
          <div className='border-border/50 bg-background/45 relative h-24 overflow-hidden rounded-lg border px-3 py-3'>
            <div className='absolute inset-3'>
              <svg
                className='pointer-events-none absolute inset-0 h-full w-full overflow-visible'
                viewBox='0 0 100 40'
                preserveAspectRatio='none'
                aria-hidden='true'
              >
                <line
                  x1='0'
                  x2='100'
                  y1={waveform.baselineY}
                  y2={waveform.baselineY}
                  className='stroke-border'
                  strokeWidth='0.8'
                  vectorEffect='non-scaling-stroke'
                />
                <polyline
                  points={waveform.linePoints}
                  fill='none'
                  className='stroke-chart-2'
                  strokeWidth='2.2'
                  strokeLinecap='round'
                  strokeLinejoin='round'
                  vectorEffect='non-scaling-stroke'
                />
              </svg>
              {waveform.anomalyPoints.map(({ bar, x, markerY }) => (
                <span
                  key={`${bar.id}-anomaly`}
                  aria-hidden='true'
                  className='ring-background bg-destructive pointer-events-none absolute size-1.5 -translate-x-1/2 -translate-y-1/2 rounded-full ring-1'
                  style={{
                    left: `${x}%`,
                    top: `${(markerY / 40) * 100}%`,
                  }}
                />
              ))}
              {waveform.points.map(({ bar, x }) => (
                <Tooltip key={bar.id}>
                  <TooltipTrigger
                    render={
                      <button
                        type='button'
                        className={cn(
                          'absolute h-full w-2 -translate-x-1/2 rounded-sm opacity-0 transition-opacity hover:opacity-100 focus-visible:ring-ring focus-visible:ring-2 focus-visible:outline-none',
                          'bg-chart-2/10'
                        )}
                        style={{
                          left: `${x}%`,
                          top: 0,
                        }}
                        aria-label={historyAriaLabel(bar, t)}
                      />
                    }
                  />
                  <TooltipContent className='border-border bg-popover text-popover-foreground max-w-sm rounded-lg border px-3 py-2 shadow-md'>
                    <MonitorHistoryDetails bar={bar} />
                  </TooltipContent>
                </Tooltip>
              ))}
            </div>
          </div>
        )}
      </TooltipProvider>
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
    [channel]
  )
  const [monitorEnabled, setMonitorEnabled] = useState(monitorDefaults.enabled)
  const [monitorIntervalInput, setMonitorIntervalInput] = useState(
    String(monitorDefaults.intervalMinutes)
  )
  const [monitorModel, setMonitorModel] = useState(monitorDefaults.monitorModel)
  const [savedMonitorSettings, setSavedMonitorSettings] =
    useState<ChannelMonitorSettingsDraft>(monitorDefaults)
  const [isSavingMonitorSettings, setIsSavingMonitorSettings] = useState(false)
  const [historyViewMode, setHistoryViewMode] =
    useState<MonitorHistoryViewMode>('availability')
  const query = useQuery({
    queryKey: ['channel-monitor-detail', channelId],
    queryFn: async () => getChannelMonitorDetail(channelId),
    enabled: open && channelId > 0,
  })

  useEffect(() => {
    if (!open) return
    setMonitorEnabled(monitorDefaults.enabled)
    setMonitorIntervalInput(String(monitorDefaults.intervalMinutes))
    setMonitorModel(monitorDefaults.monitorModel)
    setSavedMonitorSettings(monitorDefaults)
  }, [monitorDefaults, open])

  const detail = query.data?.data
  const detailLoadError = query.isError ? query.error.message : null
  const info = detail?.info ?? channel?.monitor_info ?? undefined
  const records = detail?.recent_records ?? EMPTY_MONITOR_RECORDS
  const latestRecord = records.at(-1) ?? null
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
  const monitorModelOptions = useMemo(() => {
    const models = parseModelsString(channel?.models ?? '')
    const selectedModel = monitorModel.trim()
    const allModels = new Set([
      ...models,
      ...(selectedModel ? [selectedModel] : []),
    ])
    return [...allModels].map((model) => ({
      value: model,
      label: model,
    }))
  }, [channel?.models, monitorModel])
  const channelType = channel?.type ?? 1
  const typeLabel = t(getChannelTypeLabel(channelType))
  const icon = getLobeIcon(`${getChannelTypeIcon(channelType)}.Color`, 28)
  const latestModel =
    info?.latest_model?.trim() ||
    latestRecord?.model?.trim() ||
    channel?.test_model?.trim() ||
    ''
  const latestTime =
    info?.latest_checked_at && info.latest_checked_at > 0
      ? formatRelativeTime(info.latest_checked_at)
      : t('No data')
  const monitorIntervalValue = Number(monitorIntervalInput)
  const isMonitorIntervalValid =
    !monitorEnabled ||
    (Number.isInteger(monitorIntervalValue) && monitorIntervalValue >= 1)
  const currentMonitorDraft: ChannelMonitorSettingsDraft = {
    enabled: monitorEnabled,
    intervalMinutes: normalizeMonitorInterval(
      monitorIntervalInput,
      savedMonitorSettings.intervalMinutes
    ),
    monitorModel: monitorModel.trim(),
  }
  const monitorSettingsDirty =
    currentMonitorDraft.enabled !== savedMonitorSettings.enabled ||
    currentMonitorDraft.intervalMinutes !==
      savedMonitorSettings.intervalMinutes ||
    currentMonitorDraft.monitorModel !== savedMonitorSettings.monitorModel

  const handleSaveMonitorSettings = async () => {
    if (!channel) return
    if (!isMonitorIntervalValid) {
      toast.error(t('Monitoring interval must be at least 1 minute'))
      return
    }
    setIsSavingMonitorSettings(true)
    try {
      const response = await updateChannelMonitorSettings(
        channel.id,
        buildChannelMonitorSettingsPayload(channel, currentMonitorDraft),
        'monitor'
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
                    {t('Monitor Model')}
                  </FieldLabel>
                  <Combobox
                    id={`channel-monitor-test-model-${channelId}`}
                    options={monitorModelOptions}
                    value={monitorModel}
                    placeholder={t('Select monitor model')}
                    searchPlaceholder={t('Search models...')}
                    emptyText={t('No models found')}
                    allowCustomValue
                    onValueChange={(value) => setMonitorModel(value ?? '')}
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

            {query.isError && (
              <Empty className='border-border/60 bg-background/35 rounded-xl border py-6'>
                <EmptyHeader>
                  <EmptyTitle>{t('Failed to load monitor data')}</EmptyTitle>
                  <EmptyDescription>{detailLoadError}</EmptyDescription>
                </EmptyHeader>
              </Empty>
            )}

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
                icon={Clock}
                label={t('Average first token latency')}
                value={metricText(info?.average_first_token_latency_ms, t)}
              />
              <SecondaryMetric
                icon={Hash}
                label={`${t('Input tokens')} / ${t('Output tokens')}`}
                value={tokenText(
                  info?.latest_prompt_tokens,
                  info?.latest_completion_tokens
                )}
              />
              <SecondaryMetric
                icon={CheckCircle2}
                label={t('Successful checks')}
                value={`${info?.seven_day_successes ?? 0} / ${info?.seven_day_checks ?? 0}`}
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
                  {monitorRefreshText(info, t, formatRelativeTime)}
                </div>
                <div>{latestTime}</div>
              </div>
            </div>

            <div className='flex flex-col gap-3'>
              <div className='flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between'>
                <div className='text-muted-foreground flex items-center justify-between gap-3 text-sm font-semibold sm:min-w-0 sm:flex-1 sm:justify-start'>
                  <span>{t('Recent {{value}} records', { value: 60 })}</span>
                  <span>{records.length}/60</span>
                </div>
                <ToggleGroup
                  value={[historyViewMode]}
                  onValueChange={(value) => {
                    const nextValue = value.find(
                      (item): item is MonitorHistoryViewMode =>
                        item === 'availability' || item === 'first-token'
                    )
                    if (nextValue) setHistoryViewMode(nextValue)
                  }}
                  aria-label={t('History view')}
                  variant='outline'
                  size='sm'
                  spacing={1}
                  className='w-full justify-end sm:w-auto'
                >
                  <ToggleGroupItem
                    value='availability'
                    className='min-w-0 flex-1 sm:flex-none'
                  >
                    <CheckCircle2 data-icon='inline-start' />
                    <span className='truncate'>{t('Availability status')}</span>
                  </ToggleGroupItem>
                  <ToggleGroupItem
                    value='first-token'
                    className='min-w-0 flex-1 sm:flex-none'
                  >
                    <Activity data-icon='inline-start' />
                    <span className='truncate'>
                      {t('First token waveform')}
                    </span>
                  </ToggleGroupItem>
                </ToggleGroup>
              </div>
              <MonitorHistory bars={historyBars} viewMode={historyViewMode} />
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
