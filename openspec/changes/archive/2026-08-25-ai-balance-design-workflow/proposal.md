## Why

既有配置、校验、版本、Graph 证据、模拟与风险 artifacts 已规划出确定性分析链，但策划尚不能从目标和约束安全地产生可审阅的数值修改提案。需要冻结一个人在回路的 AI 设计工作流，使模型只能基于固定证据生成类型化 DraftPatch，并且任何事实写入仍由既有服务端校验、并发控制和人工接受流程决定。

## What Changes

- 增加 OpenAI-compatible Provider 配置与能力检测，支持 loopback 本地端点和用户主动配置的云端端点；非敏感 endpoint/model 放本机设置，密钥只进入 Windows Credential Manager 或不持久化环境变量后备。
- 增加绑定不可变 base config revision 的持久化 AI design Job：接收目标、约束、允许修改的对象/字段和预算，显式调用 local-rag `retrieve` 固定证据，再通过结构化工具白名单编排既有 diff、校验、模拟、风险和有界确定性搜索能力。
- 增加版本化 Prompt、DraftPatch Schema、工具契约和输入 manifest；审计记录 provider/model、所有版本身份、目标/约束、证据引用、结构化响应、工具调用、校验/模拟/风险结果、假设、错误与取消，不保存 secret 或无必要的敏感输入。
- AI 只生成提案，不能访问 Repository、工作草稿写接口或发布接口。DraftPatch 仅允许白名单对象/字段上的类型化操作，绑定 stable ID、expected entity version 和 base revision；无证据、越权路径、非法结构、确定性校验失败或结果陈旧都不能进入可接受状态。
- 增加流式进度、SSE 断线轮询、用户取消、明确的可重试/不可重试失败和最多三轮结构修复。AI 中断或 Provider 调用失败不自动重试；用户显式重试创建新的尝试并保留因果链，确定性工具可按既有幂等契约安全复用。
- 增加 `/ai-design` 的目标/约束、Provider 状态、进度、候选 diff、原始证据、假设、校验/模拟/风险反馈、三轮上限、接受与放弃界面；Provider、检索或 AI 不可用时降级为不生成提案，不影响人工编辑与确定性能力。
- 增加 DraftPatch 审阅与应用：用户明确接受后，在一个事务内重新检查 base revision、每个 entity version、允许路径和基础 Schema，通过 #5 的同一保存服务原子更新工作草稿并生成一个 config revision；随后由既有 #6–#11 链路重新运行，用户仍需独立完成发布确认。
- 本 change 不增加自动发布、AI 直接写事实、通用 agent/脚本执行、任意 HTTP/数据库/文件工具、无限迭代、自然语言证据造事实，或用模型替代确定性校验、搜索、模拟和风险判定。

## Capabilities

### New Capabilities

- `ai-balance-design-workflow`: 人在回路的证据约束 AI 数值设计、Provider/凭据边界、结构化工具与 DraftPatch 契约、流式 Job、审阅应用、降级和完整审计。

### Modified Capabilities

无。

## Impact

- 当前仓库新增绿地落点 `internal/ai` 及 Provider、retrieval、tool orchestration、DraftPatch、audit adapters，扩展 `internal/app`、`internal/storage/sqlite`、`internal/httpapi`、`api/openapi.yaml`、统一 Job worker，以及 `web` 的 `/ai-design` 与 `/settings` 页面。
- 项目数据库新增逻辑数据 `ai_design_runs`、`draft_patches`、`evidence_refs` 和工具/尝试审计，复用 #7 的 `jobs`/`job_events`；endpoint/model 属于 `%LOCALAPPDATA%/EcoGuardian` 设置，凭据不进入项目 DB、日志、审计或备份。
- API 增加 `POST /api/v1/ai-design-jobs`、`GET /api/v1/draft-patches/{id}`、`POST /api/v1/draft-patches/{id}/accept`、`POST /api/v1/draft-patches/{id}/discard`，复用统一 Job GET/SSE/cancel，并扩展非敏感设置与 Credential Manager 写入/清除接口。
- 硬实现依赖 #3 的只读 snapshot-bound retrieval 契约及 #5–#11 已规划的 revision/materialization、FULL validation、diff、Graph evidence、simulation、risk、Job 和审计边界；apply 时缺少所需边界必须停止，不能建立平行事实、校验、模拟、风险、Job 或发布模型。
- 外部只读消费 local-rag `POST /v1/graphs/{namespace}/retrieve`，始终显式传 namespace/snapshot 和有界过滤，保存 resolved snapshot/hash、generation/model/score/evidence/degradation；不修改 `local-rag` 或其 artifacts。
