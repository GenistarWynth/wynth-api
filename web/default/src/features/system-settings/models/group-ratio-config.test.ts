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

import {
  collectConfiguredGroupNames,
  parseAutoGroups,
  parseGroupDescriptionMap,
  parseGroupRatioMap,
  parseNestedGroupRatioMap,
  parseSpecialUsableRules,
  serializeSpecialUsableRules,
  setSpecialUsableRuleVisibility,
} from './group-ratio-config'

describe('group ratio JSON parsing', () => {
  test('rejects invalid container shapes instead of exposing unsafe values', () => {
    assert.deepEqual(parseGroupRatioMap('null'), {})
    assert.deepEqual(parseGroupRatioMap('[1, 2]'), {})
    assert.deepEqual(parseGroupDescriptionMap('"default"'), {})
    assert.deepEqual(parseGroupDescriptionMap('{"default":3,"vip":null}'), {
      default: '3',
      vip: '',
    })
    assert.deepEqual(parseNestedGroupRatioMap('{"vip":null,"default":[]}'), {})
    assert.deepEqual(parseAutoGroups('{"0":"default"}'), [])
    assert.deepEqual(parseSpecialUsableRules('[]'), [])
  })

  test('collects the complete registry without losing top-up-only groups', () => {
    assert.deepEqual(
      collectConfiguredGroupNames({
        groupRatio: '{"default":1,"zero":0}',
        userUsableGroups: '{"vip":"VIP"}',
        topupGroupRatio: '{"partner":1.2,"default":1}',
      }),
      ['default', 'zero', 'vip', 'partner']
    )
  })
})

describe('special usable group rule round trips', () => {
  test('preserves prefixed and unprefixed visible rules', () => {
    const source = {
      vip: {
        '+:premium': 'Half price',
        '-:default': 'custom removal marker',
        special: 'Special group',
      },
    }

    const rules = parseSpecialUsableRules(JSON.stringify(source))

    assert.deepEqual(
      rules.map((rule) => ({
        userGroup: rule.userGroup,
        visible: rule.visible,
        visibleKeyStyle: rule.visibleKeyStyle,
        targetGroup: rule.targetGroup,
        description: rule.description,
      })),
      [
        {
          userGroup: 'vip',
          visible: true,
          visibleKeyStyle: 'prefixed',
          targetGroup: 'premium',
          description: 'Half price',
        },
        {
          userGroup: 'vip',
          visible: false,
          visibleKeyStyle: 'prefixed',
          targetGroup: 'default',
          description: 'custom removal marker',
        },
        {
          userGroup: 'vip',
          visible: true,
          visibleKeyStyle: 'plain',
          targetGroup: 'special',
          description: 'Special group',
        },
      ]
    )
    assert.deepEqual(JSON.parse(serializeSpecialUsableRules(rules)), source)
  })

  test('changes visibility without discarding the original visible key style', () => {
    const [plainRule] = parseSpecialUsableRules(
      '{"vip":{"special":"Special group"}}'
    )

    const hiddenRule = setSpecialUsableRuleVisibility(plainRule, false)
    assert.deepEqual(JSON.parse(serializeSpecialUsableRules([hiddenRule])), {
      vip: { '-:special': 'remove' },
    })

    const visibleRule = setSpecialUsableRuleVisibility(hiddenRule, true)
    assert.deepEqual(JSON.parse(serializeSpecialUsableRules([visibleRule])), {
      vip: { special: '' },
    })
  })
})
