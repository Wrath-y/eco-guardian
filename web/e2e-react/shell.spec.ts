import { expect, test, type Page } from '@playwright/test'

const project = { id: '01948c1e-0000-7000-8000-000000000000', name: 'e2e', db_schema_version: 21 }

async function mockAPI(page: Page) {
  let active = false
  await page.route('**/api/v1/**', async route => {
    const request = route.request()
    const path = new URL(request.url()).pathname
    const json = (body: unknown, status = 200) => route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) })
    if (path.endsWith('/projects/current')) return active ? json(project) : json({ title: 'not open' }, 404)
    if (path.endsWith('/projects/recent')) return json([])
    if (path.endsWith('/project-selections')) return json({ token: 'selection' }, 201)
    if (path.endsWith('/projects') && request.method() === 'POST') { active = true; return json(project, 201) }
    if (path.endsWith('/projects/close') && request.method() === 'POST') { active = false; return route.fulfill({ status: 204 }) }
    if (path.endsWith('/runtime/status')) return json({ schema_version: 1, generation: 1, build: { version: '1.0.0', build: 'e2e', commit: 'abc', package_mode: 'development' }, listener: { url: 'http://127.0.0.1:43123' }, phase: 'ready', project: { state: active ? 'active' : 'none', project_id: active ? project.id : null, recent_count: 0, recovery_required: false }, process: { ownership: 'external', state: 'ready', launch_generation: 1, endpoint: null, restart_attempt: 0, reason: null }, dependencies: [], capabilities: [], recovery: [], log_location: '<project-directory>/logs', updated_at: new Date().toISOString() })
    if (path.endsWith('/runtime/capabilities')) return json({ release: { enabled: true, disabled_reasons: [] }, graph: { available: true, compatible: true, required_capabilities: [], degradations: [], disabled_reasons: [], release_disabled_reasons: [] }, ai: { state: 'unconfigured', enabled: false, endpoint_classification: null, credential_present: false, structured_output: false, tool_calls: false, streaming: false, reasons: ['AI_DISABLED'], prompt: { id: 'p', version: 'v1', hash: 'a'.repeat(64) }, draft_patch_schema: { id: 's', version: 'v1', hash: 'b'.repeat(64) }, tools: [], orchestrator: { id: 'o', version: 'v1', hash: 'c'.repeat(64) }, budget: { id: 'b', version: 'v1', hash: 'd'.repeat(64) }, limits: { policy: { id: 'p', version: 'v1', hash: 'e'.repeat(64) }, max_format_repairs: 3, max_provider_turns: 3, max_tool_calls: 3, max_search_candidates: 20, max_duration_millis: 1000, max_context_bytes: 1000, max_output_bytes: 1000, max_tool_result_bytes: 1000, retrieval_seed_limit: 3, retrieval_result_limit: 3, retrieval_graph_depth: 2 } }, backup: { available: true, root_health: 'healthy', restore_state: 'idle', recovery_required: false, disabled_reasons: [], safe_actions: [] } })
    if (/\/entities\/(attribute|tag|skill|item|effect)$/.test(path)) return json({ items: [], next_cursor: null })
    if (path.endsWith('/schemas/entities/tag')) return json({ kind: 'tag', schema_id: 'urn:eco:schema:tag:1', schema: {}, dsl_registry: { dsl_version: 'v1', manifest_hash: 'a'.repeat(64), selector_template: '${scope:symbol}', scopes: [], units: [], functions: [] } })
    if (path.endsWith('/entities/tag') && request.method() === 'POST') return json({}, 201)
    return json({ title: `unmocked ${path}` }, 404)
  })
}

test('creates a project and opens the React Ant Design authoring flow', async ({ page }) => {
  await mockAPI(page)
  await page.goto('/projects')
  await expect(page.getByRole('heading', { name: '项目空间' })).toBeVisible()
  await expect(page.getByText('React · Ant Design')).toBeVisible()
  await page.getByRole('button', { name: /创建项目/ }).click()
  await expect(page.getByRole('heading', { name: 'e2e' })).toBeVisible()
  await page.goto('/config/tag')
  await expect(page.getByRole('heading', { name: '标签配置' })).toBeVisible()
  await page.getByRole('button', { name: '新建标签' }).click()
  await expect(page.getByRole('heading', { name: '新建标签' })).toBeVisible()
  await expect(page.getByLabel('分类')).toBeVisible()
  await page.getByLabel('关联标签（可选）').click()
  await expect(page.getByText('暂无已有标签，可留空创建当前标签')).toBeVisible()
  await page.keyboard.press('Escape')
  await page.getByLabel('父标签').click()
  await expect(page.getByText('暂无父标签，可留空创建根标签')).toBeVisible()
  await expect(page.getByText(/“火焰”可归入 element 分类/)).toBeVisible()
  await expect(page.getByText('系统会按当前表单自动生成配置数据（Payload），无需编写 JSON。')).toBeVisible()
})

test('keeps the project close button above the decorative card layer', async ({ page }) => {
  await mockAPI(page)
  await page.goto('/projects')
  await page.getByRole('button', { name: /创建项目/ }).click()

  await page.getByRole('button', { name: /关闭项目/ }).click()

  await expect(page.getByText('尚未打开项目')).toBeVisible()
  await expect(page.getByText('项目已安全关闭')).toBeVisible()
})
