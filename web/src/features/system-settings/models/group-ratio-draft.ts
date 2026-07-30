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

/** Keep a ratio input as text while the user is editing it. */
export function normalizeGroupRatioDraft(value: unknown): string {
  const parsed = Number(value)
  return Number.isFinite(parsed) ? String(parsed) : '1'
}

/** Convert a completed ratio draft to the numeric representation sent upstream. */
export function serializeGroupRatioDraft(value: string): number {
  const parsed = Number(value)
  return Number.isFinite(parsed) ? parsed : 1
}
