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
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'

import { SettingsSection } from '../../components/settings-section'
import { buildOAuthCallbackUrl } from '../oauth-callback-url'
import { ProviderFormDialog } from './components/provider-form-dialog'
import { ProviderTable } from './components/provider-table'
import { useCustomOAuthProviders } from './hooks/use-custom-oauth-providers'
import type { CustomOAuthProvider } from './types'

type CustomOAuthSectionProps = {
  serverAddress: string
}

export function CustomOAuthSection(props: CustomOAuthSectionProps) {
  const { t } = useTranslation()
  const { data: providers = [], isLoading } = useCustomOAuthProviders()
  const [dialogOpen, setDialogOpen] = useState(false)
  const [editingProvider, setEditingProvider] =
    useState<CustomOAuthProvider | null>(null)
  const callbackUrl = buildOAuthCallbackUrl(
    props.serverAddress,
    '{slug}',
    t('Site URL')
  )

  const handleCreate = () => {
    setEditingProvider(null)
    setDialogOpen(true)
  }

  const handleEdit = (provider: CustomOAuthProvider) => {
    setEditingProvider(provider)
    setDialogOpen(true)
  }

  const handleDialogChange = (open: boolean) => {
    setDialogOpen(open)
    if (!open) {
      setEditingProvider(null)
    }
  }

  if (isLoading) {
    return (
      <SettingsSection title={t('Custom OAuth Providers')}>
        <div className='text-muted-foreground py-8 text-center text-sm'>
          {t('Loading...')}
        </div>
      </SettingsSection>
    )
  }

  return (
    <SettingsSection title={t('Custom OAuth Providers')}>
      <Alert>
        <AlertTitle>{t('Callback URL format')}</AlertTitle>
        <AlertDescription className='flex flex-col gap-2 text-left'>
          <p>
            {t(
              'Use this callback URL pattern when registering a custom OAuth provider.'
            )}
          </p>
          <div className='bg-muted flex min-w-0 items-center gap-2 rounded-md px-2 py-1.5'>
            <code className='min-w-0 flex-1 text-xs break-all'>
              {callbackUrl}
            </code>
            <CopyButton
              value={callbackUrl}
              tooltip={t('Copy callback URL')}
              aria-label={t('Copy callback URL')}
            />
          </div>
        </AlertDescription>
      </Alert>

      <ProviderTable
        providers={providers}
        onEdit={handleEdit}
        onCreate={handleCreate}
      />

      <ProviderFormDialog
        open={dialogOpen}
        onOpenChange={handleDialogChange}
        provider={editingProvider}
        serverAddress={props.serverAddress}
      />
    </SettingsSection>
  )
}
