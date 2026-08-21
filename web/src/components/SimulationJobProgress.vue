<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { useQueryClient } from '@tanstack/vue-query'
import { cancelSimulationJob, getSimulationJob, simulationKeys, type SimulationJob, type SimulationJobEvent, useSimulationJob } from '@/api/simulation'

const props = defineProps<{ projectID: string; jobID: string }>()
const emit = defineEmits<{ 'run-ready': [runID: string] }>()
const client = useQueryClient()
const query = useSimulationJob(() => props.projectID, () => props.jobID)
const events = ref<SimulationJobEvent[]>([])
const streamState = ref<'connecting' | 'connected' | 'polling'>('connecting')
const canceling = ref(false)
const cancelError = ref('')
const progressHeading = ref<HTMLElement>()
let stream: EventSource | undefined
let pollTimer: number | undefined

const job = computed(() => query.data.value)
const terminal = (status?: SimulationJob['status']) => status === 'succeeded' || status === 'failed' || status === 'canceled' || status === 'interrupted'
const canCancel = computed(() => job.value?.status === 'queued' || job.value?.status === 'running')
const latestEvent = computed(() => events.value.at(-1))
const currentPhase = computed(() => latestEvent.value?.phase ?? (job.value?.status === 'running' ? 'RUNNING' : job.value?.status?.toUpperCase()))
const failureMessage = computed(() => {
  const error = latestEvent.value?.error ?? ''
  if (error.startsWith('BUDGET_EXCEEDED:')) return '已超过固定预算；没有生成部分成功结果。调整受限预算或场景后，使用新的尝试重新运行。'
  if (error.startsWith('TIMEOUT:')) return '本地执行已超时；没有生成部分成功结果。可在确认预算后使用新的尝试重新运行。'
  if (error.startsWith('RECOVERY_MISMATCH:') || error.startsWith('RECOVERY_UNAVAILABLE:')) return '恢复所需的历史输入或实现不可用；此 Job 不能安全重试为成功。'
  return '任务失败；请查看持久化的安全错误码后，使用新的尝试重新运行。'
})
const eventStorageKey = () => `simulation-job-ordinal:${props.projectID}:${props.jobID}`
function closeStream() { stream?.close(); stream = undefined }
function clearPolling() { if (pollTimer !== undefined) { window.clearTimeout(pollTimer); pollTimer = undefined } }
function remember(event: SimulationJobEvent) {
  if (!events.value.some(value => value.ordinal === event.ordinal)) events.value = [...events.value, event].sort((a, b) => a.ordinal - b.ordinal)
  sessionStorage.setItem(eventStorageKey(), String(event.ordinal))
}
function storeJob(value: SimulationJob) { client.setQueryData(simulationKeys.job(props.projectID, props.jobID), value) }
function useResult(value: SimulationJob) {
  if (value.status === 'succeeded' && value.result_type === 'simulation_run' && value.result_id) emit('run-ready', value.result_id)
}
async function refresh() {
  try {
    const value = await getSimulationJob(props.jobID)
    storeJob(value); useResult(value)
    if (!terminal(value.status)) schedulePoll(value)
  } catch { schedulePoll(job.value) }
}
function schedulePoll(value?: SimulationJob) {
  clearPolling()
  if (!value || terminal(value.status)) return
  pollTimer = window.setTimeout(() => { void refresh() }, Math.max(value.poll_after_ms || 1000, 100))
}
function startPolling(value?: SimulationJob) { closeStream(); streamState.value = 'polling'; schedulePoll(value) }
function startStream(value: SimulationJob) {
  if (terminal(value.status)) { useResult(value); return }
  if (typeof EventSource === 'undefined') { startPolling(value); return }
  closeStream(); clearPolling(); streamState.value = 'connecting'
  // Native EventSource retains Last-Event-ID when reconnecting. The ordinal in
  // session storage deduplicates persisted-event replay after an app restart.
  sessionStorage.getItem(eventStorageKey())
  stream = new EventSource(value.events_url || `/api/v1/jobs/${value.id}/events`)
  stream.onopen = () => { streamState.value = 'connected' }
  stream.onerror = () => startPolling(value)
  stream.addEventListener('job', raw => {
    try { remember(JSON.parse((raw as MessageEvent<string>).data) as SimulationJobEvent); void refresh() } catch { startPolling(value) }
  })
  stream.addEventListener('terminal', raw => {
    try { const terminalJob = JSON.parse((raw as MessageEvent<string>).data) as SimulationJob; storeJob(terminalJob); useResult(terminalJob); closeStream(); clearPolling() } catch { startPolling(value) }
  })
}
async function cancel() {
  if (!canCancel.value || canceling.value) return
  canceling.value = true; cancelError.value = ''
  try { const value = await cancelSimulationJob(props.jobID); storeJob(value); if (terminal(value.status)) { closeStream(); clearPolling() } } catch (cause) { cancelError.value = cause instanceof Error ? cause.message : '无法请求取消模拟任务' } finally { canceling.value = false }
}
watch(() => [props.projectID, props.jobID] as const, () => { events.value = []; closeStream(); clearPolling() }, { immediate: true })
watch(job, (value, previous) => {
  if (!value) return
  if (terminal(value.status)) {
    closeStream(); clearPolling(); useResult(value)
    if (!terminal(previous?.status)) void nextTick(() => progressHeading.value?.focus())
  } else if (streamState.value === 'polling') schedulePoll(value); else startStream(value)
}, { immediate: true })
onBeforeUnmount(() => { closeStream(); clearPolling() })
</script>

<template>
  <section aria-labelledby="simulation-job-heading">
    <h2 id="simulation-job-heading" ref="progressHeading" tabindex="-1">模拟任务进度</h2>
    <p v-if="query.isPending.value" role="status">正在读取模拟任务…</p>
    <p v-else-if="query.isError.value" role="alert">{{ query.error.value?.message }} <button type="button" @click="refresh">重试状态查询</button></p>
    <template v-else-if="job">
      <p aria-live="polite">任务 {{ job.id }} · 状态：<strong>{{ job.status }}</strong> · 阶段：{{ currentPhase }} · {{ streamState === 'polling' ? 'SSE 已断开，正在按服务端建议轮询。' : streamState === 'connected' ? '已连接到实时事件流。' : '正在连接实时事件流。' }}</p>
      <p v-if="canceling" role="status">正在持久化取消请求；在安全边界前不推断最终状态。</p>
      <p v-if="job.status === 'canceled'">任务已取消；不将部分样本显示为成功结果。</p>
      <p v-else-if="job.status === 'interrupted'">任务已中断；恢复时会核对已捕获的输入与实现指纹。</p>
      <p v-else-if="job.status === 'failed'" role="alert">{{ failureMessage }}</p>
      <button v-if="canCancel" type="button" :disabled="canceling" @click="cancel">{{ canceling ? '正在请求取消…' : '取消模拟' }}</button>
      <p v-if="cancelError" role="alert">{{ cancelError }}</p>
      <ol v-if="events.length" aria-label="模拟任务事件"><li v-for="event in events" :key="event.ordinal">#{{ event.ordinal }} · {{ event.phase }} · {{ event.progress }}%<span v-if="event.warning"> · WARNING：{{ event.warning }}</span><span v-if="event.error"> · ERROR：{{ event.error }}</span></li></ol>
      <p v-else>尚无持久化进度事件。</p>
    </template>
  </section>
</template>
