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
export type TestStatus = 'idle' | 'testing' | 'success' | 'error'

export type TestResult = {
  status: TestStatus
  responseTime?: number
  completedAt?: number
  error?: string
  errorCode?: string
}

export type BatchProgress = {
  total: number
  completed: number
  success: number
  failed: number
}

export const BATCH_TEST_CONCURRENCY = 5
const BATCH_TEST_DELAY_MS = 100

function waitForNextBatch(ms: number) {
  return new Promise<void>((resolve) => window.setTimeout(resolve, ms))
}

type BatchProgressToastId = string | number

type BatchProgressToastAdapter = {
  loading: (
    message: string,
    options: { id?: BatchProgressToastId; description?: string }
  ) => BatchProgressToastId
  dismiss: (id: BatchProgressToastId) => void
}

function createBatchProgressToastController(
  adapter: BatchProgressToastAdapter
) {
  let toastId: BatchProgressToastId | null = null
  let isDismissed = false

  return {
    show(message: string, options: { description?: string }) {
      if (isDismissed) return
      const nextToastId = adapter.loading(message, {
        ...options,
        id: toastId ?? undefined,
      })
      if (toastId === null) {
        toastId = nextToastId
      }
    },
    dismiss() {
      if (isDismissed) return
      isDismissed = true
      if (toastId === null) return
      const activeToastId = toastId
      toastId = null
      adapter.dismiss(activeToastId)
    },
  }
}

type BatchStopRef = {
  current: boolean
}

export type BatchTestSummary = BatchProgress & {
  results: TestResult[]
  stopped: boolean
}

type RunBatchModelTestsOptions = {
  models: string[]
  stopRequested: BatchStopRef
  isActive?: () => boolean
  testModel: (model: string) => Promise<TestResult | undefined>
  createFallbackResult: (error?: unknown) => TestResult
  onResult: (model: string, result: TestResult) => void
  onProgress: (progress: BatchProgress) => void
  onSettled?: () => void
  waitBetweenBatches?: () => Promise<void>
}

export async function runBatchModelTests(
  options: RunBatchModelTestsOptions
): Promise<BatchTestSummary> {
  const uniqueModels = [
    ...new Set(options.models.map((model) => model.trim()).filter(Boolean)),
  ]
  const results: TestResult[] = []
  let completed = 0
  let success = 0
  let failed = 0
  const isActive = options.isActive ?? (() => true)

  const reportProgress = () => {
    if (!isActive()) return
    options.onProgress({
      total: uniqueModels.length,
      completed,
      success,
      failed,
    })
  }

  try {
    reportProgress()

    for (
      let startIndex = 0;
      startIndex < uniqueModels.length;
      startIndex += BATCH_TEST_CONCURRENCY
    ) {
      if (options.stopRequested.current || !isActive()) break

      const batch = uniqueModels.slice(
        startIndex,
        startIndex + BATCH_TEST_CONCURRENCY
      )
      await Promise.allSettled(
        batch.map(async (model) => {
          if (!isActive()) return

          let result: TestResult | undefined
          try {
            const testedResult = await options.testModel(model)
            if (!isActive()) return
            if (testedResult) {
              result = testedResult
            } else {
              result = options.createFallbackResult()
              options.onResult(model, result)
            }
          } catch (error: unknown) {
            if (!isActive()) return
            result = options.createFallbackResult(error)
            options.onResult(model, result)
          }

          if (!isActive()) return
          results.push(result)
          completed += 1
          if (result.status === 'success') success += 1
          failed = completed - success
          reportProgress()
        })
      )

      if (
        !isActive() ||
        options.stopRequested.current ||
        startIndex + BATCH_TEST_CONCURRENCY >= uniqueModels.length
      ) {
        break
      }

      await (options.waitBetweenBatches?.() ??
        waitForNextBatch(BATCH_TEST_DELAY_MS))
    }

    return {
      total: uniqueModels.length,
      completed,
      success,
      failed,
      results,
      stopped: options.stopRequested.current && completed < uniqueModels.length,
    }
  } finally {
    if (isActive()) {
      options.onSettled?.()
    }
  }
}

type StartBatchRunOptions = {
  toast: BatchProgressToastAdapter
  runningMessage: string
  stoppingMessage: string
  formatProgressDescription: (progress: BatchProgress) => string
}

type BatchRunOptions = Omit<
  RunBatchModelTestsOptions,
  'isActive' | 'onProgress' | 'onSettled' | 'stopRequested'
> & {
  onOutcome?: (summary: BatchTestSummary) => void
  onError?: (error: unknown) => void
  onFinally?: () => void
}

export type BatchRunSession = {
  isCurrent: () => boolean
  isCancelled: () => boolean
  runIfCurrent: <T>(callback: () => T) => T | undefined
  reportProgress: (progress: BatchProgress) => void
  requestStop: () => void
  cancel: () => void
  run: (options: BatchRunOptions) => Promise<BatchTestSummary | undefined>
}

export function createBatchRunManager() {
  let currentSession: BatchRunSession | null = null
  let currentGeneration = 0

  return {
    start(options: StartBatchRunOptions): BatchRunSession {
      currentSession?.cancel()

      const generation = currentGeneration + 1
      currentGeneration = generation
      const stopRequested: BatchStopRef = { current: false }
      const progressToast = createBatchProgressToastController(options.toast)
      let cancelled = false
      let lastProgress: BatchProgress | undefined

      const session: BatchRunSession = {
        isCurrent: () =>
          !cancelled &&
          currentGeneration === generation &&
          currentSession === session,
        isCancelled: () => cancelled,
        runIfCurrent<T>(callback: () => T) {
          if (!session.isCurrent()) return undefined
          return callback()
        },
        reportProgress(progress) {
          if (!session.isCurrent()) return

          lastProgress = progress
          progressToast.show(
            stopRequested.current
              ? options.stoppingMessage
              : options.runningMessage,
            { description: options.formatProgressDescription(progress) }
          )
        },
        requestStop() {
          if (!session.isCurrent() || stopRequested.current) return

          stopRequested.current = true
          if (lastProgress) {
            session.reportProgress(lastProgress)
          }
        },
        cancel() {
          if (cancelled) return

          cancelled = true
          stopRequested.current = true
          progressToast.dismiss()
          if (currentSession === session) {
            currentSession = null
          }
        },
        async run(runOptions) {
          try {
            const summary = await runBatchModelTests({
              ...runOptions,
              stopRequested,
              isActive: session.isCurrent,
              testModel: async (model) => {
                if (!session.isCurrent()) return undefined

                const result = await runOptions.testModel(model)
                return session.isCurrent() ? result : undefined
              },
              createFallbackResult: (error) =>
                runOptions.createFallbackResult(error),
              onResult: (model, result) => {
                session.runIfCurrent(() => runOptions.onResult(model, result))
              },
              onProgress: session.reportProgress,
              onSettled: undefined,
            })

            if (!session.isCurrent()) return undefined

            progressToast.dismiss()
            runOptions.onOutcome?.(summary)
            return summary
          } catch (error: unknown) {
            if (!session.isCurrent()) return undefined

            progressToast.dismiss()
            if (!runOptions.onError) throw error

            runOptions.onError(error)
            return undefined
          } finally {
            progressToast.dismiss()
            session.runIfCurrent(() => runOptions.onFinally?.())
            if (currentSession === session) {
              currentSession = null
            }
          }
        },
      }

      currentSession = session
      return session
    },
    cancelCurrent() {
      currentSession?.cancel()
    },
  }
}

export function getModelTestActionLabels(translate: (key: string) => string) {
  const label = translate('Test Connection')
  return {
    accessibleName: label,
    tooltip: label,
  }
}
