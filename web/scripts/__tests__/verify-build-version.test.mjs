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

import { containsEncodedVersionLiteral } from '../verify-build-version.mjs'

describe('release build version verifier', () => {
  test('accepts only the exact encoded version literal', () => {
    const artifact = Buffer.from('const version="v1.0.0-rc.62";')

    assert.equal(containsEncodedVersionLiteral(artifact, 'v1.0.0-rc.62'), true)
    assert.equal(containsEncodedVersionLiteral(artifact, 'v1.0.0-rc.6'), false)
    assert.equal(containsEncodedVersionLiteral(artifact, 'v1.0.0'), false)
  })

  test('rejects unrelated raw asset bytes', () => {
    const artifact = Buffer.from('asset-v1.0.0-rc.62.png')

    assert.equal(containsEncodedVersionLiteral(artifact, 'v1.0.0-rc.62'), false)
  })

  test('matches the JavaScript encoding of version metacharacters', () => {
    const version = 'v1.0.0-rc."quoted\\path\nline'
    const artifact = Buffer.from(`const version=${JSON.stringify(version)};`)

    assert.equal(containsEncodedVersionLiteral(artifact, version), true)
  })
})
