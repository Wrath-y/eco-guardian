import { useQuery } from '@tanstack/vue-query'
import { computed } from 'vue'
import type { components } from './generated'

type RevisionPage = components['schemas']['RevisionPage']
type RevisionDetail = components['schemas']['RevisionDetail']
type RevisionDiff = components['schemas']['RevisionDiff']
type ReleasePage = components['schemas']['ReleasePage']
type ReleasePolicyPage = components['schemas']['ReleasePolicyPage']
type RuntimeCapabilities = components['schemas']['RuntimeCapabilities']
type CreateReleaseRequest = components['schemas']['CreateReleaseRequest']
type ReleaseJobAccepted = components['schemas']['ReleaseJobAccepted']
export type ReleaseJob = components['schemas']['Job']
export type ReleaseJobEvent = components['schemas']['JobEvent']
export type ReleaseDetail = components['schemas']['ReleaseDetail']

export class VersioningApiError extends Error {
  constructor(message: string, readonly code?: string) { super(message) }
}

async function read<T>(path: string): Promise<T> {
  const response = await fetch(`/api/v1${path}`)
  if (!response.ok) throw new Error((await response.json().catch(() => null) as { title?: string } | null)?.title || '版本数据加载失败')
  return response.json() as Promise<T>
}

export const versionKeys = {
  history: (projectID: string, cursor = '') => ['versions', projectID, 'history', cursor] as const,
  revision: (projectID: string, revisionID: string) => ['versions', projectID, 'revision', revisionID] as const,
  diff: (projectID: string, baseID: string, targetID: string) => ['versions', projectID, 'diff', baseID, targetID] as const,
  releases: (projectID: string) => ['versions', projectID, 'releases'] as const,
  policies: (projectID: string) => ['versions', projectID, 'policies'] as const,
  job: (projectID: string, jobID: string) => ['versions', projectID, 'job', jobID] as const,
}

export function useRevisionHistory(projectID: () => string, cursor: () => string = () => '') {
  return useQuery({ queryKey: computed(() => versionKeys.history(projectID(), cursor())), queryFn: () => read<RevisionPage>(`/revisions${cursor() ? `?cursor=${encodeURIComponent(cursor())}` : ''}`), enabled: computed(() => Boolean(projectID())), retry: false })
}
export function useReleaseHistory(projectID: () => string, cursor: () => string = () => '') {
  return useQuery({ queryKey: computed(() => [...versionKeys.releases(projectID()), cursor()] as const), queryFn: () => read<ReleasePage>(`/releases${cursor() ? `?cursor=${encodeURIComponent(cursor())}` : ''}`), enabled: computed(() => Boolean(projectID())), retry: false })
}
export function usePolicyHistory(projectID: () => string) {
  return useQuery({ queryKey: computed(() => versionKeys.policies(projectID())), queryFn: () => read<ReleasePolicyPage>('/release-policies'), enabled: computed(() => Boolean(projectID())), retry: false })
}
export function useRuntimeCapabilities(projectID: () => string) {
  return useQuery({ queryKey: computed(() => ['versions', projectID(), 'runtime-capabilities'] as const), queryFn: () => read<RuntimeCapabilities>('/runtime/capabilities'), enabled: computed(() => Boolean(projectID())), retry: false })
}
export function useRevisionDetail(projectID: () => string, revisionID: () => string) {
  return useQuery({ queryKey: computed(() => versionKeys.revision(projectID(), revisionID())), queryFn: () => read<RevisionDetail>(`/revisions/${revisionID()}`), enabled: computed(() => Boolean(projectID() && revisionID())), retry: false })
}
export function useRevisionDiff(projectID: () => string, baseID: () => string, targetID: () => string) {
  return useQuery({ queryKey: computed(() => versionKeys.diff(projectID(), baseID(), targetID())), queryFn: () => read<RevisionDiff>(`/revisions/${targetID()}/diff?base=${encodeURIComponent(baseID())}`), enabled: computed(() => Boolean(projectID() && baseID() && targetID())), retry: false })
}
export function useReleaseJob(projectID: () => string, jobID: () => string) {
  return useQuery({ queryKey: computed(() => versionKeys.job(projectID(), jobID())), queryFn: () => read<ReleaseJob>(`/jobs/${jobID()}`), enabled: computed(() => Boolean(projectID() && jobID())), retry: false })
}
export async function cancelReleaseJob(jobID: string): Promise<ReleaseJob> {
  const response = await fetch(`/api/v1/jobs/${jobID}/cancel`, { method: 'POST' })
  if (!response.ok) throw new Error((await response.json().catch(() => null) as { title?: string } | null)?.title || '无法取消发布任务')
  return response.json() as Promise<ReleaseJob>
}
export async function getReleaseDetail(path: string): Promise<ReleaseDetail> {
  const resource = path.startsWith('/api/v1') ? path.slice('/api/v1'.length) : path
  return read<ReleaseDetail>(resource)
}
export async function createCheckpoint(currentRevisionID: string, name = ''): Promise<RevisionDetail> {
  const response = await fetch('/api/v1/revisions', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ kind: 'checkpoint', current_working: { revision_id: currentRevisionID }, name }) })
  if (!response.ok) throw new Error((await response.json().catch(() => null) as { title?: string } | null)?.title || '无法创建检查点')
  return response.json() as Promise<RevisionDetail>
}
export async function restoreRelease(currentRevisionID: string, sourceReleaseID: string): Promise<RevisionDetail> {
  const response = await fetch('/api/v1/revisions', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ kind: 'restore_release', current_working: { revision_id: currentRevisionID }, source_release_id: sourceReleaseID }) })
  if (!response.ok) throw new Error((await response.json().catch(() => null) as { title?: string } | null)?.title || '无法从正式版本创建回滚候选')
  return response.json() as Promise<RevisionDetail>
}
export async function submitRelease(request: CreateReleaseRequest, idempotencyKey: string): Promise<ReleaseJobAccepted> {
  const response = await fetch('/api/v1/releases', { method: 'POST', headers: { 'Content-Type': 'application/json', 'Idempotency-Key': idempotencyKey }, body: JSON.stringify(request) })
  if (!response.ok) {
    const problem = await response.json().catch(() => null) as { title?: string; code?: string } | null
    throw new VersioningApiError(problem?.title || '发布预检未通过', problem?.code)
  }
  return response.json() as Promise<ReleaseJobAccepted>
}
