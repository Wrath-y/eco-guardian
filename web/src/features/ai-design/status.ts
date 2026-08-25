import type { AIStageError } from './api'

export function aiFailureGuidance(error?: AIStageError): string {
  if (!error) return '任务失败；没有把任何部分模型输出当作候选。请查看安全错误码后决定是否创建新尝试。'
  switch (error.code) {
    case 'AI_PROVIDER_UNCONFIGURED': return 'Provider 尚未配置。请先填写 endpoint、model 和写入凭据，再测试能力。'
    case 'AI_SNAPSHOT_INDEX_NOT_READY': return '冻结 Snapshot 的检索索引未就绪。请显式重建该 Snapshot 的索引后再创建新尝试。'
    case 'AI_EVIDENCE_UNAVAILABLE':
    case 'AI_RETRIEVAL_UNAVAILABLE': return '没有可确认的检索证据，因此不会生成可接受候选。请检查 Graph Snapshot 与检索组件。'
    case 'AI_OUTPUT_INVALID': return 'Provider 输出不符合 DraftPatch 契约；部分或非法输出从未成为可执行候选。'
    case 'AI_REPAIR_EXHAUSTED': return '三轮格式修复预算已用尽。请检查模型兼容性后显式创建新尝试。'
    case 'AI_TOOL_POLICY_VIOLATION': return '模型请求了未授权工具、目标或字段；请求已在执行前拒绝。'
    case 'AI_BUDGET_EXCEEDED': return '固定工具、搜索、时间或输出预算已用尽；没有生成部分成功结果。'
    case 'AI_CANCELED': return '任务已取消；晚到结果只会被记录为 ignored，不会形成候选。'
    case 'AI_INTERRUPTED': return '任务在远程完成状态不确定时中断；系统不会自动再次调用 Provider。'
    default: return `${error.code}：${error.retryable ? '这是可重试失败；确认依赖恢复后可显式创建新尝试。' : '这是不可重试失败；请先修正配置或输入。'}`
  }
}
