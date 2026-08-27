import { fireEvent, render, screen, waitFor } from '@testing-library/vue'
import { createRouter, createMemoryHistory } from 'vue-router'
import { describe, expect, it, vi } from 'vitest'
import StructuredPayloadForm from '../src/components/StructuredPayloadForm.vue'
import EntityEditorView from '../src/views/EntityEditorView.vue'
import { emptyDraft, emptyPayload, validateDraft, type EntityKind } from '../src/forms/registry'

describe('structured authoring forms', () => {
  for (const kind of ['attribute', 'tag', 'character', 'skill', 'item', 'effect'] as EntityKind[]) {
    it(`renders the ${kind} form without a raw code editor`, () => {
      render(StructuredPayloadForm, { props: { kind, modelValue: emptyPayload(kind) } })
      expect(screen.getByRole('group', { name: '配置字段' })).toBeTruthy()
      expect(screen.queryByText(/JSON|YAML|脚本|导入|导出|预览/)).toBeNull()
    })
  }

  it('accepts base-shaped fixtures and reports linked envelope errors for every kind', () => {
    for (const kind of ['attribute', 'tag', 'character', 'skill', 'item', 'effect'] as EntityKind[]) {
      const draft = emptyDraft(kind) as unknown as Record<string, unknown>; draft.key = `${kind}_fixture`; draft.name = kind
      const payload = draft.payload as Record<string, unknown>
      if (kind === 'attribute') { payload.dimension = 'combat'; payload.base_unit = 'point' }
      if (kind === 'tag') payload.category = 'element'
      if (kind === 'item') payload.slot = 'main_hand'
      expect(validateDraft(draft, kind)).toEqual([])
      draft.key = ''; expect(validateDraft(draft, kind).some(issue => issue.path === 'key')).toBe(true)
    }
  })

  it('offers only registry metadata and inserts a stable selector token without evaluating it', async () => {
    const view = render(StructuredPayloadForm, { props: {
      kind: 'character', modelValue: { attribute_values: [{ output_attribute_id: '', expression: '' }], skill_ids: [], item_ids: [], rule_blocks: [] },
      dsl: { dsl_version: 'dsl-v1', manifest_hash: 'manifest', selector_template: '${scope:symbol}', scopes: ['self', 'source', 'target', 'scenario'], units: [{ name: 'scalar', value_type: 'decimal', dimension: 'scalar', base: 'scalar' }], functions: [{ name: 'min', signature: '(T,T,...)->T', pure: true, implementation_version: '1' }] },
    } })
    expect(screen.getByRole('button', { name: '插入 self 选择器' })).toBeTruthy()
    expect(view.container.querySelector('option[value="min("]')).toBeTruthy()
    await fireEvent.click(screen.getByRole('button', { name: '插入 self 选择器' }))
    expect(screen.getByDisplayValue('${self:symbol}')).toBeTruthy()
    expect(screen.queryByText(/执行|预览|运行公式/)).toBeNull()
  })

  it('keeps invalid input and focuses the linked error summary', async () => {
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response(JSON.stringify({ kind: 'tag', schema_id: 'urn:eco:schema:tag:1', schema: {} }), { status: 200 }))))
    const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/projects', component: { template: '<main />' } }, { path: '/config/:kind/:id', component: EntityEditorView }] })
    await router.push('/config/tag/new'); await router.isReady()
    render({ template: '<RouterView />' }, { global: { plugins: [router] } })
    await waitFor(() => expect(screen.getByRole('button', { name: '保存' })).toBeTruthy())
    await fireEvent.click(screen.getByRole('button', { name: '保存' }))
    const summary = await screen.findByRole('alert')
    expect(summary.textContent).toContain('请修正')
    expect(document.activeElement).toBe(summary)
  })

  it('announces a saved LOCAL BLOCK revision without presenting it as a FULL pass', async () => {
    const id = '01948c1e-0000-7000-8000-000000000010'
    vi.stubGlobal('fetch', vi.fn((url: string) => Promise.resolve(new Response(JSON.stringify(url.includes('/schemas/') ? { kind: 'tag', schema_id: 'urn:eco:schema:tag:1', schema: {} } : {
      entity: { id, kind: 'tag', key: 'fire', name: 'Fire', tag_ids: [], status: 'active', schema_version: 1, payload: { category: 'element', parent_tag_ids: [] }, extensions: {}, entity_version: 1, created_at: '', updated_at: '' },
      revision: { id: '01948c1e-0000-7000-8000-000000000011', display_revision: 1, config_hash: 'a'.repeat(64), created_at: '', validation: { run_id: '01948c1e-0000-7000-8000-000000000012', scope: 'LOCAL', error: 0, block: 1, warning: 0, info: 0 } },
    }), { status: url.includes('/schemas/') ? 200 : 201, headers: url.includes('/schemas/') ? {} : { ETag: '"entity:1"' } }))))
    const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/projects', component: { template: '<main />' } }, { path: '/config/:kind/:id', component: EntityEditorView }] })
    await router.push('/config/tag/new'); await router.isReady()
    render({ template: '<RouterView />' }, { global: { plugins: [router] } })
    await waitFor(() => expect(screen.getByRole('button', { name: '保存' })).toBeTruthy())
    await fireEvent.update(screen.getByLabelText('Key'), 'fire')
    await fireEvent.update(screen.getByLabelText('名称'), 'Fire')
    await fireEvent.update(document.querySelector('[data-field-path="/payload/category"]') as HTMLInputElement, 'element')
    await fireEvent.click(screen.getByRole('button', { name: '保存' }))
    expect(await screen.findByText(/LOCAL 校验摘要：ERROR 0，BLOCK 1/)).toBeTruthy()
    expect(screen.getByText(/此修订含 BLOCK/)).toBeTruthy()
    expect(screen.queryByText('当前 FULL 校验通过。')).toBeNull()
  })

  it('retains the form after daily backup failure and requires an explicit same-day waiver', async () => {
    const failedJobID = '01948c1e-0000-7000-8000-000000000020'
    let entityPosts = 0
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      if (url.includes('/schemas/')) return Promise.resolve(new Response(JSON.stringify({ kind: 'tag', schema_id: 'urn:eco:schema:tag:1', schema: {} }), { status: 200 }))
      if (url.endsWith('/daily-waivers')) return Promise.resolve(new Response(JSON.stringify({ project_uuid: '01948c1e-0000-7000-8000-000000000021', local_date: '2026-08-26', failed_backup_job_id: failedJobID, confirmed_at: new Date().toISOString(), replay: false }), { status: 200 }))
      if (init?.method === 'POST') {
        entityPosts++
        if (entityPosts === 1) return Promise.resolve(new Response(JSON.stringify({ code: 'DAILY_BACKUP_REQUIRED', title: 'Daily backup failed', details: { failed_backup_job_id: failedJobID, state: 'awaiting_waiver' } }), { status: 409 }))
        return Promise.resolve(new Response(JSON.stringify({ entity: { id: '01948c1e-0000-7000-8000-000000000022', kind: 'tag', key: 'fire', name: 'Fire', payload: { category: 'element', parent_tag_ids: [] }, extensions: {} }, revision: { validation: { run_id: '01948c1e-0000-7000-8000-000000000023', scope: 'LOCAL', error: 0, block: 0, warning: 0, info: 0 } } }), { status: 201 }))
      }
      return Promise.resolve(new Response(null, { status: 404 }))
    })
    vi.stubGlobal('fetch', fetchMock)
    const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/projects', component: { template: '<main />' } }, { path: '/config/:kind/:id', component: EntityEditorView }] })
    await router.push('/config/tag/new'); await router.isReady()
    render({ template: '<RouterView />' }, { global: { plugins: [router] } })
    await fireEvent.update(await screen.findByLabelText('Key'), 'fire')
    await fireEvent.update(screen.getByLabelText('名称'), 'Fire')
    await fireEvent.update(document.querySelector('[data-field-path="/payload/category"]') as HTMLInputElement, 'element')
    await fireEvent.click(screen.getByRole('button', { name: '保存' }))
    expect(await screen.findByRole('heading', { name: '今日编辑尚未受备份保护' })).toBeTruthy()
    expect(screen.getByDisplayValue('fire')).toBeTruthy()
    expect(screen.getByText(/系统不会自动接受 waiver/)).toBeTruthy()
    await fireEvent.click(screen.getByRole('button', { name: '明确继续：今天无恢复点' }))
    expect(await screen.findByText(/2026-08-26 已明确选择无恢复点继续编辑/)).toBeTruthy()
    expect(await screen.findByText(/保存成功/)).toBeTruthy()
    const waiver = fetchMock.mock.calls.find(([input]) => String(input).endsWith('/daily-waivers'))
    expect(JSON.parse(String(waiver?.[1]?.body))).toEqual({ failed_backup_job_id: failedJobID, confirmation: 'CONTINUE_WITHOUT_BACKUP_TODAY' })
  })

  it('renders an unknown extension as a read-only warning', async () => {
    vi.stubGlobal('fetch', vi.fn((url: string) => Promise.resolve(new Response(JSON.stringify(url.includes('/schemas/') ? { kind: 'tag', schema_id: 'urn:eco:schema:tag:1', schema: {} } : {
      id: '01948c1e-0000-7000-8000-000000000000', kind: 'tag', key: 'fire', name: 'Fire', tag_ids: [], status: 'active', schema_version: 1,
      payload: { category: 'element', parent_tag_ids: [] }, extensions: { 'urn:future/example': { retained: true } }, entity_version: 1, created_at: '', updated_at: '',
    }), { status: 200, headers: url.includes('/entities/') ? { ETag: '"entity:1"' } : {} }))))
    const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/projects', component: { template: '<main />' } }, { path: '/config/:kind/:id', component: EntityEditorView }] })
    await router.push('/config/tag/01948c1e-0000-7000-8000-000000000000'); await router.isReady()
    render({ template: '<RouterView />' }, { global: { plugins: [router] } })
    expect(await screen.findByText('此对象包含当前版本不支持的扩展；它们将只读保留。')).toBeTruthy()
    expect(screen.getByText(/urn:future\/example/)).toBeTruthy()
  })

  it('focuses an RFC 6901 formula field and selects its UTF-8 diagnostic span', async () => {
    const id = '01948c1e-0000-7000-8000-000000000000'
    vi.stubGlobal('fetch', vi.fn((url: string) => Promise.resolve(new Response(JSON.stringify(url.includes('/schemas/') ? { kind: 'character', schema_id: 'urn:eco:schema:character:1', schema: {} } : {
      id, kind: 'character', key: 'hero', name: 'Hero', tag_ids: [], status: 'active', schema_version: 1,
      payload: { attribute_values: [{ output_attribute_id: 'attack', expression: '甲😀+1' }], skill_ids: [], item_ids: [], rule_blocks: [] }, extensions: {}, entity_version: 1, created_at: '', updated_at: '',
    }), { status: 200, headers: url.includes('/entities/') ? { ETag: '"entity:1"' } : {} }))))
    const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/projects', component: { template: '<main />' } }, { path: '/config/:kind/:id', component: EntityEditorView }] })
    await router.push(`/config/character/${id}?field_path=/payload/attribute_values/0/expression&span_start=3&span_end=7`); await router.isReady()
    render({ template: '<RouterView />' }, { global: { plugins: [router] } })
    const expression = await screen.findByDisplayValue('甲😀+1') as HTMLInputElement
    await waitFor(() => expect(document.activeElement).toBe(expression))
    expect([expression.selectionStart, expression.selectionEnd]).toEqual([1, 3])
  })

  it('falls back to field focus when a formula span is not valid UTF-8', async () => {
    const id = '01948c1e-0000-7000-8000-000000000001'
    vi.stubGlobal('fetch', vi.fn((url: string) => Promise.resolve(new Response(JSON.stringify(url.includes('/schemas/') ? { kind: 'character', schema_id: 'urn:eco:schema:character:1', schema: {} } : {
      id, kind: 'character', key: 'hero', name: 'Hero', tag_ids: [], status: 'active', schema_version: 1,
      payload: { attribute_values: [{ output_attribute_id: 'attack', expression: '甲😀+1' }], skill_ids: [], item_ids: [], rule_blocks: [] }, extensions: {}, entity_version: 1, created_at: '', updated_at: '',
    }), { status: 200, headers: url.includes('/entities/') ? { ETag: '"entity:1"' } : {} }))))
    const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/projects', component: { template: '<main />' } }, { path: '/config/:kind/:id', component: EntityEditorView }] })
    await router.push(`/config/character/${id}?field_path=/payload/attribute_values/0/expression&span_start=1&span_end=7`); await router.isReady()
    render({ template: '<RouterView />' }, { global: { plugins: [router] } })
    const expression = await screen.findByDisplayValue('甲😀+1') as HTMLInputElement
    await waitFor(() => expect(document.activeElement).toBe(expression))
    expect(expression.selectionStart).toBe(expression.selectionEnd)
  })
})
