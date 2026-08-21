import { useQuery } from '@tanstack/vue-query'
import { computed } from 'vue'
import type { components } from './generated'

export type SimulationJobRequest = components['schemas']['CreateSimulationJobRequest']
export type SimulationJobAccepted = components['schemas']['SimulationJobAccepted']
export type SimulationRun = components['schemas']['SimulationRun']

export const simulationKeys = {
  run: (projectID: string, runID: string) => ['simulation', projectID, 'run', runID] as const,
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
export function useSimulationRun(projectID: () => string, runID: () => string) {
  return useQuery({ queryKey: computed(() => simulationKeys.run(projectID(), runID())), queryFn: () => getSimulationRun(runID()), enabled: computed(() => Boolean(projectID() && runID())), retry: false })
}
