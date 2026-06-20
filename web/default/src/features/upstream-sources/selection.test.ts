import assert from 'node:assert/strict'
import { describe, test } from 'node:test'
import {
  hasMappingSelectionChanges,
  resolveSelectedMappingIDs,
} from './selection'

const mappings = [
  { id: 1, sync_enabled: true },
  { id: 2, sync_enabled: true },
  { id: 3, sync_enabled: false },
]

describe('upstream source mapping selection', () => {
  test('removes unchecked persisted mappings from the selected IDs', () => {
    const selected = resolveSelectedMappingIDs(mappings, { 2: false })

    assert.deepEqual(selected, [1])
    assert.equal(hasMappingSelectionChanges(mappings, { 2: false }), true)
  })

  test('does not require saving when overrides match persisted values', () => {
    assert.equal(
      hasMappingSelectionChanges(mappings, { 2: true, 3: false }),
      false
    )
  })
})
