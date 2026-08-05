import { fireEvent, render, screen } from '@testing-library/vue'
import { describe, expect, it, vi } from 'vitest'
import ValidationPanel from '@/components/ValidationPanel.vue'
import type { components } from '@/api/generated'

const run = {
  id: '01948c1e-0000-7000-8000-000000000000', source: { type: 'working' }, scope: 'FULL', input_hash: 'hash', versions: { schema: 'v1', dsl: 'v1', registry: 'v1', numeric_policy: 'v1' }, status: 'completed', summary: { error: 0, block: 1, warning: 1, info: 0 }, result_hash: 'result', created_at: '',
  issues: [
    { severity: 'BLOCK', code: 'FORMULA_SYNTAX_INVALID', entity_id: '01948c1e-0000-7000-8000-000000000000', field_path: '/payload/attribute_values/0/expression', message_key: 'validation.formula_syntax_invalid', message: 'formula syntax invalid', fix_hint_key: 'validation.fix_formula', fix_hint: 'Fix the formula.', evidence: { token: '@' }, fingerprint: 'block' },
    { severity: 'WARNING', code: 'EXAMPLE_WARNING', entity_id: '01948c1e-0000-7000-8000-000000000000', field_path: '/name', message_key: 'validation.example_warning', message: 'example warning', fingerprint: 'warning' },
  ],
} as unknown as components['schemas']['ValidationRun']

describe('validation panel', () => {
  it('keeps the complete summary while filtering and activates an issue', async () => {
    const view = render(ValidationPanel, { props: { run, loading: false, error: '', stale: false } })
    expect(screen.getByText(/完整摘要：/).textContent).toContain('BLOCK 1')
    await fireEvent.click(screen.getByRole('button', { name: '显示或隐藏 WARNING 问题' }))
    expect(screen.queryByText(/EXAMPLE_WARNING/)).toBeNull()
    expect(screen.getByText(/完整摘要：/).textContent).toContain('WARNING 1')
    await fireEvent.click(screen.getByRole('button', { name: /定位 BLOCK 问题 FORMULA_SYNTAX_INVALID/ }))
    expect(view.emitted().activate?.[0]).toEqual([run.issues[0]])
  })

  it('supports keyboard filter navigation while keeping issue activation a native button action', async () => {
    const view = render(ValidationPanel, { props: { run, loading: false, error: '', stale: false } })
    const errorFilter = screen.getByRole('button', { name: '显示或隐藏 ERROR 问题' })
    errorFilter.focus()
    await fireEvent.keyDown(errorFilter, { key: 'ArrowRight' })
    expect(document.activeElement).toBe(screen.getByRole('button', { name: '显示或隐藏 BLOCK 问题' }))
    const issue = screen.getByRole('button', { name: /定位 BLOCK 问题 FORMULA_SYNTAX_INVALID/ })
    await fireEvent.click(issue)
    expect(view.emitted().activate?.[0]).toEqual([run.issues[0]])
  })

  it('shows distinct empty, failed, stale, and delayed-progress states', async () => {
    const { rerender } = render(ValidationPanel, { props: { run: null, loading: false, error: '', stale: false } })
    expect(screen.getByText('尚未运行校验。')).toBeTruthy()
    await rerender({ run: null, loading: false, error: '校验请求失败', stale: false })
    expect(screen.getByRole('alert').textContent).toContain('校验请求失败')
    await rerender({ run, loading: false, error: '', stale: true })
    expect(screen.getByText(/结果已过期/)).toBeTruthy()
  })

  it('announces validation progress after 500 ms', async () => {
    vi.useFakeTimers()
    try {
      render(ValidationPanel, { props: { run: null, loading: true, error: '', stale: false } })
      expect(screen.getByText('正在启动校验…')).toBeTruthy()
      await vi.advanceTimersByTimeAsync(500)
      expect(screen.getByRole('status').textContent).toContain('校验仍在进行中')
    } finally {
      vi.useRealTimers()
    }
  })

  it('describes a clean current FULL result in text', () => {
    const passing = { ...run, summary: { error: 0, block: 0, warning: 0, info: 0 }, issues: [] }
    render(ValidationPanel, { props: { run: passing, loading: false, error: '', stale: false } })
    expect(screen.getByText('当前 FULL 校验通过。')).toBeTruthy()
  })
})
