import { expect, test, type Page } from '@playwright/test'

const projectID = '01948c1e-0000-7000-8000-000000000000'
const baselineID = '01948c1e-0000-7000-8000-000000000001'
const candidateID = '01948c1e-0000-7000-8000-000000000002'
const checkpointID = '01948c1e-0000-7000-8000-000000000003'
const jobID = '01948c1e-0000-7000-8000-000000000004'
const releaseID = '01948c1e-0000-7000-8000-000000000005'
const secondReleaseID = '01948c1e-0000-7000-8000-000000000006'
const rollbackID = '01948c1e-0000-7000-8000-000000000007'
const hash = 'a'.repeat(64)
type State = { active: boolean; saved: boolean; checkpointed: boolean; releases: number; rolledBack: boolean; capabilityEnabled?: boolean; releaseConflict?: boolean; releaseError?: string; jobStatus?: 'running' | 'interrupted'; idempotent?: boolean }
function revision(id: string, display: number, status: Array<'working' | 'candidate' | 'active_release' | 'history'> = ['history']) { return { id, display_revision: display, config_hash: hash, metadata: { revision_id: id, config_hash: hash, name: id === checkpointID ? 'release checkpoint' : null, source_release_id: id === rollbackID ? releaseID : null, version_manifest: { entries: [], hash }, created_at: '2026-01-01T00:00:00Z' }, status, timeline: [] } }
async function mockVersionAPI(page: Page, state: State) {
  await page.route('**/api/v1/**', async route => {
    const request = route.request(); const path = new URL(request.url()).pathname
    const json = (body: unknown, status = 200) => route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) })
    if (path.endsWith('/projects/current')) return state.active ? json({ id: projectID, name: 'version-e2e', db_schema_version: 4 }) : json({}, 404)
    if (path.endsWith('/projects/recent')) return json([])
    if (path.endsWith('/project-selections')) return json({ token: 'selection' }, 201)
    if (path.endsWith('/projects') && request.method() === 'POST') { state.active = true; return json({ id: projectID, name: 'version-e2e', db_schema_version: 4 }, 201) }
    if (path.includes('/schemas/entities/')) return json({ kind: 'tag', schema_id: 'urn:eco:schema:tag:1', schema: {} })
    if (path.endsWith('/entities/tag') && request.method() === 'POST') { state.saved = true; return json({ entity: { id: candidateID, kind: 'tag', key: 'fire', name: 'Fire', tag_ids: [], status: 'active', schema_version: 1, payload: { category: 'element', parent_tag_ids: [] }, extensions: {} }, revision: { id: candidateID, validation: { run_id: candidateID, scope: 'LOCAL', error: 0, block: 0, warning: 0, info: 0 } } }, 201) }
    if (path.includes('/entities/tag/') && request.method() === 'GET') return route.fulfill({ status: 200, headers: { ETag: '"tag:1"' }, contentType: 'application/json', body: JSON.stringify({ id: candidateID, kind: 'tag', key: 'fire', name: 'Fire', tag_ids: [], status: 'active', schema_version: 1, payload: { category: 'element', parent_tag_ids: [] }, extensions: {} }) })
    if (path.endsWith('/validation/runs') && request.method() === 'POST') return json({ id: jobID, source: { type: 'working' }, scope: 'FULL', input_hash: hash, versions: { schema: 'v1', dsl: 'v1', registry: 'v1', numeric_policy: 'v1' }, status: 'completed', summary: { error: 0, block: 0, warning: 0, info: 0 }, result_hash: hash, created_at: '2026-01-01T00:00:00Z', issues: [] }, 201)
    if (path.endsWith('/revisions') && request.method() === 'GET') return json({ items: [revision(baselineID, 1, ['active_release', 'history']), ...(state.saved ? [revision(candidateID, 2, ['working', 'history'])] : []), ...(state.checkpointed ? [revision(checkpointID, 3, ['working', 'history'])] : []), ...(state.rolledBack ? [revision(rollbackID, 4, ['working', 'history'])] : [])] })
    if (path.endsWith('/revisions') && request.method() === 'POST') { const command = request.postDataJSON() as { kind: string }; if (command.kind === 'restore_release') { state.rolledBack = true; return json(revision(rollbackID, 4, ['working', 'history']), 201) }; state.checkpointed = true; return json(revision(checkpointID, 3, ['working', 'history']), 201) }
    if (path.includes('/revisions/') && path.endsWith('/diff')) return json({ base_revision_id: baselineID, target_revision_id: candidateID, base_config_hash: hash, target_config_hash: hash, baseline_state: 'AVAILABLE', changes: ['ADD', 'DELETE', 'MOVE', 'MODIFY'].map((kind, index) => ({ entity_id: candidateID, entity_kind: 'tag', path: `/payload/${index}`, kind, old_value: index ? 'old' : undefined, new_value: index === 1 ? undefined : 'new' })) })
    if (path.includes('/revisions/')) { const id = path.split('/').pop()!; return json(revision(id, id === baselineID ? 1 : id === checkpointID ? 3 : 2, id === baselineID ? ['active_release', 'history'] : ['working', 'history'])) }
    if (path.endsWith('/release-policies')) return json({ items: [{ id: candidateID, display_version: 1, policy_hash: hash, scenes: [], samples: 1000, threshold_id: 'default', threshold_enabled: true, capabilities: state.capabilityEnabled === false ? [{ capability_id: 'risk', gate_id: 'compare', contract_version: '1' }] : [], created_at: '2026-01-01T00:00:00Z' }] })
    if (path.endsWith('/runtime/capabilities')) return json({ release: state.capabilityEnabled ?? true ? { enabled: true, disabled_reasons: [] } : { enabled: false, disabled_reasons: [{ capability_id: 'risk', gate_id: 'compare', code: 'MISSING', detail: '风险 Gate 尚未注册' }] } })
    if (path.endsWith('/releases') && request.method() === 'GET') return json({ items: Array.from({ length: state.releases }, (_, index) => ({ id: index ? secondReleaseID : releaseID, revision_id: candidateID, policy_id: candidateID, intent_id: jobID, notes: index ? 'second' : 'first', gate_evidence: [], confirmations: [], created_at: '2026-01-01T00:00:00Z' })) })
    if (path.endsWith('/releases') && request.method() === 'POST') { if (state.releaseConflict) return json({ title: 'Expected release baseline is no longer current', code: 'RELEASE_BASE_CONFLICT' }, 409); if (state.releaseError) return json({ title: state.releaseError, code: 'MANDATORY_BACKUP_FAILED' }, 422); if (!state.idempotent || state.releases === 0) state.releases++; return json({ job_id: jobID, location: `/api/v1/jobs/${jobID}` }, 202) }
    if (path.endsWith(`/jobs/${jobID}`)) { const id = state.releases > 1 ? secondReleaseID : releaseID; const status = state.jobStatus ?? 'succeeded'; return json({ id: jobID, kind: 'release', status, request_hash: hash, events_url: `/api/v1/jobs/${jobID}/events`, poll_after_ms: 100, ...(status === 'succeeded' ? { result_type: 'release', result_id: id, result_url: `/api/v1/releases/${id}` } : {}), created_at: '2026-01-01T00:00:00Z' }) }
    if (path.endsWith(`/releases/${releaseID}`) || path.endsWith(`/releases/${secondReleaseID}`)) { const id = path.endsWith(secondReleaseID) ? secondReleaseID : releaseID; return json({ id, revision_id: candidateID, policy_id: candidateID, intent_id: jobID, gate_evidence: [], confirmations: [], created_at: '2026-01-01T00:00:00Z', active_pointer: { release_id: id, generation: state.releases } }) }
    return json({}, 404)
  })
}

test('version workflow saves, checkpoints, diffs, validates and establishes one read-only release', async ({ page }) => {
  const state: State = { active: false, saved: false, checkpointed: false, releases: 0, rolledBack: false }
  await mockVersionAPI(page, state)
  await page.goto('/projects'); await page.getByRole('button', { name: '创建项目' }).click(); await expect(page.getByText('当前项目：version-e2e')).toBeVisible()
  await page.goto('/config/tag/new'); await page.getByLabel('Key').fill('fire'); await page.getByLabel('名称').fill('Fire'); await page.locator('[data-field-path="/payload/category"]').fill('element'); await page.getByRole('button', { name: '保存' }).click(); await expect(page.getByText(/保存成功/)).toBeVisible()
  await page.goto(`/config/tag/${candidateID}`); await page.getByRole('button', { name: '运行 FULL 校验' }).click(); await expect(page.getByText('当前 FULL 校验通过。')).toBeVisible()
  await page.goto('/versions'); await page.getByRole('button', { name: '版本 2' }).click(); await page.getByRole('button', { name: '创建同内容检查点' }).click(); await expect(page.getByText('版本 3')).toBeVisible()
  const candidateRow = page.getByRole('button', { name: '版本 2' }).locator('..'); await candidateRow.getByRole('button', { name: '选择候选' }).click(); await candidateRow.getByRole('link', { name: '查看差异' }).click()
  for (const kind of ['ADD', 'DELETE', 'MOVE', 'MODIFY']) await expect(page.getByText(kind).first()).toBeVisible()
  await page.getByLabel('预期正式版本 ID').fill(''); await page.getByRole('checkbox', { name: '我确认建立首个正式版本基线' }).check(); await page.getByRole('button', { name: '提交服务端发布预检' }).click()
  await expect(page.getByText('正式版本指针已确认提交。')).toBeVisible(); await expect(page.getByText(`当前正式版本：${releaseID}（已确认指针提交）`)).toBeVisible()
})

test('a second release leaves the first immutable and a rollback creates a forward revision', async ({ page }) => {
  const state: State = { active: true, saved: true, checkpointed: false, releases: 1, rolledBack: false }
  await mockVersionAPI(page, state); await page.goto(`/versions/${candidateID}/diff?base=${baselineID}`)
  await page.getByLabel('预期正式版本 ID').fill(releaseID); await page.getByRole('button', { name: '提交服务端发布预检' }).click(); await expect(page.getByText(`当前正式版本：${secondReleaseID}（已确认指针提交）`)).toBeVisible()
  await page.goto('/versions'); await expect(page.getByText('正式版本 01948c1e · revision 01948c1e · first')).toBeVisible(); await page.getByRole('button', { name: '从此正式版本创建回滚候选' }).first().click(); await expect(page.getByText('版本 4')).toBeVisible(); await expect(page.getByText('从正式版本回滚')).toBeVisible(); await expect(page.getByText('正式版本 01948c1e · revision 01948c1e · first')).toBeVisible()
})

test('a missing required Gate keeps release disabled with a server-provided next action', async ({ page }) => {
  const state: State = { active: true, saved: true, checkpointed: false, releases: 0, rolledBack: false, capabilityEnabled: false }
  await mockVersionAPI(page, state); await page.goto(`/versions/${candidateID}/diff?base=${baselineID}`)
  await expect(page.getByText('MISSING: 风险 Gate 尚未注册')).toBeVisible(); await expect(page.getByText(/注册或恢复此服务端 Gate/)).toBeVisible(); await expect(page.getByRole('button', { name: '提交服务端发布预检' })).toBeDisabled()
})

test('a concurrent baseline conflict keeps release inputs and requires an explicit refresh', async ({ page }) => {
  const state: State = { active: true, saved: true, checkpointed: false, releases: 1, rolledBack: false, releaseConflict: true }
  await mockVersionAPI(page, state); await page.goto(`/versions/${candidateID}/diff?base=${baselineID}`)
  await page.getByLabel('预期正式版本 ID').fill(baselineID); await page.getByLabel('发布说明').fill('不要丢失这段说明'); await page.getByRole('button', { name: '提交服务端发布预检' }).click()
  await expect(page.getByText(/正式版本基线冲突/)).toBeVisible(); await expect(page.getByLabel('发布说明')).toHaveValue('不要丢失这段说明'); await page.getByRole('button', { name: '刷新为当前正式版本基线' }).click(); await expect(page.getByLabel('预期正式版本 ID')).toHaveValue(releaseID)
})

test('a mandatory backup failure is shown as a non-replayable preflight error', async ({ page }) => {
  const state: State = { active: true, saved: true, checkpointed: false, releases: 1, rolledBack: false, releaseError: 'Mandatory backup failed' }
  await mockVersionAPI(page, state); await page.goto(`/versions/${candidateID}/diff?base=${baselineID}`)
  await page.getByLabel('预期正式版本 ID').fill(releaseID); await page.getByRole('button', { name: '提交服务端发布预检' }).click(); await expect(page.getByRole('alert')).toContainText('Mandatory backup failed'); await expect(page.getByText(/不会自动提交、重放陈旧发布/)).toHaveCount(0)
})

test('an interrupted Job is not presented as canceled or published while intent recovery is required', async ({ page }) => {
  const state: State = { active: true, saved: true, checkpointed: false, releases: 1, rolledBack: false, jobStatus: 'interrupted' }
  await mockVersionAPI(page, state); await page.goto(`/versions/${candidateID}/diff?base=${baselineID}`); await page.getByLabel('预期正式版本 ID').fill(releaseID); await page.getByRole('button', { name: '提交服务端发布预检' }).click()
  await expect(page.getByText(/任务已中断：外部步骤可能已被接受/)).toBeVisible(); await expect(page.getByText(/这不是已取消或已发布/)).toBeVisible()
})

test('an SSE disconnect visibly switches a running release Job to polling fallback', async ({ page }) => {
  const state: State = { active: true, saved: true, checkpointed: false, releases: 1, rolledBack: false, jobStatus: 'running' }
  await mockVersionAPI(page, state); await page.goto(`/versions/${candidateID}/diff?base=${baselineID}`); await page.getByLabel('预期正式版本 ID').fill(releaseID); await page.getByRole('button', { name: '提交服务端发布预检' }).click()
  await expect(page.getByText(/SSE 已断开，正在按服务端建议轮询/)).toBeVisible()
})

for (const title of ['Stale FULL validation result', 'Required metric unavailable', 'Optional metric WARNING requires acknowledgement', 'Deterministic validation BLOCK cannot be overridden']) {
  test(`release preflight exposes ${title}`, async ({ page }) => {
    const state: State = { active: true, saved: true, checkpointed: false, releases: 1, rolledBack: false, releaseError: title }
    await mockVersionAPI(page, state); await page.goto(`/versions/${candidateID}/diff?base=${baselineID}`); await page.getByLabel('预期正式版本 ID').fill(releaseID); await page.getByRole('button', { name: '提交服务端发布预检' }).click()
    await expect(page.getByRole('alert')).toContainText(title); await expect(page.getByText(/可覆盖数值 BLOCK/)).toBeVisible()
  })
}

test('a repeated idempotency key returns the original Job without a second release effect', async ({ page }) => {
  await page.addInitScript(() => { Object.defineProperty(crypto, 'randomUUID', { value: () => '00000000-0000-4000-8000-000000000000' }) })
  const state: State = { active: true, saved: true, checkpointed: false, releases: 0, rolledBack: false, idempotent: true }
  await mockVersionAPI(page, state); await page.goto(`/versions/${candidateID}/diff?base=${baselineID}`); await page.getByRole('checkbox', { name: '我确认建立首个正式版本基线' }).check(); await page.getByRole('button', { name: '提交服务端发布预检' }).click(); await page.getByRole('button', { name: '提交服务端发布预检' }).click(); expect(state.releases).toBe(1)
})
