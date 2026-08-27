import { fireEvent, render, screen, waitFor } from '@testing-library/vue'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import RuntimeStatusBanner from '@/components/RuntimeStatusBanner.vue'
import MaintenanceOverlay from '@/components/MaintenanceOverlay.vue'
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
  logs: { max_bytes: 5242880, max_files: 5 }, backup: { retention_days: 30, root_selection_state: 'custom', daily_retention_count: 10, release_migration_retention_count: 5, root_health: 'healthy' },
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

  it('keeps the global maintenance overlay until server-authoritative restore state clears', async () => {
    const pinia = createPinia()
    setActivePinia(pinia)
    const store = useRuntimeStore()
    store.runtimeCapabilities = { backup: { available: false, root_health: 'healthy', restore_state: 'maintenance', recovery_required: false, disabled_reasons: ['RESTORE_REPLACEMENT_NON_INTERRUPTIBLE'], safe_actions: ['inspect_recovery'] } } as typeof store.runtimeCapabilities
    render(MaintenanceOverlay, { global: { plugins: [pinia], stubs: { RouterLink: { template: '<a href="#"><slot /></a>' } } } })
    expect(await screen.findByRole('alertdialog')).toBeTruthy()
    expect(screen.getByText(/不能取消、修改、切换或关闭项目/)).toBeTruthy()
    expect(screen.getByRole('link', { name: '查看恢复与 Job 状态' })).toBeTruthy()
    store.runtimeCapabilities = { ...store.runtimeCapabilities!, backup: { ...store.runtimeCapabilities!.backup, restore_state: 'idle', available: true, disabled_reasons: [], safe_actions: [] } }
    await waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull())
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

  it('selects and resets the backup root without exposing a path input', async () => {
    const selected = { ...settings, backup: { ...settings.backup, root_selection_state: 'custom' as const } }
    const reset = { ...settings, backup: { ...settings.backup, root_selection_state: 'default' as const } }
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const uri = String(input)
      if (uri.endsWith('/backup-root-selection')) return Promise.resolve(new Response(JSON.stringify({ settings: selected, effects: [{ field: 'backup.root', disposition: 'applied' }] }), { status: 200 }))
      if (init?.method === 'PATCH') return Promise.resolve(new Response(JSON.stringify({ settings: reset, effects: [{ field: 'backup.root', disposition: 'applied' }] }), { status: 200 }))
      return Promise.resolve(new Response(JSON.stringify(settings), { status: 200 }))
    })
    vi.stubGlobal('fetch', fetchMock)
    render(RuntimeSettingsView, { global: { plugins: [createPinia()], stubs: { RouterLink: { template: '<a><slot /></a>' } } } })
    await fireEvent.click(await screen.findByRole('button', { name: '选择自定义备份目录' }))
    expect(await screen.findByText('自定义备份根已验证并应用。')).toBeTruthy()
    await fireEvent.click(screen.getByRole('button', { name: '恢复默认备份目录' }))
    expect(await screen.findByText('已恢复默认备份根。')).toBeTruthy()
    expect(screen.queryByRole('textbox', { name: /备份.*路径|备份.*目录/ })).toBeNull()
    const rootRequest = fetchMock.mock.calls.find(([input]) => String(input).endsWith('/backup-root-selection'))
    expect(rootRequest?.[1]?.body).toBeUndefined()
  })
})
