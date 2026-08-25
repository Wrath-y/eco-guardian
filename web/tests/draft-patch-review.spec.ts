import { fireEvent, render, screen, waitFor } from '@testing-library/vue'
import { VueQueryPlugin } from '@tanstack/vue-query'
import { describe, expect, it, vi } from 'vitest'
import DraftPatchReview from '../src/features/ai-design/DraftPatchReview.vue'

const projectID = '01948c1e-0000-7000-8000-000000000200'
const patchID = '01948c1e-0000-7000-8000-000000000201'
const targetID = '01948c1e-0000-7000-8000-000000000202'
const baseID = '01948c1e-0000-7000-8000-000000000203'
const attemptID = '01948c1e-0000-7000-8000-000000000204'
const hash = 'a'.repeat(64)
const identity = { id: 'fixture', version: 'v1', hash }
const patch = {
  id: patchID, patch_hash: hash, schema: identity, base_revision_id: baseID, input_hash: 'b'.repeat(64), evidence_manifest: identity,
  targets: [{ entity_id: targetID, kind: 'skill', expected_entity_version: 7, operations: [{ ordinal: 1, kind: 'replace', path: '/payload/cooldown', value: '8', evidence: ['evidence-1'] }] }],
  rationale: 'AI-generated rationale.', assumptions: ['AI-generated assumption.'], attempts: [{ id: attemptID, ordinal: 1, stage: 'patch_sealed', outcome: 'succeeded', manifest: identity, repair_round: 0 }],
  preview: { advisory: true, input_hash: 'b'.repeat(64), result_hash: 'c'.repeat(64), evaluators: [identity], acceptable: true, issues: [], evidence: [{ id: 'evidence-1', kind: 'retrieval', manifest_hash: hash, citation: 'Immutable graph citation.', mode: 'bm25_only', degraded: true, generations: [identity], scores: { bm25: '1.5' }, warnings: ['VECTOR_UNAVAILABLE'] }] },
  freshness: { state: 'fresh', conflicting_targets: [] }, decision: null,
  links: { self: `/api/v1/draft-patches/${patchID}`, job: '/api/v1/jobs/job', accept: `/api/v1/draft-patches/${patchID}/accept`, discard: `/api/v1/draft-patches/${patchID}/discard`, formal_validation: null, formal_graph: null, formal_simulation: null, formal_risk: null }, created_at: '2026-08-25T00:00:00Z',
}

describe('DraftPatch review', () => {
  it('renders textual original/canonical evidence and preserves review on accept conflict', async () => {
    const fetchMock = vi.fn((url: string, init?: RequestInit) => {
      if (url.endsWith(`/draft-patches/${patchID}`) && !init?.method) return Promise.resolve(new Response(JSON.stringify(patch), { status: 200 }))
      if (url.endsWith(`/entities/skill/${targetID}`)) return Promise.resolve(new Response(JSON.stringify({ id: targetID, kind: 'skill', entity_version: 7, payload: { cooldown: '10' } }), { status: 200 }))
      if (url.endsWith(`/draft-patches/${patchID}/accept`)) return Promise.resolve(new Response(JSON.stringify({ title: 'Revision conflict', code: 'REVISION_CONFLICT', retryable: false }), { status: 409 }))
      return Promise.resolve(new Response('', { status: 404 }))
    })
    vi.stubGlobal('fetch', fetchMock)
    render(DraftPatchReview, { props: { projectID, patchID }, global: { plugins: [VueQueryPlugin] } })
		expect(await screen.findByText(/Immutable graph citation/)).toBeTruthy()
    expect(screen.getByText(/bm25_only/)).toBeTruthy()
    expect(screen.getByText(/VECTOR_UNAVAILABLE/)).toBeTruthy()
    expect(screen.getByText('AI-generated rationale.')).toBeTruthy()
    await waitFor(() => expect(screen.getByText('"10"')).toBeTruthy())
    await fireEvent.click(screen.getByRole('button', { name: '接受并创建 revision（不发布）' }))
    expect(await screen.findByText(/本页输入与候选保持不变/)).toBeTruthy()
    expect(document.activeElement?.getAttribute('role')).toBe('alert')
    expect(screen.getByText('AI-generated rationale.')).toBeTruthy()
    const call = fetchMock.mock.calls.find(([url]) => String(url).endsWith(`/draft-patches/${patchID}/accept`))
    expect(JSON.parse(String(call?.[1]?.body))).toEqual({ patch_hash: hash, base_revision_id: baseID, targets: [{ entity_id: targetID, expected_entity_version: 7 }] })
  })

	it('disables accept for stale server-authoritative freshness', async () => {
    vi.stubGlobal('fetch', vi.fn((url: string) => Promise.resolve(new Response(JSON.stringify(url.includes('/entities/') ? { payload: { cooldown: '10' } } : { ...patch, freshness: { state: 'stale', conflicting_targets: [targetID] } }), { status: 200 }))))
    render(DraftPatchReview, { props: { projectID, patchID }, global: { plugins: [VueQueryPlugin] } })
    const button = await screen.findByRole('button', { name: '接受并创建 revision（不发布）' }) as HTMLButtonElement
    expect(button.disabled).toBe(true)
		expect(screen.getByText(/候选已 stale/)).toBeTruthy()
	})

	it('keeps textual alternatives and keyboard focus at a 1024px desktop viewport', async () => {
		Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1024 })
		window.dispatchEvent(new Event('resize'))
		vi.stubGlobal('fetch', vi.fn((url: string) => Promise.resolve(new Response(JSON.stringify(url.includes('/entities/') ? { payload: { cooldown: '10' } } : patch), { status: 200 }))))
		render(DraftPatchReview, { props: { projectID, patchID }, global: { plugins: [VueQueryPlugin] } })
		expect(await screen.findByText(/状态：pending \/ acceptable/)).toBeTruthy()
		expect(screen.getByText(/目标 .* 的逐项差异/)).toBeTruthy()
		expect(screen.getByText(/事实证据与 advisory 反馈/)).toBeTruthy()
		const accept = screen.getByRole('button', { name: '接受并创建 revision（不发布）' }) as HTMLButtonElement
		accept.focus()
		expect(document.activeElement).toBe(accept)
	})
})
