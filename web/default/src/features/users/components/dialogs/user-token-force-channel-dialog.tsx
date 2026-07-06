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
import { useEffect, useMemo, useState, type ReactNode } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Loader2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { Combobox } from '@/components/ui/combobox'
import { Label } from '@/components/ui/label'
import { StatusBadge } from '@/components/status-badge'
import { searchChannels } from '@/features/channels/api'
import {
  clearUserTokenForceChannel,
  forceUserTokenChannel,
  getUserTokenForceChannel,
} from '../../api'

interface UserTokenForceChannelDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  userId: number
  tokenId: number
  tokenName: string
  tokenGroup?: string
  // Pre-selected channel when opening (e.g. the channel of the log row that was clicked).
  initialChannelId?: number
}

export function UserTokenForceChannelDialog({
  open,
  onOpenChange,
  userId,
  tokenId,
  tokenName,
  tokenGroup,
  initialChannelId,
}: UserTokenForceChannelDialogProps) {
  const { t } = useTranslation()
  const [channelId, setChannelId] = useState('')
  const [submitting, setSubmitting] = useState(false)

  const group = tokenGroup?.trim() || ''
  // A fixed group narrows the channel candidates; "auto"/empty lists all enabled
  // channels and lets the backend validate group membership on apply.
  const filterByGroup = group && group !== 'auto' ? group : undefined
  const hasToken = tokenId > 0

  const {
    data: statusData,
    isLoading: statusLoading,
    refetch: refetchStatus,
  } = useQuery({
    queryKey: ['token-force-channel', userId, tokenId],
    queryFn: () => getUserTokenForceChannel(userId, tokenId),
    enabled: open && hasToken,
    staleTime: 0,
  })

  const { data: channelsData, isLoading: channelsLoading } = useQuery({
    queryKey: ['force-channel-candidates', filterByGroup],
    queryFn: () =>
      searchChannels({
        group: filterByGroup,
        status: 'enabled',
        page_size: 100,
      }),
    enabled: open && hasToken,
    staleTime: 30_000,
  })

  const override = statusData?.data
  const active = !!override?.active

  const channelOptions = useMemo(
    () =>
      (channelsData?.data?.items ?? []).map((ch) => ({
        value: String(ch.id),
        label: `#${ch.id} · ${ch.name}`,
      })),
    [channelsData]
  )

  // Prefill on open: the currently-forced channel if any, else the clicked row's channel.
  // Reset on close.
  useEffect(() => {
    if (!open) {
      setChannelId('')
      return
    }
    if (active && override) {
      setChannelId(String(override.channel_id))
    } else if (initialChannelId && initialChannelId > 0) {
      setChannelId(String(initialChannelId))
    }
  }, [open, active, override, initialChannelId])

  const handleForce = async () => {
    if (!hasToken) return
    const id = Number.parseInt(channelId, 10)
    if (!id) {
      toast.error(t('Select a channel first'))
      return
    }
    setSubmitting(true)
    try {
      // No ttl_seconds: the override is a sticky forced switch with no expiry.
      const res = await forceUserTokenChannel(userId, tokenId, {
        channel_id: id,
      })
      if (res.success) {
        toast.success(t('Channel override applied'))
        await refetchStatus()
      } else {
        toast.error(res.message || t('Failed to apply channel override'))
      }
    } catch {
      toast.error(t('An unexpected error occurred'))
    } finally {
      setSubmitting(false)
    }
  }

  let statusNode: ReactNode
  if (statusLoading) {
    statusNode = (
      <div className='text-muted-foreground flex items-center gap-2'>
        <Loader2 className='size-4 animate-spin' /> {t('Loading...')}
      </div>
    )
  } else if (active) {
    statusNode = (
      <div className='flex items-center gap-2'>
        <StatusBadge variant='success' label={t('Active')} copyable={false} />
        <span>
          {t('Currently forced to channel #{{id}}', {
            id: override?.channel_id,
          })}
        </span>
      </div>
    )
  } else {
    statusNode = (
      <span className='text-muted-foreground'>
        {t('No channel override in effect')}
      </span>
    )
  }

  const handleClear = async () => {
    if (!hasToken) return
    setSubmitting(true)
    try {
      const res = await clearUserTokenForceChannel(userId, tokenId)
      if (res.success) {
        toast.success(t('Channel override cleared'))
        // Drop the local selection so Apply is disabled and the just-cleared
        // channel can't be re-forced by an accidental follow-up click.
        setChannelId('')
        await refetchStatus()
      } else {
        toast.error(res.message || t('Failed to clear channel override'))
      }
    } catch {
      toast.error(t('An unexpected error occurred'))
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className='sm:max-w-md'>
        <DialogHeader>
          <DialogTitle>{t('Force Channel')}</DialogTitle>
          <DialogDescription>
            {t(
              'Force this API key to switch to a specific channel in its group. It takes effect on the next request; the request currently in flight is not interrupted. It overrides channel affinity, but if the forced channel fails the request still falls over to other channels.'
            )}
          </DialogDescription>
        </DialogHeader>

        <div className='flex flex-col gap-4'>
          {tokenName && (
            <div className='text-muted-foreground text-sm'>
              {t('API Key')}:{' '}
              <span className='text-foreground font-medium'>{tokenName}</span>
              {group && (
                <>
                  {' · '}
                  {t('Group')}: <span className='font-mono'>{group}</span>
                </>
              )}
            </div>
          )}

          <div className='rounded-md border p-3 text-sm'>{statusNode}</div>

          <div className='flex flex-col gap-2'>
            <Label>{t('Channel')}</Label>
            <Combobox
              options={channelOptions}
              value={channelId}
              onValueChange={(v) => setChannelId(v ?? '')}
              placeholder={
                channelsLoading ? t('Loading...') : t('Select a channel')
              }
              searchPlaceholder={t('Search channels...')}
              emptyText={t('No channels found in this group')}
            />
          </div>
        </div>

        <DialogFooter className='gap-2 sm:gap-2'>
          {active && (
            <Button
              variant='outline'
              onClick={handleClear}
              disabled={submitting}
            >
              {t('Clear override')}
            </Button>
          )}
          <Button onClick={handleForce} disabled={submitting || !channelId}>
            {submitting ? t('Applying...') : t('Apply')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
