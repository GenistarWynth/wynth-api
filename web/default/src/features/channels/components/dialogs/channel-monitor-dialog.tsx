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
import { useQuery } from '@tanstack/react-query'
import {
  Activity,
  Clock,
  Cpu,
  Gauge,
  Hash,
  RefreshCw,
  type LucideIcon,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from '@/components/ui/empty'
import { Separator } from '@/components/ui/separator'
import { Skeleton } from '@/components/ui/skeleton'
import { StatusBadge, dotColorMap, type StatusVariant } from '@/components/status-badge'
import { cn } from '@/lib/utils'
import { getChannelMonitorDetail } from '../../api'
import { formatRelativeTime, formatResponseTime } from '../../lib'
import type { Channel, ChannelMonitorRecord } from '../../types'

interface ChannelMonitorDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  channel: Channel | null
}

type MonitorStatus = ChannelMonitorRecord['status']

function monitorStatusVariant(status: MonitorStatus | undefined): StatusVariant {
  if (status === 'success') return 'success'
  if (status === 'degraded') return 'warning'
  if (status === 'failed' || status === 'error') return 'danger'
  return 'neutral'
}

function formatAvailability(value: number | null | undefined, fallback: string) {
  if (typeof value !== 'number' || !Number.isFinite(value)) return fallback
  return `${(value * 100).toFixed(2)}%`
}

function metricText(value: number | undefined, t: (key: string, options?: { value?: number | string }) => string) {
  if (typeof value !== 'number' || value <= 0) return t('No data')
  return formatResponseTime(value, t)
}

function statusLabel(status: MonitorStatus | undefined, t: (key: string) => string) {
  if (status === 'success') return t('Normal')
  if (status === 'degraded') return t('Degraded')
  if (status === 'failed') return t('Failed')
  if (status === 'error') return t('Error')
  return t('No data')
}

function recordTitle(record: ChannelMonitorRecord, t: (key: string, options?: { value?: number | string }) => string) {
  const time = record.checked_at > 0 ? formatRelativeTime(record.checked_at) : t('No data')
  const latency = formatResponseTime(record.latency_ms, t)
  const firstToken = metricText(record.first_token_latency_ms, t)
  return `${time} · ${statusLabel(record.status, t)} · ${latency} · ${t('First token')} ${firstToken}`
}

function MonitorTimeline({
  records,
}: {
  records: ChannelMonitorRecord[]
}) {
  const { t } = useTranslation()
  if (records.length === 0) {
    return (
      <Empty className='border-border/60 rounded-lg border py-8'>
        <EmptyHeader>
          <EmptyTitle>{t('No monitor data')}</EmptyTitle>
          <EmptyDescription>{t('Monitor data will appear after the next probe')}</EmptyDescription>
        </EmptyHeader>
      </Empty>
    )
  }

  return (
    <div className='flex flex-col gap-2'>
      <div className='grid grid-cols-[repeat(auto-fit,minmax(5px,1fr))] gap-1'>
        {records.map((record) => (
          <div
            key={record.id}
            className={cn(
              'h-10 min-w-1 rounded-sm',
              dotColorMap[monitorStatusVariant(record.status)]
            )}
            title={recordTitle(record, t)}
          />
        ))}
      </div>
      <div className='text-muted-foreground flex items-center justify-between text-[11px] font-medium'>
        <span>{t('Past')}</span>
        <span>{t('Now')}</span>
      </div>
    </div>
  )
}

function MetricTile({
  icon: Icon,
  label,
  value,
}: {
  icon: LucideIcon
  label: string
  value: string
}) {
  return (
    <div className='bg-muted/40 flex min-h-20 flex-col justify-between gap-2 rounded-lg p-3'>
      <div className='text-muted-foreground flex items-center gap-1.5 text-xs font-medium'>
        <Icon className='size-3.5' />
        {label}
      </div>
      <div className='text-xl font-semibold tracking-normal'>{value}</div>
    </div>
  )
}

function ChannelMonitorSkeleton() {
  return (
    <div className='flex flex-col gap-4'>
      <div className='flex items-start justify-between gap-3'>
        <div className='flex flex-col gap-2'>
          <Skeleton className='h-5 w-48' />
          <Skeleton className='h-4 w-32' />
        </div>
        <Skeleton className='h-6 w-14 rounded-full' />
      </div>
      <div className='grid gap-3 sm:grid-cols-2'>
        <Skeleton className='h-20 rounded-lg' />
        <Skeleton className='h-20 rounded-lg' />
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
  const channelId = channel?.id ?? 0
  const query = useQuery({
    queryKey: ['channel-monitor-detail', channelId],
    queryFn: async () => getChannelMonitorDetail(channelId),
    enabled: open && channelId > 0,
  })

  const detail = query.data?.data
  const info = detail?.info ?? channel?.monitor_info
  const records = detail?.recent_records ?? []
  const latestStatus = info?.latest_status
  const availability = formatAvailability(info?.seven_day_availability, t('No data'))
  const latestTime =
    info?.latest_checked_at && info.latest_checked_at > 0
      ? formatRelativeTime(info.latest_checked_at)
      : t('No data')

  const modelNames = useMemo(() => {
    const models = records
      .map((record) => record.model.trim())
      .filter(Boolean)
    return [...new Set(models)].slice(-3)
  }, [records])

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className='max-h-[calc(100vh-2rem)] overflow-y-auto sm:max-w-3xl'>
        <DialogHeader>
          <DialogTitle>{t('Channel Monitor')}</DialogTitle>
          <DialogDescription>{channel?.name ?? t('No data')}</DialogDescription>
        </DialogHeader>

        {query.isLoading ? (
          <ChannelMonitorSkeleton />
        ) : query.isError ? (
          <Empty className='border-border/60 rounded-lg border py-8'>
            <EmptyHeader>
              <EmptyTitle>{t('Failed to load monitor data')}</EmptyTitle>
              <EmptyDescription>{query.error.message}</EmptyDescription>
            </EmptyHeader>
          </Empty>
        ) : (
          <div className='flex flex-col gap-4'>
            <div className='flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between'>
              <div className='flex min-w-0 flex-col gap-2'>
                <div className='flex items-center gap-2'>
                  <Activity className='text-muted-foreground size-5 shrink-0' />
                  <div className='min-w-0 truncate text-base font-semibold'>
                    {channel?.name ?? t('Channel')}
                  </div>
                </div>
                <div className='flex flex-wrap items-center gap-1.5'>
                  <StatusBadge
                    label={info?.enabled ? t('Enabled') : t('Disabled')}
                    variant={info?.enabled ? 'success' : 'neutral'}
                    copyable={false}
                  />
                  <StatusBadge
                    label={statusLabel(latestStatus, t)}
                    variant={monitorStatusVariant(latestStatus)}
                    copyable={false}
                  />
                  {modelNames.map((model) => (
                    <StatusBadge
                      key={model}
                      label={model}
                      variant='info'
                      copyable={false}
                    />
                  ))}
                </div>
              </div>
              <div className='text-muted-foreground flex items-center gap-1.5 text-xs'>
                <RefreshCw className='size-3.5' />
                {latestTime}
              </div>
            </div>

            <div className='grid gap-3 sm:grid-cols-2'>
              <MetricTile
                icon={Gauge}
                label={t('Conversation latency')}
                value={metricText(info?.latest_latency_ms, t)}
              />
              <MetricTile
                icon={Clock}
                label={t('First token latency')}
                value={metricText(info?.latest_first_token_latency_ms, t)}
              />
              <MetricTile
                icon={Cpu}
                label={t('Endpoint ping')}
                value={metricText(info?.latest_endpoint_latency_ms, t)}
              />
              <MetricTile
                icon={Hash}
                label={t('Tokens')}
                value={`${info?.latest_prompt_tokens ?? 0} / ${
                  info?.latest_completion_tokens ?? 0
                }`}
              />
            </div>

            <Separator />

            <div className='flex flex-col gap-3'>
              <div className='flex flex-col gap-1 sm:flex-row sm:items-end sm:justify-between'>
                <div>
                  <div className='text-muted-foreground text-xs font-medium'>
                    {t('Availability')} · {t('7-day')}
                  </div>
                  <div className='text-4xl font-semibold tracking-normal'>
                    {availability}
                  </div>
                </div>
                <div className='text-muted-foreground text-xs'>
                  {t('Recent {{value}} records', { value: records.length })}
                </div>
              </div>
              <MonitorTimeline records={records} />
            </div>

            {info?.latest_message && (
              <div className='bg-muted/40 text-destructive rounded-lg p-3 text-xs'>
                {info.latest_message}
              </div>
            )}
          </div>
        )}
      </DialogContent>
    </Dialog>
  )
}
