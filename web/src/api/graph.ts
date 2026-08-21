import { useQuery } from '@tanstack/vue-query'
import { computed } from 'vue'
import type { components } from './generated'

export type GraphStatus = components['schemas']['GraphStatus']
export type GraphSyncRequest = components['schemas']['GraphSyncRequest']
export type GraphSyncJobAccepted = components['schemas']['GraphSyncJobAccepted']

export class GraphApiError extends Error {
  constructor(message: string, readonly code?: string, readonly retryable?: boolean) { super(message) }
}

export const graphKeys = { status: (projectID: string, revisionID: string) => ['graph', projectID, revisionID, 'status'] as const }

async function graphResponse<T>(response: Response): Promise<T> {
  if (response.ok) return response.json() as Promise<T>
  const problem = await response.json().catch(() => null) as { title?: string; code?: string; retryable?: boolean } | null
  throw new GraphApiError(problem?.title || '图谱服务请求失败', problem?.code, problem?.retryable)
}

export async function getGraphStatus(revisionID: string): Promise<GraphStatus> {
  return graphResponse<GraphStatus>(await fetch(`/api/v1/revisions/${encodeURIComponent(revisionID)}/graph-status`))
}
export function useGraphStatus(projectID: () => string, revisionID: () => string) {
  return useQuery({ queryKey: computed(() => graphKeys.status(projectID(), revisionID())), queryFn: () => getGraphStatus(revisionID()), enabled: computed(() => Boolean(projectID() && revisionID())), retry: false })
}

export async function ensureGraphSync(revisionID: string): Promise<GraphSyncJobAccepted> {
  return graphResponse<GraphSyncJobAccepted>(await fetch(`/api/v1/revisions/${encodeURIComponent(revisionID)}/graph-sync`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ intent: 'ensure' satisfies GraphSyncRequest['intent'] }) }))
}

export async function retryGraphSync(revisionID: string, retryOfJobID: string, idempotencyKey: string): Promise<GraphSyncJobAccepted> {
  const request: GraphSyncRequest = { intent: 'retry', retry_of_job_id: retryOfJobID }
  return graphResponse<GraphSyncJobAccepted>(await fetch(`/api/v1/revisions/${encodeURIComponent(revisionID)}/graph-sync`, { method: 'POST', headers: { 'Content-Type': 'application/json', 'Idempotency-Key': idempotencyKey }, body: JSON.stringify(request) }))
}
