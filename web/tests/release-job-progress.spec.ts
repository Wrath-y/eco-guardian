import { render, screen } from '@testing-library/vue'
import { VueQueryPlugin } from '@tanstack/vue-query'
import { describe, expect, it, vi } from 'vitest'
import ReleaseJobProgress from '../src/components/ReleaseJobProgress.vue'
import type { ReleaseJob } from '../src/api/versions'

const id = '01948c1e-0000-7000-8000-000000000000'
const releaseID = '01948c1e-0000-7000-8000-000000000001'
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
function job(status: ReleaseJob['status'] = 'queued'): ReleaseJob {
  return { id, kind: 'release', revision_id: id, status, request_hash: 'a'.repeat(64), events_url: `/api/v1/jobs/${id}/events`, poll_after_ms: 100, created_at: '2026-01-01T00:00:00Z', ...(status === 'succeeded' ? { result_type: 'release', result_id: releaseID, result_url: `/api/v1/releases/${releaseID}` } : {}) }
}
function renderProgress() { return render(ReleaseJobProgress, { props: { projectID: id, jobID: id }, global: { plugins: [VueQueryPlugin] } }) }

describe('release Job progress', () => {
  it('renders persisted warning and error events, then confirms the active pointer only after terminal success', async () => {
    FakeEventSource.instances = []; vi.stubGlobal('EventSource', FakeEventSource)
    vi.stubGlobal('fetch', vi.fn((url: string) => Promise.resolve(new Response(JSON.stringify(url.includes('/releases/') ? { id: releaseID, active_pointer: { release_id: releaseID, generation: 2 } } : job()), { status: 200 }))))
    renderProgress()
    await screen.findByText('queued')
    const stream = FakeEventSource.instances[0]
    stream.emit('job', JSON.stringify({ job_id: id, ordinal: 1, phase: 'BACKUP_SUCCEEDED', progress: 40, warning: '备份空间较低', error: '旧错误', created_at: '2026-01-01T00:00:01Z' }))
    expect(await screen.findByText(/WARNING：备份空间较低/)).toBeTruthy()
    expect(screen.getByText(/ERROR：旧错误/)).toBeTruthy()
    stream.emit('terminal', JSON.stringify(job('succeeded')))
    expect(await screen.findByText('正式版本指针已确认提交。')).toBeTruthy()
    expect(screen.getByRole('link', { name: `打开发布任务结果 ${releaseID}` }).getAttribute('href')).toBe(`/api/v1/releases/${releaseID}`)
  })

  it('falls back to polling after an SSE failure without re-submitting the release', async () => {
    FakeEventSource.instances = []; vi.stubGlobal('EventSource', FakeEventSource)
    const fetchMock = vi.fn(() => Promise.resolve(new Response(JSON.stringify(job('running')), { status: 200 }))); vi.stubGlobal('fetch', fetchMock)
    renderProgress(); await screen.findByText('running')
    vi.useFakeTimers()
    FakeEventSource.instances[0].onerror?.(new Event('error'))
    expect(await screen.findByText(/SSE 已断开，正在按服务端建议轮询/)).toBeTruthy()
    await vi.advanceTimersByTimeAsync(100)
    const calls = fetchMock.mock.calls as unknown as Array<[string, RequestInit | undefined]>
    expect(calls.filter(([url]) => String(url).includes(`/jobs/${id}`))).toHaveLength(2)
    expect(calls.some(([, options]) => options?.method === 'POST')).toBe(false)
    vi.useRealTimers()
  })

  it('distinguishes canceled, interrupted, and failed retry states', async () => {
    vi.stubGlobal('EventSource', FakeEventSource)
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response(JSON.stringify(job('interrupted')), { status: 200 }))))
    const interrupted = renderProgress(); expect(await screen.findByText(/任务已中断：外部步骤可能已被接受/)).toBeTruthy(); interrupted.unmount()
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response(JSON.stringify(job('canceled')), { status: 200 }))))
    const canceled = renderProgress(); expect(await screen.findByText(/任务已取消：外部发布步骤尚未被接受/)).toBeTruthy(); canceled.unmount()
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response(JSON.stringify(job('failed')), { status: 200 }))))
    renderProgress(); expect(await screen.findByRole('button', { name: '重试状态查询' })).toBeTruthy()
  })
})
