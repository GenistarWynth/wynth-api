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
import {
  ArrowRight,
  BookOpen,
  CircleDollarSign,
  RefreshCcw,
} from 'lucide-react'
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

  const capabilities = [
    {
      icon: <CircleDollarSign className='size-5' />,
      title: t('Cost-aware routing'),
      description: t(
        'Channel ratios, priority tiers, and monitoring signals stay visible before traffic reaches expensive upstreams.'
      ),
    },
    {
      icon: <RefreshCcw className='size-5' />,
      title: t('Stable fallback'),
      description: t(
        'Retry within the current priority tier first, then fall back only when that tier is exhausted.'
      ),
    },
  ]

  const providerPills = [
    { initial: 'O', name: 'OpenAI', status: t('Supported') },
    { initial: 'C', name: 'Claude', status: t('Soon') },
    { initial: 'G', name: 'Gemini', status: t('Soon') },
    { initial: 'X', name: 'Codex', status: t('Supported') },
    { initial: '+', name: t('More'), status: t('Soon') },
  ]

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
        'bg-background relative z-10 flex min-h-[calc(100svh-4.5rem)] overflow-hidden px-6 pt-8 pb-4 sm:min-h-svh sm:pt-24 sm:pb-6',
        props.className
      )}
    >
      <div
        aria-hidden
        className='pointer-events-none absolute inset-0 -z-10 bg-[linear-gradient(to_right,color-mix(in_oklch,var(--border)_55%,transparent)_1px,transparent_1px),linear-gradient(to_bottom,color-mix(in_oklch,var(--border)_55%,transparent)_1px,transparent_1px)] bg-[size:4.5rem_4.5rem] opacity-45'
      />
      <div
        aria-hidden
        className='pointer-events-none absolute inset-y-0 left-0 -z-10 w-1/2 bg-[radial-gradient(circle_at_20%_78%,color-mix(in_oklch,var(--warning)_30%,transparent)_0%,transparent_44%)]'
      />
      <div
        aria-hidden
        className='pointer-events-none absolute inset-y-0 right-0 -z-10 w-1/2 bg-[radial-gradient(circle_at_70%_22%,color-mix(in_oklch,var(--info)_25%,transparent)_0%,transparent_48%)]'
      />

      <div className='mx-auto flex w-full max-w-6xl flex-col justify-between'>
        <div className='grid flex-1 items-center gap-6 lg:grid-cols-[0.95fr_1.05fr] lg:gap-14'>
          <div className='max-w-2xl text-left'>
            <div className='landing-animate-fade-up bg-primary/5 text-primary mb-4 inline-flex items-center rounded-full px-3 py-1.5 text-[11px] font-semibold uppercase opacity-0 sm:mb-5'>
              {t('UNIFIED AI GATEWAY')}
            </div>
            <h1
              className='landing-animate-fade-up max-w-[11ch] text-4xl leading-none font-semibold opacity-0 sm:text-6xl lg:text-7xl'
              style={{ animationDelay: '60ms' }}
            >
              {t('One screen for every upstream')}
            </h1>
            <p
              className='landing-animate-fade-up text-muted-foreground mt-4 max-w-xl text-sm leading-6 opacity-0 sm:mt-6 sm:text-base sm:leading-7'
              style={{ animationDelay: '120ms' }}
            >
              {t(
                'Aggregate new-api, sub2api, account pools, monitoring, and strict priority routing behind one OpenAI-compatible API.'
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
          </div>

          <div
            className='landing-animate-fade-up grid gap-4 opacity-0 lg:grid-cols-[0.95fr_1.05fr] lg:gap-5'
            style={{ animationDelay: '240ms' }}
          >
            <div className='flex items-center justify-center lg:justify-end'>
              <HeroGlobe className='max-w-[14.5rem] sm:max-w-[22rem] lg:max-w-[26rem]' />
            </div>

            <div className='hidden flex-col justify-center gap-4 md:flex'>
              <div className='grid gap-4 sm:grid-cols-2 lg:grid-cols-1'>
                {capabilities.map((item) => (
                  <div
                    key={item.title}
                    className='border-border/70 bg-card/80 rounded-2xl border p-5 shadow-[0_18px_50px_rgba(15,23,42,0.06)] backdrop-blur dark:shadow-[0_18px_50px_rgba(0,0,0,0.24)]'
                  >
                    <div className='bg-primary text-primary-foreground mb-4 flex size-11 items-center justify-center rounded-xl'>
                      {item.icon}
                    </div>
                    <h3 className='text-lg font-semibold'>{item.title}</h3>
                    <p className='text-muted-foreground mt-2 text-sm leading-6'>
                      {item.description}
                    </p>
                  </div>
                ))}
              </div>

              <div className='border-border/80 overflow-hidden rounded-2xl border bg-slate-950 text-slate-100 shadow-[0_22px_70px_rgba(15,23,42,0.22)]'>
                <div className='flex h-10 items-center gap-2 border-b border-white/10 px-4'>
                  <span className='size-3 rounded-full bg-red-400' />
                  <span className='size-3 rounded-full bg-amber-300' />
                  <span className='size-3 rounded-full bg-emerald-400' />
                  <span className='ml-auto font-mono text-xs text-slate-500'>
                    {t('terminal')}
                  </span>
                </div>
                <div className='space-y-2 px-5 py-4 font-mono text-xs leading-6'>
                  <p>
                    <span className='text-emerald-400'>$ </span>
                    <span className='text-cyan-300'>
                      {
                        'curl -X POST https://your-domain.example/v1/chat/completions'
                      }
                    </span>
                  </p>
                  <p className='text-slate-500'>{t('# Wynth API routing')}</p>
                  <p>
                    <span className='rounded bg-emerald-500/15 px-2 py-1 text-emerald-300'>
                      {t('200 OK')}
                    </span>
                    <span className='ml-2 text-amber-200'>
                      {'{ "route": "best healthy upstream" }'}
                    </span>
                  </p>
                </div>
              </div>
            </div>
          </div>
        </div>

        <div className='landing-animate-fade-up mt-8 hidden flex-wrap items-center justify-center gap-3 opacity-0 md:flex lg:mt-0'>
          {providerPills.map((provider) => (
            <div
              key={provider.name}
              className='border-border/70 bg-background/80 flex min-h-12 items-center gap-3 rounded-2xl border px-4 py-2 shadow-sm backdrop-blur'
            >
              <span className='bg-primary text-primary-foreground flex size-8 items-center justify-center rounded-lg text-sm font-semibold'>
                {provider.initial}
              </span>
              <span className='text-sm font-medium'>{provider.name}</span>
              <span className='bg-primary/10 text-primary rounded-md px-2 py-0.5 text-[11px]'>
                {provider.status}
              </span>
            </div>
          ))}
        </div>

        <div className='text-muted-foreground/60 mt-6 flex flex-wrap items-center justify-center gap-x-2 gap-y-1 text-xs'>
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
