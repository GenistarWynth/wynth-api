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
import { Add01Icon, Delete02Icon } from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { Button } from '@/components/ui/button'
import { deleteInvalidRedemptions } from '../api'
import { ERROR_MESSAGES } from '../constants'
import { useRedemptions } from './redemptions-provider'

export function RedemptionsPrimaryButtons() {
  const { t } = useTranslation()
  const { setOpen, triggerRefresh } = useRedemptions()
  const [showDeleteInvalidConfirm, setShowDeleteInvalidConfirm] =
    useState(false)
  const [isDeleting, setIsDeleting] = useState(false)

  const handleDeleteInvalid = async () => {
    setIsDeleting(true)
    try {
      const result = await deleteInvalidRedemptions()
      if (!result.success) {
        toast.error(result.message || t(ERROR_MESSAGES.DELETE_INVALID_FAILED))
        return
      }

      toast.success(
        t('Successfully deleted {{count}} invalid redemption codes', {
          count: result.data || 0,
        })
      )
      triggerRefresh()
      setShowDeleteInvalidConfirm(false)
    } catch {
      toast.error(t(ERROR_MESSAGES.DELETE_INVALID_FAILED))
    } finally {
      setIsDeleting(false)
    }
  }

  return (
    <>
      <div className='flex flex-wrap gap-2'>
        <Button
          size='sm'
          variant='outline'
          onClick={() => setShowDeleteInvalidConfirm(true)}
        >
          <HugeiconsIcon
            icon={Delete02Icon}
            data-icon='inline-start'
            className='text-destructive'
          />
          {t('Delete Invalid')}
        </Button>
        <Button size='sm' onClick={() => setOpen('create')}>
          <HugeiconsIcon icon={Add01Icon} data-icon='inline-start' />
          {t('Create Code')}
        </Button>
      </div>

      <ConfirmDialog
        destructive
        open={showDeleteInvalidConfirm}
        onOpenChange={setShowDeleteInvalidConfirm}
        handleConfirm={handleDeleteInvalid}
        isLoading={isDeleting}
        className='max-w-md'
        title={t('Delete Invalid Redemption Codes?')}
        desc={
          <>
            {t('This will delete all')} <strong>{t('used')}</strong>,{' '}
            <strong>{t('disabled')}</strong>
            {t(', and')} <strong>{t('expired')}</strong>{' '}
            {t('redemption codes.')}
            <br />
            {t('This action cannot be undone.')}
          </>
        }
        confirmText={t('Delete Invalid')}
      />
    </>
  )
}
