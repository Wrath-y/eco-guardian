import type { components } from '@/api/generated'
import type { EntityKind } from '@/forms/registry'
import { apiRequest, postJSON } from './api'

type Entity = components['schemas']['Entity']
type EntityDraft = components['schemas']['EntityDraft']
type EntityPage = components['schemas']['EntityPage']
type SaveEntityResponse = components['schemas']['SaveEntityResponse']
type ReferenceResolver = (kind: EntityKind, key: string) => string

interface BaseDataDefinition {
  kind: EntityKind
  key: string
  name: string
  description: string
  balanceGroup: string
  tagKeys: string[]
  payload: (resolve: ReferenceResolver) => EntityDraft['payload']
}

export interface BaseDataInitializationResult {
  created: number
  skipped: number
  createdByKind: Record<EntityKind, number>
}

const entityKinds: EntityKind[] = ['tag', 'attribute', 'effect', 'skill', 'item', 'character']

const baseDataDefinitions: BaseDataDefinition[] = [
  {
    kind: 'tag', key: 'starter_content', name: '基础内容', balanceGroup: 'starter_tags', tagKeys: [],
    description: '标记由基础数据初始化生成、适合继续扩展的配置。',
    payload: () => ({ category: 'gameplay', parent_tag_ids: [] }),
  },
  {
    kind: 'tag', key: 'player', name: '玩家', balanceGroup: 'faction_tags', tagKeys: [],
    description: '标记由玩家控制或归属于玩家阵营的角色。',
    payload: () => ({ category: 'faction', parent_tag_ids: [] }),
  },
  {
    kind: 'tag', key: 'enemy', name: '敌人', balanceGroup: 'faction_tags', tagKeys: [],
    description: '标记与玩家对抗的角色。',
    payload: () => ({ category: 'faction', parent_tag_ids: [] }),
  },
  {
    kind: 'attribute', key: 'health', name: '生命值', balanceGroup: 'combat_core', tagKeys: ['starter_content'],
    description: '角色能够承受伤害的总量，降至零时视为失去战斗能力。',
    payload: () => ({ value_type: 'decimal', dimension: 'health', base_unit: 'health_point', default: '100', min: '0' }),
  },
  {
    kind: 'attribute', key: 'attack_power', name: '攻击力', balanceGroup: 'combat_core', tagKeys: ['starter_content'],
    description: '衡量角色造成伤害的基础能力。',
    payload: () => ({ value_type: 'decimal', dimension: 'damage', base_unit: 'damage_point', default: '10', min: '0' }),
  },
  {
    kind: 'attribute', key: 'defense', name: '防御力', balanceGroup: 'combat_core', tagKeys: ['starter_content'],
    description: '衡量角色抵御攻击的基础能力。',
    payload: () => ({ value_type: 'decimal', dimension: 'scalar', base_unit: 'scalar', default: '0', min: '0' }),
  },
  {
    kind: 'effect', key: 'basic_damage', name: '基础伤害', balanceGroup: 'starter_effects', tagKeys: ['starter_content'],
    description: '立即使目标失去 10 点生命值。',
    payload: resolve => ({
      duration: '0',
      modifiers: [{ attribute_id: resolve('attribute', 'health'), operation: 'Add', value: '-10' }],
      trigger_blocks: [],
      stack_rule: { operation: 'Add', priority: '0', max_stacks: '1', refresh_policy: 'refresh' },
    }),
  },
  {
    kind: 'effect', key: 'minor_heal', name: '小型治疗', balanceGroup: 'starter_effects', tagKeys: ['starter_content'],
    description: '立即为目标恢复 20 点生命值。',
    payload: resolve => ({
      duration: '0',
      modifiers: [{ attribute_id: resolve('attribute', 'health'), operation: 'Add', value: '20' }],
      trigger_blocks: [],
      stack_rule: { operation: 'Add', priority: '0', max_stacks: '1', refresh_policy: 'refresh' },
    }),
  },
  {
    kind: 'effect', key: 'guard_bonus', name: '防御姿态', balanceGroup: 'starter_effects', tagKeys: ['starter_content'],
    description: '在 2 个时间单位内增加 5 点防御力。',
    payload: resolve => ({
      duration: '2',
      modifiers: [{ attribute_id: resolve('attribute', 'defense'), operation: 'Add', value: '5' }],
      trigger_blocks: [],
      stack_rule: { operation: 'Add', priority: '0', max_stacks: '1', refresh_policy: 'refresh' },
    }),
  },
  {
    kind: 'skill', key: 'basic_attack', name: '普通攻击', balanceGroup: 'starter_skills', tagKeys: ['starter_content'],
    description: '无冷却地对主要目标造成一次基础伤害。',
    payload: resolve => ({ costs: [], cooldown: '0', target_selector: { type: 'primary_target' }, effect_ids: [resolve('effect', 'basic_damage')], rule_blocks: [] }),
  },
  {
    kind: 'skill', key: 'first_aid', name: '急救', balanceGroup: 'starter_skills', tagKeys: ['starter_content'],
    description: '为自己恢复少量生命值。',
    payload: resolve => ({ costs: [], cooldown: '3', target_selector: { type: 'self' }, effect_ids: [resolve('effect', 'minor_heal')], rule_blocks: [] }),
  },
  {
    kind: 'skill', key: 'guard_stance', name: '防守姿态', balanceGroup: 'starter_skills', tagKeys: ['starter_content'],
    description: '短时间提升自己的防御力。',
    payload: resolve => ({ costs: [], cooldown: '4', target_selector: { type: 'self' }, effect_ids: [resolve('effect', 'guard_bonus')], rule_blocks: [] }),
  },
  {
    kind: 'item', key: 'training_sword', name: '训练木剑', balanceGroup: 'starter_items', tagKeys: ['starter_content'],
    description: '适合新手使用的基础近战武器，提供少量攻击力。',
    payload: resolve => ({
      slot: 'weapon', effect_ids: [], enhance_tag_ids: [], rule_blocks: [],
      attribute_modifiers: [{ attribute_id: resolve('attribute', 'attack_power'), operation: 'Add', value: '3' }],
    }),
  },
  {
    kind: 'item', key: 'cloth_armor', name: '布甲', balanceGroup: 'starter_items', tagKeys: ['starter_content'],
    description: '轻便的基础护甲，提供少量防御力。',
    payload: resolve => ({
      slot: 'armor', effect_ids: [], enhance_tag_ids: [], rule_blocks: [],
      attribute_modifiers: [{ attribute_id: resolve('attribute', 'defense'), operation: 'Add', value: '3' }],
    }),
  },
  {
    kind: 'item', key: 'healing_potion', name: '治疗药水', balanceGroup: 'starter_items', tagKeys: ['starter_content'],
    description: '使用后触发一次小型治疗的消耗品。',
    payload: resolve => ({ slot: 'consumable', effect_ids: [resolve('effect', 'minor_heal')], attribute_modifiers: [], enhance_tag_ids: [], rule_blocks: [] }),
  },
  {
    kind: 'character', key: 'trainee_guardian', name: '见习守卫', balanceGroup: 'starter_characters', tagKeys: ['starter_content', 'player'],
    description: '攻守均衡的入门角色，携带训练木剑和布甲。',
    payload: resolve => ({
      attribute_values: [
        { output_attribute_id: resolve('attribute', 'health'), expression: '100[health_point]' },
        { output_attribute_id: resolve('attribute', 'attack_power'), expression: '12[damage_point]' },
        { output_attribute_id: resolve('attribute', 'defense'), expression: '5' },
      ],
      skill_ids: [resolve('skill', 'basic_attack'), resolve('skill', 'guard_stance')],
      item_ids: [resolve('item', 'training_sword'), resolve('item', 'cloth_armor')],
      rule_blocks: [],
    }),
  },
  {
    kind: 'character', key: 'field_medic', name: '战地医师', balanceGroup: 'starter_characters', tagKeys: ['starter_content', 'player'],
    description: '拥有基础攻击和急救能力的支援角色。',
    payload: resolve => ({
      attribute_values: [
        { output_attribute_id: resolve('attribute', 'health'), expression: '80[health_point]' },
        { output_attribute_id: resolve('attribute', 'attack_power'), expression: '8[damage_point]' },
        { output_attribute_id: resolve('attribute', 'defense'), expression: '4' },
      ],
      skill_ids: [resolve('skill', 'basic_attack'), resolve('skill', 'first_aid')],
      item_ids: [resolve('item', 'healing_potion')],
      rule_blocks: [],
    }),
  },
  {
    kind: 'character', key: 'forest_slime', name: '森林史莱姆', balanceGroup: 'starter_characters', tagKeys: ['starter_content', 'enemy'],
    description: '数值简单、适合作为初始对手的敌方角色。',
    payload: resolve => ({
      attribute_values: [
        { output_attribute_id: resolve('attribute', 'health'), expression: '45[health_point]' },
        { output_attribute_id: resolve('attribute', 'attack_power'), expression: '7[damage_point]' },
        { output_attribute_id: resolve('attribute', 'defense'), expression: '2' },
      ],
      skill_ids: [resolve('skill', 'basic_attack')],
      item_ids: [],
      rule_blocks: [],
    }),
  },
]

function emptyKindCounts(): Record<EntityKind, number> {
  return { attribute: 0, tag: 0, character: 0, skill: 0, item: 0, effect: 0 }
}

export const baseDataCounts = Object.freeze(baseDataDefinitions.reduce((counts, definition) => {
  counts[definition.kind] += 1
  return counts
}, emptyKindCounts()))

export const baseDataTotal = baseDataDefinitions.length

function identity(kind: EntityKind, key: string) {
  return `${kind}:${key}`
}

async function listAllEntities(kind: EntityKind) {
  const entities: Entity[] = []
  let cursor = ''
  do {
    const params = new URLSearchParams({ limit: '200' })
    if (cursor) params.set('cursor', cursor)
    const page = await apiRequest<EntityPage>(`/api/v1/entities/${kind}?${params}`)
    entities.push(...page.items)
    cursor = page.next_cursor ?? ''
  } while (cursor)
  return entities
}

export async function initializeBaseData(): Promise<BaseDataInitializationResult> {
  const pages = await Promise.all(entityKinds.map(async kind => [kind, await listAllEntities(kind)] as const))
  const knownIDs = new Map<string, string>()
  for (const [kind, entities] of pages) {
    for (const entity of entities) knownIDs.set(identity(kind, entity.key), entity.id)
  }

  const result: BaseDataInitializationResult = { created: 0, skipped: 0, createdByKind: emptyKindCounts() }
  const resolve: ReferenceResolver = (kind, key) => {
    const id = knownIDs.get(identity(kind, key))
    if (!id) throw new Error(`基础数据依赖缺失：${kind}/${key}`)
    return id
  }

  for (const definition of baseDataDefinitions) {
    const definitionIdentity = identity(definition.kind, definition.key)
    if (knownIDs.has(definitionIdentity)) {
      result.skipped += 1
      continue
    }
    const draft: EntityDraft = {
      key: definition.key,
      name: definition.name,
      description: definition.description,
      tag_ids: definition.tagKeys.map(key => resolve('tag', key)),
      balance_group: definition.balanceGroup,
      payload: definition.payload(resolve),
      extensions: {},
    }
    const response = await postJSON<SaveEntityResponse>(`/api/v1/entities/${definition.kind}`, draft)
    knownIDs.set(definitionIdentity, response.entity.id)
    result.created += 1
    result.createdByKind[definition.kind] += 1
  }
  return result
}
