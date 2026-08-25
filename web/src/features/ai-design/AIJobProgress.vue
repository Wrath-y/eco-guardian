<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { useQueryClient } from '@tanstack/vue-query'
import { aiKeys, cancelAIJob, getAIJob, type AIJob, type AIJobEvent, useAIJob } from './api'
import { aiFailureGuidance } from './status'

const props = defineProps<{ projectID: string; jobID: string }>()
const emit = defineEmits<{ 'patch-ready': [patchID: string]; retry: [] }>()
const client = useQueryClient()
const query = useAIJob(() => props.projectID, () => props.jobID)
const events = ref<AIJobEvent[]>([])
const streamState = ref<'connecting' | 'connected' | 'polling'>('connecting')
const canceling = ref(false)
const actionError = ref('')
const heading = ref<HTMLElement>()
let stream: EventSource | undefined
let pollTimer: number | undefined
let terminalStreamStarted = false

const job = computed(() => query.data.value)
const terminal = (status?: AIJob['status']) => status === 'succeeded' || status === 'failed' || status === 'canceled' || status === 'interrupted'
const canCancel = computed(() => job.value?.status === 'queued' || job.value?.status === 'running')
const lastError = computed(() => [...events.value].reverse().find(event => event.error)?.error)
const canRetry = computed(() => (job.value?.status === 'failed' || job.value?.status === 'interrupted') && Boolean(lastError.value?.retryable))
const repairCount = computed(() => Math.max(0, ...events.value.map(event => event.repair_count ?? 0)))
const current = computed(() => events.value.at(-1))
const storageKey = () => `ai-job-ordinal:${props.projectID}:${props.jobID}`
function closeStream() { stream?.close(); stream = undefined }
function clearPolling() { if (pollTimer !== undefined) { window.clearTimeout(pollTimer); pollTimer = undefined } }
function storeJob(value: AIJob) {
  client.setQueryData(aiKeys.job(props.projectID, props.jobID), value)
  if (value.status === 'succeeded' && value.result_type === 'draft_patch' && value.result_id) emit('patch-ready', value.result_id)
}
function remember(event: AIJobEvent) {
  if (!events.value.some(value => value.ordinal === event.ordinal)) events.value = [...events.value, event].sort((left, right) => left.ordinal - right.ordinal)
  sessionStorage.setItem(storageKey(), String(event.ordinal))
}
async function refresh() {
  try {
    const value = await getAIJob(props.jobID)
    storeJob(value)
    if (!terminal(value.status)) schedulePoll(value)
  } catch { schedulePoll(job.value) }
}
function schedulePoll(value?: AIJob) {
  clearPolling()
  if (!value || terminal(value.status)) return
  pollTimer = window.setTimeout(() => { void refresh() }, Math.max(value.poll_after_ms || 1000, 100))
}
function startPolling(value?: AIJob) { closeStream(); streamState.value = 'polling'; schedulePoll(value) }
function startStream(value: AIJob) {
	if (typeof EventSource === 'undefined') { startPolling(value); return }
  closeStream(); clearPolling(); streamState.value = 'connecting'
  stream = new EventSource(value.events_url || `/api/v1/jobs/${value.id}/events`)
  stream.onopen = () => { streamState.value = 'connected' }
  stream.onerror = () => startPolling(value)
  stream.addEventListener('job', raw => {
    try { remember(JSON.parse((raw as MessageEvent<string>).data) as AIJobEvent); void refresh() } catch { startPolling(value) }
  })
  stream.addEventListener('terminal', raw => {
    try { storeJob(JSON.parse((raw as MessageEvent<string>).data) as AIJob); closeStream(); clearPolling() } catch { startPolling(value) }
  })
}
async function cancel() {
  if (!canCancel.value || canceling.value) return
  canceling.value = true; actionError.value = ''
  try { const value = await cancelAIJob(props.jobID); storeJob(value); if (terminal(value.status)) { closeStream(); clearPolling() } }
  catch (cause) { actionError.value = cause instanceof Error ? cause.message : '无法请求取消 AI Job' } finally { canceling.value = false }
}
watch(() => [props.projectID, props.jobID] as const, () => { events.value = []; terminalStreamStarted = false; closeStream(); clearPolling(); streamState.value = 'connecting' }, { immediate: true })
watch(job, (value, previous) => {
	if (!value) return
	if (terminal(value.status)) {
		clearPolling(); storeJob(value)
		if (!terminalStreamStarted && typeof EventSource !== 'undefined') { terminalStreamStarted = true; startStream(value) } else if (events.value.length) closeStream()
		if (!terminal(previous?.status)) void nextTick(() => heading.value?.focus())
  } else if (streamState.value === 'polling') schedulePoll(value); else startStream(value)
}, { immediate: true })
onBeforeUnmount(() => { closeStream(); clearPolling() })
</script>

<template>
  <section class="ai-card" aria-labelledby="ai-job-heading">
    <h2 id="ai-job-heading" ref="heading" tabindex="-1">AI 设计 Job</h2>
    <p v-if="query.isPending.value" role="status">正在读取持久化 Job…</p>
    <p v-else-if="query.isError.value" role="alert">{{ query.error.value?.message }} <button type="button" @click="refresh">重试状态查询</button></p>
    <template v-else-if="job">
      <p aria-live="polite">状态：<strong>{{ job.status }}</strong>；阶段：{{ current?.phase ?? job.phase ?? '等待事件' }}；进度 {{ current?.progress ?? 0 }}%；格式修复 {{ repairCount }}/3。{{ streamState === 'polling' ? 'SSE 不可用，正在按服务端 cadence 轮询。' : streamState === 'connected' ? '已连接持久化事件流。' : '正在连接持久化事件流。' }}</p>
      <p v-if="job.status === 'queued'">请求已排队，尚未调用 Provider。</p>
      <p v-else-if="job.status === 'running'">只显示已提交的阶段事件；流式模型片段不会显示为候选。</p>
      <p v-else-if="job.status === 'canceled'">取消已持久化；任何晚到结果都不能密封 DraftPatch。</p>
      <p v-else-if="job.status === 'interrupted'" role="alert">{{ aiFailureGuidance(lastError) }}</p>
      <p v-else-if="job.status === 'failed'" role="alert">{{ aiFailureGuidance(lastError) }}<span v-if="lastError?.request_id"> 请求 ID：{{ lastError.request_id }}</span></p>
      <p v-else-if="job.status === 'succeeded'">DraftPatch 已密封，正在读取不可变审阅资源。</p>
      <button v-if="canCancel" type="button" :disabled="canceling" @click="cancel">{{ canceling ? '正在请求取消…' : '取消 AI Job' }}</button>
      <button v-if="canRetry" type="button" @click="emit('retry')">确认并显式创建新尝试</button>
      <p v-if="lastError?.rebuild_required" role="note">下一步：显式重建冻结 Snapshot 的检索索引；AI 页面不会自动触发重建。</p>
      <p v-if="actionError" role="alert">{{ actionError }}</p>
      <ol v-if="events.length" aria-label="AI Job 持久化事件">
        <li v-for="event in events" :key="event.ordinal">#{{ event.ordinal }} · {{ event.kind ?? 'progress' }} · {{ event.phase }} · {{ event.progress }}%<span v-if="event.warning_code"> · WARNING {{ event.warning_code }} {{ event.warning_ref }}</span><span v-if="event.tool"> · TOOL {{ event.tool.id }}@{{ event.tool.version }}</span><span v-if="event.error"> · ERROR {{ event.error.code }}（{{ event.error.retryable ? '可重试' : '不可重试' }}）</span></li>
      </ol>
      <p v-else>尚无持久化事件；不会从部分 Provider 输出推断进度。</p>
    </template>
  </section>
</template>
