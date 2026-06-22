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
import { cn } from '@/lib/utils'
import { useStatus } from '@/hooks/use-status'
import { Button } from '@/components/ui/button'
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
  const currentYear = new Date().getFullYear()

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
        'bg-background relative z-10 flex min-h-[calc(100svh-4.5rem)] overflow-hidden px-0 pt-8 pb-4 sm:min-h-svh sm:pt-20 sm:pb-6',
        props.className
      )}
    >
      <div
        aria-hidden
        className='pointer-events-none absolute inset-0 -z-10 bg-[linear-gradient(to_right,color-mix(in_oklch,var(--border)_36%,transparent)_1px,transparent_1px),linear-gradient(to_bottom,color-mix(in_oklch,var(--border)_36%,transparent)_1px,transparent_1px)] bg-[size:4.5rem_4.5rem] opacity-20'
      />
      <div
        aria-hidden
        className='pointer-events-none absolute inset-y-0 left-0 -z-10 w-3/5 bg-[radial-gradient(circle_at_16%_82%,color-mix(in_oklch,var(--warning)_26%,transparent)_0%,transparent_42%)]'
      />
      <div
        aria-hidden
        className='pointer-events-none absolute inset-y-0 right-0 -z-10 w-3/5 bg-[radial-gradient(circle_at_68%_36%,color-mix(in_oklch,var(--muted)_70%,transparent)_0%,transparent_46%)]'
      />

      <div className='relative flex w-full flex-1 flex-col justify-between'>
        <div
          className='landing-animate-fade-up pointer-events-none absolute inset-y-0 right-[clamp(1rem,4vw,4rem)] z-0 hidden items-center justify-end opacity-0 xl:flex 2xl:right-[clamp(2rem,3vw,5rem)]'
          style={{ animationDelay: '240ms' }}
        >
          <HeroGlobe className='w-[min(58vw,calc(100svh-6rem),54rem)] 2xl:w-[min(60vw,calc(100svh-6rem),58rem)]' />
        </div>

        <div className='relative z-10 flex flex-1 items-center px-6 sm:px-10 lg:px-[clamp(3rem,7vw,10rem)] xl:pr-[52vw]'>
          <div className='max-w-2xl text-left'>
            <div className='landing-animate-fade-up bg-primary/5 text-primary mb-4 inline-flex items-center rounded-full px-3 py-1.5 text-[11px] font-semibold uppercase opacity-0 sm:mb-5'>
              {t('AI Gateway Control Plane')}
            </div>
            <h1
              className='landing-animate-fade-up text-4xl leading-none font-semibold opacity-0 sm:text-6xl lg:text-7xl'
              style={{ animationDelay: '60ms' }}
            >
              <span className='block'>{t('Upstream aggregation')}</span>
              <span className='block'>{t('Dependable capacity')}</span>
            </h1>
            <p
              className='landing-animate-fade-up text-muted-foreground mt-4 max-w-xl text-sm leading-6 opacity-0 sm:mt-6 sm:text-base sm:leading-7'
              style={{ animationDelay: '120ms' }}
            >
              {t(
                'Unify new-api, sub2api, account pools, monitoring, and priority orchestration into one OpenAI-compatible control plane.'
              )}
            </p>

            <div
              className='landing-animate-fade-up mt-6 flex flex-wrap items-center gap-2 opacity-0 sm:mt-8 sm:gap-3'
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
                    className='border-border/50 hover:border-border hover:bg-muted/50 hidden h-11 rounded-lg px-5 text-sm font-medium sm:inline-flex'
                    render={<Link to='/pricing' />}
                  >
                    {t('View Pricing')}
                  </Button>
                  {renderDocsButton()}
                </>
              )}
            </div>

            <div
              className='landing-animate-fade-up relative mt-8 flex min-h-[17rem] items-center justify-center opacity-0 sm:min-h-[26rem] xl:hidden'
              style={{ animationDelay: '240ms' }}
            >
              <HeroGlobe className='w-[min(86vw,24rem)] sm:w-[min(76vw,36rem)] lg:w-[min(56vw,40rem)]' />
            </div>
          </div>
        </div>

        <div className='text-muted-foreground/60 relative z-10 mt-6 flex flex-wrap items-center justify-center gap-x-2 gap-y-1 px-6 text-xs sm:px-10'>
          <span>&copy; {currentYear} Wynth API.</span>
          <span>{t('Project attribution')}:</span>
          <a
            href='https://github.com/QuantumNous/new-api'
            target='_blank'
            rel='noopener noreferrer'
            className='text-foreground/70 hover:text-foreground font-medium transition-colors'
          >
            {t('New API')}
          </a>
          <span>{t('by QuantumNous')}</span>
        </div>
      </div>
    </section>
  )
}
