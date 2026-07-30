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
  parseChannelConnectionInfo,
  type ChannelConnectionInfo,
} from '@/lib/channel-connection-info'

type ClipboardReader = {
  readText?: () => Promise<string>
}

type ChannelConnectionFeedback = {
  onFilled: () => void
  onUnableToRead: () => void
  onConnectionInfoNotFound: () => void
}

type ChannelConnectionFieldSetter = (
  field: 'key' | 'base_url',
  value: string,
  options: { shouldDirty: true; shouldValidate: true }
) => void

type ChannelConnectionPasteOutcome =
  | 'filled'
  | 'unavailable'
  | 'not-found'
  | 'cancelled'

export type ChannelConnectionClipboardController = {
  setMode: (open: boolean, editing: boolean) => Promise<void>
  paste: (
    handlers: ChannelConnectionFeedback
  ) => Promise<ChannelConnectionPasteOutcome>
  fill: (onFilled: () => void) => boolean
  ignore: () => void
  close: () => void
  deactivate: () => void
}

function getBrowserClipboard(): ClipboardReader | null {
  try {
    if (typeof navigator === 'undefined') return null
    return navigator.clipboard
  } catch {
    return null
  }
}

function resolveClipboardReader(
  reader: ClipboardReader | null | undefined
): ClipboardReader | null {
  return reader === undefined ? getBrowserClipboard() : reader
}

export function createChannelConnectionClipboardController(options: {
  clipboard?: ClipboardReader | null
  onDetectedChange: (connectionInfo: ChannelConnectionInfo | null) => void
  setValue: ChannelConnectionFieldSetter
}): ChannelConnectionClipboardController {
  let detectedConnectionInfo: ChannelConnectionInfo | null = null
  let isOpen = false
  let isEditing = false
  let requestGeneration = 0

  const updateDetectedConnectionInfo = (
    connectionInfo: ChannelConnectionInfo | null
  ) => {
    detectedConnectionInfo = connectionInfo
    options.onDetectedChange(connectionInfo)
  }

  const clear = () => {
    requestGeneration += 1
    updateDetectedConnectionInfo(null)
  }

  const isActiveRequest = (generation: number) =>
    isOpen && !isEditing && generation === requestGeneration

  const applyConnectionInfo = (
    connectionInfo: ChannelConnectionInfo,
    onFilled: () => void
  ) => {
    if (!isOpen || isEditing) return false
    clear()
    const setValueOptions = {
      shouldDirty: true,
      shouldValidate: true,
    } as const
    options.setValue('key', connectionInfo.key, setValueOptions)
    options.setValue('base_url', connectionInfo.url, setValueOptions)
    onFilled()
    return true
  }

  return {
    async setMode(open: boolean, editing: boolean): Promise<void> {
      isOpen = open
      isEditing = editing
      clear()
      if (!isOpen || isEditing) return

      const clipboard = resolveClipboardReader(options.clipboard)
      if (!clipboard?.readText) return
      const generation = requestGeneration

      try {
        const text = await clipboard.readText()
        if (!isActiveRequest(generation)) return
        updateDetectedConnectionInfo(parseChannelConnectionInfo(text))
      } catch {
        /* Automatic detection is best-effort and intentionally silent. */
      }
    },

    async paste(
      handlers: ChannelConnectionFeedback
    ): Promise<ChannelConnectionPasteOutcome> {
      if (!isOpen || isEditing) return 'cancelled'
      clear()
      const generation = requestGeneration
      const clipboard = resolveClipboardReader(options.clipboard)
      if (!clipboard?.readText) {
        if (!isActiveRequest(generation)) return 'cancelled'
        handlers.onUnableToRead()
        return 'unavailable'
      }

      let text: string
      try {
        text = await clipboard.readText()
      } catch {
        if (!isActiveRequest(generation)) return 'cancelled'
        handlers.onUnableToRead()
        return 'unavailable'
      }

      if (!isActiveRequest(generation)) return 'cancelled'
      const connectionInfo = parseChannelConnectionInfo(text)
      if (!connectionInfo) {
        handlers.onConnectionInfoNotFound()
        return 'not-found'
      }

      return applyConnectionInfo(connectionInfo, handlers.onFilled)
        ? 'filled'
        : 'cancelled'
    },

    fill(onFilled: () => void): boolean {
      if (!detectedConnectionInfo) return false
      return applyConnectionInfo(detectedConnectionInfo, onFilled)
    },

    ignore(): void {
      clear()
    },

    close(): void {
      isOpen = false
      isEditing = false
      clear()
    },

    deactivate(): void {
      isOpen = false
      isEditing = false
      clear()
    },
  }
}
