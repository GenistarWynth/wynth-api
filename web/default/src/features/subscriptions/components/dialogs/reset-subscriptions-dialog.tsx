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
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { Switch } from '@/components/ui/switch'
import {
  SecureVerificationDialog,
  useSecureVerification,
} from '@/features/auth/secure-verification'
import { isVerificationRequiredError } from '@/lib/secure-verification'

import { resetPlanSubscriptions } from '../../api'
import { useSubscriptions } from '../subscriptions-provider'

export function ResetSubscriptionsDialog() {
  const { t } = useTranslation()
  const { open, setOpen, currentRow, triggerRefresh } = useSubscriptions()
  const [advanceResetTime, setAdvanceResetTime] = useState(true)
  const [resetting, setResetting] = useState(false)
  const {
    open: verificationOpen,
    setOpen: setVerificationOpen,
    methods: verificationMethods,
    state: verificationState,
    executeVerification,
    withVerification,
    cancel: cancelVerification,
    setCode: setVerificationCode,
    switchMethod: switchVerificationMethod,
  } = useSecureVerification()
  const isOpen = open === 'reset-subscriptions'
  const plan = currentRow?.plan
  const planLabel = plan?.title || (plan?.id ? `#${plan.id}` : '-')

  useEffect(() => {
    if (isOpen) setAdvanceResetTime(true)
  }, [isOpen])

  const performReset = async () => {
    if (!plan?.id) return null
    const res = await resetPlanSubscriptions(plan.id, {
      advance_reset_time: advanceResetTime,
    })
    if (!res.success) {
      throw new Error(res.message || t('Operation failed'))
    }
    toast.success(
      t('Reset {{count}} active subscriptions', {
        count: res.data?.reset_count || 0,
      })
    )
    triggerRefresh()
    setOpen(null)
    return res
  }

  const handleConfirm = async () => {
    if (!plan?.id) return
    setResetting(true)
    try {
      await withVerification(performReset, {
        preferredMethod: 'passkey',
        title: t('Reset subscription quota'),
        description: t('Reset all active subscriptions under {{plan}}?', {
          plan: planLabel,
        }),
      })
    } catch (error) {
      if (!isVerificationRequiredError(error)) {
        toast.error(
          error instanceof Error ? error.message : t('Operation failed')
        )
      }
    } finally {
      setResetting(false)
    }
  }

  return (
    <>
      <ConfirmDialog
        open={isOpen}
        onOpenChange={(nextOpen) => !nextOpen && setOpen(null)}
        title={t('Reset subscription quota')}
        desc={t('Reset all active subscriptions under {{plan}}?', {
          plan: planLabel,
        })}
        confirmText={t('Reset quota')}
        handleConfirm={handleConfirm}
        disabled={!plan?.id}
        isLoading={resetting}
      >
        <label className='flex items-center justify-between gap-3 rounded-md border px-3 py-2 text-sm'>
          <span>{t('Advance next reset time')}</span>
          <Switch
            checked={advanceResetTime}
            onCheckedChange={(checked) => setAdvanceResetTime(!!checked)}
            aria-label={t('Advance next reset time')}
          />
        </label>
      </ConfirmDialog>

      <SecureVerificationDialog
        open={verificationOpen}
        onOpenChange={setVerificationOpen}
        methods={verificationMethods}
        state={verificationState}
        onVerify={async (method, code) => {
          await executeVerification(method, code)
        }}
        onCancel={cancelVerification}
        onCodeChange={setVerificationCode}
        onMethodChange={switchVerificationMethod}
      />
    </>
  )
}
