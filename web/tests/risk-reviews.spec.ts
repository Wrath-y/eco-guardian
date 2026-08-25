import { fireEvent, render, screen } from '@testing-library/vue'
import { VueQueryPlugin } from '@tanstack/vue-query'
import { createPinia } from 'pinia'
import { createMemoryHistory, createRouter } from 'vue-router'
import { describe, expect, it, vi } from 'vitest'
import RiskReviewDetail from '../src/features/risk-reviews/RiskReviewDetail.vue'
import RiskJobProgress from '../src/features/risk-reviews/RiskJobProgress.vue'
import RiskReviewsView from '../src/features/risk-reviews/RiskReviewsView.vue'
import type { RiskJob, RiskReview } from '../src/features/risk-reviews/api'
import { useProjectStore } from '../src/stores/project'

const id = '01948c1e-0000-7000-8000-000000000001'
const id2 = '01948c1e-0000-7000-8000-000000000002'
const hash = 'a'.repeat(64)
const hashB = 'b'.repeat(64)
function review(overridable = false, structural = false): RiskReview {
  const item = structural ? {
    id: 'structure-cycle', ordinal: 0, kind: 'structural', role: 'required', comparison_status: 'COMPARABLE', severity: 'BLOCK', reason: null, rule: { id: 'risk-structure', version: 'v1', hash }, evidence_hash: hash, item_hash: hashB, overridable: false, override_classification: 'NON_OVERRIDABLE', metric_evidence: null,
    structural_evidence: { type: 'VALIDATION_ISSUE', rule: { id: 'risk-structure', version: 'v1', hash }, entity_id: id, field_path: '/payload/formula', ordinal: 0, fingerprint: hash },
  } : {
    id: 'metric-resource-row', ordinal: 0, kind: 'metric', role: 'required', comparison_status: 'COMPARABLE', severity: overridable ? 'BLOCK' : 'WARNING', reason: null, rule: { id: 'risk-relative', version: 'v1', hash }, evidence_hash: hash, item_hash: hashB, overridable, override_classification: overridable ? 'NUMERIC_ELIGIBLE' : 'NON_OVERRIDABLE', structural_evidence: null,
    metric_evidence: { scene_id: 'resource-balance', scene_version: 'v1', metric_id: 'metric-resource', metric_version: 'v1', unit: 'ratio', direction: 'target_range', target_range: { lower: '0.8', upper: '1.2', bounds: 'inclusive' }, absolute_threshold: null, subject: { type: 'cohort', entity_kind: 'character', balance_group: 'starter', stable_id: null, members: [id, id2] }, candidate: { status: 'AVAILABLE', value: '1.3', confidence_low: '1.2', confidence_high: '1.4', unavailable: null, run_id: id, result_hash: hash }, baseline: { status: 'UNAVAILABLE', value: null, confidence_low: null, confidence_high: null, unavailable: { code: 'ZERO_BASELINE', missing: ['baseline.value'], message: 'Baseline is zero' }, run_id: id2, result_hash: hashB }, signed_delta: null, risk_delta: null, relative_risk: null, assumptions: ['fixed seed'] },
  }
  return {
    id, report_kind: 'calculation', schema_version: 'v1', project_id: id, candidate: { revision_id: id, config_hash: hash, version_manifest_hash: hashB }, baseline: { type: 'BASELINE', release_id: id2, revision: { revision_id: id2, config_hash: hashB, version_manifest_hash: hash } }, policy: { id: 'policy', version: '1', hash }, threshold: { identity: { id: 'threshold', version: '1', hash }, enabled: true, source: 'modified_starter', body_hash: hashB, assumptions: [], entries: [{ scene_id: 'resource-balance', scene_version: 'v1', metric_id: 'metric-resource', metric_version: 'v1', balance_group: null, unit: 'ratio', direction: 'target_range', relative_warning: '0.1', relative_block: '0.25', absolute_warning: null, absolute_block: null }] }, validation: { id: 'validation', version: 'v1', hash }, implementations: [{ id: 'risk-comparison', version: 'v1', hash }], simulation_run_ids: [id, id2], input_hash: hash, calculation_hash: hashB, explanation_evidence_hash: hash, report_hash: hashB, source_report_id: null, decision_item_ids: [], decision_reason: null, items: [item], read_time: { freshness: 'FRESH', freshness_reasons: [], gate_state: overridable || structural ? 'BLOCK' : 'WARNING', gate_item_ids: [item.id], active_baseline_release_id: id2, release_audit_url: null, impact_evidence_refs: [] }, created_at: '2026-08-24T00:00:00Z',
  } as unknown as RiskReview
}
function testRouter() { return createRouter({ history: createMemoryHistory(), routes: [{ path: '/', component: { template: '<main />' } }, { path: '/versions/:id/diff', component: { template: '<main />' } }, { path: '/config/:kind/:id', component: { template: '<main />' } }] }) }

describe('risk review evidence', () => {
  it('shows status separately, target bounds, missing values as text, cohort members and Graph degradation', async () => {
    const router = testRouter(); await router.push('/'); await router.isReady()
    render(RiskReviewDetail, { props: { review: review() }, global: { plugins: [router] } })
    expect(screen.getByText('COMPARABLE')).toBeTruthy()
		expect(screen.getAllByText('WARNING').length).toBeGreaterThan(0)
    expect(screen.getAllByText(/不适用（未提供）/).length).toBeGreaterThan(0)
    expect(screen.getAllByText(/\[0.8, 1.2\] inclusive/).length).toBeGreaterThan(0)
    expect(screen.getByText(/Graph\/影响证据不可用/)).toBeTruthy()
    await fireEvent.click(screen.getByText(/metric-resource-row · 展开证据/))
    expect(screen.getByText(id2)).toBeTruthy()
    expect(screen.queryByRole('button', { name: /创建 immutable decision report/ })).toBeNull()
  })

  it('creates a decision request only for eligible numeric BLOCK and keeps server BLOCK visible', async () => {
    const router = testRouter(); await router.push('/'); await router.isReady()
    const rendered = render(RiskReviewDetail, { props: { review: review(true) }, global: { plugins: [router] } })
    expect(screen.getAllByText('BLOCK').length).toBeGreaterThan(0)
    await fireEvent.update(screen.getByRole('textbox', { name: '说明' }), 'Reviewed exact numeric evidence')
    await fireEvent.click(screen.getByRole('button', { name: '创建 immutable decision report' }))
    expect(rendered.emitted().decision?.[0]).toEqual([[{ item_id: 'metric-resource-row', item_hash: hashB }], 'Reviewed exact numeric evidence'])
  })

  it('keeps structural BLOCK non-overridable and links to the validation field', async () => {
    const router = testRouter(); await router.push('/'); await router.isReady()
    render(RiskReviewDetail, { props: { review: review(false, true) }, global: { plugins: [router] } })
    await fireEvent.click(screen.getByText(/structure-cycle · 展开证据/))
    expect(screen.getByText(/打开 #6 issue 对应字段/)).toBeTruthy()
    expect(screen.getByText(/不可覆盖结构\/验证类别/)).toBeTruthy()
    expect(screen.queryByRole('textbox', { name: '说明' })).toBeNull()
  })
})

class FakeEventSource {
  static instances: FakeEventSource[] = []
  onopen: ((event: Event) => void) | null = null
  onerror: ((event: Event) => void) | null = null
  private listeners = new Map<string, Array<(event: MessageEvent<string>) => void>>()
  constructor(readonly url: string) { FakeEventSource.instances.push(this) }
  addEventListener(type: string, listener: (event: MessageEvent<string>) => void) { this.listeners.set(type, [...(this.listeners.get(type) ?? []), listener]) }
  close() { /* test transport */ }
  emit(type: string, data: string) { for (const listener of this.listeners.get(type) ?? []) listener(new MessageEvent('message', { data })) }
}
function job(status: RiskJob['status'] = 'running'): RiskJob { return { id, kind: 'risk_review', revision_id: id, status, request_hash: hash, events_url: `/api/v1/jobs/${id}/events`, poll_after_ms: 100, created_at: '2026-08-24T00:00:00Z' } }

describe('risk durable Job UI', () => {
  it('deduplicates persisted events, falls back to polling and never submits a second Job', async () => {
    FakeEventSource.instances = []; sessionStorage.clear(); vi.stubGlobal('EventSource', FakeEventSource)
    const fetchMock = vi.fn(() => Promise.resolve(new Response(JSON.stringify(job()), { status: 200 }))); vi.stubGlobal('fetch', fetchMock)
    render(RiskJobProgress, { props: { projectID: id, jobID: id }, global: { plugins: [VueQueryPlugin] } })
    await screen.findByText('running')
    FakeEventSource.instances[0].emit('job', JSON.stringify({ job_id: id, ordinal: 4, phase: 'COMPARING', progress: 100, created_at: '2026-08-24T00:00:01Z' }))
    expect((await screen.findAllByText(/COMPARING/)).length).toBeGreaterThan(0)
    expect(sessionStorage.getItem(`risk-job-ordinal:${id}:${id}`)).toBe('4')
    vi.useFakeTimers(); FakeEventSource.instances[0].onerror?.(new Event('error'))
    expect(await screen.findByText(/SSE 不可用/)).toBeTruthy()
    await vi.advanceTimersByTimeAsync(100)
    const calls = fetchMock.mock.calls as unknown as Array<[string, RequestInit | undefined]>
    expect(calls.some(([, options]) => options?.method === 'POST')).toBe(false)
    vi.useRealTimers()
  })

  it.each([['canceled' as const, '已取消；任何部分结果都不视为成功。'], ['interrupted' as const, '已中断；恢复前必须重新核对全部固定 identity。']])('renders %s textually and focuses the terminal heading', async (status, message) => {
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response(JSON.stringify(job(status)), { status: 200 }))))
    render(RiskJobProgress, { props: { projectID: id, jobID: id }, global: { plugins: [VueQueryPlugin] } })
    expect(await screen.findByText(message)).toBeTruthy()
    expect(document.activeElement?.id).toBe('risk-job-heading')
  })
})

describe('risk review admission page', () => {
	it('requires explicit threshold enable and submits exact current baseline/run identities at 1024px', async () => {
		sessionStorage.clear()
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1024 })
    const revision = { id, display_revision: 2, config_hash: hash, metadata: { revision_id: id, config_hash: hash, version_manifest: { entries: [{ component: 'risk', version: 'v1', manifest_hash: hash }], hash: hashB }, created_at: '2026-08-24T00:00:00Z' }, status: ['candidate'], timeline: [] }
    const baselineRevision = { ...revision, id: id2, config_hash: hashB, metadata: { ...revision.metadata, revision_id: id2, config_hash: hashB, version_manifest: { ...revision.metadata.version_manifest, hash } } }
    const release = { id: id2, revision_id: id2, policy_id: id, intent_id: id, gate_evidence: [], confirmations: [], created_at: '2026-08-24T00:00:00Z' }
    const policy = { id, display_version: 1, policy_hash: hash, scenes: [{ id: 'resource-balance', scene_version: 'v1', seed: 1, required: true, metrics: [{ id: 'metric-resource', required: true }] }], samples: 100, threshold_id: 'starter', threshold_enabled: true, capabilities: [{ capability_id: 'risk', gate_id: 'balance-risk', contract_version: 'v1' }], created_at: '2026-08-24T00:00:00Z' }
    const fetchMock = vi.fn((url: string, options?: RequestInit) => {
      if (options?.method === 'POST') return Promise.resolve(new Response(JSON.stringify({ job: job('queued'), location: `/api/v1/jobs/${id}` }), { status: 202 }))
      if (url.includes('/release-policies')) return Promise.resolve(new Response(JSON.stringify({ items: [policy] }), { status: 200 }))
      if (url.includes('/releases')) return Promise.resolve(new Response(JSON.stringify({ items: [release] }), { status: 200 }))
      if (url.endsWith(`/revisions/${id2}`)) return Promise.resolve(new Response(JSON.stringify(baselineRevision), { status: 200 }))
      if (url.endsWith(`/revisions/${id}`)) return Promise.resolve(new Response(JSON.stringify(revision), { status: 200 }))
      return Promise.resolve(new Response(JSON.stringify({ items: [revision] }), { status: 200 }))
    }); vi.stubGlobal('fetch', fetchMock); vi.stubGlobal('EventSource', FakeEventSource)
    const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/risk-reviews', component: RiskReviewsView }, { path: '/projects', component: { template: '<main />' } }, { path: '/versions', component: { template: '<main />' } }, { path: '/simulations', component: { template: '<main />' } }] }); await router.push('/risk-reviews'); await router.isReady()
    const pinia = createPinia(); useProjectStore(pinia).current = { id, name: 'Test', db_schema_version: 16 }
		render({ template: '<RouterView />' }, { global: { plugins: [pinia, VueQueryPlugin, router] } })
		expect(await screen.findByText(/未配置（starter 仍 inactive）/)).toBeTruthy()
		await screen.findByRole('option', { name: /revision 2/ })
		await screen.findByRole('option', { name: /policy 1/ })
		expect(screen.getByRole('button', { name: '创建风险复核 Job' }).hasAttribute('disabled')).toBe(true)
		await fireEvent.update(screen.getByLabelText('候选 revision'), id)
		await screen.findByText(/resource-balance@v1 \/ metric-resource@v1/)
    await fireEvent.click(screen.getByLabelText('启用 starter'))
    await fireEvent.click(screen.getByLabelText(/我明确确认创建并启用/))
    await fireEvent.update(screen.getByLabelText('Candidate run ID'), id)
    await fireEvent.update(screen.getByLabelText('Candidate result hash'), hash)
    await fireEvent.update(screen.getByLabelText('Baseline run ID'), id2)
    await fireEvent.update(screen.getByLabelText('Baseline result hash'), hashB)
    const submit = screen.getByRole('button', { name: '创建风险复核 Job' })
		expect(submit.hasAttribute('disabled')).toBe(false); submit.focus(); expect(document.activeElement).toBe(submit)
    await fireEvent.click(submit)
    const post = fetchMock.mock.calls.find(([, options]) => options?.method === 'POST') as [string, RequestInit]
    const body = JSON.parse(String(post[1].body)) as { baseline: { type: string; release_id: string }; threshold: { type: string; confirmed: boolean }; simulation_runs: Array<{ run_id: string; result_hash: string }> }
    expect(body.baseline).toMatchObject({ type: 'BASELINE', release_id: id2 })
    expect(body.threshold).toEqual({ type: 'STARTER', confirmed: true })
    expect(body.simulation_runs).toEqual([{ run_id: id, result_hash: hash }, { run_id: id2, result_hash: hashB }])
  })
})
