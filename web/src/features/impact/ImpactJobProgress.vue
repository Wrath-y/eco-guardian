<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useQueryClient } from '@tanstack/vue-query'
import { cancelImpactJob, getImpactJob, impactKeys, type ImpactJob, type ImpactJobEvent, useImpactJob } from './api'

const props = defineProps<{ projectID: string; jobID: string }>()
const emit = defineEmits<{ ready: [reportID: string]; retry: [] }>()
const queryClient = useQueryClient()
const query = useImpactJob(() => props.projectID, () => props.jobID)
const events = ref<ImpactJobEvent[]>([])
const connection = ref<'connecting' | 'live' | 'polling'>('connecting')
const canceling = ref(false)
let stream: EventSource | undefined
let timer: number | undefined

const job = computed(() => query.data.value)
const terminal = (value?: ImpactJob) => ['succeeded', 'failed', 'canceled', 'interrupted'].includes(value?.status ?? '')
const canCancel = computed(() => job.value?.status === 'queued' || job.value?.status === 'running')
const latest = computed(() => events.value.at(-1))
function close() { stream?.close(); stream = undefined; if (timer !== undefined) window.clearTimeout(timer); timer = undefined }
function store(value: ImpactJob) { queryClient.setQueryData(impactKeys.job(props.projectID, props.jobID), value); if (value.status === 'succeeded' && value.result_type === 'impact_analysis' && value.result_id) emit('ready', value.result_id) }
async function refresh() { try { const value = await getImpactJob(props.jobID); store(value); if (!terminal(value)) poll(value) } catch { poll(job.value) } }
function poll(value?: ImpactJob) { close(); connection.value = 'polling'; if (!value || terminal(value)) return; timer = window.setTimeout(() => void refresh(), Math.max(value.poll_after_ms || 1000, 100)) }
function connect(value: ImpactJob) {
  if (terminal(value)) { store(value); return }
  if (typeof EventSource === 'undefined') { poll(value); return }
  close(); connection.value = 'connecting'; stream = new EventSource(value.events_url || `/api/v1/jobs/${value.id}/events`)
  stream.onopen = () => { connection.value = 'live' }
  stream.onerror = () => poll(value)
  stream.addEventListener('job', event => { try { const item = JSON.parse((event as MessageEvent<string>).data) as ImpactJobEvent; if (!events.value.some(existing => existing.ordinal === item.ordinal)) events.value = [...events.value, item].sort((a, b) => a.ordinal - b.ordinal); sessionStorage.setItem(`impact-ordinal:${props.projectID}:${props.jobID}`, String(item.ordinal)); void refresh() } catch { poll(value) } })
  stream.addEventListener('terminal', event => { try { store(JSON.parse((event as MessageEvent<string>).data) as ImpactJob); close() } catch { poll(value) } })
}
async function cancel() { if (!canCancel.value) return; canceling.value = true; try { store(await cancelImpactJob(props.jobID)) } finally { canceling.value = false } }
watch(() => [props.projectID, props.jobID] as const, () => { events.value = []; close() }, { immediate: true })
watch(job, value => { if (value) connection.value === 'polling' ? poll(value) : connect(value) }, { immediate: true })
onBeforeUnmount(close)
</script>

<template>
  <section class="card" aria-labelledby="impact-job-title">
    <h2 id="impact-job-title" tabindex="-1">影响分析任务</h2>
    <p v-if="query.isPending.value" role="status">正在读取持久化 Job…</p>
    <p v-else-if="query.isError.value" role="alert">{{ query.error.value?.message }} <button type="button" @click="refresh">重试查询</button></p>
    <template v-else-if="job">
      <p aria-live="polite">状态：<strong>{{ job.status }}</strong> · 阶段：{{ latest?.phase ?? 'QUEUED' }} · {{ latest?.progress ?? 0 }}% · {{ connection === 'live' ? 'SSE 已连接' : connection === 'polling' ? '断线后轮询' : '正在连接 SSE' }}</p>
      <p v-if="job.status === 'failed'" role="alert">{{ latest?.error || '分析失败，未生成部分成功报告。' }}</p>
      <p v-if="job.status === 'canceled'">任务已取消；暂存数据不作为报告展示。</p>
      <p v-if="job.status === 'interrupted'">任务已中断，服务恢复时会重新核对固定 identity。</p>
      <button v-if="canCancel" type="button" :disabled="canceling" @click="cancel">{{ canceling ? '正在取消…' : '取消任务' }}</button>
      <button v-if="terminal(job) && job.status !== 'succeeded'" type="button" @click="emit('retry')">创建新尝试</button>
      <ol v-if="events.length" aria-label="持久化任务事件"><li v-for="event in events" :key="event.ordinal">#{{ event.ordinal }} {{ event.phase }} · {{ event.progress }}% <span v-if="event.warning">· {{ event.warning }}</span></li></ol>
    </template>
  </section>
</template>
