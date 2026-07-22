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
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Loader2, Trash2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { LongText } from '@/components/long-text'
import { StatusBadge, type StatusVariant } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { ScrollArea } from '@/components/ui/scroll-area'
import { Separator } from '@/components/ui/separator'
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { Skeleton } from '@/components/ui/skeleton'
import { formatNumber, formatTimestamp } from '@/lib/format'

import {
  clearUpstreamSourceSession,
  getUpstreamSourceMonitoringOverview,
  updateUpstreamSourceMonitor,
  upstreamSourcesQueryKeys,
} from './api'
import type { UpstreamSource, UpstreamSourceGroupChange } from './types'

type MonitoringSheetProps = {
  open: boolean
  source?: UpstreamSource
  onOpenChange: (open: boolean) => void
  onSourceUpdated: (source: UpstreamSource) => void
}

function timestamp(value: number) {
  return value > 0 ? formatTimestamp(value) : '-'
}

function authVariant(status: string): StatusVariant {
  switch (status) {
    case 'healthy':
      return 'success'
    case 'expiring':
      return 'warning'
    case 'expired':
    case 'invalid':
      return 'danger'
    default:
      return 'neutral'
  }
}

function scanVariant(status: string): StatusVariant {
  switch (status) {
    case 'success':
      return 'success'
    case 'partial':
      return 'warning'
    case 'failed':
      return 'danger'
    default:
      return 'neutral'
  }
}

function changeLabel(
  change: UpstreamSourceGroupChange,
  t: (key: string) => string
) {
  switch (change.change_type) {
    case 'added':
      return t('Group added')
    case 'removed':
      return t('Group removed')
    case 'restored':
      return t('Group restored')
    case 'rate_changed':
      return t('Rate changed')
  }
}

function MonitoringList(props: {
  title: string
  emptyLabel: string
  children: React.ReactNode
  empty: boolean
}) {
  return (
    <Card size='sm'>
      <CardHeader>
        <CardTitle>{props.title}</CardTitle>
      </CardHeader>
      <CardContent>
        {props.empty ? (
          <p className='text-muted-foreground text-sm'>{props.emptyLabel}</p>
        ) : (
          <div className='flex flex-col gap-3'>{props.children}</div>
        )}
      </CardContent>
    </Card>
  )
}

export function MonitoringSheet(props: MonitoringSheetProps) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const sourceID = props.source?.id ?? 0
  const overviewQuery = useQuery({
    queryKey: upstreamSourcesQueryKeys.monitoring(sourceID),
    queryFn: async () => {
      const result = await getUpstreamSourceMonitoringOverview(sourceID)
      if (!result.success) {
        throw new Error(result.message || t('Failed to load monitoring data'))
      }
      return result.data
    },
    enabled: props.open && sourceID > 0,
  })

  const monitorMutation = useMutation({
    mutationFn: async (enabled: boolean) =>
      updateUpstreamSourceMonitor(
        sourceID,
        enabled,
        props.source?.monitor_interval_minutes || 60
      ),
    onSuccess: (result) => {
      if (!result.success || !result.data) {
        toast.error(result.message || t('Failed to update monitor'))
        return
      }
      toast.success(
        result.data.monitor_enabled
          ? t('Monitor enabled')
          : t('Monitor disabled')
      )
      props.onSourceUpdated(result.data)
      queryClient.invalidateQueries({
        queryKey: upstreamSourcesQueryKeys.monitoring(sourceID),
      })
    },
  })

  const clearSessionMutation = useMutation({
    mutationFn: () => clearUpstreamSourceSession(sourceID),
    onSuccess: (result) => {
      if (!result.success || !result.data) {
        toast.error(result.message || t('Failed to clear session'))
        return
      }
      toast.success(t('Session cleared'))
      props.onSourceUpdated(result.data)
    },
  })

  const overview = overviewQuery.data
  const source = props.source

  return (
    <Sheet open={props.open} onOpenChange={props.onOpenChange}>
      <SheetContent className='sm:max-w-2xl'>
        <SheetHeader>
          <SheetTitle>{t('Source monitoring')}</SheetTitle>
          <SheetDescription>
            {source
              ? t('Health and recent monitor activity for {{name}}.', {
                  name: source.name,
                })
              : t('Health and recent monitor activity.')}
          </SheetDescription>
        </SheetHeader>
        <ScrollArea className='min-h-0 flex-1 px-4'>
          {(!source || overviewQuery.isLoading) && (
            <div className='flex flex-col gap-3 py-2'>
              <Skeleton className='h-32 w-full' />
              <Skeleton className='h-48 w-full' />
              <Skeleton className='h-48 w-full' />
            </div>
          )}
          {source && !overviewQuery.isLoading && overviewQuery.error && (
            <p className='text-destructive py-4 text-sm'>
              {overviewQuery.error.message}
            </p>
          )}
          {source && !overviewQuery.isLoading && !overviewQuery.error && (
            <div className='flex flex-col gap-4 py-2'>
              <div className='grid gap-4 md:grid-cols-2'>
                <Card size='sm'>
                  <CardHeader>
                    <CardTitle>{t('Auth health')}</CardTitle>
                    <CardDescription>
                      {t('Last validated')}:{' '}
                      {timestamp(source.auth_last_validated_at)}
                    </CardDescription>
                  </CardHeader>
                  <CardContent className='flex flex-col gap-2'>
                    <StatusBadge
                      label={source.auth_status || t('Unknown')}
                      variant={authVariant(source.auth_status)}
                      copyable={false}
                    />
                    {source.last_auth_error && (
                      <LongText className='text-destructive text-xs'>
                        {source.last_auth_error}
                      </LongText>
                    )}
                  </CardContent>
                </Card>
                <Card size='sm'>
                  <CardHeader>
                    <CardTitle>{t('Monitor schedule')}</CardTitle>
                    <CardDescription>
                      {t('Last monitor')}: {timestamp(source.last_monitor_time)}
                    </CardDescription>
                  </CardHeader>
                  <CardContent className='flex flex-wrap gap-2'>
                    <StatusBadge
                      label={
                        source.monitor_enabled ? t('Enabled') : t('Disabled')
                      }
                      variant={source.monitor_enabled ? 'success' : 'neutral'}
                      copyable={false}
                    />
                    <StatusBadge
                      label={t('{{count}} minute interval', {
                        count: source.monitor_interval_minutes || 60,
                      })}
                      variant='neutral'
                      copyable={false}
                    />
                  </CardContent>
                </Card>
              </div>

              <div className='grid gap-4 md:grid-cols-2'>
                <Card size='sm'>
                  <CardHeader>
                    <CardTitle>{t('Latest balance')}</CardTitle>
                    <CardDescription>
                      {overview?.balance
                        ? timestamp(overview.balance.collected_at)
                        : t('No balance snapshot')}
                    </CardDescription>
                  </CardHeader>
                  <CardContent className='text-lg font-medium'>
                    {overview?.balance
                      ? `${formatNumber(overview.balance.available)} ${overview.balance.currency}`
                      : '-'}
                  </CardContent>
                </Card>
                <Card size='sm'>
                  <CardHeader>
                    <CardTitle>{t('Subscription summary')}</CardTitle>
                    <CardDescription>
                      {t('{{count}} usage windows', {
                        count: overview?.subscription_usage.length ?? 0,
                      })}
                    </CardDescription>
                  </CardHeader>
                  <CardContent className='flex flex-col gap-2'>
                    {(overview?.subscription_usage ?? [])
                      .slice(0, 3)
                      .map((usage) => (
                        <div
                          key={usage.id}
                          className='flex items-center justify-between gap-3 text-sm'
                        >
                          <LongText className='min-w-0'>
                            {usage.name || usage.subscription_key} ·{' '}
                            {usage.window}
                          </LongText>
                          <span className='text-muted-foreground shrink-0'>
                            {usage.remaining_percent == null
                              ? '-'
                              : `${formatNumber(usage.remaining_percent)}%`}
                          </span>
                        </div>
                      ))}
                    {!overview?.subscription_usage.length && (
                      <span className='text-muted-foreground text-sm'>
                        {t('No subscription snapshot')}
                      </span>
                    )}
                  </CardContent>
                </Card>
              </div>

              <Separator />

              <MonitoringList
                title={t('Recent monitor runs')}
                emptyLabel={t('No recent monitor runs')}
                empty={!overview?.scans.length}
              >
                {(overview?.scans ?? []).slice(0, 5).map((scan) => (
                  <div
                    key={scan.id}
                    className='flex items-start justify-between gap-3'
                  >
                    <div className='min-w-0'>
                      <p className='text-sm'>{timestamp(scan.started_at)}</p>
                      {scan.error_summary && (
                        <LongText className='text-destructive text-xs'>
                          {scan.error_summary}
                        </LongText>
                      )}
                    </div>
                    <StatusBadge
                      label={scan.status}
                      variant={scanVariant(scan.status)}
                      copyable={false}
                    />
                  </div>
                ))}
              </MonitoringList>

              <MonitoringList
                title={t('Recent changes')}
                emptyLabel={t('No recent changes')}
                empty={!overview?.changes.length}
              >
                {(overview?.changes ?? []).slice(0, 5).map((change) => (
                  <div
                    key={change.id}
                    className='flex items-start justify-between gap-3 text-sm'
                  >
                    <div className='min-w-0'>
                      <p>{changeLabel(change, t)}</p>
                      <LongText className='text-muted-foreground text-xs'>
                        {change.upstream_group_name || change.upstream_group_id}
                      </LongText>
                    </div>
                    <span className='text-muted-foreground shrink-0 text-xs'>
                      {timestamp(change.created_at)}
                    </span>
                  </div>
                ))}
              </MonitoringList>

              <MonitoringList
                title={t('Recent announcements')}
                emptyLabel={t('No recent announcements')}
                empty={!overview?.announcements.length}
              >
                {(overview?.announcements ?? [])
                  .slice(0, 5)
                  .map((announcement) => (
                    <div
                      key={announcement.id}
                      className='flex flex-col gap-1 text-sm'
                    >
                      <div className='flex items-start justify-between gap-3'>
                        <LongText className='font-medium'>
                          {announcement.title}
                        </LongText>
                        {announcement.is_new && (
                          <StatusBadge
                            label={t('New')}
                            variant='info'
                            copyable={false}
                          />
                        )}
                      </div>
                      {announcement.content && (
                        <LongText className='text-muted-foreground text-xs'>
                          {announcement.content}
                        </LongText>
                      )}
                    </div>
                  ))}
              </MonitoringList>
            </div>
          )}
        </ScrollArea>
        <SheetFooter className='flex-row justify-between sm:justify-between'>
          <Button
            type='button'
            variant='outline'
            disabled={!source?.session_source || clearSessionMutation.isPending}
            onClick={() => clearSessionMutation.mutate()}
          >
            {clearSessionMutation.isPending ? (
              <Loader2 data-icon='inline-start' className='animate-spin' />
            ) : (
              <Trash2 data-icon='inline-start' />
            )}
            {t('Clear session')}
          </Button>
          <Button
            type='button'
            disabled={!source || monitorMutation.isPending}
            onClick={() =>
              source && monitorMutation.mutate(!source.monitor_enabled)
            }
          >
            {monitorMutation.isPending && (
              <Loader2 data-icon='inline-start' className='animate-spin' />
            )}
            {source?.monitor_enabled
              ? t('Disable monitor')
              : t('Enable monitor')}
          </Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  )
}
