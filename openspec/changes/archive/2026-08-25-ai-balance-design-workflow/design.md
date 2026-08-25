## Context

仓库仍是绿地规划状态，没有 Go/Vue 业务源码、迁移或测试。#5–#11 的 OpenSpec artifacts 已分别冻结项目/实体与事务保存、确定性 FULL validation、不可变 revision/diff/Job、Graph Snapshot 与影响证据、确定性 simulation 和 risk report 边界，但尚未 apply；#12 实现必须在这些端口实际存在后接入，缺失时停止，不能在 `internal/ai` 内复制任何事实或确定性引擎。

外部 `local-rag` #3 已定义 snapshot-bound `POST /v1/graphs/{namespace}/retrieve`：显式 ready Snapshot 可在未激活时查询，响应固定 `resolved_snapshot_version`/`content_hash`、mode/degradation、generation/model、阶段分数与完整 evidence；默认 explicit、limit 20、depth 1，结果上限 100、深度上限 3；索引驱逐返回 `SNAPSHOT_INDEX_NOT_READY` 且 retrieve 不隐式重建。该契约是 #12 唯一自然语言检索来源，不在当前仓库更改。

#7 的统一 Job 状态为 `queued/running/succeeded/failed/canceled/interrupted`，支持 GET、SSE、cancel 与轮询；确定性步骤可安全重放，AI 调用中断不能自动重试。#12 还受一个安全不变量约束：AI 没有 Repository、revision、release 或 Graph activation 权限，它的唯一产物是可审阅 DraftPatch；只有本地用户 accept 才能通过 #5 服务写 working draft 并生成 revision。

## Goals / Non-Goals

**Goals:**

- 建立可替换的 OpenAI-compatible Provider port、能力探测、流式事件和取消边界，且 secret 永不进入项目事实与审计。
- 以一个规范、冻结的 `AIDesignInputV1` 驱动检索、模型、结构化工具、提案预览和审计，完整固定模型/Prompt/Schema/tool/input/evaluator 版本。
- 用服务端 tool policy 和 DraftPatch validator 建立最小权限；所有事实读取绑定 base revision，所有候选修改绑定 stable ID、expected entity version 和允许路径。
- 在不创建正式 simulation/risk 事实的情况下，复用 #6/#10/#11 的纯 evaluator 对隔离 proposal materialization 生成 advisory preview；accept 后再由正式链路重新计算。
- 复用 #7 Job/SSE/cancel/idempotency 及 #5 串行事务，在并发、取消、重试和崩溃下保持至多一次模型 attempt sealing、一次 Patch decision 和一次 accepted revision。

**Non-Goals:**

- 不建设自主 agent 平台、通用工具市场、任意脚本/网络/数据库/文件访问或动态 Provider plugin。
- 不让模型决定 Schema/公式/引用/模拟/风险/发布结论，不把 AI preview 注册为 release Gate，也不允许 AI 或 accept 隐式发布。
- 不改变 local-rag #3、#5–#11 的公共事实模型或用 AI 弥补未 apply 的依赖；不持久化 chain-of-thought。
- 不保证模型输出在相同输入下字节确定；可复现目标是输入、版本、证据、调用与决定可审计，确定性工具结果可复算。

## Decisions

### 1. 在 `internal/ai` 建立编排外壳，只通过既有 ports 读取和评估

建议边界：

```text
internal/ai/
  contract/       # input, Patch, tool, evidence, attempt and audit DTO/codecs
  provider/       # transport-neutral Provider port and capability model
  retrieval/      # local-rag #3 consumer adapter and evidence pinning
  tools/          # allowlist registry, policy validation and result envelopes
  preview/        # proposal materialization plus #6/#10/#11 adapters
  orchestration/  # Job stages, attempts, cancel and sealing
  application/    # accept/discard commands and read projections
  audit/          # redaction, canonical audit events and hashes
```

`contract`、Patch validator 和 tool policy 只依赖不可变领域 DTO、规范 JSON/hash 和版本身份；不依赖 Gin、SQLite、Vue 或具体 Provider。`orchestration` 只能依赖 #5 revision materializer/read port、#6 validator、#8 Graph identity reader、#10 simulation evaluator、#11 risk evaluator、#7 JobStore/EventStore 及 AI stores。HTTP/SQLite/Credential Manager/OpenAI/local-rag 都是 adapters。

这使 architecture tests 能证明纯 AI 代码无法导入 Repository 或 release worker。备选方案是在 handler 中直接串接 Provider 和各服务；它会绕过 Job 恢复、取消、审计和依赖反转，因此不采用。

### 2. 在接纳 Job 前构造并 hash 一个完整 `AIDesignInputV1`

输入解析阶段在短读事务中固定：

- project UUID、base config revision/config hash/VersionManifest 与 materialization hash；
- 每个允许目标的 stable ID、kind、expected `entity_version` 和规范字段路径；
- 当前 release baseline identity/hash 或带标签的 `NO_BASELINE`；
- 用户目标、目标 Metric、约束、场景、允许 patch operations；
- `AIBudgetPolicyV1` 及展开后的 retrieval/provider/tool/search limits；
- validation、Graph projection、simulation、Metric、risk 与 numeric Registry 版本期望。

字符串使用 UTF-8，map key、target/path set 按 raw bytes 排序，decimal 复用 #6 规范表示；所有默认值在 hash 前展开。`input_hash = SHA-256(domain-separated canonical AIDesignInputV1)`。显示名、Job/time/worker、当前 UI 状态和 secret 不进入 input hash。Job idempotency 使用 `(project, idempotency_key, input_hash)`；同 key 同输入返回原 Job，同 key 异输入返回 409。

备选方案是在每个阶段读取“当前 revision/settings”；这会把一次运行混合到多个事实版本，无法审计，故拒绝。Provider endpoint/model/Prompt 等运行配置在创建 attempt 时从全局设置解析一次并固定到 attempt manifest，不因后续设置变更漂移。

### 3. 检索 adapter 完整遵守 local-rag #3，而不是只取拼接文本

`RetrievalEvidencePort` 从 #8 获取 base revision 的精确 namespace/Snapshot/content hash 和 ready/query capability，再调用 #3：

- 总是显式发送 `snapshot_version=base_config_revision_id`；默认 `relationship_kinds=[explicit]`，请求中的 node/edge filters、seed/result limits 和 depth 来自版本化 budget；
- 接收后核对 `resolved_snapshot_version`、`content_hash`、generation identities 和请求 filters；不一致整次失败；
- 把每个 result 的 Node/citation、BM25/Vector rank/raw score、RRF、hop/path confidence、graph/rerank score、seed/path records、provenance、mode/degraded/warnings、algorithm/model/generation 规范化为 insert-only evidence manifest；
- Provider 上下文只包含有界 citation payload 和 opaque evidence ID，不能用模型文本补造 Edge、来源或分数。

`hybrid`、`bm25_only`、`vector_only` 均可作为有证据的成功；空结果或 `RETRIEVAL_UNAVAILABLE` 不生成候选。`SNAPSHOT_INDEX_NOT_READY` 映射为带 `rebuild_required` 的不可确认失败；#12 不主动 rebuild，因为 #3 明确要求调用方显式操作且 PRD 未为 AI 页面增加自动重建。备选方案是依赖 active Snapshot 或复用未固定的影响报告文本；前者不复现，后者可能 revision pair/Graph hash 不匹配，因此不采用。

### 4. Provider port 只支持结构化 completion/tool events，并在服务端封装兼容差异

Provider port 输入是 `ProviderAttemptRequest`：固定 system/developer Prompt、用户目标/约束、evidence manifest 摘要、DraftPatch JSON Schema、tool definitions、model parameters、timeout/cancel token。输出为顺序 event stream：usage、tool call、structured response、warning 或 terminal error；只有 terminal structured response 可进入 Patch parser，流式 partial 永不成为 Patch。

adapter 使用 OpenAI-compatible 子集，并通过启动/设置测试探测 structured output、tool call、stream cancel 和 model identity。loopback 与云端使用相同领域接口；endpoint 归类、TLS/loopback 安全与可展示诊断在 adapter 层处理。Provider 不接收 Credential Manager handle 以外的 secret 读权限，HTTP trace 在 header/body 持久化前经过 redactor。

Prompt 由编译期 `PromptRegistry` 提供不可变 template ID/version/hash；DraftPatch、tool schema 和 orchestrator 也分别有 Registry identity。生产配置可选择已注册版本，不能在设置中写任意 system Prompt。这样既满足版本审计，也避免把通用 prompt 注入面扩大。备选方案是让用户自由编辑 system Prompt 或直接透传 provider SDK 对象；都会破坏安全契约和 fixture，故不采用。

### 5. Tool Registry 是服务端闭集，模型永远拿不到写工具

v1 注册六类逻辑工具：

| Tool | 数据来源/执行器 | 副作用 |
|---|---|---|
| `read_revision_context` | #5/#7 固定 materialization 与 diff reader | 无 |
| `retrieve_evidence` | 决策 3 的 #3 adapter；只执行由 frozen input 派生的预生成请求并在 Provider 前封存 | 外部只读 |
| `validate_proposal` | #6 pure FULL evaluator over proposal materialization | 无正式事实写入 |
| `preview_simulation` | #10 engine/evaluator over sealed proposal input | 只写 AI audit preview |
| `preview_risk` | #11 deterministic rules over preview results | 只写 AI audit preview |
| `search_parameters` | 版本化有界 grid/constraint search，调用上述 evaluator | 只写 AI audit preview |

每个 descriptor 固定 tool name/version/schema hash、所需 input identities、最大调用/结果大小、timeout、determinism 和 cancel behavior。`retrieve_evidence` 是编排器的结构化预生成步骤，不向 Provider 提供自由 query 参数；它只能在 `evidence_pinned` 前运行一次，随后 Provider 仅能引用封存 evidence IDs。其余 Provider tool call 前由 PolicyGuard 强制 target/path subset、base identity、预算、Schema 和阶段顺序；结果 envelope 带 tool call ID、input/result hash、实现版本和 evidence refs。Registry 启动时拒绝重名、版本/hash 漂移或任何 mutation/release capability。

`AIBudgetPolicyV1` 将“硬上限”变成版本化产品数据；至少固定自动格式修复上限 3、provider turn/tool call/search candidate/time/output token 上限，并在 runtime/status 和 Job 输入中回显实际值。具体数值作为该 Registry 的 fixture 固定并由容量测试验证，不暴露为用户可无限上调的设置。

备选方案是把现有 HTTP API 作为任意 function calling 列表交给模型；即使接口在 loopback，也会暴露保存、发布与凭据，违反最小权限，因此拒绝。

### 6. DraftPatch 使用规范 typed operations，并把证据与修改逐项关联

`DraftPatchV1` 顶层包含 schema identity、base revision/config/version manifest、ordered targets、rationale、assumptions、evidence manifest identity。每个 target 固定 stable ID/kind/expected entity version；operation 只允许已注册的 `replace` 以及 Schema 明确允许的 collection `add/remove`，路径必须落在用户 allowlist 且不得触及 envelope identity、status、revision/release、Registry identity或未授权 extensions。值首先按 DraftPatch JSON Schema 解码，再按 #5 entity Schema 与 #6 canonical decimal/type/unit 规则重验。

每个 operation 至少关联一个本次检索 evidence ID，并可关联 simulation/risk/search evidence；rationale 不能代替引用。Patch canonical order 固定 target ID、field path、operation ordinal，生成 `patch_hash` 和 schema-aware original/canonical diff。原始 Provider structured response 作为审计 blob 保存（经过 redaction、大小限制），但永不直接执行。

自动格式修复是新的 Provider attempt，输入仅含稳定 parse/Schema error code 与原响应的有界结构，不加入新证据或扩大 tools/allowlist。最多三轮修复后终止；语义 BLOCK、证据缺失和越权绝不进入格式修复。备选方案是宽松解析/自动猜字段或 RFC 6902 任意路径；这会掩盖模型错误和放大写范围，故不采用。

### 7. Proposal materialization 是隔离的评估输入，不冒充 revision 或正式报告

编排器从 base immutable materialization 复制规范引用，在内存或内容寻址临时 blob 中应用已验证 Patch，得到 `ProposalMaterializationV1` 与 hash。它不写 `working_entities`、`config_revisions`、`simulation_runs`、`risk_reviews` 或 Gate Registry。

评估顺序固定：#5 Schema → #6 引用/DSL/FULL validation → 版本化有界搜索（适用时）→ #10 固定场景 evaluator → #11 risk comparator/structure rules。adapter 必须调用已 apply 的纯 evaluator/Registry，而不是复刻 parser、simulation engine 或 threshold rules。预览结果存入 AI run 的 tool/audit payload，标记 `advisory=true`，带完整 input/result/version hash，不能被 #7 Gate 读取。

#10/#11 当前公共 Job 契约只接受 revision，因此 AI preview 不调用它们的正式 HTTP create endpoints；实现时必须由它们的应用内 evaluator port 支持 sealed materialization。如果已 apply 基线没有该纯端口，#12 停止并提交依赖报告，而不是创建第二个引擎。accept 后的新 revision 再通过正式 FULL validation、Graph、simulation、risk Job 生成权威结果。

这一选择同时满足“接受前展示反馈”和“正式分析必须绑定 revision”。备选方案是为预览先创建隐藏 config revision；这会让 AI 在人工接受前写事实并污染历史，明确拒绝。

### 8. Attempt sealing、取消和恢复采用单向状态与 generation check

一个 #7 Job 下可有有序 attempts；初始调用、每次格式修复和用户显式 retry 都有独立 attempt identity、manifest 和 terminal outcome。阶段为 `input_pinned → evidence_pinned → provider/tool_loop → deterministic_preview → patch_sealed`，只允许前进。数据库写入采用 Job ownership + expected phase/cancel generation 条件更新。

SSE 事件只投影已提交 `job_events`，包括阶段、有限进度、warning、tool identity 与 terminal result；不推送 secret 或未 redacted 原始 Provider body。cancel 增加 cancel generation 并通知 context：尚未开始的调用不执行，支持取消的 HTTP/工具收到信号；不支持可靠远端取消时 Job 可为 interrupted。任何晚到响应在短事务检查 generation，记录 `ignored_late_result` 后不得 seal Patch。

进程重启时，确定性 preview 可由 input/tool hashes 复用或重跑；进入 Provider request 后没有 terminal receipt 的 attempt 标记 interrupted，绝不自动再次计费调用。用户 retry 创建新 Job/attempt lineage并重新确认 base/Graph/settings identities；不会重写旧记录。

备选方案是把一次 AI 流程作为可重放普通 Job；Provider 非确定且可能计费，无法证明请求是否已被服务端处理，因此不采用。

### 9. 逻辑数据分离不可变生成事实与人工决定

保持技术方案的三个逻辑分组，物理表可按迁移规范拆分：

| 逻辑数据 | 关键内容与写入规则 |
|---|---|
| `ai_design_runs` | Job/input identity、attempt manifests、阶段、Provider/tool/preview/audit events、errors/usage；attempt terminal 后 insert-only |
| `evidence_refs` | run + stable ordinal、retrieval request/response identity、Node/citation/path/provenance/scores/generation/model/degradation；insert-only |
| `draft_patches` | immutable raw-redacted/canonical Patch、diff、validation/preview hashes、acceptability/freshness；Patch 内容 insert-only |
| Patch decision projection | accepted/discarded append-only decision、request/idempotency、expected versions、accepted revision 或错误；每 Patch 最多一个成功终态 |

大 payload 内容寻址去重并设置单项/单 run 大小上限；canonical audit hash 覆盖有序 manifests/events，但排除时间、展示 message 与 secret。读取时动态计算 current entity freshness、Provider availability 和正式 analysis links，不改写历史生成事实。链路保存业务所需 rationale/assumptions，不要求或存储隐藏思维链。

备选方案是直接把全部流事件和 prompt/body 塞进 Job JSON；它难以限制、索引、redact 和保证 immutable decision，故不采用。

### 10. Accept/Discard 是独立人工命令，accept 复用 #5 的串行写服务

`accept` 请求携带 Patch ID/hash、base config revision ID、ordered `(entity_id, expected_entity_version)` 和 Idempotency-Key。application service 在 #5 的 project-scoped serialized writer 中：

1. 锁定并读取 Patch，确认状态 pending/acceptable、用户明确动作和未取消；
2. 重新物化 base，核对 current working target versions、Patch allowlist/schema/hash 和所有 expected versions；
3. 通过 #5/#6 server validators 重验基础 Schema 与 Patch operations；
4. 在一个 SQLite 事务批量更新 working entities、blob/reference/tag 索引，创建一个 config revision/manifest，并追加 accepted decision；
5. commit 后按既有 handoff 排队 FULL validation，随后由 #8/#10/#11 正式链路处理。

任一步失败全量回滚；版本冲突返回 409 `REVISION_CONFLICT`。成功响应固定 accepted revision；相同 idempotency/request hash 重放返回同一结果，不再次写 revision。`discard` 只追加 mutually exclusive decision；已 accepted/discarded 的相反动作返回稳定冲突。accept 不直接调用 release 或 Graph activation。

若 #5 的 applied service 只有单实体 save，#12 在同一个 service/transaction abstraction 中增加 batch command，而不循环调用 HTTP save；否则多实体会生成多个 revision并破坏原子性。备选方案是前端逐实体 PATCH，明确不采用。

### 11. OpenAPI 与 UI 都是服务端资源的投影

OpenAPI 增加 PRD 固定路径，并复用 #7 Job envelope：

- `POST /api/v1/ai-design-jobs`：严格输入、Idempotency-Key，202 Job URI；
- `GET /api/v1/draft-patches/{id}`：不可变 Patch/diff/evidence/versions/preview/attempt lineage，加 read-time freshness/decision/正式结果 links；
- `POST .../{id}/accept|discard`：明确命令、idempotency、409 conflict 与 stable Problem Details；
- `GET/PATCH /api/v1/settings`：只含非敏感 Provider 配置和 `credential_present`；
- `PUT/DELETE /api/v1/settings/credentials/{provider}`：write-only secret，响应不回显；
- `GET /api/v1/runtime/status`：AI Provider/tool/Prompt/Schema capability 与 limits，不含 secret。

`/ai-design` 使用 generated client 和服务器资源，不在浏览器重算 Patch acceptability、校验、风险或 freshness。阶段事件用于进度，终态详情总从 resource GET 读取；SSE 失败轮询。证据和 AI rationale 视觉/语义分区，diff、warning/BLOCK 和状态带文本；1024px、键盘、焦点与 aria-live 按既有 WCAG 2.1 AA 基线测试。`/settings` 对云端端点明确展示数据将发送到用户配置服务的告知，但不扩大为账号或权限系统。

### 12. 测试按安全、契约、确定性和工作流分层

- Domain/property：规范 input/Patch/tool/evidence/audit hash，path allowlist，typed operations，budget，attempt/decision 状态机，redaction，乱序输入稳定性。
- Provider/contract：OpenAI-compatible mock 的 structured stream、tool call、timeout/cancel/late result、usage、三轮修复、能力缺失；secret 永不落盘。
- Cross-repo consumer：重放 local-rag #3 fixtures，覆盖 explicit Snapshot、hybrid/single-mode、generation/model/evidence、空结果、identity mismatch、index-not-ready 与 retryability。
- Integration：#5/#6/#7/#8/#10/#11 adapters，proposal preview 不产生正式事实，multi-entity accept 单事务/单 revision，conflict 全回滚，accept 后正式结果不复用 preview。
- UI/E2E：未配置/降级/无证据/非法/取消/retry/stale、证据与 diff、键盘；完整“生成 → 校验/预览 → 人工 accept → 一个 revision → 独立人工发布”流程，并证明 AI 没有任何直达 release 的路径。

## Risks / Trade-offs

- **[Provider OpenAI-compatible 方言差异导致结构化输出或取消语义漂移]** → 只实现探测过的最小子集，按 model capability 禁用功能，使用 versioned mock/fixture 和稳定 adapter errors。
- **[云端 Provider 造成项目内容外发]** → 必须由用户主动配置，设置页明确告知；最小化/限制发送 evidence 与字段，secret 隔离，审计 endpoint classification 和 redaction，不在日志保存 body。
- **[模型输出非确定，无法按 hash 重放]** → 固定完整输入/版本/参数并保存原始结构化响应与 lineage；只要求确定性工具可复算，不把模型生成当事实或 Gate。
- **[Proposal preview 与接受后正式结果不同]** → preview 固定完整 evaluator versions 且显著标记 advisory；accept 后强制重新运行正式链，任何差异在新 revision 结果中权威呈现。
- **[AI payload/audit 令 project.db 膨胀]** → 有界 context/response/event、内容寻址去重、摘要+引用、明确保留策略与容量 fixture；不保存 token stream 或 chain-of-thought。
- **[取消时远端仍完成并计费]** → 使用 cancel token 尽力终止，generation check 丢弃晚到结果并显示 interrupted；绝不承诺远端停止，也不自动 retry。
- **[多实体 accept 与现有单实体保存抽象冲突]** → 只在 #5 同一 serialized writer/application service 内增加 batch transaction command；依赖不支持时停止，禁止前端循环写。
- **[Prompt/tool injection 诱导越权]** → evidence 当不可信数据，固定 Prompt/Schema，服务端 Tool Registry 闭集与逐调用 PolicyGuard；无 mutation/release/credential tool 可被调用。

## Migration Plan

1. Apply 前验证 #5–#11 的实际 ports、VersionManifest/Job/transaction/evaluator/Gate identities 与 local-rag #3 consumer fixture；任何必需字段或纯 evaluator 缺失时输出依赖报告并停止。
2. 先增加 OpenAPI schemas、generated client、AI Registry manifests 与 additive SQLite migration；迁移前沿用项目强制备份约束。新表为空，AI capability 默认 disabled，不改变既有项目事实与 release。
3. 接入 Credential Manager/global settings、Provider mock/loopback adapter 和 runtime capability；secret 迁移不存在，旧设置不得被猜测为凭据。
4. 按 retrieval → tool/preview → Job/audit → Patch read → accept/discard → UI 顺序启用，每阶段运行契约、故障注入和 redaction tests。
5. 发布时保留 feature flag/capability kill switch。回滚应用版本前先禁用新 AI Job；已保存 AI records 保持只读，accepted revision 仍是普通 #5/#7 事实，不因禁用或卸载 AI 回滚。
6. 若迁移或 Provider adapter 发布失败，回滚 additive migration/feature activation而不修改 working entities、revision/release；已完成 DB migration 可安全保留未知 AI 表，由兼容应用忽略。
