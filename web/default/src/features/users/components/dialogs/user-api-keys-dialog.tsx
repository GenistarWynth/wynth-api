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
import { useCallback, useEffect, useState } from 'react'
import { Loader2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import {
  Sheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { Button } from '@/components/ui/button'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { StatusBadge } from '@/components/status-badge'
import {
  SecureVerificationDialog,
  useSecureVerification,
} from '@/features/auth/secure-verification'
import type { ApiKey } from '@/features/keys/types'
import {
  getUserTokens,
  updateUserTokenStatus,
  deleteUserToken,
  fetchUserTokenKey,
} from '../../api'
import { UserTokenEditDrawer } from './user-token-edit-drawer'

interface UserApiKeysDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  user: { id: number; username: string }
  canRevealKey: boolean
}

function getTokenStatusVariant(
  status: number
): 'success' | 'neutral' | 'warning' | 'danger' {
  switch (status) {
    case 1:
      return 'success'
    case 2:
      return 'neutral'
    case 3:
      return 'warning'
    case 4:
      return 'danger'
    default:
      return 'neutral'
  }
}

function getTokenStatusLabel(status: number): string {
  switch (status) {
    case 1:
      return 'Enabled'
    case 2:
      return 'Disabled'
    case 3:
      return 'Expired'
    case 4:
      return 'Exhausted'
    default:
      return 'Unknown'
  }
}

export function UserApiKeysDialog({
  open,
  onOpenChange,
  user,
  canRevealKey,
}: UserApiKeysDialogProps) {
  const { t } = useTranslation()
  const [tokens, setTokens] = useState<ApiKey[]>([])
  const [loading, setLoading] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<ApiKey | null>(null)
  const [editTarget, setEditTarget] = useState<ApiKey | null>(null)
  const [togglingId, setTogglingId] = useState<number | null>(null)
  const [pendingRevealToken, setPendingRevealToken] = useState<ApiKey | null>(
    null
  )

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

  const load = useCallback(async () => {
    if (!open) return
    setLoading(true)
    try {
      const res = await getUserTokens(user.id, { p: 1, size: 50 })
      if (res.success) {
        setTokens(res.data?.items ?? [])
      } else {
        toast.error(res.message || t('Failed to load API keys'))
      }
    } catch {
      toast.error(t('An unexpected error occurred'))
    } finally {
      setLoading(false)
    }
  }, [open, user.id, t])

  useEffect(() => {
    void load()
  }, [load])

  const handleToggle = async (tok: ApiKey) => {
    if (togglingId !== null) return
    setTogglingId(tok.id)
    const next = tok.status === 1 ? 2 : 1
    try {
      const res = await updateUserTokenStatus(user.id, tok.id, next)
      if (res.success) {
        toast.success(t('Updated'))
        void load()
      } else {
        toast.error(res.message || t('Update failed'))
      }
    } catch {
      toast.error(t('An unexpected error occurred'))
    } finally {
      setTogglingId(null)
    }
  }

  const handleDelete = async () => {
    if (!deleteTarget) return
    try {
      const res = await deleteUserToken(user.id, deleteTarget.id)
      if (res.success) {
        toast.success(t('Deleted'))
        void load()
      } else {
        toast.error(res.message || t('Delete failed'))
      }
    } catch {
      toast.error(t('An unexpected error occurred'))
    } finally {
      setDeleteTarget(null)
    }
  }

  const doReveal = useCallback(
    async (tok: ApiKey) => {
      const res = await fetchUserTokenKey(user.id, tok.id)
      if (!res.success) {
        throw new Error(res.message || t('Failed to reveal key'))
      }
      const key = res.data?.key ?? ''
      await navigator.clipboard.writeText(key)
      toast.success(t('Full key copied to clipboard'))
      return res
    },
    [user.id, t]
  )

  const handleReveal = useCallback(
    async (tok: ApiKey) => {
      setPendingRevealToken(tok)
      try {
        await withVerification(() => doReveal(tok), {
          preferredMethod: 'passkey',
          title: t('Verify to view token key'),
          description: t(
            'Use Passkey or 2FA to confirm your identity before revealing this token key.'
          ),
        })
      } catch (error) {
        if (error instanceof Error) {
          toast.error(error.message)
        }
      } finally {
        setPendingRevealToken(null)
      }
    },
    [withVerification, doReveal, t]
  )

  return (
    <>
      <Sheet open={open} onOpenChange={onOpenChange}>
        <SheetContent className='w-full sm:max-w-2xl'>
          <SheetHeader>
            <SheetTitle>
              {t('API Keys of {{name}}', { name: user.username })}
            </SheetTitle>
          </SheetHeader>
          <div className='mt-4 space-y-2'>
            {loading && (
              <div className='flex items-center justify-center py-8'>
                <Loader2 className='text-muted-foreground h-6 w-6 animate-spin' />
              </div>
            )}
            {!loading && tokens.length === 0 && (
              <div className='text-muted-foreground py-8 text-center text-sm'>
                {t('No API keys')}
              </div>
            )}
            {tokens.map((tok) => (
              <div
                key={tok.id}
                className='flex items-center justify-between rounded border p-3 gap-2'
              >
                <div className='min-w-0 flex-1'>
                  <div className='truncate font-medium text-sm'>{tok.name}</div>
                  <code className='text-muted-foreground font-mono text-xs'>
                    {tok.key}
                  </code>
                </div>
                <div className='flex items-center gap-2 shrink-0'>
                  <StatusBadge
                    variant={getTokenStatusVariant(tok.status)}
                    label={t(getTokenStatusLabel(tok.status))}
                    copyable={false}
                  />
                  <Button
                    variant='ghost'
                    size='sm'
                    onClick={() => handleToggle(tok)}
                    disabled={
                      tok.status === 3 ||
                      tok.status === 4 ||
                      togglingId === tok.id
                    }
                  >
                    {tok.status === 1 ? t('Disable') : t('Enable')}
                  </Button>
                  {canRevealKey && (
                    <Button
                      variant='ghost'
                      size='sm'
                      disabled={pendingRevealToken?.id === tok.id}
                      onClick={() => handleReveal(tok)}
                    >
                      {pendingRevealToken?.id === tok.id ? (
                        <Loader2 className='h-3 w-3 animate-spin' />
                      ) : null}
                      {t('Reveal Full Key')}
                    </Button>
                  )}
                  <Button
                    variant='ghost'
                    size='sm'
                    onClick={() => setEditTarget(tok)}
                  >
                    {t('Edit')}
                  </Button>
                  <Button
                    variant='ghost'
                    size='sm'
                    className='text-destructive'
                    onClick={() => setDeleteTarget(tok)}
                  >
                    {t('Delete')}
                  </Button>
                </div>
              </div>
            ))}
          </div>
        </SheetContent>
      </Sheet>

      <UserTokenEditDrawer
        open={!!editTarget}
        onOpenChange={(o) => !o && setEditTarget(null)}
        token={editTarget}
        userId={user.id}
        onSuccess={() => void load()}
      />

      <ConfirmDialog
        open={!!deleteTarget}
        onOpenChange={(o) => !o && setDeleteTarget(null)}
        title={t('Delete API Key')}
        desc={t('Delete this API key? This cannot be undone.')}
        confirmText={t('Delete')}
        destructive
        handleConfirm={handleDelete}
      />

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
