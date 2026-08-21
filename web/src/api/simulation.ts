import { useQuery } from '@tanstack/vue-query'
import { computed } from 'vue'
import type { components } from './generated'

export type SimulationJobRequest = components['schemas']['CreateSimulationJobRequest']
export type SimulationJobAccepted = components['schemas']['SimulationJobAccepted']
export type SimulationRun = components['schemas']['SimulationRun']
export type SimulationJob = components['schemas']['Job']
export type SimulationJobEvent = components['schemas']['JobEvent']

export const simulationKeys = {
  run: (projectID: string, runID: string) => ['simulation', projectID, 'run', runID] as const,
  job: (projectID: string, jobID: string) => ['simulation', projectID, 'job', jobID] as const,
}

export class SimulationApiError extends Error {
  constructor(message: string, readonly code?: components['schemas']['Problem']['code']) { super(message) }
}

async function response<T>(value: Response): Promise<T> {
  if (value.ok) return value.json() as Promise<T>
  const problem = await value.json().catch(() => null) as components['schemas']['Problem'] | null
  throw new SimulationApiError(problem?.title || '模拟请求失败', problem?.code)
}

export async function createSimulationJob(request: SimulationJobRequest, idempotencyKey: string): Promise<SimulationJobAccepted> {
  return response(await fetch('/api/v1/simulation-jobs', { method: 'POST', headers: { 'Content-Type': 'application/json', 'Idempotency-Key': idempotencyKey }, body: JSON.stringify(request) }))
}
export async function getSimulationRun(runID: string): Promise<SimulationRun> {
  return response(await fetch(`/api/v1/simulation-runs/${encodeURIComponent(runID)}`))
}
export async function getSimulationJob(jobID: string): Promise<SimulationJob> {
  return response(await fetch(`/api/v1/jobs/${encodeURIComponent(jobID)}`))
}
export async function cancelSimulationJob(jobID: string): Promise<SimulationJob> {
  return response(await fetch(`/api/v1/jobs/${encodeURIComponent(jobID)}/cancel`, { method: 'POST' }))
}
export function useSimulationRun(projectID: () => string, runID: () => string) {
  return useQuery({ queryKey: computed(() => simulationKeys.run(projectID(), runID())), queryFn: () => getSimulationRun(runID()), enabled: computed(() => Boolean(projectID() && runID())), retry: false })
}
export function useSimulationJob(projectID: () => string, jobID: () => string) {
  return useQuery({ queryKey: computed(() => simulationKeys.job(projectID(), jobID())), queryFn: () => getSimulationJob(jobID()), enabled: computed(() => Boolean(projectID() && jobID())), retry: false })
}
