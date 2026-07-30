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
import { isAxiosError } from 'axios'

export type ModelRatioVisualEditorProps = {
  savedModelPrice: string
  savedModelRatio: string
  savedCacheRatio: string
  savedCreateCacheRatio: string
  savedCompletionRatio: string
  savedImageRatio: string
  savedAudioRatio: string
  savedAudioCompletionRatio: string
  savedBillingMode: string
  savedBillingExpr: string
  modelPrice: string
  modelRatio: string
  cacheRatio: string
  createCacheRatio: string
  completionRatio: string
  imageRatio: string
  audioRatio: string
  audioCompletionRatio: string
  billingMode: string
  billingExpr: string
  candidateModelNames?: string[]
  candidateModelsLoading?: boolean
  filterMode?: 'all' | 'unset'
  onChange: (field: string, value: string) => void
  onSave: () => void | Promise<void>
  isSaving: boolean
}

export function areModelRatioVisualEditorPropsEqual(
  previous: ModelRatioVisualEditorProps,
  next: ModelRatioVisualEditorProps
): boolean {
  return (
    previous.savedModelPrice === next.savedModelPrice &&
    previous.savedModelRatio === next.savedModelRatio &&
    previous.savedCacheRatio === next.savedCacheRatio &&
    previous.savedCreateCacheRatio === next.savedCreateCacheRatio &&
    previous.savedCompletionRatio === next.savedCompletionRatio &&
    previous.savedImageRatio === next.savedImageRatio &&
    previous.savedAudioRatio === next.savedAudioRatio &&
    previous.savedAudioCompletionRatio === next.savedAudioCompletionRatio &&
    previous.savedBillingMode === next.savedBillingMode &&
    previous.savedBillingExpr === next.savedBillingExpr &&
    previous.modelPrice === next.modelPrice &&
    previous.modelRatio === next.modelRatio &&
    previous.cacheRatio === next.cacheRatio &&
    previous.createCacheRatio === next.createCacheRatio &&
    previous.completionRatio === next.completionRatio &&
    previous.imageRatio === next.imageRatio &&
    previous.audioRatio === next.audioRatio &&
    previous.audioCompletionRatio === next.audioCompletionRatio &&
    previous.billingMode === next.billingMode &&
    previous.billingExpr === next.billingExpr &&
    previous.candidateModelNames === next.candidateModelNames &&
    previous.candidateModelsLoading === next.candidateModelsLoading &&
    previous.filterMode === next.filterMode &&
    previous.onChange === next.onChange &&
    previous.onSave === next.onSave &&
    previous.isSaving === next.isSaving
  )
}

export function resolveEnabledModelsError(input: {
  enabled: boolean
  isError: boolean
  error?: unknown
  data?: { success: boolean; message?: string }
  fallback: string
}): string | null {
  if (!input.enabled) return null
  if (input.isError) {
    if (isAxiosError(input.error)) {
      const status = input.error.response?.status
      if (status === 401 || status === 500) return null
    }
    return input.fallback
  }
  if (input.data && !input.data.success) {
    return input.data.message || input.fallback
  }
  return null
}

export function buildBatchPricingCopyTargets(
  sourceName: string,
  targetNames: string[]
): string[] {
  return [...new Set([sourceName, ...targetNames])]
}
