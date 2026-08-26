import { computed, ref } from 'vue'
import { defineStore } from 'pinia'
import type { components } from '@/api/generated'

export type RuntimeStatus = components['schemas']['RuntimeStatusResource']
export type RuntimeAction = components['schemas']['RuntimeAction']

type Problem = Partial<components['schemas']['Problem']>

export const useRuntimeStore = defineStore('runtime', () => {
  const status = ref<RuntimeStatus>()
  const loading = ref(false)
  const reconnecting = ref(false)
  const error = ref('')
  const receivedAt = ref(0)
  let timer: ReturnType<typeof setTimeout> | undefined
  let stopped = true

  const observationAgeMS = computed(() => status.value ? Math.max(0, Date.now() - Date.parse(status.value.updated_at)) : undefined)
  const pollingCadenceMS = computed(() => {
    const phase = status.value?.phase
    return phase === 'ready' || phase === 'degraded' ? 5000 : phase === 'stopped' ? 15000 : 1000
  })

  async function refresh() {
    if (loading.value) return status.value
    loading.value = true
    try {
      const response = await fetch('/api/v1/runtime/status', { headers: { Accept: 'application/json' } })
      if (!response.ok) {
        const problem = await response.json().catch(() => null) as Problem | null
        throw new Error(problem?.title || '无法读取运行状态')
      }
      status.value = await response.json() as RuntimeStatus
      receivedAt.value = Date.now()
      error.value = ''
      return status.value
    } catch (cause) {
      error.value = cause instanceof Error ? cause.message : '无法读取运行状态'
      return undefined
    } finally {
      loading.value = false
    }
  }

  function schedule() {
    if (stopped) return
    clearTimeout(timer)
    timer = setTimeout(async () => { await refresh(); schedule() }, pollingCadenceMS.value)
  }

  async function startPolling() {
    stopped = false
    await refresh()
    schedule()
  }

  function stopPolling() {
    stopped = true
    clearTimeout(timer)
    timer = undefined
  }

  function currentAction(candidate: RuntimeAction) {
    return status.value?.capabilities.flatMap(value => value.actions).find(value => value.id === candidate.id && value.method === candidate.method && value.uri === candidate.uri)
  }

  async function execute(candidate: RuntimeAction) {
    const action = currentAction(candidate)
    if (!action || !action.uri.startsWith('/api/v1/') || action.uri.includes('{')) throw new Error('操作前提已变化，请刷新状态')
    reconnecting.value = true
    error.value = ''
    try {
      const headers: Record<string, string> = { Accept: 'application/json' }
      if (action.idempotency_required) headers['Idempotency-Key'] = crypto.randomUUID()
      const response = await fetch(action.uri, { method: action.method, headers })
      if (!response.ok) {
        const problem = await response.json().catch(() => null) as Problem | null
        throw new Error(problem?.title || '运行时操作失败')
      }
      await refresh()
    } catch (cause) {
      error.value = cause instanceof Error ? cause.message : '运行时操作失败'
      throw cause
    } finally {
      reconnecting.value = false
    }
  }

  return { status, loading, reconnecting, error, receivedAt, observationAgeMS, pollingCadenceMS, refresh, startPolling, stopPolling, execute, currentAction }
})
