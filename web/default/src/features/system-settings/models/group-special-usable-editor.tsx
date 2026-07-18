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
import {
  AlertTriangle,
  ChevronDown,
  ChevronUp,
  Plus,
  Trash2,
} from 'lucide-react'
import { useCallback, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'

import {
  parseSpecialUsableRules,
  serializeSpecialUsableRules,
  setSpecialUsableRuleVisibility,
  type SpecialUsableRule,
} from './group-ratio-config'

const sectionCardClassName =
  'relative shadow-sm ring-0 before:pointer-events-none before:absolute before:inset-0 before:rounded-xl before:border before:border-border/90'
const sectionHeaderClassName = 'border-b bg-muted/20'

type Rule = SpecialUsableRule & { _id: string }

let _idCounter = 0
function uid() {
  return `gsu_${++_idCounter}`
}

function flattenRules(value: string): Rule[] {
  return parseSpecialUsableRules(value).map((rule) => ({
    ...rule,
    _id: uid(),
  }))
}

type GroupSpecialUsableRulesEditorProps = {
  value: string
  groupOptions: string[]
  onChange: (value: string) => void
}

type GroupSelectProps = {
  options: string[]
  value: string
  placeholder: string
  onValueChange: (value: string) => void
  className?: string
}

function GroupSelect(props: GroupSelectProps) {
  const knownOptions = useMemo(() => {
    if (props.value && !props.options.includes(props.value)) {
      return [props.value, ...props.options]
    }
    return props.options
  }, [props.options, props.value])

  return (
    <Select
      value={props.value === '' ? null : props.value}
      onValueChange={(value) => {
        if (typeof value === 'string' && value !== '') {
          props.onValueChange(value)
        }
      }}
    >
      <SelectTrigger className={props.className}>
        <SelectValue placeholder={props.placeholder} />
      </SelectTrigger>
      <SelectContent alignItemWithTrigger={false}>
        <SelectGroup>
          {knownOptions.map((name) => (
            <SelectItem key={name} value={name}>
              {name}
            </SelectItem>
          ))}
        </SelectGroup>
      </SelectContent>
    </Select>
  )
}

type GroupSectionProps = {
  groupName: string
  items: Rule[]
  groupOptions: string[]
  onUpdate: (
    id: string,
    field: 'visible' | 'targetGroup' | 'description',
    value: string | boolean
  ) => void
  onRemove: (id: string) => void
  onAdd: (groupName: string) => void
  onRemoveGroup: (groupName: string) => void
}

function GroupSection(props: GroupSectionProps) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const isKnownGroup = props.groupOptions.includes(props.groupName)

  return (
    <Collapsible open={open} onOpenChange={setOpen}>
      <div className='rounded-lg border'>
        <div className='flex items-center justify-between p-3'>
          <div className='flex items-center gap-2'>
            <CollapsibleTrigger
              render={
                <Button variant='ghost' size='sm' className='h-6 w-6 p-0' />
              }
            >
              {open ? (
                <ChevronUp className='h-4 w-4' />
              ) : (
                <ChevronDown className='h-4 w-4' />
              )}
            </CollapsibleTrigger>
            <span className='font-semibold'>{props.groupName}</span>
            {!isKnownGroup && (
              <StatusBadge variant='danger' copyable={false}>
                <AlertTriangle className='mr-1 h-3 w-3' />
                {t('Not in pricing table')}
              </StatusBadge>
            )}
            <StatusBadge variant='neutral' copyable={false}>
              {props.items.length} {t('rules')}
            </StatusBadge>
          </div>
          <div className='flex items-center gap-1'>
            <Button
              variant='ghost'
              size='sm'
              className='h-7 w-7 p-0'
              onClick={() => props.onAdd(props.groupName)}
            >
              <Plus className='h-4 w-4' />
            </Button>
            <Button
              variant='ghost'
              size='sm'
              className='text-destructive h-7 w-7 p-0'
              onClick={() => props.onRemoveGroup(props.groupName)}
            >
              <Trash2 className='h-4 w-4' />
            </Button>
          </div>
        </div>
        <CollapsibleContent>
          <div className='space-y-2 border-t p-3'>
            {props.items.map((rule) => (
              <div key={rule._id} className='flex items-center gap-2'>
                <Select
                  value={rule.visible ? 'visible' : 'hidden'}
                  onValueChange={(value) =>
                    value !== null &&
                    props.onUpdate(rule._id, 'visible', value === 'visible')
                  }
                >
                  <SelectTrigger className='w-[130px]'>
                    <SelectValue>
                      <StatusBadge
                        label={rule.visible ? t('Extra visible') : t('Hidden')}
                        variant={rule.visible ? 'info' : 'danger'}
                        copyable={false}
                      />
                    </SelectValue>
                  </SelectTrigger>
                  <SelectContent alignItemWithTrigger={false}>
                    <SelectGroup>
                      <SelectItem value='visible'>
                        <StatusBadge
                          label={t('Extra visible')}
                          variant='info'
                          copyable={false}
                        />
                      </SelectItem>
                      <SelectItem value='hidden'>
                        <StatusBadge
                          label={t('Hidden')}
                          variant='danger'
                          copyable={false}
                        />
                      </SelectItem>
                    </SelectGroup>
                  </SelectContent>
                </Select>
                <div className='flex flex-1 items-center gap-1.5'>
                  <GroupSelect
                    className='flex-1'
                    options={props.groupOptions}
                    value={rule.targetGroup}
                    placeholder={t('Group name')}
                    onValueChange={(value) =>
                      props.onUpdate(rule._id, 'targetGroup', value)
                    }
                  />
                  {rule.targetGroup &&
                    !props.groupOptions.includes(rule.targetGroup) && (
                      <AlertTriangle
                        className='text-destructive h-4 w-4 shrink-0'
                        aria-label={t('Not in pricing table')}
                      />
                    )}
                </div>
                {rule.visible ? (
                  <Input
                    className='flex-1'
                    value={rule.description}
                    placeholder={t('Description')}
                    onChange={(event) =>
                      props.onUpdate(
                        rule._id,
                        'description',
                        event.target.value
                      )
                    }
                  />
                ) : (
                  <div className='text-muted-foreground flex-1 px-3 text-sm'>
                    -
                  </div>
                )}
                <Button
                  variant='ghost'
                  size='sm'
                  className='text-destructive h-8 w-8 p-0'
                  onClick={() => props.onRemove(rule._id)}
                >
                  <Trash2 className='h-4 w-4' />
                </Button>
              </div>
            ))}
          </div>
        </CollapsibleContent>
      </div>
    </Collapsible>
  )
}

export function GroupSpecialUsableRulesEditor(
  props: GroupSpecialUsableRulesEditorProps
) {
  const { t } = useTranslation()
  const [rules, setRules] = useState<Rule[]>(() => flattenRules(props.value))
  const onChange = props.onChange

  const emitChange = useCallback(
    (newRules: Rule[]) => {
      setRules(newRules)
      onChange(serializeSpecialUsableRules(newRules))
    },
    [onChange]
  )

  const updateRule = useCallback(
    (
      id: string,
      field: 'visible' | 'targetGroup' | 'description',
      value: string | boolean
    ) => {
      emitChange(
        rules.map((rule) => {
          if (rule._id !== id) return rule
          if (field === 'visible' && typeof value === 'boolean') {
            return {
              ...rule,
              ...setSpecialUsableRuleVisibility(rule, value),
            }
          }
          if (field === 'targetGroup' && typeof value === 'string') {
            return { ...rule, targetGroup: value }
          }
          if (field === 'description' && typeof value === 'string') {
            return { ...rule, description: value }
          }
          return rule
        })
      )
    },
    [rules, emitChange]
  )

  const removeRule = useCallback(
    (id: string) => emitChange(rules.filter((rule) => rule._id !== id)),
    [rules, emitChange]
  )

  const removeGroup = useCallback(
    (groupName: string) =>
      emitChange(rules.filter((rule) => rule.userGroup !== groupName)),
    [rules, emitChange]
  )

  const addRuleToGroup = useCallback(
    (groupName: string) => {
      emitChange([
        ...rules,
        {
          _id: uid(),
          userGroup: groupName,
          visible: true,
          visibleKeyStyle: 'prefixed',
          targetGroup: '',
          description: '',
        },
      ])
    },
    [rules, emitChange]
  )

  const grouped = useMemo(() => {
    const map: Record<string, Rule[]> = {}
    const order: string[] = []
    for (const rule of rules) {
      if (!rule.userGroup) continue
      if (!map[rule.userGroup]) {
        map[rule.userGroup] = []
        order.push(rule.userGroup)
      }
      map[rule.userGroup].push(rule)
    }
    return order.map((name) => ({ name, items: map[name] }))
  }, [rules])

  const newGroupCandidates = useMemo(() => {
    const usedGroups = new Set(grouped.map((group) => group.name))
    return props.groupOptions.filter((name) => !usedGroups.has(name))
  }, [grouped, props.groupOptions])

  return (
    <Card className={sectionCardClassName}>
      <CardHeader className={sectionHeaderClassName}>
        <CardTitle>{t('Special usable group rules')}</CardTitle>
        <CardDescription>
          {t(
            'Make extra groups visible to, or hide default groups from, users of a specific group.'
          )}
        </CardDescription>
      </CardHeader>
      <CardContent>
        <div className='space-y-3'>
          {grouped.length === 0 ? (
            <p className='text-muted-foreground py-4 text-center text-sm'>
              {t('No rules yet. Add a group below to get started.')}
            </p>
          ) : (
            grouped.map((group) => (
              <GroupSection
                key={group.name}
                groupName={group.name}
                items={group.items}
                groupOptions={props.groupOptions}
                onUpdate={updateRule}
                onRemove={removeRule}
                onAdd={addRuleToGroup}
                onRemoveGroup={removeGroup}
              />
            ))
          )}

          <div className='flex items-center justify-center pt-2'>
            <GroupSelect
              className='w-[240px]'
              options={newGroupCandidates}
              value=''
              placeholder={t('Add rules for a user group')}
              onValueChange={addRuleToGroup}
            />
          </div>
        </div>
      </CardContent>
    </Card>
  )
}
