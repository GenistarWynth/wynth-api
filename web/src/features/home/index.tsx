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
import { useCallback, useEffect, useRef } from 'react'
import { useTranslation } from 'react-i18next'

import { PublicLayout } from '@/components/layout'
import { RichContent } from '@/components/rich-content'
import { useTheme } from '@/context/theme-provider'
import { isLikelyHtml } from '@/lib/content-format'
import { useAuthStore } from '@/stores/auth-store'

import { Hero } from './components'
import { useHomePageContent } from './hooks'
import {
  HOME_IFRAME_SANDBOX,
  postHomeIframePreferences,
} from './lib/home-iframe'

export function Home() {
  const { i18n, t } = useTranslation()
  const iframeRef = useRef<HTMLIFrameElement>(null)
  const { resolvedTheme } = useTheme()
  const { auth } = useAuthStore()
  const isAuthenticated = !!auth.user
  const { content, isLoaded, isUrl } = useHomePageContent()

  const syncIframePreferences = useCallback(() => {
    postHomeIframePreferences(
      iframeRef.current?.contentWindow,
      resolvedTheme,
      i18n.language
    )
  }, [i18n.language, resolvedTheme])

  useEffect(() => {
    if (isUrl) {
      syncIframePreferences()
    }
  }, [isUrl, syncIframePreferences])

  if (!isLoaded) {
    return (
      <PublicLayout showMainContainer={false}>
        <main className='flex min-h-screen items-center justify-center'>
          <div className='text-muted-foreground'>{t('Loading...')}</div>
        </main>
      </PublicLayout>
    )
  }

  if (content) {
    if (isUrl) {
      return (
        <PublicLayout showMainContainer={false}>
          <main className='overflow-x-hidden'>
            <iframe
              ref={iframeRef}
              src={content}
              className='h-screen w-full border-none'
              title={t('Custom Home Page')}
              sandbox={HOME_IFRAME_SANDBOX}
              onLoad={syncIframePreferences}
            />
          </main>
        </PublicLayout>
      )
    }

    if (isLikelyHtml(content)) {
      return (
        <PublicLayout showMainContainer={false}>
          <main className='overflow-x-hidden'>
            <RichContent
              mode='html'
              htmlVariant='isolated'
              content={content}
              className='custom-home-content'
            />
          </main>
        </PublicLayout>
      )
    }

    return (
      <PublicLayout showMainContainer={false}>
        <main className='overflow-x-hidden'>
          <div className='container mx-auto py-8'>
            <RichContent
              mode='markdown'
              content={content}
              className='custom-home-content'
            />
          </div>
        </main>
      </PublicLayout>
    )
  }

  return (
    <PublicLayout showMainContainer={false}>
      <Hero isAuthenticated={isAuthenticated} />
    </PublicLayout>
  )
}
