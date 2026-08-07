<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useQueryClient } from '@tanstack/vue-query'
import { cancelReleaseJob, getReleaseDetail, type ReleaseDetail, type ReleaseJob, type ReleaseJobEvent, useReleaseJob, versionKeys } from '@/api/versions'

const props = defineProps<{ projectID: string; jobID: string }>()
const emit = defineEmits<{ 'release-committed': [release: ReleaseDetail] }>()
const client = useQueryClient()
const query = useReleaseJob(() => props.projectID, () => props.jobID)
const events = ref<ReleaseJobEvent[]>([])
const streamState = ref<'connecting' | 'connected' | 'polling'>('connecting')
const pollingFallback = ref(false)
const canceling = ref(false)
const cancelError = ref('')
const pointerState = ref<'idle' | 'checking' | 'confirmed' | 'waiting'>('idle')
let stream: EventSource | undefined
let pollTimer: number | undefined

const terminal = (status?: ReleaseJob['status']) => status === 'succeeded' || status === 'failed' || status === 'canceled' || status === 'interrupted'
const job = computed(() => query.data.value)
const canCancel = computed(() => job.value?.status === 'queued' || job.value?.status === 'running')
const canRetry = computed(() => job.value?.status === 'failed')
const activeConfirmed = computed(() => pointerState.value === 'confirmed')
function clearPolling() { if (pollTimer !== undefined) { window.clearTimeout(pollTimer); pollTimer = undefined } }
function closeStream() { stream?.close(); stream = undefined }
function remember(event: ReleaseJobEvent) {
  if (!events.value.some(value => value.ordinal === event.ordinal)) events.value = [...events.value, event].sort((a, b) => a.ordinal - b.ordinal)
}
function storeJob(value: ReleaseJob) { client.setQueryData(versionKeys.job(props.projectID, props.jobID), value) }
async function confirmPointer(value: ReleaseJob) {
  if (value.status !== 'succeeded' || !value.result_url || !value.result_id || pointerState.value === 'confirmed') return
  pointerState.value = 'checking'
  try {
    const release = await getReleaseDetail(value.result_url)
    if (release.id === value.result_id && release.active_pointer.release_id === release.id) {
      pointerState.value = 'confirmed'
      emit('release-committed', release)
      return
    }
  } catch {
    // A successful durable Job can briefly precede a readable pointer response.
  }
  pointerState.value = 'waiting'
  schedulePoll(value)
}
async function refresh() {
  const result = await query.refetch()
  if (result.data) {
    if (terminal(result.data.status)) clearPolling()
    await confirmPointer(result.data)
    if (!terminal(result.data.status) || pointerState.value === 'waiting') schedulePoll(result.data)
  }
}
function schedulePoll(value?: ReleaseJob) {
  clearPolling()
  if (!value || (terminal(value.status) && !(value.status === 'succeeded' && pointerState.value !== 'confirmed'))) return
  pollTimer = window.setTimeout(() => { void refresh() }, Math.max(value.poll_after_ms || 1000, 100))
}
function startPolling(value?: ReleaseJob) {
  closeStream()
  pollingFallback.value = true
  streamState.value = 'polling'
  schedulePoll(value)
}
function startStream(value: ReleaseJob) {
  if (terminal(value.status)) { void confirmPointer(value); return }
  closeStream(); clearPolling(); streamState.value = 'connecting'
  stream = new EventSource(value.events_url || `/api/v1/jobs/${props.jobID}/events`)
  stream.onopen = () => { streamState.value = 'connected' }
  stream.onerror = () => startPolling(value)
  stream.addEventListener('job', raw => {
    try { remember(JSON.parse((raw as MessageEvent<string>).data) as ReleaseJobEvent) } catch { /* malformed events are ignored; polling is authoritative */ }
  })
  stream.addEventListener('terminal', raw => {
    try {
      const value = JSON.parse((raw as MessageEvent<string>).data) as ReleaseJob
      storeJob(value); closeStream(); clearPolling(); void confirmPointer(value)
    } catch { startPolling(value) }
  })
}
async function cancel() {
  if (!canCancel.value) return
  canceling.value = true; cancelError.value = ''
  try {
    const value = await cancelReleaseJob(props.jobID)
    storeJob(value)
    if (terminal(value.status)) { closeStream(); clearPolling() }
  } catch (cause) { cancelError.value = cause instanceof Error ? cause.message : '无法取消发布任务' } finally { canceling.value = false }
}
function retryStatus() { void refresh() }
watch(() => [props.projectID, props.jobID] as const, () => { events.value = []; pointerState.value = 'idle'; pollingFallback.value = false; closeStream(); clearPolling() }, { immediate: true })
watch(job, value => { if (!value) return; if (terminal(value.status)) { closeStream(); clearPolling(); void confirmPointer(value) } else if (pollingFallback.value) schedulePoll(value); else startStream(value) }, { immediate: true })
onBeforeUnmount(() => { closeStream(); clearPolling() })
</script>
<template>
  <section aria-labelledby="job-progress-heading">
    <h2 id="job-progress-heading">发布任务进度</h2>
    <p v-if="query.isPending.value" role="status">正在读取发布任务…</p>
    <p v-else-if="query.isError.value" role="alert">{{ query.error.value?.message }} <button type="button" @click="retryStatus">重试状态查询</button></p>
    <template v-else-if="job">
      <p>任务 {{ job.id }} · 状态：<strong>{{ job.status }}</strong> · {{ streamState === 'polling' ? 'SSE 已断开，正在按服务端建议轮询。' : streamState === 'connected' ? '已连接到实时事件流。' : '正在连接实时事件流。' }}</p>
      <p v-if="job.status === 'canceled'">任务已取消：外部发布步骤尚未被接受。</p>
      <p v-else-if="job.status === 'interrupted'">任务已中断：外部步骤可能已被接受，恢复流程会核对 intent；这不是已取消或已发布。</p>
      <p v-else-if="job.status === 'succeeded' && !activeConfirmed" role="status">任务已成功，正在确认正式版本指针提交；候选尚未标记为当前正式版本。</p>
      <p v-else-if="activeConfirmed">正式版本指针已确认提交。</p>
      <p v-else-if="job.status === 'failed'" role="alert">发布任务失败。请查看持久化错误事件并在修复后重新发起预检。</p>
      <a v-if="job.result_url" :href="job.result_url" :aria-label="`打开发布任务结果 ${job.result_id ?? ''}`">打开任务结果与证据</a>
      <button v-if="canCancel" type="button" :disabled="canceling" @click="cancel">{{ canceling ? '正在请求取消…' : '取消发布任务' }}</button>
      <button v-if="canRetry" type="button" @click="retryStatus">重试状态查询</button>
      <p v-if="cancelError" role="alert">{{ cancelError }}</p>
      <ol v-if="events.length" aria-label="发布任务事件">
        <li v-for="event in events" :key="event.ordinal">#{{ event.ordinal }} · {{ event.phase }} · {{ event.progress }}%<span v-if="event.warning"> · WARNING：{{ event.warning }}</span><span v-if="event.error"> · ERROR：{{ event.error }}</span><a v-if="event.result_url" :href="event.result_url" :aria-label="`打开事件 ${event.ordinal} 的结果`">打开证据</a></li>
      </ol>
      <p v-else>尚无持久化进度事件。</p>
    </template>
  </section>
</template>
