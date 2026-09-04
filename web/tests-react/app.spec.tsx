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

function mockEntityCatalogs(tags: Array<{ id: string; name: string; key: string; entity_version: number; balance_group?: string }> = []) {
  const fallback = mockBase()
  return vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    const path = String(input)
    const catalog = path.match(/\/entities\/(attribute|tag|character|skill|item|effect)\?limit=200$/)
    if (catalog) {
      return Promise.resolve(new Response(JSON.stringify({ items: catalog[1] === 'tag' ? tags : [], next_cursor: null }), { status: 200 }))
    }
    if (path === '/api/v1/formula-validations' && init?.method === 'POST') {
      const request = JSON.parse(String(init.body)) as { expression: string }
      const mismatch = request.expression === '80[health_point] + 10[damage_point]'
      return Promise.resolve(new Response(JSON.stringify({
        valid: !mismatch,
        diagnostics: mismatch ? [{ code: 'FORMULA_UNIT_MISMATCH', message: 'arithmetic requires same dimension', start_byte: 0, end_byte: request.expression.length }] : [],
      }), { status: 200 }))
    }
    if (path.includes('/schemas/entities/')) {
      return Promise.resolve(new Response(JSON.stringify({
        kind: path.split('/').pop(),
        schema_id: 'urn:eco:schema:test:1',
        schema: {},
        dsl_registry: {
          dsl_version: 'v1', manifest_hash: 'a'.repeat(64), selector_template: '${scope:symbol}', scopes: ['self', 'source', 'target', 'scenario'], functions: [],
          units: [
            { name: 'scalar', value_type: 'decimal', dimension: 'scalar', base: 'scalar' },
            { name: 'damage_point', value_type: 'decimal', dimension: 'damage', base: 'damage_point' },
            { name: 'health_point', value_type: 'decimal', dimension: 'health', base: 'health_point' },
            { name: 'resource_point', value_type: 'decimal', dimension: 'resource', base: 'resource_point' },
            { name: 'millisecond', value_type: 'integer', dimension: 'duration', base: 'millisecond' },
          ],
        },
      }), { status: 200 }))
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

  it('reprobes and refreshes the displayed runtime phase', async () => {
    const user = userEvent.setup()
    let currentRuntime = { ...runtime, phase: 'degraded', generation: 1 }
    const fallback = mockBase()
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input)
      if (path.endsWith('/runtime/reprobe') && init?.method === 'POST') {
        currentRuntime = { ...runtime, phase: 'ready', generation: 2 }
        return Promise.resolve(new Response(null, { status: 204 }))
      }
      if (path.endsWith('/runtime/status')) {
        return Promise.resolve(new Response(JSON.stringify(currentRuntime), { status: 200 }))
      }
      return fallback(input, init)
    })
    vi.stubGlobal('fetch', fetchMock)
    renderApp()

    await user.click(await screen.findByRole('button', { name: '降级' }))
    expect(screen.getAllByText('降级').length).toBeGreaterThanOrEqual(2)
    await user.click(screen.getByRole('button', { name: /刷新/ }))

    await waitFor(() => expect(screen.getAllByText('就绪').length).toBeGreaterThanOrEqual(2))
    const reprobe = fetchMock.mock.calls.find(([input, init]) => String(input).endsWith('/runtime/reprobe') && init?.method === 'POST')
    expect(reprobe).toBeTruthy()
    expect(new Headers(reprobe?.[1]?.headers).get('Idempotency-Key')).toBeTruthy()
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

  it('initializes a referenced base-data set once and safely skips it on retry', async () => {
    const user = userEvent.setup()
    type Kind = 'attribute' | 'tag' | 'character' | 'skill' | 'item' | 'effect'
    type StoredEntity = Record<string, unknown> & { id: string; kind: Kind; key: string }
    const stored: Record<Kind, StoredEntity[]> = { attribute: [], tag: [], character: [], skill: [], item: [], effect: [] }
    const postedKinds: Kind[] = []
    const fallback = mockBase()
    let nextID = 1
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = new URL(String(input), 'http://localhost')
      const match = url.pathname.match(/\/api\/v1\/entities\/(attribute|tag|character|skill|item|effect)$/)
      if (!match) return fallback(input, init)
      const kind = match[1] as Kind
      if ((init?.method ?? 'GET') === 'GET') {
        return Promise.resolve(new Response(JSON.stringify({ items: stored[kind], next_cursor: null }), { status: 200 }))
      }
      const draft = JSON.parse(String(init?.body)) as Record<string, unknown> & { key: string }
      const entity: StoredEntity = {
        ...draft,
        id: `01948c1e-0000-7000-8000-${String(nextID++).padStart(12, '0')}`,
        kind,
        key: draft.key,
      }
      stored[kind].push(entity)
      postedKinds.push(kind)
      return Promise.resolve(new Response(JSON.stringify({ entity, revision: {} }), { status: 201 }))
    })
    vi.stubGlobal('fetch', fetchMock)
    renderApp()

    const closeButton = await screen.findByRole('button', { name: /关闭项目/ })
    const initializeButton = screen.getByRole('button', { name: /初始化基础数据/ })
    expect(closeButton.compareDocumentPosition(initializeButton) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    await user.click(initializeButton)
    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByText(/各 3 项，共 18 项/)).toBeTruthy()
    await user.click(within(dialog).getByRole('button', { name: '开始初始化' }))

    expect(await screen.findByText('基础数据初始化完成：新增 18 项')).toBeTruthy()
    expect(postedKinds).toEqual([
      'tag', 'tag', 'tag',
      'attribute', 'attribute', 'attribute',
      'effect', 'effect', 'effect',
      'skill', 'skill', 'skill',
      'item', 'item', 'item',
      'character', 'character', 'character',
    ])
    for (const entities of Object.values(stored)) expect(entities).toHaveLength(3)

    const entitiesByKey = new Map(Object.values(stored).flat().map(entity => [entity.key, entity]))
    const guardian = entitiesByKey.get('trainee_guardian')!
    const guardianPayload = guardian.payload as { skill_ids: string[]; item_ids: string[] }
    expect(guardian.tag_ids).toEqual([entitiesByKey.get('starter_content')!.id, entitiesByKey.get('player')!.id])
    expect(guardianPayload.skill_ids).toEqual([entitiesByKey.get('basic_attack')!.id, entitiesByKey.get('guard_stance')!.id])
    expect(guardianPayload.item_ids).toEqual([entitiesByKey.get('training_sword')!.id, entitiesByKey.get('cloth_armor')!.id])

    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    await user.click(initializeButton)
    await user.click(within(await screen.findByRole('dialog')).getByRole('button', { name: '开始初始化' }))
    expect(await screen.findByText('基础数据已全部存在，已跳过 18 项')).toBeTruthy()
    expect(postedKinds).toHaveLength(18)
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

  it('offers and removes balance-group suggestions and explains every visible field', async () => {
    const user = userEvent.setup()
    const fallback = mockEntityCatalogs()
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      if (String(input) === '/api/v1/entities/attribute?limit=200') {
        return Promise.resolve(new Response(JSON.stringify({
          items: [{ id: project.id, name: 'Boss health', key: 'boss_health', entity_version: 1, balance_group: 'boss_stats' }],
          next_cursor: null,
        }), { status: 200 }))
      }
      return fallback(input, init)
    }))
    const view = renderApp('/config/attribute/new')

    const balanceGroup = await screen.findByRole('combobox', { name: '平衡分组' })
    await user.click(balanceGroup)
    const groupLabels = await screen.findAllByText('boss_stats')
    await user.click(groupLabels[groupLabels.length - 1])
    await waitFor(() => expect((balanceGroup as HTMLInputElement).value).toBe('boss_stats'))

    await user.click(balanceGroup)
    await user.click(await screen.findByRole('button', { name: '删除平衡分组候选 boss_stats' }))
    expect(screen.queryByRole('button', { name: '删除平衡分组候选 boss_stats' })).toBeNull()
    expect(JSON.parse(localStorage.getItem(`entity-editor:hidden-balance-groups:${project.id}:attribute`) ?? '[]')).toContain('boss_stats')

    const labels = [...view.container.querySelectorAll('.entity-editor-page .ant-form-item-label > label')]
    expect(labels.length).toBeGreaterThan(0)
    expect(labels.every(label => label.querySelector('.ant-form-item-tooltip'))).toBe(true)

    const balanceLabel = screen.getByText('平衡分组').closest('label')
    const helpIcon = balanceLabel?.querySelector<HTMLElement>('.ant-form-item-tooltip')
    expect(helpIcon).toBeTruthy()
    await user.hover(helpIcon!)
    expect(await screen.findByText(/把同类型且需要一起比较的配置归入一组/)).toBeTruthy()
  })

  it('suggests registered dimensions and matching base units', async () => {
    const user = userEvent.setup()
    vi.stubGlobal('fetch', mockEntityCatalogs())
    renderApp('/config/attribute/new')

    const dimension = await screen.findByRole('combobox', { name: '量纲' })
    await user.click(dimension)
    const dimensionLabels = await screen.findAllByText('damage')
    await user.click(dimensionLabels[dimensionLabels.length - 1])
    await waitFor(() => expect((dimension as HTMLInputElement).value).toBe('damage'))

    const baseUnit = screen.getByRole('combobox', { name: '基础单位' })
    await user.click(baseUnit)
    const unitLabels = await screen.findAllByText('damage_point（damage）')
    await user.click(unitLabels[unitLabels.length - 1])
    await waitFor(() => expect((baseUnit as HTMLInputElement).value).toBe('damage_point'))
  })

  it('suggests editable formulas and explains their inputs and units', async () => {
    const user = userEvent.setup()
    const fallback = mockEntityCatalogs()
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      if (String(input) === '/api/v1/entities/attribute?limit=200') {
        return Promise.resolve(new Response(JSON.stringify({
          items: [{
            id: project.id,
            name: 'Health',
            key: 'health',
            entity_version: 1,
            payload: { value_type: 'decimal', dimension: 'health', base_unit: 'health_point', default: '100' },
          }],
          next_cursor: null,
        }), { status: 200 }))
      }
      return fallback(input, init)
    }))
    renderApp('/config/character/new')

    await user.click(await screen.findByRole('button', { name: /添加属性值/ }))
    const attribute = screen.getByRole('combobox', { name: '属性' })
    await user.click(attribute)
    const attributeLabels = await screen.findAllByText('Health（health）')
    await user.click(attributeLabels[attributeLabels.length - 1])

    const formula = screen.getByRole('combobox', { name: '数值或公式' })
    await user.click(formula)
    const formulaOptions = await screen.findAllByText('80[health_point] · 固定值（health_point）')
    await user.click(formulaOptions[formulaOptions.length - 1])
    await waitFor(() => expect((formula as HTMLInputElement).value).toBe('80[health_point]'))
    await user.click(formula)
    expect((await screen.findAllByText('${self:health} · 读取自身 · Health（health）')).length).toBeGreaterThan(0)
    await user.keyboard('{Escape}')
    await user.clear(formula)
    await user.click(formula)
    await user.paste('42[health_point]')
    expect((formula as HTMLInputElement).value).toBe('42[health_point]')
    await user.keyboard('{Escape}')
    await user.click(formula)
    expect((await screen.findAllByText('80[health_point] · 固定值（health_point）')).length).toBeGreaterThan(0)
    await user.keyboard('{Escape}')

    const formulaLabel = screen.getByText('数值或公式').closest('label')
    const helpIcon = formulaLabel?.querySelector<HTMLElement>('.ant-form-item-tooltip')
    expect(helpIcon).toBeTruthy()
    await user.hover(helpIcon!)

    expect((await screen.findAllByText('80[health_point]')).length).toBeGreaterThan(0)
    expect(screen.getByText(/当前角色配置中的“属性值”规则/)).toBeTruthy()
    expect(screen.getByText(/左侧选择的“生命值”属性配置/)).toBeTruthy()
    expect(screen.getByText(/DSL 单位注册表中的/)).toBeTruthy()
    expect(screen.getByText('${self:属性_key}')).toBeTruthy()
    const operationTable = await screen.findByRole('table', { name: '公式支持的操作示例' })
    expect(within(operationTable).getAllByRole('row')).toHaveLength(22)
    for (const operation of [
      '加法 +', '减法 -', '乘法 *', '除法 /',
      '小于 <', '小于等于 <=', '大于 >', '大于等于 >=', '等于 ==', '不等于 !=',
      '逻辑与 &&', '逻辑或 ||', '逻辑非 !',
      '条件 if', '最小值 min', '最大值 max', '区间限制 clamp', '绝对值 abs',
      '向下取整 floor', '向上取整 ceil', '四舍六入五成双 round',
    ]) expect(within(operationTable).getByText(operation)).toBeTruthy()
    expect(screen.getByText('80[health_point] + 10[damage_point]')).toBeTruthy()
    expect(screen.getByText(/会因量纲不同而校验失败/)).toBeTruthy()
  })

  it('shows an inline validation error for incompatible formula dimensions', async () => {
    const user = userEvent.setup()
    const fallback = mockEntityCatalogs()
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      if (String(input) === '/api/v1/entities/attribute?limit=200') {
        return Promise.resolve(new Response(JSON.stringify({
          items: [{
            id: project.id,
            name: 'Health',
            key: 'health',
            entity_version: 1,
            payload: { value_type: 'decimal', dimension: 'health', base_unit: 'health_point', default: '100' },
          }],
          next_cursor: null,
        }), { status: 200 }))
      }
      return fallback(input, init)
    }))
    renderApp('/config/character/new')

    await user.click(await screen.findByRole('button', { name: /添加属性值/ }))
    const attribute = screen.getByRole('combobox', { name: '属性' })
    await user.click(attribute)
    const attributeLabels = await screen.findAllByText('Health（health）')
    await user.click(attributeLabels[attributeLabels.length - 1])
    const formula = screen.getByRole('combobox', { name: '数值或公式' })
    await user.click(formula)
    await user.paste('80[health_point] + 10[damage_point]')
    await user.tab()

    expect(await screen.findByText(/公式中的单位或量纲不兼容/)).toBeTruthy()
    expect(formula.getAttribute('aria-invalid')).toBe('true')
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
