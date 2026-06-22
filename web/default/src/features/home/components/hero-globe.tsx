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
import { useId } from 'react'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'

type HeroGlobeProps = {
  className?: string
}

const nodes = [
  { label: 'OpenAI', x: 31, y: 30 },
  { label: 'Claude', x: 68, y: 25 },
  { label: 'Gemini', x: 76, y: 42 },
  { label: 'Codex', x: 54, y: 19 },
  { label: 'Users', x: 23, y: 68 },
]

export function HeroGlobe(props: HeroGlobeProps) {
  const { t } = useTranslation()
  const patternId = useId()
  const clipPathId = useId()

  return (
    <div
      className={cn(
        'relative mx-auto aspect-square w-full max-w-[25rem] md:max-w-[28rem]',
        props.className
      )}
      role='img'
      aria-label={t('Global upstream routing illustration')}
    >
      <div className='absolute inset-[8%] rounded-full bg-[radial-gradient(circle_at_35%_35%,var(--card)_0%,var(--muted)_42%,transparent_72%)] shadow-[inset_-24px_-28px_56px_rgba(15,23,42,0.08),0_28px_80px_rgba(15,23,42,0.08)] dark:shadow-[inset_-24px_-28px_56px_rgba(0,0,0,0.28),0_28px_80px_rgba(0,0,0,0.22)]' />
      <div className='absolute inset-[13%] rounded-full border border-primary/10 bg-[linear-gradient(135deg,var(--background)_0%,transparent_52%)] opacity-80' />
      <svg
        className='absolute inset-0 size-full text-primary/45 dark:text-primary/35'
        viewBox='0 0 100 100'
        aria-hidden='true'
      >
        <defs>
          <pattern
            id={patternId}
            width='4'
            height='4'
            patternUnits='userSpaceOnUse'
          >
            <circle cx='1' cy='1' r='0.35' fill='currentColor' />
          </pattern>
          <clipPath id={clipPathId}>
            <circle cx='50' cy='50' r='39' />
          </clipPath>
        </defs>
        <circle
          cx='50'
          cy='50'
          r='39'
          fill={`url(#${patternId})`}
          clipPath={`url(#${clipPathId})`}
        />
        <ellipse
          cx='50'
          cy='50'
          rx='38'
          ry='12'
          fill='none'
          stroke='currentColor'
          strokeWidth='0.35'
          opacity='0.35'
        />
        <ellipse
          cx='50'
          cy='50'
          rx='24'
          ry='38'
          fill='none'
          stroke='currentColor'
          strokeWidth='0.35'
          opacity='0.3'
        />
        <ellipse
          cx='50'
          cy='50'
          rx='12'
          ry='38'
          fill='none'
          stroke='currentColor'
          strokeWidth='0.3'
          opacity='0.22'
        />
        <path
          d='M23 68 C35 58 39 52 50 50 C60 47 69 33 76 42'
          fill='none'
          stroke='currentColor'
          strokeWidth='0.55'
          strokeDasharray='1.5 1.8'
        />
        <path
          d='M31 30 C38 35 43 43 50 50 C57 43 62 31 68 25'
          fill='none'
          stroke='currentColor'
          strokeWidth='0.55'
          strokeDasharray='1.5 1.8'
        />
        <path
          d='M54 19 C53 31 52 42 50 50 C45 55 34 62 23 68'
          fill='none'
          stroke='currentColor'
          strokeWidth='0.45'
          strokeDasharray='1.2 1.8'
          opacity='0.8'
        />
        <circle cx='50' cy='50' r='2.8' fill='currentColor' opacity='0.22' />
      </svg>
      <div className='absolute top-1/2 left-1/2 flex -translate-x-1/2 -translate-y-1/2 items-center rounded-full border border-primary/20 bg-background/85 px-3 py-1 text-xs font-semibold text-primary shadow-sm backdrop-blur'>
        {t('Wynth API')}
      </div>
      {nodes.map((node) => (
        <div
          key={node.label}
          className='absolute -translate-x-1/2 -translate-y-1/2 rounded-full border border-border/70 bg-background/90 px-2.5 py-1 text-[11px] font-medium text-foreground shadow-sm backdrop-blur'
          style={{ left: `${node.x}%`, top: `${node.y}%` }}
        >
          {t(node.label)}
        </div>
      ))}
    </div>
  )
}
