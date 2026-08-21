import { render, screen } from '@testing-library/vue'
import { VueQueryPlugin } from '@tanstack/vue-query'
import { describe, expect, it, vi } from 'vitest'
import SimulationJobProgress from '../src/components/SimulationJobProgress.vue'
import type { SimulationJob } from '../src/api/simulation'

const id = '01948c1e-0000-7000-8000-000000000000'
class FakeEventSource {
  static instances: FakeEventSource[] = []
  onopen: ((event: Event) => void) | null = null
  onerror: ((event: Event) => void) | null = null
  private listeners = new Map<string, Array<(event: MessageEvent<string>) => void>>()
  constructor(readonly url: string) { FakeEventSource.instances.push(this) }
  addEventListener(type: string, listener: (event: MessageEvent<string>) => void) { this.listeners.set(type, [...(this.listeners.get(type) ?? []), listener]) }
  close() { /* test transport */ }
  emit(type: string, data = '') { for (const listener of this.listeners.get(type) ?? []) listener(new MessageEvent('message', { data })) }
}
function job(status: SimulationJob['status'] = 'running'): SimulationJob {
  return { id, kind: 'simulation', revision_id: id, status, request_hash: 'a'.repeat(64), events_url: `/api/v1/jobs/${id}/events`, poll_after_ms: 100, created_at: '2026-01-01T00:00:00Z' }
}

describe('simulation Job progress', () => {
  it('deduplicates persisted SSE events and falls back to polling without a new submission', async () => {
    FakeEventSource.instances = []; sessionStorage.clear(); vi.stubGlobal('EventSource', FakeEventSource)
    const fetchMock = vi.fn(() => Promise.resolve(new Response(JSON.stringify(job()), { status: 200 }))); vi.stubGlobal('fetch', fetchMock)
    render(SimulationJobProgress, { props: { projectID: id, jobID: id }, global: { plugins: [VueQueryPlugin] } })
    await screen.findByText('running')
    const stream = FakeEventSource.instances[0]
    stream.emit('job', JSON.stringify({ job_id: id, ordinal: 3, phase: 'SAMPLES_RUNNING', progress: 40, created_at: '2026-01-01T00:00:01Z' }))
    expect((await screen.findAllByText(/SAMPLES_RUNNING/)).length).toBeGreaterThan(0)
    expect(sessionStorage.getItem(`simulation-job-ordinal:${id}:${id}`)).toBe('3')
    vi.useFakeTimers()
    stream.onerror?.(new Event('error'))
    expect(await screen.findByText(/SSE 已断开，正在按服务端建议轮询/)).toBeTruthy()
    expect(screen.getByText(/任务 .* 状态：/).getAttribute('aria-live')).toBe('polite')
    await vi.advanceTimersByTimeAsync(100)
    const calls = fetchMock.mock.calls as unknown as Array<[string, RequestInit | undefined]>
    expect(calls.some(([, options]) => options?.method === 'POST')).toBe(false)
    vi.useRealTimers()
  })

  it('explains a persisted budget failure without treating progress as success', async () => {
    FakeEventSource.instances = []; vi.stubGlobal('EventSource', FakeEventSource)
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response(JSON.stringify(job()), { status: 200 }))))
    render(SimulationJobProgress, { props: { projectID: id, jobID: id }, global: { plugins: [VueQueryPlugin] } })
    await screen.findByText('running')
    FakeEventSource.instances[0].emit('job', JSON.stringify({ job_id: id, ordinal: 4, phase: 'FAILED', progress: 100, error: 'BUDGET_EXCEEDED: event budget exceeded', created_at: '2026-01-01T00:00:02Z' }))
    FakeEventSource.instances[0].emit('terminal', JSON.stringify(job('failed')))
    expect(await screen.findByText(/已超过固定预算；没有生成部分成功结果/)).toBeTruthy()
  })

  it.each([
    ['canceled' as const, '任务已取消；不将部分样本显示为成功结果。'],
    ['interrupted' as const, '任务已中断；恢复时会核对已捕获的输入与实现指纹。'],
  ])('renders %s as a distinct terminal state and moves focus to the terminal heading', async (status, message) => {
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response(JSON.stringify(job(status)), { status: 200 }))))
    render(SimulationJobProgress, { props: { projectID: id, jobID: id }, global: { plugins: [VueQueryPlugin] } })
    expect(await screen.findByText(message)).toBeTruthy()
    expect(document.activeElement?.id).toBe('simulation-job-heading')
  })

  it('explains timeout and recovery failures without reporting a result', async () => {
    FakeEventSource.instances = []; vi.stubGlobal('EventSource', FakeEventSource)
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response(JSON.stringify(job()), { status: 200 }))))
    render(SimulationJobProgress, { props: { projectID: id, jobID: id }, global: { plugins: [VueQueryPlugin] } })
    await screen.findByText('running')
    FakeEventSource.instances[0].emit('job', JSON.stringify({ job_id: id, ordinal: 5, phase: 'FAILED', progress: 100, error: 'TIMEOUT: runtime deadline exceeded', created_at: '2026-01-01T00:00:03Z' }))
    FakeEventSource.instances[0].emit('terminal', JSON.stringify(job('failed')))
    expect(await screen.findByText(/本地执行已超时；没有生成部分成功结果/)).toBeTruthy()
  })

  it('explains recovery refusal without reporting a result', async () => {
    FakeEventSource.instances = []; vi.stubGlobal('EventSource', FakeEventSource)
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response(JSON.stringify(job()), { status: 200 }))))
    render(SimulationJobProgress, { props: { projectID: id, jobID: id }, global: { plugins: [VueQueryPlugin] } })
    await screen.findByText('running')
    FakeEventSource.instances[0].emit('job', JSON.stringify({ job_id: id, ordinal: 6, phase: 'FAILED', progress: 100, error: 'RECOVERY_UNAVAILABLE: captured implementation is unavailable', created_at: '2026-01-01T00:00:04Z' }))
    FakeEventSource.instances[0].emit('terminal', JSON.stringify(job('failed')))
    expect(await screen.findByText(/恢复所需的历史输入或实现不可用；此 Job 不能安全重试为成功/)).toBeTruthy()
  })
})
