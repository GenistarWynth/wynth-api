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

  test('renders monitor history details with themed tooltip content', () => {
    assert.match(dialogSource, /TooltipProvider/)
    assert.match(dialogSource, /TooltipTrigger/)
    assert.match(dialogSource, /TooltipContent/)
    assert.doesNotMatch(dialogSource, /title=\{historyTitle\(bar, t\)\}/)
    assert.match(dialogSource, /bar\.model/)
    assert.match(dialogSource, /Average first token latency/)
    assert.match(dialogSource, /Successful checks/)
  })

  test('can switch history between availability and first token waveform', () => {
    assert.match(dialogSource, /ToggleGroup/)
    assert.match(dialogSource, /historyViewMode/)
    assert.match(dialogSource, /linePoints/)
    assert.match(dialogSource, /anomalyPoints/)
    assert.match(dialogSource, /polyline/)
    assert.match(
      dialogSource,
      /className='(?=[^']*\brounded-full\b)(?=[^']*\bbg-destructive\b)[^']*'/
    )
    assert.doesNotMatch(dialogSource, /<circle/)
    assert.doesNotMatch(dialogSource, /segments/)
    assert.match(dialogSource, /First token waveform/)
    assert.match(dialogSource, /Availability status/)
  })

  test('keeps monitor settings available when detail loading fails', () => {
    assert.doesNotMatch(dialogSource, /\)\s*:\s*query\.isError\s*\?\s*\(/)
    assert.match(dialogSource, /query\.isError &&/)
    assert.match(dialogSource, /Failed to load monitor data/)
    assert.match(dialogSource, /Monitor Settings/)
  })

  test('owns editable monitor settings instead of the channel edit drawer', () => {
    assert.match(dialogSource, /Monitor Settings/)
    // Monitor-settings (de)serialization now lives in lib/channel-monitor.ts;
    // the dialog owns editing through the dedicated monitor settings endpoint.
    assert.match(dialogSource, /readChannelMonitorSettings/)
    assert.match(dialogSource, /buildChannelMonitorSettingsPayload/)
    assert.match(dialogSource, /updateChannelMonitorSettings/)
    assert.match(
      dialogSource,
      /updateChannelMonitorSettings\([\s\S]*buildChannelMonitorSettingsPayload\([\s\S]*\),\s*'monitor'\s*\)/
    )
    assert.doesNotMatch(dialogSource, /Auto Priority/)
    assert.doesNotMatch(dialogSource, /autoPriority/)
    assert.doesNotMatch(dialogSource, /channel-auto-priority/)

    assert.doesNotMatch(mutateDrawerSource, /name='channel_monitor_enabled'/)
    assert.doesNotMatch(
      mutateDrawerSource,
      /name='channel_monitor_interval_minutes'/
    )
    assert.doesNotMatch(
      mutateDrawerSource,
      /name='channel_auto_priority_enabled'/
    )
    assert.doesNotMatch(
      mutateDrawerSource,
      /name='channel_auto_priority_availability_window_hours'/
    )
  })

  test('selects the monitor test model from channel models with custom fallback', () => {
    assert.match(dialogSource, /import \{ Combobox \}/)
    assert.match(dialogSource, /parseModelsString/)
    assert.match(dialogSource, /channel\?\.models/)
    assert.match(dialogSource, /monitorModelOptions/)
    assert.match(dialogSource, /allowCustomValue/)
    assert.doesNotMatch(
      dialogSource,
      /<Input\s+id=\{`channel-monitor-test-model-\$\{channelId\}`\}/
    )
  })

  test('renders the shared post-mortem recovery schedule text', () => {
    assert.match(dialogSource, /monitorRefreshText/)
    assert.match(
      dialogSource,
      /monitorRefreshText\(info, t, formatRelativeTime\)/
    )
  })
})
