import { fireEvent, render, screen } from '@testing-library/vue'
import { describe, expect, it, vi } from 'vitest'
import ReleaseConfirmationForm from '../src/components/ReleaseConfirmationForm.vue'

const revisionID = '01948c1e-0000-7000-8000-000000000000'
const baselineID = '01948c1e-0000-7000-8000-000000000001'
const currentID = '01948c1e-0000-7000-8000-000000000002'
const hash = 'a'.repeat(64)
const candidate = { id: revisionID, display_revision: 1, config_hash: hash, metadata: { revision_id: revisionID, config_hash: hash, version_manifest: { entries: [], hash }, created_at: '2026-01-01T00:00:00Z' }, status: ['history'] as Array<'history'>, timeline: [] }
const policy = { id: revisionID, display_version: 1, policy_hash: hash, scenes: [], samples: 1000, threshold_id: 'default', threshold_enabled: true as const, capabilities: [], created_at: '2026-01-01T00:00:00Z' }

describe('release confirmation conflict recovery', () => {
  it('preserves release input and only refreshes the baseline after the user chooses it', async () => {
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response(JSON.stringify({ title: '基线已变化', code: 'RELEASE_BASE_CONFLICT' }), { status: 409 }))))
    render(ReleaseConfirmationForm, { props: { candidate, policy, enabled: true, suggestedBaseline: currentID } })
    const baseline = screen.getByRole('textbox', { name: '预期正式版本 ID' })
    const notes = screen.getByRole('textbox', { name: '发布说明' })
    await fireEvent.update(baseline, baselineID); await fireEvent.update(notes, '保留我的发布说明')
    await fireEvent.click(screen.getByRole('button', { name: '提交服务端发布预检' }))
    expect(await screen.findByText(/正式版本基线冲突/)).toBeTruthy()
    expect(document.activeElement).toBe(screen.getByRole('alert'))
    expect((baseline as HTMLInputElement).value).toBe(baselineID)
    expect((notes as HTMLTextAreaElement).value).toBe('保留我的发布说明')
    expect(screen.getByText(/不会自动提交、重放陈旧发布或合并配置/)).toBeTruthy()
    await fireEvent.click(screen.getByRole('button', { name: '刷新为当前正式版本基线' }))
    expect((baseline as HTMLInputElement).value).toBe(currentID)
    expect((notes as HTMLTextAreaElement).value).toBe('保留我的发布说明')
  })
})
