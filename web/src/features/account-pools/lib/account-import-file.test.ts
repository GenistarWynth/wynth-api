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
import { describe, test } from 'node:test'

describe('account import file reading', () => {
  test('accepts CLIProxyAPI config and auth file extensions and MIME types', async () => {
    const accountImportFile = await import('./account-import-file').catch(
      () => undefined
    )
    assert.ok(accountImportFile, 'account import file reader should exist')

    const acceptedTypes =
      accountImportFile.ACCOUNT_IMPORT_FILE_ACCEPT.split(',')
    assert.ok(acceptedTypes.includes('.json'))
    assert.ok(acceptedTypes.includes('.yaml'))
    assert.ok(acceptedTypes.includes('.yml'))
    assert.ok(acceptedTypes.includes('application/json'))
    assert.ok(acceptedTypes.includes('application/yaml'))
    assert.ok(acceptedTypes.includes('text/yaml'))
  })

  test('returns the selected file name and exact text content', async () => {
    const accountImportFile = await import('./account-import-file').catch(
      () => undefined
    )
    assert.ok(accountImportFile, 'account import file reader should exist')

    const content = 'codex-api-key:\n  - api-key: sk-test\n'
    const result = await accountImportFile.readAccountImportFile({
      name: 'accounts.yaml',
      text: async () => content,
    })

    assert.deepEqual(result, {
      name: 'accounts.yaml',
      content,
    })
  })

  test('merges multiple CPA auth JSON object files into one array payload', async () => {
    const accountImportFile = await import('./account-import-file')
    const result = await accountImportFile.readAccountImportFiles([
      {
        name: 'xai-a@mailtd.ccwu.cc.json',
        text: async () =>
          JSON.stringify({
            type: 'xai',
            email: 'xai-a@mailtd.ccwu.cc',
            access_token: 'access-a',
            refresh_token: 'refresh-a',
          }),
      },
      {
        name: 'xai-b@mailtd.ccwu.cc.json',
        text: async () =>
          JSON.stringify({
            type: 'xai',
            email: 'xai-b@mailtd.ccwu.cc',
            access_token: 'access-b',
            refresh_token: 'refresh-b',
          }),
      },
    ])

    assert.deepEqual(result.names, [
      'xai-a@mailtd.ccwu.cc.json',
      'xai-b@mailtd.ccwu.cc.json',
    ])
    assert.deepEqual(result.failedNames, [])
    assert.deepEqual(JSON.parse(result.content), [
      {
        type: 'xai',
        email: 'xai-a@mailtd.ccwu.cc',
        access_token: 'access-a',
        refresh_token: 'refresh-a',
      },
      {
        type: 'xai',
        email: 'xai-b@mailtd.ccwu.cc',
        access_token: 'access-b',
        refresh_token: 'refresh-b',
      },
    ])
  })

  test('flattens nested JSON arrays when merging multiple files', async () => {
    const accountImportFile = await import('./account-import-file')
    const result = await accountImportFile.readAccountImportFiles([
      {
        name: 'batch-a.json',
        text: async () =>
          JSON.stringify([
            { type: 'xai', email: 'a@example.com', refresh_token: 'ra' },
          ]),
      },
      {
        name: 'single-b.json',
        text: async () =>
          JSON.stringify({
            type: 'xai',
            email: 'b@example.com',
            refresh_token: 'rb',
          }),
      },
    ])

    assert.deepEqual(JSON.parse(result.content), [
      { type: 'xai', email: 'a@example.com', refresh_token: 'ra' },
      { type: 'xai', email: 'b@example.com', refresh_token: 'rb' },
    ])
  })

  test('summarizes selected file names for dialog display', async () => {
    const accountImportFile = await import('./account-import-file')
    assert.equal(
      accountImportFile.summarizeAccountImportFileNames(['a.json']),
      'a.json'
    )
    assert.equal(
      accountImportFile.summarizeAccountImportFileNames([
        'a.json',
        'b.json',
        'c.json',
      ]),
      'a.json, b.json, c.json'
    )
    assert.equal(
      accountImportFile.summarizeAccountImportFileNames([
        'a.json',
        'b.json',
        'c.json',
        'd.json',
      ]),
      'a.json, b.json, c.json (+1 more)'
    )
  })
})
