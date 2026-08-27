import { fireEvent, render, screen, waitFor } from '@testing-library/vue'
import { createPinia, setActivePinia } from 'pinia'
import { VueQueryPlugin } from '@tanstack/vue-query'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import BackupsView from '@/views/BackupsView.vue'
import { useProjectStore } from '@/stores/project'

const projectID = '018f0000-0000-7000-8000-000000000001'
const backupID = '018f0000-0000-7000-8000-000000000002'
const jobID = '018f0000-0000-7000-8000-000000000003'
const hash = 'a'.repeat(64)
const backup = { backup_id: backupID, project_uuid: projectID, type: 'manual', created_at: '2026-08-26T00:00:00Z', app_version: '1.0.0', schema_version: 22, db_bytes: 1048576, db_sha256: hash, manifest_hash: 'b'.repeat(64), validation_state: 'valid', compatibility_state: 'current', source: {}, retention: 'not automatically pruned', links: { self: `/api/v1/backups/${backupID}` } }
const page = { items: [backup], retention: { daily_count: 10, release_migration_count: 5, manual_automatic_prune: false, restore_pre_automatic_prune: false } }
const job = { id: jobID, kind: 'restore', revision_id: projectID, status: 'succeeded', request_hash: hash, events_url: `/api/v1/jobs/${jobID}/events`, poll_after_ms: 1000, result_type: 'restore', result_id: jobID, result_url: `/api/v1/restores/${jobID}`, created_at: '2026-08-26T00:00:00Z' }

function mount() {
  const pinia = createPinia()
  setActivePinia(pinia)
  const project = useProjectStore()
  project.current = { id: projectID, name: 'Fixture', db_schema_version: 22 }
  project.loaded = true
  return render(BackupsView, { global: { plugins: [pinia, VueQueryPlugin] } })
}

describe('backup and restore UI', () => {
  beforeEach(() => {
    vi.restoreAllMocks()
    sessionStorage.clear()
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1024 })
    window.dispatchEvent(new Event('resize'))
  })

  it('renders path-free stable inventory and submits one manual Job', async () => {
    const fetchMock = vi.fn((_input: RequestInfo | URL, init?: RequestInit) => Promise.resolve(new Response(JSON.stringify(init?.method === 'POST' ? { job: { ...job, kind: 'backup' }, location: `/api/v1/jobs/${jobID}`, result_type: 'backup', result_url: '' } : page), { status: init?.method === 'POST' ? 202 : 200 })))
    vi.stubGlobal('fetch', fetchMock)
    vi.stubGlobal('crypto', { ...crypto, randomUUID: () => 'idempotency-key' })
    mount()
    expect(await screen.findByText(/App 1.0.0 \/ Schema 22/)).toBeTruthy()
    expect(screen.getByText(/SHA-256 aaaaaaaa…aaaaaa/)).toBeTruthy()
    expect(screen.queryByText(/\/Users|[A-Z]:\\/)).toBeNull()
    await fireEvent.click(screen.getByRole('button', { name: '立即备份' }))
    await waitFor(() => expect(fetchMock.mock.calls.some(([, init]) => init?.method === 'POST' && (init.headers as Record<string, string>)['Idempotency-Key'] === 'idempotency-key')).toBe(true))
    const body = String(fetchMock.mock.calls.find(([, init]) => init?.method === 'POST')?.[1]?.body)
    expect(body).not.toMatch(/path|source|destination/i)
  })

  it('requires server preflight and exact destructive confirmation without copy controls', async () => {
    const preflight = { version: 'restore-preflight-v1', generation: 'c'.repeat(64), backup, target_mode: 'active', registry_state: 'matched', free_space_sufficient: true, writable: true, maintenance_available: true, confirmation: { restore_pre_backup_required: true, maintenance_required: true, migration_required: false, graph_pending: true } }
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      if (url.includes('restore-preflights')) return Promise.resolve(new Response(JSON.stringify(preflight), { status: 200 }))
      if (url.endsWith('/restores')) return Promise.resolve(new Response(JSON.stringify({ job, location: `/api/v1/jobs/${jobID}`, result_type: 'restore', result_url: '' }), { status: 202 }))
      if (url.includes('/jobs/')) return Promise.resolve(new Response(JSON.stringify(job), { status: 200 }))
      return Promise.resolve(new Response(JSON.stringify(page), { status: 200 }))
    })
    vi.stubGlobal('fetch', fetchMock)
    vi.stubGlobal('crypto', { ...crypto, randomUUID: () => 'restore-key' })
    mount()
    await fireEvent.click(await screen.findByRole('button', { name: `预检恢复备份 ${backupID}` }))
    const heading = await screen.findByRole('heading', { name: '破坏性恢复确认' })
    await waitFor(() => expect(document.activeElement).toBe(heading))
    expect(screen.getByText(/不会创建副本或新 UUID/)).toBeTruthy()
    expect(screen.getByText(/恢复后 Graph：待重新验证/)).toBeTruthy()
    expect(screen.queryByRole('textbox', { name: /路径|目录|UUID/ })).toBeNull()
    expect(screen.queryByRole('button', { name: /导入|复制|覆盖|忽略|强制/ })).toBeNull()
    expect(screen.queryByLabelText(/文件/)).toBeNull()
    const confirm = screen.getByRole('button', { name: '确认恢复当前项目' }) as HTMLButtonElement
    expect(confirm.disabled).toBe(true)
    await fireEvent.update(screen.getByLabelText(/输入 RESTORE/), 'RESTORE')
    expect(confirm.disabled).toBe(false)
    await fireEvent.click(confirm)
    await waitFor(() => expect(fetchMock.mock.calls.some(([input]) => String(input).endsWith('/restores'))).toBe(true))
    const request = fetchMock.mock.calls.find(([input]) => String(input).endsWith('/restores'))?.[1]
    expect(String(request?.body)).toContain('"confirmation":"RESTORE"')
    expect(String(request?.body)).not.toMatch(/copy|path|new_uuid/i)
  })

  it('keeps keyboard targets named and focuses the path-free error summary at 1024px', async () => {
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      if (init?.method === 'POST') return Promise.resolve(new Response(JSON.stringify({ title: '备份根不可写', detail: '请在设置中重新选择安全目录' }), { status: 503 }))
      return Promise.resolve(new Response(JSON.stringify(page), { status: 200 }))
    })
    vi.stubGlobal('fetch', fetchMock)
    mount()
    const manual = await screen.findByRole('button', { name: '立即备份' })
    const restore = await screen.findByRole('button', { name: `预检恢复备份 ${backupID}` })
    manual.focus()
    expect(document.activeElement).toBe(manual)
    restore.focus()
    expect(document.activeElement).toBe(restore)
    await fireEvent.click(manual)
    const alert = await screen.findByRole('alert')
    await waitFor(() => expect(document.activeElement).toBe(alert))
    expect(alert.textContent).toMatch(/备份根不可写/)
    expect(document.body.textContent).not.toMatch(/[A-Z]:\\|\/Users\//)
  })

  it('resumes the original restore Job after reload and renders rollback/recovery/Graph text without inferring success', async () => {
    sessionStorage.setItem(`restore-job:${projectID}`, jobID)
    const interrupted = { ...job, status: 'interrupted', phase: 'rollback', progress: 82, warning: 'Graph pending; retry after recovery', recovery_required: true, result_type: null, result_id: null, result_url: null }
    const fetchMock = vi.fn((input: RequestInfo | URL) => Promise.resolve(new Response(JSON.stringify(String(input).includes('/jobs/') ? interrupted : page), { status: 200 })))
    vi.stubGlobal('fetch', fetchMock)
    vi.stubGlobal('EventSource', undefined)
    mount()
    const restoreHeading = await screen.findByRole('heading', { name: '恢复任务' })
    const restoreSection = restoreHeading.closest('section') as HTMLElement
    await waitFor(() => expect(restoreSection.textContent).toMatch(/状态：interrupted · 阶段：rollback · 82%/))
    expect(screen.getByRole('alert').textContent).toMatch(/需要安全恢复/)
    expect(screen.getByText(/Graph pending; retry after recovery/)).toBeTruthy()
    expect(screen.getByText(/只以重启后的 journal 恢复结果为准/)).toBeTruthy()
    expect(screen.queryByText('任务成功。')).toBeNull()
    expect(fetchMock.mock.calls.filter(([input]) => String(input).includes(`/jobs/${jobID}`))).toHaveLength(1)
  })
})
