import type { RuntimeStatus } from './api'

type RuntimeReason = RuntimeStatus['capabilities'][number]['reasons'][number]

const capabilityNames: Record<string, string> = {
  'ai.design': 'AI 设计',
  backup: '项目备份',
  'graph.sync': '图谱同步',
  'impact.deterministic': '确定性影响分析',
  'local.editing': '本地编辑',
  release: '正式发布',
  retrieval: '证据检索',
  'revision.browsing': '版本浏览',
  'simulation.deterministic': '确定性模拟',
  validation: '规则验证',
}

const dependencyNames: Record<string, string> = {
  bm25: '关键词检索',
  vector: '向量检索',
  rerank: '结果重排序',
  sqlite: '本地数据库',
  graph_migrations: '图谱数据库结构',
  core_graph_query: '图谱查询',
}

const reasonMessages: Record<string, string> = {
  BACKUP_PROJECT_UNAVAILABLE: '尚未打开项目。打开或创建项目后即可备份。',
  BACKUP_IMPLEMENTATION_UNAVAILABLE: '备份服务尚未就绪，请重新检查运行状态。',
  BACKUP_SQLITE_UNAVAILABLE: '当前项目的备份服务未就绪，请重新打开项目后重试。',
  BACKUP_ROOT_UNAVAILABLE: '备份目录无法使用，请在运行设置中选择可用目录。',
  BACKUP_ROOT_UNWRITABLE: '备份目录不可写，请在运行设置中更换目录。',
  BACKUP_ROOT_INSUFFICIENT_SPACE: '备份目录空间不足，请清理空间或更换目录。',
  RESTORE_RECOVERY_REQUIRED: '恢复任务需要处理，请到“备份与恢复”查看。',
  GRAPH_DISABLED: '图谱服务已关闭，请在运行设置中启用。',
  GRAPH_UNAVAILABLE: '图谱服务尚未就绪，请检查 local-rag 并重新连接。',
  GRAPH_HEALTH_UNAVAILABLE: '无法连接图谱服务，请检查 local-rag 并重新连接。',
  RETRIEVAL_UNAVAILABLE: '检索服务尚未就绪，请先恢复图谱服务。',
  RELEASE_GATE_UNREGISTERED: '发布所需的检查项尚未注册，请检查当前发布策略。',
  RELEASE_GATE_INCOMPATIBLE: '发布策略与当前检查项版本不匹配，请检查发布策略。',
  RELEASE_GATE_REGISTRY_UNAVAILABLE: '发布检查服务暂时无法使用，请重新检查运行状态。',
  RELEASE_POLICY_INVALID: '发布策略无效，请检查项目中的发布策略。',
  AI_DISABLED: 'AI 设计已关闭，请在运行设置中启用。',
  AI_PROVIDER_UNCONFIGURED: '尚未配置 AI 服务，请填写服务地址、模型和凭据。',
  AI_ENDPOINT_REQUIRED: '请填写 AI 服务地址。',
  AI_MODEL_REQUIRED: '请选择 AI 模型。',
  AI_CREDENTIAL_REQUIRED: '请在“AI 平衡设计”中设置服务凭据。',
  AI_CREDENTIAL_UNAVAILABLE: '无法读取 AI 服务凭据，请重新设置凭据。',
  AI_SETTINGS_UNAVAILABLE: '无法读取 AI 设置，请检查运行设置。',
  AI_ENDPOINT_INVALID: 'AI 服务地址无效，请检查运行设置。',
  AI_CLOUD_NOT_ALLOWED: '云端 AI 服务尚未获准，请在运行设置中允许云端连接。',
  AI_PROVIDER_UNAVAILABLE: '无法连接 AI 服务，请检查地址和网络后重试。',
  AI_MODEL_UNAVAILABLE: '所选 AI 模型不可用，请检查模型名称。',
  AI_STRUCTURED_OUTPUT_UNSUPPORTED: '当前模型不支持结构化输出，请更换兼容模型。',
  AI_TOOL_CALLS_UNSUPPORTED: '当前模型不支持工具调用，请更换兼容模型。',
  AI_STREAMING_UNAVAILABLE: 'AI 服务暂不支持流式响应，部分体验可能受影响。',
  OBSERVATION_STALE: '上次检查结果已过期，请重新检查依赖。',
}

export function capabilityName(id: string) {
  return capabilityNames[id] ?? '其他能力'
}

export function runtimeReasonMessage(reason: RuntimeReason, projectActive = true): string {
  const { code, component } = reason
  if (!projectActive && component === 'release.gates' && code === 'RELEASE_GATE_UNREGISTERED') {
    return '尚未打开项目。打开或创建项目后即可检查发布能力。'
  }
  if (code === 'CAPABILITY_DEGRADED' || code === 'REQUIRED_CAPABILITY_UNAVAILABLE' || code === 'OPTIONAL_CAPABILITY_UNAVAILABLE') {
    const name = capabilityName(component)
    return code === 'CAPABILITY_DEGRADED' ? `依赖的${name}正在部分运行。` : `请先恢复依赖的${name}。`
  }
  const dependency = /^DEPENDENCY_([A-Za-z0-9_]+)_(UNAVAILABLE|DEGRADED|DISABLED)$/.exec(code)
  if (dependency) {
    const name = dependencyNames[dependency[1]] ?? '图谱依赖'
    if (dependency[2] === 'DISABLED') return `${name}已关闭；可用的其他检索方式仍可继续使用。`
    return dependency[2] === 'DEGRADED'
      ? `${name}运行不稳定，系统正在使用可用的检索方式。`
      : `${name}暂不可用，请检查 local-rag 后重新检查依赖。`
  }
  return reasonMessages[code] ?? '状态检查发现问题，请重新检查依赖；若仍未恢复，请查看运行设置。'
}

export function runtimeReasonSummary(reasons: RuntimeReason[], projectActive = true): string {
  if (reasons.length === 0) return '暂时不可用，请重新检查运行状态。'
  return [...new Set(reasons.map(reason => runtimeReasonMessage(reason, projectActive)))].join(' ')
}

export function providerReasonSummary(reasons: string[]) {
  return [...new Set(reasons.map(code => runtimeReasonMessage({ code, component: 'ai.provider', observation_generation: 0 })))].join(' ') || '请检查 AI 服务设置后重试。'
}

export function releaseReasonSummary(reasons: Array<{ capability_id: string; gate_id: string; code: string }>) {
  if (reasons.length === 0) return '提交时还会重新检查发布条件。'
  return reasons.map(reason => {
    const gate = reason.gate_id === 'project' ? '当前项目' : `检查项“${reason.gate_id}”`
    if (reason.code === 'MISSING') return `${gate}尚未准备好，请检查发布策略。`
    if (reason.code === 'INCOMPATIBLE') return `${gate}版本不兼容，请更新发布策略。`
    return `${gate}暂时不可用，请检查相关依赖后重试。`
  }).join(' ')
}

export const runtimeActionNames: Record<string, string> = {
  'runtime.reprobe': '重新检查依赖',
  'graph.reconnect': '重新连接图谱',
  'graph.retry': '重试图谱任务',
  'job.cancel': '取消任务',
  'provider.settings': 'AI 服务设置',
  'credential.configure': '设置 AI 凭据',
  'backup.retry': '重新检查备份',
  'backup.settings': '备份目录设置',
  'restore.inspect': '查看恢复任务',
}
