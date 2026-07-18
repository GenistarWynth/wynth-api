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
import type { Config } from 'dompurify'

export const ISOLATED_CONTENT_SANDBOX =
  'allow-forms allow-popups allow-popups-to-escape-sandbox allow-presentation'

export const ISOLATED_SANITIZE_OPTIONS = {
  ADD_ATTR: [
    'allowfullscreen',
    'autoplay',
    'class',
    'controls',
    'default',
    'id',
    'kind',
    'label',
    'loading',
    'loop',
    'muted',
    'playsinline',
    'poster',
    'preload',
    'referrerpolicy',
    'rel',
    'src',
    'srclang',
    'style',
    'target',
  ],
  ADD_TAGS: ['audio', 'iframe', 'picture', 'source', 'style', 'track', 'video'],
  FORBID_ATTR: ['srcdoc'],
  FORBID_TAGS: ['base', 'embed', 'link', 'meta', 'object', 'script'],
  FORCE_BODY: true,
} satisfies Config

type MutableHtmlElement = {
  getAttribute(name: string): string | null
  hasAttribute(name: string): boolean
  removeAttribute(name: string): void
  setAttribute(name: string, value: string): void
}

export function hardenExternalLink(link: MutableHtmlElement): void {
  const rel = new Set(
    link
      .getAttribute('rel')
      ?.split(/\s+/)
      .filter(Boolean) ?? []
  )

  rel.add('noopener')
  rel.add('noreferrer')
  link.setAttribute('rel', [...rel].join(' '))
}

export function hardenEmbeddedFrame(frame: MutableHtmlElement): void {
  frame.removeAttribute('srcdoc')
  frame.setAttribute('sandbox', ISOLATED_CONTENT_SANDBOX)
  frame.setAttribute('referrerpolicy', 'no-referrer')

  if (!frame.hasAttribute('loading')) {
    frame.setAttribute('loading', 'lazy')
  }
}

type CloneableNode<T> = {
  cloneNode(deep?: boolean): T
}

type StyleDocumentLike<T> = {
  head: {
    querySelectorAll(selector: string): Iterable<CloneableNode<T>>
  }
}

export function cloneApplicationStyleNodes<T>(
  documentLike: StyleDocumentLike<T>
): T[] {
  return [
    ...documentLike.head.querySelectorAll('style, link[rel="stylesheet"]'),
  ].map((node) => node.cloneNode(true))
}

type ClassListLike = {
  toggle(name: string, force?: boolean): void
}

type DocumentClassListLike = {
  contains(name: string): boolean
}

export function syncIsolatedTheme(
  wrapper: { classList: ClassListLike },
  documentElement: { classList: DocumentClassListLike }
): void {
  wrapper.classList.toggle(
    'dark',
    documentElement.classList.contains('dark')
  )
}
