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
import assert from 'node:assert/strict'
import { spawnSync } from 'node:child_process'
import { mkdtemp, mkdir, readFile, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { describe, test } from 'node:test'

import en from '../locales/en.json'
import fr from '../locales/fr.json'
import ja from '../locales/ja.json'
import ru from '../locales/ru.json'
import vi from '../locales/vi.json'
import zhTW from '../locales/zh-TW.json'

describe('frontend translation catalog regressions', () => {
  test('uses the intended Traditional Chinese Account Pools label', () => {
    assert.equal(zhTW.translation['Account Pools'], '帳號池')
    assert.notEqual(
      zhTW.translation['Account Pools'],
      en.translation['Account Pools']
    )
  })

  test('uses context-appropriate Traditional Chinese UI terms', () => {
    const expected = {
      '{{completed}} of {{total}} sections ready':
        '{{completed}} / {{total}} 個區段已就緒',
      'History of Midjourney-style image tasks.':
        'Midjourney 風格影像任務歷史。',
      'Required to expose Midjourney-style image generation to end users.':
        '需要向終端使用者開放 Midjourney 風格的影像生成。',
      'Auto priority may overwrite manual edits. Last run: {{time}}. Effective cost: {{cost}}x. Availability score: {{availability}}. First token score: {{firstToken}}. Throughput score: {{throughput}}.':
        '自動優先順序可能覆蓋手動修改。上次執行：{{time}}。有效成本：{{cost}}x。可用性得分：{{availability}}。首 token 得分：{{firstToken}}。吞吐量得分：{{throughput}}。',
      'Automatic channel probes are configured on each channel.':
        '每個渠道皆可設定自動探測。',
      'Direct connection (bypass pool proxy)': '直接連線（略過帳號池代理）',
      'Header Overrides JSON': '請求標頭覆蓋 JSON',
      'Clear session': '清除工作階段',
      'Currently forced to channel #{{id}}': '目前已強制切換到渠道 #{{id}}',
      'Filter by source, URL, email or group...':
        '按上游來源、URL、電子郵件或分組篩選...',
      'Client identity simulation': '用戶端身分模擬',
      Timeout: '逾時',
      Platforms: '平台',
      'Detect Models': '偵測模型',
      'Successful checks': '檢查成功',
      'Model List Updates': '模型清單更新',
      'Upstream Sources': '上游來源',
      Mappings: '對應關係',
      'Non-stream Mode': '非串流模式',
      'Additional failure keywords': '附加失敗關鍵字',
      'Built-in templates': '內建範本',
      matched: '相符',
      'Matches all groups': '符合所有分組',
      'Recent monitor runs': '近期監控紀錄',
      'Allow Private IP / Fake IP': '允許私人 IP / Fake IP',
      'Enable post-mortem recovery': '啟用自動停用後恢復檢查',
      'Failed to reveal key': '顯示金鑰失敗',
      'Failed to save post-mortem recovery settings':
        '儲存自動停用後恢復檢查設定失敗',
      'Force disable': '強制停用',
      'Force enable': '強制啟用',
      Identifier: '識別碼',
      'Maximum post-mortem recovery probes started by each one-minute worker tick.':
        '每個一分鐘背景工作週期最多啟動的自動停用後恢復檢查次數。',
      'Next post-mortem recovery: {{value}}':
        '下次自動停用後恢復檢查：{{value}}',
      'Not Routed': '未路由',
      'Password must be at least 8 characters long':
        '密碼長度至少須為 8 個字元',
      'Post-mortem recovery': '自動停用後恢復檢查',
      'Post-mortem recovery maximum (minutes)':
        '自動停用後恢復檢查最長間隔（分鐘）',
      'Post-mortem recovery minimum (minutes)':
        '自動停用後恢復檢查最短間隔（分鐘）',
      'Post-mortem recovery settings saved': '自動停用後恢復檢查設定已儲存',
      Routed: '已路由',
      'Set to 0 to score on every worker tick.':
        '設為 0，則每個背景工作週期都會評分。',
      'Upstream aggregation': '上游彙整',
      '7-day': '7 天',
    } as const

    for (const key of Object.keys(expected) as Array<keyof typeof expected>) {
      assert.equal(zhTW.translation[key], expected[key])
    }
  })

  test('avoids Mainland-specific conversion artifacts in Traditional Chinese', () => {
    for (const term of [
      '訪問令牌',
      '例項',
      '禁用',
      '全域性',
      '響應',
      '回撥',
      '配置',
      '自定義',
      '憑據',
      '流速得分',
      '已改為',
    ]) {
      const matchingKeys = Object.entries(zhTW.translation)
        .filter(([, value]) => value.includes(term))
        .map(([key]) => key)

      assert.deepEqual(matchingKeys, [], `unexpected zh-TW term: ${term}`)
    }

    const mainlandApplyKeys = Object.entries(zhTW.translation)
      .filter(([, value]) => value.replaceAll('應用程式', '').includes('應用'))
      .map(([key]) => key)
    assert.deepEqual(mainlandApplyKeys, [])
  })

  test('preserves representative shared translations from upstream', () => {
    const key = 'API key is loading, please try again in a moment'
    const expected = [
      [
        fr.translation[key],
        'La clé API est en cours de chargement, veuillez réessayer dans un instant',
      ],
      [
        ja.translation[key],
        'APIキーを読み込み中です。しばらくしてからもう一度お試しください',
      ],
      [
        ru.translation[key],
        'API-ключ загружается, пожалуйста, попробуйте еще раз через мгновение',
      ],
      [vi.translation[key], 'Khóa API đang tải, vui lòng thử lại sau một chút'],
    ] as const

    for (const [actual, upstream] of expected) {
      assert.equal(actual, upstream)
      assert.notEqual(actual, en.translation[key])
    }
  })

  test('reports an injected Traditional Chinese English fallback', async () => {
    const fixtureRoot = await mkdtemp(path.join(tmpdir(), 'sync-i18n-'))
    const localesDir = path.join(fixtureRoot, 'src/i18n/locales')
    const syncScript = path.resolve('scripts/sync-i18n.mjs')

    try {
      await mkdir(localesDir, { recursive: true })
      const fallbackCatalog = JSON.stringify(
        { translation: { 'Account Pools': 'Account Pools' } },
        null,
        2
      )
      await Promise.all([
        writeFile(path.join(localesDir, 'en.json'), fallbackCatalog),
        writeFile(path.join(localesDir, 'zh-TW.json'), fallbackCatalog),
      ])

      const result = spawnSync(process.execPath, [syncScript], {
        cwd: fixtureRoot,
        encoding: 'utf8',
      })
      assert.equal(result.status, 0, result.stderr)

      const report = JSON.parse(
        await readFile(
          path.join(localesDir, '_reports/_sync-report.json'),
          'utf8'
        )
      ) as {
        locales: Record<string, { untranslatedCount: number }>
      }
      assert.equal(report.locales['zh-TW']?.untranslatedCount, 1)

      const untranslated = JSON.parse(
        await readFile(
          path.join(localesDir, '_reports/zh-TW.untranslated.json'),
          'utf8'
        )
      ) as Record<string, string>
      assert.equal(untranslated['Account Pools'], 'Account Pools')
    } finally {
      await rm(fixtureRoot, { recursive: true, force: true })
    }
  })
})
