import assert from 'node:assert/strict'
import { describe, test } from 'node:test'
import { buildTagBatchEditPayload } from './tag-batch-edit'

describe('tag batch edit payload', () => {
  test('only serializes selected fields', () => {
    assert.deepEqual(
      buildTagBatchEditPayload({
        currentTag: 'pool',
        selectedFields: ['groups'],
        values: {
          newTag: 'renamed',
          models: 'gpt-new',
          modelMapping: '{"new":"new-upstream"}',
          groups: ['pro'],
        },
      }),
      {
        tag: 'pool',
        fields: ['groups'],
        groups: 'pro',
      }
    )
  })

  test('keeps explicit empty tag and mapping for selected clear operations', () => {
    assert.deepEqual(
      buildTagBatchEditPayload({
        currentTag: 'pool',
        selectedFields: ['tag', 'model_mapping'],
        values: {
          newTag: '',
          models: 'gpt-new',
          modelMapping: '',
          groups: ['pro'],
        },
      }),
      {
        tag: 'pool',
        fields: ['tag', 'model_mapping'],
        new_tag: '',
        model_mapping: '',
      }
    )
  })

  test('keeps explicit empty groups for selected clear operations', () => {
    assert.deepEqual(
      buildTagBatchEditPayload({
        currentTag: 'pool',
        selectedFields: ['groups'],
        values: {
          newTag: 'renamed',
          models: 'gpt-new',
          modelMapping: '{"new":"new-upstream"}',
          groups: [],
        },
      }),
      {
        tag: 'pool',
        fields: ['groups'],
        groups: '',
      }
    )
  })

  test('returns only tag and fields when no fields are selected', () => {
    assert.deepEqual(
      buildTagBatchEditPayload({
        currentTag: 'pool',
        selectedFields: [],
        values: {
          newTag: 'renamed',
          models: 'gpt-new',
          modelMapping: '{"new":"new-upstream"}',
          groups: ['pro'],
        },
      }),
      {
        tag: 'pool',
        fields: [],
      }
    )
  })
})
