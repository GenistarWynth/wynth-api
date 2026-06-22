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
import { Link } from '@tanstack/react-router'
import { ArrowRight, BookOpen } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { useStatus } from '@/hooks/use-status'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'
import { HeroGlobe } from '../hero-globe'

interface HeroProps {
  className?: string
  isAuthenticated?: boolean
}

export function Hero(props: HeroProps) {
  const { t } = useTranslation()
  const { status } = useStatus()
  const docsUrl =
    (status?.docs_link as string | undefined) || 'https://docs.newapi.pro'

  const renderDocsButton = () => {
    const isExternal = docsUrl.startsWith('http')
    if (isExternal) {
      return (
        <Button
          variant='outline'
          className='group border-border/50 hover:border-border hover:bg-muted/50 inline-flex h-11 items-center gap-1.5 rounded-lg px-5 text-sm font-medium'
          render={
            <a href={docsUrl} target='_blank' rel='noopener noreferrer' />
          }
        >
          <BookOpen className='text-muted-foreground/80 group-hover:text-foreground size-4 transition-colors duration-200' />
          <span>{t('Docs')}</span>
        </Button>
      )
    }
    return (
      <Button
        variant='outline'
        className='group border-border/50 hover:border-border hover:bg-muted/50 inline-flex h-11 items-center gap-1.5 rounded-lg px-5 text-sm font-medium'
        render={<Link to={docsUrl} />}
      >
        <BookOpen className='text-muted-foreground/80 group-hover:text-foreground size-4 transition-colors duration-200' />
        <span>{t('Docs')}</span>
      </Button>
    )
  }

  return (
    <section
      className={cn(
        'relative z-10 overflow-hidden bg-[linear-gradient(180deg,var(--background)_0%,color-mix(in_oklch,var(--muted)_55%,var(--background))_100%)] px-6 pt-28 pb-20 md:pt-[8.5rem] md:pb-28',
        props.className
      )}
    >
      <div className='pointer-events-none absolute inset-x-0 top-0 -z-10 h-40 bg-[radial-gradient(ellipse_at_top,var(--primary)_0%,transparent_65%)] opacity-[0.08] dark:opacity-[0.12]' />

      <div className='mx-auto grid max-w-6xl items-center gap-12 lg:grid-cols-[1.02fr_0.98fr]'>
        <div className='max-w-2xl text-left'>
          <div className='landing-animate-fade-up mb-5 inline-flex items-center rounded-full bg-primary/5 px-3 py-1.5 text-[11px] font-semibold text-primary uppercase opacity-0'>
            {t('THE UNIVERSAL AI GATEWAY')}
          </div>
          <h1
            className='landing-animate-fade-up max-w-[13ch] text-4xl leading-[1.04] font-semibold opacity-0 sm:text-5xl lg:text-6xl xl:text-7xl'
            style={{ animationDelay: '60ms' }}
          >
            {t('Connect every upstream through one AI gateway')}
          </h1>
          <p
            className='landing-animate-fade-up text-muted-foreground mt-6 max-w-xl text-base leading-7 opacity-0 md:text-lg'
            style={{ animationDelay: '120ms' }}
          >
            {t(
              'Aggregate new-api, sub2api, account pools, monitoring, and strict priority routing behind one OpenAI-compatible API.'
            )}
          </p>

          <div
            className='landing-animate-fade-up mt-8 flex flex-wrap items-center gap-3 opacity-0'
            style={{ animationDelay: '180ms' }}
          >
            {props.isAuthenticated ? (
              <>
                <Button
                  className='group h-11 rounded-lg px-5 text-sm font-medium'
                  render={<Link to='/dashboard' />}
                >
                  {t('Go to Dashboard')}
                  <ArrowRight className='ml-1.5 size-4 transition-transform duration-200 group-hover:translate-x-0.5' />
                </Button>
                {renderDocsButton()}
              </>
            ) : (
              <>
                <Button
                  className='group h-11 rounded-lg px-5 text-sm font-medium'
                  render={<Link to='/sign-up' />}
                >
                  {t('Get Started')}
                  <ArrowRight className='ml-1.5 size-4 transition-transform duration-200 group-hover:translate-x-0.5' />
                </Button>
                <Button
                  variant='outline'
                  className='border-border/50 hover:border-border hover:bg-muted/50 h-11 rounded-lg px-5 text-sm font-medium'
                  render={<Link to='/pricing' />}
                >
                  {t('View Pricing')}
                </Button>
                {renderDocsButton()}
              </>
            )}
          </div>
        </div>

        <div
          className='landing-animate-fade-up relative flex justify-center opacity-0'
          style={{ animationDelay: '240ms' }}
        >
          <HeroGlobe />
        </div>
      </div>
    </section>
  )
}
