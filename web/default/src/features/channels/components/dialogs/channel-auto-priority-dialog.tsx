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

import {
  runChannelAutoPriorityGroup,
  updateChannelMonitorSettings,
} from '../../api'
import { channelsQueryKeys } from '../../lib'
import {
  buildChannelAutoPrioritySettingsPayload,
  isChannelAutoPriorityManagedByUpstream,
  normalizeAutoPriorityInterval,
  normalizeAutoPriorityRateMultiplier,
  normalizeAutoPriorityWindowHours,
  readChannelAutoPrioritySettings,
  type ChannelAutoPrioritySettingsDraft,
} from '../../lib/channel-monitor'
import type { Channel } from '../../types'

interface ChannelAutoPriorityDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  channel: Channel | null
}

export function ChannelAutoPriorityDialog(
  props: ChannelAutoPriorityDialogProps
) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const channelId = props.channel?.id ?? 0
  const defaults = useMemo(
    () => readChannelAutoPrioritySettings(props.channel),
    [props.channel]
  )
  const [autoPriorityEnabled, setAutoPriorityEnabled] = useState(
    defaults.autoPriorityEnabled
  )
  const [intervalInput, setIntervalInput] = useState(
    String(defaults.autoPriorityIntervalMinutes)
  )
  const [metricsWindowInput, setMetricsWindowInput] = useState(
    String(defaults.autoPriorityWindowHours)
  )
  const [availabilityWindowInput, setAvailabilityWindowInput] = useState(
    String(defaults.autoPriorityAvailabilityWindowHours)
  )
  const [rateMultiplierInput, setRateMultiplierInput] = useState(
    defaults.autoPriorityRateMultiplier === null
      ? ''
      : String(defaults.autoPriorityRateMultiplier)
  )
  const [savedSettings, setSavedSettings] =
    useState<ChannelAutoPrioritySettingsDraft>(defaults)
  const [isSaving, setIsSaving] = useState(false)
  const [isRecomputing, setIsRecomputing] = useState(false)

  useEffect(() => {
    if (!props.open) return
    setAutoPriorityEnabled(defaults.autoPriorityEnabled)
    setIntervalInput(String(defaults.autoPriorityIntervalMinutes))
    setMetricsWindowInput(String(defaults.autoPriorityWindowHours))
    setAvailabilityWindowInput(
      String(defaults.autoPriorityAvailabilityWindowHours)
    )
    setRateMultiplierInput(
      defaults.autoPriorityRateMultiplier === null
        ? ''
        : String(defaults.autoPriorityRateMultiplier)
    )
    setSavedSettings(defaults)
  }, [defaults, props.open])

  const managedByUpstream = isChannelAutoPriorityManagedByUpstream(
    props.channel
  )
  const perChannelSettingsEditable = autoPriorityEnabled && !managedByUpstream
  const intervalValue = Number(intervalInput)
  const isIntervalValid = Number.isInteger(intervalValue) && intervalValue >= 0
  const metricsWindowValue = Number(metricsWindowInput)
  const isMetricsWindowValid =
    !perChannelSettingsEditable ||
    (Number.isInteger(metricsWindowValue) &&
      metricsWindowValue >= 1 &&
      metricsWindowValue <= 168)
  const availabilityWindowValue = Number(availabilityWindowInput)
  const isAvailabilityWindowValid =
    Number.isInteger(availabilityWindowValue) &&
    availabilityWindowValue >= 1 &&
    availabilityWindowValue <= 168
  const rateMultiplierValue =
    rateMultiplierInput.trim() === '' ? null : Number(rateMultiplierInput)
  const isRateMultiplierValid =
    !perChannelSettingsEditable ||
    rateMultiplierValue === null ||
    (Number.isFinite(rateMultiplierValue) && rateMultiplierValue > 0)
  const currentSettings: ChannelAutoPrioritySettingsDraft = {
    autoPriorityEnabled,
    autoPriorityIntervalMinutes: normalizeAutoPriorityInterval(
      intervalInput,
      savedSettings.autoPriorityIntervalMinutes
    ),
    autoPriorityWindowHours: normalizeAutoPriorityWindowHours(
      metricsWindowInput,
      savedSettings.autoPriorityWindowHours
    ),
    autoPriorityAvailabilityWindowHours: normalizeAutoPriorityWindowHours(
      availabilityWindowInput,
      savedSettings.autoPriorityAvailabilityWindowHours
    ),
    autoPriorityRateMultiplier:
      normalizeAutoPriorityRateMultiplier(rateMultiplierInput),
  }
  const settingsDirty =
    currentSettings.autoPriorityEnabled !== savedSettings.autoPriorityEnabled ||
    currentSettings.autoPriorityIntervalMinutes !==
      savedSettings.autoPriorityIntervalMinutes ||
    currentSettings.autoPriorityWindowHours !==
      savedSettings.autoPriorityWindowHours ||
    currentSettings.autoPriorityAvailabilityWindowHours !==
      savedSettings.autoPriorityAvailabilityWindowHours ||
    currentSettings.autoPriorityRateMultiplier !==
      savedSettings.autoPriorityRateMultiplier

  const handleSave = async () => {
    if (!props.channel) return
    if (!isIntervalValid) {
      toast.error(t('Auto priority interval must be 0 minutes or greater'))
      return
    }
    if (!isMetricsWindowValid) {
      toast.error(t('Auto priority window must be between 1 and 168 hours'))
      return
    }
    if (!isAvailabilityWindowValid) {
      toast.error(t('Availability window must be between 1 and 168 hours'))
      return
    }
    if (!isRateMultiplierValid) {
      toast.error(t('Auto priority rate multiplier must be greater than 0'))
      return
    }

    setIsSaving(true)
    try {
      const response = await updateChannelMonitorSettings(
        props.channel.id,
        buildChannelAutoPrioritySettingsPayload(props.channel, currentSettings),
        'auto-priority'
      )
      if (response.success) {
        setSavedSettings(currentSettings)
        toast.success(t('Auto priority settings saved'))
        queryClient.invalidateQueries({ queryKey: channelsQueryKeys.lists() })
      } else {
        toast.error(
          response.message || t('Failed to save auto priority settings')
        )
      }
    } catch (error: unknown) {
      toast.error(
        error instanceof Error
          ? error.message
          : t('Failed to save auto priority settings')
      )
    } finally {
      setIsSaving(false)
    }
  }

  const handleForceRecompute = async () => {
    if (!props.channel) return

    setIsRecomputing(true)
    try {
      const response = await runChannelAutoPriorityGroup(props.channel.id)
      if (response.success) {
        toast.success(t('Auto priority recomputed for this group'))
        queryClient.invalidateQueries({ queryKey: channelsQueryKeys.lists() })
      } else {
        toast.error(
          response.message ||
            t('Failed to recompute auto priority for this group')
        )
      }
    } catch (error: unknown) {
      toast.error(
        error instanceof Error
          ? error.message
          : t('Failed to recompute auto priority for this group')
      )
    } finally {
      setIsRecomputing(false)
    }
  }

  return (
    <Dialog open={props.open} onOpenChange={props.onOpenChange}>
      <DialogContent className='max-h-[calc(100vh-2rem)] overflow-y-auto sm:max-w-[520px]'>
        <DialogHeader>
          <DialogTitle>{t('Auto Priority')}</DialogTitle>
          <DialogDescription>
            {props.channel?.name ?? t('No data')}
          </DialogDescription>
        </DialogHeader>

        <div className='flex flex-col gap-5'>
          <Field
            orientation='horizontal'
            className='items-start justify-between gap-3'
            data-disabled={managedByUpstream || undefined}
          >
            <FieldContent>
              <FieldLabel
                htmlFor={`channel-auto-priority-enabled-${channelId}`}
              >
                {t('Auto Priority')}
              </FieldLabel>
              <FieldDescription>
                {managedByUpstream
                  ? t(
                      'Managed by the upstream source rule for this generated channel.'
                    )
                  : t(
                      'Re-score this channel from recent cost and availability data.'
                    )}
              </FieldDescription>
            </FieldContent>
            <Switch
              id={`channel-auto-priority-enabled-${channelId}`}
              checked={autoPriorityEnabled}
              disabled={managedByUpstream}
              onCheckedChange={setAutoPriorityEnabled}
            />
          </Field>

          <FieldGroup className='gap-4 sm:grid sm:grid-cols-2'>
            <Field data-invalid={!isIntervalValid || undefined}>
              <FieldLabel
                htmlFor={`channel-auto-priority-interval-${channelId}`}
              >
                {t('Auto Priority Interval Minutes')}
              </FieldLabel>
              <Input
                id={`channel-auto-priority-interval-${channelId}`}
                type='number'
                min={0}
                step={1}
                value={intervalInput}
                aria-invalid={!isIntervalValid}
                onChange={(event) => setIntervalInput(event.target.value)}
              />
              <FieldDescription>
                {t('Set to 0 to score on every worker tick.')}
              </FieldDescription>
              <FieldDescription>
                {t(
                  'The interval applies to all auto-priority channels in the current group, including upstream-generated channels.'
                )}
              </FieldDescription>
            </Field>

            <Field
              data-invalid={!isMetricsWindowValid || undefined}
              data-disabled={!perChannelSettingsEditable || undefined}
            >
              <FieldLabel htmlFor={`channel-auto-priority-window-${channelId}`}>
                {t('Metrics Window Hours')}
              </FieldLabel>
              <Input
                id={`channel-auto-priority-window-${channelId}`}
                type='number'
                min={1}
                max={168}
                step={1}
                value={metricsWindowInput}
                disabled={!perChannelSettingsEditable}
                aria-invalid={!isMetricsWindowValid}
                onChange={(event) => setMetricsWindowInput(event.target.value)}
              />
              <FieldDescription>
                {t(
                  'Scores recent usage, latency, and throughput over this window.'
                )}
              </FieldDescription>
            </Field>

            <Field data-invalid={!isAvailabilityWindowValid || undefined}>
              <FieldLabel
                htmlFor={`channel-auto-priority-availability-window-${channelId}`}
              >
                {t('Availability Window Hours')}
              </FieldLabel>
              <Input
                id={`channel-auto-priority-availability-window-${channelId}`}
                type='number'
                min={1}
                max={168}
                step={1}
                value={availabilityWindowInput}
                aria-invalid={!isAvailabilityWindowValid}
                onChange={(event) =>
                  setAvailabilityWindowInput(event.target.value)
                }
              />
              <FieldDescription>
                {t(
                  'Applies to all auto-priority channels in the current group, including upstream-generated channels.'
                )}
              </FieldDescription>
            </Field>

            <Field
              data-invalid={!isRateMultiplierValid || undefined}
              data-disabled={!perChannelSettingsEditable || undefined}
            >
              <FieldLabel
                htmlFor={`channel-auto-priority-rate-multiplier-${channelId}`}
              >
                {t('Rate Multiplier')}
              </FieldLabel>
              <Input
                id={`channel-auto-priority-rate-multiplier-${channelId}`}
                type='number'
                min={0.000001}
                step='0.01'
                value={rateMultiplierInput}
                disabled={!perChannelSettingsEditable}
                aria-invalid={!isRateMultiplierValid}
                onChange={(event) => setRateMultiplierInput(event.target.value)}
              />
              <FieldDescription>
                {t('Leave empty to score this channel at 1.0x cost.')}
              </FieldDescription>
            </Field>
          </FieldGroup>

          <div className='flex flex-col-reverse gap-2 sm:flex-row sm:justify-between'>
            <Button
              type='button'
              variant='outline'
              onClick={handleForceRecompute}
              disabled={!props.channel || isSaving || isRecomputing}
            >
              {isRecomputing && <Spinner data-icon='inline-start' />}
              {isRecomputing
                ? t('Recomputing...')
                : t('Recompute auto priority for this group now')}
            </Button>
            <Button
              type='button'
              onClick={handleSave}
              disabled={
                !props.channel ||
                !settingsDirty ||
                !isIntervalValid ||
                !isMetricsWindowValid ||
                !isAvailabilityWindowValid ||
                !isRateMultiplierValid ||
                isSaving ||
                isRecomputing
              }
            >
              {isSaving && <Spinner data-icon='inline-start' />}
              {isSaving ? t('Saving...') : t('Save Settings')}
            </Button>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  )
}
