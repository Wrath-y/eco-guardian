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
})
