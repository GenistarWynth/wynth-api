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
type AccountImportFile = Pick<File, 'name' | 'text'>

export const ACCOUNT_IMPORT_FILE_ACCEPT =
  '.json,.yaml,.yml,.txt,text/*,application/json,application/yaml,application/x-yaml,text/yaml'

export type AccountImportSelectedFilesResult = {
  names: string[]
  content: string
  failedNames: string[]
}

function fileExtension(name: string) {
  const idx = name.lastIndexOf('.')
  return idx >= 0 ? name.slice(idx).toLowerCase() : ''
}

function looksLikeJsonText(text: string) {
  const trimmed = text.trim()
  return trimmed.startsWith('{') || trimmed.startsWith('[')
}

export function summarizeAccountImportFileNames(names: string[], maxNames = 3) {
  if (names.length === 0) return ''
  if (names.length === 1) return names[0]
  if (names.length <= maxNames) return names.join(', ')
  const shown = names.slice(0, maxNames).join(', ')
  return `${shown} (+${names.length - maxNames} more)`
}

export async function readAccountImportFile(file: AccountImportFile) {
  return {
    name: file.name,
    content: await file.text(),
  }
}

/**
 * Read one or more selected import files and produce a single content payload.
 *
 * - 1 file: return raw text as-is (existing single-file behavior)
 * - N JSON objects/arrays: merge into one JSON array for CPA auth bulk import
 * - N YAML/text config files: join with blank lines (codex-api-key YAML lists)
 */
export async function readAccountImportFiles(
  files: AccountImportFile[]
): Promise<AccountImportSelectedFilesResult> {
  if (!Array.isArray(files) || files.length === 0) {
    throw new Error('No files selected')
  }

  const names: string[] = []
  const failedNames: string[] = []
  const rawContents: string[] = []

  for (const file of files) {
    names.push(file.name)
    try {
      rawContents.push(await file.text())
    } catch {
      failedNames.push(file.name)
    }
  }

  if (rawContents.length === 0) {
    return { names, content: '', failedNames }
  }

  if (rawContents.length === 1) {
    return {
      names,
      content: rawContents[0],
      failedNames,
    }
  }

  const parsedJsonItems: unknown[] = []
  const nonJsonContents: string[] = []
  let allJson = true

  for (let i = 0; i < rawContents.length; i++) {
    const content = rawContents[i]
    const name = names[i] ?? `file-${i + 1}`
    const ext = fileExtension(name)
    const preferJson = ext === '.json' || looksLikeJsonText(content)

    if (preferJson) {
      try {
        const parsed = JSON.parse(content) as unknown
        if (Array.isArray(parsed)) {
          parsedJsonItems.push(...parsed)
        } else {
          parsedJsonItems.push(parsed)
        }
        continue
      } catch {
        // Fall through and treat as plain text if JSON parse fails.
      }
    }

    allJson = false
    nonJsonContents.push(content)
  }

  if (allJson && nonJsonContents.length === 0) {
    return {
      names,
      content: JSON.stringify(parsedJsonItems, null, 2),
      failedNames,
    }
  }

  // Mixed or non-JSON selection: concatenate original file texts.
  // For pure YAML CPA configs this still works when each file already has
  // top-level codex-api-key lists; mixed JSON+YAML is best-effort.
  const joinedParts = [
    ...parsedJsonItems.map((item) => JSON.stringify(item, null, 2)),
    ...nonJsonContents,
  ]
  return {
    names,
    content: joinedParts.join('\n\n'),
    failedNames,
  }
}
