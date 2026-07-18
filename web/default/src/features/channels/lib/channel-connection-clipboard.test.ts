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
import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import { createFormControl } from 'react-hook-form'

import { encodeChannelConnectionInfo } from '@/lib/channel-connection-info'

import { createChannelConnectionClipboardController } from './channel-connection-clipboard'

const validConnectionInfo = {
  key: 'sk-clipboard',
  url: 'https://clipboard.example.com',
}

describe('channel drawer connection clipboard behavior', () => {
  test('the production lifecycle detects only while the create drawer is open', async () => {
    let reads = 0
    let detected: typeof validConnectionInfo | null = null
    const controller = createChannelConnectionClipboardController({
      clipboard: {
        async readText() {
          reads += 1
          return encodeChannelConnectionInfo(
            validConnectionInfo.key,
            validConnectionInfo.url
          )
        },
      },
      onDetectedChange(connectionInfo) {
        detected = connectionInfo
      },
      setValue() {
        assert.fail('automatic detection must not write the form')
      },
    })

    await controller.setMode(false, false)
    assert.equal(reads, 0)
    await controller.setMode(true, true)
    assert.equal(reads, 0)
    await controller.setMode(true, false)
    assert.equal(reads, 1)
    assert.deepEqual(detected, validConnectionInfo)
    await controller.setMode(true, true)
    assert.equal(reads, 1)
    assert.equal(detected, null)
    controller.deactivate()
  })

  test('StrictMode setup-cleanup-setup keeps the controller reusable and cancels stale reads', async () => {
    const replayConnectionInfo = {
      key: 'sk-replay',
      url: 'https://replay.example.com',
    }
    const pastedConnectionInfo = {
      key: 'sk-pasted',
      url: 'https://pasted.example.com',
    }
    let resolveFirstDetection: ((text: string) => void) | undefined
    const firstDetection = new Promise<string>((resolve) => {
      resolveFirstDetection = resolve
    })
    let resolvePendingPaste: ((text: string) => void) | undefined
    const pendingPasteText = new Promise<string>((resolve) => {
      resolvePendingPaste = resolve
    })
    let reads = 0
    const form = createFormControl({
      defaultValues: { key: '', base_url: '' },
    })
    const unsubscribe = form.subscribe({
      formState: { values: true, dirtyFields: true },
      callback() {},
    })
    form.register('key')
    form.register('base_url')
    let detected: typeof replayConnectionInfo | null = null
    const feedback: string[] = []
    const controller = createChannelConnectionClipboardController({
      clipboard: {
        readText() {
          reads += 1
          if (reads === 1) return firstDetection
          if (reads === 2) {
            return Promise.resolve(
              encodeChannelConnectionInfo(
                replayConnectionInfo.key,
                replayConnectionInfo.url
              )
            )
          }
          if (reads === 3) {
            return Promise.resolve(
              encodeChannelConnectionInfo(
                pastedConnectionInfo.key,
                pastedConnectionInfo.url
              )
            )
          }
          if (reads === 4) return pendingPasteText
          return Promise.resolve('{}')
        },
      },
      onDetectedChange(connectionInfo) {
        detected = connectionInfo
      },
      setValue: (field, value, options) => form.setValue(field, value, options),
    })

    const firstSetup = controller.setMode(true, false)
    controller.deactivate()
    await controller.setMode(true, false)

    assert.equal(reads, 2)
    assert.deepEqual(detected, replayConnectionInfo)
    resolveFirstDetection?.(
      encodeChannelConnectionInfo(
        validConnectionInfo.key,
        validConnectionInfo.url
      )
    )
    await firstSetup
    assert.deepEqual(detected, replayConnectionInfo)

    assert.equal(
      controller.fill(() => feedback.push('filled-after-replay')),
      true
    )
    assert.deepEqual(form.getValues(), {
      key: replayConnectionInfo.key,
      base_url: replayConnectionInfo.url,
    })

    assert.equal(
      await controller.paste({
        onFilled: () => feedback.push('pasted-after-replay'),
        onUnableToRead: () => feedback.push('unavailable'),
        onConnectionInfoNotFound: () => feedback.push('not-found'),
      }),
      'filled'
    )
    assert.deepEqual(form.getValues(), {
      key: pastedConnectionInfo.key,
      base_url: pastedConnectionInfo.url,
    })

    const pendingPaste = controller.paste({
      onFilled: () => feedback.push('stale-filled'),
      onUnableToRead: () => feedback.push('stale-unavailable'),
      onConnectionInfoNotFound: () => feedback.push('stale-not-found'),
    })
    controller.deactivate()
    await controller.setMode(true, false)
    resolvePendingPaste?.(
      encodeChannelConnectionInfo(
        validConnectionInfo.key,
        validConnectionInfo.url
      )
    )

    assert.equal(await pendingPaste, 'cancelled')
    assert.deepEqual(form.getValues(), {
      key: pastedConnectionInfo.key,
      base_url: pastedConnectionInfo.url,
    })
    assert.deepEqual(feedback, ['filled-after-replay', 'pasted-after-replay'])
    controller.deactivate()
    unsubscribe()
  })

  test('Fill updates the real form as dirty and revalidates both connection fields', async () => {
    const form = createFormControl({
      mode: 'onChange',
      defaultValues: { key: 'invalid', base_url: 'invalid' },
    })
    const unsubscribe = form.subscribe({
      formState: {
        values: true,
        dirtyFields: true,
        errors: true,
        isValid: true,
      },
      callback() {},
    })
    form.register('key', {
      validate: (value) => value.startsWith('sk-') || 'invalid key',
    })
    form.register('base_url', {
      validate: (value) => value.startsWith('https://') || 'invalid URL',
    })
    await form.trigger()
    assert.equal(form.getFieldState('key').invalid, true)
    assert.equal(form.getFieldState('base_url').invalid, true)

    let detected: typeof validConnectionInfo | null = null
    let filled = 0
    const controller = createChannelConnectionClipboardController({
      clipboard: {
        async readText() {
          return encodeChannelConnectionInfo(
            validConnectionInfo.key,
            validConnectionInfo.url
          )
        },
      },
      onDetectedChange(connectionInfo) {
        detected = connectionInfo
      },
      setValue: (field, value, options) => form.setValue(field, value, options),
    })

    await controller.setMode(true, false)
    assert.deepEqual(detected, validConnectionInfo)
    let unsubscribeValidation = () => {}
    const revalidated = new Promise<void>((resolve) => {
      unsubscribeValidation = form.subscribe({
        formState: { values: true, errors: true },
        callback(state) {
          if (
            state.values.key === validConnectionInfo.key &&
            state.values.base_url === validConnectionInfo.url &&
            Object.keys(state.errors ?? {}).length === 0
          ) {
            resolve()
          }
        },
      })
    })
    assert.equal(
      controller.fill(() => (filled += 1)),
      true
    )
    await revalidated

    assert.deepEqual(form.getValues(), {
      key: validConnectionInfo.key,
      base_url: validConnectionInfo.url,
    })
    assert.equal(form.getFieldState('key').isDirty, true)
    assert.equal(form.getFieldState('base_url').isDirty, true)
    assert.equal(form.getFieldState('key').invalid, false)
    assert.equal(form.getFieldState('base_url').invalid, false)
    assert.equal(detected, null)
    assert.equal(filled, 1)
    controller.deactivate()
    unsubscribeValidation()
    unsubscribe()
  })

  test('Ignore, close, and edit transitions clear detected state without reviving it', async () => {
    let detected: typeof validConnectionInfo | null = null
    const controller = createChannelConnectionClipboardController({
      clipboard: {
        async readText() {
          return encodeChannelConnectionInfo(
            validConnectionInfo.key,
            validConnectionInfo.url
          )
        },
      },
      onDetectedChange(connectionInfo) {
        detected = connectionInfo
      },
      setValue() {},
    })

    await controller.setMode(true, false)
    assert.deepEqual(detected, validConnectionInfo)
    controller.ignore()
    assert.equal(detected, null)

    await controller.setMode(true, false)
    assert.deepEqual(detected, validConnectionInfo)
    controller.close()
    assert.equal(detected, null)

    await controller.setMode(true, false)
    assert.deepEqual(detected, validConnectionInfo)
    await controller.setMode(true, true)
    assert.equal(detected, null)
    controller.deactivate()
  })

  test('cancelled automatic reads cannot revive detected state', async () => {
    for (const cancel of ['ignore', 'close', 'edit'] as const) {
      let resolveClipboardText: ((text: string) => void) | undefined
      const clipboardText = new Promise<string>((resolve) => {
        resolveClipboardText = resolve
      })
      let detected: typeof validConnectionInfo | null = null
      const controller = createChannelConnectionClipboardController({
        clipboard: { readText: () => clipboardText },
        onDetectedChange(connectionInfo) {
          detected = connectionInfo
        },
        setValue() {},
      })

      const detection = controller.setMode(true, false)
      if (cancel === 'ignore') controller.ignore()
      if (cancel === 'close') controller.close()
      if (cancel === 'edit') await controller.setMode(true, true)
      resolveClipboardText?.(
        encodeChannelConnectionInfo(
          validConnectionInfo.key,
          validConnectionInfo.url
        )
      )
      await detection

      assert.equal(detected, null, cancel)
      controller.deactivate()
    }
  })

  test('explicit Paste fills through the production form action and reports success', async () => {
    let reads = 0
    const form = createFormControl({
      defaultValues: { key: '', base_url: '' },
    })
    const unsubscribe = form.subscribe({
      formState: { values: true, dirtyFields: true },
      callback() {},
    })
    form.register('key')
    form.register('base_url')
    const feedback: string[] = []
    const controller = createChannelConnectionClipboardController({
      clipboard: {
        async readText() {
          reads += 1
          return reads === 1
            ? '{}'
            : encodeChannelConnectionInfo(
                validConnectionInfo.key,
                validConnectionInfo.url
              )
        },
      },
      onDetectedChange() {},
      setValue: (field, value, options) => form.setValue(field, value, options),
    })

    await controller.setMode(true, false)
    const outcome = await controller.paste({
      onFilled: () => feedback.push('filled'),
      onUnableToRead: () => feedback.push('unavailable'),
      onConnectionInfoNotFound: () => feedback.push('not-found'),
    })

    assert.equal(outcome, 'filled')
    assert.deepEqual(form.getValues(), {
      key: validConnectionInfo.key,
      base_url: validConnectionInfo.url,
    })
    assert.deepEqual(feedback, ['filled'])
    controller.deactivate()
    unsubscribe()
  })

  test('explicit Paste distinguishes missing, rejected, and invalid clipboard data without rejecting', async () => {
    for (const scenario of [
      { clipboard: null, expected: 'unavailable' },
      {
        clipboard: {
          async readText(): Promise<string> {
            throw new Error('permission denied')
          },
        },
        expected: 'unavailable',
      },
      {
        clipboard: {
          async readText() {
            return '{"_type":"other"}'
          },
        },
        expected: 'not-found',
      },
    ] as const) {
      const feedback: string[] = []
      const controller = createChannelConnectionClipboardController({
        clipboard: scenario.clipboard,
        onDetectedChange() {},
        setValue() {
          assert.fail('invalid clipboard data must not write the form')
        },
      })
      await assert.doesNotReject(() => controller.setMode(true, false))
      const outcome = await controller.paste({
        onFilled: () => feedback.push('filled'),
        onUnableToRead: () => feedback.push('unavailable'),
        onConnectionInfoNotFound: () => feedback.push('not-found'),
      })

      assert.equal(outcome, scenario.expected)
      assert.deepEqual(feedback, [scenario.expected])
      controller.deactivate()
    }
  })

  test('closing the production lifecycle cancels a pending manual Paste before it can write the form', async () => {
    let resolveManualPaste: ((text: string) => void) | undefined
    const pendingManualPaste = new Promise<string>((resolve) => {
      resolveManualPaste = resolve
    })
    let reads = 0
    const form = createFormControl({
      defaultValues: { key: '', base_url: '' },
    })
    const unsubscribe = form.subscribe({
      formState: { values: true, dirtyFields: true, errors: true },
      callback() {},
    })
    form.register('key')
    form.register('base_url')
    const feedback: string[] = []
    let detected: typeof validConnectionInfo | null = null
    const controller = createChannelConnectionClipboardController({
      clipboard: {
        readText() {
          reads += 1
          return reads === 1 ? Promise.resolve('{}') : pendingManualPaste
        },
      },
      onDetectedChange(connectionInfo) {
        detected = connectionInfo
      },
      setValue: (field, value, options) => form.setValue(field, value, options),
    })

    await controller.setMode(true, false)
    const paste = controller.paste({
      onFilled: () => feedback.push('filled'),
      onUnableToRead: () => feedback.push('unavailable'),
      onConnectionInfoNotFound: () => feedback.push('not-found'),
    })
    controller.close()
    resolveManualPaste?.(
      encodeChannelConnectionInfo(
        validConnectionInfo.key,
        validConnectionInfo.url
      )
    )

    assert.equal(await paste, 'cancelled')
    assert.deepEqual(form.getValues(), { key: '', base_url: '' })
    assert.equal(detected, null)
    assert.deepEqual(feedback, [])
    unsubscribe()
  })

  test('switching to edit cancels a pending manual Paste before it can write the form', async () => {
    let resolveManualPaste: ((text: string) => void) | undefined
    const pendingManualPaste = new Promise<string>((resolve) => {
      resolveManualPaste = resolve
    })
    let reads = 0
    const form = createFormControl({
      defaultValues: { key: '', base_url: '' },
    })
    const unsubscribe = form.subscribe({
      formState: { values: true },
      callback() {},
    })
    form.register('key')
    form.register('base_url')
    const feedback: string[] = []
    const controller = createChannelConnectionClipboardController({
      clipboard: {
        readText() {
          reads += 1
          return reads === 1 ? Promise.resolve('{}') : pendingManualPaste
        },
      },
      onDetectedChange() {},
      setValue: (field, value, options) => form.setValue(field, value, options),
    })

    await controller.setMode(true, false)
    const paste = controller.paste({
      onFilled: () => feedback.push('filled'),
      onUnableToRead: () => feedback.push('unavailable'),
      onConnectionInfoNotFound: () => feedback.push('not-found'),
    })
    await controller.setMode(true, true)
    resolveManualPaste?.(
      encodeChannelConnectionInfo(
        validConnectionInfo.key,
        validConnectionInfo.url
      )
    )

    assert.equal(await paste, 'cancelled')
    assert.deepEqual(form.getValues(), { key: '', base_url: '' })
    assert.deepEqual(feedback, [])
    controller.deactivate()
    unsubscribe()
  })
})
