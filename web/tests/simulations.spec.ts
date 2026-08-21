import { fireEvent, render, screen, waitFor } from '@testing-library/vue'
import { createPinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'
import { describe, expect, it, vi } from 'vitest'
import { VueQueryPlugin } from '@tanstack/vue-query'
import SimulationsView from '../src/views/SimulationsView.vue'
import { useProjectStore } from '../src/stores/project'

const projectID = '01948c1e-0000-7000-8000-000000000000'
const revisionID = '01948c1e-0000-7000-8000-000000000001'

describe('simulation admission form', () => {
  it('keeps the constrained scenario input after an authoritative server error', async () => {
    const fetchMock = vi.fn((url: string, init?: RequestInit) => {
      if (url.endsWith('/revisions')) return Promise.resolve(new Response(JSON.stringify({ items: [{ id: revisionID }] }), { status: 200 }))
      if (url.endsWith('/releases')) return Promise.resolve(new Response(JSON.stringify({ items: [] }), { status: 200 }))
      if (url.endsWith('/simulation-jobs') && init?.method === 'POST') return Promise.resolve(new Response(JSON.stringify({ title: 'Simulation parameter is invalid', code: 'SIMULATION_PARAMETER_INVALID' }), { status: 400 }))
      return Promise.resolve(new Response('', { status: 404 }))
    })
    vi.stubGlobal('fetch', fetchMock)
    const pinia = createPinia()
    useProjectStore(pinia).current = { id: projectID, name: 'balance', db_schema_version: 1 }
    const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/', component: SimulationsView }, { path: '/projects', component: { template: '<p>projects</p>' } }, { path: '/versions', component: { template: '<p>versions</p>' } }] })
    await router.push('/')
    await router.isReady()
    render(SimulationsView, { global: { plugins: [pinia, VueQueryPlugin, router] } })
    const source = await screen.findByRole('combobox', { name: '不可变 revision' })
    await fireEvent.update(source, revisionID)
    const amount = screen.getByRole('textbox', { name: /opening-strike 伤害/ })
    await fireEvent.update(amount, '42')
    await fireEvent.click(screen.getByRole('button', { name: '运行模拟' }))
    expect((await screen.findByText(/SIMULATION_PARAMETER_INVALID/)).textContent).toContain('SIMULATION_PARAMETER_INVALID')
    expect((amount as HTMLInputElement).value).toBe('42')
    await waitFor(() => expect(fetchMock.mock.calls.some(([url, options]) => String(url).endsWith('/simulation-jobs') && JSON.parse(String(options?.body)).parameters['/actions/opening-strike/inputs/amount'] === '42')).toBe(true))
  })

  it('opens an immutable historical run linked by the selected revision timeline', async () => {
    const runID = '01948c1e-0000-7000-8000-000000000099'
    const fetchMock = vi.fn((url: string) => {
      if (url.endsWith('/revisions')) return Promise.resolve(new Response(JSON.stringify({ items: [{ id: revisionID }] }), { status: 200 }))
      if (url.endsWith('/releases')) return Promise.resolve(new Response(JSON.stringify({ items: [] }), { status: 200 }))
      if (url.endsWith(`/revisions/${revisionID}`)) return Promise.resolve(new Response(JSON.stringify({ id: revisionID, timeline: [{ id: 'event-1', occurred_at: '2026-01-01T00:00:00Z', type: 'simulation_result', revision_id: revisionID, subject_id: runID, status: 'succeeded' }] }), { status: 200 }))
      if (url.endsWith(`/simulation-runs/${runID}`)) return Promise.resolve(new Response(JSON.stringify({ id: runID, revision_id: revisionID, input: { schema_version: 'v1', scene_id: 'single-target-30s', scene_version: 'v1', seed: 11, sample_count: 1000, metrics: [{ id: 'metric-dps', version: 'v1' }], config_hash: 'd'.repeat(64) }, input_hash: 'a'.repeat(64), result_hash: 'b'.repeat(64), fingerprint_hash: 'c'.repeat(64), reproducible: true, reasons: [], metrics: [], verification_refs: [] }), { status: 200 }))
      return Promise.resolve(new Response('', { status: 404 }))
    })
    vi.stubGlobal('fetch', fetchMock)
    const pinia = createPinia()
    useProjectStore(pinia).current = { id: projectID, name: 'balance', db_schema_version: 1 }
    const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/', component: SimulationsView }, { path: '/projects', component: { template: '<p>projects</p>' } }, { path: '/versions', component: { template: '<p>versions</p>' } }] })
    await router.push('/')
    await router.isReady()
    render(SimulationsView, { global: { plugins: [pinia, VueQueryPlugin, router] } })
    await fireEvent.update(await screen.findByRole('combobox', { name: '不可变 revision' }), revisionID)
    await fireEvent.click(await screen.findByRole('button', { name: `打开不可变 Run ${runID}` }))
    await screen.findByText('a'.repeat(64))
    expect(screen.getByText(/场景：single-target-30s@v1/)).toBeTruthy()
    expect(fetchMock.mock.calls.some(([url]) => String(url).endsWith(`/simulation-runs/${runID}`))).toBe(true)
  })
})
