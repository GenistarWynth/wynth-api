import assert from 'node:assert/strict'
import { existsSync, readFileSync } from 'node:fs'
import { describe, test } from 'node:test'
import { fileURLToPath } from 'node:url'

function readSource(relativePath: string) {
  const filePath = fileURLToPath(new URL(relativePath, import.meta.url))
  return existsSync(filePath) ? readFileSync(filePath, 'utf8') : ''
}

const dialogSource = readSource('./channel-auto-priority-dialog.tsx')
const rowActionsSource = readSource('../data-table-row-actions.tsx')
const providerSource = readSource('../channels-provider.tsx')
const dialogsSource = readSource('../channels-dialogs.tsx')
const apiSource = readSource('../../api.ts')

describe('channel auto priority dialog contract', () => {
  test('adds Auto Priority as a sibling row menu item beside Channel Monitor', () => {
    const monitorItem = rowActionsSource.indexOf("{t('Channel Monitor')}")
    const autoPriorityItem = rowActionsSource.indexOf("{t('Auto Priority')}")
    const nextSeparator = rowActionsSource.indexOf(
      '<DropdownMenuSeparator />',
      monitorItem
    )

    assert.ok(monitorItem >= 0)
    assert.ok(autoPriorityItem > monitorItem)
    assert.ok(nextSeparator > autoPriorityItem)
    assert.match(rowActionsSource, /setOpen\('channel-auto-priority'\)/)
  })

  test('registers and renders a dedicated Auto Priority dialog', () => {
    assert.match(providerSource, /\| 'channel-auto-priority'/)
    assert.match(
      dialogsSource,
      /import \{ ChannelAutoPriorityDialog \} from '.\/dialogs\/channel-auto-priority-dialog'/
    )
    assert.match(dialogsSource, /open=\{open === 'channel-auto-priority'\}/)
    assert.match(dialogsSource, /<ChannelAutoPriorityDialog/)
  })

  test('owns only auto-priority controls and reuses the settings save path', () => {
    assert.match(dialogSource, /Auto Priority/)
    assert.match(dialogSource, /Auto Priority Interval Minutes/)
    assert.match(dialogSource, /Metrics Window Hours/)
    assert.match(dialogSource, /Availability Window Hours/)
    assert.match(dialogSource, /Rate Multiplier/)
    assert.match(dialogSource, /readChannelAutoPrioritySettings/)
    assert.match(dialogSource, /buildChannelAutoPrioritySettingsPayload/)
    assert.match(dialogSource, /updateChannelMonitorSettings/)
    assert.match(
      dialogSource,
      /updateChannelMonitorSettings\([\s\S]*buildChannelAutoPrioritySettingsPayload\([\s\S]*\),\s*'auto-priority'\s*\)/
    )
    assert.doesNotMatch(dialogSource, /channel-monitor-enabled/)
    assert.doesNotMatch(dialogSource, /channel-monitor-interval/)
    assert.doesNotMatch(dialogSource, /MonitorHistory/)
  })

  test('keeps the settings form scrollable on short viewports', () => {
    assert.match(dialogSource, /max-h-\[calc\(100vh-2rem\)\]/)
    assert.match(dialogSource, /overflow-y-auto/)
  })

  test('keeps generated-channel ownership while explaining group-wide availability', () => {
    assert.match(dialogSource, /isChannelAutoPriorityManagedByUpstream/)
    assert.match(
      dialogSource,
      /Managed by the upstream source rule for this generated channel\./
    )
    assert.match(
      dialogSource,
      /Applies to all auto-priority channels in the current group, including upstream-generated channels\./
    )
    assert.doesNotMatch(
      dialogSource,
      /Applies to all manual channels in this local group\./
    )
    assert.doesNotMatch(
      dialogSource,
      /This group window comes from the upstream source rule\./
    )
  })

  test('forces one immediate recompute for the current group', () => {
    assert.match(
      apiSource,
      /post\(\s*`\/api\/channel\/\$\{id\}\/auto_priority\/run`/
    )
    assert.match(dialogSource, /runChannelAutoPriorityGroup/)
    assert.match(
      dialogSource,
      /runChannelAutoPriorityGroup\(props\.channel\.id\)/
    )
    assert.match(dialogSource, /Recompute auto priority for this group now/)
    assert.match(dialogSource, /Auto priority recomputed for this group/)
  })

  test('treats the scheduling interval as a generated-inclusive group setting', () => {
    assert.match(
      dialogSource,
      /The interval applies to all auto-priority channels in the current group, including upstream-generated channels\./
    )
    assert.doesNotMatch(
      dialogSource,
      /id=\{`channel-auto-priority-interval-\$\{channelId\}`\}[\s\S]{0,250}disabled=\{!perChannelSettingsEditable\}/
    )
  })
})
