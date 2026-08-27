<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import type { Job, JobEvent } from '@/api/backups'
import { cancelJob, getJob } from '@/api/backups'

const props = defineProps<{ jobId: string; kind: 'backup' | 'restore' }>()
const emit = defineEmits<{ terminal: [job: Job] }>()
const job = ref<Job>()
const events = ref<JobEvent[]>([])
const connection = ref<'connecting' | 'live' | 'polling'>('connecting')
const error = ref('')
const canceling = ref(false)
const cancellationUnavailable = ref(false)
let stream: EventSource | undefined
let timer: number | undefined

const terminal = computed(() => ['succeeded', 'failed', 'canceled', 'interrupted'].includes(job.value?.status ?? ''))
const canCancel = computed(() => !terminal.value && !cancellationUnavailable.value && (job.value?.status === 'queued' || job.value?.status === 'running'))
const phase = computed(() => events.value.at(-1)?.phase ?? job.value?.phase ?? 'queued')
const progress = computed(() => events.value.at(-1)?.progress ?? job.value?.progress ?? 0)
function close() { stream?.close(); stream = undefined; if (timer !== undefined) window.clearTimeout(timer); timer = undefined }
function accept(value: Job) { job.value = value; if (['succeeded', 'failed', 'canceled', 'interrupted'].includes(value.status)) { close(); emit('terminal', value) } }
async function refresh() { try { accept(await getJob(props.jobId)); if (!terminal.value) schedule() } catch (cause) { error.value = cause instanceof Error ? cause.message : '无法读取任务'; schedule() } }
function schedule() { if (timer !== undefined) window.clearTimeout(timer); if (!terminal.value) timer = window.setTimeout(() => void refresh(), Math.max(job.value?.poll_after_ms ?? 1000, 100)) }
function connect() {
  close(); connection.value = 'connecting'
  if (typeof EventSource === 'undefined') { connection.value = 'polling'; schedule(); return }
  stream = new EventSource(job.value?.events_url || `/api/v1/jobs/${props.jobId}/events`)
  stream.onopen = () => { connection.value = 'live' }
  stream.onerror = () => { close(); connection.value = 'polling'; schedule() }
  stream.addEventListener('job', raw => { try { const event = JSON.parse((raw as MessageEvent<string>).data) as JobEvent; if (!events.value.some(item => item.ordinal === event.ordinal)) events.value = [...events.value, event].sort((a, b) => a.ordinal - b.ordinal); if (props.kind === 'restore' && ['maintenance', 'restore_pre_backup', 'connections_closed', 'database_staged', 'original_parked', 'installed', 'verified', 'reopened', 'reconciled'].includes(event.phase)) cancellationUnavailable.value = true; void refresh() } catch { connection.value = 'polling'; schedule() } })
  stream.addEventListener('terminal', raw => { try { accept(JSON.parse((raw as MessageEvent<string>).data) as Job) } catch { void refresh() } })
}
async function requestCancel() { if (!canCancel.value) return; canceling.value = true; error.value = ''; try { const value = await cancelJob(props.jobId); if (value.status === job.value?.status && !value.cancel_requested_at) cancellationUnavailable.value = true; accept(value) } catch (cause) { error.value = cause instanceof Error ? cause.message : '无法请求取消' } finally { canceling.value = false } }
watch(() => props.jobId, () => { job.value = undefined; events.value = []; cancellationUnavailable.value = false; error.value = ''; void refresh().then(connect) }, { immediate: true })
onBeforeUnmount(close)
</script>

<template>
  <section class="job" :aria-labelledby="`job-${jobId}`">
    <h3 :id="`job-${jobId}`" tabindex="-1">{{ kind === 'restore' ? '恢复' : '备份' }}任务</h3>
    <p v-if="!job" role="status">正在读取服务端任务…</p>
    <template v-else>
      <p aria-live="polite">状态：<strong>{{ job.status }}</strong> · 阶段：{{ phase }} · {{ progress }}% · {{ connection === 'live' ? 'SSE 已连接' : connection === 'polling' ? 'SSE 断开，正在轮询' : '正在连接' }}</p>
      <progress :value="progress" max="100">{{ progress }}%</progress>
      <p v-if="job.warning">提示：{{ job.warning }}</p>
      <p v-if="job.error" role="alert">{{ job.error }}</p>
      <p v-if="job.recovery_required" role="alert">需要安全恢复；不要移动或修改项目文件，请重启应用并查看恢复状态。</p>
      <p v-if="cancellationUnavailable">已越过不可中断边界；应用会完成验证或安全回滚，不能再取消。</p>
      <button v-if="canCancel" type="button" :disabled="canceling" @click="requestCancel">{{ canceling ? '正在请求取消…' : '取消任务' }}</button>
      <p v-if="job.status === 'succeeded'">任务成功。<a v-if="job.result_url" :href="job.result_url">查看结果</a></p>
      <p v-if="job.status === 'interrupted'">任务已中断；只以重启后的 journal 恢复结果为准。</p>
      <ol v-if="events.length" aria-label="持久化任务事件"><li v-for="event in events" :key="event.ordinal">#{{ event.ordinal }} · {{ event.phase }} · {{ event.progress }}%<span v-if="event.warning"> · {{ event.warning }}</span><span v-if="event.error"> · {{ event.error }}</span></li></ol>
    </template>
    <p v-if="error" role="alert">{{ error }} <button type="button" @click="refresh">重试查询</button></p>
  </section>
</template>

<style scoped>.job { border: 1px solid #8c9aa8; padding: 1rem; margin-block: 1rem; } progress { width: min(32rem, 100%); }</style>
