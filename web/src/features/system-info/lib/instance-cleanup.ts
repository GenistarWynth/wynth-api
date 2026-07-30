import type { SystemInstance } from '../types'

export function resolveSystemInstanceCleanup(instances: SystemInstance[]) {
  const staleInstances = instances.filter(
    (instance) => instance.status === 'stale'
  )
  return {
    staleInstances,
    hasStaleInstances: staleInstances.length > 0,
  }
}

export function buildSystemInstanceDeletePath(nodeName: string): string {
  return `/api/system-info/instances/${encodeURIComponent(nodeName)}`
}

export async function runSystemInstanceCleanup(
  runWithVerification: () => Promise<unknown>,
  onInitialError: (error: unknown) => void
): Promise<void> {
  try {
    await runWithVerification()
  } catch (error) {
    onInitialError(error)
  }
}
