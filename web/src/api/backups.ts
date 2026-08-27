import { useQuery } from '@tanstack/vue-query'
import { computed, type MaybeRefOrGetter, toValue } from 'vue'
import type { components } from './generated'

export type BackupPage = components['schemas']['BackupPage']
export type BackupRecord = components['schemas']['BackupRecord']
export type BackupJobAccepted = components['schemas']['BackupJobAccepted']
export type RestorePreflight = components['schemas']['RestorePreflight']
export type RestoreJobAccepted = components['schemas']['RestoreJobAccepted']
export type Job = components['schemas']['Job']
export type JobEvent = components['schemas']['JobEvent']
type Problem = components['schemas']['Problem']

async function decode<T>(response: Response): Promise<T> {
  if (response.ok) return response.json() as Promise<T>
  const problem = await response.json().catch(() => null) as Problem | null
  throw new Error(problem?.title ? `${problem.title}${problem.detail ? `：${problem.detail}` : ''}` : `请求失败（${response.status}）`)
}

export const backupKeys = {
  inventory: (projectID: string, cursor = '') => ['backups', projectID, cursor] as const,
  job: (projectID: string, jobID: string) => ['backup-restore-job', projectID, jobID] as const,
}

export async function listBackups(cursor = ''): Promise<BackupPage> {
  const query = new URLSearchParams({ limit: '50' })
  if (cursor) query.set('cursor', cursor)
  return decode<BackupPage>(await fetch(`/api/v1/backups?${query}`))
}

export function useBackups(projectID: MaybeRefOrGetter<string>) {
  return useQuery({ queryKey: computed(() => backupKeys.inventory(toValue(projectID))), queryFn: () => listBackups(), enabled: computed(() => Boolean(toValue(projectID))) })
}

export async function createManualBackup(reason: string, idempotencyKey: string): Promise<BackupJobAccepted> {
  return decode<BackupJobAccepted>(await fetch('/api/v1/backups', { method: 'POST', headers: { 'Content-Type': 'application/json', 'Idempotency-Key': idempotencyKey }, body: JSON.stringify({ purpose: 'manual', reason }) }))
}

export async function createRestorePreflight(backupID: string): Promise<RestorePreflight> {
  return decode<RestorePreflight>(await fetch('/api/v1/restore-preflights', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ backup_id: backupID, target_mode: 'active' }) }))
}

export async function createRestore(preflight: RestorePreflight, idempotencyKey: string): Promise<RestoreJobAccepted> {
  return decode<RestoreJobAccepted>(await fetch('/api/v1/restores', { method: 'POST', headers: { 'Content-Type': 'application/json', 'Idempotency-Key': idempotencyKey }, body: JSON.stringify({ backup_id: preflight.backup.backup_id, target_mode: 'active', preflight_generation: preflight.generation, confirmation: 'RESTORE' }) }))
}

export async function getJob(jobID: string): Promise<Job> { return decode<Job>(await fetch(`/api/v1/jobs/${jobID}`)) }
export async function cancelJob(jobID: string): Promise<Job> { return decode<Job>(await fetch(`/api/v1/jobs/${jobID}/cancel`, { method: 'POST' })) }
