<script setup lang="ts">
import { computed, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { useProjectStore } from '@/stores/project'
import { useReleaseHistory, useRevisionHistory } from '@/api/versions'
import { SimulationApiError, createSimulationJob, useSimulationRun } from '@/api/simulation'
import type { components } from '@/api/generated'

type SourceKind = 'revision' | 'release'
type MetricID = 'metric-dps' | 'metric-healing' | 'metric-survivability' | 'metric-resource' | 'metric-control'

const scenes = [
  { id: 'single-target-30s', label: '30 秒单目标', targets: 1, duration: '30 秒', defaultSeed: 11, actions: ['opening-strike'] },
  { id: 'single-target-180s', label: '180 秒单目标', targets: 1, duration: '180 秒', defaultSeed: 12, actions: ['opening-strike'] },
  { id: 'three-target-60s', label: '60 秒三目标', targets: 3, duration: '60 秒', defaultSeed: 13, actions: ['opening-strike', 'second-strike', 'third-strike'] },
  { id: 'extreme-stacking-60s', label: '60 秒极限叠层', targets: 1, duration: '60 秒', defaultSeed: 14, actions: ['opening-strike', 'stacked-strike'] },
] as const
const metrics: { id: MetricID; label: string }[] = [
  { id: 'metric-dps', label: 'DPS' }, { id: 'metric-healing', label: '治疗' }, { id: 'metric-survivability', label: '生存' }, { id: 'metric-resource', label: '资源' }, { id: 'metric-control', label: '控制' },
]

const project = useProjectStore()
const projectID = computed(() => project.current?.id ?? '')
const revisions = useRevisionHistory(() => projectID.value)
const releases = useReleaseHistory(() => projectID.value)
const sourceKind = ref<SourceKind>('revision')
const sourceID = ref('')
const sceneID = ref<(typeof scenes)[number]['id']>('single-target-30s')
const amount = ref('10')
const selectedMetrics = ref<MetricID[]>(['metric-dps'])
const sampleCount = ref(1000)
const seed = ref('')
const maxEvents = ref<number | undefined>()
const maxSteps = ref<number | undefined>()
const maxRuntimeMS = ref<number | undefined>()
const submitting = ref(false)
const error = ref('')
const jobID = ref('')
const runID = ref('')
const run = useSimulationRun(() => projectID.value, () => runID.value)
const scene = computed(() => scenes.find(value => value.id === sceneID.value) ?? scenes[0])
const sourceOptions = computed(() => sourceKind.value === 'revision'
  ? (revisions.data.value?.items ?? []).map(value => ({ id: value.id, label: value.id }))
  : (releases.data.value?.items ?? []).map(value => ({ id: value.id, label: `${value.id} · revision ${value.revision_id}` })))
const sourceLabel = computed(() => sourceKind.value === 'revision' ? '不可变 revision' : '已发布 release')
const fingerprintSummary = computed(() => `场景 ${scene.value.id}@v1 · Metric ${selectedMetrics.value.map(value => `${value}@v1`).join('、') || '未选择'} · 默认 seed ${scene.value.defaultSeed} · 采样 ${sampleCount.value || 1000}；input 与实现指纹由服务端在接收时固定。`)

function key() { return crypto.randomUUID() }
function selectSourceKind(value: SourceKind) { sourceKind.value = value; sourceID.value = '' }
function budget(): components['schemas']['SimulationBudget'] | undefined {
  const value = { max_events: maxEvents.value, max_steps: maxSteps.value, max_runtime_ms: maxRuntimeMS.value }
  return Object.values(value).some(item => item !== undefined) ? value : undefined
}
async function submit() {
  if (!sourceID.value) { error.value = `请选择${sourceLabel.value}。`; return }
  if (!selectedMetrics.value.length) { error.value = '请至少选择一个 Metric。'; return }
  submitting.value = true; error.value = ''
  const request: components['schemas']['CreateSimulationJobRequest'] = {
    source: sourceKind.value === 'revision' ? { revision_id: sourceID.value } : { release_id: sourceID.value },
    scene_id: scene.value.id,
    scene_version: 'v1',
    parameters: { '/actions/opening-strike/inputs/amount': amount.value },
    metrics: selectedMetrics.value.map(id => ({ id, version: 'v1' })),
    sample_count: sampleCount.value || 1000,
  }
  if (seed.value.trim()) request.seed = Number(seed.value)
  const limits = budget()
  if (limits) request.budget = limits
  try {
    const accepted = await createSimulationJob(request, key())
    jobID.value = accepted.job.id
  } catch (cause) {
    error.value = cause instanceof SimulationApiError && cause.code ? `${cause.message}（${cause.code}）` : cause instanceof Error ? cause.message : '无法创建模拟任务'
  } finally { submitting.value = false }
}
</script>

<template>
  <section>
    <RouterLink to="/projects">项目</RouterLink> · <RouterLink to="/versions">版本历史</RouterLink>
    <h1>模拟</h1>
    <p>输入仅描述固定场景允许的字段；revision、场景、实现版本和最终指纹均由服务端权威校验与捕获。</p>
    <form aria-label="创建模拟任务" @submit.prevent="submit">
      <fieldset><legend>来源</legend>
        <label><input type="radio" :checked="sourceKind === 'revision'" @change="selectSourceKind('revision')"> 不可变 revision</label>
        <label><input type="radio" :checked="sourceKind === 'release'" @change="selectSourceKind('release')"> 已发布 release</label>
        <p v-if="(sourceKind === 'revision' ? revisions : releases).isPending.value" role="status">正在加载{{ sourceLabel }}…</p>
        <label v-else>{{ sourceLabel }}
          <select v-model="sourceID" :aria-label="sourceLabel" required><option disabled value="">请选择</option><option v-for="source in sourceOptions" :key="source.id" :value="source.id">{{ source.label }}</option></select>
        </label>
        <p v-if="!(sourceKind === 'revision' ? revisions : releases).isPending.value && !sourceOptions.length" role="status">没有可选择的{{ sourceLabel }}。</p>
      </fieldset>
      <fieldset><legend>固定场景</legend>
        <label>场景 <select v-model="sceneID" aria-label="场景"><option v-for="item in scenes" :key="item.id" :value="item.id">{{ item.label }}</option></select></label>
        <p>参与者：1 个来源、{{ scene.targets }} 个目标；初始状态：来源 power 100 points、每个目标 health 1000 points；持续时间：{{ scene.duration }}；动作：{{ scene.actions.join('、') }}。</p>
        <label>opening-strike 伤害（points）<input v-model="amount" inputmode="decimal" aria-describedby="amount-help"></label>
        <small id="amount-help">允许范围 0–1000；这是该模板声明的唯一可调参数。</small>
      </fieldset>
      <fieldset><legend>Metric 与运行边界</legend>
        <label v-for="metric in metrics" :key="metric.id"><input v-model="selectedMetrics" type="checkbox" :value="metric.id"> {{ metric.label }}</label>
        <label>样本数 <input v-model.number="sampleCount" type="number" min="1" max="1000" required></label>
        <label>seed（可选）<input v-model="seed" inputmode="numeric" placeholder="使用场景默认值"></label>
        <label>最大事件数（可选）<input v-model.number="maxEvents" type="number" min="1" max="10000"></label>
        <label>最大步骤数（可选）<input v-model.number="maxSteps" type="number" min="1" max="10000"></label>
        <label>最大运行时间（毫秒，可选）<input v-model.number="maxRuntimeMS" type="number" min="1" max="30000"></label>
        <p role="status">{{ fingerprintSummary }}</p>
      </fieldset>
      <button type="submit" :disabled="submitting">{{ submitting ? '正在提交…' : '运行模拟' }}</button>
    </form>
    <p v-if="error" role="alert">{{ error }}</p>
    <p v-if="jobID" role="status">已创建 Job：{{ jobID }}；可通过统一 Job 状态跟踪进度。</p>
    <section><h2>历史 Run</h2><label>Run ID <input v-model="runID"></label><p v-if="run.isPending.value" role="status">正在读取…</p><p v-else-if="run.isError.value" role="alert">{{ run.error.value?.message }}</p><pre v-else-if="run.data.value">{{ run.data.value.canonical_result }}</pre></section>
    <small v-if="projectID">项目 {{ projectID }}</small>
  </section>
</template>
