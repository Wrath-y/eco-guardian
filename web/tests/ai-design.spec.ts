import { fireEvent, render, screen, waitFor } from '@testing-library/vue'
import { VueQueryPlugin } from '@tanstack/vue-query'
import { createPinia } from 'pinia'
import { createMemoryHistory, createRouter } from 'vue-router'
import { describe, expect, it, vi } from 'vitest'
import AIDesignView from '../src/features/ai-design/AIDesignView.vue'
import { useProjectStore } from '../src/stores/project'

const projectID = '01948c1e-0000-7000-8000-000000000400'
const revisionID = '01948c1e-0000-7000-8000-000000000401'
const entityID = '01948c1e-0000-7000-8000-000000000402'
const hash = 'a'.repeat(64)
const identity = { id: 'fixture', version: 'v1', hash }
const limits = { policy: identity, max_format_repairs: 3, max_provider_turns: 4, max_tool_calls: 6, max_search_candidates: 20, max_duration_millis: 60000, max_context_bytes: 1000, max_output_bytes: 1000, max_tool_result_bytes: 1000, retrieval_seed_limit: 20, retrieval_result_limit: 20, retrieval_graph_depth: 1 }
const capability = { state: 'available', enabled: true, endpoint_classification: 'loopback', credential_present: true, structured_output: true, tool_calls: true, streaming: true, reasons: [], prompt: identity, draft_patch_schema: identity, tools: [identity], orchestrator: identity, budget: identity, limits }

describe('AI design admission', () => {
  it('selects frozen scope from server resources and preserves it on authoritative error', async () => {
    const fetchMock = vi.fn((url: string, init?: RequestInit) => {
      if (url.endsWith('/runtime/capabilities')) return Promise.resolve(new Response(JSON.stringify({ ai: capability }), { status: 200 }))
      if (url.endsWith('/settings')) return Promise.resolve(new Response(JSON.stringify({ schema_version: 1, ai: { enabled: true, endpoint: 'http://127.0.0.1/v1', model: 'fixture', request_timeout_seconds: 60, allow_cloud: false, endpoint_classification: 'loopback', credential_present: true } }), { status: 200 }))
      if (url.endsWith('/revisions')) return Promise.resolve(new Response(JSON.stringify({ items: [{ id: revisionID, display_revision: 7, config_hash: hash, metadata: { name: 'candidate' }, status: ['history'] }] }), { status: 200 }))
      if (url.includes('/entities/skill?')) return Promise.resolve(new Response(JSON.stringify({ items: [{ id: entityID, name: 'Opening Strike', key: 'opening_strike', entity_version: 9 }] }), { status: 200 }))
      if (url.endsWith('/ai-design-jobs') && init?.method === 'POST') return Promise.resolve(new Response(JSON.stringify({ title: 'Snapshot identity changed', code: 'AI_SNAPSHOT_IDENTITY_MISMATCH', retryable: false }), { status: 409 }))
      return Promise.resolve(new Response('', { status: 404 }))
    })
    vi.stubGlobal('fetch', fetchMock)
    const pinia = createPinia(); useProjectStore(pinia).current = { id: projectID, name: 'fixture', db_schema_version: 20 }
    const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/', component: AIDesignView }, { path: '/projects', component: { template: '<p>projects</p>' } }, { path: '/versions', component: { template: '<p>versions</p>' } }, { path: '/simulations', component: { template: '<p>simulations</p>' } }, { path: '/risk-reviews', component: { template: '<p>risk</p>' } }] })
    await router.push('/'); await router.isReady()
    render(AIDesignView, { global: { plugins: [pinia, VueQueryPlugin, router] } })
		const goal = await screen.findByLabelText('目标说明') as HTMLTextAreaElement
		await fireEvent.update(goal, 'Reduce burst damage without changing identity.')
		await fireEvent.update(screen.getByLabelText('Base revision'), revisionID)
		await screen.findByRole('option', { name: /Opening Strike/ })
		await fireEvent.update(await screen.findByRole('combobox', { name: '目标 1 entity' }), entityID)
    const submit = screen.getByRole('button', { name: '生成 DraftPatch 候选' }) as HTMLButtonElement
    await waitFor(() => expect(submit.disabled).toBe(false))
    await fireEvent.click(submit)
    expect(await screen.findByText(/AI_SNAPSHOT_IDENTITY_MISMATCH/)).toBeTruthy()
    expect(goal.value).toBe('Reduce burst damage without changing identity.')
    expect(document.activeElement?.getAttribute('role')).toBe('alert')
    const call = fetchMock.mock.calls.find(([url]) => String(url).endsWith('/ai-design-jobs'))
    const body = JSON.parse(String(call?.[1]?.body))
    expect(body.base_revision_id).toBe(revisionID)
    expect(body.allowed_targets[0]).toMatchObject({ entity_id: entityID, expected_entity_version: 9, kind: 'skill' })
  })
})
