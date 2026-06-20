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
import { useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { type Row } from '@tanstack/react-table'
import {
  Activity,
  MoreHorizontal,
  Boxes,
  Pencil,
  TestTube,
  Gauge,
  DollarSign,
  Download,
  Copy,
  Power,
  PowerOff,
  Key,
  Trash2,
  RefreshCw,
  Loader2,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuShortcut,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { StatusBadge } from '@/components/status-badge'
import { MODEL_FETCHABLE_TYPES } from '../constants'
import {
  channelsQueryKeys,
  formatRelativeTime,
  formatResponseTime,
  handleDeleteChannel,
  handleTestChannel,
  handleToggleChannelStatus,
  isChannelEnabled,
  isMultiKeyChannel,
} from '../lib'
import { parseUpstreamUpdateMeta } from '../lib/upstream-update-utils'
import type { Channel } from '../types'
import { useChannels } from './channels-provider'

interface DataTableRowActionsProps {
  row: Row<Channel>
}

type MonitorLatestStatus = NonNullable<
  Channel['monitor_info']
>['latest_status']

function monitorStatusVariant(status: MonitorLatestStatus | undefined) {
  if (status === 'success') return 'success'
  if (status === 'failed' || status === 'error') return 'danger'
  if (status === 'degraded') return 'warning'
  return 'neutral'
}

function formatMonitorAvailability(
  availability: number | null | undefined,
  noDataLabel: string
) {
  if (typeof availability !== 'number' || !Number.isFinite(availability)) {
    return noDataLabel
  }
  return `${Math.round(availability * 100)}%`
}

function ChannelMonitorMenuSummary({ channel }: { channel: Channel }) {
  const { t } = useTranslation()
  const monitorInfo = channel.monitor_info
  const enabled = monitorInfo?.enabled === true
  const latestStatus = monitorInfo?.latest_status
  const latestCheckedAt = monitorInfo?.latest_checked_at ?? 0
  const checks = monitorInfo?.seven_day_checks ?? 0
  const successes = monitorInfo?.seven_day_successes ?? 0
  const hasMonitorData = checks > 0 || latestCheckedAt > 0
  const availabilityLabel = formatMonitorAvailability(
    monitorInfo?.seven_day_availability,
    t('No data')
  )
  const averageLatency =
    typeof monitorInfo?.average_latency_ms === 'number'
      ? formatResponseTime(monitorInfo.average_latency_ms, t)
      : t('No data')
  const latestLatency =
    typeof monitorInfo?.latest_latency_ms === 'number'
      ? formatResponseTime(monitorInfo.latest_latency_ms, t)
      : t('No data')
  const latestTime = latestCheckedAt > 0 ? formatRelativeTime(latestCheckedAt) : t('No data')

  return (
    <div className='px-2 py-2 text-xs' onClick={(event) => event.stopPropagation()}>
      <div className='mb-2 flex items-center justify-between gap-2'>
        <span className='flex items-center gap-1.5 font-medium'>
          <Activity className='size-3.5' />
          {t('Monitor')}
        </span>
        <StatusBadge
          label={enabled ? t('Enabled') : t('Disabled')}
          variant={enabled ? 'success' : 'neutral'}
          size='sm'
          copyable={false}
        />
      </div>
      {hasMonitorData ? (
        <div className='text-muted-foreground grid gap-1'>
          <div className='flex justify-between gap-3'>
            <span>{t('7-day')}</span>
            <span className='text-foreground font-mono'>
              {successes}/{checks} · {availabilityLabel}
            </span>
          </div>
          <div className='flex justify-between gap-3'>
            <span>{t('Average')}</span>
            <span className='text-foreground font-mono'>{averageLatency}</span>
          </div>
          <div className='flex justify-between gap-3'>
            <span>{t('Latest')}</span>
            <span className='text-foreground flex items-center gap-1.5'>
              <StatusBadge
                label={latestStatus ? t(latestStatus) : t('No data')}
                variant={monitorStatusVariant(latestStatus)}
                size='sm'
                copyable={false}
              />
              <span className='font-mono'>{latestLatency}</span>
            </span>
          </div>
          <div className='flex justify-between gap-3'>
            <span>{t('Last Checked')}</span>
            <span className='text-foreground font-mono'>{latestTime}</span>
          </div>
          {monitorInfo?.latest_message && (
            <div className='text-destructive line-clamp-2'>
              {monitorInfo.latest_message}
            </div>
          )}
        </div>
      ) : (
        <div className='text-muted-foreground'>{t('No monitor data')}</div>
      )}
    </div>
  )
}

export function DataTableRowActions({ row }: DataTableRowActionsProps) {
  const { t } = useTranslation()
  const channel = row.original
  const { setOpen, setCurrentRow, upstream } = useChannels()
  const queryClient = useQueryClient()
  const [deleteConfirmOpen, setDeleteConfirmOpen] = useState(false)
  const [isTesting, setIsTesting] = useState(false)
  const [isTogglingStatus, setIsTogglingStatus] = useState(false)

  const isEnabled = isChannelEnabled(channel)
  const isMultiKey = isMultiKeyChannel(channel)

  const handleEdit = () => {
    setCurrentRow(channel)
    setOpen('update-channel')
  }

  const handleTest = () => {
    setCurrentRow(channel)
    setOpen('test-channel')
  }

  const handleDirectTest = async (e: React.MouseEvent<HTMLButtonElement>) => {
    e.stopPropagation()
    setIsTesting(true)
    try {
      await handleTestChannel(
        channel.id,
        { channelName: channel.name },
        () => {
          queryClient.invalidateQueries({ queryKey: channelsQueryKeys.lists() })
        }
      )
    } finally {
      setIsTesting(false)
    }
  }

  const handleQueryBalance = () => {
    setCurrentRow(channel)
    setOpen('balance-query')
  }

  const handleFetchModels = () => {
    setCurrentRow(channel)
    setOpen('fetch-models')
  }

  const handleManageOllamaModels = () => {
    setCurrentRow(channel)
    setOpen('ollama-models')
  }

  const handleCopy = () => {
    setCurrentRow(channel)
    setOpen('copy-channel')
  }

  const handleManageKeys = () => {
    setCurrentRow(channel)
    setOpen('multi-key-manage')
  }

  const handleToggleStatus = async (
    e?: React.MouseEvent<HTMLButtonElement>
  ) => {
    e?.stopPropagation()
    setIsTogglingStatus(true)
    try {
      await handleToggleChannelStatus(channel.id, channel.status, queryClient)
    } finally {
      setIsTogglingStatus(false)
    }
  }

  return (
    <div className='-ml-1.5 flex items-center gap-1'>
      <Tooltip>
        <TooltipTrigger
          render={
            <Button
              variant='ghost'
              size='icon-sm'
              onClick={handleDirectTest}
              disabled={isTesting}
              aria-label={t('Test Connection')}
            />
          }
        >
          {isTesting ? (
            <Loader2 className='size-4 animate-spin' />
          ) : (
            <Gauge className='size-4' />
          )}
        </TooltipTrigger>
        <TooltipContent>{t('Test Connection')}</TooltipContent>
      </Tooltip>

      <Tooltip>
        <TooltipTrigger
          render={
            <Button
              variant='ghost'
              size='icon-sm'
              onClick={handleToggleStatus}
              disabled={isTogglingStatus}
              aria-label={isEnabled ? t('Disable') : t('Enable')}
              className={
                isEnabled
                  ? 'text-destructive hover:text-destructive'
                  : 'text-emerald-600 hover:text-emerald-600 dark:text-emerald-400 dark:hover:text-emerald-400'
              }
            />
          }
        >
          {isTogglingStatus ? (
            <Loader2 className='size-4 animate-spin' />
          ) : isEnabled ? (
            <PowerOff className='size-4' />
          ) : (
            <Power className='size-4' />
          )}
        </TooltipTrigger>
        <TooltipContent>
          {isEnabled ? t('Disable') : t('Enable')}
        </TooltipContent>
      </Tooltip>

      <DropdownMenu>
        <DropdownMenuTrigger
          render={
            <Button
              variant='ghost'
              className='data-popup-open:bg-muted flex h-8 w-8 p-0'
            />
          }
        >
          <MoreHorizontal className='h-4 w-4' />
          <span className='sr-only'>{t('Open menu')}</span>
        </DropdownMenuTrigger>
        <DropdownMenuContent align='end' className='w-72'>
          <ChannelMonitorMenuSummary channel={channel} />
          <DropdownMenuSeparator />

          {/* Edit */}
          <DropdownMenuItem onClick={handleEdit}>
            {t('Edit')}
            <DropdownMenuShortcut>
              <Pencil size={16} />
            </DropdownMenuShortcut>
          </DropdownMenuItem>

          {/* Test Connection */}
          <DropdownMenuItem onClick={handleTest}>
            {t('Test Connection')}
            <DropdownMenuShortcut>
              <TestTube size={16} />
            </DropdownMenuShortcut>
          </DropdownMenuItem>

          {/* Query Balance */}
          <DropdownMenuItem onClick={handleQueryBalance}>
            {t('Query Balance')}
            <DropdownMenuShortcut>
              <DollarSign size={16} />
            </DropdownMenuShortcut>
          </DropdownMenuItem>

          {/* Fetch Models */}
          <DropdownMenuItem onClick={handleFetchModels}>
            {t('Fetch Models')}
            <DropdownMenuShortcut>
              <Download size={16} />
            </DropdownMenuShortcut>
          </DropdownMenuItem>

          {/* Detect Upstream Updates (only for fetchable channel types) */}
          {MODEL_FETCHABLE_TYPES.has(channel.type) && (
            <DropdownMenuItem
              onClick={() => {
                const meta = parseUpstreamUpdateMeta(channel.settings)
                if (
                  meta.pendingAddModels.length > 0 ||
                  meta.pendingRemoveModels.length > 0
                ) {
                  upstream.openModal(
                    channel,
                    meta.pendingAddModels,
                    meta.pendingRemoveModels,
                    meta.pendingAddModels.length > 0 ? 'add' : 'remove'
                  )
                } else {
                  upstream.detectChannelUpdates(channel)
                }
              }}
            >
              {t('Upstream Updates')}
              <DropdownMenuShortcut>
                <RefreshCw size={16} />
              </DropdownMenuShortcut>
            </DropdownMenuItem>
          )}

          {/* Ollama Models (only for Ollama channels) */}
          {channel.type === 4 && (
            <DropdownMenuItem onClick={handleManageOllamaModels}>
              {t('Manage Ollama Models')}
              <DropdownMenuShortcut>
                <Boxes size={16} />
              </DropdownMenuShortcut>
            </DropdownMenuItem>
          )}

          <DropdownMenuSeparator />

          {/* Copy Channel */}
          <DropdownMenuItem onClick={handleCopy}>
            {t('Copy Channel')}
            <DropdownMenuShortcut>
              <Copy size={16} />
            </DropdownMenuShortcut>
          </DropdownMenuItem>

          {/* Manage Keys (only for multi-key channels) */}
          {isMultiKey && (
            <DropdownMenuItem onClick={handleManageKeys}>
              {t('Manage Keys')}
              <DropdownMenuShortcut>
                <Key size={16} />
              </DropdownMenuShortcut>
            </DropdownMenuItem>
          )}

          <DropdownMenuSeparator />

          {/* Delete */}
          <DropdownMenuItem
            onSelect={(e) => {
              e.preventDefault()
              setDeleteConfirmOpen(true)
            }}
            className='text-destructive focus:text-destructive'
          >
            {t('Delete')}
            <DropdownMenuShortcut>
              <Trash2 size={16} />
            </DropdownMenuShortcut>
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>

      <ConfirmDialog
        open={deleteConfirmOpen}
        onOpenChange={setDeleteConfirmOpen}
        title={t('Delete Channel')}
        desc={`Are you sure you want to delete "${channel.name}"? This action cannot be undone.`}
        confirmText='Delete'
        destructive
        handleConfirm={() => {
          handleDeleteChannel(channel.id, queryClient)
          setDeleteConfirmOpen(false)
        }}
      />
    </div>
  )
}
