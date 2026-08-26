import { fireEvent, render, screen, waitFor } from '@testing-library/vue'
import { VueQueryPlugin } from '@tanstack/vue-query'
import { createPinia } from 'pinia'
import { createMemoryHistory, createRouter } from 'vue-router'
import { describe, expect, it, vi } from 'vitest'
import ImpactView from '../src/features/impact/ImpactView.vue'
import ImpactJobProgress from '../src/features/impact/ImpactJobProgress.vue'
import type { ImpactCommand, ImpactJob, ImpactReport } from '../src/features/impact/api'
import { useProjectStore } from '../src/stores/project'

const projectID = '01948c1e-0000-7000-8000-000000000001'
const baseID = '01948c1e-0000-7000-8000-000000000002'
const targetID = '01948c1e-0000-7000-8000-000000000003'
const jobID = '01948c1e-0000-7000-8000-000000000004'
const reportID = '01948c1e-0000-7000-8000-000000000005'
const hash = 'a'.repeat(64)
function job(status: ImpactJob['status'] = 'running'): ImpactJob { return { id: jobID, kind: 'impact_analysis', revision_id: targetID, status, request_hash: hash, events_url: `/api/v1/jobs/${jobID}/events`, poll_after_ms: 100, created_at: '2026-08-26T00:00:00Z' } }
function report(): ImpactReport { return { id: reportID, input_hash: hash, result_hash: hash, analysis_contract_version: 'dependency-impact-v1', project_uuid: projectID, base: { revision_id: baseID, config_hash: hash, version_manifest_hash: hash, graph_manifest_hash: hash, graph_node_count: 2, graph_edge_count: 1 }, target: { revision_id: targetID, config_hash: hash, version_manifest_hash: hash, graph_manifest_hash: hash, graph_node_count: 2, graph_edge_count: 1 }, filters: { relationship_kinds: ['explicit'], direction: 'incoming', node_types: [], edge_types: [] }, limits: { max_depth: 3, max_nodes: 500, default_paths_per_target: 1, expanded_max_paths: 20 }, suspected_options: { enabled: true, max_seeds: 20, max_results: 20, graph_max_depth: 2 }, mode: 'reverse_dependency_impact', changed_entities: [{ entity_id: targetID, kind: 'skill', change_kind: 'MODIFY', field_paths: ['/name'], target_node_id: 'changed', query_eligible: true }], deterministic_affected: [{ node: { id: 'affected', type: 'character', label: '确定对象', text: 'deterministic', properties: {}, provenance: {} }, minimum_depth: 1, direct: true, indirect: false, tag_rule: false, evidence_ref: hash, default_path: { source_node_id: 'changed', target_node_id: 'affected', node_ids: ['changed', 'affected'], edge_ids: ['edge'], nodes: [{ id: 'changed', type: 'skill', label: '变化技能', text: '', properties: {}, provenance: {} }, { id: 'affected', type: 'character', label: '确定对象', text: '', properties: {}, provenance: {} }], edges: [{ id: 'edge', from: 'affected', to: 'changed', type: 'character_has_skill', relation_kind: 'explicit', confidence: 1, properties: {}, provenance: { field_path: '/skill_ids/0' } }], hop_count: 1, truncated: false, truncation_reasons: [] } }], suspected_associations: [{ rank: 1, node: { id: 'suspected', type: 'item', label: '疑似对象', text: '', properties: {}, provenance: {} }, citation_text: '检索证据', seed_node_id: 'changed', relationship_kinds: ['inferred'], scores: { rrf_score: '0.1' }, algorithm_version: 'hybrid-v1', evidence_ref: 'b'.repeat(64), model_provider: 'local', model: 'embed-v1' }], suspected_state: 'degraded', truncated: true, truncation_reasons: ['MAX_DEPTH'], warnings: ['NARROW_FILTER_OR_DEPTH'], cache_hit: false, freshness: { fresh: false, reasons: ['base_revision_changed'] }, created_at: '2026-08-26T00:00:00Z', links: { self: `/api/v1/impact-analyses/${reportID}`, job: `/api/v1/jobs/${jobID}`, path_expansions: `/api/v1/impact-analyses/${reportID}/path-expansions`, explanations: `/api/v1/impact-analyses/${reportID}/explanations` } } }

class FakeEventSource {
  static instances: FakeEventSource[] = []
  onopen: ((event: Event) => void) | null = null
  onerror: ((event: Event) => void) | null = null
  listeners = new Map<string, Array<(event: MessageEvent<string>) => void>>()
  constructor(readonly url: string) { FakeEventSource.instances.push(this) }
  addEventListener(type: string, listener: (event: MessageEvent<string>) => void) { this.listeners.set(type, [...(this.listeners.get(type) ?? []), listener]) }
  close() { /* test transport */ }
  emit(type: string, data: string) { for (const listener of this.listeners.get(type) ?? []) listener(new MessageEvent('message', { data })) }
}

function routerAt(path = '/impact') { return createRouter({ history: createMemoryHistory(), routes: [{ path: '/impact', component: ImpactView }, { path: '/projects', component: { template: '<main />' } }, { path: '/versions', component: { template: '<main />' } }, { path: '/simulations', component: { template: '<main />' } }] }) }

describe('impact durable job', () => {
  it('persists SSE ordinal, renders cancellation truth and falls back to polling without creating work', async () => {
    sessionStorage.clear(); FakeEventSource.instances = []; vi.stubGlobal('EventSource', FakeEventSource)
    const fetchMock = vi.fn(() => Promise.resolve(new Response(JSON.stringify(job()), { status: 200 }))); vi.stubGlobal('fetch', fetchMock)
    render(ImpactJobProgress, { props: { projectID, jobID }, global: { plugins: [VueQueryPlugin] } })
    await screen.findByText('running')
    FakeEventSource.instances[0].emit('job', JSON.stringify({ job_id: jobID, ordinal: 7, phase: 'DEFAULT_PATHS', progress: 70, created_at: '2026-08-26T00:00:01Z' }))
    expect((await screen.findAllByText(/DEFAULT_PATHS/)).length).toBeGreaterThan(0)
    expect(sessionStorage.getItem(`impact-ordinal:${projectID}:${jobID}`)).toBe('7')
    vi.useFakeTimers(); FakeEventSource.instances[0].onerror?.(new Event('error'))
    expect(await screen.findByText(/断线后轮询/)).toBeTruthy()
    await vi.advanceTimersByTimeAsync(100)
    expect((fetchMock.mock.calls as unknown as Array<[string, RequestInit | undefined]>).some(([, options]) => options?.method === 'POST')).toBe(false)
    vi.useRealTimers()
  })
})

describe('impact analysis page', () => {
  it('reuses one idempotency key on network replay and submits exact bounded generated request', async () => {
    sessionStorage.clear(); vi.stubGlobal('EventSource', FakeEventSource)
    let posts = 0
    const fetchMock = vi.fn((url: string, options?: RequestInit) => {
      if (options?.method === 'POST') { posts++; return Promise.resolve(posts === 1 ? new Response(JSON.stringify({ title: '暂不可用', code: 'GRAPH_STORE_UNAVAILABLE', retryable: true }), { status: 503 }) : new Response(JSON.stringify({ job: job('queued'), location: `/api/v1/jobs/${jobID}`, result_type: 'impact_analysis', result_url: '', cache_hit: false }), { status: 202 })) }
      if (url.includes('/revisions')) return Promise.resolve(new Response(JSON.stringify({ items: [{ id: targetID, display_revision: 2, status: ['working', 'history'] }, { id: baseID, display_revision: 1, status: ['active_release', 'history'] }] }), { status: 200 }))
      return Promise.resolve(new Response(JSON.stringify(job('queued')), { status: 200 }))
    }); vi.stubGlobal('fetch', fetchMock)
    const router = routerAt(); await router.push('/impact'); await router.isReady()
    const pinia = createPinia(); useProjectStore(pinia).current = { id: projectID, name: 'Test', db_schema_version: 21 }
    render({ template: '<RouterView />' }, { global: { plugins: [pinia, VueQueryPlugin, router], stubs: { ImpactEvidencePath: true } } })
    await screen.findAllByRole('option', { name: /#2/ }); await waitFor(() => expect((screen.getByLabelText('Base revision') as HTMLSelectElement).value).toBe(baseID))
    const submit = screen.getByRole('button', { name: '创建 Impact Job' }); expect(submit.hasAttribute('disabled')).toBe(false)
    await fireEvent.click(submit); expect((await screen.findByRole('alert')).textContent).toContain('GRAPH_STORE_UNAVAILABLE')
    await fireEvent.click(screen.getByRole('button', { name: '创建 Impact Job' }))
    const requests = (fetchMock.mock.calls as unknown as Array<[string, RequestInit]>).filter(([, options]) => options?.method === 'POST')
    expect((requests[0][1].headers as Record<string, string>)['Idempotency-Key']).toBe((requests[1][1].headers as Record<string, string>)['Idempotency-Key'])
    const body = JSON.parse(String(requests[1][1].body)) as ImpactCommand
    expect(body).toMatchObject({ project_uuid: projectID, base_revision_id: baseID, target_revision_id: targetID, filters: { relationship_kinds: ['explicit'], direction: 'incoming' }, limits: { max_depth: 3, max_nodes: 500 } })
  })

  it('separates deterministic and suspected evidence, exposes stale/truncated text and keyboard-selectable paths', async () => {
    vi.stubGlobal('fetch', vi.fn((url: string) => Promise.resolve(new Response(JSON.stringify(url.includes('/impact-analyses/') ? report() : url.includes('/releases') ? { items: [] } : { items: [] }), { status: 200 }))))
    const router = routerAt(); await router.push(`/impact?report=${reportID}`); await router.isReady()
    const pinia = createPinia(); useProjectStore(pinia).current = { id: projectID, name: 'Test', db_schema_version: 21 }
    render({ template: '<RouterView />' }, { global: { plugins: [pinia, VueQueryPlugin, router], stubs: { ImpactEvidencePath: { props: ['path'], template: '<div aria-label="evidence-path">{{ path?.edges?.[0]?.type }}</div>' } } } })
    expect(await screen.findByText(/历史结果已陈旧/)).toBeTruthy()
    expect(screen.getByText(/确定性 affected（1）/)).toBeTruthy()
    expect(screen.getByText(/Suspected 关联（1）/)).toBeTruthy()
    expect(screen.getByText(/确定性前缀已截断：MAX_DEPTH/)).toBeTruthy()
    const target = screen.getByRole('button', { name: /确定对象 · 深度 1 · 直接影响/ }); target.focus(); await fireEvent.click(target)
    expect(document.activeElement).toBe(target)
    expect((await screen.findByLabelText('evidence-path')).textContent).toContain('character_has_skill')
  })
})
