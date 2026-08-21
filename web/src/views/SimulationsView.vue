<script setup lang="ts">
import { computed, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { useProjectStore } from '@/stores/project'
import { createSimulationJob, useSimulationRun } from '@/api/simulation'

const project = useProjectStore()
const revisionID = ref(''); const sceneID = ref('single-target-30s'); const sceneVersion = ref('v1'); const runID = ref('')
const submitting = ref(false); const error = ref(''); const jobID = ref('')
const run = useSimulationRun(() => project.current?.id ?? '', () => runID.value)
function key() { return crypto.randomUUID() }
async function submit() {
  if (!revisionID.value) { error.value = '请选择不可变 revision。'; return }
  submitting.value = true; error.value = ''
  try {
    const accepted = await createSimulationJob({ source: { revision_id: revisionID.value }, scene_id: sceneID.value, scene_version: sceneVersion.value, metrics: [{ id: 'metric-dps', version: 'v1' }], sample_count: 1000 }, key())
    jobID.value = accepted.job.id
  } catch (cause) { error.value = cause instanceof Error ? cause.message : '无法创建模拟任务' } finally { submitting.value = false }
}
const projectID = computed(() => project.current?.id ?? '')
</script>
<template><section><RouterLink to="/projects">项目</RouterLink> · <RouterLink to="/versions">版本历史</RouterLink><h1>模拟</h1><form @submit.prevent="submit"><label>Revision <input v-model="revisionID" required></label><label>场景 <input v-model="sceneID"></label><label>场景版本 <input v-model="sceneVersion"></label><button :disabled="submitting">{{ submitting ? '正在提交…' : '运行默认 1000 样本' }}</button></form><p v-if="error" role="alert">{{ error }}</p><p v-if="jobID" role="status">已创建 Job：{{ jobID }}；可通过统一 Job 状态跟踪进度。</p><section><h2>历史 Run</h2><label>Run ID <input v-model="runID"></label><p v-if="run.isPending.value" role="status">正在读取…</p><p v-else-if="run.isError.value" role="alert">{{ run.error.value?.message }}</p><pre v-else-if="run.data.value">{{ run.data.value.canonical_result }}</pre></section><small v-if="projectID">项目 {{ projectID }}</small></section></template>
