import { fireEvent, render, screen, waitFor } from '@testing-library/vue'
import { createPinia } from 'pinia'
import { describe, expect, it, vi } from 'vitest'
import ProjectsView from '../src/views/ProjectsView.vue'

describe('project lifecycle page', () => {
  it('shows recent projects without exposing a filesystem path', async () => {
    vi.stubGlobal('fetch', vi.fn((url: string) => {
      if (url.endsWith('/current')) return Promise.resolve(new Response('', { status: 404 }))
      if (url.endsWith('/recent')) return Promise.resolve(new Response(JSON.stringify([{ id: '01948c1e-0000-7000-8000-000000000000', name: 'balance' }]), { status: 200 }))
      return Promise.resolve(new Response('', { status: 500 }))
    }))
    render(ProjectsView, { global: { plugins: [createPinia()] } })
    expect(await screen.findByRole('button', { name: '打开 balance' })).toBeTruthy()
    expect(document.body.textContent).not.toContain('/Users/')
  })

  it('uses the native selection capability before creating a project', async () => {
    const fetchMock = vi.fn((url: string) => {
      if (url.endsWith('/current')) return Promise.resolve(new Response('', { status: 404 }))
      if (url.endsWith('/recent')) return Promise.resolve(new Response('[]', { status: 200 }))
      if (url.endsWith('/project-selections')) return Promise.resolve(new Response(JSON.stringify({ token: 'opaque' }), { status: 201 }))
      if (url.endsWith('/projects')) return Promise.resolve(new Response(JSON.stringify({ id: '01948c1e-0000-7000-8000-000000000000', name: 'new', db_schema_version: 1 }), { status: 201 }))
      return Promise.resolve(new Response('', { status: 500 }))
    })
    vi.stubGlobal('fetch', fetchMock)
    render(ProjectsView, { global: { plugins: [createPinia()] } })
    await fireEvent.click(await screen.findByRole('button', { name: '创建项目' }))
    await waitFor(() => expect(screen.getByText(/当前项目：new/)).toBeTruthy())
    expect(fetchMock.mock.calls.some(([url]) => String(url).endsWith('/project-selections'))).toBe(true)
  })

  it('surfaces a project-lock failure and keeps the project page actionable', async () => {
    vi.stubGlobal('fetch', vi.fn((url: string) => {
      if (url.endsWith('/current')) return Promise.resolve(new Response('', { status: 404 }))
      if (url.endsWith('/recent')) return Promise.resolve(new Response('[]', { status: 200 }))
      if (url.endsWith('/project-selections')) return Promise.resolve(new Response(JSON.stringify({ token: 'opaque' }), { status: 201 }))
      if (url.endsWith('/projects')) return Promise.resolve(new Response(JSON.stringify({ title: 'Project is locked', retryable: true }), { status: 423 }))
      return Promise.resolve(new Response('', { status: 500 }))
    }))
    render(ProjectsView, { global: { plugins: [createPinia()] } })
    await fireEvent.click(await screen.findByRole('button', { name: '打开项目' }))
    expect((await screen.findByRole('alert')).textContent).toContain('Project is locked')
    expect(screen.getByRole('button', { name: '创建项目' })).toBeTruthy()
  })
})
