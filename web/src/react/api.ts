import type { components } from '@/api/generated'

export type ActiveProject = { id: string; name: string; db_schema_version: number }
export type RuntimeStatus = components['schemas']['RuntimeStatusResource']
export type RuntimeCapabilities = components['schemas']['RuntimeCapabilities']
export type Problem = Partial<components['schemas']['Problem']>

export class ApiError extends Error {
  readonly status: number
  readonly code?: string
  readonly retryable: boolean
  readonly details?: Record<string, unknown>

  constructor(message: string, status: number, problem?: Problem | null) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.code = problem?.code
    this.retryable = Boolean(problem?.retryable)
    this.details = problem?.details
  }
}

export async function apiRequest<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(path, {
    ...init,
    headers: {
      Accept: 'application/json',
      ...(init?.body ? { 'Content-Type': 'application/json' } : {}),
      ...init?.headers,
    },
  })
  if (!response.ok) {
    const problem = await response.json().catch(() => null) as Problem | null
    const message = problem?.title
      ? `${problem.title}${problem.detail ? `：${problem.detail}` : ''}`
      : `请求失败（${response.status}）`
    throw new ApiError(message, response.status, problem)
  }
  if (response.status === 204) return undefined as T
  return response.json() as Promise<T>
}

export function postJSON<T>(path: string, body: unknown, headers?: HeadersInit) {
  return apiRequest<T>(path, { method: 'POST', headers, body: JSON.stringify(body) })
}

export function patchJSON<T>(path: string, body: unknown, headers?: HeadersInit) {
  return apiRequest<T>(path, { method: 'PATCH', headers, body: JSON.stringify(body) })
}

export function shortID(value?: string | null, head = 8, tail = 6) {
  if (!value) return '—'
  if (value.length <= head + tail + 1) return value
  return `${value.slice(0, head)}…${value.slice(-tail)}`
}

export function newIdempotencyKey() {
  return crypto.randomUUID()
}

export function errorMessage(cause: unknown, fallback: string) {
  return cause instanceof Error ? cause.message : fallback
}
