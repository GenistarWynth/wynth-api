import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { describe, test } from 'node:test'

const dialogSource = readFileSync(
  fileURLToPath(new URL('./channel-monitor-dialog.tsx', import.meta.url)),
  'utf8'
)

describe('channel monitor dialog theme contract', () => {
  test('does not force a dark color scheme over the current app theme', () => {
    assert.equal(/\bclassName=['"][^'"]*\bdark\b/.test(dialogSource), false)
  })
})
