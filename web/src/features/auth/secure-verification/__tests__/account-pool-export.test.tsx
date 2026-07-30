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
import { Window } from 'happy-dom'
import { afterAll, beforeAll, describe, expect, it, vi } from 'vitest'

import type { useSecureVerification as useSecureVerificationType } from '../hooks/use-secure-verification'

const domWindow = new Window()
const domGlobals = [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'Node',
  'Element',
  'Event',
  'CustomEvent',
  'MutationObserver',
  'requestAnimationFrame',
  'cancelAnimationFrame',
  'getComputedStyle',
] as const

for (const key of domGlobals) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: domWindow[key],
  })
}

const { act, useEffect } = await import('react')
const { createRoot } = await import('react-dom/client')
const verificationApi = await import('../api')
const { useSecureVerification } =
  await import('../hooks/use-secure-verification')

const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true

type VerificationHook = ReturnType<typeof useSecureVerificationType>

function HookHarness(props: { onRender: (value: VerificationHook) => void }) {
  const value = useSecureVerification()
  useEffect(() => props.onRender(value), [props, value])
  return null
}

describe('account-pool export secure verification', () => {
  beforeAll(() => {
    vi.spyOn(verificationApi, 'checkVerificationMethods').mockResolvedValue({
      has2FA: true,
      hasPasskey: true,
      passkeySupported: true,
    })
  })

  afterAll(() => {
    vi.restoreAllMocks()
    domWindow.close()
  })

  it('keeps a captured pool resource passkey-only through proof execution', async () => {
    const verify = vi.spyOn(verificationApi, 'verify').mockResolvedValue({
      proof_token: 'one-time-proof',
      expires_at: 123,
      method: 'passkey',
      scope: 'account_pool.credentials.export',
      resource: '42',
    })
    const exportRequest = vi.fn().mockResolvedValue({ success: true })
    const container = document.createElement('div')
    document.body.append(container)
    const root = createRoot(container)
    let current: VerificationHook | undefined

    await act(async () => {
      root.render(
        <HookHarness
          onRender={(value) => {
            current = value
          }}
        />
      )
    })
    expect(current).toBeDefined()

    let selectedPoolID = 42
    const poolID = selectedPoolID
    await act(async () => {
      await current?.startVerification(
        (proofToken) => exportRequest(poolID, proofToken),
        {
          scope: 'account_pool.credentials.export',
          resource: String(poolID),
          preferredMethod: 'passkey',
          allowedMethods: ['passkey'],
        }
      )
    })
    selectedPoolID = 99

    expect(current?.methods).toEqual({
      has2FA: false,
      hasPasskey: true,
      passkeySupported: true,
    })
    expect(current?.state.method).toBe('passkey')
    expect(current?.state.resource).toBe('42')

    await act(async () => {
      await current?.executeVerification()
    })

    expect(verify).toHaveBeenCalledWith(
      'passkey',
      'account_pool.credentials.export',
      '',
      '42'
    )
    expect(exportRequest).toHaveBeenCalledWith(42, 'one-time-proof')
    expect(selectedPoolID).toBe(99)

    await act(async () => root.unmount())
    container.remove()
  })
})
