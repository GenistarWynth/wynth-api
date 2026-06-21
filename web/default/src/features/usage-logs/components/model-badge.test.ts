import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { describe, test } from 'node:test'
import { fileURLToPath } from 'node:url'

const badgeSource = readFileSync(
  fileURLToPath(new URL('./model-badge.tsx', import.meta.url)),
  'utf8'
)

describe('model badge popover contract', () => {
  test('disables copy interaction on the interactive badge inside the popover trigger', () => {
    assert.match(badgeSource, /copyable=\{false\}/)
  })

  test('keeps audit-only rows interactive even without mapping or mismatch', () => {
    assert.match(badgeSource, /const hasAuditDetails = Boolean\(/)
    assert.match(
      badgeSource,
      /if \(!isMapped && !hasActualMismatch && !hasAuditDetails\)/
    )
  })
})
