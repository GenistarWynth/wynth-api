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
import { before, describe, test } from 'node:test'

import { createInstance } from 'i18next'
import { createElement, type ReactElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { I18nextProvider, initReactI18next } from 'react-i18next'

import {
  ModelTestAction,
  TestResultCell,
  TestStatusCell,
} from './channel-test-dialog'
import {
  BATCH_TEST_CONCURRENCY,
  createBatchRunManager,
  getModelTestActionLabels,
  type TestResult,
} from './channel-test-dialog-logic'

type Deferred<T> = {
  promise: Promise<T>
  resolve: (value: T) => void
  reject: (reason?: unknown) => void
}

function deferred<T>(): Deferred<T> {
  let resolve!: (value: T) => void
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise
    reject = rejectPromise
  })
  return { promise, resolve, reject }
}

const i18n = createInstance()

before(async () => {
  await i18n.use(initReactI18next).init({
    lng: 'en',
    fallbackLng: 'en',
    resources: { en: { translation: {} } },
    interpolation: { escapeValue: false },
  })
})

function renderWithI18n(element: ReactElement) {
  return renderToStaticMarkup(createElement(I18nextProvider, { i18n }, element))
}

describe('channel test batch progress lifecycle', () => {
  test('updates one stable toast ID as controlled model promises resolve and dismisses it on completion', async () => {
    const loadingCalls: Array<{
      message: string
      id?: string | number
      description?: string
    }> = []
    const dismissedIds: Array<string | number> = []
    const firstProgress = deferred<void>()
    const secondProgress = deferred<void>()
    const manager = createBatchRunManager()
    const session = manager.start({
      toast: {
        loading: (message, options) => {
          loadingCalls.push({ message, ...options })
          if (options.description?.startsWith('1/2')) firstProgress.resolve()
          if (options.description?.startsWith('2/2')) secondProgress.resolve()
          return loadingCalls.length === 1 ? 'batch-progress' : 'ignored-id'
        },
        dismiss: (id) => dismissedIds.push(id),
      },
      runningMessage: 'Batch testing models...',
      stoppingMessage: 'Stopping batch test...',
      formatProgressDescription: (progress) =>
        `${progress.completed}/${progress.total} · ${progress.success} succeeded, ${progress.failed} failed`,
    })
    const first = deferred<TestResult>()
    const second = deferred<TestResult>()

    const runPromise = session.run({
      models: ['model-a', 'model-b'],
      testModel: (model) =>
        model === 'model-a' ? first.promise : second.promise,
      createFallbackResult: (error) => ({
        status: 'error',
        error: error instanceof Error ? error.message : 'failed',
      }),
      onResult: () => undefined,
      waitBetweenBatches: () => Promise.resolve(),
    })

    assert.equal(loadingCalls.length, 1)
    assert.equal(loadingCalls[0].id, undefined)

    first.resolve({ status: 'success', responseTime: 120, completedAt: 1 })
    await firstProgress.promise
    assert.equal(loadingCalls.at(-1)?.id, 'batch-progress')

    second.resolve({ status: 'error', error: 'provider failed' })
    await secondProgress.promise
    const summary = await runPromise

    assert.ok(summary)
    assert.deepEqual(
      loadingCalls.slice(1).map((call) => call.id),
      ['batch-progress', 'batch-progress']
    )
    assert.deepEqual(dismissedIds, ['batch-progress'])
    assert.deepEqual(
      {
        completed: summary.completed,
        success: summary.success,
        failed: summary.failed,
        stopped: summary.stopped,
      },
      { completed: 2, success: 1, failed: 1, stopped: false }
    )
  })

  test('dismisses progress before publishing a successful batch outcome', async () => {
    const events: string[] = []
    const manager = createBatchRunManager()
    const session = manager.start({
      toast: {
        loading: (message) => {
          events.push(`loading:${message}`)
          return 'success-progress'
        },
        dismiss: (id) => events.push(`dismiss:${id}`),
      },
      runningMessage: 'Batch testing models...',
      stoppingMessage: 'Stopping batch test...',
      formatProgressDescription: (progress) =>
        `${progress.completed}/${progress.total}`,
    })

    const summary = await session.run({
      models: ['success-model'],
      testModel: async () => ({ status: 'success' }),
      createFallbackResult: () => ({ status: 'error' }),
      onResult: () => undefined,
      onOutcome: () => events.push('outcome'),
    })

    assert.ok(summary)
    const successDismissIndex = events.indexOf('dismiss:success-progress')
    const successOutcomeIndex = events.indexOf('outcome')
    assert.notEqual(successDismissIndex, -1)
    assert.notEqual(successOutcomeIndex, -1)
    assert.ok(successDismissIndex < successOutcomeIndex)
  })

  test('dismisses progress before publishing a batch error', async () => {
    const events: string[] = []
    const manager = createBatchRunManager()
    const session = manager.start({
      toast: {
        loading: (message) => {
          events.push(`loading:${message}`)
          return 'error-progress'
        },
        dismiss: (id) => events.push(`dismiss:${id}`),
      },
      runningMessage: 'Batch testing models...',
      stoppingMessage: 'Stopping batch test...',
      formatProgressDescription: (progress) =>
        `${progress.completed}/${progress.total}`,
    })

    const summary = await session.run({
      models: Array.from(
        { length: BATCH_TEST_CONCURRENCY + 1 },
        (_, index) => `error-model-${index + 1}`
      ),
      testModel: async () => ({ status: 'success' }),
      createFallbackResult: () => ({ status: 'error' }),
      onResult: () => undefined,
      onError: (error) =>
        events.push(
          `error:${error instanceof Error ? error.message : 'failed'}`
        ),
      waitBetweenBatches: async () => {
        throw new Error('forced batch failure')
      },
    })

    assert.equal(summary, undefined)
    const errorDismissIndex = events.indexOf('dismiss:error-progress')
    const errorOutcomeIndex = events.indexOf('error:forced batch failure')
    assert.notEqual(errorDismissIndex, -1)
    assert.notEqual(errorOutcomeIndex, -1)
    assert.ok(errorDismissIndex < errorOutcomeIndex)
  })

  test('keeps the same progress toast in stopping state until in-flight tests settle', async () => {
    const requests = Array.from({ length: BATCH_TEST_CONCURRENCY }, () =>
      deferred<TestResult>()
    )
    const startedModels: string[] = []
    const loadingCalls: Array<{
      message: string
      id?: string | number
      description?: string
    }> = []
    const dismissedIds: Array<string | number> = []
    const firstStoppedProgress = deferred<void>()
    const manager = createBatchRunManager()
    const session = manager.start({
      toast: {
        loading: (message, options) => {
          loadingCalls.push({ message, ...options })
          if (
            message === 'Stopping batch test...' &&
            options.description?.startsWith('1/6')
          ) {
            firstStoppedProgress.resolve()
          }
          return loadingCalls.length === 1 ? 'stop-progress' : 'ignored-id'
        },
        dismiss: (id) => dismissedIds.push(id),
      },
      runningMessage: 'Batch testing models...',
      stoppingMessage: 'Stopping batch test...',
      formatProgressDescription: (progress) =>
        `${progress.completed}/${progress.total} · ${progress.success} succeeded, ${progress.failed} failed`,
    })

    const runPromise = session.run({
      models: [
        ...requests.map((_, index) => `model-${index + 1}`),
        'next-batch-model',
      ],
      testModel: (model) => {
        startedModels.push(model)
        const requestIndex = startedModels.length - 1
        return requests[requestIndex].promise
      },
      createFallbackResult: () => ({ status: 'error' }),
      onResult: () => undefined,
      waitBetweenBatches: () => Promise.resolve(),
    })

    assert.equal(startedModels.length, BATCH_TEST_CONCURRENCY)
    assert.deepEqual(loadingCalls[0], {
      message: 'Batch testing models...',
      description: '0/6 · 0 succeeded, 0 failed',
      id: undefined,
    })

    session.requestStop()

    assert.deepEqual(loadingCalls[1], {
      message: 'Stopping batch test...',
      description: '0/6 · 0 succeeded, 0 failed',
      id: 'stop-progress',
    })
    assert.deepEqual(dismissedIds, [])

    requests[0].resolve({ status: 'success' })
    await firstStoppedProgress.promise
    assert.deepEqual(loadingCalls.at(-1), {
      message: 'Stopping batch test...',
      description: '1/6 · 1 succeeded, 0 failed',
      id: 'stop-progress',
    })
    assert.deepEqual(dismissedIds, [])

    for (const request of requests.slice(1)) {
      request.resolve({ status: 'success' })
    }
    const summary = await runPromise

    assert.ok(summary)
    assert.equal(startedModels.includes('next-batch-model'), false)
    assert.equal(summary.stopped, true)
    assert.equal(summary.completed, BATCH_TEST_CONCURRENCY)
    assert.deepEqual(dismissedIds, ['stop-progress'])
  })

  test('cancelled run cannot mutate a newer run with the same model', async () => {
    const loadingCalls: Array<{
      run: 'run-1' | 'run-2'
      message: string
      id?: string | number
      description?: string
    }> = []
    const dismissed: Array<{ run: 'run-1' | 'run-2'; id: string | number }> = []
    const runOneRequests = Array.from({ length: BATCH_TEST_CONCURRENCY }, () =>
      deferred<TestResult>()
    )
    const runTwoRequest = deferred<TestResult>()
    const startedRunOne: string[] = []
    const startedRunTwo: string[] = []
    const results: Record<string, TestResult> = {}
    const testingModels = new Set<string>()
    const outcomes: string[] = []
    const stateCleanup: string[] = []
    const selectionCleanup: string[] = []
    const cacheRefreshes: string[] = []
    const manager = createBatchRunManager()

    const createSession = (run: 'run-1' | 'run-2') =>
      manager.start({
        toast: {
          loading: (message, options) => {
            loadingCalls.push({ run, message, ...options })
            return `${run}-progress`
          },
          dismiss: (id) => dismissed.push({ run, id }),
        },
        runningMessage: 'Batch testing models...',
        stoppingMessage: 'Stopping batch test...',
        formatProgressDescription: (progress) =>
          `${progress.completed}/${progress.total}`,
      })

    const runOne = createSession('run-1')
    const runOnePromise = runOne.run({
      models: [
        'shared-model',
        'run-1-model-2',
        'run-1-model-3',
        'run-1-model-4',
        'run-1-model-5',
        'run-1-model-6',
      ],
      testModel: async (model) => {
        startedRunOne.push(model)
        const request = runOneRequests[startedRunOne.length - 1]
        runOne.runIfCurrent(() => {
          testingModels.add(model)
          results[model] = { status: 'testing' }
        })
        try {
          const result = await request.promise
          return runOne.runIfCurrent(() => {
            results[model] = result
            return result
          })
        } finally {
          runOne.runIfCurrent(() => testingModels.delete(model))
        }
      },
      createFallbackResult: () => ({ status: 'error' }),
      onResult: (model, result) => {
        results[model] = result
      },
      onOutcome: () => outcomes.push('run-1'),
      onFinally: () => {
        stateCleanup.push('run-1')
        selectionCleanup.push('run-1')
        cacheRefreshes.push('run-1')
      },
      waitBetweenBatches: () => Promise.resolve(),
    })

    assert.equal(startedRunOne.length, BATCH_TEST_CONCURRENCY)
    assert.equal(testingModels.has('shared-model'), true)

    manager.cancelCurrent()
    assert.equal(runOne.isCancelled(), true)
    assert.deepEqual(dismissed, [{ run: 'run-1', id: 'run-1-progress' }])

    // Mirrors the dialog close reset before a fresh generation starts.
    for (const key of Object.keys(results)) delete results[key]
    testingModels.clear()

    const runTwo = createSession('run-2')
    const runTwoPromise = runTwo.run({
      models: ['shared-model'],
      testModel: async (model) => {
        startedRunTwo.push(model)
        runTwo.runIfCurrent(() => {
          testingModels.add(model)
          results[model] = { status: 'testing' }
        })
        try {
          const result = await runTwoRequest.promise
          return runTwo.runIfCurrent(() => {
            results[model] = result
            return result
          })
        } finally {
          runTwo.runIfCurrent(() => testingModels.delete(model))
        }
      },
      createFallbackResult: () => ({ status: 'error' }),
      onResult: (model, result) => {
        results[model] = result
      },
      onOutcome: () => outcomes.push('run-2'),
      onFinally: () => {
        stateCleanup.push('run-2')
        selectionCleanup.push('run-2')
        cacheRefreshes.push('run-2')
      },
    })

    assert.deepEqual(startedRunTwo, ['shared-model'])
    assert.deepEqual(results['shared-model'], { status: 'testing' })
    assert.equal(testingModels.has('shared-model'), true)
    const runTwoToastCallsBeforeRunOneSettles = loadingCalls.filter(
      (call) => call.run === 'run-2'
    ).length

    for (const request of runOneRequests) {
      request.resolve({ status: 'error', error: 'stale run-1 result' })
    }
    const staleSummary = await runOnePromise

    assert.equal(staleSummary, undefined)
    assert.equal(startedRunOne.includes('run-1-model-6'), false)
    assert.equal(
      loadingCalls.filter((call) => call.run === 'run-2').length,
      runTwoToastCallsBeforeRunOneSettles
    )
    assert.deepEqual(outcomes, [])
    assert.deepEqual(stateCleanup, [])
    assert.deepEqual(selectionCleanup, [])
    assert.deepEqual(cacheRefreshes, [])
    assert.deepEqual(results['shared-model'], { status: 'testing' })
    assert.equal(testingModels.has('shared-model'), true)

    runTwoRequest.resolve({
      status: 'success',
      responseTime: 42,
      completedAt: 2,
    })
    const freshSummary = await runTwoPromise

    assert.ok(freshSummary)
    assert.deepEqual(results['shared-model'], {
      status: 'success',
      responseTime: 42,
      completedAt: 2,
    })
    assert.equal(testingModels.has('shared-model'), false)
    assert.deepEqual(outcomes, ['run-2'])
    assert.deepEqual(stateCleanup, ['run-2'])
    assert.deepEqual(selectionCleanup, ['run-2'])
    assert.deepEqual(cacheRefreshes, ['run-2'])
    assert.deepEqual(dismissed, [
      { run: 'run-1', id: 'run-1-progress' },
      { run: 'run-2', id: 'run-2-progress' },
    ])
  })
})

describe('channel test result columns and actions', () => {
  test('keeps status limited to state while rendering latency in the result cell', () => {
    const result: TestResult = {
      status: 'success',
      responseTime: 321,
      completedAt: 1,
    }
    const statusMarkup = renderWithI18n(
      createElement(TestStatusCell, { result })
    )
    const resultMarkup = renderWithI18n(
      createElement(TestResultCell, {
        result,
        model: 'gpt-test',
        onOpenDetails: () => undefined,
      })
    )

    assert.match(statusMarkup, />Success</)
    assert.doesNotMatch(statusMarkup, /321/)
    assert.match(resultMarkup, /321/)
    assert.doesNotMatch(resultMarkup, />Success</)
  })

  test('keeps failure details and model pricing repair reachable from the result cell', () => {
    const result: TestResult = {
      status: 'error',
      errorCode: 'model_price_error',
      error: 'Provider price lookup failed\nraw pricing trace',
    }
    const statusMarkup = renderWithI18n(
      createElement(TestStatusCell, { result })
    )
    const resultMarkup = renderWithI18n(
      createElement(TestResultCell, {
        result,
        model: 'unpriced-model',
        onOpenDetails: () => undefined,
      })
    )

    assert.match(statusMarkup, />Failed</)
    assert.doesNotMatch(statusMarkup, /Provider price lookup failed/)
    assert.match(
      resultMarkup,
      /Model price is not configured\. Please complete model pricing in settings\./
    )
    assert.match(resultMarkup, />Go to Settings</)
    assert.match(resultMarkup, />Details</)
  })

  test('uses the same translated accessible name and tooltip label for the icon action', () => {
    const labels = getModelTestActionLabels((key) => `translated:${key}`)
    assert.deepEqual(labels, {
      accessibleName: 'translated:Test Connection',
      tooltip: 'translated:Test Connection',
    })

    const idleMarkup = renderWithI18n(
      createElement(ModelTestAction, {
        model: 'gpt-test',
        isTesting: false,
        isBatchTesting: false,
        onTest: () => undefined,
      })
    )
    const testingMarkup = renderWithI18n(
      createElement(ModelTestAction, {
        model: 'gpt-test',
        isTesting: true,
        isBatchTesting: false,
        onTest: () => undefined,
      })
    )
    const batchMarkup = renderWithI18n(
      createElement(ModelTestAction, {
        model: 'gpt-test',
        isTesting: false,
        isBatchTesting: true,
        onTest: () => undefined,
      })
    )

    assert.match(idleMarkup, /aria-label="Test Connection"/)
    assert.match(idleMarkup, /data-icon="dashboard-speed"/)
    assert.doesNotMatch(idleMarkup, /disabled=""/)
    assert.match(testingMarkup, /disabled=""/)
    assert.match(testingMarkup, /role="status"/)
    assert.match(batchMarkup, /disabled=""/)
  })
})
