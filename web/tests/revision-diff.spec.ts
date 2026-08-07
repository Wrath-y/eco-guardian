import { fireEvent, render, screen } from '@testing-library/vue'
import { createPinia } from 'pinia'
import { VueQueryPlugin } from '@tanstack/vue-query'
import { createRouter, createMemoryHistory } from 'vue-router'
import { describe, expect, it, vi } from 'vitest'
import RevisionDiffView from '../src/views/RevisionDiffView.vue'
import { useProjectStore } from '../src/stores/project'

const target = '01948c1e-0000-7000-8000-000000000000'
function router() { return createRouter({ history: createMemoryHistory(), routes: [{ path: '/versions', component: { template: '<main />' } }, { path: '/config/:kind/:id', component: { template: '<main />' } }, { path: '/versions/:id/diff', component: RevisionDiffView }] }) }
function detail() { return { id: target, display_revision: 2, config_hash: 'a'.repeat(64), metadata: { revision_id: target, config_hash: 'a'.repeat(64), version_manifest: { entries: [], hash: 'a'.repeat(64) }, created_at: '2026-01-01T00:00:00Z' }, status: ['history'], timeline: [] } }

describe('revision diff', () => {
  it('renders explicit no-baseline guidance without requesting a fake comparison', async () => {
    const fetchMock = vi.fn(() => Promise.resolve(new Response(JSON.stringify(detail()), { status: 200 }))); vi.stubGlobal('fetch', fetchMock)
    const appRouter = router(); await appRouter.push(`/versions/${target}/diff`); await appRouter.isReady()
    const pinia = createPinia(); useProjectStore(pinia).current = { id: target, name: 'Test', db_schema_version: 4 }
    render({ template: '<RouterView />' }, { global: { plugins: [pinia, VueQueryPlugin, appRouter] } })
    expect(await screen.findByText(/NO_BASELINE/)).toBeTruthy(); expect(fetchMock).not.toHaveBeenCalledWith(expect.stringContaining('/diff?'))
  })
  it('renders structured changes and an invalid-base explanation', async () => {
    vi.stubGlobal('fetch', vi.fn((url: string) => url.includes('/diff?') ? Promise.resolve(new Response(JSON.stringify({ title: '基准不存在' }), { status: 400 })) : Promise.resolve(new Response(JSON.stringify(detail()), { status: 200 }))))
    const appRouter = router(); await appRouter.push(`/versions/${target}/diff?base=missing`); await appRouter.isReady()
    const pinia = createPinia(); useProjectStore(pinia).current = { id: target, name: 'Test', db_schema_version: 4 }
    render({ template: '<RouterView />' }, { global: { plugins: [pinia, VueQueryPlugin, appRouter] } })
    expect(await screen.findByText(/无效或不可读取的基准版本/)).toBeTruthy(); expect(document.activeElement).toBe(screen.getByRole('alert'))
    await fireEvent.update(screen.getByRole('textbox', { name: '基准版本 ID' }), target); await fireEvent.click(screen.getByRole('button', { name: '比较' }))
  })
  it('keeps diff expansion and release controls keyboard reachable at 1024px', async () => {
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1024 })
    const policy = { id: target, display_version: 1, policy_hash: 'a'.repeat(64), scenes: [], samples: 1000, threshold_id: 'default', threshold_enabled: true, capabilities: [], created_at: '2026-01-01T00:00:00Z' }
    vi.stubGlobal('fetch', vi.fn((url: string) => Promise.resolve(new Response(JSON.stringify(url.includes('/diff?') ? { base_revision_id: target, target_revision_id: target, base_config_hash: 'a'.repeat(64), target_config_hash: 'a'.repeat(64), baseline_state: 'AVAILABLE', changes: [{ entity_id: target, entity_kind: 'tag', path: '/name', kind: 'MODIFY', old_value: '旧', new_value: '新' }] } : url.includes('/release-policies') ? { items: [policy] } : url.includes('/runtime/capabilities') ? { release: { enabled: false, disabled_reasons: [] } } : url.includes('/releases') ? { items: [] } : detail()), { status: 200 }))))
    const appRouter = router(); await appRouter.push(`/versions/${target}/diff?base=${target}`); await appRouter.isReady()
    const pinia = createPinia(); useProjectStore(pinia).current = { id: target, name: 'Test', db_schema_version: 4 }
    render({ template: '<RouterView />' }, { global: { plugins: [pinia, VueQueryPlugin, appRouter] } })
    await screen.findByText('查看前后值')
    const summary = document.querySelector<HTMLElement>('summary[aria-label="展开 MODIFY /name 的前后值"]')!
    summary.focus(); expect(document.activeElement).toBe(summary); await fireEvent.click(summary)
    expect(summary.closest('details')?.open).toBe(true)
    expect(screen.getByRole('textbox', { name: '发布说明' })).toBeTruthy()
  })
})
