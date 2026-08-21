<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { useQueryClient } from '@tanstack/vue-query'
import { getGraphJob, graphKeys, retryGraphSync, type GraphJob, type GraphJobEvent, useGraphStatus } from '@/api/graph'

const props = defineProps<{ projectID: string; revisionID: string; compact?: boolean }>()
const query = useGraphStatus(() => props.projectID, () => props.revisionID)
const client = useQueryClient()
const statusText = computed(() => ({ saved: '已保存', validating: '正在校验', blocked_validation: '校验已阻断', graph_queued: '图谱已排队', graph_building: '图谱构建中', graph_ready: '图谱已就绪', graph_failed: '图谱失败' } as Record<string, string>)[query.data.value?.pipeline_state ?? ''] ?? '图谱状态未知')
const components = computed(() => query.data.value?.provider?.components ?? [])
const events = ref<GraphJobEvent[]>([])
const polledJob = ref<GraphJob>()
const streamState = ref<'connecting' | 'connected' | 'polling'>('connecting')
const retrying = ref(false)
const retryMessage = ref('')
const retryFeedback = ref<HTMLElement>()
let stream: EventSource | undefined
let pollTimer: number | undefined

const job = computed(() => polledJob.value ?? query.data.value?.job)
const terminal = (status?: GraphJob['status']) => status === 'succeeded' || status === 'failed' || status === 'canceled' || status === 'interrupted'
const canRetry = computed(() => query.data.value?.actions?.includes('retry') && job.value?.status === 'failed')
const eventStorageKey = (jobID: string) => `graph-job-ordinal:${props.projectID}:${jobID}`
const retryStorageKey = (jobID: string) => `graph-retry:${props.revisionID}:${jobID}`
function closeStream() { stream?.close(); stream = undefined }
function clearPolling() { if (pollTimer !== undefined) { window.clearTimeout(pollTimer); pollTimer = undefined } }
function remember(event: GraphJobEvent) {
  if (!events.value.some(value => value.ordinal === event.ordinal)) events.value = [...events.value, event].sort((a, b) => a.ordinal - b.ordinal)
  sessionStorage.setItem(eventStorageKey(event.job_id), String(event.ordinal))
}
function storeJob(value: GraphJob) {
  polledJob.value = value
  client.setQueryData(graphKeys.job(props.projectID, value.id), value)
}
async function refresh(value = job.value) {
  if (!value) return
  try { storeJob(await getGraphJob(value.id)) } catch { /* graph status is the authoritative fallback when the common Job route is temporarily unavailable */ }
  await query.refetch()
  const current = job.value
  if (current && !terminal(current.status)) schedulePoll(current)
}
function schedulePoll(value?: GraphJob) {
  clearPolling()
  if (!value || terminal(value.status)) return
  pollTimer = window.setTimeout(() => { void refresh(value) }, Math.max(value.poll_after_ms || 1000, 100))
}
function startPolling(value?: GraphJob) {
  closeStream(); streamState.value = 'polling'; schedulePoll(value)
}
function startStream(value: GraphJob) {
  if (terminal(value.status)) return
  if (typeof EventSource === 'undefined') { startPolling(value); return }
  closeStream(); clearPolling(); streamState.value = 'connecting'
  // EventSource retains Last-Event-ID during reconnects. Across reloads the
  // durable Job/status read below remains authoritative; the ordinal lets the
  // UI deduplicate replayed persisted events.
  const persisted = sessionStorage.getItem(eventStorageKey(value.id))
  if (persisted) { /* intentionally retain the persisted ordinal for deduplication */ }
  stream = new EventSource(value.events_url || `/api/v1/jobs/${value.id}/events`)
  stream.onopen = () => { streamState.value = 'connected' }
  stream.onerror = () => startPolling(value)
  stream.addEventListener('job', raw => {
    try { remember(JSON.parse((raw as MessageEvent<string>).data) as GraphJobEvent); void query.refetch() } catch { startPolling(value) }
  })
  stream.addEventListener('terminal', raw => {
    try { storeJob(JSON.parse((raw as MessageEvent<string>).data) as GraphJob); closeStream(); clearPolling(); void query.refetch() } catch { startPolling(value) }
  })
}
function idempotencyKey(jobID: string) {
  const storageKey = retryStorageKey(jobID)
  const existing = sessionStorage.getItem(storageKey)
  if (existing) return existing
  const key = typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function' ? crypto.randomUUID() : `graph-retry-${jobID}-${Date.now()}`
  sessionStorage.setItem(storageKey, key)
  return key
}
async function retry() {
  const failed = job.value
  if (!failed || !canRetry.value || retrying.value) return
  retrying.value = true; retryMessage.value = ''
  try {
    const accepted = await retryGraphSync(props.revisionID, failed.id, idempotencyKey(failed.id))
    storeJob(accepted.job)
    retryMessage.value = `已提交重试请求，复用 Job ${accepted.job.id}。`
    await query.refetch()
  } catch (cause) {
    retryMessage.value = cause instanceof Error ? `重试未完成：${cause.message}` : '重试未完成；再次操作会复用同一幂等键。'
  } finally {
    retrying.value = false
    await nextTick()
    retryFeedback.value?.focus()
    // Query updates can replace the status block in the same render cycle.
    await nextTick()
    retryFeedback.value?.focus()
  }
}
watch(() => [props.projectID, props.revisionID] as const, () => { events.value = []; polledJob.value = undefined; retryMessage.value = ''; closeStream(); clearPolling() }, { immediate: true })
watch(job, value => { if (!value) return; if (terminal(value.status)) { closeStream(); clearPolling() } else if (streamState.value === 'polling') schedulePoll(value); else startStream(value) }, { immediate: true })
onBeforeUnmount(() => { closeStream(); clearPolling() })
</script>
<template>
  <section class="graph-status" :aria-label="`revision ${revisionID} 的图谱状态`">
    <h3 v-if="!compact">图谱状态</h3>
    <p v-if="query.isPending.value" role="status">正在加载图谱状态…</p>
    <p v-else-if="query.isError.value" role="alert">无法加载图谱状态：{{ query.error.value?.message }} <button type="button" @click="query.refetch()">重试</button></p>
    <template v-else-if="query.data.value">
      <p><strong>{{ statusText }}</strong> · 新鲜度：{{ query.data.value.freshness ?? 'unknown' }}<span v-if="query.data.value.freshness_reasons?.length">（{{ query.data.value.freshness_reasons.join('、') }}）</span></p>
      <template v-if="!compact"><p v-if="query.data.value.projection">projector {{ query.data.value.projection.projection_schema_version }}/{{ query.data.value.projection.projector_version }} · Nodes {{ query.data.value.projection.node_count }} · Edges {{ query.data.value.projection.edge_count }} · <code>{{ query.data.value.projection.graph_manifest_hash }}</code></p><p v-else>尚无可用投影摘要。</p><p v-if="job">Job {{ job.status }}<span v-if="query.data.value.job_phase"> · {{ query.data.value.job_phase }} {{ query.data.value.job_progress ?? 0 }}%</span> · {{ streamState === 'polling' ? 'SSE 已断开，正在轮询持久 Job。' : streamState === 'connected' ? '已连接到实时事件流。' : '正在连接实时事件流。' }}</p><ol v-if="events.length" aria-label="图谱任务事件"><li v-for="event in events" :key="event.ordinal">#{{ event.ordinal }} · {{ event.phase }} · {{ event.progress }}%<span v-if="event.warning"> · WARNING：{{ event.warning }}</span><span v-if="event.error"> · ERROR：{{ event.error }}</span></li></ol><p v-if="query.data.value.provider">Graph/FTS/Vector：<span v-for="component in components" :key="component.name">{{ component.name }}={{ component.state }}；</span>观测于 {{ query.data.value.provider.observed_at }}</p></template>
      <ul v-if="query.data.value.warnings?.length" aria-label="图谱警告"><li v-for="warning in query.data.value.warnings" :key="warning.code">WARNING：{{ warning.code }}</li></ul>
      <p v-if="query.data.value.impact_state === 'queued'">影响分析已排队，等待下游模块处理。</p>
      <p v-if="query.data.value.error" role="alert">{{ query.data.value.error.code }}<span v-if="query.data.value.error.request_id"> · request {{ query.data.value.error.request_id }}</span></p>
      <template v-if="canRetry"><button type="button" :disabled="retrying" @click="retry">{{ retrying ? '正在提交重试…' : '重试图谱同步' }}</button><p>业务配置编辑不受图谱失败影响。</p></template>
      <p v-if="retryMessage" ref="retryFeedback" tabindex="-1" :role="retryMessage.startsWith('重试未完成') ? 'alert' : 'status'">{{ retryMessage }}</p>
    </template>
    <p v-else>该 revision 尚无图谱状态。</p>
  </section>
</template>
