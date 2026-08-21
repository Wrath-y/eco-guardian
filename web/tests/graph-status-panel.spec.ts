import { fireEvent, render, screen, waitFor } from '@testing-library/vue'
import { VueQueryPlugin } from '@tanstack/vue-query'
import { describe, expect, it, vi } from 'vitest'
import GraphStatusPanel from '../src/components/GraphStatusPanel.vue'
import type { GraphJob, GraphStatus } from '../src/api/graph'

const revisionID = '01948c1e-0000-7000-8000-000000000000'
const jobID = '01948c1e-0000-7000-8000-000000000001'
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
function job(status: GraphJob['status'] = 'failed'): GraphJob {
  return { id: jobID, kind: 'graph_sync', revision_id: revisionID, status, request_hash: 'a'.repeat(64), events_url: `/api/v1/jobs/${jobID}/events`, poll_after_ms: 100, created_at: '2026-01-01T00:00:00Z' }
}
function status(state: GraphStatus['pipeline_state'] = 'graph_failed', current = job()): GraphStatus {
  return { revision_id: revisionID, config_hash: 'b'.repeat(64), pipeline_state: state, freshness: 'stale', freshness_reasons: ['GRAPH_NOT_READY'], validation_result: 'PASS', projection: null, job: current, warnings: [], actions: state === 'graph_failed' ? ['retry', 'inspect'] : ['wait'], evidence: [] }
}
function renderPanel() { return render(GraphStatusPanel, { props: { projectID: revisionID, revisionID }, global: { plugins: [VueQueryPlugin] } }) }

describe('graph status panel', () => {
  it('reuses its persisted retry idempotency key after a network replay and focuses feedback', async () => {
    sessionStorage.clear()
    let attempts = 0
    const fetchMock = vi.fn((url: string, options?: RequestInit) => {
      if (options?.method === 'POST') {
        attempts += 1
        return Promise.resolve(attempts === 1 ? new Response(JSON.stringify({ title: '网络暂不可用' }), { status: 503 }) : new Response(JSON.stringify({ job: job('queued'), location: `/api/v1/jobs/${jobID}` }), { status: 202 }))
      }
      return Promise.resolve(new Response(JSON.stringify(status()), { status: 200 }))
    })
    vi.stubGlobal('fetch', fetchMock)
    renderPanel()
    const retry = await screen.findByRole('button', { name: '重试图谱同步' })
    await fireEvent.click(retry)
    expect((await screen.findByRole('alert')).textContent).toContain('重试未完成')
    await fireEvent.click(screen.getByRole('button', { name: '重试图谱同步' }))
    expect((await screen.findByRole('status')).textContent).toContain('已提交重试请求')
    const posts = (fetchMock.mock.calls as unknown as Array<[string, RequestInit]>).filter(([, options]) => options?.method === 'POST')
    expect(posts).toHaveLength(2)
    expect((posts[0][1].headers as Record<string, string>)['Idempotency-Key']).toBe((posts[1][1].headers as Record<string, string>)['Idempotency-Key'])
    await waitFor(() => expect(document.activeElement).toBe(screen.getByRole('status')))
  })

  it('uses persisted SSE ordinals, then polls the common Job resource after disconnect', async () => {
    FakeEventSource.instances = []; sessionStorage.clear(); vi.stubGlobal('EventSource', FakeEventSource)
    const fetchMock = vi.fn((url: string) => Promise.resolve(new Response(JSON.stringify(url.includes('/jobs/') ? job('running') : status('graph_building', job('running'))), { status: 200 })))
    vi.stubGlobal('fetch', fetchMock)
    renderPanel()
    await screen.findByText('图谱构建中')
    FakeEventSource.instances[0].emit('job', JSON.stringify({ job_id: jobID, ordinal: 3, phase: 'PROJECTED', progress: 40, created_at: '2026-01-01T00:00:01Z' }))
    expect(sessionStorage.getItem(`graph-job-ordinal:${revisionID}:${jobID}`)).toBe('3')
    vi.useFakeTimers()
    FakeEventSource.instances[0].onerror?.(new Event('error'))
    expect(await screen.findByText(/SSE 已断开，正在轮询持久 Job/)).toBeTruthy()
    await vi.advanceTimersByTimeAsync(100)
    expect(fetchMock.mock.calls.some(([url]) => String(url).includes(`/api/v1/jobs/${jobID}`))).toBe(true)
    vi.useRealTimers()
  })
})
