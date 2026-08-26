import { useQuery } from '@tanstack/vue-query'
import { computed } from 'vue'
import type { components } from '@/api/generated'

export type AICapability = components['schemas']['AIProviderCapability']
export type AISettings = components['schemas']['SettingsResource']
export type PatchAISettings = components['schemas']['PatchSettingsRequest']
export type SettingsUpdate = components['schemas']['SettingsUpdateResult']
export type AIDesignRequest = components['schemas']['CreateAIDesignJobRequest']
export type AIDesignAccepted = components['schemas']['AIDesignJobAccepted']
export type DraftPatch = components['schemas']['DraftPatchResource']
export type AcceptResult = components['schemas']['AcceptDraftPatchResult']
export type DiscardResult = components['schemas']['DiscardDraftPatchResult']
export type AIJob = components['schemas']['Job'] & { phase?: string; input_hash?: string }
export type AIStageError = { code: string; retryable: boolean; request_id?: string | null; rebuild_required?: boolean }
export type AIJobEvent = Omit<components['schemas']['JobEvent'], 'error' | 'warning'> & {
  kind?: 'stage' | 'progress' | 'warning' | 'tool' | 'repair' | 'ignored_late_result' | 'terminal'
  attempt_id?: string
  warning_code?: string
  warning_ref?: string
  tool?: components['schemas']['AIVersionIdentity']
  repair_count?: number
  outcome?: string
  error?: AIStageError
}

type Problem = Partial<components['schemas']['Problem']>

export class AIApiError extends Error {
  constructor(message: string, readonly code = 'AI_REQUEST_FAILED', readonly retryable = false, readonly details?: Record<string, unknown>) { super(message) }
}

async function response<T>(request: Promise<Response>): Promise<T> {
  const result = await request
  if (!result.ok) {
    const problem = await result.json().catch(() => null) as Problem | null
    throw new AIApiError(problem?.title || 'AI 工作流请求失败', problem?.code || 'AI_REQUEST_FAILED', Boolean(problem?.retryable), problem?.details)
  }
  return result.json() as Promise<T>
}

export const aiKeys = {
  settings: (projectID: string) => ['ai-design', projectID, 'settings'] as const,
  capability: (projectID: string) => ['ai-design', projectID, 'capability'] as const,
  job: (projectID: string, jobID: string) => ['ai-design', projectID, 'job', jobID] as const,
  patch: (projectID: string, patchID: string) => ['ai-design', projectID, 'patch', patchID] as const,
}

export function getAISettings() { return response<AISettings>(fetch('/api/v1/settings')) }
export function patchAISettings(value: PatchAISettings) { return response<SettingsUpdate>(fetch('/api/v1/settings', { method: 'PATCH', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(value) })) }
export function getAICapability() { return response<components['schemas']['RuntimeCapabilities']>(fetch('/api/v1/runtime/capabilities')).then(value => value.ai) }
export function setProviderCredential(credential: string) { return response<components['schemas']['ProviderCredentialStatus']>(fetch('/api/v1/settings/credentials/openai-compatible', { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ credential }) })) }
export function clearProviderCredential() { return response<components['schemas']['ProviderCredentialStatus']>(fetch('/api/v1/settings/credentials/openai-compatible', { method: 'DELETE' })) }
export function createAIDesignJob(value: AIDesignRequest, key: string) { return response<AIDesignAccepted>(fetch('/api/v1/ai-design-jobs', { method: 'POST', headers: { 'Content-Type': 'application/json', 'Idempotency-Key': key }, body: JSON.stringify(value) })) }
export function getAIJob(jobID: string) { return response<AIJob>(fetch(`/api/v1/jobs/${encodeURIComponent(jobID)}`)) }
export function cancelAIJob(jobID: string) { return response<AIJob>(fetch(`/api/v1/jobs/${encodeURIComponent(jobID)}/cancel`, { method: 'POST' })) }
export function getDraftPatch(patchID: string) { return response<DraftPatch>(fetch(`/api/v1/draft-patches/${encodeURIComponent(patchID)}`)) }
export function acceptDraftPatch(patch: DraftPatch, key: string) {
  return response<AcceptResult>(fetch(patch.links.accept, { method: 'POST', headers: { 'Content-Type': 'application/json', 'Idempotency-Key': key }, body: JSON.stringify({ patch_hash: patch.patch_hash, base_revision_id: patch.base_revision_id, targets: patch.targets.map(target => ({ entity_id: target.entity_id, expected_entity_version: target.expected_entity_version })) }) }))
}
export function discardDraftPatch(patch: DraftPatch, key: string, reason: string) {
  return response<DiscardResult>(fetch(patch.links.discard, { method: 'POST', headers: { 'Content-Type': 'application/json', 'Idempotency-Key': key }, body: JSON.stringify({ patch_hash: patch.patch_hash, ...(reason.trim() ? { reason: reason.trim() } : {}) }) }))
}

export function useAISettings(projectID: () => string) {
  return useQuery({ queryKey: computed(() => aiKeys.settings(projectID())), queryFn: getAISettings, enabled: computed(() => Boolean(projectID())), retry: false })
}
export function useAICapability(projectID: () => string) {
  return useQuery({ queryKey: computed(() => aiKeys.capability(projectID())), queryFn: getAICapability, enabled: computed(() => Boolean(projectID())), retry: false })
}
export function useAIJob(projectID: () => string, jobID: () => string) {
  return useQuery({ queryKey: computed(() => aiKeys.job(projectID(), jobID())), queryFn: () => getAIJob(jobID()), enabled: computed(() => Boolean(projectID() && jobID())), retry: false })
}
export function useDraftPatch(projectID: () => string, patchID: () => string) {
  return useQuery({ queryKey: computed(() => aiKeys.patch(projectID(), patchID())), queryFn: () => getDraftPatch(patchID()), enabled: computed(() => Boolean(projectID() && patchID())), retry: false })
}
