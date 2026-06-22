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
import { type CSSProperties, useId } from 'react'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'

type HeroGlobeProps = {
  className?: string
}

const globeDots = [-66, -50, -34, -18, -2, 14, 30, 46, 62].flatMap(
  (latitude, rowIndex) => {
    const count = Math.round(
      18 + Math.cos((Math.abs(latitude) * Math.PI) / 180) * 26
    )
    return Array.from({ length: count }, (_, dotIndex) => ({
      latitude,
      longitude:
        (360 / count) * dotIndex + (rowIndex % 2 === 0 ? 0 : 360 / count / 2),
      key: `${latitude}-${dotIndex}`,
    }))
  }
)

const meridians = [-75, -50, -25, 0, 25, 50, 75]
const latitudes = [-56, -34, -12, 12, 34, 56]

const routes = [
  'M24 53 C36 32 59 31 75 45',
  'M28 67 C41 51 53 50 70 61',
  'M35 38 C45 45 54 51 63 35',
]

export function HeroGlobe(props: HeroGlobeProps) {
  const { t } = useTranslation()
  const gradientId = useId()
  const glowId = useId()

  return (
    <div
      className={cn(
        'hero-globe-stage relative mx-auto aspect-square w-full max-w-[42rem]',
        props.className
      )}
      role='img'
      aria-label={t('Global upstream routing illustration')}
    >
      <div aria-hidden className='hero-globe-halo' />
      <div aria-hidden className='hero-globe-shell'>
        <div className='hero-globe-sphere'>
          {meridians.map((angle) => (
            <span
              key={`meridian-${angle}`}
              className='hero-globe-ring hero-globe-meridian'
              style={
                {
                  '--globe-ring-transform': `translate(-50%, -50%) rotateY(${angle}deg)`,
                } as CSSProperties
              }
            />
          ))}
          {latitudes.map((latitude) => (
            <span
              key={`latitude-${latitude}`}
              className='hero-globe-ring hero-globe-latitude'
              style={
                {
                  '--globe-ring-transform': `translate(-50%, -50%) translateY(${(Math.sin((latitude * Math.PI) / 180) * 42).toFixed(1)}%) rotateX(78deg) scale(${Math.cos((Math.abs(latitude) * Math.PI) / 180).toFixed(2)})`,
                } as CSSProperties
              }
            />
          ))}
          {globeDots.map((dot) => (
            <span
              key={dot.key}
              className='hero-globe-dot'
              style={
                {
                  '--globe-dot-transform': `translate(-50%, -50%) rotateY(${dot.longitude}deg) rotateX(${dot.latitude}deg) translateZ(var(--globe-radius))`,
                } as CSSProperties
              }
            />
          ))}
        </div>

        <svg className='hero-globe-routes' viewBox='0 0 100 100'>
          <defs>
            <linearGradient id={gradientId} x1='0%' y1='0%' x2='100%' y2='0%'>
              <stop offset='0%' stopColor='currentColor' stopOpacity='0' />
              <stop offset='46%' stopColor='currentColor' stopOpacity='0.85' />
              <stop offset='100%' stopColor='currentColor' stopOpacity='0' />
            </linearGradient>
            <filter id={glowId} x='-30%' y='-30%' width='160%' height='160%'>
              <feGaussianBlur stdDeviation='1.2' result='blur' />
              <feMerge>
                <feMergeNode in='blur' />
                <feMergeNode in='SourceGraphic' />
              </feMerge>
            </filter>
          </defs>
          {routes.map((route, index) => (
            <path
              key={route}
              d={route}
              fill='none'
              stroke={`url(#${gradientId})`}
              strokeLinecap='round'
              strokeWidth='0.85'
              filter={`url(#${glowId})`}
              className='hero-globe-route'
              style={{ animationDelay: `${index * 1.2}s` }}
            />
          ))}
        </svg>

        <div className='hero-globe-orbit hero-globe-orbit-a'>
          <span />
        </div>
        <div className='hero-globe-orbit hero-globe-orbit-b'>
          <span />
        </div>
        <div className='hero-globe-orbit hero-globe-orbit-c'>
          <span />
        </div>
      </div>
    </div>
  )
}
