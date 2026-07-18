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
import DOMPurify, { type Config } from 'dompurify'
import { useEffect, useMemo, useRef } from 'react'

import { cn } from '@/lib/utils'

import {
  ISOLATED_SANITIZE_OPTIONS,
  cloneApplicationStyleNodes,
  hardenEmbeddedFrame,
  hardenExternalLink,
  syncIsolatedTheme,
} from './html-content-policy'

export type HtmlContentVariant = 'inline' | 'isolated'

interface HtmlContentProps {
  content: string
  className?: string
  variant?: HtmlContentVariant
}

const isolatedContentBaseStyles = `
<style>
  :host {
    display: block;
    width: 100%;
    color: inherit;
    font: inherit;
  }

  *,
  *::before,
  *::after {
    box-sizing: border-box;
  }

  img,
  video,
  iframe {
    max-width: 100%;
  }

  iframe {
    border: 0;
  }
</style>
`

const isolatedSanitizeOptions = {
  ...ISOLATED_SANITIZE_OPTIONS,
} satisfies Config

function hardenIsolatedHtml(html: string): string {
  if (typeof document === 'undefined') {
    return html
  }

  const template = document.createElement('template')
  template.innerHTML = html

  template.content.querySelectorAll('a[target="_blank"]').forEach((link) => {
    hardenExternalLink(link)
  })

  template.content.querySelectorAll('iframe').forEach((frame) => {
    hardenEmbeddedFrame(frame)
  })

  return template.innerHTML
}

function sanitizeHtmlContent(
  content: string,
  variant: HtmlContentVariant
): string {
  if (variant === 'isolated') {
    const html = DOMPurify.sanitize(content, isolatedSanitizeOptions)

    return hardenIsolatedHtml(html)
  }

  return DOMPurify.sanitize(content)
}

function IsolatedHtmlContent(props: {
  className?: string
  html: string
}): React.ReactElement {
  const containerRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const container = containerRef.current
    if (!container) {
      return
    }

    const shadowRoot =
      container.shadowRoot ?? container.attachShadow({ mode: 'open' })
    const applicationStyleNodes = cloneApplicationStyleNodes<Node>(document)
    const wrapper = document.createElement('div')
    syncIsolatedTheme(wrapper, document.documentElement)
    wrapper.innerHTML = props.html

    const contentTemplate = document.createElement('template')
    contentTemplate.innerHTML = isolatedContentBaseStyles

    shadowRoot.replaceChildren(
      ...applicationStyleNodes,
      contentTemplate.content,
      wrapper
    )

    const observer = new MutationObserver(() =>
      syncIsolatedTheme(wrapper, document.documentElement)
    )
    observer.observe(document.documentElement, {
      attributes: true,
      attributeFilter: ['class'],
    })

    return () => observer.disconnect()
  }, [props.html])

  return (
    <div
      ref={containerRef}
      className={cn('block w-full', props.className)}
    />
  )
}

export function HtmlContent(props: HtmlContentProps) {
  const variant = props.variant ?? 'inline'
  const html = useMemo(
    () => sanitizeHtmlContent(props.content, variant),
    [props.content, variant]
  )

  if (variant === 'isolated') {
    return <IsolatedHtmlContent className={props.className} html={html} />
  }

  return (
    <div
      className={cn(
        'prose prose-neutral dark:prose-invert max-w-none',
        props.className
      )}
      // eslint-disable-next-line react/no-danger -- html is sanitized above
      dangerouslySetInnerHTML={{ __html: html }}
    />
  )
}
