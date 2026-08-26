<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { useRevisionHistory } from '@/api/versions'
import { useProjectStore } from '@/stores/project'
import { createImpact, expandImpact, explainImpact, ImpactApiError, type ImpactCommand, type ImpactExpansion, type ImpactExplanation, type ImpactPath, useImpactReport } from './api'
import ImpactEvidencePath from './ImpactEvidencePath.vue'
import ImpactJobProgress from './ImpactJobProgress.vue'

const project = useProjectStore()
const route = useRoute()
const router = useRouter()
const projectID = computed(() => project.current?.id ?? '')
const revisions = useRevisionHistory(() => projectID.value)
const historical = ref(false)
const baseID = ref('')
const targetID = ref('')
const direction = ref<'incoming' | 'outgoing' | 'both'>('incoming')
const maxDepth = ref(3)
const maxNodes = ref(500)
const suspected = ref(false)
const submitting = ref(false)
const error = ref('')
const errorBox = ref<HTMLElement>()
const jobID = ref('')
const reportID = ref(typeof route.query.report === 'string' ? route.query.report : '')
const reportInput = ref(reportID.value)
const report = useImpactReport(() => projectID.value, () => reportID.value)
const selectedTarget = ref('')
const selectedPath = ref<ImpactPath>()
const expansion = ref<ImpactExpansion>()
const expansionLoading = ref(false)
const explanation = ref<ImpactExplanation>()
const explanationLoading = ref(false)
const expandedPathIndex = ref(0)
const browseType = ref('')
const browseDepth = ref(6)

const activeBaseline = computed(() => revisions.data.value?.items.find(item => item.status.includes('active_release'))?.id ?? '')
const selectedAffected = computed(() => report.data.value?.deterministic_affected.find(item => item.node.id === selectedTarget.value))
const selectedEvidenceRef = computed(() => selectedAffected.value?.evidence_ref ?? '')
const visibleAffected = computed(() => (report.data.value?.deterministic_affected ?? []).filter(item => (!browseType.value || item.node.type === browseType.value) && item.minimum_depth <= browseDepth.value))
const affectedTypes = computed(() => [...new Set((report.data.value?.deterministic_affected ?? []).map(item => item.node.type))].sort())
const canSubmit = computed(() => Boolean(projectID.value && baseID.value && targetID.value && baseID.value !== targetID.value && maxDepth.value >= 1 && maxDepth.value <= 6 && maxNodes.value >= 1 && maxNodes.value <= 500))
const modeLabel = computed(() => report.data.value?.mode === 'reverse_dependency_impact' ? '反向依赖影响' : '关系探索（非默认反向影响）')
const stateLabel: Record<string, string> = { disabled: '未启用', ready: '可用', degraded: '降级但确定性结果可用', rebuild_required: '索引已驱逐，需要显式重建', unavailable: '检索不可用', canceled: '检索已取消' }

function attemptKey() { return `impact-attempt:${projectID.value}:${baseID.value}:${targetID.value}:${direction.value}:${maxDepth.value}:${maxNodes.value}:${suspected.value}` }
function idempotencyKey() { const storageKey = attemptKey(); const existing = sessionStorage.getItem(storageKey); if (existing) return existing; const value = crypto.randomUUID(); sessionStorage.setItem(storageKey, value); return value }
function command(): ImpactCommand { return { project_uuid: projectID.value, base_revision_id: baseID.value, target_revision_id: targetID.value, filters: { relationship_kinds: ['explicit'], direction: direction.value, node_types: [], edge_types: [] }, limits: { max_depth: maxDepth.value, max_nodes: maxNodes.value, default_paths_per_target: 1, expanded_max_paths: 20 }, suspected: { enabled: suspected.value, max_seeds: 20, max_results: 20, graph_max_depth: 2 } } }
async function submit() {
  if (!canSubmit.value) { showError('请选择同一项目中不同的 base/target revision，并使用有界限制。'); return }
  submitting.value = true; error.value = ''
  try { const accepted = await createImpact(command(), idempotencyKey()); jobID.value = accepted.job.id; sessionStorage.setItem(`impact-active-job:${projectID.value}`, accepted.job.id); if (accepted.result_id) ready(accepted.result_id) }
  catch (cause) { showError(cause instanceof ImpactApiError ? `${cause.message}${cause.code ? `（${cause.code}${cause.retryable ? '，可重试' : ''}）` : ''}` : cause instanceof Error ? cause.message : '无法创建影响分析') }
  finally { submitting.value = false }
}
function showError(message: string) { error.value = message; void nextTick(() => errorBox.value?.focus()) }
function ready(id: string) { reportID.value = id; reportInput.value = id; jobID.value = ''; sessionStorage.removeItem(`impact-active-job:${projectID.value}`); void router.replace({ path: '/impact', query: { report: id } }) }
function retry() { sessionStorage.removeItem(attemptKey()); jobID.value = '' }
function openReport() { if (reportInput.value.trim()) ready(reportInput.value.trim()) }
function selectTarget(id: string) { selectedTarget.value = id; expansion.value = undefined; expandedPathIndex.value = 0; explanation.value = undefined; selectedPath.value = report.data.value?.deterministic_affected.find(item => item.node.id === id)?.default_path }
function moveTarget(current: string, delta: number) { const index = visibleAffected.value.findIndex(item => item.node.id === current); if (index < 0 || !visibleAffected.value.length) return; const next = visibleAffected.value[(index + delta + visibleAffected.value.length) % visibleAffected.value.length]; selectTarget(next.node.id); void nextTick(() => document.getElementById(`impact-target-${next.node.id}`)?.focus()) }
async function expand() {
  if (!reportID.value || !selectedTarget.value) return
  expansionLoading.value = true; error.value = ''
  try { expansion.value = await expandImpact(reportID.value, selectedTarget.value, 20, `impact-expand:${reportID.value}:${selectedTarget.value}:20`); expandedPathIndex.value = 0; selectedPath.value = expansion.value.paths[0] ?? selectedAffected.value?.default_path }
  catch (cause) { showError(cause instanceof Error ? cause.message : '无法扩展路径') }
  finally { expansionLoading.value = false }
}
function chooseExpandedPath(index: number) { expandedPathIndex.value = index; selectedPath.value = expansion.value?.paths[index] }
async function explain() {
  if (!reportID.value || !selectedEvidenceRef.value) return
  explanationLoading.value = true; error.value = ''
  try { explanation.value = await explainImpact(reportID.value, [selectedEvidenceRef.value], `impact-explain:${reportID.value}:${selectedEvidenceRef.value}`) }
  catch (cause) { showError(cause instanceof Error ? cause.message : 'AI 解释不可用') }
  finally { explanationLoading.value = false }
}
watch(activeBaseline, value => { if (!historical.value && value) baseID.value = value }, { immediate: true })
watch(historical, value => { if (!value) baseID.value = activeBaseline.value })
watch(revisions.data, value => { if (!targetID.value && value?.items.length) targetID.value = value.items[0].id })
watch(projectID, value => { jobID.value = value ? sessionStorage.getItem(`impact-active-job:${value}`) ?? '' : '' }, { immediate: true })
watch(() => report.data.value?.id, () => { const first = report.data.value?.deterministic_affected[0]; if (first) selectTarget(first.node.id); else { selectedTarget.value = ''; selectedPath.value = undefined } })
</script>

<template>
  <section class="impact-page">
    <nav><RouterLink to="/projects">项目</RouterLink> · <RouterLink to="/versions">版本历史</RouterLink> · <RouterLink to="/simulations">模拟</RouterLink></nav>
    <h1>依赖影响分析</h1>
    <p>确定性 changed/affected 与证据路径只来自固定 target Graph Snapshot；相似检索与 AI 解释始终分区呈现。</p>

    <form class="card options" aria-label="创建影响分析" @submit.prevent="submit">
      <h2>Revision 对与分析选项</h2>
      <p v-if="!activeBaseline" role="status"><strong>自动基线等待：</strong>当前没有 active release，服务器会保留 NO_BASELINE 状态且不会回退到 working/latest revision。</p>
      <label><input v-model="historical" type="checkbox"> 手动选择历史 base（默认使用当前 active release）</label>
      <label>Base revision <select v-model="baseID" :disabled="!historical"><option value="">{{ activeBaseline ? '请选择' : '等待 active release' }}</option><option v-for="item in revisions.data.value?.items ?? []" :key="item.id" :value="item.id">#{{ item.display_revision }} · {{ item.id }}</option></select></label>
      <label>Target revision <select v-model="targetID"><option value="">请选择</option><option v-for="item in revisions.data.value?.items ?? []" :key="item.id" :value="item.id">#{{ item.display_revision }} · {{ item.id }}</option></select></label>
      <label>方向 <select v-model="direction"><option value="incoming">incoming · 反向依赖</option><option value="outgoing">outgoing · 关系探索</option><option value="both">both · 关系探索</option></select></label>
      <label>最大深度 <input v-model.number="maxDepth" type="number" min="1" max="6"></label>
      <label>最大 Node <input v-model.number="maxNodes" type="number" min="1" max="500"></label>
      <label><input v-model="suspected" type="checkbox"> 启用可选 suspected 检索（默认折叠，不影响确定性结果）</label>
      <button type="submit" :disabled="submitting || !canSubmit">{{ submitting ? '正在固定输入…' : '创建 Impact Job' }}</button>
    </form>
    <p v-if="error" ref="errorBox" role="alert" tabindex="-1" class="error">{{ error }}</p>
    <ImpactJobProgress v-if="jobID" :project-i-d="projectID" :job-i-d="jobID" @ready="ready" @retry="retry" />

    <section class="card" aria-labelledby="impact-history-title"><h2 id="impact-history-title">不可变历史报告</h2><form @submit.prevent="openReport"><label>Report ID <input v-model="reportInput"></label><button type="submit">读取</button></form></section>
    <p v-if="reportID && report.isPending.value" role="status">正在读取报告与当前 freshness…</p>
    <p v-else-if="report.isError.value" role="alert">{{ report.error.value?.message }}</p>
    <article v-else-if="report.data.value" class="report">
      <header class="card"><h2>{{ modeLabel }}</h2><p>Report {{ report.data.value.id }} · {{ report.data.value.cache_hit ? '精确缓存命中' : '原始执行结果' }} · <strong>{{ report.data.value.freshness.fresh ? '当前上下文仍新鲜' : '历史结果已陈旧' }}</strong></p><p v-if="!report.data.value.freshness.fresh">原因：{{ report.data.value.freshness.reasons.join('、') }}</p><p>Base {{ report.data.value.base.revision_id }} → Target {{ report.data.value.target.revision_id }} · Graph {{ report.data.value.target.graph_manifest_hash }}</p><p v-if="report.data.value.truncated" role="status">确定性前缀已截断：{{ report.data.value.truncation_reasons.join('、') }}。这不表示没有更多影响。</p><ul v-if="report.data.value.warnings.length"><li v-for="warning in report.data.value.warnings" :key="warning">{{ warning }}</li></ul></header>

      <section class="columns">
        <section class="card"><h2>Changed（{{ report.data.value.changed_entities.length }}）</h2><p v-if="!report.data.value.changed_entities.length" role="status">配置内容相同，没有 changed entity；这是有效空报告。</p><ol><li v-for="item in report.data.value.changed_entities" :key="item.entity_id"><strong>{{ item.change_kind }} {{ item.kind }}</strong> · {{ item.entity_id }}<span v-if="!item.query_eligible"> · 不参与查询：{{ item.ineligibility }}</span><ul><li v-for="field in item.field_paths" :key="field">{{ field }}</li></ul></li></ol></section>
        <section class="card"><h2>确定性 affected（{{ report.data.value.deterministic_affected.length }}）</h2><p>文字图例：直接影响＝深度 1；间接影响＝深度 2+；标签规则路径另有明确文字标签。</p><label>浏览类型 <select v-model="browseType"><option value="">全部类型</option><option v-for="kind in affectedTypes" :key="kind" :value="kind">{{ kind }}</option></select></label><label>浏览最大深度 <input v-model.number="browseDepth" type="number" min="1" max="6"></label><p v-if="!report.data.value.deterministic_affected.length" role="status">没有确定性 affected Node；请结合 changed eligibility、筛选和错误状态判断。</p><p v-else-if="!visibleAffected.length" role="status">当前浏览筛选没有对象；报告原始结果未被修改。</p><ol class="affected"><li v-for="item in visibleAffected" :key="item.node.id"><button :id="`impact-target-${item.node.id}`" type="button" :aria-current="selectedTarget === item.node.id ? 'true' : undefined" @click="selectTarget(item.node.id)" @keydown.up.prevent="moveTarget(item.node.id, -1)" @keydown.down.prevent="moveTarget(item.node.id, 1)">{{ item.node.label || item.node.id }} · 深度 {{ item.minimum_depth }} · {{ item.direct ? '直接影响' : '间接影响' }}<span v-if="item.tag_rule"> · 标签规则路径</span></button></li></ol></section>
      </section>

      <section v-if="selectedAffected" class="card evidence"><h2>所选对象证据</h2><div class="path-actions"><button type="button" :disabled="expansionLoading" @click="expand">{{ expansionLoading ? '正在获取有界路径…' : '展开其他路径（最多 20）' }}</button><button type="button" :disabled="explanationLoading" @click="explain">{{ explanationLoading ? '正在请求 AI…' : '可选 AI 解释所选证据' }}</button></div><div v-if="expansion?.paths.length" role="group" aria-label="选择证据路径"><button v-for="(_path, index) in expansion.paths" :key="index" type="button" :aria-pressed="expandedPathIndex === index" @click="chooseExpandedPath(index)">路径 {{ index + 1 }}</button></div><p v-else-if="expansion">扩展成功但没有额外路径；原始默认路径仍保留。</p><ImpactEvidencePath :path="selectedPath" /><aside v-if="explanation" class="ai"><h3>AI generated 解释</h3><p>状态：{{ explanation.status }}<span v-if="explanation.provider"> · {{ explanation.provider }} / {{ explanation.model }}</span></p><p v-if="explanation.text">{{ explanation.text }}</p><p v-else role="status">{{ explanation.diagnostic || 'AI 没有返回可验证解释；原始路径仍可用。' }}</p><p>Evidence ref：{{ explanation.evidence_refs.join('、') || selectedEvidenceRef }}</p></aside></section>

      <details class="card suspected"><summary>Suspected 关联（{{ report.data.value.suspected_associations.length }}）· {{ stateLabel[report.data.value.suspected_state] }}</summary><p>此区不属于 deterministic affected，不参与 Gate 或发布阻塞。</p><ol><li v-for="item in report.data.value.suspected_associations" :key="item.evidence_ref"><strong>#{{ item.rank }} {{ item.node.label }}</strong><p>{{ item.citation_text }}</p><p>证据：{{ item.relationship_kinds.join('、') }} · algorithm {{ item.algorithm_version }} · generation {{ item.fts_generation || '无 FTS' }} / {{ item.vector_generation || '无 Vector' }} · model {{ item.model_provider || '无' }}/{{ item.model || '无' }}</p><pre>{{ JSON.stringify(item.scores, null, 2) }}</pre></li></ol></details>
    </article>
  </section>
</template>

<style scoped>
.impact-page { max-width: 1440px; margin: 0 auto; color: #172033; }
.card { border: 1px solid #7a8597; border-radius: .6rem; padding: 1rem; margin: 1rem 0; background: #fff; }
.options { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: .75rem 1.25rem; }
.options h2, .options p, .options button { grid-column: 1 / -1; }
label { display: block; }
input:not([type="checkbox"]), select { width: 100%; box-sizing: border-box; padding: .45rem; }
button { padding: .45rem .75rem; margin: .2rem .4rem .2rem 0; border: 1px solid #174d74; border-radius: .3rem; background: #075d91; color: #fff; }
button[aria-current="true"], button[aria-pressed="true"] { background: #7a3e00; text-decoration: underline; }
button:disabled { background: #d6dae0; color: #4d5562; border-color: #939aa5; }
.columns { display: grid; grid-template-columns: minmax(0, 2fr) minmax(0, 3fr); gap: 1rem; }
.affected button { width: 100%; text-align: left; }
.error { border-left: 5px solid #a01717; padding: .75rem; }
.ai { margin-top: 1rem; padding: 1rem; border: 2px dashed #6c4a7e; }
pre { white-space: pre-wrap; overflow-wrap: anywhere; }
summary { cursor: pointer; font-weight: 700; }
@media (max-width: 1024px) { .options, .columns { grid-template-columns: 1fr; } .options h2, .options p, .options button { grid-column: 1; } }
</style>
