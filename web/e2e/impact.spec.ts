import { expect, test, type Page } from '@playwright/test'
import AxeBuilder from '@axe-core/playwright'

const projectID = '01948c1e-0000-7000-8000-000000000201'
const baseID = '01948c1e-0000-7000-8000-000000000202'
const targetID = '01948c1e-0000-7000-8000-000000000203'
const jobID = '01948c1e-0000-7000-8000-000000000204'
const reportID = '01948c1e-0000-7000-8000-000000000205'
const hash = 'a'.repeat(64)

function path(id = 'edge-default') { return { source_node_id: 'changed', target_node_id: 'affected', node_ids: ['changed', 'affected'], edge_ids: [id], nodes: [{ id: 'changed', type: 'skill', label: '变化技能', text: 'changed', properties: {}, provenance: {} }, { id: 'affected', type: 'character', label: '受影响角色', text: 'affected', properties: {}, provenance: {} }], edges: [{ id, from: 'affected', to: 'changed', type: 'character_has_skill', relation_kind: 'explicit', confidence: 1, properties: {}, provenance: { field_path: '/skill_ids/0', ordinal: 0 } }], hop_count: 1, truncated: false, truncation_reasons: [] } }
function report(stale = false) { return { id: reportID, input_hash: hash, result_hash: hash, analysis_contract_version: 'dependency-impact-v1', project_uuid: projectID, base: { revision_id: baseID, config_hash: hash, version_manifest_hash: hash, graph_manifest_hash: hash, graph_node_count: 3, graph_edge_count: 2 }, target: { revision_id: targetID, config_hash: hash, version_manifest_hash: hash, graph_manifest_hash: hash, graph_node_count: 3, graph_edge_count: 2 }, filters: { relationship_kinds: ['explicit'], node_types: [], edge_types: [], direction: 'incoming' }, limits: { max_depth: 3, max_nodes: 500, default_paths_per_target: 1, expanded_max_paths: 20 }, suspected_options: { enabled: true, max_seeds: 20, max_results: 20, graph_max_depth: 2 }, mode: 'reverse_dependency_impact', changed_entities: [{ entity_id: targetID, kind: 'skill', change_kind: 'MODIFY', field_paths: ['/name'], target_node_id: 'changed', query_eligible: true }], deterministic_affected: [{ node: { id: 'affected', type: 'character', label: '受影响角色', text: 'affected', properties: {}, provenance: {} }, minimum_depth: 1, direct: true, indirect: false, tag_rule: true, evidence_ref: hash, default_path: path() }], suspected_associations: [{ rank: 1, node: { id: 'suspected', type: 'item', label: '疑似道具', text: '', properties: {}, provenance: {} }, citation_text: '可选检索证据', seed_node_id: 'changed', relationship_kinds: ['inferred'], scores: { rrf_score: '0.2' }, algorithm_version: 'hybrid-v1', evidence_ref: 'b'.repeat(64), model_provider: 'local', model: 'embed-v1' }], suspected_state: stale ? 'rebuild_required' : 'degraded', truncated: stale, truncation_reasons: stale ? ['MAX_DEPTH'] : [], warnings: stale ? ['SNAPSHOT_INDEX_NOT_READY'] : ['VECTOR_UNAVAILABLE'], cache_hit: false, freshness: { fresh: !stale, reasons: stale ? ['base_revision_changed'] : [] }, created_at: '2026-08-26T00:00:00Z', links: { self: `/api/v1/impact-analyses/${reportID}`, job: `/api/v1/jobs/${jobID}`, path_expansions: `/api/v1/impact-analyses/${reportID}/path-expansions`, explanations: `/api/v1/impact-analyses/${reportID}/explanations` } } }
function job(status: 'queued' | 'running' | 'succeeded' | 'canceled' = 'succeeded') { return { id: jobID, kind: 'impact_analysis', revision_id: targetID, status, request_hash: hash, events_url: `/api/v1/jobs/${jobID}/events`, poll_after_ms: 100, ...(status === 'succeeded' ? { result_type: 'impact_analysis', result_id: reportID, result_url: `/api/v1/impact-analyses/${reportID}` } : {}), created_at: '2026-08-26T00:00:00Z' } }

async function mockImpact(page: Page, state: { stale: boolean; running: boolean; posts: number; canceled: boolean }) {
  await page.route('**/api/v1/**', async route => {
    const request = route.request(); const pathName = new URL(request.url()).pathname
    const json = (body: unknown, status = 200) => route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) })
    if (pathName.endsWith('/projects/current')) return json({ id: projectID, name: 'impact-e2e', db_schema_version: 21 })
    if (pathName.endsWith('/projects/recent')) return json([])
    if (pathName.endsWith('/runtime/status')) return json({ phase: 'ready', project: { state: 'open' }, capabilities: [], recovery: [], log_location: 'local' })
    if (pathName.endsWith('/revisions')) return json({ items: [{ id: targetID, display_revision: 2, status: ['working', 'history'] }, { id: baseID, display_revision: 1, status: ['active_release', 'history'] }] })
    if (pathName.endsWith('/impact-analyses') && request.method() === 'POST') { state.posts++; const current = state.running ? job('running') : job(); return json({ job: current, location: `/api/v1/jobs/${jobID}`, result_type: 'impact_analysis', result_id: current.result_id ?? null, result_url: current.result_url ?? '', cache_hit: state.posts > 1 }, 202) }
    if (pathName.endsWith(`/jobs/${jobID}/cancel`)) { state.canceled = true; state.running = false; return json(job('canceled'), 202) }
    if (pathName.endsWith(`/jobs/${jobID}`)) return json(state.canceled ? job('canceled') : state.running ? job('running') : job())
    if (pathName.endsWith('/path-expansions')) return json({ report_id: reportID, expansion_hash: hash, target_node_id: 'affected', paths: [path('edge-expanded')], truncation_reasons: [], warnings: [] })
    if (pathName.endsWith('/explanations')) return json({ id: targetID, report_id: reportID, status: 'failed', ai_generated: true, evidence_refs: [hash], diagnostic: 'AI_UNAVAILABLE', created_at: '2026-08-26T00:00:01Z' }, 202)
    if (pathName.endsWith(`/impact-analyses/${reportID}`)) return json(report(state.stale))
    return json({}, 404)
  })
}

test('fixed revision pair reaches immutable report, expands evidence and survives reload', async ({ page }) => {
  const state = { stale: false, running: false, posts: 0, canceled: false }; await mockImpact(page, state); await page.goto('/impact')
  await expect(page.getByLabel('Base revision')).toHaveValue(baseID); await page.getByLabel('Target revision').selectOption(targetID); await page.getByRole('button', { name: '创建 Impact Job' }).click()
  await expect(page.getByRole('button', { name: /受影响角色 · 深度 1 · 直接影响 · 标签规则路径/ })).toBeVisible(); await expect(page.getByText(/character_has_skill/).first()).toBeVisible()
  await page.getByRole('button', { name: /展开其他路径/ }).click(); await expect(page.getByRole('button', { name: '路径 1' })).toBeVisible(); await expect(page.getByRole('img', { name: /changed 到 affected/ })).toBeVisible()
  await page.setViewportSize({ width: 1024, height: 900 }); const accessibility = await new AxeBuilder({ page }).include('.impact-page').analyze(); expect(accessibility.violations.filter(item => ['serious', 'critical'].includes(item.impact ?? ''))).toEqual([])
  await page.reload(); await expect(page.getByText(/受影响角色 · 深度 1 · 直接影响/)).toBeVisible(); expect(state.posts).toBe(1)
})

test('historical stale/rebuild state stays separate and canceled work retries only explicitly', async ({ page }) => {
  const state = { stale: true, running: true, posts: 0, canceled: false }; await mockImpact(page, state); await page.goto(`/impact?report=${reportID}`)
  await expect(page.getByText(/历史结果已陈旧/)).toBeVisible(); await expect(page.getByText(/确定性前缀已截断：MAX_DEPTH/)).toBeVisible(); await expect(page.getByText(/需要显式重建/)).toBeVisible(); await page.getByText(/Suspected 关联/).click(); await expect(page.getByText(/不属于 deterministic affected/)).toBeVisible()
  await page.getByLabel('Target revision').selectOption(targetID); await page.getByRole('button', { name: '创建 Impact Job' }).click(); await page.getByRole('button', { name: '取消任务' }).click(); await expect(page.getByText(/暂存数据不作为报告展示/)).toBeVisible(); expect(state.posts).toBe(1)
  await page.getByRole('button', { name: '创建新尝试' }).click(); expect(state.posts).toBe(1); await page.getByRole('button', { name: '创建 Impact Job' }).click(); expect(state.posts).toBe(2)
})
