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
import { AxiosError } from 'axios'

export type QueryErrorNavigation =
  | { to: '/sign-in'; search: { redirect: string } }
  | { to: '/500' }

type QueryErrorHandlerDependencies = {
  translate: (key: string) => string
  toastError: (message: string) => void
  resetAuth: () => void
  getCurrentHref: () => string
  navigate: (options: QueryErrorNavigation) => void
}

export function handleQueryError(
  error: unknown,
  dependencies: QueryErrorHandlerDependencies
): void {
  if (!(error instanceof AxiosError)) return

  if (error.response?.status === 401) {
    dependencies.toastError(dependencies.translate('Session expired!'))
    dependencies.resetAuth()
    dependencies.navigate({
      to: '/sign-in',
      search: { redirect: dependencies.getCurrentHref() },
    })
    return
  }

  if (error.response?.status === 500) {
    dependencies.toastError(dependencies.translate('Internal Server Error!'))
    dependencies.navigate({ to: '/500' })
  }
}
