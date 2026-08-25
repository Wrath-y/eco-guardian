import { fireEvent, render, screen, waitFor } from '@testing-library/vue'
import { VueQueryPlugin } from '@tanstack/vue-query'
import { describe, expect, it, vi } from 'vitest'
import ProviderSettingsPanel from '../src/features/ai-design/ProviderSettingsPanel.vue'

const projectID = '01948c1e-0000-7000-8000-000000000100'
const hash = 'a'.repeat(64)
const identity = { id: 'fixture', version: 'v1', hash }
const limits = { policy: identity, max_format_repairs: 3, max_provider_turns: 4, max_tool_calls: 6, max_search_candidates: 20, max_duration_millis: 60000, max_context_bytes: 1000, max_output_bytes: 1000, max_tool_result_bytes: 1000, retrieval_seed_limit: 20, retrieval_result_limit: 20, retrieval_graph_depth: 1 }
const settings = { schema_version: 1, ai: { enabled: true, endpoint: 'http://127.0.0.1:11434/v1', model: 'fixture-model', request_timeout_seconds: 60, allow_cloud: false, endpoint_classification: 'loopback', credential_present: false } }
const capability = { state: 'available', enabled: true, endpoint_classification: 'loopback', credential_present: false, structured_output: true, tool_calls: true, streaming: true, reasons: [], prompt: identity, draft_patch_schema: identity, tools: [identity], orchestrator: identity, budget: identity, limits }

describe('Provider settings', () => {
  it('writes credentials once and clears the secret from client state', async () => {
    const fetchMock = vi.fn((url: string, init?: RequestInit) => {
      if (url.endsWith('/settings/credentials/openai-compatible') && init?.method === 'PUT') return Promise.resolve(new Response(JSON.stringify({ provider: 'openai-compatible', credential_present: true, source: 'credential_manager' }), { status: 200 }))
      if (url.endsWith('/runtime/status')) return Promise.resolve(new Response(JSON.stringify({ ai: capability }), { status: 200 }))
      if (url.endsWith('/settings')) return Promise.resolve(new Response(JSON.stringify(settings), { status: 200 }))
      return Promise.resolve(new Response('', { status: 404 }))
    })
    vi.stubGlobal('fetch', fetchMock)
    render(ProviderSettingsPanel, { props: { projectID }, global: { plugins: [VueQueryPlugin] } })
    const input = await screen.findByLabelText('新凭据（仅写入）') as HTMLInputElement
    await fireEvent.update(input, 'credential-canary')
    await fireEvent.click(screen.getByRole('button', { name: '设置/替换凭据' }))
    await screen.findByText(/页面不保留其内容/)
    expect(input.value).toBe('')
    expect(document.body.textContent).not.toContain('credential-canary')
    await waitFor(() => expect(fetchMock.mock.calls.some(([url, init]) => String(url).endsWith('/settings/credentials/openai-compatible') && String(init?.body).includes('credential-canary'))).toBe(true))
  })

  it('shows explicit cloud disclosure context', async () => {
    vi.stubGlobal('fetch', vi.fn((url: string) => Promise.resolve(new Response(JSON.stringify(url.endsWith('/runtime/status') ? { ai: capability } : settings), { status: 200 }))))
    render(ProviderSettingsPanel, { props: { projectID }, global: { plugins: [VueQueryPlugin] } })
    await fireEvent.click(await screen.findByLabelText('明确允许 cloud endpoint'))
    expect(screen.getByText(/冻结目标、目标\/约束及有界证据 citation/)).toBeTruthy()
  })
})
