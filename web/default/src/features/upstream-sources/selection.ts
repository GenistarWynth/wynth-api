import type { UpstreamSourceMapping } from './types'

type MappingSelectionState = Pick<UpstreamSourceMapping, 'id' | 'sync_enabled'>

export function resolveSelectedMappingIDs(
  mappings: MappingSelectionState[],
  overrides: Record<number, boolean>
) {
  return mappings
    .filter((mapping) => overrides[mapping.id] ?? mapping.sync_enabled)
    .map((mapping) => mapping.id)
}

export function hasMappingSelectionChanges(
  mappings: MappingSelectionState[],
  overrides: Record<number, boolean>
) {
  return mappings.some((mapping) => {
    const selected = overrides[mapping.id] ?? mapping.sync_enabled
    return selected !== mapping.sync_enabled
  })
}
