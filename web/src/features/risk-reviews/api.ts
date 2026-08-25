import { useQuery } from '@tanstack/vue-query'
import { computed } from 'vue'
import type { components } from '@/api/generated'

export type RiskCommand = components['schemas']['RiskReviewCommand']
export type RiskReview = components['schemas']['RiskReview']
export type RiskAccepted = components['schemas']['RiskJobAccepted']
export type RiskJob = components['schemas']['Job']
export type RiskJobEvent = components['schemas']['JobEvent']
export type RiskProblem = components['schemas']['Problem']

export const riskKeys = {
  review: (projectID: string, reportID: string) => ['risk-reviews', projectID, 'review', reportID] as const,
  job: (projectID: string, jobID: string) => ['risk-reviews', projectID, 'job', jobID] as const,
}

export class RiskApiError extends Error {
  constructor(message: string, readonly code?: RiskProblem['code'], readonly retryable = false) { super(message) }
}

async function response<T>(value: Response): Promise<T> {
  if (value.ok) return value.json() as Promise<T>
  const problem = await value.json().catch(() => null) as RiskProblem | null
  throw new RiskApiError(problem?.title || '风险复核请求失败', problem?.code, problem?.retryable ?? false)
}

export async function createRiskReview(command: RiskCommand, idempotencyKey: string): Promise<RiskAccepted> {
  return response(await fetch('/api/v1/risk-reviews', { method: 'POST', headers: { 'Content-Type': 'application/json', 'Idempotency-Key': idempotencyKey }, body: JSON.stringify(command) }))
}

export async function getRiskReview(reportID: string): Promise<RiskReview> {
  return response(await fetch(`/api/v1/risk-reviews/${encodeURIComponent(reportID)}`))
}

export async function getRiskJob(jobID: string): Promise<RiskJob> {
  return response(await fetch(`/api/v1/jobs/${encodeURIComponent(jobID)}`))
}

export async function cancelRiskJob(jobID: string): Promise<RiskJob> {
  return response(await fetch(`/api/v1/jobs/${encodeURIComponent(jobID)}/cancel`, { method: 'POST' }))
}

export function useRiskReview(projectID: () => string, reportID: () => string) {
  return useQuery({ queryKey: computed(() => riskKeys.review(projectID(), reportID())), queryFn: () => getRiskReview(reportID()), enabled: computed(() => Boolean(projectID() && reportID())), retry: false })
}

export function useRiskJob(projectID: () => string, jobID: () => string) {
  return useQuery({ queryKey: computed(() => riskKeys.job(projectID(), jobID())), queryFn: () => getRiskJob(jobID()), enabled: computed(() => Boolean(projectID() && jobID())), retry: false })
}
