import { fireEvent, render, screen, waitFor } from '@testing-library/vue'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import RuntimeStatusBanner from '@/components/RuntimeStatusBanner.vue'
import RuntimeSettingsView from '@/views/RuntimeSettingsView.vue'
import { useRuntimeStore, type RuntimeStatus } from '@/stores/runtime'

const now = new Date().toISOString()
function runtimeStatus(overrides: Partial<RuntimeStatus> = {}): RuntimeStatus {
  return {
    schema_version: 1, generation: 4,
    build: { version: '1.2.3', build: 'fixture', commit: 'abc', package_mode: 'lightweight' },
    listener: { url: 'http://127.0.0.1:43123' }, phase: 'ready',
    project: { state: 'none', project_id: null, recent_count: 0, recovery_required: false },
    process: { ownership: 'external', state: 'ready', launch_generation: 3, endpoint: 'http://127.0.0.1:43124', restart_attempt: 0, reason: null },
    dependencies: [],
    capabilities: [
      { id: 'local.editing', version: '1', state: 'available', observation_generation: 4, reasons: [], actions: [] },
      { id: 'graph.sync', version: '1', state: 'available', observation_generation: 4, reasons: [], actions: [] },
      { id: 'retrieval', version: '1', state: 'available', observation_generation: 4, reasons: [], actions: [] },
      { id: 'ai.design', version: '1', state: 'available', observation_generation: 4, reasons: [], actions: [] },
      { id: 'release', version: '1', state: 'available', observation_generation: 4, reasons: [], actions: [] },
    ], recovery: [], log_location: '<local-app-data>/EcoGuardian/logs', updated_at: now,
    ...overrides,
  }
}

const settings = {
  schema_version: 1, browser: { auto_open: true }, package: { mode: 'lightweight' },
  graph: { mode: 'external', endpoint: 'http://127.0.0.1:43124', health_timeout_seconds: 5, startup_timeout_seconds: 20, restart_limit: 3 },
  ai: { enabled: true, endpoint: 'http://127.0.0.1:11434/v1', model: 'fixture', request_timeout_seconds: 60, allow_cloud: false, endpoint_classification: 'loopback', credential_present: true },
  logs: { max_bytes: 5242880, max_files: 5 }, backup: { retention_days: 30 },
} as const

describe('runtime status store and global UI', () => {
  beforeEach(() => { setActivePinia(createPinia()); vi.restoreAllMocks() })

  it('keeps server capability decisions authoritative and tracks observation age/cadence', async () => {
    const value = runtimeStatus({ phase: 'degraded', capabilities: [{ id: 'graph.sync', version: '1', state: 'unavailable', observation_generation: 9, reasons: [{ code: 'GRAPH_CORE_UNAVAILABLE', component: 'graph.core', observation_generation: 9 }], actions: [] }] })
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response(JSON.stringify(value), { status: 200 }))))
    const store = useRuntimeStore()
    await store.refresh()
    expect(store.status?.capabilities[0].state).toBe('unavailable')
    expect(store.pollingCadenceMS).toBe(5000)
    expect(store.observationAgeMS).toBeGreaterThanOrEqual(0)
  })

  it('renders independent degradation, unaffected capabilities, safe locations, and server actions', async () => {
    const action = { id: 'runtime.reprobe', method: 'POST', uri: '/api/v1/runtime/reprobe', idempotency_required: true } as const
    const value = runtimeStatus({ phase: 'degraded', capabilities: [
      { id: 'local.editing', version: '1', state: 'available', observation_generation: 5, reasons: [], actions: [] },
      { id: 'graph.sync', version: '1', state: 'unavailable', observation_generation: 5, reasons: [{ code: 'GRAPH_CORE_UNAVAILABLE', component: 'graph.core', observation_generation: 5 }], actions: [action] },
      { id: 'retrieval', version: '1', state: 'degraded', observation_generation: 5, reasons: [{ code: 'VECTOR_UNAVAILABLE', component: 'retrieval.vector', observation_generation: 5 }], actions: [] },
      { id: 'ai.design', version: '1', state: 'unavailable', observation_generation: 5, reasons: [{ code: 'AI_PROVIDER_UNAVAILABLE', component: 'ai.provider', observation_generation: 5 }], actions: [] },
      { id: 'release', version: '1', state: 'unavailable', observation_generation: 5, reasons: [{ code: 'REQUIRED_GATE_UNAVAILABLE', component: 'release.gates', observation_generation: 5 }], actions: [] },
    ], recovery: [{ job_kind: 'graph_sync', state: 'reconciling', count: 1 }] })
    const fetchMock = vi.fn((_input: RequestInfo | URL, init?: RequestInit) => Promise.resolve(init?.method === 'POST' ? new Response(null, { status: 204 }) : new Response(JSON.stringify(value), { status: 200 })))
    vi.stubGlobal('fetch', fetchMock)
    const writeText = vi.fn(() => Promise.resolve())
    vi.stubGlobal('navigator', { ...navigator, clipboard: { writeText } })
    render(RuntimeStatusBanner, { global: { plugins: [createPinia()], stubs: { RouterLink: { template: '<a><slot /></a>' } } } })
    expect(await screen.findByText(/Graph core：不可用/)).toBeTruthy()
    expect(screen.getByText(/仍可使用：local.editing/)).toBeTruthy()
    expect(screen.getByText(/GRAPH_CORE_UNAVAILABLE/)).toBeTruthy()
    const reprobe = screen.getByRole('button', { name: '重新探测' })
    reprobe.focus()
    expect(document.activeElement).toBe(reprobe)
    await fireEvent.click(reprobe)
    await waitFor(() => expect(fetchMock.mock.calls.some(([, init]) => init?.method === 'POST' && (init.headers as Record<string, string>)['Idempotency-Key'])).toBe(true))
    await fireEvent.click(screen.getByRole('button', { name: '复制监听地址' }))
    expect(writeText).toHaveBeenCalledWith(value.listener.url)
  })

  it.each([
    ['loading_settings', '降级'], ['ready', '可用'], ['degraded', '降级'], ['recovering', '降级'], ['stopped', '降级'],
  ] as const)('renders phase %s with a text alternative', async (phase, ecoText) => {
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response(JSON.stringify(runtimeStatus({ phase })), { status: 200 }))))
    render(RuntimeStatusBanner, { global: { plugins: [createPinia()], stubs: { RouterLink: { template: '<a><slot /></a>' } } } })
    expect(await screen.findByText(`Eco：${ecoText}`)).toBeTruthy()
  })
})

describe('runtime settings UI', () => {
  it('saves only non-secret controls and announces apply/reconnect effects', async () => {
    const result = { settings, effects: [{ field: 'browser.auto_open', disposition: 'applied' }, { field: 'graph.endpoint', disposition: 'reconnect_required' }] }
    const fetchMock = vi.fn((_input: RequestInfo | URL, init?: RequestInit) => Promise.resolve(init?.method === 'PATCH' ? new Response(JSON.stringify(result), { status: 200 }) : new Response(JSON.stringify(settings), { status: 200 })))
    vi.stubGlobal('fetch', fetchMock)
    render(RuntimeSettingsView, { global: { plugins: [createPinia()], stubs: { RouterLink: { template: '<a><slot /></a>' } } } })
    expect(await screen.findByRole('heading', { name: '运行设置' })).toBeTruthy()
    await fireEvent.click(await screen.findByRole('button', { name: '保存设置' }))
    expect(await screen.findByText(/reconnect_required：graph.endpoint/)).toBeTruthy()
    const patch = fetchMock.mock.calls.find(([, init]) => init?.method === 'PATCH')?.[1]
    expect(String(patch?.body)).not.toMatch(/credential|password|secret|token/i)
    expect(screen.getByText(/凭据状态：已配置/)).toBeTruthy()
  })
})
