import { describe, expect, it, vi } from 'vitest'
import { ApiError, apiRequest, shortID } from '@/react/api'

describe('React API client', () => {
  it('preserves problem details and retryability', async () => {
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response(JSON.stringify({ title: 'Project is locked', code: 'PROJECT_LOCKED', retryable: true, details: { owner: 'other' } }), { status: 423, headers: { 'Content-Type': 'application/json' } }))))
    await expect(apiRequest('/api/v1/projects')).rejects.toBeInstanceOf(ApiError)
    await expect(apiRequest('/api/v1/projects')).rejects.toMatchObject({ message: 'Project is locked', status: 423, code: 'PROJECT_LOCKED', retryable: true, details: { owner: 'other' } })
  })

  it('formats stable identities without losing their ends', () => {
    expect(shortID('01948c1e-0000-7000-8000-000000000000')).toBe('01948c1e…000000')
  })
})
