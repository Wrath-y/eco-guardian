## Purpose

定义一个人在回路、证据可追溯且失败可降级的 AI 数值设计工作流，使模型只能为固定基础 revision 生成类型化 DraftPatch 提案，并由用户通过既有确定性服务审阅和应用，而不能直接修改事实或发布。

## Requirements

### Requirement: Provider 配置、能力与凭据隔离
系统 SHALL 支持 OpenAI-compatible 的 loopback 本地 Provider 和用户主动配置的云端 Provider。非敏感 endpoint、model、超时及启用状态 SHALL 通过全局设置管理；secret SHALL 只写入 Windows Credential Manager，或从不持久化的环境变量后备读取，并且 SHALL NOT 出现在项目数据库、设置响应、日志、审计、错误详情或备份中。AI capability SHALL 独立报告未配置、可用、降级和不可用状态，且 SHALL NOT 影响编辑、校验、版本、模拟、风险和备份 capability。

#### Scenario: 配置本地 Provider
- **WHEN** 用户保存合法的 loopback endpoint、model 和 Credential Manager 凭据并通过能力检测
- **THEN** AI capability 显示可用，设置读取只返回凭据存在状态而不返回 secret

#### Scenario: Provider 未配置时安全降级
- **WHEN** 没有可用 Provider 或模型能力不兼容结构化输出契约
- **THEN** 系统禁用创建 AI 提案并显示可操作原因，同时人工编辑和所有确定性能力保持可用

#### Scenario: 拒绝凭据泄漏
- **WHEN** Provider 返回包含请求诊断的错误或用户导出项目备份
- **THEN** secret 不出现在错误、日志、AI 审计或备份内容中

### Requirement: 人工定义并冻结设计输入
创建 AI design Job SHALL 要求用户选择一个不可变 base `config_revision_id`，提交目标、目标指标、约束、允许修改的实体 stable ID 与字段路径、场景选择和有界预算。系统 SHALL 在接纳 Job 前解析并冻结 base revision 的 config/version identities、每个目标 `entity_version`、当前 release baseline（存在时）、目标与约束、允许范围和默认展开后的预算；运行中 working state、显示名称或 active release 的变化 SHALL NOT 悄然改变输入。

#### Scenario: 创建固定输入的设计 Job
- **WHEN** 用户对一个可物化且具有匹配 FULL validation 结果的 revision 提交完整目标、约束和允许修改范围
- **THEN** 系统返回 202 Job，并保存不可变输入 manifest 与 hash

#### Scenario: 拒绝不确定修改范围
- **WHEN** 请求省略允许修改字段、引用不存在的 stable ID、使用显示名称代替 stable ID 或预算超出发布上限
- **THEN** 系统在调用检索或 Provider 前拒绝请求且不创建可运行 Job

#### Scenario: 运行期间事实发生变化
- **WHEN** Job 运行时 working draft 或 active release 改变
- **THEN** Job 仍只读取冻结的 base revision/baseline，并在审阅时单独标记 freshness，不混用新事实

### Requirement: 固定快照的可引用检索证据
每个可生成提案的运行 SHALL 通过 local-rag `POST /v1/graphs/{namespace}/retrieve` 获取上下文，显式传递项目 namespace、等于 base `config_revision_id` 的 ready Snapshot、精确过滤与不超过 provider 上限的 `seed_limit`、`result_limit` 和 `graph_depth`；默认关系种类 SHALL 为 `explicit`。系统 SHALL 验证响应的 `resolved_snapshot_version` 和 `content_hash` 精确匹配 #8 Graph identity，并保存结果 Node、citation text、seed/path evidence、关系种类、置信/阶段分数、FTS/Vector generation、算法/model identity、mode、degraded 和 warnings 的稳定引用。检索结果 SHALL 只是提案证据，不能写成业务事实或确定因果。

#### Scenario: 固定成功证据
- **WHEN** local-rag 对显式 base Snapshot 返回身份匹配且至少一个可引用结果
- **THEN** 系统冻结 evidence refs 和检索 manifest，Provider 只能引用这些 evidence identities

#### Scenario: 拒绝混合快照证据
- **WHEN** 检索响应的 resolved Snapshot 或 content hash 与 base Graph identity 不一致
- **THEN** 本次尝试失败且不调用 Provider、不保存可审阅 DraftPatch

#### Scenario: 接受可用的检索降级
- **WHEN** local-rag 使用 `bm25_only` 或 `vector_only` 返回可引用结果和降级 warning
- **THEN** 系统可继续生成提案，但 SHALL 在候选与审计中显示实际 mode、model/generation identities 和 warning

#### Scenario: 无证据或索引未就绪
- **WHEN** 检索为空、两种基础召回均不可用，或返回 `SNAPSHOT_INDEX_NOT_READY`
- **THEN** 系统不允许提案进入确认状态，显示稳定失败与 `rebuild_required` 等下一步，且 retrieve 本身不隐式创建重建任务

### Requirement: 结构化工具白名单与最小权限
AI 编排器 SHALL 只暴露版本化、JSON Schema 约束的工具白名单：读取冻结 revision/diff、执行上述证据检索、验证 proposal materialization、运行既有固定场景模拟预览、运行既有风险规则预览，以及执行有界确定性参数搜索。检索工具 SHALL 只接受服务端从冻结输入生成的请求，并 SHALL 在 Provider 生成前固定 evidence manifest；模型不能修改 query、Snapshot、filters 或 limits，也不能在生成中追加新证据。所有工具 SHALL 由服务端验证参数、base identity、允许实体/路径、调用次数、结果大小和时间预算；模型 SHALL NOT 获得 Repository、任意 SQL/HTTP/文件/进程、working draft mutation、revision creation、credential 或 release/Graph activation 工具。工具结果 SHALL 带稳定 identity/hash 并按调用序记录。

#### Scenario: 执行允许的只读工具
- **WHEN** 模型发出符合当前工具 Schema、base identity 和预算的模拟预览调用
- **THEN** 编排器调用既有确定性能力并把带版本/hash 的结构化结果返回给模型和审计

#### Scenario: 拒绝越权工具或参数
- **WHEN** 模型请求未注册工具、修改不允许字段、访问另一个 revision、进行任意网络调用或超出预算
- **THEN** 编排器拒绝调用，记录稳定 policy error，且不发生外部副作用

#### Scenario: 确定性搜索优先
- **WHEN** 允许修改的数值字段和约束能够表示为有界区间或离散候选
- **THEN** 系统使用版本化网格/约束搜索评估候选，AI 只解释和选择已计算结果而不伪造搜索值

### Requirement: 模型、Prompt、Schema、工具和输入版本可复现
每次尝试 SHALL 固定并审计 provider identity、endpoint classification、model identity、模型参数、Prompt template/version/hash、DraftPatch Schema version/hash、tool contract versions/hashes、orchestrator version、base config/version manifest、目标/约束/允许范围、evidence manifest、确定性 evaluator/engine/Metric/risk/search versions 和规范输入 hash。重试或格式修复 SHALL 形成有序 attempt lineage，不能覆盖先前尝试。审计 SHALL 保存 PRD 要求的原始结构化响应、解析结果、工具调用与结果、rationale、assumptions、错误和 token/时延元数据，但 SHALL 不请求或保存隐藏思维链。

#### Scenario: 查看完整尝试来源
- **WHEN** 用户打开一个历史 DraftPatch
- **THEN** 系统可展示生成该提案的模型、Prompt/Schema/tool/input 版本、证据、确定性结果和 attempt lineage

#### Scenario: 配置在运行中变化
- **WHEN** 管理员在 Job 运行期间修改默认 model 或 Prompt template
- **THEN** 当前尝试继续使用创建时固定版本，新设置只影响后续新 Job

#### Scenario: 原始响应含敏感片段
- **WHEN** Provider 结构化响应回显 secret、环境变量值或未获准的敏感字段
- **THEN** 系统在持久化和日志输出前拒绝或不可逆遮盖该片段，并记录 redaction 事件而非敏感值

### Requirement: 版本化类型 DraftPatch 与证据门禁
Provider 输出 SHALL 符合版本化 DraftPatch Schema，包含 base revision、目标 stable ID、expected `entity_version`、仅限白名单操作的字段路径与类型值、rationale、assumptions 和非空 evidence refs。Patch SHALL NOT 修改 entity/revision/release 身份、历史 revision、状态指针、Schema/Registry 版本、extensions 中未授权内容或任何允许范围外路径；数值 SHALL 使用领域规范表示。服务端 SHALL 重新解析并验证结构，不信任模型声明或客户端 AST。

#### Scenario: 产生可审阅的多实体补丁
- **WHEN** Provider 返回引用固定证据、只修改允许路径且所有类型和 expected versions 合法的多实体 DraftPatch
- **THEN** 系统生成稳定 patch identity 和原始/规范 diff，并继续确定性评估

#### Scenario: 拒绝越权或伪造证据
- **WHEN** DraftPatch 修改禁止路径、引用未检索 evidence ID、缺少 assumption、使用非法十进制或尝试创建 release
- **THEN** 该输出为非法且不能进入可接受状态或写入 working draft

#### Scenario: 无证据不得确认
- **WHEN** 每个修改不能映射到至少一个固定 evidence ref 或检索证据为空
- **THEN** 系统将提案标记为不可确认并显示缺失证据原因

### Requirement: 有界结构修复与确定性提案评估
结构化输出解析或 Schema 错误 MAY 触发最多三轮自动格式修复；修复只可处理表示问题，不能扩大目标、约束、工具、证据或允许路径。通过结构门禁后，系统 SHALL 对隔离的 proposal materialization 复用 #5/#6 的 Schema、引用、公式与规则服务，并复用 #10/#11 的版本化 evaluator 对选定固定场景产生模拟/风险预览；这些预览 SHALL 标为 advisory AI-run evidence，不能冒充正式 simulation run、risk report 或 release Gate。确定性 BLOCK、缺失必需输入、预算超限或不一致 SHALL 阻止提案进入可接受状态，AI 文本不能覆盖。

#### Scenario: 三轮内修复结构
- **WHEN** 第一次响应是可修复的非法 JSON 且第三次之前得到同一语义范围内的合法 DraftPatch
- **THEN** 系统保留所有尝试并只评估最终合法结构

#### Scenario: 达到修复上限
- **WHEN** 连续三轮仍不能得到合法 DraftPatch
- **THEN** Job 以稳定不可接受结果结束，不再调用 Provider且不写入 working draft

#### Scenario: 确定性校验阻断
- **WHEN** proposal materialization 产生基础 Schema、引用、公式、静态环、无界事件或无上限叠加错误
- **THEN** 提案不能进入可接受状态，显示原始确定性 issue identities/evidence，模型说明不能降低严重级别

#### Scenario: 展示模拟和风险预览边界
- **WHEN** proposal materialization 通过确定性校验且固定场景预览完成
- **THEN** 候选展示 exact evaluator/engine/Metric/risk versions、输入/结果 hash、指标、假设和风险，但明确说明接受后仍需对新 revision 重新运行正式链路

### Requirement: 持久化 Job、流式进度、取消与显式重试
AI design SHALL 复用统一 Job 的 `queued/running/succeeded/failed/canceled/interrupted`、GET、SSE 和轮询契约，并发出检索、Provider、工具、评估和候选 sealing 的阶段事件。SSE 断线 SHALL 可凭事件游标回放或退回轮询。用户取消 SHALL 停止尚未开始的调用，并在 Provider/工具返回后丢弃任何晚到结果而不 seal DraftPatch。AI/Provider 中断、超时或进程重启 SHALL NOT 自动重新调用模型；用户显式重试 SHALL 创建新的 attempt/Job lineage。只有无副作用且符合既有幂等契约的确定性步骤 MAY 自动重放或复用。

#### Scenario: 流式观察并断线恢复
- **WHEN** 客户端在 Provider 阶段 SSE 断线后使用最后事件 ID 重连
- **THEN** 系统回放后续事件或提供同一 Job 的轮询状态，不创建重复 Provider 调用

#### Scenario: 取消运行中设计
- **WHEN** 用户取消一个 Provider 请求正在进行的 Job
- **THEN** Job 最终为 canceled 或 interrupted，任何晚到响应被审计为 ignored 且不能生成可接受 DraftPatch

#### Scenario: 用户显式重试暂时失败
- **WHEN** Provider 超时或 local-rag 返回 retryable 临时错误后用户选择重试
- **THEN** 系统创建关联原失败尝试的新 attempt，重新确认冻结 identities/freshness，并保留两个尝试的审计

### Requirement: 提案审阅、状态与可访问性
`/ai-design` SHALL 展示 Provider/capability、目标与约束、阶段进度、取消、候选原始/规范 diff、逐项 evidence、rationale、assumptions、工具与版本身份、校验/模拟/风险反馈、修复轮数、freshness 和接受/放弃操作。页面 SHALL 覆盖未配置、加载、空态、无证据、检索降级、非法输出、可重试/不可重试失败、预算/三轮上限、取消、成功和 stale。事实证据与 AI 生成说明 SHALL 清晰区分，状态、风险和差异不能只依赖颜色，关键流程 SHALL 支持键盘和明确焦点。

#### Scenario: 审阅可接受提案
- **WHEN** 候选通过所有提案门禁且 base/target versions 仍新鲜
- **THEN** 用户可逐项查看 diff、原始证据、假设和确定性反馈后明确选择接受或放弃

#### Scenario: 展示陈旧候选
- **WHEN** working entities 的任一目标 entity version 在候选生成后发生变化
- **THEN** 页面显示 stale 和冲突对象，禁用接受但保留只读证据与放弃操作

#### Scenario: 不依赖图形或颜色审阅
- **WHEN** 用户只使用键盘和文本替代查看提案
- **THEN** 用户仍能定位每项修改、证据、warning/BLOCK、错误摘要及接受/放弃控件

### Requirement: 人工接受后原子应用并重新运行正式链路
只有用户对可接受 DraftPatch 发出明确 accept，系统才 SHALL 通过 #5 的同一 application service 和序列化写事务重新校验 base `config_revision_id`、每个目标 `entity_version`、允许路径、基础 Schema 与 Patch identity，原子更新所有目标 working entities 并生成恰好一个新的 config revision。任一冲突或校验失败 SHALL 回滚全部修改并返回 `409 REVISION_CONFLICT` 或相应稳定错误。新 revision SHALL 进入既有 FULL validation、Graph、固定场景 simulation 和 risk 流程；其正式结果不得复用 proposal preview。AI 或 accept endpoint SHALL NOT 创建/修改 release、激活 Graph 或代表用户确认 Gate。

#### Scenario: 原子接受多实体提案
- **WHEN** 用户接受的 Patch base revision、所有 target entity versions 和服务端校验均仍匹配
- **THEN** 所有修改在一个事务写入 working draft、产生一个新 config revision，并排队既有正式分析链

#### Scenario: 接受时发生 revision 冲突
- **WHEN** 任一目标 entity version 或 base 条件不再匹配
- **THEN** 系统返回 409 `REVISION_CONFLICT`，不写入任何实体、不生成 revision，并保留提案供比较或放弃

#### Scenario: 接受不等于发布
- **WHEN** AI 提案已成功应用并完成后续确定性分析
- **THEN** active release 保持不变，用户仍须在 #7 流程独立审阅 Gate、说明和确认后才能发布

### Requirement: 放弃、不可变审计与保留边界
用户 SHALL 能放弃未应用的 DraftPatch；discard SHALL 使其永久不可接受但保留不可变生成、证据、评估和决定审计。accept/discard SHALL 幂等且互斥，记录 actor 为本机用户动作、时间、请求 identity 和结果；已经 accepted、discarded、stale 或失败的 Patch SHALL NOT 被重新应用。AI 审计 SHALL 保存于项目数据库但不成为配置事实，且 SHALL 使用有界 payload、引用和保留策略，不能保存 secret、完整 Credential、无关文件或隐藏思维链。

#### Scenario: 放弃提案
- **WHEN** 用户放弃一个 pending DraftPatch
- **THEN** Patch 进入 discarded，重复 discard 返回同一结果，后续 accept 被拒绝且 working draft 不变

#### Scenario: 重放接受请求
- **WHEN** 同一 accept 请求在成功后因客户端断线被重复提交
- **THEN** 系统返回原 accepted 结果和同一 config revision，不生成第二个 revision

#### Scenario: 审计历史失败和决定
- **WHEN** 用户查看历史 AI design run
- **THEN** 系统可只读查看尝试、证据、结构化响应、工具、评估、错误和人工决定，同时敏感值保持不可恢复地移除

### Requirement: 失败隔离与端到端不变量验证
Provider、检索、流式连接、工具或 AI 输出的失败 SHALL 局限于 AI capability，不能改变公式、引用、模拟、风险或发布结论，也不能损坏 working draft、revision 或 release。自动测试 SHALL 覆盖 Provider mock、模型/Prompt/input/tool 版本固定、检索降级/身份不匹配/无证据、非法 JSON、三轮上限、工具越权、多实体 Patch、entity version 冲突、敏感值遮盖、取消/晚到响应、显式重试、确定性 BLOCK、原子 accept/discard 和“接受后一个 revision、人工发布”主流程。

#### Scenario: AI 依赖全部不可用
- **WHEN** Provider 与 retrieval capability 均不可用
- **THEN** AI 设计显示不可用且不产生提案，人工编辑、校验、版本、模拟、风险和备份的固定测试结果不变

#### Scenario: 完成完整人在回路流程
- **WHEN** 固定 fixture 依次完成检索、生成、校验、预览、人工接受和新 revision 的正式分析
- **THEN** 所有证据和版本可追溯、只产生一个新 revision、active release 未变化，并且只能由后续独立人工发布动作改变正式版本
