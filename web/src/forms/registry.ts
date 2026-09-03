import type { components } from '@/api/generated'

export type EntityKind = components['schemas']['EntityKind']
export type Entity = components['schemas']['Entity']
export type EntityDraft = components['schemas']['EntityDraft']
export type EntitySchema = components['schemas']['EntitySchema']
export type Payload = components['schemas']['EntityPayload']

export const entityKindLabels: Record<EntityKind, string> = {
  attribute: '属性',
  tag: '标签',
  character: '角色',
  skill: '技能',
  item: '物品',
  effect: '效果',
}

// This is deliberately a fixed, auditable renderer vocabulary. The server may
// supply schema metadata, but it never supplies executable UI or component names.
export const rendererMap = Object.freeze({
  scalar: 'TextField', enum: 'SelectField', object: 'ObjectBlock', array: 'ArrayBlock',
  entityReference: 'EntityReference', tagReference: 'TagReference',
})

export interface FieldIssue { path: string; message: string }

export function emptyPayload(kind: EntityKind): Payload {
  switch (kind) {
    case 'attribute': return { value_type: 'decimal', dimension: '', base_unit: '', default: '0' }
    case 'tag': return { category: '', parent_tag_ids: [] }
    case 'character': return { attribute_values: [], skill_ids: [], item_ids: [], rule_blocks: [] }
    case 'skill': return { costs: [], cooldown: '0', target_selector: { type: 'self' }, effect_ids: [], rule_blocks: [] }
    case 'item': return { slot: '', effect_ids: [], attribute_modifiers: [], enhance_tag_ids: [], rule_blocks: [] }
    case 'effect': return { duration: '0', modifiers: [], trigger_blocks: [], stack_rule: { operation: 'Add', max_stacks: '1', refresh_policy: 'refresh' } }
  }
}

export function emptyDraft(kind: EntityKind): EntityDraft {
  return { key: '', name: '', description: '', tag_ids: [], balance_group: '', payload: emptyPayload(kind), extensions: {} }
}

function record(value: unknown): Record<string, unknown> {
  return value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : {}
}

function omitEmpty(value: Record<string, unknown>, keys: string[]) {
  for (const key of keys) if (value[key] === '' || value[key] === undefined || value[key] === null) delete value[key]
}

function stringifyNumbers(value: Record<string, unknown>, keys: string[]) {
  for (const key of keys) if (typeof value[key] === 'number') value[key] = String(value[key])
}

function prepareSelector(value: unknown) {
  const selector = record(value)
  if (selector.type !== 'targets_with_tag') delete selector.tag_id
  else omitEmpty(selector, ['tag_id'])
  return selector
}

function prepareTriggers(value: unknown) {
  if (!Array.isArray(value)) return
  for (const item of value) {
    const trigger = record(item)
    omitEmpty(trigger, ['condition', 'termination_budget'])
    stringifyNumbers(trigger, ['termination_budget'])
    trigger.target = prepareSelector(trigger.target)
  }
}

function prepareModifiers(value: unknown) {
  if (!Array.isArray(value)) return
  for (const item of value) stringifyNumbers(record(item), ['value'])
}

/** Converts form values into the existing API payload shape and removes blank optional fields. */
export function preparePayload(kind: EntityKind, value: unknown): Payload {
  const payload = record(JSON.parse(JSON.stringify(value ?? {})) as unknown)
  switch (kind) {
    case 'attribute':
      omitEmpty(payload, ['min', 'max', 'display_scale'])
      stringifyNumbers(payload, ['default', 'min', 'max', 'display_scale'])
      if (payload.value_type === 'boolean') {
        delete payload.min
        delete payload.max
        delete payload.display_scale
      }
      break
    case 'character':
      prepareTriggers(payload.rule_blocks)
      break
    case 'skill':
      stringifyNumbers(payload, ['cooldown'])
      payload.target_selector = prepareSelector(payload.target_selector)
      prepareTriggers(payload.rule_blocks)
      break
    case 'item':
      prepareModifiers(payload.attribute_modifiers)
      prepareTriggers(payload.rule_blocks)
      break
    case 'effect': {
      stringifyNumbers(payload, ['duration'])
      prepareModifiers(payload.modifiers)
      prepareTriggers(payload.trigger_blocks)
      const stackRule = record(payload.stack_rule)
      omitEmpty(stackRule, ['priority', 'cap'])
      stringifyNumbers(stackRule, ['priority', 'max_stacks', 'cap'])
      payload.stack_rule = stackRule
      break
    }
    case 'tag':
      break
  }
  return payload as Payload
}

export function validateDraft(value: Record<string, unknown>, kind: EntityKind): FieldIssue[] {
  const issues: FieldIssue[] = []
  const required = (v: unknown, path: string, label: string) => { if (v === undefined || v === null || v === '') issues.push({ path, message: `${label} 为必填项` }) }
  required(value.key, 'key', 'Key'); required(value.name, 'name', '名称')
  if (typeof value.key === 'string' && value.key && !/^[a-z][a-z0-9_]*$/.test(value.key)) issues.push({ path: 'key', message: 'Key 必须以小写字母开头，仅可使用小写字母、数字和下划线' })
  const payload = (value.payload ?? {}) as Record<string, unknown>
  const fields: Record<EntityKind, string[]> = {
    attribute: ['value_type', 'dimension', 'base_unit', 'default'], tag: ['category', 'parent_tag_ids'],
    character: ['attribute_values', 'skill_ids', 'item_ids', 'rule_blocks'], skill: ['costs', 'cooldown', 'target_selector', 'effect_ids', 'rule_blocks'],
    item: ['slot', 'effect_ids', 'attribute_modifiers', 'enhance_tag_ids', 'rule_blocks'], effect: ['duration', 'modifiers', 'trigger_blocks', 'stack_rule'],
  }
  for (const field of fields[kind]) required(payload[field], `payload.${field}`, field)
  return issues
}
