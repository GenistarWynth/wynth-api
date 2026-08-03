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
import { existsSync, readdirSync, readFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

export function containsEncodedVersionLiteral(artifactBytes, expectedVersion) {
  return artifactBytes.includes(Buffer.from(JSON.stringify(expectedVersion)))
}

const scriptPath = fileURLToPath(import.meta.url)
const scriptDirectory = path.dirname(scriptPath)
const distDirectory = path.resolve(scriptDirectory, '..', 'dist')

function verifyBuildVersion(expectedVersion) {
  if (!existsSync(distDirectory)) {
    console.error(
      `Frontend artifact directory does not exist: ${distDirectory}`
    )
    return 1
  }

  const pendingDirectories = [distDirectory]
  while (pendingDirectories.length > 0) {
    const directory = pendingDirectories.pop()
    for (const entry of readdirSync(directory, { withFileTypes: true })) {
      const entryPath = path.join(directory, entry.name)
      if (entry.isDirectory()) {
        pendingDirectories.push(entryPath)
        continue
      }
      if (
        !entry.isFile() ||
        !containsEncodedVersionLiteral(readFileSync(entryPath), expectedVersion)
      ) {
        continue
      }

      const relativePath = path
        .relative(distDirectory, entryPath)
        .replaceAll('\\', '/')
      process.stdout.write(
        `Frontend artifact contains ${expectedVersion}: dist/${relativePath}\n`
      )
      return 0
    }
  }

  console.error(
    `Frontend artifact does not contain expected version: ${expectedVersion}`
  )
  return 1
}

if (process.argv[1] && path.resolve(process.argv[1]) === scriptPath) {
  const expectedVersion = process.argv[2]
  if (!expectedVersion) {
    console.error('Usage: node scripts/verify-build-version.mjs <version>')
    process.exit(2)
  }
  process.exit(verifyBuildVersion(expectedVersion))
}
