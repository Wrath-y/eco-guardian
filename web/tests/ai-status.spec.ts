import { describe, expect, it } from 'vitest'
import { aiFailureGuidance } from '../src/features/ai-design/status'

describe('AI failure guidance', () => {
  it('covers actionable terminal states without treating partial output as success', () => {
    const cases = [
      ['AI_PROVIDER_UNCONFIGURED', 'Provider 尚未配置'], ['AI_SNAPSHOT_INDEX_NOT_READY', '显式重建'],
      ['AI_EVIDENCE_UNAVAILABLE', '不会生成可接受候选'], ['AI_OUTPUT_INVALID', '部分或非法输出从未成为可执行候选'],
      ['AI_REPAIR_EXHAUSTED', '三轮格式修复预算已用尽'], ['AI_TOOL_POLICY_VIOLATION', '未授权工具'],
      ['AI_BUDGET_EXCEEDED', '没有生成部分成功结果'], ['AI_CANCELED', '晚到结果'], ['AI_INTERRUPTED', '不会自动再次调用 Provider'],
    ] as const
    for (const [code, text] of cases) expect(aiFailureGuidance({ code, retryable: false })).toContain(text)
    expect(aiFailureGuidance({ code: 'AI_PROVIDER_TIMEOUT', retryable: true })).toContain('可重试失败')
    expect(aiFailureGuidance({ code: 'AI_PROVIDER_AUTH_FAILED', retryable: false })).toContain('不可重试失败')
  })
})
