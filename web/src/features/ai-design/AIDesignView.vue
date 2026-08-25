<script setup lang="ts">
import { computed, nextTick, reactive, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import type { components } from '@/api/generated'
import { useRevisionHistory } from '@/api/versions'
import { useProjectStore } from '@/stores/project'
import AIJobProgress from './AIJobProgress.vue'
import DraftPatchReview from './DraftPatchReview.vue'
import ProviderSettingsPanel from './ProviderSettingsPanel.vue'
import { AIApiError, createAIDesignJob, type AIDesignRequest, useAICapability } from './api'

type EntityKind = components['schemas']['EntityKind']
type EntityOption = { id: string; name: string; key: string; entity_version: number }
type TargetForm = { kind: EntityKind; entity_id: string; path: string; replace: boolean; add: boolean; remove: boolean }
const kinds: EntityKind[] = ['attribute', 'tag', 'character', 'skill', 'item', 'effect']
const pathOptions: Record<EntityKind, string[]> = {
  attribute: ['/payload/default', '/payload/min', '/payload/max', '/payload/display_scale'], tag: ['/payload/category', '/payload/parent_tag_ids'],
  character: ['/payload/attribute_values', '/payload/skill_ids', '/payload/item_ids', '/payload/rule_blocks'], skill: ['/payload/cooldown', '/payload/costs', '/payload/effect_ids', '/payload/rule_blocks'],
  item: ['/payload/slot', '/payload/effect_ids', '/payload/attribute_modifiers', '/payload/enhance_tag_ids', '/payload/rule_blocks'], effect: ['/payload/duration', '/payload/modifiers', '/payload/trigger_blocks', '/payload/stack_rule'],
}
const project = useProjectStore()
const projectID = computed(() => project.current?.id ?? '')
const history = useRevisionHistory(() => projectID.value)
const capability = useAICapability(() => projectID.value)
const catalogs = ref<Partial<Record<EntityKind, EntityOption[]>>>({})
const catalogLoading = ref<Partial<Record<EntityKind, boolean>>>({})
const form = reactive({ base_revision_id: '', goal_id: 'balance', goal: '', metric_id: 'metric-dps', metric_version: 'v1', direction: 'minimize' as 'minimize' | 'maximize' | 'target', target: '', unit: 'points_per_second', constraint_id: '', constraint_path: '', constraint_operator: 'equal' as components['schemas']['AIConstraint']['operator'], constraint_value: '', scene: 'single-target-30s', targets: [{ kind: 'skill', entity_id: '', path: '/payload/cooldown', replace: true, add: false, remove: false }] as TargetForm[] })
const submitting = ref(false)
const error = ref('')
const errorSummary = ref<HTMLElement>()
const jobID = ref('')
const patchID = ref('')
const lastRequest = ref<AIDesignRequest>()
let attempt = 0

const revisions = computed(() => history.data.value?.items ?? [])
const ready = computed(() => Boolean(form.base_revision_id && form.goal.trim() && form.metric_id.trim() && form.targets.length && form.targets.every(target => target.entity_id && target.path && (target.replace || target.add || target.remove)) && (capability.data.value?.state === 'available' || capability.data.value?.state === 'degraded')))
function key() { attempt += 1; return `ai-design-${Date.now()}-${attempt}` }
function addTarget() { form.targets.push({ kind: 'skill', entity_id: '', path: '/payload/cooldown', replace: true, add: false, remove: false }); void loadKind('skill') }
function removeTarget(index: number) { if (form.targets.length > 1) form.targets.splice(index, 1) }
async function loadKind(kind: EntityKind) {
  if (catalogs.value[kind] || catalogLoading.value[kind]) return
  catalogLoading.value = { ...catalogLoading.value, [kind]: true }
  try {
    const response = await fetch(`/api/v1/entities/${kind}?limit=200`)
    if (response.ok) catalogs.value = { ...catalogs.value, [kind]: (await response.json() as { items: EntityOption[] }).items }
  } finally { catalogLoading.value = { ...catalogLoading.value, [kind]: false } }
}
function targetChanged(target: TargetForm) { target.entity_id = ''; target.path = pathOptions[target.kind][0]; void loadKind(target.kind) }
function buildRequest(): AIDesignRequest {
  const constraints: components['schemas']['AIConstraint'][] = []
  if (form.constraint_id.trim()) {
    let value: unknown = form.constraint_value
    if (form.constraint_value.trim()) value = JSON.parse(form.constraint_value)
    constraints.push({ id: form.constraint_id.trim(), path: form.constraint_path.trim(), operator: form.constraint_operator, value })
  }
  return {
    base_revision_id: form.base_revision_id, goals: [{ id: form.goal_id.trim(), description: form.goal.trim() }],
    metrics: [{ metric_id: form.metric_id.trim(), version: form.metric_version.trim(), direction: form.direction, ...(form.target.trim() ? { target: form.target.trim() } : {}), unit: form.unit.trim() }],
    constraints, scenes: [form.scene], allowed_targets: form.targets.map(target => {
      const entity = catalogs.value[target.kind]?.find(value => value.id === target.entity_id)
      return { entity_id: target.entity_id, kind: target.kind, expected_entity_version: entity?.entity_version ?? 0, paths: [{ path: target.path, operations: [target.replace ? 'replace' as const : null, target.add ? 'add' as const : null, target.remove ? 'remove' as const : null].filter((value): value is 'replace' | 'add' | 'remove' => value !== null) }] }
    }),
  }
}
async function submit(request?: AIDesignRequest) {
  submitting.value = true; error.value = ''
  try {
    const frozen = request ?? buildRequest()
    const accepted = await createAIDesignJob(frozen, key())
    lastRequest.value = structuredClone(frozen); jobID.value = accepted.job.id; patchID.value = accepted.job.result_type === 'draft_patch' && accepted.job.result_id ? accepted.job.result_id : ''
    sessionStorage.setItem(`ai-design-active-job:${projectID.value}`, jobID.value)
  } catch (cause) {
    const issue = cause as AIApiError
    error.value = issue.code ? `${issue.message}（${issue.code}${issue.retryable ? '，可重试' : ''}）` : issue.message
    await nextTick(); errorSummary.value?.focus()
  } finally { submitting.value = false }
}
function retry() { if (lastRequest.value) void submit(lastRequest.value) }
function patchReady(id: string) { patchID.value = id; sessionStorage.removeItem(`ai-design-active-job:${projectID.value}`) }
watch(revisions, values => { if (!form.base_revision_id && values.length) form.base_revision_id = values[0].id }, { immediate: true })
watch(projectID, value => { jobID.value = value ? sessionStorage.getItem(`ai-design-active-job:${value}`) ?? '' : ''; patchID.value = ''; void loadKind('skill') }, { immediate: true })
</script>

<template>
  <main class="ai-page">
    <nav><RouterLink to="/projects">项目</RouterLink> · <RouterLink to="/versions">版本历史</RouterLink> · <RouterLink to="/simulations">模拟</RouterLink> · <RouterLink to="/risk-reviews">风险复核</RouterLink></nav>
    <h1>AI 平衡设计</h1>
    <p>AI 只能生成可审阅 DraftPatch；没有 Repository、发布或 Graph activation 权限。接受后仍需独立发布确认。</p>
    <ProviderSettingsPanel :project-i-d="projectID" />

    <section class="ai-card" aria-labelledby="input-heading">
      <h2 id="input-heading">冻结输入与允许范围</h2>
      <p v-if="history.isPending.value" role="status">正在读取不可变 revision…</p>
      <p v-else-if="!revisions.length" role="alert">没有可选择的不可变 base revision。请先创建配置 revision。</p>
      <div v-if="error" ref="errorSummary" tabindex="-1" role="alert" aria-live="assertive" class="decision-summary">{{ error }}<p>表单输入已保留。</p></div>
      <form class="form-grid" aria-label="创建 AI 设计 Job" @submit.prevent="submit()">
        <label>Base revision <select v-model="form.base_revision_id"><option value="">请选择</option><option v-for="revision in revisions" :key="revision.id" :value="revision.id">#{{ revision.display_revision }} · {{ revision.id }}</option></select></label>
        <label>目标 ID <input v-model="form.goal_id" maxlength="128"></label><label>目标说明 <textarea v-model="form.goal" maxlength="4000"></textarea></label>
        <fieldset><legend>Metric</legend><label>ID <input v-model="form.metric_id"></label><label>Version <input v-model="form.metric_version"></label><label>方向 <select v-model="form.direction"><option value="minimize">minimize</option><option value="maximize">maximize</option><option value="target">target</option></select></label><label>Target（可选） <input v-model="form.target"></label><label>Unit <input v-model="form.unit"></label></fieldset>
        <fieldset><legend>Constraint（可选，typed JSON）</legend><label>ID <input v-model="form.constraint_id"></label><label>JSON Pointer <input v-model="form.constraint_path" placeholder="/payload/cooldown"></label><label>Operator <select v-model="form.constraint_operator"><option v-for="value in ['equal','not_equal','less','less_or_equal','greater','greater_or_equal','in','range']" :key="value" :value="value">{{ value }}</option></select></label><label>JSON value <input v-model="form.constraint_value" placeholder="&quot;10&quot;"></label></fieldset>
        <label>固定 Scene <select v-model="form.scene"><option value="single-target-30s">single-target-30s</option><option value="multi-target-60s">multi-target-60s</option></select></label>
        <fieldset><legend>允许目标、字段与操作</legend><article v-for="(target, index) in form.targets" :key="index" class="target-input"><label>Kind <select v-model="target.kind" @change="targetChanged(target)"><option v-for="kind in kinds" :key="kind" :value="kind">{{ kind }}</option></select></label><label>Stable entity <select v-model="target.entity_id" :aria-label="`目标 ${index + 1} entity`"><option value="">请选择</option><option v-for="entity in catalogs[target.kind] ?? []" :key="entity.id" :value="entity.id">{{ entity.name }}（{{ entity.key }}，v{{ entity.entity_version }}）</option></select></label><label>允许字段 <select v-model="target.path"><option v-for="path in pathOptions[target.kind]" :key="path" :value="path">{{ path }}</option></select></label><span>操作：</span><label><input v-model="target.replace" type="checkbox"> replace</label><label><input v-model="target.add" type="checkbox"> add</label><label><input v-model="target.remove" type="checkbox"> remove</label><button type="button" :disabled="form.targets.length === 1" @click="removeTarget(index)">移除此目标</button></article><button type="button" @click="addTarget">添加目标</button></fieldset>
        <p v-if="capability.data.value">服务端预算：最多 {{ capability.data.value.limits.max_format_repairs }} 轮 repair、{{ capability.data.value.limits.max_tool_calls }} 次工具调用、{{ capability.data.value.limits.max_search_candidates }} 个搜索候选；客户端不能扩大。</p>
        <p v-if="capability.data.value && capability.data.value.state !== 'available' && capability.data.value.state !== 'degraded'" role="alert">Provider 状态 {{ capability.data.value.state }}，生成按钮由服务端能力投影禁用。</p>
        <button type="submit" :disabled="submitting || !ready">{{ submitting ? '正在固定输入并接收 Job…' : '生成 DraftPatch 候选' }}</button>
      </form>
    </section>

    <AIJobProgress v-if="jobID" :project-i-d="projectID" :job-i-d="jobID" @patch-ready="patchReady" @retry="retry" />
    <DraftPatchReview v-if="patchID" :project-i-d="projectID" :patch-i-d="patchID" />
    <section v-else-if="!jobID" class="ai-card" aria-label="候选空态"><p>尚无候选。配置 Provider、选择冻结输入和允许范围后再生成。</p></section>
  </main>
</template>

<style scoped>
.ai-page { max-width: 1440px; margin: 0 auto; padding: 1.25rem; color: #172033; background: #f6f8fb; }
:deep(.ai-card) { margin: 1rem 0; padding: 1rem; border: 1px solid #718096; border-radius: .6rem; background: #fff; }
:deep(.form-grid) { display: grid; gap: .8rem; }
:deep(label) { display: flex; gap: .45rem; align-items: center; flex-wrap: wrap; }
:deep(input), :deep(select), :deep(textarea) { border: 1px solid #596579; border-radius: .25rem; padding: .4rem; background: #fff; color: #101828; }
:deep(textarea) { min-height: 5rem; min-width: min(36rem, 100%); }
:deep(button) { border: 1px solid #164e87; border-radius: .3rem; padding: .45rem .75rem; background: #075ea8; color: #fff; }
:deep(button:disabled) { background: #667085; border-color: #667085; }
:deep(.cloud-warning), :deep(.decision-summary) { border-left: .35rem solid #8a4b08; padding: .75rem; background: #fff4e5; }
:deep(.target-input), :deep(.target-diff), :deep(.evidence-card) { border: 1px solid #a0aec0; padding: .75rem; margin: .6rem 0; }
:deep(table) { width: 100%; border-collapse: collapse; }
:deep(th), :deep(td) { border: 1px solid #718096; padding: .5rem; text-align: left; vertical-align: top; }
:deep(pre) { white-space: pre-wrap; overflow-wrap: anywhere; }
@media (max-width: 1024px) { .ai-page { padding: .75rem; } :deep(table) { display: block; overflow-x: auto; } :deep(.credential-row) { display: grid; gap: .5rem; } }
</style>
