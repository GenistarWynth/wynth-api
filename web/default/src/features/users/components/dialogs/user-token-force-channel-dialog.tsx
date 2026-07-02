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
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { StatusBadge } from '@/components/status-badge'
import { searchChannels } from '@/features/channels/api'
import type { ApiKey } from '@/features/keys/types'
import {
  clearUserTokenForceChannel,
  forceUserTokenChannel,
  getUserTokenForceChannel,
} from '../../api'

interface UserTokenForceChannelDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  token: ApiKey | null
  userId: number
}

const DEFAULT_TTL_MINUTES = '30'

export function UserTokenForceChannelDialog({
  open,
  onOpenChange,
  token,
  userId,
}: UserTokenForceChannelDialogProps) {
  const { t } = useTranslation()
  const [channelId, setChannelId] = useState('')
  const [ttlMinutes, setTtlMinutes] = useState(DEFAULT_TTL_MINUTES)
  const [submitting, setSubmitting] = useState(false)

  const tokenGroup = token?.group?.trim() || ''
  // A fixed group narrows the channel candidates; "auto"/empty lists all enabled
  // channels and lets the backend validate group membership on apply.
  const filterByGroup =
    tokenGroup && tokenGroup !== 'auto' ? tokenGroup : undefined

  const {
    data: statusData,
    isLoading: statusLoading,
    refetch: refetchStatus,
  } = useQuery({
    queryKey: ['token-force-channel', userId, token?.id],
    queryFn: () => getUserTokenForceChannel(userId, token?.id ?? 0),
    enabled: open && !!token,
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
    enabled: open && !!token,
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

  // Prefill with the currently-forced channel on open; reset on close.
  useEffect(() => {
    if (open && active && override) {
      setChannelId(String(override.channel_id))
    }
    if (!open) {
      setChannelId('')
      setTtlMinutes(DEFAULT_TTL_MINUTES)
    }
  }, [open, active, override])

  const handleForce = async () => {
    if (!token) return
    const id = Number.parseInt(channelId, 10)
    if (!id) {
      toast.error(t('Select a channel first'))
      return
    }
    const minutes = Number.parseInt(ttlMinutes, 10)
    setSubmitting(true)
    try {
      const res = await forceUserTokenChannel(userId, token.id, {
        channel_id: id,
        ttl_seconds: Number.isFinite(minutes) && minutes > 0 ? minutes * 60 : 0,
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
    if (!token) return
    setSubmitting(true)
    try {
      const res = await clearUserTokenForceChannel(userId, token.id)
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
              'Route this API key to a specific channel in its group. It takes effect on the next request; the request currently in flight is not interrupted.'
            )}
          </DialogDescription>
        </DialogHeader>

        <div className='flex flex-col gap-4'>
          {token && (
            <div className='text-muted-foreground text-sm'>
              {t('API Key')}:{' '}
              <span className='text-foreground font-medium'>{token.name}</span>
              {tokenGroup && (
                <>
                  {' · '}
                  {t('Group')}:{' '}
                  <span className='font-mono'>{tokenGroup}</span>
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

          <div className='flex flex-col gap-2'>
            <Label>{t('Duration (minutes)')}</Label>
            <Input
              type='number'
              min={1}
              value={ttlMinutes}
              onChange={(e) => setTtlMinutes(e.target.value)}
              placeholder={DEFAULT_TTL_MINUTES}
            />
            <p className='text-muted-foreground text-xs'>
              {t(
                'Defaults to 30 minutes. Maximum 24 hours; the override auto-expires.'
              )}
            </p>
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
