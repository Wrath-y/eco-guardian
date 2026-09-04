import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { describe, expect, it, vi } from 'vitest'
import App from '@/react/App'

const project = { id: '01948c1e-0000-7000-8000-000000000000', name: 'balance', db_schema_version: 21 }
const runtime = {
  schema_version: 1, generation: 1, build: { version: '1.0.0', build: 'test', commit: 'abc', package_mode: 'development' },
  listener: { url: 'http://127.0.0.1:43123' }, phase: 'ready', project: { state: 'active', project_id: project.id, recent_count: 1, recovery_required: false },
  process: { ownership: 'external', state: 'ready', launch_generation: 1, endpoint: null, restart_attempt: 0, reason: null },
  dependencies: [], capabilities: [], recovery: [], log_location: '<project-directory>/logs', updated_at: new Date().toISOString(),
}
const capabilities = {
  release: { enabled: true, disabled_reasons: [] },
  graph: { available: true, compatible: true, required_capabilities: [], degradations: [], disabled_reasons: [], release_disabled_reasons: [] },
  ai: { state: 'available', enabled: true, endpoint_classification: 'loopback', credential_present: true, structured_output: true, tool_calls: true, streaming: true, reasons: [], prompt: { id: 'p', version: 'v1', hash: 'a'.repeat(64) }, draft_patch_schema: { id: 's', version: 'v1', hash: 'b'.repeat(64) }, tools: [], orchestrator: { id: 'o', version: 'v1', hash: 'c'.repeat(64) }, budget: { id: 'b', version: 'v1', hash: 'd'.repeat(64) }, limits: { policy: { id: 'p', version: 'v1', hash: 'e'.repeat(64) }, max_format_repairs: 3, max_provider_turns: 3, max_tool_calls: 5, max_search_candidates: 20, max_duration_millis: 1000, max_context_bytes: 1000, max_output_bytes: 1000, max_tool_result_bytes: 1000, retrieval_seed_limit: 3, retrieval_result_limit: 3, retrieval_graph_depth: 2 } },
  backup: { available: true, root_health: 'healthy', restore_state: 'idle', recovery_required: false, disabled_reasons: [], safe_actions: [] },
}

function renderApp(path = '/projects') {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 }, mutations: { retry: false } } })
  return render(<QueryClientProvider client={client}><MemoryRouter initialEntries={[path]}><App /></MemoryRouter></QueryClientProvider>)
}

function mockBase(active = true) {
  return vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    const path = String(input)
    if (path.endsWith('/projects/current')) return Promise.resolve(active ? new Response(JSON.stringify(project), { status: 200 }) : new Response(JSON.stringify({ title: 'not open' }), { status: 404 }))
    if (path.endsWith('/runtime/status')) return Promise.resolve(new Response(JSON.stringify(runtime), { status: 200 }))
    if (path.endsWith('/runtime/capabilities')) return Promise.resolve(new Response(JSON.stringify(capabilities), { status: 200 }))
    if (path.endsWith('/projects/recent')) return Promise.resolve(new Response('[]', { status: 200 }))
    if (path.endsWith('/project-selections')) return Promise.resolve(new Response(JSON.stringify({ token: 'opaque' }), { status: 201 }))
    if (path.endsWith('/projects') && init?.method === 'POST') return Promise.resolve(new Response(JSON.stringify(project), { status: 201 }))
    if (path.includes('/entities/tag')) return Promise.resolve(new Response(JSON.stringify({ items: [{ id: project.id, kind: 'tag', name: 'Flame', key: 'flame', entity_version: 1 }], next_cursor: null }), { status: 200 }))
    return Promise.resolve(new Response(JSON.stringify({ title: 'not mocked' }), { status: 404 }))
  })
}

function mockEntityCatalogs(tags: Array<{ id: string; name: string; key: string; entity_version: number }> = []) {
  const fallback = mockBase()
  return vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    const path = String(input)
    const catalog = path.match(/\/entities\/(attribute|tag|skill|item|effect)\?limit=200$/)
    if (catalog) {
      return Promise.resolve(new Response(JSON.stringify({ items: catalog[1] === 'tag' ? tags : [], next_cursor: null }), { status: 200 }))
    }
    if (path.includes('/schemas/entities/')) {
      return Promise.resolve(new Response(JSON.stringify({ schema_id: 'test', schema: {} }), { status: 200 }))
    }
    return fallback(input, init)
  })
}

describe('React application shell', () => {
  it('renders the Ant Design workspace and project state', async () => {
    vi.stubGlobal('fetch', mockBase())
    renderApp()
    expect(await screen.findByRole('heading', { name: '项目空间' })).toBeTruthy()
    expect(screen.getByText('React · Ant Design')).toBeTruthy()
    expect(await screen.findByRole('heading', { name: 'balance' })).toBeTruthy()
  })

  it('creates a project through the native selection capability', async () => {
    const user = userEvent.setup()
    const fetchMock = mockBase(false)
    vi.stubGlobal('fetch', fetchMock)
    renderApp()
    await user.click(await screen.findByRole('button', { name: /创建项目/ }))
    expect(await screen.findByRole('heading', { name: 'balance' })).toBeTruthy()
    await waitFor(() => expect(fetchMock.mock.calls.some(([input]) => String(input).endsWith('/project-selections'))).toBe(true))
    await waitFor(() => expect((screen.getByRole('button', { name: /创建项目/ }) as HTMLButtonElement).disabled).toBe(false))
  })

  it('closes the active project without waiting for a deferred editor timer', async () => {
    const fallback = mockBase()
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      if (String(input).endsWith('/projects/close') && init?.method === 'POST') {
        return Promise.resolve(new Response(null, { status: 204 }))
      }
      return fallback(input, init)
    })
    vi.stubGlobal('fetch', fetchMock)
    renderApp()

    const closeButton = await screen.findByRole('button', { name: /关闭项目/ })
    const deferredTimer = vi.spyOn(window, 'setTimeout').mockImplementation(() => undefined as unknown as ReturnType<typeof window.setTimeout>)
    fireEvent.click(closeButton)
    await Promise.resolve()
    await Promise.resolve()
    deferredTimer.mockRestore()

    expect(fetchMock.mock.calls.some(([input, init]) => String(input).endsWith('/projects/close') && init?.method === 'POST')).toBe(true)
    expect(await screen.findByText('尚未打开项目')).toBeTruthy()
  })

  it('routes an active project to the Ant Design entity table', async () => {
    vi.stubGlobal('fetch', mockBase())
    renderApp('/config/tag')
    expect(await screen.findByRole('heading', { name: '标签配置' })).toBeTruthy()
    expect(await screen.findByRole('button', { name: 'Flame' })).toBeTruthy()
  })

  it('allows the first tag to be created without a circular tag prerequisite', async () => {
    const user = userEvent.setup()
    vi.stubGlobal('fetch', mockEntityCatalogs())
    renderApp('/config/tag/new')

    expect(await screen.findByRole('heading', { name: '新建标签' })).toBeTruthy()
    const relatedTags = screen.getByRole('combobox', { name: '关联标签（可选）' })
    const parentTags = screen.getByRole('combobox', { name: '父标签' })

    await user.click(relatedTags)
    expect(await screen.findByText('暂无已有标签，可留空创建当前标签')).toBeTruthy()
    await user.keyboard('{Escape}')
    await user.click(parentTags)
    expect(await screen.findByText('暂无父标签，可留空创建根标签')).toBeTruthy()
    expect(screen.queryByText(/请先在标签配置中创建/)).toBeNull()
  })

  it('keeps the tag configuration guidance on non-tag entity forms', async () => {
    const user = userEvent.setup()
    vi.stubGlobal('fetch', mockEntityCatalogs())
    renderApp('/config/attribute/new')

    expect(await screen.findByRole('heading', { name: '新建属性' })).toBeTruthy()
    await user.click(screen.getByRole('combobox', { name: '标签' }))
    expect(await screen.findByText('暂无标签，请先在标签配置中创建')).toBeTruthy()
  })

  it('submits a selected parent tag through the shared reference control', async () => {
    const user = userEvent.setup()
    const catalogMock = mockEntityCatalogs([{ id: project.id, name: 'Flame', key: 'flame', entity_version: 1 }])
    let submitted: { payload?: { parent_tag_ids?: string[] } } | undefined
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input)
      if (path === '/api/v1/entities/tag' && init?.method === 'POST') {
        submitted = JSON.parse(String(init.body)) as typeof submitted
        return Promise.resolve(new Response(JSON.stringify({
          entity: { id: project.id, kind: 'tag', key: 'child_tag', name: 'Child tag' },
          revision: { validation: { error: 0, block: 0, warning: 0, info: 0 } },
        }), { status: 201 }))
      }
      return catalogMock(input, init)
    }))
    renderApp('/config/tag/new')

    await user.type(await screen.findByRole('textbox', { name: 'Key' }), 'child_tag')
    await user.type(screen.getByRole('textbox', { name: '名称' }), 'Child tag')
    await user.type(screen.getByRole('textbox', { name: '分类' }), 'element')
    await user.click(screen.getByRole('combobox', { name: '父标签' }))
    await user.click(await screen.findByText('Flame（flame）'))
    await user.click(screen.getByRole('button', { name: /保存/ }))

    await waitFor(() => expect(submitted?.payload?.parent_tag_ids).toEqual([project.id]))
  })

  it('offers a confirmed close-other-instance flow and retries the locked project', async () => {
    const user = userEvent.setup()
    let released = false
    const fallback = mockBase(false)
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input)
      if (path.endsWith('/projects/recent')) return Promise.resolve(new Response(JSON.stringify([project]), { status: 200 }))
      if (path.endsWith(`/projects/recent/${project.id}/close-other-instance`) && init?.method === 'POST') {
        released = true
        return Promise.resolve(new Response(null, { status: 204 }))
      }
      if (path.endsWith(`/projects/recent/${project.id}`) && init?.method === 'POST') {
        return Promise.resolve(released
          ? new Response(JSON.stringify(project), { status: 200 })
          : new Response(JSON.stringify({ title: 'Project is locked', code: 'PROJECT_LOCKED', retryable: false }), { status: 423 }))
      }
      return fallback(input, init)
    })
    vi.stubGlobal('fetch', fetchMock)
    renderApp()
    await user.click(await screen.findByRole('button', { name: '打开 balance' }))
    await user.click(await screen.findByRole('button', { name: '关闭其他实例' }))
    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByText(/尚未保存的表单输入可能丢失/)).toBeTruthy()
    await user.click(within(dialog).getByRole('button', { name: '关闭其他实例' }))
    expect(await screen.findByRole('heading', { name: 'balance' })).toBeTruthy()
    expect(fetchMock.mock.calls.some(([input]) => String(input).endsWith('/close-other-instance'))).toBe(true)
  })

  it.each([
    ['/config/tag/new', '新建标签'],
    ['/versions', '版本历史'],
    [`/versions/${project.id}/diff`, '版本差异与发布'],
    ['/simulations', '模拟实验'],
    ['/impact', '依赖影响分析'],
    ['/risk-reviews', '风险复核'],
    ['/ai-design', 'AI 平衡设计'],
    ['/backups', '备份与恢复'],
    ['/settings', '运行设置'],
  ])('loads the migrated route %s', async (path, heading) => {
    vi.stubGlobal('fetch', mockBase())
    renderApp(path)
    expect(await screen.findByRole('heading', { name: heading })).toBeTruthy()
  })
})
