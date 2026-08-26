import { computed } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import type { components } from '@/api/generated'

export type ImpactCommand = components['schemas']['CreateImpactAnalysisRequest']
export type ImpactAccepted = components['schemas']['ImpactJobAccepted']
export type ImpactReport = components['schemas']['ImpactAnalysisReport']
export type ImpactExpansion = components['schemas']['ImpactPathExpansion']
export type ImpactExplanation = components['schemas']['ImpactExplanation']
export type ImpactJob = components['schemas']['Job']
export type ImpactJobEvent = components['schemas']['JobEvent']
export type ImpactPath = components['schemas']['ImpactPath']
type Problem = components['schemas']['Problem']

export class ImpactApiError extends Error {
  constructor(message: string, readonly code?: string, readonly retryable = false) { super(message) }
}

async function response<T>(value: Response): Promise<T> {
  if (value.ok) return value.json() as Promise<T>
  const problem = await value.json().catch(() => null) as Problem | null
  throw new ImpactApiError(problem?.title || '影响分析请求失败', problem?.code, problem?.retryable ?? false)
}

export async function createImpact(command: ImpactCommand, key: string): Promise<ImpactAccepted> {
  return response(await fetch('/api/v1/impact-analyses', { method: 'POST', headers: { 'Content-Type': 'application/json', 'Idempotency-Key': key }, body: JSON.stringify(command) }))
}

export async function getImpact(reportID: string): Promise<ImpactReport> {
  return response(await fetch(`/api/v1/impact-analyses/${encodeURIComponent(reportID)}`))
}

export async function getImpactJob(jobID: string): Promise<ImpactJob> {
  return response(await fetch(`/api/v1/jobs/${encodeURIComponent(jobID)}`))
}

export async function cancelImpactJob(jobID: string): Promise<ImpactJob> {
  return response(await fetch(`/api/v1/jobs/${encodeURIComponent(jobID)}/cancel`, { method: 'POST' }))
}

export async function expandImpact(reportID: string, targetNodeID: string, maxPaths: number, key: string): Promise<ImpactExpansion> {
  return response(await fetch(`/api/v1/impact-analyses/${encodeURIComponent(reportID)}/path-expansions`, { method: 'POST', headers: { 'Content-Type': 'application/json', 'Idempotency-Key': key }, body: JSON.stringify({ target_node_id: targetNodeID, max_paths: maxPaths }) }))
}

export async function explainImpact(reportID: string, evidenceRefs: string[], key: string): Promise<ImpactExplanation> {
  return response(await fetch(`/api/v1/impact-analyses/${encodeURIComponent(reportID)}/explanations`, { method: 'POST', headers: { 'Content-Type': 'application/json', 'Idempotency-Key': key }, body: JSON.stringify({ evidence_refs: evidenceRefs }) }))
}

export const impactKeys = {
  report: (projectID: string, reportID: string) => ['impact', projectID, 'report', reportID] as const,
  job: (projectID: string, jobID: string) => ['impact', projectID, 'job', jobID] as const,
}

export function useImpactReport(projectID: () => string, reportID: () => string) {
  return useQuery({ queryKey: computed(() => impactKeys.report(projectID(), reportID())), queryFn: () => getImpact(reportID()), enabled: computed(() => Boolean(projectID() && reportID())), retry: false })
}

export function useImpactJob(projectID: () => string, jobID: () => string) {
  return useQuery({ queryKey: computed(() => impactKeys.job(projectID(), jobID())), queryFn: () => getImpactJob(jobID()), enabled: computed(() => Boolean(projectID() && jobID())), retry: false })
}
