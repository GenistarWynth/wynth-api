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
import {
  Key01Icon,
  LinkSquare02Icon,
  Refresh01Icon,
} from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { useMutation } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import {
  Field,
  FieldDescription,
  FieldGroup,
  FieldLabel,
  FieldLegend,
  FieldSet,
} from '@/components/ui/field'
import { Spinner } from '@/components/ui/spinner'
import { Textarea } from '@/components/ui/textarea'

import {
  exchangeAccountPoolXAIOAuthCode,
  generateAccountPoolXAIOAuthAuthorization,
  refreshAccountPoolXAIOAuthAccount,
} from '../api'
import type {
  AccountPoolXAIOAuthAuthorization,
  AccountPoolXAIOAuthTokenResult,
  ApiResponse,
} from '../types'

type XAIOAuthFlowProps = {
  poolID: number
  accountID?: number
  proxyID: number
  onAccountRefreshed: () => void
  onTokenResult: (result: AccountPoolXAIOAuthTokenResult) => void
}

function responseData<T>(result: ApiResponse<T>, fallback: string): T {
  if (!result.success || !result.data) {
    throw new Error(result.message || fallback)
  }
  return result.data
}

export function XAIOAuthFlow(props: XAIOAuthFlowProps) {
  const { t } = useTranslation()
  const [authorization, setAuthorization] =
    useState<AccountPoolXAIOAuthAuthorization>()
  const [authorizationInput, setAuthorizationInput] = useState('')
  const [credentialsLoaded, setCredentialsLoaded] = useState(false)

  const authorizeMutation = useMutation({
    mutationFn: async () => {
      const result = await generateAccountPoolXAIOAuthAuthorization(
        props.poolID,
        {
          proxy_id: props.proxyID,
        }
      )
      return responseData(result, t('Failed to start Grok OAuth'))
    },
  })

  const exchangeMutation = useMutation({
    mutationFn: async () => {
      if (!authorization) {
        throw new Error(t('Start Grok OAuth first'))
      }
      const result = await exchangeAccountPoolXAIOAuthCode(props.poolID, {
        session_id: authorization.session_id,
        code: authorizationInput.trim(),
      })
      return responseData(result, t('Failed to exchange Grok OAuth code'))
    },
    onSuccess: (result) => {
      props.onTokenResult(result)
      setAuthorization(undefined)
      setAuthorizationInput('')
      setCredentialsLoaded(true)
      toast.success(t('Grok OAuth credentials loaded'))
    },
    onError: (error) => {
      setAuthorization(undefined)
      setAuthorizationInput('')
      toast.error(error instanceof Error ? error.message : t('Request failed'))
    },
  })

  const refreshMutation = useMutation({
    mutationFn: async () => {
      if (!props.accountID) {
        throw new Error(t('Select an account first'))
      }
      const result = await refreshAccountPoolXAIOAuthAccount(
        props.poolID,
        props.accountID
      )
      return responseData(result, t('Failed to refresh Grok OAuth token'))
    },
    onSuccess: () => {
      props.onAccountRefreshed()
      toast.success(t('Grok OAuth token refreshed'))
    },
    onError: (error) => {
      toast.error(error instanceof Error ? error.message : t('Request failed'))
    },
  })

  const handleStartOAuth = () => {
    const popup = window.open('', '_blank')
    if (popup) popup.opener = null

    authorizeMutation.mutate(undefined, {
      onSuccess: (result) => {
        setAuthorization(result)
        setAuthorizationInput('')
        setCredentialsLoaded(false)
        if (popup && !popup.closed) popup.location.href = result.auth_url
        toast.success(t('Grok authorization page opened'))
      },
      onError: (error) => {
        if (popup) popup.close()
        toast.error(
          error instanceof Error ? error.message : t('Request failed')
        )
      },
    })
  }

  return (
    <FieldSet className='sm:col-span-2'>
      <FieldLegend>{t('Grok OAuth Login')}</FieldLegend>
      <FieldDescription>
        {t(
          'Authorize with X.AI, then paste the final callback URL or authorization code below. Credentials stay in this form until you save the account.'
        )}
      </FieldDescription>
      <FieldGroup>
        <div className='flex flex-wrap gap-2'>
          <Button
            type='button'
            variant='outline'
            disabled={authorizeMutation.isPending}
            onClick={handleStartOAuth}
          >
            {authorizeMutation.isPending ? (
              <Spinner data-icon='inline-start' />
            ) : (
              <HugeiconsIcon icon={Key01Icon} data-icon='inline-start' />
            )}
            {t('Start Grok OAuth')}
          </Button>
          {authorization ? (
            <Button
              variant='outline'
              render={
                <a
                  href={authorization.auth_url}
                  target='_blank'
                  rel='noreferrer'
                />
              }
              nativeButton={false}
            >
              <HugeiconsIcon icon={LinkSquare02Icon} data-icon='inline-start' />
              {t('Open authorization page')}
            </Button>
          ) : null}
          {props.accountID ? (
            <Button
              type='button'
              variant='outline'
              disabled={refreshMutation.isPending}
              onClick={() => refreshMutation.mutate()}
            >
              {refreshMutation.isPending ? (
                <Spinner data-icon='inline-start' />
              ) : (
                <HugeiconsIcon icon={Refresh01Icon} data-icon='inline-start' />
              )}
              {t('Refresh saved token')}
            </Button>
          ) : null}
        </div>
        {authorization ? (
          <Field>
            <FieldLabel htmlFor='account-pool-xai-oauth-code'>
              {t('Callback URL or authorization code')}
            </FieldLabel>
            <Textarea
              id='account-pool-xai-oauth-code'
              value={authorizationInput}
              onChange={(event) => setAuthorizationInput(event.target.value)}
              placeholder={t('Paste the full callback URL or code')}
            />
            <FieldDescription>
              {t(
                'The loopback callback page may not load; copy its full URL from the browser address bar.'
              )}
            </FieldDescription>
            <Button
              type='button'
              disabled={
                exchangeMutation.isPending || !authorizationInput.trim()
              }
              onClick={() => exchangeMutation.mutate()}
            >
              {exchangeMutation.isPending ? (
                <Spinner data-icon='inline-start' />
              ) : (
                <HugeiconsIcon icon={Key01Icon} data-icon='inline-start' />
              )}
              {t('Load OAuth credentials')}
            </Button>
          </Field>
        ) : null}
        {credentialsLoaded ? (
          <Alert>
            <AlertTitle>{t('OAuth credentials ready')}</AlertTitle>
            <AlertDescription>
              {t(
                'Review the account settings, then create or save the account.'
              )}
            </AlertDescription>
          </Alert>
        ) : null}
      </FieldGroup>
    </FieldSet>
  )
}
