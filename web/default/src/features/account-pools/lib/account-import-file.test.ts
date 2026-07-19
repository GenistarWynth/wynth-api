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
})
