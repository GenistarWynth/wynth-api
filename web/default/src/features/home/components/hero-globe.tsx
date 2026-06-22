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
import { useEffect, useRef, useState } from 'react'
import createGlobe, { type Arc, type Marker } from 'cobe'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'

type HeroGlobeProps = {
  className?: string
}

const markers: Marker[] = [
  {
    id: 'hub-wynth',
    location: [37.7749, -122.4194],
    size: 0.075,
    color: [0.63, 0.25, 0.02],
  },
  {
    id: 'model-codex',
    location: [40.7128, -74.006],
    size: 0.035,
    color: [0.72, 0.35, 0.13],
  },
  {
    id: 'model-claude',
    location: [51.5072, -0.1276],
    size: 0.034,
    color: [0.72, 0.35, 0.13],
  },
  {
    id: 'model-gemini',
    location: [35.6762, 139.6503],
    size: 0.034,
    color: [0.72, 0.35, 0.13],
  },
]

const arcs: Arc[] = [
  {
    id: 'wynth-codex',
    from: [37.7749, -122.4194],
    to: [40.7128, -74.006],
    color: [0.72, 0.35, 0.13],
  },
  {
    id: 'wynth-claude',
    from: [37.7749, -122.4194],
    to: [51.5072, -0.1276],
    color: [0.72, 0.35, 0.13],
  },
  {
    id: 'wynth-gemini',
    from: [37.7749, -122.4194],
    to: [35.6762, 139.6503],
    color: [0.72, 0.35, 0.13],
  },
]

export function HeroGlobe(props: HeroGlobeProps) {
  const { t } = useTranslation()
  const canvasRef = useRef<HTMLCanvasElement | null>(null)
  const stageRef = useRef<HTMLDivElement | null>(null)
  const [size, setSize] = useState(0)

  useEffect(() => {
    const stage = stageRef.current
    if (!stage) {
      return
    }

    const updateSize = () => {
      const rect = stage.getBoundingClientRect()
      if (rect.width < 1 || rect.height < 1) {
        setSize(0)
        return
      }
      setSize(Math.max(280, Math.round(Math.min(rect.width, rect.height))))
    }

    updateSize()
    const observer = new ResizeObserver(updateSize)
    observer.observe(stage)

    return () => observer.disconnect()
  }, [])

  useEffect(() => {
    const canvas = canvasRef.current
    if (!canvas || size <= 0) {
      return
    }

    const reduceMotion = window.matchMedia(
      '(prefers-reduced-motion: reduce)'
    ).matches
    let phi = 4.25
    let frame = 0

    const globe = createGlobe(canvas, {
      width: size,
      height: size,
      phi,
      theta: 0.22,
      dark: 0,
      diffuse: 1.18,
      mapSamples: 22_000,
      mapBrightness: 6.8,
      mapBaseBrightness: 0.01,
      baseColor: [0.98, 0.955, 0.91],
      markerColor: [0.68, 0.28, 0.04],
      glowColor: [1, 0.965, 0.91],
      arcColor: [0.72, 0.35, 0.13],
      arcWidth: 0.42,
      arcHeight: 0.22,
      markerElevation: 0.024,
      markers,
      arcs,
      opacity: 0.98,
      scale: 1,
      devicePixelRatio: Math.min(window.devicePixelRatio || 1, 2),
    })

    const tick = () => {
      if (!reduceMotion) {
        phi += 0.0022
      }
      globe.update({ phi })
      frame = window.requestAnimationFrame(tick)
    }

    tick()

    return () => {
      window.cancelAnimationFrame(frame)
      globe.destroy()
    }
  }, [size])

  return (
    <div
      ref={stageRef}
      className={cn('hero-globe-stage', props.className)}
      role='img'
      aria-label={t('Global upstream routing illustration')}
    >
      <canvas
        ref={canvasRef}
        className='hero-globe-canvas'
        width={Math.max(size, 1)}
        height={Math.max(size, 1)}
      />
      <div className='hero-globe-label hero-globe-label-model hero-globe-label-codex'>
        Codex
      </div>
      <div className='hero-globe-label hero-globe-label-model hero-globe-label-claude'>
        Claude
      </div>
      <div className='hero-globe-label hero-globe-label-model hero-globe-label-gemini'>
        Gemini
      </div>
      <div className='hero-globe-label hero-globe-label-hub hero-globe-label-wynth'>
        Wynth API
      </div>
    </div>
  )
}
