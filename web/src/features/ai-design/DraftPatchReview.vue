<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { useQueryClient } from '@tanstack/vue-query'
import { versionKeys } from '@/api/versions'
import { AIApiError, acceptDraftPatch, discardDraftPatch, type DraftPatch, useDraftPatch } from './api'

const props = defineProps<{ projectID: string; patchID: string }>()
const client = useQueryClient()
const query = useDraftPatch(() => props.projectID, () => props.patchID)
const originals = ref<Record<string, Record<string, unknown>>>({})
const decisionBusy = ref<'accept' | 'discard' | ''>('')
const decisionError = ref('')
const decisionMessage = ref('')
const discardReason = ref('')
const conflict = ref(false)
const summary = ref<HTMLElement>()
const patch = computed(() => query.data.value)
const canDecide = computed(() => Boolean(patch.value && !patch.value.decision && patch.value.freshness.state === 'fresh'))
const canAccept = computed(() => Boolean(canDecide.value && patch.value?.preview?.acceptable))

function pointerValue(source: unknown, pointer: string): unknown {
  if (!pointer.startsWith('/')) return undefined
  return pointer.slice(1).split('/').reduce<unknown>((value, token) => {
    if (value === null || typeof value !== 'object') return undefined
    const key = token.replace(/~1/g, '/').replace(/~0/g, '~')
    return (value as Record<string, unknown>)[key]
  }, source)
}
function text(value: unknown): string { return value === undefined ? '字段不存在' : JSON.stringify(value, null, 2) }
function key(prefix: string) { return `${prefix}-${crypto.randomUUID?.() ?? Date.now()}` }
async function loadOriginals(value?: DraftPatch) {
  if (!value) return
  const loaded: Record<string, Record<string, unknown>> = {}
  await Promise.all(value.targets.map(async target => {
    const response = await fetch(`/api/v1/entities/${target.kind}/${target.entity_id}`)
    if (response.ok) loaded[target.entity_id] = await response.json() as Record<string, unknown>
  }))
  originals.value = loaded
}
async function focusSummary() { await nextTick(); summary.value?.focus() }
async function accept() {
  if (!patch.value || !canAccept.value) return
  decisionBusy.value = 'accept'; decisionError.value = ''; decisionMessage.value = ''; conflict.value = false
  try {
    const result = await acceptDraftPatch(patch.value, key('accept'))
    decisionMessage.value = `已创建 revision ${result.revision_id}；published=false，仍需单独人工发布。`
    await Promise.all([
      query.refetch(),
      client.invalidateQueries({ queryKey: versionKeys.history(props.projectID) }),
    ])
    await focusSummary()
  } catch (cause) {
    const error = cause as AIApiError
    conflict.value = error.code === 'REVISION_CONFLICT' || error.code === 'AI_PATCH_STALE'
    decisionError.value = conflict.value ? '接受冲突：base 或目标 entity_version 已变化。本页输入与候选保持不变，请比较服务器 freshness 后决定放弃或重新生成。' : error.message
    await focusSummary()
  } finally { decisionBusy.value = '' }
}
async function discard() {
  if (!patch.value || !canDecide.value) return
  decisionBusy.value = 'discard'; decisionError.value = ''; decisionMessage.value = ''
  try { await discardDraftPatch(patch.value, key('discard'), discardReason.value); decisionMessage.value = 'DraftPatch 已追加 discarded 决策；没有修改 working draft。'; await query.refetch(); await focusSummary() }
  catch (cause) { decisionError.value = cause instanceof Error ? cause.message : '无法放弃 DraftPatch'; await focusSummary() } finally { decisionBusy.value = '' }
}
watch(patch, value => { void loadOriginals(value) }, { immediate: true })
</script>

<template>
  <article class="ai-card" aria-labelledby="patch-heading">
    <h2 id="patch-heading">DraftPatch 候选审阅</h2>
    <p v-if="query.isPending.value" role="status">正在读取不可变候选…</p>
    <p v-else-if="query.isError.value" role="alert">{{ query.error.value?.message }} <button type="button" @click="query.refetch()">重试读取</button></p>
    <template v-else-if="patch">
      <div v-if="decisionError || decisionMessage" ref="summary" tabindex="-1" :role="decisionError ? 'alert' : 'status'" aria-live="assertive" class="decision-summary">
        <strong>{{ decisionError ? '决策未完成' : '决策已提交' }}</strong><p>{{ decisionError || decisionMessage }}</p>
        <p v-if="conflict">Patch {{ patch.id }} 与表单内容均未清除。</p>
      </div>
      <header>
        <p>Patch {{ patch.id }} · hash <code>{{ patch.patch_hash }}</code></p>
        <p>base revision {{ patch.base_revision_id }} · freshness：<strong>{{ patch.freshness.state }}</strong><span v-if="patch.freshness.conflicting_targets.length"> · 冲突目标 {{ patch.freshness.conflicting_targets.join('、') }}</span></p>
        <p>状态：{{ patch.decision?.kind ?? (patch.preview?.acceptable ? 'pending / acceptable' : patch.preview ? 'pending / blocked' : 'failed / no preview') }}</p>
      </header>

      <section aria-labelledby="diff-heading">
        <h3 id="diff-heading">多实体原值 / 规范 Patch 差异</h3>
        <p>“原值”来自当前只读 entity 投影；“规范值”来自服务器密封 DraftPatch。状态和操作均有文字，不依赖颜色。</p>
        <section v-for="target in patch.targets" :key="target.entity_id" class="target-diff">
          <h4>{{ target.kind }} {{ target.entity_id }} · 期望 entity_version {{ target.expected_entity_version }}</h4>
          <table>
            <caption>目标 {{ target.entity_id }} 的逐项差异</caption>
            <thead><tr><th>序号/操作</th><th>字段路径</th><th>原值</th><th>规范候选值</th><th>证据 ID</th></tr></thead>
            <tbody><tr v-for="operation in target.operations" :key="operation.ordinal"><td>{{ operation.ordinal }} · {{ operation.kind }}</td><td><code>{{ operation.path }}</code></td><td><pre>{{ text(pointerValue(originals[target.entity_id], operation.path)) }}</pre></td><td><pre>{{ text(operation.value) }}</pre></td><td><ul><li v-for="evidence in operation.evidence" :key="evidence"><a :href="`#evidence-${evidence}`">{{ evidence }}</a></li></ul></td></tr></tbody>
          </table>
        </section>
      </section>

      <section aria-labelledby="evidence-heading">
        <h3 id="evidence-heading">事实证据与 advisory 反馈</h3>
        <p v-if="!patch.preview" role="alert">没有完整 preview；此候选不可接受，也不会把缺失值显示为 0。</p>
        <template v-else>
          <p>advisory=true · acceptable=<strong>{{ patch.preview.acceptable }}</strong> · result {{ patch.preview.result_hash }}</p>
          <ul v-if="patch.preview.issues.length"><li v-for="issue in patch.preview.issues" :key="issue">{{ issue }}</li></ul>
          <p v-if="!patch.preview.evidence.length" role="alert">没有证据；候选不可确认。</p>
          <article v-for="evidence in patch.preview.evidence" :id="`evidence-${evidence.id}`" :key="evidence.id" class="evidence-card">
            <h4>{{ evidence.kind }} evidence {{ evidence.id }}</h4>
            <p>citation：{{ evidence.citation ?? '无 citation' }}</p><p>检索模式：{{ evidence.mode ?? '不适用' }}；degraded={{ evidence.degraded }}</p>
            <p>scores：{{ Object.entries(evidence.scores).map(([name, value]) => `${name}=${value}`).join('；') || '无分数' }}</p>
            <p>generations：{{ evidence.generations.map(value => `${value.id}@${value.version}#${value.hash.slice(0, 12)}`).join('；') || '无 generation' }}</p>
            <ul v-if="evidence.warnings.length"><li v-for="warning in evidence.warnings" :key="warning">WARNING：{{ warning }}</li></ul>
          </article>
          <p>Evaluator identities：{{ patch.preview.evaluators.map(value => `${value.id}@${value.version}#${value.hash.slice(0, 12)}`).join('；') }}</p>
        </template>
      </section>

      <section aria-labelledby="ai-explanation-heading">
        <h3 id="ai-explanation-heading">AI 生成说明（不是事实证据）</h3>
        <p><strong>Rationale：</strong>{{ patch.rationale }}</p>
        <ul><li v-for="assumption in patch.assumptions" :key="assumption">Assumption：{{ assumption }}</li></ul>
      </section>

      <section aria-labelledby="attempt-heading"><h3 id="attempt-heading">Attempt、repair 与版本身份</h3><ol><li v-for="attempt in patch.attempts" :key="attempt.id">#{{ attempt.ordinal }} {{ attempt.stage }} · {{ attempt.outcome }} · repair {{ attempt.repair_round }}/3 · manifest {{ attempt.manifest.id }}@{{ attempt.manifest.version }}#{{ attempt.manifest.hash }}</li></ol><p>Patch Schema {{ patch.schema.id }}@{{ patch.schema.version }}#{{ patch.schema.hash }}；Evidence manifest {{ patch.evidence_manifest.id }}@{{ patch.evidence_manifest.version }}#{{ patch.evidence_manifest.hash }}</p></section>

      <section aria-labelledby="formal-heading"><h3 id="formal-heading">接受后的正式结果链接</h3><ul><li>FULL validation：<a v-if="patch.links.formal_validation" :href="patch.links.formal_validation">打开</a><span v-else>尚未运行</span></li><li>Graph：<a v-if="patch.links.formal_graph" :href="patch.links.formal_graph">打开</a><span v-else>尚未运行</span></li><li>Simulation：<a v-if="patch.links.formal_simulation" :href="patch.links.formal_simulation">打开</a><span v-else>尚未运行</span></li><li>Risk：<a v-if="patch.links.formal_risk" :href="patch.links.formal_risk">打开</a><span v-else>尚未运行</span></li></ul></section>

      <section aria-labelledby="decision-heading"><h3 id="decision-heading">服务器权威决策</h3><p>接受会原子创建一个新 revision，但<strong>不会发布</strong>；发布仍需在版本页单独人工确认。</p><p v-if="patch.freshness.state === 'stale'" role="alert">候选已 stale，接受被禁用；可放弃后基于新 revision 重新生成。</p><p v-if="patch.decision">已由 {{ patch.decision.actor }} 于 {{ patch.decision.decided_at }} 决定：{{ patch.decision.kind }}<span v-if="patch.decision.accepted_revision_id">，revision {{ patch.decision.accepted_revision_id }}</span>。</p><template v-else><button type="button" :disabled="!canAccept || Boolean(decisionBusy)" @click="accept">{{ decisionBusy === 'accept' ? '正在接受…' : '接受并创建 revision（不发布）' }}</button><label>放弃原因 <input v-model="discardReason" maxlength="1000"></label><button type="button" :disabled="!canDecide || Boolean(decisionBusy)" @click="discard">{{ decisionBusy === 'discard' ? '正在放弃…' : '放弃候选' }}</button></template></section>
    </template>
  </article>
</template>
