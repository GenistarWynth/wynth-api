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
import type { TagBatchEditField, TagOperationParams } from '../types'

type TagBatchEditValues = {
  newTag: string
  models: string
  modelMapping: string
  groups: string[]
}

export function buildTagBatchEditPayload(params: {
  currentTag: string
  selectedFields: TagBatchEditField[]
  values: TagBatchEditValues
}): TagOperationParams {
  const fields = Array.from(new Set(params.selectedFields))
  const payload: TagOperationParams = {
    tag: params.currentTag,
    fields,
  }

  if (fields.includes('tag')) {
    payload.new_tag = params.values.newTag.trim()
  }
  if (fields.includes('models')) {
    payload.models = params.values.models.trim()
  }
  if (fields.includes('model_mapping')) {
    payload.model_mapping = params.values.modelMapping.trim()
  }
  if (fields.includes('groups')) {
    payload.groups = params.values.groups.join(',')
  }

  return payload
}
