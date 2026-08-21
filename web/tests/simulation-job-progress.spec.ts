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
    expect(await screen.findByText(/SAMPLES_RUNNING/)).toBeTruthy()
    expect(sessionStorage.getItem(`simulation-job-ordinal:${id}:${id}`)).toBe('3')
    vi.useFakeTimers()
    stream.onerror?.(new Event('error'))
    expect(await screen.findByText(/SSE 已断开，正在按服务端建议轮询/)).toBeTruthy()
    await vi.advanceTimersByTimeAsync(100)
    const calls = fetchMock.mock.calls as unknown as Array<[string, RequestInit | undefined]>
    expect(calls.some(([, options]) => options?.method === 'POST')).toBe(false)
    vi.useRealTimers()
  })
})
