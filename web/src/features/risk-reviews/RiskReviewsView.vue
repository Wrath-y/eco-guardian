<script setup lang="ts">
import { computed, nextTick, reactive, ref, watch } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import { useProjectStore } from '@/stores/project'
import { usePolicyHistory, useReleaseHistory, useRevisionDetail, useRevisionHistory } from '@/api/versions'
import type { components } from '@/api/generated'
import { createRiskReview, RiskApiError, useRiskReview, type RiskCommand } from './api'
import RiskJobProgress from './RiskJobProgress.vue'
import RiskReviewDetail from './RiskReviewDetail.vue'

type ThresholdMode = 'inactive' | 'existing' | 'starter' | 'modified'
type RunDraft = { candidateID: string; candidateHash: string; baselineID: string; baselineHash: string }
const project = useProjectStore()
const route = useRoute()
const projectID = computed(() => project.current?.id ?? '')
const revisions = useRevisionHistory(() => projectID.value)
const releases = useReleaseHistory(() => projectID.value)
const policies = usePolicyHistory(() => projectID.value)
const candidateID = ref('')
const candidate = useRevisionDetail(() => projectID.value, () => candidateID.value)
const baselineKind = ref<'BASELINE' | 'NO_BASELINE'>('BASELINE')
const baselineReleaseID = ref('')
const baselineRelease = computed(() => releases.data.value?.items.find(item => item.id === baselineReleaseID.value))
const baselineRevisionID = computed(() => baselineKind.value === 'BASELINE' ? baselineRelease.value?.revision_id ?? '' : '')
const baselineRevision = useRevisionDetail(() => projectID.value, () => baselineRevisionID.value)
const policyID = ref('')
const policy = computed(() => policies.data.value?.items.find(item => item.id === policyID.value))
const requirements = computed(() => (policy.value?.scenes ?? []).flatMap(scene => scene.metrics.map(metric => ({ key: `${scene.id}:${scene.scene_version || 'v1'}:${metric.id}:v1`, scene_id: scene.id, scene_version: scene.scene_version || 'v1', metric_id: metric.id, metric_version: 'v1', role: scene.required && metric.required ? 'required' as const : 'optional' as const }))))
const modifiedStarterSupported = computed(() => requirements.value.every(requirement => requirement.metric_id === 'metric-resource' && requirement.metric_version === 'v1'))
const runs = reactive<Record<string, RunDraft>>({})
const thresholdMode = ref<ThresholdMode>('inactive')
const thresholdConfirmed = ref(false)
const existingThreshold = reactive({ id: '', version: '', hash: '' })
const modified = reactive({ source: 'modified-starter', warning: '0.1', block: '0.25', ruleID: 'risk-structure', ruleVersion: 'v1', ruleHash: '' })
const submitting = ref(false)
const error = ref('')
const errorSummary = ref<HTMLElement>()
const jobID = ref('')
const initialReportID = typeof route.query.report === 'string' ? route.query.report : ''
const reportID = ref(initialReportID)
const reportInput = ref(initialReportID)
const review = useRiskReview(() => projectID.value, () => reportID.value)
const recentReports = ref<string[]>([])
const hashPattern = /^[a-f0-9]{64}$/
const idPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/

const ready = computed(() => {
  if (!candidate.data.value || !policy.value || thresholdMode.value === 'inactive') return false
  if (baselineKind.value === 'BASELINE' && (!baselineRelease.value || !baselineRevision.data.value)) return false
  if ((thresholdMode.value === 'starter' || thresholdMode.value === 'modified') && !thresholdConfirmed.value) return false
  if (thresholdMode.value === 'existing' && (!existingThreshold.id.trim() || !existingThreshold.version.trim() || !hashPattern.test(existingThreshold.hash))) return false
  if (thresholdMode.value === 'modified' && !modifiedStarterSupported.value) return false
  if (thresholdMode.value === 'modified' && (!hashPattern.test(modified.ruleHash) || !decimal(modified.warning) || !decimal(modified.block))) return false
  return requirements.value.every(requirement => {
    const run = runs[requirement.key]
    return run && idPattern.test(run.candidateID) && hashPattern.test(run.candidateHash) && (baselineKind.value === 'NO_BASELINE' || (idPattern.test(run.baselineID) && hashPattern.test(run.baselineHash)))
  })
})
function decimal(value: string) { return /^-?(0|[1-9][0-9]*)(\.[0-9]+)?$/.test(value) }
function runFor(key: string) { return runs[key] ?? (runs[key] = { candidateID: '', candidateHash: '', baselineID: '', baselineHash: '' }) }
function attemptKey() { return `risk-attempt:${projectID.value}:${candidateID.value}:${policyID.value}:${baselineKind.value}:${baselineReleaseID.value}` }
function idempotencyKey() { const key = attemptKey(); const existing = sessionStorage.getItem(key); if (existing) return existing; const value = crypto.randomUUID(); sessionStorage.setItem(key, value); return value }
function uniqueRuns(side: 'candidate' | 'baseline') {
  const seen = new Set<string>(); const result: components['schemas']['RiskSimulationRef'][] = []
  for (const requirement of requirements.value) { const run = runFor(requirement.key); const id = side === 'candidate' ? run.candidateID : run.baselineID; const hash = side === 'candidate' ? run.candidateHash : run.baselineHash; if (!seen.has(id)) { seen.add(id); result.push({ run_id: id, result_hash: hash }) } }
  return result
}
function threshold(): components['schemas']['RiskThresholdSelection'] {
  if (thresholdMode.value === 'existing') return { type: 'EXISTING', threshold: { id: existingThreshold.id.trim(), version: existingThreshold.version.trim(), hash: existingThreshold.hash } }
  if (thresholdMode.value === 'starter') return { type: 'STARTER', confirmed: true }
  return { type: 'MODIFIED_STARTER', confirmed: true, body: { schema_version: 'v1', source: modified.source.trim(), assumptions: ['metric-resource@v1 target_range [0.8, 1.2] inclusive'], entries: requirements.value.map(requirement => ({ scene_id: requirement.scene_id, scene_version: requirement.scene_version, metric_id: requirement.metric_id, metric_version: requirement.metric_version, balance_group: null, unit: 'ratio', direction: 'target_range', relative_warning: modified.warning, relative_block: modified.block, absolute_warning: null, absolute_block: null })), structural_rule_versions: [{ id: modified.ruleID.trim(), version: modified.ruleVersion.trim(), hash: modified.ruleHash }] } }
}
function command(): RiskCommand {
  const candidateValue = candidate.data.value!; const policyValue = policy.value!
  const baseline: components['schemas']['RiskBaseline'] = baselineKind.value === 'NO_BASELINE' ? { type: 'NO_BASELINE' } : { type: 'BASELINE', release_id: baselineRelease.value!.id, revision: { revision_id: baselineRevision.data.value!.id, config_hash: baselineRevision.data.value!.config_hash, version_manifest_hash: baselineRevision.data.value!.metadata.version_manifest.hash } }
  return { command: 'evaluate', candidate: { revision_id: candidateValue.id, config_hash: candidateValue.config_hash, version_manifest_hash: candidateValue.metadata.version_manifest.hash }, baseline, policy: { id: policyValue.id, version: String(policyValue.display_version), hash: policyValue.policy_hash }, policy_requirements: requirements.value.map(({ key: _key, ...requirement }) => requirement), threshold: threshold(), simulation_runs: [...uniqueRuns('candidate'), ...(baselineKind.value === 'BASELINE' ? uniqueRuns('baseline') : [])] }
}
async function submit() {
  if (!ready.value) { error.value = thresholdMode.value === 'inactive' ? 'threshold 尚未配置；不能把项目标记为安全。请明确启用 starter、修改 starter 或选择已启用版本。' : thresholdMode.value === 'modified' && !modifiedStarterSupported.value ? '当前 policy 含有浏览器没有权威 unit/direction 元数据的 Metric；请选择服务端 starter 或已有 enabled threshold，不能由 UI 猜测。' : '请完成精确 candidate/baseline、policy、run/result hash 与 threshold identity。'; return }
  submitting.value = true; error.value = ''
  try { const accepted = await createRiskReview(command(), idempotencyKey()); jobID.value = accepted.job.id; sessionStorage.setItem(`risk-active-job:${projectID.value}`, accepted.job.id) } catch (cause) { error.value = cause instanceof RiskApiError && cause.code ? `${cause.message}（${cause.code}${cause.retryable ? '，可重试' : ''}）` : cause instanceof Error ? cause.message : '无法创建风险复核' } finally { submitting.value = false }
}
async function createDecision(items: { item_id: string; item_hash: string }[], reason: string) {
  if (!review.data.value) return
  error.value = ''
  const value: RiskCommand = { command: 'record_numeric_decision', source_report_id: review.data.value.id, source_calculation_hash: review.data.value.calculation_hash, eligible_items: items, reason }
  try { const accepted = await createRiskReview(value, crypto.randomUUID()); jobID.value = accepted.job.id } catch (cause) { error.value = cause instanceof Error ? cause.message : '无法创建 immutable decision report' }
}
function reviewReady(id: string) { reportID.value = id; reportInput.value = id; sessionStorage.removeItem(`risk-active-job:${projectID.value}`); const values = [id, ...recentReports.value.filter(value => value !== id)].slice(0, 10); recentReports.value = values; sessionStorage.setItem(`risk-recent:${projectID.value}`, JSON.stringify(values)) }
function openReport() { if (reportInput.value.trim()) reportID.value = reportInput.value.trim() }
function retry() { sessionStorage.removeItem(attemptKey()); jobID.value = '' }
watch(requirements, values => { for (const requirement of values) runFor(requirement.key) }, { immediate: true })
watch(releases.data, values => { if (!baselineReleaseID.value && values?.items.length) baselineReleaseID.value = values.items[0].id })
watch(policies.data, values => { if (!policyID.value && values?.items.length) policyID.value = values.items[0].id })
watch(projectID, value => { jobID.value = value ? sessionStorage.getItem(`risk-active-job:${value}`) ?? '' : ''; try { recentReports.value = value ? JSON.parse(sessionStorage.getItem(`risk-recent:${value}`) || '[]') as string[] : [] } catch { recentReports.value = [] } }, { immediate: true })
watch(error, value => { if (value) void nextTick(() => errorSummary.value?.focus()) })
</script>

<template>
  <section class="risk-page">
    <nav><RouterLink to="/projects">项目</RouterLink> · <RouterLink to="/versions">版本历史</RouterLink> · <RouterLink to="/simulations">模拟</RouterLink></nav>
    <h1>风险复核</h1>
    <p>服务器固定输入、执行 decimal 比较并评估 Gate；此页面只呈现生成 API 资源，不在 Pinia 或浏览器重算风险事实。</p>
    <form class="risk-grid" aria-label="创建风险复核" @submit.prevent="submit">
      <fieldset class="risk-card"><legend>Candidate、baseline 与 policy</legend>
        <label>候选 revision <select v-model="candidateID" required><option value="" disabled>请选择</option><option v-for="item in revisions.data.value?.items ?? []" :key="item.id" :value="item.id">revision {{ item.display_revision }} · {{ item.id }}</option></select></label>
        <p v-if="candidate.isPending.value" role="status">正在核对候选 identity…</p><p v-else-if="candidate.data.value">config {{ candidate.data.value.config_hash }} · manifest {{ candidate.data.value.metadata.version_manifest.hash }}</p>
        <label><input v-model="baselineKind" type="radio" value="BASELINE"> 当前正式 release</label><label><input v-model="baselineKind" type="radio" value="NO_BASELINE"> 明确 NO_BASELINE（首次发布）</label>
        <label v-if="baselineKind === 'BASELINE'">当前 release <select v-model="baselineReleaseID"><option value="" disabled>请选择</option><option v-for="item in releases.data.value?.items ?? []" :key="item.id" :value="item.id">{{ item.id }} · revision {{ item.revision_id }}</option></select></label>
        <p v-else>NO_BASELINE 不等于“无变化”或 PASS；报告完成后发布仍需明确建立基线。</p>
        <label>Release policy <select v-model="policyID" required><option value="" disabled>请选择</option><option v-for="item in policies.data.value?.items ?? []" :key="item.id" :value="item.id">policy {{ item.display_version }} · {{ item.id }}</option></select></label>
      </fieldset>

      <fieldset class="risk-card"><legend>Threshold 状态与选择</legend>
        <p><strong>{{ thresholdMode === 'inactive' ? '未配置（starter 仍 inactive）' : thresholdMode === 'existing' ? '选择已有 enabled version' : thresholdMode === 'starter' ? '明确启用 starter' : '创建修改版 starter' }}</strong></p>
        <p>starter 边界：relative warning 10%、BLOCK 25%；metric-resource@v1 为 ratio，目标区间 [0.8, 1.2] inclusive。未明确启用时绝不显示安全。</p>
        <label><input v-model="thresholdMode" type="radio" value="inactive"> 保持未配置</label><label><input v-model="thresholdMode" type="radio" value="existing"> 已有 enabled version</label><label><input v-model="thresholdMode" type="radio" value="starter"> 启用 starter</label><label><input v-model="thresholdMode" type="radio" value="modified"> 修改 starter</label>
        <template v-if="thresholdMode === 'existing'"><label>Threshold ID <input v-model="existingThreshold.id"></label><label>Version <input v-model="existingThreshold.version"></label><label>Body hash <input v-model="existingThreshold.hash" minlength="64" maxlength="64"></label></template>
        <template v-if="thresholdMode === 'starter' || thresholdMode === 'modified'"><label><input v-model="thresholdConfirmed" type="checkbox"> 我明确确认创建并启用此 threshold version</label></template>
        <template v-if="thresholdMode === 'modified'"><p v-if="!modifiedStarterSupported" role="alert">当前 policy 包含缺少浏览器权威 unit/direction 元数据的 Metric；修改版已禁用，请使用服务端 starter 或已有 enabled threshold。</p><label>来源 <input v-model="modified.source"></label><label>Relative WARNING <input v-model="modified.warning" inputmode="decimal"></label><label>Relative BLOCK <input v-model="modified.block" inputmode="decimal"></label><label>结构规则 ID <input v-model="modified.ruleID"></label><label>结构规则 version <input v-model="modified.ruleVersion"></label><label>结构规则 hash <input v-model="modified.ruleHash" minlength="64" maxlength="64"></label></template>
      </fieldset>

      <fieldset class="risk-card risk-wide"><legend>Policy scene / Metric 与精确 simulation result</legend>
        <p v-if="!requirements.length" role="status">当前 policy 没有可呈现的 requirement。</p>
        <section v-for="requirement in requirements" :key="requirement.key" class="run-row"><h2>{{ requirement.scene_id }}@{{ requirement.scene_version }} / {{ requirement.metric_id }}@{{ requirement.metric_version }} · {{ requirement.role }}</h2><label>Candidate run ID <input v-model="runFor(requirement.key).candidateID"></label><label>Candidate result hash <input v-model="runFor(requirement.key).candidateHash" maxlength="64"></label><template v-if="baselineKind === 'BASELINE'"><label>Baseline run ID <input v-model="runFor(requirement.key).baselineID"></label><label>Baseline result hash <input v-model="runFor(requirement.key).baselineHash" maxlength="64"></label></template></section>
        <p>提交前会保留输入并拒绝缺失、格式错误或当前 baseline/candidate 尚未加载的选择。</p>
      </fieldset>
      <button class="risk-wide" type="submit" :disabled="submitting || !ready">{{ submitting ? '正在接收并固定输入…' : '创建风险复核 Job' }}</button>
    </form>
    <p v-if="error" ref="errorSummary" role="alert" tabindex="-1">{{ error }}</p>
    <RiskJobProgress v-if="jobID && projectID" :project-i-d="projectID" :job-i-d="jobID" @review-ready="reviewReady" @retry="retry" />

    <section class="risk-card" aria-labelledby="history-heading"><h2 id="history-heading">不可变报告与最近历史</h2><form @submit.prevent="openReport"><label>Report ID <input v-model="reportInput"></label><button type="submit">读取报告</button></form><ul v-if="recentReports.length"><li v-for="id in recentReports" :key="id"><button type="button" @click="reportInput = id; reportID = id">打开 {{ id }}</button></li></ul><p v-else role="status">暂无本地最近访问链接；报告事实仍只从服务器读取。</p></section>
    <p v-if="review.isPending.value" role="status">正在加载 immutable report 与当前 freshness/Gate projection…</p>
    <p v-else-if="review.isError.value" role="alert">{{ review.error.value?.message }}</p>
    <RiskReviewDetail v-else-if="review.data.value" :review="review.data.value" @decision="createDecision" />
    <small v-if="projectID">项目 {{ projectID }}</small>
  </section>
</template>

<style scoped>
.risk-page { max-width: 1440px; margin: 0 auto; padding: 1.25rem; color: #172033; }
.risk-grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 1rem; }
.risk-card { border: 1px solid #8a94a6; border-radius: .5rem; padding: 1rem; margin: 1rem 0; background: #fff; }
.risk-wide { grid-column: 1 / -1; }
label { display: block; margin: .65rem 0; }
input:not([type="radio"]):not([type="checkbox"]), select, textarea { width: min(100%, 48rem); padding: .45rem; border: 1px solid #596579; border-radius: .25rem; }
button { margin: .25rem .5rem .25rem 0; padding: .5rem .85rem; color: #fff; background: #0756a3; border: 1px solid #064782; border-radius: .25rem; }
button:disabled { color: #4f5663; background: #d9dde4; border-color: #a3a9b4; }
.run-row { border-top: 1px solid #c5cad3; padding-top: .5rem; }
.run-row h2 { font-size: 1rem; }
:deep(.risk-table-wrap) { overflow-x: auto; }
:deep(table) { border-collapse: collapse; min-width: 1100px; }
:deep(th), :deep(td) { padding: .5rem; border: 1px solid #697489; vertical-align: top; text-align: left; }
:deep(code) { overflow-wrap: anywhere; }
@media (max-width: 1024px) { .risk-page { padding: .75rem; } .risk-grid { grid-template-columns: 1fr; } .risk-wide { grid-column: 1; } }
</style>
