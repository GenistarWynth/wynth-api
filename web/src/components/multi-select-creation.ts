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

type MultiSelectCreationOption = {
  label: string
  value: string
}

const MULTI_SELECT_SEPARATOR_REGEX = /[,，\n]/

function splitMultiSelectDraft(value: string): {
  completed: string[]
  draft: string
} {
  if (!MULTI_SELECT_SEPARATOR_REGEX.test(value)) {
    return { completed: [], draft: value }
  }
  const normalized = value.replaceAll('，', ',').replaceAll('\n', ',')
  const parts = normalized.split(',')
  const draft = parts.at(-1) ?? ''
  const completed = parts
    .slice(0, -1)
    .map((part) => part.trim())
    .filter(Boolean)
  return { completed, draft }
}

function conflictsWithExistingValue(
  value: string,
  options: MultiSelectCreationOption[],
  selected: Set<string>
): boolean {
  return (
    selected.has(value) ||
    options.some((option) => option.value === value || option.label === value)
  )
}

export function isMultiSelectValueCreatable(
  input: string,
  options: MultiSelectCreationOption[],
  selected: string[]
): boolean {
  const value = input.trim()
  if (!value) return false
  return !conflictsWithExistingValue(value, options, new Set(selected))
}

type MultiSelectCreationTransition = {
  draft: string
  nextSelected: string[]
  selectionChanged: boolean
}

export function transitionMultiSelectCreationInput({
  input,
  options,
  selected,
  commitDraft = false,
}: {
  input: string
  options: MultiSelectCreationOption[]
  selected: string[]
  commitDraft?: boolean
}): MultiSelectCreationTransition {
  const parsed = commitDraft
    ? { completed: [input.trim()].filter(Boolean), draft: '' }
    : splitMultiSelectDraft(input)
  const additions: string[] = []
  const seen = new Set(selected)
  for (const raw of parsed.completed) {
    const value = raw.trim()
    if (!value || conflictsWithExistingValue(value, options, seen)) continue
    seen.add(value)
    additions.push(value)
  }

  return {
    draft: parsed.draft,
    nextSelected: additions.length > 0 ? [...selected, ...additions] : selected,
    selectionChanged: additions.length > 0,
  }
}
