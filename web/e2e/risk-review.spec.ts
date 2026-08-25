import { expect, test, type Page } from '@playwright/test'

const projectID = '01948c1e-0000-7000-8000-000000000100'
const candidateID = '01948c1e-0000-7000-8000-000000000101'
const baselineRevisionID = '01948c1e-0000-7000-8000-000000000102'
const baselineReleaseID = '01948c1e-0000-7000-8000-000000000103'
const candidateRunID = '01948c1e-0000-7000-8000-000000000104'
const baselineRunID = '01948c1e-0000-7000-8000-000000000105'
const riskJobID = '01948c1e-0000-7000-8000-000000000106'
const calculationID = '01948c1e-0000-7000-8000-000000000107'
const decisionJobID = '01948c1e-0000-7000-8000-000000000108'
const decisionID = '01948c1e-0000-7000-8000-000000000109'
const releaseJobID = '01948c1e-0000-7000-8000-000000000110'
const committedReleaseID = '01948c1e-0000-7000-8000-000000000111'
const policyID = '01948c1e-0000-7000-8000-000000000112'
const hashA = 'a'.repeat(64); const hashB = 'b'.repeat(64); const hashC = 'c'.repeat(64)
type MockState = { firstRelease: boolean; riskPosts: Array<Record<string, unknown>>; releasePosts: Array<Record<string, unknown>>; cancelRequested?: boolean; running?: boolean }
type E2EItem = { id: string; ordinal: number; severity: string | null; [key: string]: unknown }

function revision(id: string, configHash: string, manifestHash: string) { return { id, display_revision: id === candidateID ? 2 : 1, config_hash: configHash, metadata: { revision_id: id, config_hash: configHash, version_manifest: { entries: [{ component: 'risk', version: 'v1', manifest_hash: manifestHash }], hash: manifestHash }, created_at: '2026-08-24T00:00:00Z' }, status: id === candidateID ? ['candidate'] : ['active_release', 'history'], timeline: [] } }
function threshold() { return { identity: { id: 'threshold-v1', version: '1', hash: hashC }, enabled: true, source: 'modified_starter', body_hash: hashC, assumptions: ['metric-resource@v1 target_range [0.8, 1.2] inclusive'], entries: [{ scene_id: 'resource-balance', scene_version: 'v1', metric_id: 'metric-resource', metric_version: 'v1', balance_group: 'starter', unit: 'ratio', direction: 'target_range', relative_warning: '0.1', relative_block: '0.25', absolute_warning: null, absolute_block: null }] } }
function metricItem(overridable = false, status: 'COMPARABLE' | 'NOT_COMPARABLE' | 'UNAVAILABLE' = 'COMPARABLE', role: 'required' | 'optional' = 'required') {
  return { id: `metric-resource-${role}-${status}`, ordinal: 0, kind: 'metric', role, comparison_status: status, severity: status === 'COMPARABLE' ? (overridable ? 'BLOCK' : 'WARNING') : null, reason: status === 'COMPARABLE' ? null : status === 'NOT_COMPARABLE' ? 'ZERO_BASELINE' : 'METRIC_UNAVAILABLE', rule: { id: 'risk-relative', version: 'v1', hash: hashA }, evidence_hash: hashB, item_hash: hashC, overridable, override_classification: overridable ? 'NUMERIC_ELIGIBLE' : 'NON_OVERRIDABLE', structural_evidence: null, metric_evidence: { scene_id: 'resource-balance', scene_version: 'v1', metric_id: 'metric-resource', metric_version: 'v1', unit: 'ratio', direction: 'target_range', target_range: { lower: '0.8', upper: '1.2', bounds: 'inclusive' }, absolute_threshold: null, subject: { type: 'cohort', entity_kind: 'character', balance_group: 'starter', stable_id: null, members: [candidateID, baselineRevisionID] }, candidate: { status: 'AVAILABLE', value: '1.3', confidence_low: '1.2', confidence_high: '1.4', unavailable: null, run_id: candidateRunID, result_hash: hashA }, baseline: { status: status === 'UNAVAILABLE' ? 'UNAVAILABLE' : 'AVAILABLE', value: status === 'UNAVAILABLE' ? null : '1', confidence_low: status === 'UNAVAILABLE' ? null : '0.9', confidence_high: status === 'UNAVAILABLE' ? null : '1.1', unavailable: status === 'UNAVAILABLE' ? { code: 'METRIC_UNAVAILABLE', missing: ['baseline.value'], message: 'Metric unavailable' } : null, run_id: baselineRunID, result_hash: hashB }, signed_delta: status === 'COMPARABLE' ? '0.3' : null, risk_delta: status === 'COMPARABLE' ? '0.3' : null, relative_risk: status === 'COMPARABLE' ? '0.3' : null, assumptions: ['fixed seed', 'same cohort'] } }
}
function structuralItem(id: string, ordinal: number): E2EItem {
  return { id, ordinal, kind: 'structural', role: 'required', comparison_status: 'COMPARABLE', severity: 'BLOCK', reason: null, rule: { id, version: 'v1', hash: hashA }, evidence_hash: hashB, item_hash: hashC, overridable: false, override_classification: 'NON_OVERRIDABLE', metric_evidence: null, structural_evidence: { type: 'VALIDATION_ISSUE', rule: { id, version: 'v1', hash: hashA }, entity_id: candidateID, field_path: '/payload/formula', ordinal, fingerprint: hashC } }
}
function report(firstRelease: boolean, kind: 'calculation' | 'decision' = 'calculation', items: E2EItem[] = firstRelease ? [] : [metricItem(true)]) {
  return { id: kind === 'decision' ? decisionID : calculationID, report_kind: kind, schema_version: 'v1', project_id: projectID, candidate: { revision_id: candidateID, config_hash: hashA, version_manifest_hash: hashB }, baseline: firstRelease ? { type: 'NO_BASELINE' } : { type: 'BASELINE', release_id: baselineReleaseID, revision: { revision_id: baselineRevisionID, config_hash: hashB, version_manifest_hash: hashA } }, policy: { id: policyID, version: '1', hash: hashA }, threshold: threshold(), validation: { id: 'validation', version: 'v1', hash: hashA }, implementations: [{ id: 'risk-comparison', version: 'v1', hash: hashA }], simulation_run_ids: firstRelease ? [candidateRunID] : [candidateRunID, baselineRunID], input_hash: hashA, calculation_hash: hashB, explanation_evidence_hash: hashA, report_hash: hashC, source_report_id: kind === 'decision' ? calculationID : null, decision_item_ids: kind === 'decision' ? [metricItem(true).id] : [], decision_reason: kind === 'decision' ? 'Reviewed exact numeric evidence' : null, items: kind === 'decision' ? [] : items, read_time: { freshness: 'FRESH', freshness_reasons: [], gate_state: firstRelease ? 'PASS' : items.some(item => item.severity === 'BLOCK') ? 'BLOCK' : 'WARNING', gate_item_ids: items.map(item => item.id), active_baseline_release_id: firstRelease ? null : baselineReleaseID, release_audit_url: kind === 'decision' ? `/api/v1/releases/${committedReleaseID}` : null, impact_evidence_refs: [] }, created_at: '2026-08-24T00:00:00Z' }
}
function job(id: string, resultID: string, status: 'running' | 'succeeded' | 'canceled' = 'succeeded') { return { id, kind: 'risk_review', revision_id: candidateID, status, request_hash: hashA, events_url: `/api/v1/jobs/${id}/events`, poll_after_ms: 100, ...(status === 'succeeded' ? { result_type: 'risk_review', result_id: resultID, result_url: `/api/v1/risk-reviews/${resultID}` } : {}), created_at: '2026-08-24T00:00:00Z' } }

async function mockRiskAPI(page: Page, state: MockState, customReport?: Record<string, unknown>) {
  await page.route('**/api/v1/**', async route => {
    const request = route.request(); const path = new URL(request.url()).pathname
    const json = (body: unknown, status = 200) => route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) })
    if (path.endsWith('/projects/current')) return json({ id: projectID, name: 'risk-e2e', db_schema_version: 16 })
    if (path.endsWith('/projects/recent')) return json([])
    if (path.endsWith('/revisions') && request.method() === 'GET') return json({ items: [revision(candidateID, hashA, hashB), revision(baselineRevisionID, hashB, hashA)] })
    if (path.endsWith(`/revisions/${candidateID}`)) return json(revision(candidateID, hashA, hashB))
    if (path.endsWith(`/revisions/${baselineRevisionID}`)) return json(revision(baselineRevisionID, hashB, hashA))
    if (path.endsWith('/release-policies')) return json({ items: [{ id: policyID, display_version: 1, policy_hash: hashA, scenes: [{ id: 'resource-balance', scene_version: 'v1', seed: 1, required: true, metrics: [{ id: 'metric-resource', required: true }] }], samples: 100, threshold_id: 'threshold-v1', threshold_enabled: true, capabilities: [{ capability_id: 'risk', gate_id: 'balance-risk', contract_version: 'v1' }], created_at: '2026-08-24T00:00:00Z' }] })
    if (path.endsWith('/runtime/capabilities')) return json({ release: { enabled: true, disabled_reasons: [] }, graph: { available: false, compatible: false, required_capabilities: [], degradations: ['unavailable'], disabled_reasons: [], release_disabled_reasons: [], observed_at: '2026-08-24T00:00:00Z' } })
    if (path.endsWith('/releases') && request.method() === 'GET') return json({ items: state.firstRelease ? [] : [{ id: baselineReleaseID, revision_id: baselineRevisionID, policy_id: policyID, intent_id: releaseJobID, gate_evidence: [], confirmations: [], created_at: '2026-08-24T00:00:00Z' }] })
    if (path.endsWith('/risk-reviews') && request.method() === 'POST') { const command = request.postDataJSON() as Record<string, unknown>; state.riskPosts.push(command); const decision = command.command === 'record_numeric_decision'; const acceptedJob = decision ? job(decisionJobID, decisionID) : state.running ? job(riskJobID, calculationID, 'running') : job(riskJobID, calculationID); return json({ job: acceptedJob, location: `/api/v1/jobs/${acceptedJob.id}` }, 202) }
    if (path.endsWith(`/jobs/${riskJobID}/cancel`) && request.method() === 'POST') { state.cancelRequested = true; state.running = false; return json(job(riskJobID, calculationID, 'canceled'), 202) }
    if (path.endsWith(`/jobs/${riskJobID}`)) return json(state.cancelRequested ? job(riskJobID, calculationID, 'canceled') : state.running ? job(riskJobID, calculationID, 'running') : job(riskJobID, calculationID))
    if (path.endsWith(`/jobs/${decisionJobID}`)) return json(job(decisionJobID, decisionID))
    if (path.endsWith(`/risk-reviews/${decisionID}`)) return json(report(false, 'decision'))
    if (path.includes('/risk-reviews/')) return json(customReport ?? report(state.firstRelease))
    if (path.endsWith('/releases') && request.method() === 'POST') { state.releasePosts.push(request.postDataJSON() as Record<string, unknown>); return json({ job_id: releaseJobID, location: `/api/v1/jobs/${releaseJobID}` }, 202) }
    if (path.endsWith(`/jobs/${releaseJobID}`)) return json({ id: releaseJobID, kind: 'release', revision_id: candidateID, status: 'succeeded', request_hash: hashA, events_url: `/api/v1/jobs/${releaseJobID}/events`, poll_after_ms: 100, result_type: 'release', result_id: committedReleaseID, result_url: `/api/v1/releases/${committedReleaseID}`, created_at: '2026-08-24T00:00:00Z' })
    if (path.endsWith(`/releases/${committedReleaseID}`)) return json({ id: committedReleaseID, revision_id: candidateID, policy_id: policyID, intent_id: releaseJobID, gate_evidence: [], confirmations: state.releasePosts.at(-1)?.confirmations ?? [], created_at: '2026-08-24T00:00:00Z', active_pointer: { release_id: committedReleaseID, generation: 1 } })
    if (path.endsWith('/graph-status')) return json({ revision_id: candidateID, pipeline_state: 'unavailable', freshness: 'unavailable', freshness_reasons: ['GRAPH_UNAVAILABLE'], warnings: [], actions: [], evidence: [] })
    return json({}, 404)
  })
}

async function selectCoreInput(page: Page, firstRelease: boolean) {
  await page.getByLabel('候选 revision').selectOption(candidateID)
  if (firstRelease) await page.getByLabel('明确 NO_BASELINE（首次发布）').check()
  await page.getByLabel('修改 starter').check(); await page.getByLabel(/我明确确认创建并启用/).check(); await page.getByLabel('结构规则 hash').fill(hashA)
  await page.getByLabel('Candidate run ID').fill(candidateRunID); await page.getByLabel('Candidate result hash').fill(hashA)
  if (!firstRelease) { await page.getByLabel('Baseline run ID').fill(baselineRunID); await page.getByLabel('Baseline result hash').fill(hashB) }
}

test('first release explicitly modifies starter, preserves NO_BASELINE and establishes a release audit', async ({ page }) => {
  const state: MockState = { firstRelease: true, riskPosts: [], releasePosts: [] }; await mockRiskAPI(page, state); await page.goto('/risk-reviews'); await selectCoreInput(page, true)
  await page.getByRole('button', { name: '创建风险复核 Job' }).click(); await expect(page.getByText(/baseline：NO_BASELINE/)).toBeVisible(); await expect(page.getByText(/没有伪造差异项/)).toBeVisible(); await expect(page.getByText(/\[0.8, 1.2\] inclusive/).first()).toBeVisible()
  expect(state.riskPosts[0]).toMatchObject({ command: 'evaluate', baseline: { type: 'NO_BASELINE' }, threshold: { type: 'MODIFIED_STARTER', confirmed: true } })
  await page.getByRole('link', { name: /转到版本差异/ }).click(); await expect(page.getByText(/服务器风险摘要/)).toBeVisible(); await page.getByLabel('我确认建立首个正式版本基线').check(); await page.getByRole('button', { name: '提交服务端发布预检' }).click(); await expect(page.getByText(/正式版本指针已确认提交/)).toBeVisible()
  expect(state.releasePosts[0]).toMatchObject({ expected_baseline_release_id: null, confirmations: [{ kind: 'establish_baseline', confirmed: true }] }); expect((state.riskPosts[0].baseline as { type: string }).type).toBe('NO_BASELINE')
})

test('subsequent release inspects exact evidence, creates a decision and requires a second confirmation without changing BLOCK', async ({ page }) => {
  const state: MockState = { firstRelease: false, riskPosts: [], releasePosts: [] }; await mockRiskAPI(page, state); await page.goto('/risk-reviews'); await selectCoreInput(page, false)
  await page.getByRole('button', { name: '创建风险复核 Job' }).click(); await expect(page.getByText(/relative warning 0.1 \/ block 0.25/)).toBeVisible(); await expect(page.getByText(/CI 1.2–1.4/)).toBeVisible(); await expect(page.getByText('BLOCK').first()).toBeVisible()
  await page.getByRole('textbox', { name: '说明' }).fill('Reviewed exact numeric evidence'); await page.getByRole('button', { name: '创建 immutable decision report' }).click(); await expect(page.getByText(/decision/).first()).toBeVisible(); expect(state.riskPosts[1]).toMatchObject({ command: 'record_numeric_decision', source_report_id: calculationID, reason: 'Reviewed exact numeric evidence' })
  await page.getByRole('link', { name: /转到版本差异/ }).click(); await page.getByLabel('预期正式版本 ID').fill(baselineReleaseID); await expect(page.getByLabel('数值风险覆盖说明')).toHaveValue('Reviewed exact numeric evidence'); await page.getByLabel('我确认该数值风险覆盖').check(); await page.getByRole('button', { name: '提交服务端发布预检' }).click()
  expect(state.releasePosts[0]).toMatchObject({ expected_baseline_release_id: baselineReleaseID, confirmations: [{ kind: 'numeric_override', confirmed: true, reason: 'Reviewed exact numeric evidence' }] }); expect((report(false).items[0] as { severity: string }).severity).toBe('BLOCK')
})

test('failure states remain textual, Graph-independent and a canceled Job retries only by explicit action', async ({ page }) => {
  const failureReport = report(false, 'calculation', [metricItem(false, 'NOT_COMPARABLE', 'required'), { ...metricItem(false, 'UNAVAILABLE', 'optional'), ordinal: 1 }, structuralItem('STATIC_FORMULA_CYCLE', 2), structuralItem('EVENT_LOOP_UNBOUNDED', 3), structuralItem('STACK_UNBOUNDED', 4)]); failureReport.read_time = { ...failureReport.read_time, freshness: 'STALE', freshness_reasons: ['THRESHOLD_CHANGED'], gate_state: 'STALE' }
  const state: MockState = { firstRelease: false, riskPosts: [], releasePosts: [], running: true }; await mockRiskAPI(page, state, failureReport); await page.goto(`/risk-reviews?report=${calculationID}`)
  await expect(page.getByText('NOT_COMPARABLE').first()).toBeVisible(); await expect(page.getByText('UNAVAILABLE').first()).toBeVisible(); await expect(page.getByText(/THRESHOLD_CHANGED/)).toBeVisible(); await expect(page.getByText('STATIC_FORMULA_CYCLE').first()).toBeVisible(); await expect(page.getByText('EVENT_LOOP_UNBOUNDED').first()).toBeVisible(); await expect(page.getByText('STACK_UNBOUNDED').first()).toBeVisible(); await expect(page.getByText(/Graph\/影响证据不可用/)).toBeVisible(); const evidence = page.getByText(/metric-resource-required-NOT_COMPARABLE · 展开证据/); await evidence.focus(); await expect(evidence).toBeFocused(); await evidence.press('Enter')
  await selectCoreInput(page, false); await page.getByRole('button', { name: '创建风险复核 Job' }).click(); await expect(page.getByText(/状态：running/)).toBeVisible(); await page.getByRole('button', { name: '取消复核' }).click(); await expect(page.getByText(/任何部分结果都不视为成功/)).toBeVisible(); await page.getByRole('button', { name: '以当前表单创建新尝试' }).click(); expect(state.riskPosts).toHaveLength(1); await page.getByRole('button', { name: '创建风险复核 Job' }).click(); expect(state.riskPosts).toHaveLength(2)
})
