# 数值策划分析工具实现计划 — 需求拆分

> 来源：`file: docs/implementation-plan.md`
> 拆分日期：2026-07-31
> 目标用户：需要在本机录入、分析和迭代游戏数值的策划人员
> 版本或编号：未提供
> 期望交付时间：未提供

## 范围与假设

本计划交付一个优先面向 Windows 10/11 x64 的本地数值策划工具。用户从单一入口启动，浏览器自动打开；没有 Graph RAG 或 AI 服务时仍可编辑、校验、保存和备份项目。架构决策依次优先保证使用方便、本地部署、方便启动和易于扩展。

- `eco-guardian` 是当前仓库，负责业务事实、规则、版本、模拟、风险、AI 工作流和本地运行；当前没有业务源码，全部属于绿地建设。
- `local-rag` 是外部仓库，负责通用 Graph Snapshot、图查询和混合检索。其需求保留在总计划中，但默认不在当前仓库生成 OpenSpec change。
- 一个项目对应一个目录和一个 `project.db`；单进程一次打开一个项目，并以稳定 UUID 作为 Graph Namespace。移动、改名或恢复项目不会改变 UUID。
- 业务对象使用 UUIDv7 作为稳定 ID，显示用 `key` 可以修改。删除采用 tombstone；历史快照不做物理修改。
- 工作流为“编辑中的工作草稿 → 用户明确保存产生不可变 config revision → 校验/同步/模拟/风险复核 → release”。不按键自动产生修订；回滚通过旧 release 指向的 revision 创建新 revision 并重新发布完成。
- Graph Snapshot 始终不可变。“增量同步”表示基于父快照的差量创建新快照，不修改基础快照。
- 正式发布必须完成确定性校验、Graph 同步、必要模拟、风险复核和人工确认。外部服务离线时可以继续编辑和保存候选，但不能完成正式发布。
- 公式、引用、模拟和发布判断不依赖 LLM。AI 只生成带证据的草案补丁，永不直接写正式版本。
- 长任务统一使用可持久化 Job；UI 使用 SSE 接收进度，断线后退回轮询。

版本术语和标识统一如下：

- `entity_version`：单个实体的单调递增版本号，用于 ETag/If-Match 并发控制。
- `config_revision_id`：每次明确保存产生的项目级不可变配置快照 UUIDv7；PRD 中“配置快照”统一称 Configuration Revision。
- `display_revision`：项目内单调递增、仅用于界面显示的修订号。
- `config_hash`：整个 revision 规范化内容的 SHA-256，用于去重和一致性校验；相同内容的两个命名 revision 仍拥有不同 ID。
- `candidate`：被选中执行门禁或发布的 config revision 状态，不是另一份数据副本。
- `release`：指向某个 config revision 的只读正式版本记录。
- `Graph Snapshot version`：直接使用 `config_revision_id`；其 `graph_manifest_hash` 与 `config_hash` 分开保存。

明确不在 MVP 范围：通用战斗引擎、未经人工确认的 AI 自动发布、任意 Excel 字段自动推断、多用户协作、账号与权限、远程无鉴权部署、社区发现和社区摘要。

## 需求清单

| # | 优先级 | 范围 | 归属 | Change 名称 | 标题 | 模块 | OpenSpec 状态 |
|---|---|---|---|---|---|---|---|
| 1 | HIGH | BACKEND | EXTERNAL | `graph-snapshot-lifecycle` | 图快照生命周期 | Graph Store | ✅ 已生成（local-rag） |
| 2 | HIGH | BACKEND | EXTERNAL | `constrained-graph-query` | 受约束图查询 | Graph Query | ✅ 已生成（local-rag） |
| 3 | HIGH | BACKEND | EXTERNAL | `hybrid-graph-retrieval` | 混合图检索 | Graph RAG | ✅ 已生成（local-rag） |
| 4 | HIGH | CROSS_CUTTING | EXTERNAL | `graph-service-operability` | 图服务运维保障 | 服务运维 | ✅ 已生成（local-rag） |
| 5 | HIGH | FULLSTACK | CURRENT_REPO | `domain-config-authoring` | 领域配置编辑 | 配置管理 | ✅ 已生成 |
| 6 | HIGH | FULLSTACK | CURRENT_REPO | `config-rule-validation` | 配置规则校验 | 规则引擎 | ✅ 已生成 |
| 6.5 | HIGH | BACKEND | CURRENT_REPO | `typed-rule-materialization` | 类型化规则物化 | 规则物化 | ✅ 已生成 |
| 7 | HIGH | FULLSTACK | CURRENT_REPO | `versioned-config-workflow` | 配置版本管理 | 版本管理 | ✅ 已生成 |
| 8 | HIGH | FULLSTACK | CURRENT_REPO | `graph-projection-sync` | 图投影与同步 | Graph 集成 | ✅ 已生成 |
| 9 | HIGH | FULLSTACK | CURRENT_REPO | `dependency-impact-analysis` | 依赖影响分析 | 影响分析 | ✅ 已生成 |
| 10 | HIGH | FULLSTACK | CURRENT_REPO | `reproducible-simulation` | 可复现场景模拟 | 模拟引擎 | ✅ 已生成 |
| 11 | HIGH | FULLSTACK | CURRENT_REPO | `balance-risk-assessment` | 数值风险评估 | 风险分析 | ✅ 已生成 |
| 12 | HIGH | FULLSTACK | CURRENT_REPO | `ai-balance-design-workflow` | AI 数值设计闭环 | AI 设计 | ✅ 已生成 |
| 13 | HIGH | CROSS_CUTTING | CURRENT_REPO | `local-runtime-resilience` | 本地运行与降级 | 桌面运行 | ✅ 已生成 |
| 14 | HIGH | FULLSTACK | CURRENT_REPO | `project-backup-restore` | 项目备份恢复 | 数据可靠性 | ✅ 已生成 |

PRD 将 §3.1 和 §3.2 中的能力全部定义为 MVP“必须实现”，所以统一标记为 HIGH；实施顺序由依赖关系决定。

## 需求详情

### #1 [HIGH][BACKEND] graph-snapshot-lifecycle — 图快照生命周期

**来源依据**：§2.1、§3.1、§4、§7、§9.2、§12 阶段一、§13.1
**目标与摘要**：为不同项目保存隔离、不可变、可重建的 Graph Snapshot。全量或差量同步都创建新版本，保留节点、边、来源、显式/推测类别和置信度。
**用户流程**：`eco-guardian` 创建配置快照后提交图投影；`local-rag` 原子物化并构建索引；就绪后可查询，正式发布时再激活。失败时旧活动版本保持不变。
**前端变更**：无。
**后端变更**：在外部仓库建立 Namespace、Snapshot、Node、Edge、活动指针和索引状态；使用 SQLite 事务保证物化原子性。Namespace 首次同步时隐式创建。
**接口/事件契约**：
- `PUT /v1/graphs/{namespace}/snapshots/{version}`：支持 `mode=full` 或 `mode=delta`；delta 必须带 `base_version`。
- Snapshot PUT 不使用 Idempotency-Key；客户端提交最终物化图的 `content_hash`，服务端重新规范化计算并校验。相同版本/相同 hash 在 ready 时返回 200、building 时返回 202 和原 task_id；相同版本/不同 hash 返回 `409 CONTENT_HASH_CONFLICT`，hash 校验不一致返回 `422 CONTENT_HASH_MISMATCH`。
- `GET /v1/graphs/{namespace}/snapshots/{version}` 查询 version、base_version、content_hash、节点/边数量、task_id、`building/ready/failed` 及 graph/fts/vector 子状态。
- Graph 与 FTS 就绪是顶层 ready 的必要条件；Vector/Rerank 不可用只产生降级 warning。`POST /v1/graphs/{namespace}/snapshots/{version}/activate` 只接受 ready 快照并原子切换活动指针；building/failed 返回 `409 SNAPSHOT_NOT_READY`，重复激活返回成功且 `changed=false`。
- `DELETE /v1/graphs/{namespace}/snapshots/{version}` 只允许删除非活动且无写任务的快照。
**数据与配置**：节点包含 `id/type/label/text/properties/provenance`；边包含 `id/from/to/type/relation_kind/confidence/properties/provenance`。显式边 confidence 固定为 1；推测边必须记录模型或算法版本、时间和证据。
**验收标准**：
- 两个 Namespace、多个版本之间的节点、边和索引完全隔离。
- 差量创建结果与等价全量创建结果一致，基础快照保持不变。
- 同步或索引失败不改变当前活动版本；进程重启后状态和活动指针可恢复。
- 悬空边、重复 ID、基础版本不匹配均返回稳定错误且不会产生半成品快照。
**依赖**：无当前仓库硬依赖；需与 #8 协同维护稳定 ID、投影 payload、Schema 版本及内容 hash 消费者契约。
**不在范围**：分片流式导入、自动历史清理、业务规则理解、独立图数据库。
**待澄清**：无；不可变与增量冲突已按“差量创建新快照”解决。
**代码库依据**：`docs/implementation-plan.md`（PRD_STATED）；当前仓库无 `local-rag` 源码（EXTERNAL）。

### #2 [HIGH][BACKEND] constrained-graph-query — 受约束图查询

**来源依据**：§3.1、§4.1–§4.2、§11、§12 阶段一、§13.1
**目标与摘要**：基于指定快照提供不调用 LLM 的直接邻居、多层遍历和两点/多点路径查询，为确定性影响分析返回可复现证据。
**用户流程**：调用方给出起点或端点、版本、方向和过滤条件；服务返回有序节点、边和路径，达到上限时明确标记截断。
**前端变更**：无。
**后端变更**：实现循环安全的确定性 BFS、类型/方向/关系类别过滤、结果限制及逐边 provenance。
**接口/事件契约**：
- `POST /v1/graphs/{namespace}/traverse`：`max_depth` 默认 3、最大 6；`max_nodes` 默认和硬上限均为 500。
- `POST /v1/graphs/{namespace}/paths`：`max_depth` 默认 3、最大 6；`max_paths` 默认 20、最大 100。
- local-rag 契约允许不传 snapshot_version，此时使用请求开始时解析到的活动版本；eco 消费者为保证复现始终显式传版本。所有响应回显 `resolved_snapshot_version`。未传版本且没有活动快照返回 `NO_ACTIVE_SNAPSHOT`；显式指定且 ready 的候选快照无需激活即可查询。
- 默认仅查询 `explicit`；调用方必须显式请求 `inferred`。
- 超过硬上限返回 `400 LIMIT_EXCEEDED`；达到调用方限制返回 200、`truncated=true` 和原因。路径按跳数升序、推测边数量升序、最小边置信度降序、Edge ID 序列字典序升序；路径置信度取最小边置信度。traverse 节点按深度、Node ID 排序。
**数据与配置**：复用 #1 图数据，并建立 Namespace、Snapshot、端点和关系类型索引。
**验收标准**：
- 固定图谱重复查询得到完全一致的节点、路径和顺序。
- 循环图不会重复扩展或无限执行；达到软限制返回 `truncated=true`。
- 显式与推测关系可以独立过滤，所有边都能定位来源。
- 不存在节点、没有可解析活动版本和未就绪快照返回稳定错误码；显式 ready 候选快照在激活前可查询。
**依赖**：#1。
**不在范围**：无界全路径枚举、由 LLM 解释或改变确定性结果。
**待澄清**：无；默认版本、限制、排序和置信度规则已经确定。
**代码库依据**：`docs/implementation-plan.md`（PRD_STATED）；当前仓库未发现实现。

### #3 [HIGH][BACKEND] hybrid-graph-retrieval — 混合图检索

**来源依据**：§1、§3.1、§4.2、§6、§9.2、§12 阶段一、§13.1
**目标与摘要**：通过向量与 BM25 召回种子节点，再沿受约束关系图扩展和可选重排，返回可引用的上下文和证据。
**用户流程**：调用方提交自然语言问题和快照；服务返回种子、扩展结果、证据路径、各阶段得分及降级说明。
**前端变更**：无。
**后端变更**：使用 Reciprocal Rank Fusion（默认 `k=60`）融合 BM25/Vector；图扩展分数按 `seed_rrf_score / (1 + hop_count) * path_confidence` 计算；Rerank 作为可选最后阶段。
**接口/事件契约**：`POST /v1/graphs/{namespace}/retrieve` 接受 query、快照、类型过滤、`seed_limit`、`result_limit` 和 `graph_depth`；默认 limit 20、最大 100，图扩展深度默认 1、最大 3。
**数据与配置**：FTS5、sqlite-vec、Embedding/Rerank 模型版本及索引 generation。显式与推测证据分开返回。`local-rag` 支持存储推测边，但 MVP 不负责自动生成持久化推测边。保留集合为“活动版本 ∪ 最近 20 个版本”；驱逐只删除 FTS/Vector generation，不删除 Snapshot、Node 或 Edge。
**验收标准**：
- 固定问题召回预期种子并沿约束路径扩展，结果不跨 Namespace/Snapshot。
- Vector、BM25 或 Rerank 单项不可用时按能力矩阵降级并返回 warning。
- Vector 与 BM25 均不可用时返回 `503 RETRIEVAL_UNAVAILABLE`，是否可重试由故障原因决定。
- 每项结果可追溯到文本命中、种子或关系路径，并携带模型版本。
- 响应包含 mode_used、degraded、warnings、resolved_snapshot_version、各阶段得分、模型版本和 evidence。临时超时的 `RETRIEVAL_UNAVAILABLE` 可重试，能力未安装或未配置则不可重试。
- 被驱逐索引的 retrieve 返回 `409 SNAPSHOT_INDEX_NOT_READY` 和 `rebuild_required=true`；eco 显式重建并重试，retrieve 不隐式创建任务。traverse/paths 仍可查询该历史图。
**依赖**：#1、#2，以及 `local-rag` 既有 Embedding、FTS 和 Rerank 能力。
**不在范围**：从任意非结构化文档自动抽取完整知识图谱、社区发现和社区摘要。
**待澄清**：无；融合、排序、降级和索引保留策略已确定。仅活动版本和最近 20 个快照保留检索索引，旧快照按需重建。
**代码库依据**：`docs/implementation-plan.md`（PRD_STATED）；外部既有能力未经当前仓库源码验证（UNKNOWN）。

### #4 [HIGH][CROSS_CUTTING] graph-service-operability — 图服务运维保障

**来源依据**：§3.1、§10、§11、§12 阶段一、§13.1
**目标与摘要**：让宿主可靠检测 Graph 服务及 API 兼容性，观察任务、错误和降级状态，并安全重建派生索引。
**用户流程**：宿主启动时检查服务和能力；同步或重建时查看任务状态；派生索引损坏时从 Graph Snapshot 重建，源数据缺失则要求宿主重新同步。
**前端变更**：无；用户可见状态由 #13 展示。
**后端变更**：SQLite 持久化任务、进程内 worker、影子 generation 重建、结构化健康与统一错误。
**接口/事件契约**：
- `GET /health` 返回 status、service/API/schema 版本、capabilities、配置限制、SQLite/worker/model 状态。
- `POST /v1/graphs/{namespace}/snapshots/{version}/rebuild` 接受 `components=[fts,vector,graph_indexes]`，必须携带 Idempotency-Key，返回 202 和 task_id。来源仅为 local-rag Graph Snapshot 表；使用影子 generation，成功后原子切换，失败保留旧 generation。源图缺失统一返回 `REIMPORT_REQUIRED`。
- `GET /v1/tasks/{task_id}` 返回 `queued/running/succeeded/failed`、阶段、进度、warning 和统一错误。
- local-rag Task 只有上述四态且 MVP 无取消端点；eco Job 可以有 canceled/interrupted。外部 running Task 在服务重启时按幂等步骤重新进入 queued。
- `/health.status` 为 `ok/degraded/unavailable`；ok/degraded 返回 200，SQLite、迁移或核心图查询不可用返回 503，Vector/Rerank 故障只产生 degraded。响应包含 api_versions、supported_schema_versions、capabilities、limits 和依赖状态。
- 错误对象包含稳定 `code`、可展示 message、`retryable`、details 和 request_id；HTTP 使用 400/404/409/422/500/503。稳定错误至少包括 CONTENT_HASH_MISMATCH、CONTENT_HASH_CONFLICT、BASE_SNAPSHOT_NOT_FOUND、SNAPSHOT_NOT_READY、ACTIVE_SNAPSHOT_DELETE_FORBIDDEN、LIMIT_EXCEEDED、NO_ACTIVE_SNAPSHOT、SNAPSHOT_INDEX_NOT_READY、RETRIEVAL_UNAVAILABLE、REIMPORT_REQUIRED、TASK_NOT_FOUND 和 INTERNAL_ERROR。
**数据与配置**：任务记录、索引 generation、服务 SemVer 和 capability 列表；不新增业务事实。
**验收标准**：
- 健康检查能区分核心不可用和 Vector/Rerank 降级。
- 删除 FTS/Vector 派生索引后重建，固定查询结果等价；失败不会替换旧 generation。
- 任务在重启后保持并按幂等步骤恢复。
- provider/consumer 契约测试覆盖同步、激活、遍历、路径、检索、重建和错误格式。
**依赖**：健康框架可先行；完整验收依赖 #1–#3。
**不在范围**：独立任务服务、消息队列、远程运维平台、鉴权和网络暴露。
**待澄清**：无；重建优先使用本地 Graph Snapshot，源损坏时返回 `REIMPORT_REQUIRED`。
**代码库依据**：`docs/implementation-plan.md`（PRD_STATED）；当前仓库无外部服务实现。

### #5 [HIGH][FULLSTACK] domain-config-authoring — 领域配置编辑

**来源依据**：§3.2、§5、§9.1、§12 阶段二、§13.2 人工录入流程
**目标与摘要**：策划无需修改数据库或代码，即可创建、浏览、编辑、归档人物、技能、道具、效果、标签和属性，以及结构化公式、触发、目标和叠加配置。
**用户流程**：通过原生目录选择器创建或打开项目 → 选择对象类型 → 搜索/分页浏览 → 新建或编辑 → 即时基础校验 → 明确保存并生成不可变 config revision；存在冲突时保留输入并引导刷新或复制。切换项目前必须处理脏表单和运行任务。
**前端变更**：提供项目创建/打开/最近项目入口、六类对象列表和 Schema 驱动表单；覆盖加载、空态、搜索无结果、脏表单、保存中、冲突、失败重试、成功及不受支持扩展字段提示。桌面优先支持 1024px 以上布局、键盘操作和明确焦点。
**后端变更**：建立项目锁、Schema Registry、实体 CRUD、引用和标签索引。核心对象使用稳定 envelope 与版本化 JSON payload；服务端始终重新验证输入，并在一次事务中保存实体变化和新的 config revision。
**接口/事件契约**：
- `POST /api/v1/project-selections` 由后端调用 Windows 原生目录选择器并返回短期 selection token；浏览器不直接提交任意绝对路径。
- `POST /api/v1/projects` 使用 selection token 创建或打开项目；`POST /api/v1/projects/close` 关闭当前项目，切换由 close/open 编排。同一进程只有一个 active project。
- 切换时 restore/migration/release Job 必须完成；可取消 Job 先取消，已提交的外部 Graph Task 可停止等待并将 eco Job 标记 interrupted，重开项目后核对。
- `GET/POST /api/v1/entities/{kind}` 与 `GET/PATCH/DELETE /api/v1/entities/{kind}/{id}`。
- 修改使用 ETag/If-Match；冲突返回 `409 REVISION_CONFLICT`。DELETE 创建 tombstone，不物理清除历史；存在活动引用时返回 `409 ENTITY_REFERENCED` 并列出引用位置。
- OpenAPI 是契约事实源并生成 TypeScript client；错误使用 RFC 9457 Problem Details 加稳定业务码。
**数据与配置**：一个项目目录包含 `project.db`。`project_meta` 保存稳定 UUID；`working_entities` 保存 id、kind、key、entity_version、payload hash、状态和时间；规范化 JSON blob 按 hash 去重；引用、标签索引可重建。全局最近项目和设置保存在用户应用数据目录。

v1 Schema Registry 固定以下 `$id` 和最小字段：

- `urn:eco:schema:entity-envelope:1`：id、kind、key、name、description、tag_ids、balance_group、payload、extensions、entity_version、status。
- `urn:eco:schema:attribute:1`：value_type、dimension、base_unit、default/min/max、display_scale。
- `urn:eco:schema:tag:1`：category、parent_tag_ids。
- `urn:eco:schema:character:1`：attribute_values、skill_ids、item_ids、rule_blocks。
- `urn:eco:schema:skill:1`：costs、cooldown、target_selector、effect_ids、rule_blocks。
- `urn:eco:schema:item:1`：slot、effect_ids、attribute_modifiers、enhance_tag_ids、rule_blocks。
- `urn:eco:schema:effect:1`：duration、modifiers、trigger_blocks、stack_rule。

共享结构包括 FormulaBinding（输出属性、表达式）、TriggerRule（事件、条件、目标、效果、终止预算）、TargetSelector、Modifier 和 StackRule。内置事件为 on_use/on_hit/on_damage/on_heal/on_interval/on_effect_applied；目标选择为 self/source/primary_target/all_targets/targets_with_tag。数组引用基数均为 0..n；对象 key 在同项目同 kind 内唯一。新增字段或事件通过新 Schema 版本和编译期 Registry 扩展。
**验收标准**：
- 六类对象可新增、查询、修改和归档，重启后数据、稳定 ID 和引用保持正确。
- 每次明确保存都原子生成唯一、不可变的 config revision；失败不会留下半成品实体或修订。
- 项目移动或改名后 Namespace 不变；第二进程尝试写同一项目时得到清晰提示。
- 创建、打开、关闭和切换项目在脏表单、运行 Job 和文件锁场景下均有确定行为。
- 未知 `extensions` 字段原样保存但不参与当前版本校验、投影或模拟，并在 UI 标记。
- 多标签页并发修改不会静默覆盖；错误、加载和空态具备组件测试。
**依赖**：无；本需求内冻结 v1 领域 envelope 和 Schema Registry。
**不在范围**：用户动态安装 Go plugin、运行脚本、自定义任意 UI 插件、Excel 自动推断。
**待澄清**：无；一个项目一目录/数据库、UUIDv7、tombstone 和编译期模块注册已经确定。
**代码库依据**：`docs/implementation-plan.md`（PRD_STATED）；无 Go/Vue manifest 或源码（绿地/未发现）。

### #6 [HIGH][FULLSTACK] config-rule-validation — 配置规则校验

**来源依据**：§2.2、§3.2、§5、§11、§12 阶段二、§13.2 修正错误流程
**目标与摘要**：让策划在保存候选或发布前获得可定位、可重复的引用、公式、单位和循环依赖校验；确定错误不会被 AI 或人工说明绕过。
**用户流程**：编辑时得到字段级快速校验 → 保存后运行完整校验 → 按对象和严重级别查看结果 → 跳转错误字段修正 → 重新校验。
**前端变更**：公式编辑器、变量/函数提示、字段错误定位、完整校验摘要；区分 ERROR、BLOCK、WARNING 和 INFO，并支持过滤及从结果跳回表单。ERROR 表示请求/基础 Schema 无法保存；BLOCK 可以保存 revision 但禁止 Graph 同步、模拟和发布；WARNING/INFO 不阻断。
**后端变更**：实现版本化 DSL lexer/parser/AST、变量解析、类型与单位检查、引用校验、依赖图和循环分类；扩展函数必须编译期注册、纯确定性并声明签名和实现版本。
**接口/事件契约**：`POST /api/v1/validation/runs` 对工作草稿或不可变快照执行校验；结果带 entity_id、field_path、formula span、severity、code 和修复提示。保存实体只执行快速局部校验，创建候选与发布执行全量校验。
**数据与配置**：数值以规范化十进制字符串持久化，采用 decimal128 上下文：34 位有效数字、指数范围 -6143..6144、HALF_EVEN、只允许有限值；NaN、Infinity、除零和超出范围均为 BLOCK。Percentage 内部以比例表示（`1` 等于 100%），Duration 基准单位为毫秒。单位 Registry 固定 scalar、count、millisecond、ratio、resource_point、health_point、damage_point、healing_point、control_millisecond；同维度才能加减和比较，转换必须显式。DSL v1 支持 Decimal/Integer/Boolean/Duration/Percentage、作用域变量、`+ - * /`、比较/布尔运算以及 if/min/max/clamp/abs/floor/ceil/round 白名单。禁止循环、I/O、反射、动态调用和隐式随机。叠加采用 Add/Multiply/Override/Max/Min 枚举、显式优先级与上限。公式源码只存在 entity payload 中；compiled AST、formula index、entity references 都是按 config revision 和 field path 可重建的派生索引。
**验收标准**：
- 非法引用、函数、类型、单位、除零、溢出范围和静态公式环均稳定报错并定位。
- 有限事件循环必须声明次数、持续时间或资源预算；无界事件和无上限叠加为不可覆盖错误。
- Parser fuzz、AST golden、精度/舍入边界及引用删除均有自动测试。
- decimal 指数、规范化序列化、Percentage/Duration 转换和单位维度均有跨版本 golden 测试。
- 同一快照、DSL/数值策略版本产生完全一致的校验结果。
**依赖**：#5。
**不在范围**：任意表达式执行、用户脚本、通用编程语言、由 LLM 判定公式正确性。
**待澄清**：无；v1 类型、数值策略、函数扩展和有界事件原则已经确定。
**代码库依据**：`docs/implementation-plan.md`（PRD_STATED）；规则引擎为绿地建设。

### #6.5 [HIGH][BACKEND] typed-rule-materialization — 类型化规则物化

**来源依据**：§2.2、§3.2、§5、§7–§8、§12 阶段三、§13.2
**目标与摘要**：将已通过完整校验的不可变 config revision 物化为 FormulaBinding、TriggerRule、Modifier 与 StackRule 的冻结类型化规则图，供模拟等确定性消费者使用；禁止消费者重新解析通用 JSON 或建立平行规则模型。
**用户流程**：该能力不新增直接 UI。用户保存并完成 FULL 校验后，模拟等下游按明确 revision 读取物化结果；若校验、版本或派生工件不匹配，下游显示明确不可用原因并不能启动计算。
**前端变更**：无直接页面或 HTTP 写操作；#10 在模拟启动失败时复用统一诊断展示物化不可用、版本漂移或缺失工件。
**后端变更**：新增 transport-neutral RuleSet port 与 revision adapter，使用 #5 immutable manifest、#6 canonical AST/index 与精确 PASS FULL validation certification，产出带稳定 rule ID、field path、ordinal、来源、引用、完整语义版本与 materialization hash 的不可变 typed DTO。Trigger/Stack 的安全扫描保持 #6 validation 内部实现；RuleSet 只携带 certifying validation result identity/hash，不复制该扫描器。物化只遍历 v1 已知结构；未知 extensions 保持不透明，不参与当前行为。
**接口/事件契约**：内部 `RuleMaterializer.Materialize(revision_id)` 仅接受显式 immutable revision，要求对应 config hash 与 Schema/DSL/AST/Registry/NumericPolicy 版本完全匹配的 PASS FULL validation；返回 RuleSet、canonical bytes/hash 或稳定诊断。规则结构从 immutable manifest 的已知 v1 字段解码，公式只复用已保存 canonical AST，不重新解析；不得读取 working state、active-release 指针、Graph 或 AI 输出；不得执行公式、调度事件或修改 revision。
**数据与配置**：RuleSet 为派生数据，可按 `revision/config/version/materialization hash` 缓存并在校验后重建；revision payload、manifest 和 validation artifacts 仍是唯一事实源。hash 包含物化语义、canonical AST 和 provenance，不包含 request/run ID、墙钟、数据库行 ID、显示字段或 worker 顺序。
**验收标准**：
- 同一 revision 在重启、不同数据库行顺序和不同 worker 环境中得到字节级等价的有序 RuleSet、rule ID 与 materialization hash。
- 缺少、陈旧、BLOCK/ERROR 或版本不匹配的 FULL validation，缺失/重复 AST/index、悬空引用、未知 v1 enum、非法 quantity/span 均稳定失败且不返回部分规则。
- FormulaBinding、TriggerRule、Modifier、StackRule 均保留 source entity、field path、ordinal、类型化字段、已解析引用及可追溯的 PASS FULL validation identity/hash；未知 extension 不进入规则图。
- 缓存损坏或丢失只会由不可变工件重建，绝不改写 revision、validation result 或既有 simulation 结果。
**依赖**：#5、#6；为 #10 的 stateful evaluator adapter 提供硬实现前置，不反向依赖 Graph、风险、AI、release 或 UI。
**不在范围**：修改领域 Schema 或 validation severity、重新实现 DSL/decimal/unit/reference/safety、公式执行、通用规则脚本、模拟状态引擎、HTTP endpoint 或配置编辑。Windows 10/11 容量与端到端场景继续由 #10 的 capacity/E2E 验收负责。
**待澄清**：无；v1 仅覆盖现有已冻结 schema，新增规则字段必须通过新的 Schema/Registry/materialization 版本引入。
**代码库依据**：`internal/validation/formula_index.go` 与 `internal/validation/rules.go` 当前只提供 validation-oriented index/JSON scan；`internal/simulation/engine/run.go` 目前只接收 event evaluator callback，尚无 typed rule input。

### #7 [HIGH][FULLSTACK] versioned-config-workflow — 配置版本管理

**来源依据**：§3.2、§7、§11、§12 阶段二、§13.2
**目标与摘要**：支持编辑中的工作草稿、不可变 config revision、字段级差异、风险复核和只读 release；所有历史结果可定位到同一份输入。candidate 是被选中执行门禁的 revision 状态，不是数据副本。
**用户流程**：编辑工作草稿并明确保存成 config revision → 可选添加名称/说明 → 与当前 release 比较 → 完成校验/同步/模拟/风险复核 → 强制备份 → 填写发布说明并发布；回滚时从旧 release 指向的 revision 创建新 revision。
**前端变更**：版本列表、状态时间线、字段级 diff、无基准提示、发布检查清单、warning/block 说明和发布确认。
**后端变更**：内容寻址 revision、对象 blob 去重、差异计算、版本化 release policy、发布状态机及可恢复 saga。发布先完成强制备份，再记录 intent、幂等激活 Graph Snapshot，成功后事务切换 release 指针；崩溃后恢复 intent。所有分析显式使用 config_revision_id。
**接口/事件契约**：
- 实体写操作在同一事务生成不可变 revision；`POST /api/v1/revisions` 用于在没有实体变化时创建命名检查点。
- `GET /api/v1/revisions/{id}` 和 `GET /api/v1/revisions/{id}/diff?base={id}`。
- `GET/POST /api/v1/release-policies` 查询或创建版本化必检场景、Metric、样本、阈值和 capability 策略。
- `POST /api/v1/releases` 创建发布 Job；服务离线、确定校验失败或必需分析缺失时拒绝。
- `GET /api/v1/jobs/{id}` 与 SSE `/api/v1/jobs/{id}/events` 返回发布进度；轮询作为后备。
**数据与配置**：`config_revisions`、`revision_entities`、`release_policies`、`release_intents`、`releases` 和 active release 指针。公式、引用和关系派生索引不作为 revision 的第二事实源。revision 保存 Schema、DSL、projector、模拟引擎和数值策略版本。起始 release policy 要求 30 秒单目标与 60 秒极限叠层场景、至少一个必需 Metric、默认 1000 样本及已启用阈值版本；180 秒单目标和 60 秒三目标初始为可选。用户可创建新 policy 版本。
**验收标准**：
- config revision 不可原地修改，release 只保存指针和发布元数据；字段 diff 对新增、删除、移动和修改均稳定。
- Graph 激活前后每个故障点都能在重启后恢复，不出现无记录的半发布状态。
- 正式发布必须通过确定校验；数值 BLOCK 需要明确覆盖说明，公式环和无界事件不可覆盖。
- 首次发布仍执行 release policy 中的必检模拟，风险结果为 `NO_BASELINE`，用户确认“建立基线”后允许发布；后续发布必须与当前 release 比较。
- release policy 缺失、必需场景/Metric 不可用、阈值未启用或发布前备份失败时禁止发布；可选 Metric 不可用只产生 WARNING。
- 回滚不会修改旧版本，只会产生新的候选和正式版本。
**依赖**：硬实现依赖只有 #5、#6。本需求提供 Gate 注册接口；#8、#10、#11、#14 分别接入 Graph、模拟/风险和备份门禁，不作为 #7 的反向硬依赖。所有必需 Gate 注册完成前发布 capability 保持禁用。
**不在范围**：多分支合并、多人审批、权限角色、修改历史 config revision 或 release。
**待澄清**：无；版本状态机、发布门禁、回滚和跨系统 saga 已确定。
**代码库依据**：`docs/implementation-plan.md`（PRD_STATED）；版本存储为绿地建设。

### #8 [HIGH][FULLSTACK] graph-projection-sync — 图投影与同步

**来源依据**：§2.1–§2.2、§3.2、§4.2、§7、§10–§13.2
**目标与摘要**：把配置快照确定性投影为稳定节点、显式关系和检索文本，并可靠同步到同版本 Graph Snapshot。
**用户流程**：保存 revision 后系统自动全量校验 → 无 BLOCK 时自动排队同步 → UI 显示进度和版本/数量校验 → 成功后排队影响分析 → 失败时继续编辑并可一键重试。
**前端变更**：revision 和全局服务状态中显示 saved/validating/blocked_validation/graph_queued/graph_building/graph_ready/graph_failed/陈旧，展示节点数、边数、graph manifest hash、错误和重试。
**后端变更**：建立版本化投影器注册接口、稳定 ID、关系类型 Registry、Graph client、幂等 Job、指数退避和消费者契约 fixture。投影节点 ID 使用 `urn:eco:{project_uuid}:{kind}:{entity_uuid}`；Edge ID 为 `source_node_id|relation_type|target_node_id|source_field_path|ordinal` 规范串的 SHA-256 Base32；显式边携带实体字段或公式 AST span 来源。只对幂等请求最多重试 3 次。每个 config revision 固化 projection_schema_version 和 projector_version，旧 revision 始终使用原版本投影器。
**接口/事件契约**：内部 `POST /api/v1/revisions/{id}/graph-sync` 创建 eco Job，`GET /api/v1/revisions/{id}/graph-status` 查询；外部调用 #1 和 #4 的同步、状态与健康接口。Snapshot PUT 以 namespace/version/graph_manifest_hash 幂等，不发送 Idempotency-Key；request_id 始终发送。local-rag 对缺失 base 返回 `BASE_SNAPSHOT_NOT_FOUND` 且不创建目标，eco 捕获后重新投影并发送 full。
**数据与配置**：`graph_sync_states`、Job、projection_schema_version、projector_version、config_hash、graph_manifest_hash、投影摘要和最后错误；Graph 数据仍为派生数据。关系 Registry v1 固定 character_has_skill、character_uses_item、skill_applies_effect、item_applies_effect、formula_reads_attribute、effect_modifies_attribute、entity_has_tag、item_enhances_tag、effect_triggers_effect，方向均为字段拥有者到被引用对象；影响查询沿反向依赖解释。MVP 不把 AI 推测写回业务事实；疑似影响主要来自 #3 检索证据。
**验收标准**：
- 同一业务快照重复投影得到相同节点、边、来源、顺序和 hash。
- delta 与 full 同步结果等价；delta hash 是最终物化图而不是差量 payload。基础版本缺失或不匹配时，由 eco 收到稳定错误后重新发送 full；删除节点同时移除继承关联边并再次校验悬空边。
- 网络失败、进程退出、重复请求和版本冲突不会损坏业务快照或产生重复 Graph 版本。
- 服务恢复后自动重试待同步 Job，用户也可手动重试；正式发布前必须校验版本和数量。
- 相同 revision/pipeline stage/input hash 的重复事件不会创建重复 Job。外部 Task 接受后不能取消；用户可以停止等待，eco Job 记为 interrupted 并在重开项目后核对状态。
- projector 升级后，旧 revision 仍能用记录的版本生成原 graph_manifest_hash；新投影逻辑只应用于新 revision。
**依赖**：#1、#4、#5–#7。
**不在范围**：在 `local-rag` 内实现业务规则、把 Graph 数据作为唯一事实源、AI 自动生成持久化确定关系。
**待澄清**：无；稳定 ID、同步状态机、重试、全量回退和发布边界已确定。
**代码库依据**：`docs/implementation-plan.md`（PRD_STATED）；当前无 Graph client 或投影实现（绿地）。

### #9 [HIGH][FULLSTACK] dependency-impact-analysis — 依赖影响分析

**来源依据**：§1、§2.2、§3.2、§4.2、§11、§13.2
**目标与摘要**：对象变化后自动展示直接、间接和标签规则影响，并通过证据路径解释原因；确定影响和疑似影响严格分区。
**用户流程**：revision 全量校验通过并达到 graph_ready，或用户选择两个 graph_ready revision → 系统排队分析 → 查看影响对象和默认最短路径 → 按类型/方向/深度过滤 → 展开其他路径或疑似证据。
**前端变更**：影响清单、关系图和文本路径替代视图；覆盖无变化、无路径、截断、Graph 未同步、服务降级、加载、取消和重试。显式/疑似结果使用文字标签和图例，不能只依赖颜色。
**后端变更**：比较快照变更集合，调用 `traverse/paths` 获得确定路径，按需调用 `retrieve` 获得疑似关联；保存分析输入和结果摘要。确定结果默认显式关系、深度 3，达到上限时提示缩小范围。
**接口/事件契约**：`POST /api/v1/impact-analyses` 绑定不可变 config_revision/base_revision 并创建 Job；`GET /api/v1/impact-analyses/{id}` 查询历史详情。结果返回 changed entities、affected entities、paths、relation_kind、confidence、provenance、truncated 和 warnings。唯一自动触发链为 `saved → validating → blocked_validation | graph_queued → graph_building → graph_ready → impact_queued → impact_ready/failed`。
**数据与配置**：`impact_reports` 保存输入 hash、Graph 版本、过滤条件、路径和证据引用；Graph 查询结果不是业务事实。
**验收标准**：
- 固定修改返回预期直接、多层和标签依赖以及逐边来源。
- 疑似结果携带来源和置信度、默认折叠且永不参与发布阻断。
- 路径截断、无路径和服务不可用都给出可操作反馈，不伪装成“无影响”。
- 关系图可通过键盘选择节点，并提供等价文本路径。
- Graph ready 前不会创建影响 Job；重复状态事件按 revision/input hash 去重。
**依赖**：#2、#3、#8。
**不在范围**：由自然语言证据产生确定关系、无界路径枚举、把相似性当作计算因果。
**待澄清**：无；保存成功后自动触发、默认深度和展示规则已确定。
**代码库依据**：`docs/implementation-plan.md`（PRD_STATED）；UI 和分析编排均为绿地。

### #10 [HIGH][FULLSTACK] reproducible-simulation — 可复现场景模拟

**来源依据**：§3.2、§5、§7–§8、§12 阶段三、§13.2
**目标与摘要**：使用受限、确定性的事件调度器运行少量固定场景，得到可复现指标；不建设任意游戏机制的通用战斗引擎。
**用户流程**：选择 config revision 或 release 指向的 revision 和可用场景 → 调整允许的场景参数 → 启动并查看进度 → 查看指标、置信区间和假设 → 保存结果或取消。
**前端变更**：场景/版本选择、参数校验、任务进度、取消、超时、失败重试、指标不可用原因和历史结果。
**后端变更**：版本化场景模板、稳定事件排序、引擎自有 PRNG、指标模块注册接口和资源预算。内置可克隆模板：30 秒单目标、180 秒单目标、60 秒三目标、60 秒极限叠层。
**接口/事件契约**：`POST /api/v1/simulation-jobs` 创建绑定 config revision 的 Job；`GET /api/v1/simulation-runs/{id}` 查询历史详情。结果回显 config_revision_id、scene、engine、numeric policy、metric modules、sample count、seed、input/result hash。随机场景默认 1000 样本，可取消。
**数据与配置**：`scenario_definitions`、`simulation_runs`、指标结果和复现元数据。事件按时间、规则优先级、来源 ID、序号稳定排序；数值使用 #6 策略。
**验收标准**：
- 相同快照、场景、引擎、策略、样本和种子得到相同结果 hash。
- DPS、治疗、生存、资源和控制作为独立模块；缺少结构化规则时明确返回不可用原因，不做估算。
- 任务取消、预算超限和进程中断有稳定状态；确定性任务可以重新运行。
- v1 二进制内置其 major 版本曾写入项目的全部 evaluator；同一 evaluator version 永不改变语义。未来移除只能通过新的 major change 和兼容运行器/迁移方案。若应用缺少项目引用的 evaluator，允许只读查看已存结果但禁止声称“已复现”或基于它发布。
**依赖**：#5–#7、#6.5。#6.5 是 stateful rule evaluator 的硬实现前置：模拟必须从已校验 immutable RuleSet 读取类型化规则并将其 materialization hash 纳入 input fingerprint。
**不在范围**：通用战斗引擎、任意脚本事件、未建模机制的猜测结果。
**待澄清**：无；首批模板、数值策略、默认样本、稳定排序和版本保留已经确定。
**代码库依据**：`docs/implementation-plan.md`（PRD_STATED）；模拟器为绿地建设。

### #11 [HIGH][FULLSTACK] balance-risk-assessment — 数值风险评估

**来源依据**：§1、§3.2、§7–§8、§11–§13.2
**目标与摘要**：比较候选与基准版本在同一场景下的指标，以版本化阈值和静态规则提示数值膨胀，并保存策划处理结论。
**用户流程**：选择候选及基准 → 自动运行或复用模拟 → 查看指标差异、同定位集合、警戒/阻断和原因 → 调整配置或填写覆盖说明 → 形成发布复核报告。
**前端变更**：指标对比、阈值边界、同定位对象、结构风险、计算假设和 warning/block 处理；无基准、指标不可用、结果陈旧和任务失败均有明确状态。
**后端变更**：版本化阈值、cohort 比较、新乘区/重复乘算/静态环/无界事件检查和不可变风险报告。对象 envelope 使用可选 `balance_group` 形成同定位集合；未设置时只比较同 stable ID 对象。每个 Metric Module 声明 `higher_is_risk`、`lower_is_risk` 或 `target_range` 风险方向。
**接口/事件契约**：`POST /api/v1/risk-reviews` 绑定 candidate、baseline、simulation results 和 threshold version；`GET /api/v1/risk-reviews/{id}` 查询历史详情。结果包含绝对/相对差异、comparison_status、severity、rule、evidence、assumptions 和 override 状态。
**数据与配置**：系统提供按指标相对变化 10% 警戒、25% 阻断的起始模板，项目首次使用风险功能时必须明确启用或修改；未启用时显示“阈值未配置”，不能显示“安全”。阈值可按项目和定位修改并版本化。静态公式环、无界事件和无上限叠加属于不可覆盖校验错误；数值 BLOCK 可由用户填写原因后覆盖。
**验收标准**：
- 正负方向、10%/25% 边界、绝对阈值和 cohort 选择有确定测试。
- 报告保存基准、候选、场景、阈值、指标差异、风险原因、假设和用户说明。
- 陈旧模拟或阈值版本不匹配时禁止直接复用并明确提示。
- 疑似 Graph 影响只作解释证据，不改变风险等级。
- 首次发布报告状态为 NO_BASELINE；必检模拟成功且用户确认建立基线后通过。后续基准为当前 release。
- 基准值为 0 时不计算相对变化，优先使用 Metric 配置的绝对阈值；缺少绝对阈值时返回 NOT_COMPARABLE。必需 Metric 的 NOT_COMPARABLE 阻止发布，可选 Metric 只产生 WARNING。
**依赖**：#6、#7、#10；#9 可提供补充路径证据。
**不在范围**：脱离场景给出绝对平衡结论、自动替用户接受风险。
**待澄清**：无；默认阈值、定位字段、阻断覆盖和不可覆盖错误边界已经确定。
**代码库依据**：`docs/implementation-plan.md`（PRD_STATED）；风险模块为绿地建设。

### #12 [HIGH][FULLSTACK] ai-balance-design-workflow — AI 数值设计闭环

**来源依据**：§3.2、§6–§8、§11–§13.2
**目标与摘要**：策划输入定位、目标指标和约束后，获得带证据的类型化 DraftPatch，并复用既有校验、模拟、风险和人工确认链路。
**用户流程**：配置 Provider → 输入目标和约束 → Graph RAG 检索 → AI 生成候选补丁 → 自动校验/模拟/确定性搜索 → 查看差异、证据和假设 → 接受到工作草稿或继续调整。
**前端变更**：Provider 就绪状态、目标/约束表单、任务进度和取消、候选 diff、证据、校验/模拟反馈、三轮上限、接受或放弃。无证据、非法输出、服务超时和结果陈旧均不能进入确认状态。
**后端变更**：OpenAI-compatible Provider 接口支持本地和可选云端；模型和端点放全局设置，密钥存 Windows Credential Manager，环境变量作为不持久化后备。AI 仅生成带 expected revision 的类型化 DraftPatch；数值区间优先使用网格/约束搜索。
**接口/事件契约**：`POST /api/v1/ai-design-jobs` 创建 Job；`GET /api/v1/draft-patches/{id}` 返回补丁与原始 diff，`POST /api/v1/draft-patches/{id}/accept` 原子接受多实体补丁并生成一个 config revision，`POST /api/v1/draft-patches/{id}/discard` 放弃。接受请求携带 base config_revision_id 和每个目标 entity_version，冲突返回 `409 REVISION_CONFLICT`。`GET/PATCH /api/v1/settings` 读写非敏感 Provider/local-rag/备份设置，`PUT/DELETE /api/v1/settings/credentials/{provider}` 通过 Credential Manager 写入或清除密钥。响应/审计包含 provider、model、Prompt/Schema 版本、目标、约束、evidence refs、原始结构化响应、补丁、模拟、假设和错误。自动格式修复最多 3 轮，AI 中断后不自动重试。
**数据与配置**：`ai_design_runs`、`draft_patches` 和审计记录，不保存密钥。接受补丁使用 If-Match 写入工作草稿并生成 config revision；补丁不能调用发布接口或修改 revision/release。
**验收标准**：
- 非法 JSON、越权路径、revision 冲突、无证据和校验失败均不会写入草稿。
- 接受后的草稿必须再次经过服务端 Schema、引用、公式校验和模拟。
- 所有代码路径都不能让 AI 直接创建或修改正式版本。
- Provider 超时、取消、三轮上限和确定性搜索预算均有自动测试。
**依赖**：#3、#6–#11。
**不在范围**：AI 直接发布、在项目数据库保存密钥、无限自主迭代、用 AI 替代确定性搜索或校验。
**待澄清**：无；Provider、凭据、DraftPatch、证据门禁、重试和审计策略已确定。
**代码库依据**：`docs/implementation-plan.md`（PRD_STATED）；AI Provider 与工作流均为绿地。

### #13 [HIGH][CROSS_CUTTING] local-runtime-resilience — 本地运行与降级

**来源依据**：§1、§3.2、§9.1、§10–§13.2
**目标与摘要**：用户通过一个入口在本机启动完整工具；依赖故障时保持离线编辑、校验、版本和备份能力，并清楚说明受影响功能。
**用户流程**：启动 `eco-guardian.exe` → 自动打开最近项目和浏览器 → 检测或启动兼容 Graph 服务 → 首次使用 AI 时通过向导配置本地或云端 Provider → 使用工具；服务异常时继续离线工作，恢复后自动重试待同步任务。
**前端变更**：全局服务状态、降级 banner、功能禁用原因、重连、日志位置和手动重试；浏览器打开失败时终端/日志显示可复制本地地址。
**后端变更**：前端、迁移和默认配置嵌入单一 exe；仅监听 `127.0.0.1` 随机端口。“完整 Graph 本地包”附固定兼容版 `local-rag`、Python runtime 和 Embedding/Rerank 模型，但不捆绑 LLM；AI 需要在首次向导配置本地或云端 OpenAI-compatible Provider。轻量包连接已有服务。只管理自身启动的子进程，已有外部进程绝不终止。
**接口/事件契约**：启动时调用 #4 `/health`，以支持 `/v1`、Schema 1.0、必需 capability 和最低修复版本判定兼容；SemVer 主要用于诊断，不因实现自身 major 不同而拒绝仍完整支持契约的服务。eco 长任务统一 `queued/running/succeeded/failed/canceled/interrupted`，SSE 推送、轮询后备；local-rag Task 保持四态且无取消端点。
**数据与配置**：全局设置、最近项目、运行日志和非业务进程状态放 `%LOCALAPPDATA%/EcoGuardian`；项目业务事实只在 `project.db`。Windows Job Object 回收自有子进程，日志轮转。
**验收标准**：
- 在干净 Windows 10/11 x64 环境解压后无需开发工具即可启动完整 Graph 本地包；AI 首次使用需要配置 Provider。
- 端口占用、模型缺失、版本不兼容、浏览器启动失败和 Graph 超时都有明确降级。
- Graph 不可用时仍可编辑、校验、创建候选和备份，但影响分析、AI 与正式发布禁用。
- 恢复后待同步任务可自动或手动重试；关闭时只终止本进程启动的子进程。
- Graph 核心可用但 Vector/Rerank 降级时确定性遍历、模拟和编辑保持可用，检索/AI 显示局部降级；AI Provider 单独不可用时只有 AI 禁用；各 capability 恢复后分别重新启用。
**依赖**：最小运行骨架可先行；完整验收依赖 #4、#8。
**不在范围**：macOS/Linux 正式发行、远程监听、多用户服务、自动终止用户自行启动的进程。
**待澄清**：无；首发 OS、双发行包、进程所有权、端口和降级边界已确定。
**代码库依据**：`docs/implementation-plan.md`（PRD_STATED）；无构建或打包配置（绿地）。

### #14 [HIGH][FULLSTACK] project-backup-restore — 项目备份恢复

**来源依据**：§3.2、§10–§11、§13.2
**目标与摘要**：用户可以对业务事实源执行一致性备份和安全恢复；Graph 索引缺失时从业务快照重建，不要求备份派生索引。
**用户流程**：手动备份或系统按策略自动备份 → 查看时间、类型、版本和校验状态 → 对活动项目选择 UUID 匹配的备份恢复 → 系统创建恢复前备份、校验并替换数据库 → 重开项目并核对/重建 Graph。
**前端变更**：备份列表、手动备份、保留说明、进度、checksum/兼容错误、恢复确认和恢复后同步状态。
**后端变更**：SQLite Online Backup API 写临时文件，执行 `integrity_check` 和 checksum 后原子改名。恢复时进入维护模式、取得独占锁、创建恢复前备份、原子替换并在失败时回滚。
**接口/事件契约**：`POST /api/v1/backups`、`GET /api/v1/backups`、`POST /api/v1/restores` 均使用 Job；恢复只接受相同或更旧 Schema，旧库升级前再次备份，拒绝较新数据库降级。活动项目原地恢复必须 project UUID 匹配；恢复到空目录被视为恢复同一逻辑项目并保留 UUID。MVP 不支持“恢复为副本”；全局注册表发现同一 UUID 对应另一现存路径时要求用户明确迁移注册，应用不会同时打开两份。
**数据与配置**：默认备份目录为用户“文档/EcoGuardian Backups/{project_uuid}”，可在本机设置中修改。备份包包含 `project.db`、manifest、项目 UUID、应用/Schema 版本和 SHA-256，不包含 `local-rag` 索引。迁移前、恢复前和正式发布前强制备份；每日第一次业务变更事务前自动备份，Job/event 等系统元数据写入不触发。日常备份失败时普通编辑可由用户明确“本日继续但无恢复点”，而迁移、恢复和发布前备份失败不可绕过。保留最近 10 个日常和 5 个发布/迁移备份，手动备份不自动删除。
**验收标准**：
- 并发写入期间得到一致备份；磁盘满、权限错误和损坏 checksum 不产生可选恢复项。
- 恢复失败自动回到恢复前数据库；成功后项目、版本、报告和待同步状态正确。
- 项目移动或恢复不改变 project UUID；Graph 状态置为待核对并可重新同步。
- 较新 Schema 备份被明确拒绝，不进行不可逆降级。
**依赖**：硬实现依赖 #5；#7、#8、#11 通过整库备份与恢复后 hook 做集成验收，不是 #14 的前置硬依赖。迁移前备份必须在首个真实数据版本前交付。
**不在范围**：备份正在写入的 SQLite 文件副本、云端备份、Graph 索引作为强制备份内容。
**待澄清**：无；备份触发、保留、兼容、恢复锁和 Graph 重建策略已确定。
**代码库依据**：`docs/implementation-plan.md`（PRD_STATED）；备份实现为绿地建设。
