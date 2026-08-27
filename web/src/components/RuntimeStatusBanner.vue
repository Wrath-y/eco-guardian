<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useRuntimeStore, type RuntimeAction } from '@/stores/runtime'

const runtime = useRuntimeStore()
const copyMessage = ref('')
const actionMessage = ref('')
const status = computed(() => runtime.status)
const capabilities = computed(() => new Map(status.value?.capabilities.map(value => [value.id, value]) ?? []))
const dependencies = computed(() => new Map(status.value?.dependencies.map(value => [value.id, value]) ?? []))
const recoveryState = computed(() => status.value?.recovery.some(value => value.state === 'recovery_required') ? 'unavailable' : status.value?.recovery.some(value => value.state === 'pending' || value.state === 'reconciling') ? 'degraded' : 'available')
const indicators = computed(() => [
  { id: 'eco', label: 'Eco', state: status.value?.phase === 'ready' ? 'available' : status.value?.phase === 'degraded' ? 'degraded' : status.value ? 'degraded' : 'unavailable' },
  { id: 'graph-core', label: 'Graph core', state: dependencies.value.get('graph.core')?.state ?? capabilities.value.get('graph.sync')?.state ?? 'unavailable' },
  { id: 'graph-fts', label: 'Graph/FTS', state: dependencies.value.get('graph.fts')?.state ?? capabilities.value.get('graph.sync')?.state ?? 'unavailable' },
  { id: 'bm25', label: 'BM25', state: dependencies.value.get('retrieval.bm25')?.state ?? capabilities.value.get('retrieval')?.state ?? 'unavailable' },
  { id: 'vector', label: 'Vector', state: dependencies.value.get('retrieval.vector')?.state ?? capabilities.value.get('retrieval')?.state ?? 'unavailable' },
  { id: 'rerank', label: 'Rerank', state: dependencies.value.get('retrieval.rerank')?.state ?? capabilities.value.get('retrieval')?.state ?? 'unavailable' },
  { id: 'ai', label: 'AI Provider', state: dependencies.value.get('ai.provider')?.state ?? capabilities.value.get('ai.design')?.state ?? 'unavailable' },
  { id: 'recovery', label: '恢复', state: recoveryState.value },
  { id: 'release', label: '发布', state: capabilities.value.get('release')?.state ?? 'unavailable' },
])
const affected = computed(() => status.value?.capabilities.filter(value => value.state !== 'available') ?? [])
const unaffected = computed(() => status.value?.capabilities.filter(value => value.state === 'available') ?? [])
const actions = computed(() => {
  const unique = new Map<string, RuntimeAction>()
  for (const capability of status.value?.capabilities ?? []) for (const action of capability.actions) unique.set(`${action.id}:${action.uri}`, action)
  return [...unique.values()]
})
const ageLabel = computed(() => runtime.observationAgeMS === undefined ? '尚未观察' : `${Math.floor(runtime.observationAgeMS / 1000)} 秒前`)

function icon(state: string) { return state === 'available' || state === 'healthy' ? '✓' : state === 'degraded' ? '!' : '×' }
function stateText(state: string) { return state === 'available' || state === 'healthy' ? '可用' : state === 'degraded' ? '降级' : '不可用' }
function actionLabel(action: RuntimeAction) { return ({ 'runtime.reprobe': '重新探测', 'graph.reconnect': '重新连接', 'graph.retry': '重试 Graph', 'job.cancel': '取消 Job', 'provider.settings': 'Provider 设置', 'credential.configure': '配置凭据', 'backup.retry': '重试备份', 'backup.settings': '备份设置', 'restore.inspect': '查看恢复状态' } as Record<string, string>)[action.id] ?? action.id }
async function run(action: RuntimeAction) {
  actionMessage.value = ''
  try { await runtime.execute(action); actionMessage.value = `${actionLabel(action)}已提交。` } catch { actionMessage.value = runtime.error }
}
async function copy(value: string, label: string) {
  try { await navigator.clipboard.writeText(value); copyMessage.value = `${label}已复制。` } catch { copyMessage.value = `${label}复制失败，请手动选择。` }
}

onMounted(() => runtime.startPolling())
onUnmounted(() => runtime.stopPolling())
</script>

<template>
  <aside class="runtime-shell" aria-labelledby="runtime-heading">
    <div class="runtime-title-row">
      <h1 id="runtime-heading">本地运行状态</h1>
      <span aria-live="polite">{{ runtime.loading ? '正在刷新' : `观察于${ageLabel}` }}</span>
      <button type="button" :disabled="runtime.loading" @click="runtime.refresh">刷新</button>
      <RouterLink to="/settings">运行设置</RouterLink>
    </div>
    <p v-if="runtime.error" role="alert">{{ runtime.error }}</p>
    <ul v-if="status" class="service-indicators" aria-label="服务可用性">
      <li v-for="item in indicators" :key="item.id" :data-state="item.state"><span aria-hidden="true">{{ icon(item.state) }}</span> {{ item.label }}：{{ stateText(item.state) }}</li>
    </ul>
    <section v-if="status && (status.phase === 'degraded' || affected.length)" class="degradation" aria-labelledby="degradation-heading" aria-live="polite">
      <h2 id="degradation-heading">部分服务降级</h2>
      <ul><li v-for="item in affected" :key="item.id"><strong>{{ item.id }}</strong>：{{ item.state }}<span v-if="item.reasons.length">（{{ item.reasons.map(reason => reason.code).join('、') }}）</span></li></ul>
      <p>仍可使用：{{ unaffected.length ? unaffected.map(item => item.id).join('、') : '当前无已确认可用能力' }}。</p>
      <div class="runtime-actions">
        <template v-for="action in actions" :key="`${action.id}:${action.uri}`">
          <RouterLink v-if="action.id === 'provider.settings' || action.id === 'credential.configure' || action.id === 'backup.settings'" to="/settings">{{ actionLabel(action) }}</RouterLink>
          <RouterLink v-else-if="action.id === 'restore.inspect'" to="/backups">{{ actionLabel(action) }}</RouterLink>
          <button v-else type="button" :disabled="runtime.reconnecting || !runtime.currentAction(action)" @click="run(action)">{{ actionLabel(action) }}</button>
        </template>
      </div>
    </section>
    <div v-if="status" class="runtime-locations">
      <span>监听地址：<code>{{ status.listener.url }}</code> <button type="button" aria-label="复制监听地址" @click="copy(status.listener.url, '监听地址')">复制</button></span>
      <span>日志位置：<code>{{ status.log_location }}</code> <button type="button" aria-label="复制日志位置" @click="copy(status.log_location, '日志位置')">复制</button></span>
    </div>
    <p class="sr-only" aria-live="polite">{{ copyMessage }} {{ actionMessage }}</p>
  </aside>
</template>

<style scoped>
.runtime-shell { border: 1px solid #aab5c0; border-radius: .5rem; padding: .75rem; background: #f7fafc; }
.runtime-title-row, .runtime-locations, .runtime-actions { display: flex; flex-wrap: wrap; align-items: center; gap: .75rem; }
.runtime-title-row h1 { font-size: 1.1rem; margin: 0 auto 0 0; }
.service-indicators { display: flex; flex-wrap: wrap; gap: .5rem 1rem; padding: 0; list-style: none; }
.service-indicators li[data-state="available"], .service-indicators li[data-state="healthy"] { color: #176b31; }
.service-indicators li[data-state="degraded"] { color: #805500; }
.service-indicators li[data-state="unavailable"] { color: #9b1c1c; }
.degradation { border-left: 4px solid #b36b00; padding-left: .75rem; }
.degradation h2 { font-size: 1rem; }
.runtime-locations code { overflow-wrap: anywhere; }
.sr-only { position: absolute; width: 1px; height: 1px; padding: 0; margin: -1px; overflow: hidden; clip: rect(0, 0, 0, 0); white-space: nowrap; border: 0; }
</style>
