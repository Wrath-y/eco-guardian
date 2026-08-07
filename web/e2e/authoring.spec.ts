import { expect, test, type Page } from '@playwright/test'

const kinds = ['attribute', 'tag', 'character', 'skill', 'item', 'effect']
type MockState = { active: boolean; mutations: number; created: Set<string>; localBlock?: boolean }
async function mockAPI(page: Page, state: MockState = { active: false, mutations: 0, created: new Set() }) {
  await page.route('**/api/v1/**', async route => {
    const request = route.request(); const url = new URL(request.url()); const path = url.pathname
    const json = (body: unknown, status = 200) => route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) })
    if (path.endsWith('/projects/current')) return state.active ? json({ id: '01948c1e-0000-7000-8000-000000000000', name: 'e2e', db_schema_version: 1 }) : json({ code: 'PROJECT_NOT_OPEN' }, 404)
    if (path.endsWith('/projects/recent')) return json([])
    if (path.endsWith('/project-selections')) return json({ token: 'selection' }, 201)
    if (path.endsWith('/projects') && request.method() === 'POST') { state.active = true; return json({ id: '01948c1e-0000-7000-8000-000000000000', name: 'e2e', db_schema_version: 1 }, 201) }
    if (path.endsWith('/projects/close')) { state.active = false; return route.fulfill({ status: 204 }) }
    if (path.includes('/schemas/entities/')) return json({ kind: path.split('/').pop(), schema_id: `urn:eco:schema:${path.split('/').pop()}:1`, schema: {} })
    if (path.includes('/entities/') && request.method() === 'GET') {
      if (path.split('/').length > 5) return route.fulfill({ status: 200, headers: { ETag: '"01948c1e-0000-7000-8000-000000000000:1"' }, contentType: 'application/json', body: JSON.stringify({ id: '01948c1e-0000-7000-8000-000000000000', kind: 'tag', key: 'fire', name: 'Fire', tag_ids: [], status: 'active', schema_version: 1, payload: { category: 'element', parent_tag_ids: [] }, extensions: {}, entity_version: 1, created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z' }) })
      return json({ items: [], next_cursor: null })
    }
    if (path.endsWith('/validation/runs') && request.method() === 'POST') return json({ id: '01948c1e-0000-7000-8000-000000000020', source: { type: 'working' }, scope: 'FULL', input_hash: 'a'.repeat(64), versions: { schema: 'schema-v1', dsl: 'dsl-v1', registry: 'registry-v1', numeric_policy: 'decimal128-v1' }, status: 'completed', summary: { error: 0, block: 0, warning: 0, info: 0 }, result_hash: 'b'.repeat(64), created_at: '2026-01-01T00:00:00Z', issues: [] }, 201)
    if (path.includes('/entities/') && request.method() === 'PATCH') { state.mutations++; if (state.mutations > 1) return json({ title: 'Entity revision conflict', code: 'REVISION_CONFLICT' }, 409); return route.fulfill({ status: 200, headers: { ETag: '"01948c1e-0000-7000-8000-000000000000:2"' }, contentType: 'application/json', body: JSON.stringify({ entity: { id: '01948c1e-0000-7000-8000-000000000000', kind: 'tag', key: 'fire', name: 'First', tag_ids: [], status: 'active', schema_version: 1, payload: { category: 'element', parent_tag_ids: [] }, extensions: {}, entity_version: 2, created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z' }, revision: { id: '01948c1e-0000-7000-8000-000000000001', display_revision: 2, config_hash: '0000000000000000000000000000000000000000000000000000000000000000', created_at: '2026-01-01T00:00:00Z', validation: { run_id: '01948c1e-0000-7000-8000-000000000002', scope: 'LOCAL', error: 0, block: 0, warning: 0, info: 0 } } }) }) }
    if (path.includes('/entities/') && request.method() === 'POST') { state.created.add(path.split('/').pop() ?? ''); return json({ entity: { id: '01948c1e-0000-7000-8000-000000000000', kind: 'tag', key: 'fire', name: 'Fire', tag_ids: [], status: 'active', schema_version: 1, payload: { category: 'element', parent_tag_ids: [] }, extensions: {}, entity_version: 1, created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z' }, revision: { id: '01948c1e-0000-7000-8000-000000000001', display_revision: 1, config_hash: '0000000000000000000000000000000000000000000000000000000000000000', created_at: '2026-01-01T00:00:00Z', validation: { run_id: '01948c1e-0000-7000-8000-000000000002', scope: 'LOCAL', error: 0, block: state.localBlock ? 1 : 0, warning: 0, info: 0 } } }, 201) }
    return json({ code: 'VALIDATION_FAILED' }, 400)
  })
}

test('project creation exposes all six structured authoring routes', async ({ page }) => {
  await mockAPI(page); await page.goto('/projects'); await page.getByRole('button', { name: '创建项目' }).click(); await expect(page.getByText('当前项目：e2e')).toBeVisible()
  for (const kind of kinds) { await page.goto(`/config/${kind}/new`); await expect(page.getByRole('heading', { name: `${kind} 编辑器` })).toBeVisible(); await expect(page.getByText('配置字段')).toBeVisible() }
})

test('invalid save retains input and moves focus to the error summary', async ({ page }) => {
  await mockAPI(page); await page.goto('/projects'); await page.getByRole('button', { name: '创建项目' }).click(); await expect(page.getByText('当前项目：e2e')).toBeVisible(); await page.goto('/config/tag/new')
  await page.getByRole('button', { name: '保存' }).click(); const summary = page.getByRole('alert'); await expect(summary).toContainText('请修正'); await expect(summary).toBeFocused()
})

test('a LOCAL BLOCK save is explicit, and a corrected/reloaded entity can receive a current FULL pass', async ({ page }) => {
  const state: MockState = { active: false, mutations: 0, created: new Set(), localBlock: true }
  await mockAPI(page, state); await page.goto('/projects'); await page.getByRole('button', { name: '创建项目' }).click(); await expect(page.getByText('当前项目：e2e')).toBeVisible(); await page.goto('/config/tag/new')
  await page.getByLabel('Key').fill('broken_reference'); await page.getByLabel('名称').fill('Broken reference'); await page.locator('[data-field-path="/payload/category"]').fill('element')
  await page.getByRole('button', { name: '保存' }).click()
  await expect(page.getByText(/LOCAL 校验摘要：ERROR 0，BLOCK 1/)).toBeVisible()
  await expect(page.getByText(/此修订含 BLOCK/)).toBeVisible()
  state.localBlock = false
  await page.goto('/config/tag/01948c1e-0000-7000-8000-000000000000?field_path=/name')
  await expect(page.getByLabel('名称')).toBeFocused()
  await page.getByRole('button', { name: '运行 FULL 校验' }).click(); await expect(page.getByText('当前 FULL 校验通过。')).toBeVisible()
  await page.reload(); await page.getByRole('button', { name: '运行 FULL 校验' }).click(); await expect(page.getByText('当前 FULL 校验通过。')).toBeVisible()
})

test('two tabs retain the losing input after REVISION_CONFLICT', async ({ browser }) => {
  const state: MockState = { active: true, mutations: 0, created: new Set() }; const winner = await browser.newPage(); const loser = await browser.newPage(); await mockAPI(winner, state); await mockAPI(loser, state)
  const path = '/config/tag/01948c1e-0000-7000-8000-000000000000'; await winner.goto(path); await loser.goto(path); await winner.getByLabel('名称').fill('Winner'); await winner.getByRole('button', { name: '保存' }).click(); await expect(winner.getByText('保存成功。已创建新的配置修订。')).toBeVisible(); await loser.getByLabel('名称').fill('Loser'); await loser.getByRole('button', { name: '保存' }).click()
  await expect(loser.getByRole('alert')).toContainText('服务器版本已更新'); await expect(loser.getByLabel('名称')).toHaveValue('Loser'); expect(state.mutations).toBe(2); await loser.getByRole('button', { name: '刷新服务器版本' }).click(); await expect(loser.getByLabel('名称')).toHaveValue('Fire'); await winner.close(); await loser.close()
})

test('dirty editor can continue editing or discard before project navigation', async ({ page }) => {
  await mockAPI(page); await page.goto('/projects'); await page.getByRole('button', { name: '创建项目' }).click(); await expect(page.getByText('当前项目：e2e')).toBeVisible(); await page.goto('/config/tag/new'); await page.getByLabel('名称').fill('Unsaved')
  await page.getByRole('link', { name: '项目' }).click(); await expect(page.getByRole('dialog')).toBeVisible(); await page.getByRole('button', { name: '继续编辑' }).click(); await expect(page).toHaveURL(/\/config\/tag\/new/)
  await page.getByRole('link', { name: '项目' }).click(); await page.getByRole('button', { name: '放弃修改' }).click(); await expect(page).toHaveURL(/\/projects/)
})
