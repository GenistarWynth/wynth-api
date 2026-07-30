import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import { resolveModelListRequest } from './list-request'

describe('model list request policy', () => {
  test('uses search whenever any model filter is active', () => {
    const cases = [
      { keyword: 'gpt-4o' },
      { vendor: '12' },
      { status: 'enabled' },
      { syncOfficial: 'yes' },
    ]

    for (const filters of cases) {
      const request = resolveModelListRequest({
        ...filters,
        page: 1,
        pageSize: 10,
      })

      assert.equal(request.mode, 'search')
    }
  })

  test('uses list when filters are blank or all', () => {
    assert.deepEqual(
      resolveModelListRequest({
        keyword: '   ',
        vendor: 'all',
        status: 'all',
        syncOfficial: '',
        page: 2,
        pageSize: 20,
      }),
      {
        mode: 'list',
        params: { p: 2, page_size: 20 },
      }
    )
  })

  test('normalizes and includes every active filter with pagination', () => {
    assert.deepEqual(
      resolveModelListRequest({
        keyword: '  gpt 4o  ',
        vendor: ' 12 ',
        status: 'disabled',
        syncOfficial: 'no',
        page: 3,
        pageSize: 50,
      }),
      {
        mode: 'search',
        params: {
          keyword: 'gpt 4o',
          vendor: '12',
          status: 'disabled',
          sync_official: 'no',
          p: 3,
          page_size: 50,
        },
      }
    )
  })
})
