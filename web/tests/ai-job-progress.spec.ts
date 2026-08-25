import { fireEvent, render, screen, waitFor } from '@testing-library/vue'
import { VueQueryPlugin } from '@tanstack/vue-query'
import { describe, expect, it, vi } from 'vitest'
import AIJobProgress from '../src/features/ai-design/AIJobProgress.vue'

const projectID = '01948c1e-0000-7000-8000-000000000300'
const jobID = '01948c1e-0000-7000-8000-000000000301'
const baseID = '01948c1e-0000-7000-8000-000000000302'
const hash = 'a'.repeat(64)
function job(status: string) { return { id: jobID, kind: 'ai_design', revision_id: baseID, status, request_hash: hash, events_url: `/api/v1/jobs/${jobID}/events`, poll_after_ms: 1000, created_at: '2026-08-25T00:00:00Z', updated_at: '2026-08-25T00:00:00Z', phase: 'provider/tool_loop' } }

class FakeEventSource {
  static instances: FakeEventSource[] = []
  onopen: (() => void) | null = null
  onerror: (() => void) | null = null
  listeners = new Map<string, (event: MessageEvent<string>) => void>()
  constructor(readonly url: string) { FakeEventSource.instances.push(this) }
  addEventListener(name: string, listener: EventListenerOrEventListenerObject) { this.listeners.set(name, listener as (event: MessageEvent<string>) => void) }
  close() {}
  emit(name: string, value: unknown) { this.listeners.get(name)?.(new MessageEvent(name, { data: JSON.stringify(value) })) }
}

describe('AI Job progress', () => {
  it('replays a persisted terminal error and enables only explicit retry', async () => {
    FakeEventSource.instances = []
    vi.stubGlobal('EventSource', FakeEventSource)
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response(JSON.stringify(job('failed')), { status: 200 }))))
    const view = render(AIJobProgress, { props: { projectID, jobID }, global: { plugins: [VueQueryPlugin] } })
		await screen.findByText('failed')
    await waitFor(() => expect(FakeEventSource.instances.length).toBe(1))
    FakeEventSource.instances[0].emit('job', { job_id: jobID, ordinal: 1, kind: 'terminal', phase: 'provider/tool_loop', progress: 50, outcome: 'failed', created_at: '2026-08-25T00:00:00Z', error: { code: 'AI_PROVIDER_TIMEOUT', retryable: true, request_id: 'provider-request_1' } })
    expect(await screen.findByText(/AI_PROVIDER_TIMEOUT：这是可重试失败/)).toBeTruthy()
    expect(screen.getByText(/请求 ID：provider-request_1/)).toBeTruthy()
    await fireEvent.click(screen.getByRole('button', { name: '确认并显式创建新尝试' }))
    expect(view.emitted().retry).toHaveLength(1)
    expect(document.body.textContent).not.toContain('partial response')
  })

  it('uses the shared idempotent cancel command for a queued Job', async () => {
    vi.stubGlobal('EventSource', undefined)
    const fetchMock = vi.fn((_url: string, init?: RequestInit) => Promise.resolve(new Response(JSON.stringify(job(init?.method === 'POST' ? 'canceled' : 'queued')), { status: 200 })))
    vi.stubGlobal('fetch', fetchMock)
    render(AIJobProgress, { props: { projectID, jobID }, global: { plugins: [VueQueryPlugin] } })
    await fireEvent.click(await screen.findByRole('button', { name: '取消 AI Job' }))
    expect(await screen.findByText(/取消已持久化/)).toBeTruthy()
    expect(fetchMock.mock.calls.some(([, init]) => init?.method === 'POST')).toBe(true)
  })
})
