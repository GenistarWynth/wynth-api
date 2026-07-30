import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import {
  loadRedemptionList,
  resolveRedemptionListRequest,
  serializeRedemptionListParams,
} from './list-request'

describe('redemption list request policy', () => {
  test('uses search when status is the only filter', () => {
    assert.deepEqual(
      resolveRedemptionListRequest({
        keyword: '',
        status: ['expired'],
        page: 2,
        pageSize: 20,
      }),
      {
        mode: 'search',
        params: {
          keyword: '',
          status: 'expired',
          p: 2,
          page_size: 20,
        },
      }
    )
  })

  test('uses list only when keyword and status are both empty', () => {
    assert.deepEqual(
      resolveRedemptionListRequest({
        keyword: '   ',
        status: [],
        page: 1,
        pageSize: 10,
      }),
      {
        mode: 'list',
        params: { p: 1, page_size: 10 },
      }
    )
  })

  test('serializes keyword status and pagination with URLSearchParams', () => {
    assert.equal(
      serializeRedemptionListParams({
        keyword: '兑换 码&vip',
        status: 'expired',
        p: 3,
        page_size: 50,
      }),
      'keyword=%E5%85%91%E6%8D%A2+%E7%A0%81%26vip&status=expired&p=3&page_size=50'
    )
  })

  test('reports a rejected request and rethrows the original error', async () => {
    const request = resolveRedemptionListRequest({
      keyword: 'vip',
      status: [],
      page: 1,
      pageSize: 10,
    })
    const rejection = new Error('network unavailable')
    const reported: Array<{ error: unknown; mode: string }> = []

    await assert.rejects(
      loadRedemptionList(request, {
        list: async () => {
          throw new Error('list dependency must not be called')
        },
        search: async () => {
          throw rejection
        },
        onRejected: (error, mode) => reported.push({ error, mode }),
      }),
      (error) => error === rejection
    )

    assert.deepEqual(reported, [{ error: rejection, mode: 'search' }])
  })
})
