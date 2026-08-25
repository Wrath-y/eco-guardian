import { expect, test, type Page, type Route } from '@playwright/test'

const projectID = '01948c1e-1000-7000-8000-000000000001'
const baseRevisionID = '01948c1e-1000-7000-8000-000000000002'
const acceptedRevisionID = '01948c1e-1000-7000-8000-000000000003'
const skillID = '01948c1e-1000-7000-8000-000000000004'
const effectID = '01948c1e-1000-7000-8000-000000000005'
const patchID = '01948c1e-1000-7000-8000-000000000006'
const hashA = 'a'.repeat(64)
const hashB = 'b'.repeat(64)
const hashC = 'c'.repeat(64)
const identity = { id: 'fixture', version: 'v1', hash: hashA }
const limits = { policy: identity, max_format_repairs: 3, max_provider_turns: 4, max_tool_calls: 6, max_search_candidates: 20, max_duration_millis: 60_000, max_context_bytes: 65_536, max_output_bytes: 65_536, max_tool_result_bytes: 65_536, retrieval_seed_limit: 20, retrieval_result_limit: 20, retrieval_graph_depth: 1 }

type AIState = {
  capability?: 'available' | 'unavailable'
  admissionErrors?: string[]
  terminalError?: { code: string; retryable: boolean; rebuild_required?: boolean }
  running?: boolean
  canceled?: boolean
  blocked?: boolean
  stale?: boolean
  acceptConflict?: boolean
  accepted?: boolean
  createPosts: Array<Record<string, unknown>>
  acceptPosts: Array<Record<string, unknown>>
  releasePosts: Array<Record<string, unknown>>
  settingsPosts: Array<Record<string, unknown>>
  credentialBodies: string[]
}

function revision(id: string, display: number) {
  return { id, display_revision: display, config_hash: id === baseRevisionID ? hashA : hashB, metadata: { revision_id: id, name: id === baseRevisionID ? 'AI base' : 'AI accepted', created_at: '2026-08-25T00:00:00Z' }, status: ['history'], timeline: [] }
}

function capability(state: AIState) {
  const available = state.capability !== 'unavailable'
  return { state: available ? 'available' : 'unavailable', enabled: true, endpoint_classification: 'loopback', credential_present: true, structured_output: available, tool_calls: available, streaming: available, reasons: available ? [] : ['PROVIDER_UNCONFIGURED'], prompt: identity, draft_patch_schema: identity, tools: [identity], orchestrator: identity, budget: identity, limits }
}

function settings() {
  return { schema_version: 1, ai: { enabled: true, endpoint: 'http://127.0.0.1:11434/v1', model: 'fixture-model', request_timeout_seconds: 60, allow_cloud: false, endpoint_classification: 'loopback', credential_present: true } }
}

function job(id: string, state: AIState) {
  const status = state.canceled ? 'canceled' : state.running ? 'running' : state.terminalError ? 'failed' : 'succeeded'
  return {
    id, kind: 'ai_design', revision_id: baseRevisionID, status, request_hash: hashA,
    events_url: `/api/v1/jobs/${id}/events`, poll_after_ms: 100,
    ...(status === 'succeeded' ? { result_type: 'draft_patch', result_id: patchID, result_url: `/api/v1/draft-patches/${patchID}` } : {}),
    created_at: '2026-08-25T00:00:00Z', updated_at: '2026-08-25T00:00:01Z', phase: status === 'succeeded' ? 'patch_sealed' : 'provider/tool_loop',
  }
}

function draftPatch(state: AIState) {
  const accepted = Boolean(state.accepted)
  return {
    id: patchID, patch_hash: hashB, schema: { id: 'eco.ai.draft-patch', version: 'v1', hash: hashA }, base_revision_id: baseRevisionID, input_hash: hashC,
    evidence_manifest: { id: 'eco.ai.evidence-manifest', version: 'v1', hash: hashB },
    targets: [
      { entity_id: skillID, kind: 'skill', expected_entity_version: 7, operations: [{ ordinal: 1, kind: 'replace', path: '/payload/cooldown', value: '8', evidence: ['evidence-bm25'] }] },
      { entity_id: effectID, kind: 'effect', expected_entity_version: 4, operations: [{ ordinal: 2, kind: 'replace', path: '/payload/duration', value: '12', evidence: ['evidence-vector'] }] },
    ],
    rationale: 'AI proposes a bounded cooldown and duration adjustment.', assumptions: ['Fixed 30 second scene.', 'No identity fields change.'],
    attempts: [{ id: '01948c1e-1000-7000-8000-000000000020', ordinal: 1, stage: 'patch_sealed', outcome: 'succeeded', manifest: { id: 'eco.ai.attempt', version: 'v1', hash: hashC }, repair_round: 0 }],
    preview: {
      advisory: true, input_hash: hashC, result_hash: hashA,
      evaluators: [
        { id: 'eco.validation.full', version: 'v1', hash: hashA },
        { id: 'eco.simulation.fixed-scene', version: 'v1', hash: hashB },
        { id: 'eco.risk.comparison', version: 'v1', hash: hashC },
      ],
      acceptable: !state.blocked, issues: state.blocked ? ['STATIC_FORMULA_CYCLE · deterministic BLOCK'] : [],
      evidence: [
        { id: 'evidence-bm25', kind: 'retrieval', manifest_hash: hashA, citation: 'Pinned skill citation.', mode: 'bm25_only', degraded: true, generations: [{ id: 'fts5', version: 'unicode61', hash: hashA }], scores: { bm25: '1.75' }, warnings: ['VECTOR_UNAVAILABLE'] },
        { id: 'evidence-vector', kind: 'retrieval', manifest_hash: hashB, citation: 'Pinned effect citation.', mode: 'vector_only', degraded: true, generations: [{ id: 'embedding', version: 'fixture-v1', hash: hashB }], scores: { vector: '0.91', rerank: '0.88' }, warnings: ['BM25_UNAVAILABLE', 'RERANK_DEGRADED'] },
      ],
    },
    freshness: { state: state.stale ? 'stale' : 'fresh', conflicting_targets: state.stale ? [effectID] : [] },
    decision: accepted ? { kind: 'accepted', actor: 'local_user', decided_at: '2026-08-25T00:00:02Z', accepted_revision_id: acceptedRevisionID } : null,
    links: {
      self: `/api/v1/draft-patches/${patchID}`, job: '/api/v1/jobs/ai-job-1', accept: `/api/v1/draft-patches/${patchID}/accept`, discard: `/api/v1/draft-patches/${patchID}/discard`,
      formal_validation: accepted ? `/api/v1/revisions/${acceptedRevisionID}/validation` : null,
      formal_graph: accepted ? `/api/v1/revisions/${acceptedRevisionID}/graph-status` : null,
      formal_simulation: accepted ? `/api/v1/simulation-runs?revision_id=${acceptedRevisionID}` : null,
      formal_risk: accepted ? `/api/v1/risk-reviews?revision_id=${acceptedRevisionID}` : null,
    },
    created_at: '2026-08-25T00:00:00Z',
  }
}

async function fulfillSSE(route: Route, id: string, state: AIState) {
  const currentJob = job(id, state)
  const events: string[] = []
  if (state.canceled) {
    events.push(`id: 2\nevent: terminal\ndata: ${JSON.stringify(currentJob)}\n`)
  } else if (state.running) {
    events.push(`id: 1\nevent: job\ndata: ${JSON.stringify({ job_id: id, ordinal: 1, kind: 'ignored_late_result', phase: 'provider/tool_loop', progress: 50, warning_code: 'IGNORED_LATE_RESULT', warning_ref: 'cancel-generation-1', created_at: '2026-08-25T00:00:01Z' })}\n`)
  } else if (state.terminalError) {
    events.push(`id: 1\nevent: job\ndata: ${JSON.stringify({ job_id: id, ordinal: 1, kind: 'terminal', phase: 'provider/tool_loop', progress: 50, outcome: 'failed', error: state.terminalError, created_at: '2026-08-25T00:00:01Z' })}\n`)
    events.push(`id: 2\nevent: terminal\ndata: ${JSON.stringify(currentJob)}\n`)
  } else {
    events.push(`id: 1\nevent: terminal\ndata: ${JSON.stringify(currentJob)}\n`)
  }
  await route.fulfill({ status: 200, headers: { 'Content-Type': 'text/event-stream', 'Cache-Control': 'no-cache' }, body: `${events.join('\n')}\n` })
}

async function mockAI(page: Page, state: AIState) {
  await page.route('**/api/v1/**', async route => {
    const request = route.request()
    const path = new URL(request.url()).pathname
    const json = (body: unknown, status = 200) => route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) })
    if (path.endsWith('/projects/current')) return json({ id: projectID, name: 'ai-e2e', db_schema_version: 20 })
    if (path.endsWith('/projects/recent')) return json([])
    if (path.endsWith('/runtime/status')) return json({ ai: capability(state) })
    if (path.endsWith('/settings') && request.method() === 'GET') return json(settings())
    if (path.endsWith('/settings') && request.method() === 'PATCH') { state.settingsPosts.push(request.postDataJSON() as Record<string, unknown>); return json(settings()) }
    if (path.endsWith('/settings/credentials/openai-compatible') && request.method() === 'PUT') { state.credentialBodies.push(request.postData() || ''); return json({ provider: 'openai-compatible', credential_present: true, source: 'credential_manager' }) }
    if (path.endsWith('/revisions') && request.method() === 'GET') return json({ items: state.accepted ? [revision(acceptedRevisionID, 8), revision(baseRevisionID, 7)] : [revision(baseRevisionID, 7)] })
    if (path.endsWith('/entities/skill')) return json({ items: [{ id: skillID, name: 'Opening Strike', key: 'opening_strike', entity_version: 7 }] })
    if (path.endsWith('/entities/effect')) return json({ items: [{ id: effectID, name: 'Burning Mark', key: 'burning_mark', entity_version: 4 }] })
    if (path.endsWith(`/entities/skill/${skillID}`)) return json({ id: skillID, kind: 'skill', entity_version: 7, payload: { cooldown: '10' } })
    if (path.endsWith(`/entities/effect/${effectID}`)) return json({ id: effectID, kind: 'effect', entity_version: 4, payload: { duration: '10' } })
    if (path.endsWith('/ai-design-jobs') && request.method() === 'POST') {
      state.createPosts.push(request.postDataJSON() as Record<string, unknown>)
      const error = state.admissionErrors?.shift()
      if (error) return json({ title: 'Frozen identity rejected', code: error, retryable: false }, 409)
      const id = `ai-job-${state.createPosts.length}`
      return json({ job: job(id, state), location: `/api/v1/jobs/${id}` }, 202)
    }
    const eventMatch = path.match(/\/jobs\/(ai-job-\d+)\/events$/)
    if (eventMatch) return fulfillSSE(route, eventMatch[1], state)
    const cancelMatch = path.match(/\/jobs\/(ai-job-\d+)\/cancel$/)
    if (cancelMatch && request.method() === 'POST') { state.running = false; state.canceled = true; return json(job(cancelMatch[1], state)) }
    const jobMatch = path.match(/\/jobs\/(ai-job-\d+)$/)
    if (jobMatch) return json(job(jobMatch[1], state))
    if (path.endsWith(`/draft-patches/${patchID}`)) return json(draftPatch(state))
    if (path.endsWith(`/draft-patches/${patchID}/accept`) && request.method() === 'POST') {
      state.acceptPosts.push(request.postDataJSON() as Record<string, unknown>)
      if (state.acceptConflict) return json({ title: 'Revision conflict', code: 'REVISION_CONFLICT', retryable: false }, 409)
      state.accepted = true
      return json({ patch_id: patchID, revision_id: acceptedRevisionID, replay: false, published: false })
    }
    if (path.endsWith('/releases') && request.method() === 'POST') { state.releasePosts.push(request.postDataJSON() as Record<string, unknown>); return json({}, 202) }
    return json({}, 404)
  })
}

function newState(overrides: Partial<AIState> = {}): AIState {
  return { createPosts: [], acceptPosts: [], releasePosts: [], settingsPosts: [], credentialBodies: [], ...overrides }
}

async function fillFrozenInput(page: Page, multiEntity = false) {
  await page.getByLabel('Base revision').selectOption(baseRevisionID)
  await page.getByLabel('目标说明').fill('Reduce burst damage while retaining deterministic safety.')
  await page.getByRole('combobox', { name: '目标 1 entity' }).selectOption(skillID)
  if (multiEntity) {
    await page.getByRole('button', { name: '添加目标' }).click()
    const second = page.locator('.target-input').nth(1)
    await second.getByLabel('Kind').selectOption('effect')
    await second.getByRole('combobox', { name: '目标 2 entity' }).selectOption(effectID)
  }
}

test('primary AI design flow pins evidence, accepts one multi-entity revision and never publishes implicitly', async ({ page }) => {
  const state = newState()
  await mockAI(page, state)
  await page.goto('/ai-design')

  await page.getByLabel('OpenAI-compatible endpoint').fill('http://127.0.0.1:11434/v1')
  await page.getByLabel('Model').fill('fixture-model')
  await page.getByRole('button', { name: '保存非敏感设置' }).click()
  await expect(page.getByText(/非敏感 Provider 设置已保存/)).toBeVisible()
  await page.getByLabel('新凭据（仅写入）').fill('credential-canary')
  await page.getByRole('button', { name: '设置/替换凭据' }).click()
  await expect(page.getByLabel('新凭据（仅写入）')).toHaveValue('')
  await page.getByRole('button', { name: '测试 Provider 能力' }).click()
  await expect(page.getByText(/能力测试完成：available/)).toBeVisible()

  await fillFrozenInput(page, true)
  await page.getByRole('button', { name: '生成 DraftPatch 候选' }).click()
  await expect(page.getByRole('heading', { name: 'DraftPatch 候选审阅' })).toBeVisible()
  await expect(page.getByText(/skill .*期望 entity_version 7/)).toBeVisible()
  await expect(page.getByText(/effect .*期望 entity_version 4/)).toBeVisible()
  await expect(page.getByText('Pinned skill citation.')).toBeVisible()
  await expect(page.getByText('Pinned effect citation.')).toBeVisible()
  await expect(page.getByText(/bm25_only/)).toBeVisible()
  await expect(page.getByText(/vector_only/)).toBeVisible()
  await expect(page.getByText(/VECTOR_UNAVAILABLE/)).toBeVisible()
  await expect(page.getByText(/BM25_UNAVAILABLE/)).toBeVisible()
  await expect(page.getByText(/RERANK_DEGRADED/)).toBeVisible()
  await expect(page.getByText(/eco.validation.full@v1/)).toBeVisible()
  await expect(page.getByText(/eco.simulation.fixed-scene@v1/)).toBeVisible()
  await expect(page.getByText(/eco.risk.comparison@v1/)).toBeVisible()
  await expect(page.getByText(/AI proposes a bounded/)).toBeVisible()
  await expect(page.getByText(/Fixed 30 second scene/)).toBeVisible()

  await page.getByRole('button', { name: '接受并创建 revision（不发布）' }).click()
  await expect(page.getByText(new RegExp(`已创建 revision ${acceptedRevisionID}`))).toBeVisible()
  await expect(page.getByRole('link', { name: '打开' })).toHaveCount(4)
  await expect(page.getByLabel('Base revision').locator(`option[value="${acceptedRevisionID}"]`)).toHaveCount(1)
  expect(state.acceptPosts).toHaveLength(1)
  expect(state.releasePosts).toHaveLength(0)
  expect(state.createPosts[0]).toMatchObject({ base_revision_id: baseRevisionID, allowed_targets: [{ entity_id: skillID, expected_entity_version: 7 }, { entity_id: effectID, expected_entity_version: 4 }] })
  expect(state.credentialBodies[0]).toContain('credential-canary')
  await expect(page.locator('body')).not.toContainText('credential-canary')
  await expect(page.getByText(/发布仍需在版本页单独人工确认/)).toBeVisible()
})

test('unconfigured Provider disables generation and frozen Snapshot/hash admission failures preserve input', async ({ page }) => {
  const state = newState({ capability: 'unavailable', admissionErrors: ['AI_SNAPSHOT_IDENTITY_MISMATCH', 'AI_SNAPSHOT_HASH_MISMATCH'] })
  await mockAI(page, state)
  await page.goto('/ai-design')
  await fillFrozenInput(page)
  await expect(page.getByText(/Provider 状态 unavailable/)).toBeVisible()
  await expect(page.getByRole('button', { name: '生成 DraftPatch 候选' })).toBeDisabled()
  expect(state.createPosts).toHaveLength(0)

  state.capability = 'available'
  await page.getByRole('button', { name: '测试 Provider 能力' }).click()
  for (const code of ['AI_SNAPSHOT_IDENTITY_MISMATCH', 'AI_SNAPSHOT_HASH_MISMATCH']) {
    await page.getByRole('button', { name: '生成 DraftPatch 候选' }).click()
    await expect(page.getByText(new RegExp(code))).toBeVisible()
    await expect(page.getByLabel('目标说明')).toHaveValue('Reduce burst damage while retaining deterministic safety.')
  }
})

for (const failure of [
  { code: 'AI_EVIDENCE_UNAVAILABLE', text: '没有可确认的检索证据' },
  { code: 'AI_SNAPSHOT_INDEX_NOT_READY', text: '显式重建冻结 Snapshot' },
  { code: 'AI_OUTPUT_INVALID', text: '不符合 DraftPatch 契约' },
  { code: 'AI_REPAIR_EXHAUSTED', text: '三轮格式修复预算已用尽' },
  { code: 'AI_TOOL_POLICY_VIOLATION', text: '未授权工具、目标或字段' },
  { code: 'AI_BUDGET_EXCEEDED', text: '固定工具、搜索、时间或输出预算已用尽' },
]) {
  test(`terminal ${failure.code} exposes only actionable committed failure`, async ({ page }) => {
    const state = newState({ terminalError: { code: failure.code, retryable: false, rebuild_required: failure.code === 'AI_SNAPSHOT_INDEX_NOT_READY' } })
    await mockAI(page, state)
    await page.goto('/ai-design')
    await fillFrozenInput(page)
    await page.getByRole('button', { name: '生成 DraftPatch 候选' }).click()
    await expect(page.getByText(new RegExp(failure.text))).toBeVisible()
    await expect(page.getByRole('heading', { name: 'DraftPatch 候选审阅' })).toHaveCount(0)
    if (failure.code === 'AI_SNAPSHOT_INDEX_NOT_READY') await expect(page.getByText(/AI 页面不会自动触发重建/)).toBeVisible()
  })
}

test('deterministic BLOCK, stale target and accept conflict remain server-authoritative', async ({ page }) => {
  const state = newState({ blocked: true })
  await mockAI(page, state)
  await page.goto('/ai-design')
  await fillFrozenInput(page)
  await page.getByRole('button', { name: '生成 DraftPatch 候选' }).click()
  await expect(page.getByText(/STATIC_FORMULA_CYCLE · deterministic BLOCK/)).toBeVisible()
  await expect(page.getByRole('button', { name: '接受并创建 revision（不发布）' })).toBeDisabled()

  state.blocked = false
  state.stale = true
  await page.reload()
  await fillFrozenInput(page)
  await page.getByRole('button', { name: '生成 DraftPatch 候选' }).click()
  await expect(page.getByText(/候选已 stale/)).toBeVisible()
  await expect(page.getByRole('button', { name: '接受并创建 revision（不发布）' })).toBeDisabled()

  state.stale = false
  state.acceptConflict = true
  await page.reload()
  await fillFrozenInput(page)
  await page.getByRole('button', { name: '生成 DraftPatch 候选' }).click()
  await page.getByRole('button', { name: '接受并创建 revision（不发布）' }).click()
  await expect(page.getByText(/本页输入与候选保持不变/)).toBeVisible()
  await expect(page.getByText(/AI proposes a bounded/)).toBeVisible()
  expect(state.releasePosts).toHaveLength(0)
})

test('cancel ignores a late response and a retryable failure requires explicit retry', async ({ page }) => {
  const state = newState({ running: true })
  await mockAI(page, state)
  await page.goto('/ai-design')
  await fillFrozenInput(page)
  await page.getByRole('button', { name: '生成 DraftPatch 候选' }).click()
  await expect(page.getByText(/ignored_late_result/)).toBeVisible()
  await page.getByRole('button', { name: '取消 AI Job' }).click()
  await expect(page.getByText(/晚到结果都不能密封 DraftPatch/)).toBeVisible()
  await expect(page.getByRole('heading', { name: 'DraftPatch 候选审阅' })).toHaveCount(0)

  state.terminalError = { code: 'AI_PROVIDER_TIMEOUT', retryable: true }
  state.canceled = false
  await page.reload()
  await fillFrozenInput(page)
  await page.getByRole('button', { name: '生成 DraftPatch 候选' }).click()
  await expect(page.getByText(/AI_PROVIDER_TIMEOUT：这是可重试失败/)).toBeVisible()
  expect(state.createPosts).toHaveLength(2)
  await page.getByRole('button', { name: '确认并显式创建新尝试' }).click()
  await expect.poll(() => state.createPosts.length).toBe(3)
  await expect(page.getByText(/AI_PROVIDER_TIMEOUT：这是可重试失败/)).toBeVisible()
})
