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
import { useQueryClient } from '@tanstack/react-query'
import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import {
  Field,
  FieldContent,
  FieldDescription,
  FieldGroup,
  FieldLabel,
} from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Spinner } from '@/components/ui/spinner'
import { Switch } from '@/components/ui/switch'

import { updateChannelMonitorSettings } from '../../api'
import { channelsQueryKeys } from '../../lib'
import {
  buildChannelDeadRecoverySettingsPayload,
  isChannelDeadRecoveryRangeValid,
  readChannelDeadRecoverySettings,
  type ChannelDeadRecoverySettingsDraft,
} from '../../lib/channel-monitor'
import type { Channel } from '../../types'

interface ChannelDeadRecoveryDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  channel: Channel | null
  onSaved: (channel: Channel) => void
}

export function ChannelDeadRecoveryDialog(
  props: ChannelDeadRecoveryDialogProps
) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const channelId = props.channel?.id ?? 0
  const defaults = useMemo(
    () => readChannelDeadRecoverySettings(props.channel),
    [props.channel]
  )
  const [enabled, setEnabled] = useState(defaults.enabled)
  const [minMinutesInput, setMinMinutesInput] = useState(
    String(defaults.minMinutes)
  )
  const [maxMinutesInput, setMaxMinutesInput] = useState(
    String(defaults.maxMinutes)
  )
  const [savedSettings, setSavedSettings] =
    useState<ChannelDeadRecoverySettingsDraft>(defaults)
  const [isSaving, setIsSaving] = useState(false)

  useEffect(() => {
    if (!props.open) return
    setEnabled(defaults.enabled)
    setMinMinutesInput(String(defaults.minMinutes))
    setMaxMinutesInput(String(defaults.maxMinutes))
    setSavedSettings(defaults)
  }, [defaults, props.open])

  const minMinutes = Number(minMinutesInput)
  const maxMinutes = Number(maxMinutesInput)
  const isMinimumValid = Number.isInteger(minMinutes) && minMinutes >= 1
  const isRangeValid = isChannelDeadRecoveryRangeValid(
    minMinutesInput,
    maxMinutesInput
  )
  const currentSettings: ChannelDeadRecoverySettingsDraft = {
    enabled,
    minMinutes: isMinimumValid ? minMinutes : savedSettings.minMinutes,
    maxMinutes: isRangeValid ? maxMinutes : savedSettings.maxMinutes,
  }
  const isDirty =
    currentSettings.enabled !== savedSettings.enabled ||
    currentSettings.minMinutes !== savedSettings.minMinutes ||
    currentSettings.maxMinutes !== savedSettings.maxMinutes

  const handleSave = async () => {
    if (!props.channel) return
    if (!isMinimumValid) {
      toast.error(t('Minimum recovery delay must be at least 1 minute'))
      return
    }
    if (!isRangeValid) {
      toast.error(
        t('Maximum recovery delay must be greater than or equal to the minimum')
      )
      return
    }

    setIsSaving(true)
    try {
      const payload = buildChannelDeadRecoverySettingsPayload(
        props.channel,
        currentSettings
      )
      const response = await updateChannelMonitorSettings(
        props.channel.id,
        payload,
        'dead-recovery'
      )
      if (!response.success) {
        toast.error(
          response.message || t('Failed to save post-mortem recovery settings')
        )
        return
      }

      setSavedSettings(currentSettings)
      if (response.data) props.onSaved(response.data)
      toast.success(t('Post-mortem recovery settings saved'))
      queryClient.invalidateQueries({ queryKey: channelsQueryKeys.lists() })
      queryClient.invalidateQueries({
        queryKey: ['channel-monitor-detail', props.channel.id],
      })
    } catch (error: unknown) {
      toast.error(
        error instanceof Error
          ? error.message
          : t('Failed to save post-mortem recovery settings')
      )
    } finally {
      setIsSaving(false)
    }
  }

  return (
    <Dialog open={props.open} onOpenChange={props.onOpenChange}>
      <DialogContent className='sm:max-w-md'>
        <DialogHeader>
          <DialogTitle>{t('Post-mortem recovery')}</DialogTitle>
          <DialogDescription>
            {t(
              'Only applies when this channel is auto-disabled and monitoring is off.'
            )}
          </DialogDescription>
        </DialogHeader>

        <FieldGroup>
          <Field orientation='horizontal'>
            <FieldContent>
              <FieldLabel
                htmlFor={`channel-dead-recovery-enabled-${channelId}`}
              >
                {t('Enable post-mortem recovery')}
              </FieldLabel>
            </FieldContent>
            <Switch
              id={`channel-dead-recovery-enabled-${channelId}`}
              checked={enabled}
              onCheckedChange={setEnabled}
            />
          </Field>

          <div className='grid gap-4 sm:grid-cols-2'>
            <Field data-invalid={!isMinimumValid || undefined}>
              <FieldLabel htmlFor={`channel-dead-recovery-min-${channelId}`}>
                {t('Post-mortem recovery minimum (minutes)')}
              </FieldLabel>
              <Input
                id={`channel-dead-recovery-min-${channelId}`}
                type='number'
                min={1}
                step={1}
                value={minMinutesInput}
                aria-invalid={!isMinimumValid}
                onChange={(event) => setMinMinutesInput(event.target.value)}
              />
              <FieldDescription>
                {t(
                  'Earliest randomized delay before retrying an auto-disabled channel without per-channel monitoring.'
                )}
              </FieldDescription>
            </Field>

            <Field data-invalid={!isRangeValid || undefined}>
              <FieldLabel htmlFor={`channel-dead-recovery-max-${channelId}`}>
                {t('Post-mortem recovery maximum (minutes)')}
              </FieldLabel>
              <Input
                id={`channel-dead-recovery-max-${channelId}`}
                type='number'
                min={1}
                step={1}
                value={maxMinutesInput}
                aria-invalid={!isRangeValid}
                onChange={(event) => setMaxMinutesInput(event.target.value)}
              />
              <FieldDescription>
                {t(
                  'Latest randomized delay before retrying an auto-disabled channel without per-channel monitoring.'
                )}
              </FieldDescription>
            </Field>
          </div>
        </FieldGroup>

        <DialogFooter>
          <Button
            type='button'
            variant='outline'
            onClick={() => props.onOpenChange(false)}
            disabled={isSaving}
          >
            {t('Cancel')}
          </Button>
          <Button
            type='button'
            onClick={handleSave}
            disabled={!props.channel || !isDirty || !isRangeValid || isSaving}
          >
            {isSaving && <Spinner data-icon='inline-start' />}
            {isSaving ? t('Saving...') : t('Save Settings')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
