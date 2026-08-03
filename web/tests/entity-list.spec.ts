import { fireEvent, render, screen, waitFor } from '@testing-library/vue'
import { createRouter, createMemoryHistory } from 'vue-router'
import { describe, expect, it, vi } from 'vitest'
import EntityListView from '../src/views/EntityListView.vue'

function listRouter() { return createRouter({ history: createMemoryHistory(), routes: [{ path: '/projects', component: { template: '<main />' } }, { path: '/config/:kind', component: EntityListView }, { path: '/config/:kind/:id', component: { template: '<main />' } }] }) }

describe('entity list states', () => {
  it('shows an actionable empty state and keyboard-focusable create link', async () => {
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response(JSON.stringify({ items: [], next_cursor: null }), { status: 200 }))))
    const router = listRouter(); await router.push('/config/tag'); await router.isReady()
    render({ template: '<RouterView />' }, { global: { plugins: [router] } })
    const create = await screen.findByRole('link', { name: '新建对象' }); create.focus()
    expect(document.activeElement).toBe(create)
  })

  it('keeps a stable cursor action and reports no search results', async () => {
    const fetchMock = vi.fn((url: string) => {
      if (url.includes('query=none')) return Promise.resolve(new Response(JSON.stringify({ items: [], next_cursor: null }), { status: 200 }))
      return Promise.resolve(new Response(JSON.stringify({ items: [{ id: '01948c1e-0000-7000-8000-000000000000', name: 'Flame', key: 'flame', entity_version: 1 }], next_cursor: 'next' }), { status: 200 }))
    })
    vi.stubGlobal('fetch', fetchMock)
    const router = listRouter(); await router.push('/config/tag'); await router.isReady()
    render({ template: '<RouterView />' }, { global: { plugins: [router] } })
    const next = await screen.findByRole('button', { name: '下一页' }); next.focus(); await fireEvent.click(next)
    await waitFor(() => expect(fetchMock.mock.calls.some(([url]) => String(url).includes('cursor=next'))).toBe(true))
    await fireEvent.update(screen.getByRole('textbox', { name: '搜索' }), 'none')
    expect(await screen.findByText('没有匹配结果。')).toBeTruthy()
  })

  it('offers retry after a terminal list request failure', async () => {
    const fetchMock = vi.fn(() => Promise.resolve(new Response(JSON.stringify({ title: '服务不可用' }), { status: 503 })))
    vi.stubGlobal('fetch', fetchMock)
    const router = listRouter(); await router.push('/config/tag'); await router.isReady()
    render({ template: '<RouterView />' }, { global: { plugins: [router] } })
    await fireEvent.click(await screen.findByRole('button', { name: '重试' }))
    expect(fetchMock).toHaveBeenCalledTimes(2)
  })
})
