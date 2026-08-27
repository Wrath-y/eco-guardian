import { expect, test, type Page, type Route } from '@playwright/test'

const projectID = '01948c1e-0000-7000-8000-000000000100'
const entityID = '01948c1e-0000-7000-8000-000000000101'
const revisionID = '01948c1e-0000-7000-8000-000000000102'
const backupID = '01948c1e-0000-7000-8000-000000000103'
const backupJobID = '01948c1e-0000-7000-8000-000000000104'
const restoreJobID = '01948c1e-0000-7000-8000-000000000105'
const reportID = '01948c1e-0000-7000-8000-000000000106'
const hash = 'a'.repeat(64)

type RestoreState = {
  entityName: string
  snapshotName: string
  backupCreated: boolean
  restorePolls: number
  graph: 'ready' | 'pending'
  preflightFailure?: string
  restoreFailure?: 'rolled_back' | 'recovery_required' | 'mandatory'
  dailyFailure?: boolean
  dailyWaived?: boolean
}

const job = (id: string, kind: 'backup' | 'restore', status: string, extra: Record<string, unknown> = {}) => ({
  id, kind, status, request_hash: hash, events_url: `/api/v1/jobs/${id}/events`, poll_after_ms: 100,
  created_at: '2026-08-26T00:00:00Z', ...extra,
})

const backupRecord = () => ({
  backup_id: backupID, project_uuid: projectID, type: 'manual', created_at: '2026-08-26T00:00:00Z', app_version: '1.0.0', schema_version: 22,
  db_bytes: 4096, db_sha256: hash, manifest_hash: 'b'.repeat(64), validation_state: 'valid', compatibility_state: 'current',
  source: {}, retention: 'manual backups are retained', result_url: `/api/v1/backups/${backupID}`,
})

async function fulfillJSON(route: Route, body: unknown, status = 200) {
  await route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) })
}

async function mockBackupRestore(page: Page, state: RestoreState) {
  await page.route('**/api/v1/**', async route => {
    const request = route.request(); const url = new URL(request.url()); const path = url.pathname
    if (path.endsWith('/projects/current')) return fulfillJSON(route, { id: projectID, name: 'backup-e2e', db_schema_version: 22 })
    if (path.endsWith('/projects/recent')) return fulfillJSON(route, [])
    if (path.includes('/schemas/entities/')) return fulfillJSON(route, { kind: 'tag', schema_id: 'urn:eco:schema:tag:1', schema: {} })
    if (path === `/api/v1/entities/tag/${entityID}` && request.method() === 'GET') return route.fulfill({ status: 200, headers: { ETag: `"${entityID}:1"` }, contentType: 'application/json', body: JSON.stringify({ id: entityID, kind: 'tag', key: 'restore_e2e', name: state.entityName, tag_ids: [], status: 'active', schema_version: 1, payload: { category: 'element', parent_tag_ids: [] }, extensions: {}, entity_version: 1, created_at: '2026-08-26T00:00:00Z', updated_at: '2026-08-26T00:00:00Z' }) })
    if (path === `/api/v1/entities/tag/${entityID}` && request.method() === 'PATCH') { if (state.dailyFailure && !state.dailyWaived) return fulfillJSON(route, { title: 'Daily backup failed', code: 'DAILY_BACKUP_REQUIRED', details: { failed_backup_job_id: backupJobID, state: 'awaiting_waiver' } }, 503); state.entityName = request.postDataJSON().name; return fulfillJSON(route, { entity: { id: entityID, kind: 'tag', key: 'restore_e2e', name: state.entityName, tag_ids: [], status: 'active', schema_version: 1, payload: { category: 'element', parent_tag_ids: [] }, extensions: {}, entity_version: 2 }, revision: { id: revisionID, validation: { run_id: reportID, scope: 'LOCAL', error: 0, block: 0, warning: 0, info: 0 } } }) }
    if (path.endsWith('/backups/daily-waivers') && request.method() === 'POST') { state.dailyWaived = true; return fulfillJSON(route, { local_date: '2026-08-26', failed_backup_job_id: backupJobID, confirmed: true }) }
    if (path === '/api/v1/backups' && request.method() === 'GET') return fulfillJSON(route, { items: state.backupCreated ? [backupRecord()] : [], next_cursor: null, retention: { daily_count: 10, release_migration_count: 5 } })
    if (path === '/api/v1/backups' && request.method() === 'POST') { state.snapshotName = state.entityName; state.backupCreated = true; return fulfillJSON(route, { job: job(backupJobID, 'backup', 'queued'), location: `/api/v1/jobs/${backupJobID}`, result_type: 'backup', result_url: '' }, 202) }
    if (path === `/api/v1/jobs/${backupJobID}`) return fulfillJSON(route, job(backupJobID, 'backup', 'succeeded', { result_type: 'backup', result_id: backupID, result_url: `/api/v1/backups/${backupID}`, progress: 100, phase: 'succeeded' }))
    if (path.endsWith('/restore-preflights')) {
      if (state.preflightFailure) return fulfillJSON(route, { type: 'about:blank', title: state.preflightFailure, status: 422, code: 'RESTORE_PREFLIGHT_FAILED' }, 422)
      return fulfillJSON(route, { version: 'restore-preflight-v1', generation: 'c'.repeat(64), backup: backupRecord(), target_mode: 'active', registry_state: 'matched', free_space_sufficient: true, writable: true, maintenance_available: true, confirmation: { restore_pre_backup_required: true, maintenance_required: true, migration_required: false, graph_pending: true } })
    }
    if (path === '/api/v1/restores' && request.method() === 'POST') { state.restorePolls = 0; return fulfillJSON(route, { job: job(restoreJobID, 'restore', 'queued'), location: `/api/v1/jobs/${restoreJobID}`, result_type: 'restore', result_url: '' }, 202) }
    if (path === `/api/v1/jobs/${restoreJobID}`) {
      state.restorePolls++
      if (state.restorePolls < 3) return fulfillJSON(route, job(restoreJobID, 'restore', 'running', { phase: 'installed', progress: 75, warning: 'Graph verification is pending' }))
      if (state.restoreFailure === 'rolled_back') return fulfillJSON(route, job(restoreJobID, 'restore', 'failed', { phase: 'rolled_back', progress: 100, error: 'RESTORE_ROLLED_BACK' }))
      if (state.restoreFailure === 'recovery_required') return fulfillJSON(route, job(restoreJobID, 'restore', 'interrupted', { phase: 'recovery_required', progress: 75, recovery_required: true }))
      if (state.restoreFailure === 'mandatory') return fulfillJSON(route, job(restoreJobID, 'restore', 'failed', { phase: 'restore_pre_backup', progress: 25, error: 'MANDATORY_BACKUP_FAILED' }))
      state.entityName = state.snapshotName; state.graph = 'pending'
      return fulfillJSON(route, job(restoreJobID, 'restore', 'succeeded', { phase: 'succeeded', progress: 100, warning: 'Graph verification is pending', result_type: 'restore', result_id: restoreJobID, result_url: `/api/v1/restores/${restoreJobID}` }))
    }
    if (path.endsWith(`/validation/runs/${reportID}`)) return fulfillJSON(route, { id: reportID, source: { type: 'revision', revision_id: revisionID }, scope: 'FULL', status: 'completed', result_hash: hash })
    if (path.endsWith(`/revisions/${revisionID}/graph-sync`) && request.method() === 'POST') { state.graph = 'ready'; return fulfillJSON(route, { job: job('01948c1e-0000-7000-8000-000000000107', 'backup', 'queued') }, 202) }
    if (path.endsWith(`/revisions/${revisionID}/graph-status`)) return fulfillJSON(route, { revision_id: revisionID, pipeline_state: state.graph === 'ready' ? 'graph_ready' : 'saved', freshness: state.graph === 'ready' ? 'fresh' : 'stale', freshness_reasons: state.graph === 'ready' ? [] : ['RESTORE_PENDING_VERIFICATION'], warnings: state.graph === 'ready' ? [] : [{ code: 'RESTORE_PENDING_VERIFICATION' }], actions: state.graph === 'ready' ? [] : ['retry'] })
    if (path.endsWith(`/jobs/${backupJobID}/events`) || path.endsWith(`/jobs/${restoreJobID}/events`)) return route.fulfill({ status: 204 })
    return fulfillJSON(route, { title: `Unhandled ${request.method()} ${path}` }, 404)
  })
}

test('manual backup, business edit, reload-safe restore and full Graph rebuild preserve the selected timepoint', async ({ page }) => {
  const state: RestoreState = { entityName: 'Before', snapshotName: '', backupCreated: false, restorePolls: 0, graph: 'ready' }
  await mockBackupRestore(page, state)
  await page.goto('/backups'); await page.getByRole('button', { name: '立即备份' }).click(); await expect(page.getByText('任务成功。')).toBeVisible(); await expect(page.getByText('manual')).toBeVisible()
  await page.goto(`/config/tag/${entityID}`); await page.getByLabel('名称').fill('After'); await page.getByRole('button', { name: '保存' }).click(); await expect(page.getByText(/保存成功/)).toBeVisible(); expect(state.entityName).toBe('After')
  await page.goto('/backups'); await page.getByRole('button', { name: `预检恢复备份 ${backupID}` }).click(); await expect(page.getByText('恢复后 Graph：待重新验证')).toBeVisible(); await page.getByLabel(/输入/).fill('RESTORE'); await page.getByRole('button', { name: '确认恢复当前项目' }).click()
  await expect(page.getByText(/installed/)).toBeVisible(); await page.reload(); await expect(page.getByText('任务成功。')).toBeVisible({ timeout: 10_000 })
  await page.goto(`/config/tag/${entityID}`); await expect(page.getByLabel('名称')).toHaveValue('Before')
  const report = await page.evaluate(async id => (await fetch(`/api/v1/validation/runs/${id}`)).json(), reportID); expect(report.result_hash).toBe(hash)
  const pending = await page.evaluate(async id => (await fetch(`/api/v1/revisions/${id}/graph-status`)).json(), revisionID); expect(pending.freshness_reasons).toContain('RESTORE_PENDING_VERIFICATION')
  await page.evaluate(async id => { await fetch(`/api/v1/revisions/${id}/graph-sync`, { method: 'POST', headers: { 'Idempotency-Key': 'restore-full-rebuild' } }) }, revisionID)
  const ready = await page.evaluate(async id => (await fetch(`/api/v1/revisions/${id}/graph-status`)).json(), revisionID); expect(ready.pipeline_state).toBe('graph_ready')
})

test('daily backup failure retains business input until an explicit same-day waiver retries the save', async ({ page }) => {
  const state: RestoreState = { entityName: 'Before', snapshotName: '', backupCreated: false, restorePolls: 0, graph: 'ready', dailyFailure: true }
  await mockBackupRestore(page, state); await page.goto(`/config/tag/${entityID}`); await page.getByLabel('名称').fill('Retained input'); await page.getByRole('button', { name: '保存' }).click()
  await expect(page.getByText('今日编辑尚未受备份保护')).toBeVisible(); await expect(page.getByLabel('名称')).toHaveValue('Retained input'); expect(state.entityName).toBe('Before')
  await page.getByRole('button', { name: '明确继续：今天无恢复点' }).click(); await expect(page.getByText(/保存成功/)).toBeVisible(); await expect(page.getByText(/2026-08-26 已明确选择无恢复点继续编辑/)).toBeVisible(); expect(state.entityName).toBe('Retained input')
})

for (const title of ['Insufficient restore space', 'Backup root permission denied', 'Backup checksum damaged', 'Project UUID mismatch', 'Backup Schema is newer', 'Project registry conflict']) {
  test(`restore preflight blocks ${title} without presenting destructive confirmation`, async ({ page }) => {
    const state: RestoreState = { entityName: 'Before', snapshotName: 'Before', backupCreated: true, restorePolls: 0, graph: 'ready', preflightFailure: title }
    await mockBackupRestore(page, state); await page.goto('/backups'); await page.getByRole('button', { name: `预检恢复备份 ${backupID}` }).click(); await expect(page.getByText(title, { exact: true })).toBeVisible(); await expect(page.getByText('破坏性恢复确认')).toHaveCount(0); expect(state.entityName).toBe('Before')
  })
}

for (const failure of ['rolled_back', 'recovery_required', 'mandatory'] as const) {
  test(`restore exposes authoritative ${failure} state and never infers success`, async ({ page }) => {
    const state: RestoreState = { entityName: 'After', snapshotName: 'Before', backupCreated: true, restorePolls: 0, graph: 'ready', restoreFailure: failure }
    await mockBackupRestore(page, state); await page.goto('/backups'); await page.getByRole('button', { name: `预检恢复备份 ${backupID}` }).click(); await page.getByLabel(/输入/).fill('RESTORE'); await page.getByRole('button', { name: '确认恢复当前项目' }).click()
    if (failure === 'recovery_required') await expect(page.getByText(/需要安全恢复/)).toBeVisible({ timeout: 10_000 }); else await expect(page.getByText(failure === 'mandatory' ? 'MANDATORY_BACKUP_FAILED' : 'RESTORE_ROLLED_BACK', { exact: true })).toBeVisible({ timeout: 10_000 })
    await expect(page.getByText('任务成功。')).toHaveCount(0); expect(state.entityName).toBe('After')
  })
}
