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
import { Loader2, Play, Save } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { StatusBadge, type StatusVariant } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import {
  Field,
  FieldContent,
  FieldDescription,
  FieldGroup,
  FieldLabel,
  FieldLegend,
  FieldSet,
  FieldTitle,
} from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group'
import { Switch } from '@/components/ui/switch'
import { formatTimestamp } from '@/lib/format'

import { empiricalBillingProbeStatusLabel } from './billing-probe-status'
import {
  UPSTREAM_SOURCE_AUTO_PRIORITY_COST_ADVERTISED,
  UPSTREAM_SOURCE_AUTO_PRIORITY_COST_EMPIRICAL_PROBE,
  type UpstreamSourceAutoPriorityCostSource,
  type UpstreamSourceBillingProbe,
  type UpstreamSourceBillingProbeUpdateRequest,
  type UpstreamSourceEmpiricalBillingStatus,
} from './types'

function empiricalBillingProbeStatusVariant(
  status: UpstreamSourceEmpiricalBillingStatus
): StatusVariant {
  switch (status) {
    case 'ready':
      return 'success'
    case 'temporary_failed':
    case 'stale':
      return 'warning'
    case 'identity_mismatch':
    case 'malformed':
      return 'danger'
    case 'disabled':
    case 'missing':
    case 'unsupported':
      return 'neutral'
  }
}

function formatBillingProbeMultiplier(value?: number | null) {
  return value === null || value === undefined ? '-' : `${value.toFixed(3)}x`
}

function formatBillingProbeTimestamp(value?: number) {
  return value && value > 0 ? formatTimestamp(value) : '-'
}

function BillingProbeDiagnostic(props: { label: string; value: string }) {
  return (
    <div className='flex min-w-0 flex-col gap-1'>
      <dt className='text-muted-foreground text-xs'>{props.label}</dt>
      <dd className='truncate text-sm font-medium'>{props.value}</dd>
    </div>
  )
}

export function BillingProbeControls(props: {
  probe: UpstreamSourceBillingProbe
  saving: boolean
  running: boolean
  onSave: (request: UpstreamSourceBillingProbeUpdateRequest) => void
  onRun: () => void
}) {
  const { t } = useTranslation()
  const probe = props.probe
  const [enabled, setEnabled] = useState(probe.enabled)
  const [intervalMinutes, setIntervalMinutes] = useState(probe.interval_minutes)
  const [costSource, setCostSource] =
    useState<UpstreamSourceAutoPriorityCostSource>(
      probe.auto_priority_cost_source ||
        UPSTREAM_SOURCE_AUTO_PRIORITY_COST_ADVERTISED
    )
  const idPrefix = `billing-probe-${probe.source_id}-${probe.mapping_id}`

  return (
    <FieldSet className='p-4'>
      <FieldLegend>{t('Billing probe')}</FieldLegend>
      <FieldGroup className='gap-4'>
        <Field>
          <FieldLabel>{t('Auto-priority cost source')}</FieldLabel>
          <RadioGroup
            value={costSource}
            onValueChange={(value) =>
              setCostSource(value as UpstreamSourceAutoPriorityCostSource)
            }
            className='grid gap-2 sm:grid-cols-2'
          >
            <FieldLabel htmlFor={`${idPrefix}-advertised`}>
              <Field orientation='horizontal'>
                <RadioGroupItem
                  id={`${idPrefix}-advertised`}
                  value={UPSTREAM_SOURCE_AUTO_PRIORITY_COST_ADVERTISED}
                />
                <FieldContent>
                  <FieldTitle>{t('Advertised')}</FieldTitle>
                  <FieldDescription>
                    {t('Use the mapping rate supplied by the upstream source.')}
                  </FieldDescription>
                </FieldContent>
              </Field>
            </FieldLabel>
            <FieldLabel htmlFor={`${idPrefix}-empirical`}>
              <Field orientation='horizontal'>
                <RadioGroupItem
                  id={`${idPrefix}-empirical`}
                  value={UPSTREAM_SOURCE_AUTO_PRIORITY_COST_EMPIRICAL_PROBE}
                />
                <FieldContent>
                  <FieldTitle>{t('Empirical probe')}</FieldTitle>
                  <FieldDescription>
                    {t('Use a fresh sanitized last-good billing snapshot.')}
                  </FieldDescription>
                </FieldContent>
              </Field>
            </FieldLabel>
          </RadioGroup>
        </Field>

        <Field orientation='horizontal'>
          <FieldContent>
            <FieldTitle id={`${idPrefix}-enabled-label`}>
              {t('Probe collection')}
            </FieldTitle>
            <FieldDescription>
              {t(
                'Collect billing snapshots independently from cost selection.'
              )}
            </FieldDescription>
          </FieldContent>
          <Switch
            checked={enabled}
            onCheckedChange={(checked) => setEnabled(Boolean(checked))}
            aria-labelledby={`${idPrefix}-enabled-label`}
          />
        </Field>

        <Field>
          <FieldLabel htmlFor={`${idPrefix}-interval`}>
            {t('Probe interval (minutes)')}
          </FieldLabel>
          <Input
            id={`${idPrefix}-interval`}
            type='number'
            min={5}
            max={1440}
            value={intervalMinutes}
            onChange={(event) =>
              setIntervalMinutes(Number(event.currentTarget.value))
            }
          />
        </Field>

        <dl className='grid gap-3 sm:grid-cols-2'>
          <BillingProbeDiagnostic
            label={t('Advertised rate')}
            value={formatBillingProbeMultiplier(
              probe.advertised_effective_rate_multiplier
            )}
          />
          <BillingProbeDiagnostic
            label={t('Empirical probe rate')}
            value={formatBillingProbeMultiplier(
              probe.empirical_nominal_rate_multiplier
            )}
          />
        </dl>

        <div className='flex flex-wrap items-center gap-2'>
          <span className='text-sm font-medium'>{t('Empirical status')}:</span>
          <StatusBadge
            label={t(empiricalBillingProbeStatusLabel(probe.empirical_status))}
            variant={empiricalBillingProbeStatusVariant(probe.empirical_status)}
            copyable={false}
          />
          {probe.has_last_good && (
            <StatusBadge
              label={probe.fresh ? t('Fresh') : t('Stale')}
              variant={probe.fresh ? 'success' : 'warning'}
              copyable={false}
            />
          )}
        </div>

        <dl className='grid gap-3 sm:grid-cols-2 lg:grid-cols-4'>
          <BillingProbeDiagnostic
            label={t('Last attempt')}
            value={formatBillingProbeTimestamp(probe.last_attempt_at)}
          />
          <BillingProbeDiagnostic
            label={t('Last good received')}
            value={formatBillingProbeTimestamp(probe.received_at)}
          />
          <BillingProbeDiagnostic
            label={t('Fresh until')}
            value={formatBillingProbeTimestamp(probe.fresh_until)}
          />
          <BillingProbeDiagnostic
            label={t('Observed at')}
            value={probe.observed_at || '-'}
          />
        </dl>

        {probe.has_last_good && (
          <dl className='grid gap-3 sm:grid-cols-2 lg:grid-cols-4'>
            <BillingProbeDiagnostic
              label={t('Group rate')}
              value={formatBillingProbeMultiplier(probe.group_rate_multiplier)}
            />
            <BillingProbeDiagnostic
              label={t('User rate')}
              value={formatBillingProbeMultiplier(probe.user_rate_multiplier)}
            />
            <BillingProbeDiagnostic
              label={t('Resolved rate')}
              value={formatBillingProbeMultiplier(
                probe.resolved_rate_multiplier
              )}
            />
            <BillingProbeDiagnostic
              label={t('Peak pricing')}
              value={probe.peak_rate_enabled ? t('Enabled') : t('Disabled')}
            />
            {probe.peak_rate_enabled && (
              <>
                <BillingProbeDiagnostic
                  label={t('Peak window')}
                  value={`${probe.peak_start || '-'}–${probe.peak_end || '-'}`}
                />
                <BillingProbeDiagnostic
                  label={t('Peak multiplier')}
                  value={formatBillingProbeMultiplier(
                    probe.peak_rate_multiplier
                  )}
                />
                <BillingProbeDiagnostic
                  label={t('Timezone')}
                  value={probe.timezone || '-'}
                />
              </>
            )}
            <BillingProbeDiagnostic
              label={t('Applied peak (diagnostic)')}
              value={formatBillingProbeMultiplier(
                probe.applied_peak_multiplier
              )}
            />
            <BillingProbeDiagnostic
              label={t('Effective rate (diagnostic)')}
              value={formatBillingProbeMultiplier(
                probe.effective_rate_multiplier
              )}
            />
          </dl>
        )}

        <div className='flex flex-wrap justify-end gap-2'>
          <Button
            type='button'
            variant='outline'
            disabled={!probe.enabled || props.running || props.saving}
            onClick={props.onRun}
          >
            {props.running ? (
              <Loader2 data-icon='inline-start' className='animate-spin' />
            ) : (
              <Play data-icon='inline-start' />
            )}
            {t('Run probe')}
          </Button>
          <Button
            type='button'
            disabled={props.saving || props.running}
            onClick={() =>
              props.onSave({
                enabled,
                interval_minutes: intervalMinutes,
                auto_priority_cost_source: costSource,
              })
            }
          >
            {props.saving ? (
              <Loader2 data-icon='inline-start' className='animate-spin' />
            ) : (
              <Save data-icon='inline-start' />
            )}
            {t('Save probe settings')}
          </Button>
        </div>
      </FieldGroup>
    </FieldSet>
  )
}
