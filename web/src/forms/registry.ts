import type { components } from '@/api/generated'

export type EntityKind = components['schemas']['EntityKind']
export type Entity = components['schemas']['Entity']
export type EntityDraft = components['schemas']['EntityDraft']
export type EntitySchema = components['schemas']['EntitySchema']
export type Payload = components['schemas']['EntityPayload']

// This is deliberately a fixed, auditable renderer vocabulary. The server may
// supply schema metadata, but it never supplies executable UI or component names.
export const rendererMap = Object.freeze({
  scalar: 'TextField', enum: 'SelectField', object: 'ObjectBlock', array: 'ArrayBlock',
  entityReference: 'EntityReference', tagReference: 'TagReference',
})

export interface FieldIssue { path: string; message: string }

export function emptyPayload(kind: EntityKind): Payload {
  switch (kind) {
    case 'attribute': return { value_type: 'decimal', dimension: '', base_unit: '', default: 0 }
    case 'tag': return { category: '', parent_tag_ids: [] }
    case 'character': return { attribute_values: [], skill_ids: [], item_ids: [], rule_blocks: [] }
    case 'skill': return { costs: [], cooldown: 0, target_selector: { type: 'self' }, effect_ids: [], rule_blocks: [] }
    case 'item': return { slot: '', effect_ids: [], attribute_modifiers: [], enhance_tag_ids: [], rule_blocks: [] }
    case 'effect': return { duration: 0, modifiers: [], trigger_blocks: [], stack_rule: { operation: 'Add', max_stacks: 1, refresh_policy: 'refresh' } }
  }
}

export function emptyDraft(kind: EntityKind): EntityDraft {
  return { key: '', name: '', description: '', tag_ids: [], balance_group: '', payload: emptyPayload(kind), extensions: {} }
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
