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
export const HOME_IFRAME_SANDBOX =
  'allow-forms allow-popups allow-popups-to-escape-sandbox allow-scripts allow-top-navigation-by-user-activation'

type IframeMessageTarget = {
  postMessage(message: unknown, targetOrigin: string): void
}

export function postHomeIframePreferences(
  target: IframeMessageTarget | null | undefined,
  theme: string,
  language: string
): void {
  try {
    target?.postMessage({ themeMode: theme }, '*')
    target?.postMessage({ lang: language }, '*')
  } catch {
    // Cross-origin frames may reject access while navigating.
  }
}
