import { describe, expect, it } from 'vitest'
import { providerReasonSummary, releaseReasonSummary, runtimeReasonMessage } from '@/react/runtimeCopy'

describe('runtime status copy', () => {
  it('explains AI configuration and release gate failures without exposing codes as copy', () => {
    expect(providerReasonSummary(['AI_CREDENTIAL_REQUIRED', 'AI_MODEL_UNAVAILABLE'])).toContain('设置服务凭据')
    expect(releaseReasonSummary([{ capability_id: 'graph', gate_id: 'projection', code: 'INCOMPATIBLE' }])).toContain('版本不兼容')
    expect(runtimeReasonMessage({ code: 'CAPABILITY_DEGRADED', component: 'retrieval', observation_generation: 1 })).toContain('证据检索')
  })
})
