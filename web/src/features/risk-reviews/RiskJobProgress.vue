<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { useQueryClient } from '@tanstack/vue-query'
import { cancelRiskJob, getRiskJob, riskKeys, type RiskJob, type RiskJobEvent, useRiskJob } from './api'

const props = defineProps<{ projectID: string; jobID: string }>()
const emit = defineEmits<{ 'review-ready': [reportID: string]; retry: [] }>()
const client = useQueryClient()
const query = useRiskJob(() => props.projectID, () => props.jobID)
const events = ref<RiskJobEvent[]>([])
const streamState = ref<'connecting' | 'connected' | 'polling'>('connecting')
const canceling = ref(false)
const cancelError = ref('')
const heading = ref<HTMLElement>()
let stream: EventSource | undefined
let pollTimer: number | undefined

const job = computed(() => query.data.value)
const terminal = (status?: RiskJob['status']) => status === 'succeeded' || status === 'failed' || status === 'canceled' || status === 'interrupted'
const canCancel = computed(() => job.value?.status === 'queued' || job.value?.status === 'running')
const latest = computed(() => events.value.at(-1))
const phase = computed(() => latest.value?.phase ?? job.value?.status?.toUpperCase() ?? 'QUEUED')
const ordinalKey = () => `risk-job-ordinal:${props.projectID}:${props.jobID}`
function closeStream() { stream?.close(); stream = undefined }
function clearPolling() { if (pollTimer !== undefined) window.clearTimeout(pollTimer); pollTimer = undefined }
function storeJob(value: RiskJob) { client.setQueryData(riskKeys.job(props.projectID, props.jobID), value) }
function useResult(value: RiskJob) { if (value.status === 'succeeded' && value.result_type === 'risk_review' && value.result_id) emit('review-ready', value.result_id) }
function remember(value: RiskJobEvent) {
  if (!events.value.some(event => event.ordinal === value.ordinal)) events.value = [...events.value, value].sort((a, b) => a.ordinal - b.ordinal)
  sessionStorage.setItem(ordinalKey(), String(value.ordinal))
}
async function refresh() {
  try { const value = await getRiskJob(props.jobID); storeJob(value); useResult(value); if (!terminal(value.status)) schedulePoll(value) } catch { schedulePoll(job.value) }
}
function schedulePoll(value?: RiskJob) {
  clearPolling(); if (!value || terminal(value.status)) return
  pollTimer = window.setTimeout(() => { void refresh() }, Math.max(value.poll_after_ms || 1000, 100))
}
function startPolling(value?: RiskJob) { closeStream(); streamState.value = 'polling'; schedulePoll(value) }
function startStream(value: RiskJob) {
  if (terminal(value.status)) { useResult(value); return }
  if (typeof EventSource === 'undefined') { startPolling(value); return }
  closeStream(); clearPolling(); streamState.value = 'connecting'
  stream = new EventSource(value.events_url || `/api/v1/jobs/${value.id}/events`)
  stream.onopen = () => { streamState.value = 'connected' }
  stream.onerror = () => startPolling(value)
  stream.addEventListener('job', raw => {
    try { remember(JSON.parse((raw as MessageEvent<string>).data) as RiskJobEvent); void refresh() } catch { startPolling(value) }
  })
  stream.addEventListener('terminal', raw => {
    try { const value = JSON.parse((raw as MessageEvent<string>).data) as RiskJob; storeJob(value); useResult(value); closeStream(); clearPolling() } catch { startPolling(value) }
  })
}
async function cancel() {
  if (!canCancel.value || canceling.value) return
  canceling.value = true; cancelError.value = ''
  try { const value = await cancelRiskJob(props.jobID); storeJob(value); if (terminal(value.status)) { closeStream(); clearPolling() } } catch (cause) { cancelError.value = cause instanceof Error ? cause.message : '无法取消风险复核' } finally { canceling.value = false }
}
watch(() => [props.projectID, props.jobID] as const, () => { events.value = []; closeStream(); clearPolling() }, { immediate: true })
watch(job, (value, previous) => {
  if (!value) return
  if (terminal(value.status)) {
    closeStream(); clearPolling(); useResult(value)
    if (!terminal(previous?.status)) void nextTick(() => heading.value?.focus())
  } else if (streamState.value === 'polling') schedulePoll(value); else startStream(value)
}, { immediate: true })
onBeforeUnmount(() => { closeStream(); clearPolling() })
</script>

<template>
  <section aria-labelledby="risk-job-heading" class="risk-card">
    <h2 id="risk-job-heading" ref="heading" tabindex="-1">风险复核任务</h2>
    <p v-if="query.isPending.value" role="status">正在读取持久化 Job…</p>
    <p v-else-if="query.isError.value" role="alert">{{ query.error.value?.message }} <button type="button" @click="refresh">重试状态查询</button></p>
    <template v-else-if="job">
      <p aria-live="polite">Job {{ job.id }} · 状态：<strong>{{ job.status }}</strong> · 阶段：{{ phase }} · {{ streamState === 'polling' ? 'SSE 不可用，按服务端间隔轮询。' : streamState === 'connected' ? '实时事件已连接。' : '正在连接实时事件。' }}</p>
      <p v-if="job.status === 'canceled'">已取消；任何部分结果都不视为成功。</p>
      <p v-else-if="job.status === 'interrupted'">已中断；恢复前必须重新核对全部固定 identity。</p>
      <p v-else-if="job.status === 'failed'" role="alert">失败：{{ latest?.error || '安全错误详情不可用' }}。不会从进度百分比推断成功。</p>
      <button v-if="canCancel" type="button" :disabled="canceling" @click="cancel">{{ canceling ? '正在持久化取消…' : '取消复核' }}</button>
      <button v-if="job.status === 'failed' || job.status === 'canceled' || job.status === 'interrupted'" type="button" @click="emit('retry')">以当前表单创建新尝试</button>
      <p v-if="cancelError" role="alert">{{ cancelError }}</p>
      <ol v-if="events.length" aria-label="风险任务持久化事件"><li v-for="event in events" :key="event.ordinal">#{{ event.ordinal }} · {{ event.phase }} · {{ event.progress }}%<span v-if="event.warning"> · WARNING：{{ event.warning }}</span><span v-if="event.error"> · ERROR：{{ event.error }}</span></li></ol>
      <p v-else>尚无新的持久化事件；页面刷新不会创建 Job。</p>
    </template>
  </section>
</template>
