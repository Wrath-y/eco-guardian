## Purpose

为不可变配置 revision 提供受限、版本化且可审计的固定场景模拟，使相同输入能够跨并行度、重试与进程重启产生相同结果，并向策划和发布门禁提供可信指标而非猜测值。

## Requirements

### Requirement: Immutable revision admission and input capture
每次模拟 SHALL 明确绑定当前项目中一个不可变 `config_revision_id`，或先把所选 release 解析为其指向的 revision，并 SHALL 固定 `config_hash` 与该 revision 的 VersionManifest；模拟 MUST NOT 读取后续 working state、当前 release 指针变化或其他 revision。服务端在创建 Job 前 MUST 消费 #6 的精确 FULL `ValidationGate`，只有 revision、config hash、Schema、DSL、Registry 与 NumericPolicy 版本全部匹配且没有 ERROR/BLOCK 的成功结果才能进入模拟。

系统 SHALL 在接受前规范化并固定场景定义/版本、允许参数、参与对象、动作序列、初始状态、资源预算、样本数、seed、引擎/事件/PRNG/数值/evaluator/Metric/归并版本，形成不可变 simulation input 与 `input_hash`；缺失或不兼容的必需输入 MUST 以稳定 Problem Details 拒绝，且不得创建可被误读为可运行的 Job。

#### Scenario: Resolve a release once
- **WHEN** 用户选择一个 release 并创建模拟，而该 release 指向一个可读且 FULL validation 通过的 revision
- **THEN** 系统把该 revision ID、config hash 与版本 manifest 固定进输入，之后 active release 或 working state 变化不改变该 Job

#### Scenario: Reject a blocked or stale validation result
- **WHEN** revision 只有 LOCAL、working、缺失、失败、含 ERROR/BLOCK 或解释版本不匹配的 validation result
- **THEN** 创建请求以稳定的需要完整校验或校验阻断错误失败，不创建 simulation Job 或 run

### Requirement: Versioned bounded scenario definitions
系统 SHALL 内置并版本化四个可克隆模板：30 秒单目标、180 秒单目标、60 秒三目标、60 秒极限叠层。每个模板及其克隆 SHALL 显式保存 scene schema/version、参与对象、目标数量、动作序列、持续时间、初始状态、默认 seed、允许调整的参数及其类型/范围，以及事件、步骤和资源预算；已被 simulation run 引用的场景版本 MUST 保持只读，新修改必须创建新的场景定义版本/identity。

用户 SHALL 只能修改场景声明为可调的参数。系统 MUST 拒绝任意脚本、未注册事件、未注册 evaluator、动态代码或模板未声明的机制；未知 entity extension MUST 保留在配置事实中但不得被场景声称为已模拟。

#### Scenario: Clone a built-in template
- **WHEN** 用户克隆 60 秒三目标模板并只调整其声明允许的参数
- **THEN** 系统创建新的版本化场景定义，保留模板来源并固定完整规范输入，而不修改内置模板

#### Scenario: Reject an undeclared mechanism
- **WHEN** 场景参数试图加入任意脚本、未注册事件或模板未声明的可执行行为
- **THEN** 系统拒绝保存或启动并返回稳定字段错误，不执行该行为也不以近似机制替代

### Requirement: Deterministic event queue and time progression
模拟引擎 SHALL 使用受限的离散事件队列。每个事件的规范排序键 MUST 依次为事件时间、规则优先级、来源 stable ID、插入序号；所有比较 SHALL 使用规范值与原始 UTF-8 stable ID bytes，不得依赖 map、数据库、goroutine 或完成顺序。插入序号 SHALL 在单个样本内从规范初始状态和确定事件派生顺序分配。

引擎 SHALL 从当前时间直接推进到下一个有序事件时间，并使用版本化 Duration/事件时间语义；同一时间的事件必须按完整排序键逐一执行。持续时间边界外的事件不得执行，时间回退、无界零时间事件或预算耗尽 MUST 产生稳定失败/预算结果，而不是挂起、按帧猜测或改变排序。

#### Scenario: Order simultaneous events stably
- **WHEN** 多个来源在同一时间产生相同优先级的事件且输入/存储枚举顺序被打乱
- **THEN** 每次运行都按来源 stable ID 和插入序号执行相同事件序列并得到相同样本结果

#### Scenario: Advance to the next event boundary
- **WHEN** 当前队列的下一个事件位于未来且其时间不超过场景持续时间
- **THEN** 引擎按版本化时间语义直接推进到该时间并执行事件，不引入依赖机器时钟或帧率的中间步骤

### Requirement: Pinned DSL evaluator and numeric policy
公式与结构化规则 SHALL 只通过 #6 暴露的版本化 parser、typed AST、纯 evaluator 和 Registry 执行，服务端 MUST NOT 接受客户端 AST、运行时插件或隐式随机。每个样本的计算 SHALL 使用 revision VersionManifest 与 simulation input 固定的 NumericPolicy：34 位有效数字、指数 `-6143..6144`、`HALF_EVEN`、有限值、规范十进制字符串、Percentage ratio 与 Duration 整数毫秒，并保持单位/维度约束。

NaN、Infinity、除零、溢出、维度不匹配、缺失 evaluator 或 evaluator contract drift MUST 产生稳定的不可成功复现结果；系统 MUST NOT 转为 float、零值、截断值或当前版本 evaluator。相同 evaluator version 的语义 MUST 永不改变。

#### Scenario: Evaluate a numeric boundary repeatedly
- **WHEN** 同一 revision 和场景在相同 evaluator 与 NumericPolicy 版本下触发 decimal128 舍入边界
- **THEN** 所有样本运行产生相同规范十进制中间/最终结果，且结果不依赖底层库默认设置

#### Scenario: Fail on a missing historical evaluator
- **WHEN** 项目引用的 evaluator version 在当前应用中不可用
- **THEN** 系统允许只读返回已有结果，但拒绝新运行或复现成功声明，并且不使用较新 evaluator 代替

### Requirement: Engine-owned deterministic PRNG and seed semantics
需要随机性的场景 SHALL 只使用 simulation engine 自有、版本化并纳入引擎指纹的 PRNG 算法；系统 MUST NOT 使用标准库默认随机流、全局共享随机状态、机器时间或 worker 调度作为随机源。请求 SHALL 固定一个规范 seed；每个样本的随机流 SHALL 由 seed、样本 ordinal 与 PRNG version 通过版本化派生规则唯一确定，使样本独立于 worker 数量、分片和执行顺序。

相同 seed 与完整输入指纹 SHALL 产生相同随机 draw 序列；改变 seed、PRNG version 或样本 ordinal MUST 改变相应输入身份。随机 draw 的类型、边界与消耗顺序 SHALL 由 evaluator/engine contract 固定，未执行分支不得通过实现偶然行为消耗随机值。

#### Scenario: Change worker count without changing random streams
- **WHEN** 同一 1000 样本输入分别用一个和多个 worker 执行
- **THEN** 每个样本 ordinal 获得相同随机流和结果，最终 result hash 完全相同

#### Scenario: Repeat a fixed seed
- **WHEN** 用户以相同 revision、场景、版本指纹、样本数和 seed 重新运行
- **THEN** 系统使用相同样本随机流并产生相同规范结果与 result hash

### Requirement: Parallel samples with stable aggregation
系统 MAY 并行执行独立样本，但 SHALL 为样本分配从零开始的稳定 ordinal，并 SHALL 以 ordinal 升序将样本结果输入版本化归并器；worker 分片、完成顺序、重试次数或并发度 MUST NOT 改变聚合结果。随机场景默认样本数 SHALL 为 1000；请求的样本数必须为场景/资源预算允许的正整数。

聚合 SHALL 使用固定 NumericPolicy 和版本化算法计算 Metric 汇总、样本数和置信区间，并记录 aggregation version 与假设；不得依赖非结合 float 累加或无序 map reduce。完成 run 的 `result_hash` SHALL 覆盖按规范顺序编码的指标值、置信区间、不可用原因、假设、警告和完整版本指纹，但 SHALL 排除 Job/run ID、时间戳、进度事件、worker 数与机器信息。

#### Scenario: Merge out-of-order completions
- **WHEN** 样本以不同顺序完成或某些样本在恢复后重算
- **THEN** 系统仍按样本 ordinal 稳定归并，得到相同指标、置信区间和 result hash

#### Scenario: Apply the default sample count
- **WHEN** 随机场景创建请求没有显式样本数
- **THEN** 规范输入固定样本数 1000，并在 run 结果与 input hash 中回显该值

### Requirement: Independent versioned Metric Modules
系统 SHALL 通过编译期、版本化 Metric Registry 分别提供 DPS、治疗、生存、资源和控制五类 Metric Module。每个 module SHALL 声明稳定 metric ID、schema/version、所需结构化输入、单位、聚合/置信区间语义、假设，以及 `higher_is_risk`、`lower_is_risk` 或 `target_range` 方向和可选绝对阈值；module MUST 只消费已固定样本事件/状态，不得读取 working state、外部模型或机器环境。

一个 Metric 缺少结构化规则或所需输入时 SHALL 返回带稳定 code、缺失字段/能力和可读说明的 `UNAVAILABLE` 结果；系统 MUST NOT 返回 0、估算值或由其他 Metric 推断的替代值。一个 Metric 不可用不得改变其他独立 Metric 的确定结果；是否阻断发布由 #7 policy 中 required/optional 声明决定。

#### Scenario: Keep one unavailable metric explicit
- **WHEN** revision 能计算 DPS 但缺少治疗 Metric 所需的结构化治疗规则
- **THEN** run 保存确定的 DPS 结果，并把治疗标为带原因的 UNAVAILABLE，而不是保存为零或估算值

#### Scenario: Repeat independent metrics
- **WHEN** 对相同样本事件流启用相同版本的五个 Metric Module
- **THEN** 每个 module 以稳定 ID/版本产生独立有序结果，禁用一个可选 module 不改变其他 module 的值

### Requirement: Resource budgets and truthful terminal outcomes
每个场景和 simulation Job SHALL 固定可审计的资源预算，至少约束模拟时长、事件/步骤数量、样本数和本地执行时间/资源上限。引擎 MUST 在确定性边界检查事件/步骤/场景预算，并在本地运行边界检查 Job 超时或资源限制；超过上限 SHALL 以稳定 `BUDGET_EXCEEDED` 或 timeout 终止，不得继续无界执行或提交部分结果为成功。

Job 状态 SHALL 使用 `queued/running/succeeded/failed/canceled/interrupted`。只有全部请求样本和 Metric 归并完成、结果在短事务中封存后才能为 `succeeded`；失败、超时、预算超限、取消或中断 MUST 保留阶段、进度、稳定原因与安全重试信息，且不得存在可被误解为成功的 run。

#### Scenario: Exceed the event budget
- **WHEN** 一个已通过静态校验的有界场景在运行时达到其固定事件预算而仍有待处理事件
- **THEN** Job 以稳定预算超限原因失败，不保存成功指标或成功 result hash

#### Scenario: Seal only complete results
- **WHEN** 所有样本与 Metric 已完成并通过规范 hash 校验
- **THEN** 系统原子封存不可变 simulation run、设置 Job result identity/URL 并进入 succeeded

### Requirement: Idempotent cancellation and restart recovery
`POST /api/v1/simulation-jobs` SHALL 要求项目范围的 `Idempotency-Key`。相同 key 与字节等价的规范 simulation input SHALL 返回同一 Job；相同 key 用于不同输入 SHALL 返回稳定 409 幂等冲突。自动 policy 运行 SHALL 以完整 input hash 去重，且不得因重试创建语义重复的成功 run。

取消请求 SHALL 持久化 cancellation intent；worker 在样本/阶段安全点停止调度新工作并收敛到 `canceled`。取消后的部分样本、Metric 或 hash MUST NOT 对外成为成功 run，重复取消返回当前状态。进程或项目在运行中关闭时 Job SHALL 进入可解释的 `interrupted`/恢复状态；重开后系统 SHALL 只在完整输入与实现指纹仍完全可用时恢复原 Job，复用经过规范 hash 校验的确定性样本 checkpoint 或重跑缺失样本，并以相同 ordinal 稳定归并。版本不匹配时 MUST 停止并要求按原指纹兼容运行，不能混合结果。

#### Scenario: Cancel a running simulation
- **WHEN** 用户取消一个正在执行的 1000 样本 Job
- **THEN** 系统持久化取消、停止继续调度并最终显示 canceled，不提交部分结果为成功；再次取消不会创建新 Job

#### Scenario: Recover after process interruption
- **WHEN** 进程在部分确定性样本完成后中断并以相同项目和实现指纹重开
- **THEN** 系统恢复原 Job，从已验证 checkpoint/缺失 ordinal 安全继续或确定性重算，并产生与不中断执行相同的最终 result hash

### Requirement: Complete version fingerprint and historical compatibility
每个 simulation input/run SHALL 保存并回显完整复现指纹，至少包括 config revision/config hash/VersionManifest、scene identity/schema/version/body hash、engine/event ordering/time semantics、PRNG、DSL/evaluator Registry、NumericPolicy、Metric modules、aggregation policy、样本数、seed、资源预算与规范 input hash。完成结果 SHALL 另外保存 result schema/version、result hash、各 Metric 结果/置信区间/不可用原因、假设和警告。

同一 evaluator version 的语义 MUST 永不改变。v1 major 二进制 SHALL 内置该 major 曾写入项目的全部 evaluator 实现；移除 evaluator 只能通过新的 major change 与明确兼容运行器或迁移方案。若任一必需历史实现缺失，系统 SHALL 保留已有 simulation run 的只读访问及其原始指纹，但 MUST 将复现能力标为 unavailable，禁止声称结果已复现或让该结果满足新发布 Gate。

#### Scenario: Verify a stored run
- **WHEN** 用户对已存 run 发起复现验证且完整历史指纹仍受支持
- **THEN** 系统按该 run 的不可变输入重跑，只有新旧 result hash 完全相同时才标记 verified，并保留两次 run 身份

#### Scenario: Open a project with a missing evaluator
- **WHEN** 当前应用缺少项目历史 run 指纹引用的 evaluator
- **THEN** UI/API 仍可只读展示已存结果、版本与 hash，但明确标记不可复现且该结果不能用于新发布

### Requirement: Simulation API and immutable result retrieval
OpenAPI SHALL 定义 `POST /api/v1/simulation-jobs`，请求明确选择 revision 或 release、scene identity/version、允许参数、Metric 集、样本数、seed、预算和 `Idempotency-Key`；接受时返回 `202`、Job URI 与最终 `result_type/result_id/result_url` 约定。系统 SHALL 复用 `GET /api/v1/jobs/{id}`、SSE `GET /api/v1/jobs/{id}/events` 和 `POST /api/v1/jobs/{id}/cancel` 提供阶段、样本进度、warning、断线续传、轮询后备和取消。

`GET /api/v1/simulation-runs/{id}` SHALL 返回不可变历史详情，包括完整输入/结果指纹、revision/scene/engine/numeric/evaluator/Metric/aggregation identities、样本数、seed、指标、置信区间、假设、不可用原因、input/result hash 与复现能力。完成 run、Metric 结果及其指纹 MUST insert-only；stale/当前可复现状态 SHALL 在读取时根据当前选择和可用实现计算，不得改写历史事实。所有错误 SHALL 使用 RFC 9457 Problem Details 与稳定 code，且不得暴露 SQL、堆栈、secret 或本机路径。

#### Scenario: Create and retrieve a run
- **WHEN** 合法请求被接受、Job 完成且客户端访问其 result URL
- **THEN** POST 返回的 Job 最终指向一个 GET 可读取的不可变 run，且所有复现字段与实际规范输入和结果一致

#### Scenario: Reload progress after disconnect
- **WHEN** SSE 连接在运行中断开或页面刷新
- **THEN** 客户端通过持久 Job/event ordinal 与轮询恢复一致阶段和样本进度，不创建第二个 Job 或推断成功

### Requirement: Versioned simulation Gate registration
系统 SHALL 向 #7 Gate Registry 注册稳定、版本化的 simulation capability/Gate descriptor 和 VersionContributor。Gate evaluation SHALL 按当前 release policy 核对 candidate revision/config/version manifest、每个 required scene、样本数、seed policy、engine/evaluator/numeric/Metric/aggregation versions、input/result hash、成功状态、Metric 可用性与复现能力；只有全部 required 输入完全匹配且 run 未 stale 时才能返回 PASS。

required scene 或 Metric 缺失、失败、不可用、版本不匹配、结果陈旧、未成功或缺少 evaluator SHALL BLOCK；policy 明确 optional 的 scene/Metric 不可用 SHALL 产生可见 WARNING。Gate evidence SHALL 引用不可变 run/Metric identity 与 hash；人工说明、AI 文本或风险覆盖 MUST NOT 把模拟缺失、不可复现或结构输入缺失改为 PASS。

#### Scenario: Pass an exact policy simulation set
- **WHEN** candidate 的所有 required scene/Metric 都有匹配 policy 与完整版本指纹的成功可复现 run
- **THEN** simulation Gate 返回 PASS 并提供不可变 run/Metric evidence refs 供 release 审计

#### Scenario: Reject a stale run
- **WHEN** run 的 revision、scene、样本、seed、Metric 或任一实现版本与当前 policy/candidate 不同
- **THEN** Gate 将该结果标为 stale/不匹配并阻止其满足 required simulation，而不重新标记历史 run

### Requirement: Accessible simulation workflow and historical results
`/simulations` SHALL 提供 revision 或 release、场景和 Metric 选择，限制在声明范围内的参数编辑，样本/seed/预算展示，客户端即时提示与服务端权威校验，Job 阶段/样本进度、取消、超时、预算超限、可重试/不可重试失败、失败重试、指标不可用原因、置信区间、假设、历史结果、stale 与复现验证状态。页面 MUST 使用生成的 OpenAPI client，且不得在客户端自行计算 Gate、指标或成功状态。

页面 SHALL 覆盖首次加载、局部刷新、空态、无可用 revision/scene、FULL validation 未通过、提交中、SSE 断线轮询、canceled/interrupted、缺失 evaluator 只读降级与结果陈旧。状态颜色/图标必须有等价文本；键盘用户 SHALL 能选择输入、定位参数错误、启动/取消、浏览 Metric/不可用原因并查看复现指纹，焦点在错误和终态变化后移动到可理解位置。

#### Scenario: Explain an unavailable metric
- **WHEN** 一个完成 run 包含治疗 Metric 的结构化输入缺失
- **THEN** 页面展示 UNAVAILABLE、稳定原因和所需输入，仍展示其他可用指标且不把缺失值渲染为零

#### Scenario: Browse a historical result after evaluator removal
- **WHEN** 用户打开一个引用当前缺失 evaluator 的历史 run
- **THEN** 页面只读展示原指标、置信区间、假设、版本与 hash，并以文本说明不可验证且不可用于新发布

### Requirement: Reproducibility and capacity verification
系统 SHALL 以版本化固定 fixture 和 golden 覆盖四个内置模板、五类 Metric、同时间事件、event/stack 边界、decimal128/单位边界、PRNG draws、样本并行归并、不可用输入、取消、预算超限、崩溃恢复、缺失 evaluator、API/Gate 和 UI 状态。相同完整输入在不同 map/SQLite 枚举顺序、worker 数、样本完成顺序、独立进程和中断恢复下 MUST 产生相同事件/样本/Metric 规范结果与 result hash。

在 4 核、16 GB RAM、NVMe 的参考 Windows 10/11 x64 环境，默认 1000 样本的固定性能 fixture SHALL 以小于 30 秒为目标并可取消；实现 MUST 保持有界内存、worker、事件、payload 与持久化批次。性能不达标时 SHALL 报告可诊断的进度/预算结果，不得通过降低样本、跳过 Metric、改变 NumericPolicy 或引入非确定归并伪造达标。

#### Scenario: Reproduce across processes and concurrency
- **WHEN** 固定 fixture 在独立进程、不同 worker 数、打乱输入枚举和一次中断恢复中重复执行
- **THEN** 所有成功执行得到相同 input hash、样本结果顺序、Metric/置信区间与 result hash

#### Scenario: Run the default capacity fixture
- **WHEN** 参考环境执行默认 1000 样本容量测试并在运行中请求取消
- **THEN** 系统显示有界进度、响应取消且不提交部分成功结果；未取消的基线运行以 30 秒目标记录实际耗时和版本指纹
