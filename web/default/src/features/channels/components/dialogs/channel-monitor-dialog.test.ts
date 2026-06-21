import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { describe, test } from 'node:test'
import { fileURLToPath } from 'node:url'

const dialogSource = readFileSync(
  fileURLToPath(new URL('./channel-monitor-dialog.tsx', import.meta.url)),
  'utf8'
)
const mutateDrawerSource = readFileSync(
  fileURLToPath(
    new URL('../drawers/channel-mutate-drawer.tsx', import.meta.url)
  ),
  'utf8'
)

describe('channel monitor dialog theme contract', () => {
  test('does not force a dark color scheme over the current app theme', () => {
    assert.equal(/\bclassName=['"][^'"]*\bdark\b/.test(dialogSource), false)
  })

  test('owns editable monitor settings instead of the channel edit drawer', () => {
    assert.match(dialogSource, /Monitor Settings/)
    assert.match(dialogSource, /channel_monitor_enabled/)
    assert.match(dialogSource, /channel_monitor_interval_minutes/)
    assert.match(dialogSource, /test_model/)
    assert.match(dialogSource, /updateChannel/)

    assert.doesNotMatch(mutateDrawerSource, /name='channel_monitor_enabled'/)
    assert.doesNotMatch(
      mutateDrawerSource,
      /name='channel_monitor_interval_minutes'/
    )
  })

  test('selects the monitor test model from channel models with custom fallback', () => {
    assert.match(dialogSource, /import \{ Combobox \}/)
    assert.match(dialogSource, /parseModelsString/)
    assert.match(dialogSource, /channel\?\.models/)
    assert.match(dialogSource, /testModelOptions/)
    assert.match(dialogSource, /allowCustomValue/)
    assert.doesNotMatch(
      dialogSource,
      /<Input\s+id=\{`channel-monitor-test-model-\$\{channelId\}`\}/
    )
  })
})
