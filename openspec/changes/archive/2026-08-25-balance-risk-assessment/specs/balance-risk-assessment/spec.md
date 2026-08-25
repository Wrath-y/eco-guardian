## Purpose

为不可变 candidate revision 提供绑定固定场景、版本化 Metric 与阈值的确定性风险复核，使策划能够比较当前 release 基准、理解结构与数值风险、审计人工处理结论，并让发布门禁准确区分无基准、不可比较、陈旧和可覆盖/不可覆盖阻断。

## ADDED Requirements

### Requirement: Immutable candidate, baseline, policy, and simulation admission
每次风险复核 SHALL 显式绑定同一项目的 candidate revision/config hash/VersionManifest、release policy identity/hash、threshold version identity/hash，以及被比较场景和 Metric 对应的不可变 simulation run/result hash。后续 working state、active release、policy、threshold 或 Registry 变化 MUST NOT 改变已接受 Job 或已完成报告的输入。

后续发布复核的 baseline MUST 是创建复核时的当前 active release revision，并 SHALL 固定其 config/version identity 和相同场景、Metric 的 simulation run identity；提交发布时 baseline 不再是当前 active release、任一 simulation input/result/实现指纹不匹配、结果未成功/不可复现或场景、样本、seed policy、Metric 不同 SHALL 使结果明确为 stale/不匹配且不得复用。系统 MUST 在 candidate 或 baseline 未通过 #6 精确 FULL `ValidationGate`、policy 或已启用 threshold 缺失、必需 simulation 输入不可用时拒绝生成可满足发布 Gate 的报告。

#### Scenario: Freeze a subsequent-release comparison
- **WHEN** 当前 release 为 baseline R1，candidate R2 具有与 policy、threshold 和相同必检场景完全匹配的成功 simulation runs
- **THEN** 风险 Job 固定 R1/R2、policy、threshold、run/Metric/实现 identities 与 hashes，后续 working 或配置变化不改变其比较输入

#### Scenario: Reject a stale simulation result
- **WHEN** candidate 或 baseline run 的 revision、场景、样本、seed policy、Metric、NumericPolicy、evaluator、aggregation、input hash 或 result hash 与当前复核输入不匹配
- **THEN** 系统拒绝直接复用并返回可定位的 stale/identity mismatch 原因，不以旧指标生成当前风险结论

### Requirement: Immutable versioned threshold configuration
系统 SHALL 提供按项目版本化且 insert-only 的 threshold version；每个版本 SHALL 固定适用场景、Metric、可选 `balance_group` 作用域、Metric 风险方向、相对 WARNING/BLOCK 边界、可选绝对边界、结构规则版本、启用状态、规范 body/hash 和创建审计。修改任何阈值、作用域、方向、规则或启用状态 MUST 创建新版本，历史报告继续引用原版本且不得由当前阈值重解释。

系统 SHALL 提供相对风险 10% WARNING、25% BLOCK 的起始模板，但模板在项目首次使用风险功能时 MUST 由用户明确启用或基于它创建修改版。未启用或没有适用 threshold 时 UI/API SHALL 显示“阈值未配置”，risk Gate SHALL 阻止发布，且系统 MUST NOT 显示“安全”、PASS 或隐式采用模板。

#### Scenario: Enable the starter threshold explicitly
- **WHEN** 项目首次使用风险功能且用户确认启用 10% WARNING、25% BLOCK 起始模板
- **THEN** 系统创建一个已启用的只读 threshold version，后续报告引用其精确 identity/hash

#### Scenario: Do not infer safety without thresholds
- **WHEN** 项目尚未启用任何适用于必检场景和 Metric 的 threshold version
- **THEN** 页面和 Gate 明确报告阈值未配置并阻止发布，不运行隐式 10%/25% 判断，也不显示安全结论

### Requirement: Deterministic Metric direction and boundary comparison
风险比较 SHALL 使用 #10 Metric Module 固化的规范 aggregate value、单位、`higher_is_risk`、`lower_is_risk` 或 `target_range` 方向及可选绝对阈值。candidate 与非零 baseline 的规范相对变化 SHALL 由版本化比较规则以 decimal128 语义计算；`higher_is_risk` 只把向更高值的变化计入风险，`lower_is_risk` 只把向更低值的变化计入风险，反方向变化不 SHALL 被错误标为数值膨胀。`target_range` SHALL 相对其版本化目标区间确定越界方向与距离，不得由 UI 猜测。

起始相对模板 SHALL 将风险方向上的变化绝对幅度达到 10% 且小于 25% 分类为 WARNING，达到或超过 25% 分类为 BLOCK，低于 10% 的可比较结果分类为 INFO；恰好 10% 和 25% MUST 分别落在 WARNING 和 BLOCK。计算、边界比较、规范编码和排序 MUST 不依赖 float、数据库/map 顺序、UI locale 或机器环境。Metric 置信区间与假设 SHALL 原样保存为证据；起始模板使用规范 aggregate value作确定性边界判断，置信区间不得概率性地提升或降低 severity。

#### Scenario: Classify exact starter boundaries
- **WHEN** 一个 `higher_is_risk` Metric 相对 baseline 分别增加规范的 9.999%、10%、24.999% 和 25%
- **THEN** 起始模板依次产生 INFO、WARNING、WARNING 和 BLOCK，并在重复运行中返回相同规范差异与规则证据

#### Scenario: Apply the lower-is-risk direction
- **WHEN** 一个 `lower_is_risk` Metric 的 candidate 比非零 baseline 降低 10%，而同样幅度的升高不在该 Metric 的风险方向
- **THEN** 降低结果为 WARNING，升高结果不被标为 WARNING/BLOCK，报告仍保存带符号绝对/相对差异和方向

#### Scenario: Compare a target-range metric
- **WHEN** candidate aggregate 离开 Metric Module 与 threshold version 固定的目标区间
- **THEN** 系统按版本化区间方向和距离确定 severity，并保存目标边界、实际值与规则身份，不由客户端重新计算

### Requirement: Zero baseline and explicit non-comparability
baseline aggregate 为规范零值时，系统 MUST NOT 计算或显示相对变化。系统 SHALL 优先使用该 Metric 与适用 threshold version 声明的绝对 WARNING/BLOCK 边界；缺少适用绝对阈值时 comparison status MUST 为 `NOT_COMPARABLE`，并保存稳定原因、baseline/candidate 值、单位、Metric 和 threshold identities，不得用无穷大、0%、估算值或“安全”替代。

release policy 声明为 required 的 Metric 出现 `NOT_COMPARABLE` SHALL 阻止发布；仅声明为 optional 的 Metric 出现 `NOT_COMPARABLE` SHALL 产生可见 WARNING 而不单独阻止发布。绝对边界的等值语义 SHALL 与相对边界一致：达到 WARNING/BLOCK 边界分别进入 WARNING/BLOCK。

#### Scenario: Use an absolute threshold for a zero baseline
- **WHEN** baseline 为 0 且 Metric/threshold version 提供适用的绝对 WARNING 和 BLOCK 边界
- **THEN** 系统不计算相对变化，按 candidate 的规范绝对差异和方向在精确边界上确定 severity

#### Scenario: Block a required non-comparable metric
- **WHEN** baseline 为 0、没有绝对阈值且该 Metric 在 release policy 中为 required
- **THEN** 报告返回 `NOT_COMPARABLE` 与明确原因，risk Gate 阻止发布且不显示安全结论

#### Scenario: Warn for an optional non-comparable metric
- **WHEN** 相同 `NOT_COMPARABLE` 情况只影响 policy 中的 optional Metric
- **THEN** risk Gate 产生可见 WARNING，其他可比较必需 Metric 仍按各自规则确定结果

### Requirement: Cohort selection uses balance group or stable identity
对象 envelope 的非空 `balance_group` SHALL 定义同定位 cohort；风险报告 SHALL 固定 candidate 与 baseline 中被纳入 cohort 的 stable IDs、对象 kind、选择依据和稳定排序。没有 `balance_group` 的对象 MUST 只与 baseline 中相同 stable ID 的对象比较，不得按名称、标签、文本相似度或模型推断自动配对。cohort 缺员、新增、删除或身份不匹配 SHALL 作为明确 comparison status/evidence 保存，不得静默丢弃或补造对象。

#### Scenario: Compare members in one balance group
- **WHEN** candidate 与 baseline 的多个同 kind 对象显式声明相同 `balance_group`
- **THEN** 系统按该 group 形成并保存稳定 cohort，使用版本化 cohort 规则比较而不受显示名称或存储顺序影响

#### Scenario: Fall back only to the same stable ID
- **WHEN** 一个对象没有 `balance_group`
- **THEN** 系统只选择 baseline 中相同 stable ID 对象；不存在时返回明确缺少可比对象状态，不按名称或相似度寻找替代项

### Requirement: Required, optional, unavailable, and missing Metric semantics
每个报告 SHALL 保留 policy 对场景和 Metric 的 required/optional 声明。simulation run 中 Metric `UNAVAILABLE`、缺失输入或缺失实现 SHALL 保存 #10 的稳定 code、缺失字段/能力、可读原因、假设及 run/Metric evidence，不得转为 0、估算值或可比较结果。required 场景或 Metric 的缺失、失败、不可用、不可复现或不匹配 MUST 阻止 risk Gate；optional Metric 的同类状态 SHALL 产生 WARNING，并 MUST 不改变其他独立 Metric 的确定差异与 severity。

#### Scenario: Preserve a missing Metric reason
- **WHEN** candidate run 能计算 DPS 但治疗 Metric 因结构化输入缺失而为 `UNAVAILABLE`
- **THEN** 报告保存 DPS 的确定比较和治疗的原始缺失原因/证据，不把治疗显示为 0 或推断值

#### Scenario: Enforce required and optional policy roles
- **WHEN** 一个 required Metric 与一个 optional Metric 均不可用
- **THEN** required 项阻止 Gate，optional 项产生 WARNING，且两者的原因和 policy 角色均可见并进入审计

### Requirement: Structural risk and immutable severity provenance
风险复核 SHALL 以版本化规则检查并报告新乘区、重复乘算等已定义结构风险，保存 rule identity/version、固定 severity、涉及 revision/entity/field、规范证据和确定排序。静态公式环、无界事件与无上限叠加 MUST 复用 #6 FULL validation 中的 `STATIC_FORMULA_CYCLE`、`EVENT_LOOP_UNBOUNDED`、`STACK_UNBOUNDED` 等确定事实并保持不可覆盖 BLOCK；本 capability MUST NOT 复制 parser、依赖图、循环或规则安全判定，也不得把校验 BLOCK 降为风险 WARNING。

结构风险 severity 与数值风险 severity SHALL 仅由版本化规则和固定输入计算，取值为 BLOCK、WARNING 或 INFO；用户说明、AI 文本、Graph 路径、置信分数或 provider 健康状态 MUST NOT 改变原 severity、comparison status 或规则证据。

#### Scenario: Preserve an unbounded validation block
- **WHEN** candidate 的精确 FULL validation 报告无界事件或无上限叠加
- **THEN** 风险复核引用该 validation evidence 并保持不可覆盖 BLOCK，且 candidate 不得因填写风险说明而进入发布

#### Scenario: Report a versioned multiplier rule
- **WHEN** candidate 相对 baseline 新增一个命中当前结构规则版本的新乘区或重复乘算模式
- **THEN** 报告以该规则固定的 severity 保存定位与证据，相同 revision/rule input 重复执行产生相同结果

### Requirement: Numeric BLOCK override audit without severity mutation
只有被 risk rule 明确标记为可覆盖的数值 BLOCK MAY 接受人工处理。用户 MUST 填写非空覆盖说明；系统 SHALL 以引用原 calculation report/item identities 的新不可变 decision report 保存原 BLOCK severity、rule/result identity、candidate、baseline、policy、threshold version、用户说明和处理状态，不得原地修改 calculation report 或把原风险项改写为 PASS、WARNING 或 INFO。发布时用户还 MUST 通过 #7 执行二次确认，release 审计 SHALL 同时引用 decision report 与原 calculation identities。

静态公式环、无界事件、无上限叠加、其他 validation BLOCK、缺失/陈旧/不可复现必需模拟、required `NOT_COMPARABLE`、policy/Gate/threshold 缺失和备份失败 MUST NOT 显示或接受覆盖入口。risk Gate SHALL 通过版本化 descriptor 向 #7 声明哪些数值 BLOCK 可覆盖；是否带覆盖继续发布由 #7 的确认与完整 Gate 审计契约处理，风险报告本身保持原始结论。

#### Scenario: Record an allowed numeric override
- **WHEN** 一个规则声明可覆盖的数值 BLOCK 具有匹配当前上下文的报告，用户填写非空原因
- **THEN** 系统创建引用原 report/items 的不可变 decision report 并保留 BLOCK severity/result identity；#7 仅在发布时取得二次确认后才可在其他 Gate 全部满足时消费该决定

#### Scenario: Refuse a structural override
- **WHEN** 用户尝试对静态公式环、无界事件或无上限叠加填写覆盖说明
- **THEN** UI/API 拒绝该操作且 Gate 仍为不可覆盖 BLOCK，不创建可被发布消费的覆盖审计

### Requirement: First-release NO_BASELINE and subsequent current-release Gate
项目没有 active release 时，风险复核 SHALL 返回报告级 `NO_BASELINE`，仍执行 release policy 中所有必检 simulation 并保存 candidate 场景、Metric、置信区间/不可用原因、阈值、假设和结构规则结果。只有必检 simulation 成功、required Metric 可用、threshold 已启用、没有不可覆盖 BLOCK，且用户明确确认“建立基线”后，risk Gate 才 SHALL 向 #7 提供允许首发继续的完整证据；系统 MUST NOT 伪造 candidate-vs-baseline 差异或把 `NO_BASELINE` 显示为低风险。

存在 active release 时，risk Gate SHALL 要求报告 baseline 精确等于预检与提交时的当前 release revision，并核对 candidate/policy/threshold/simulation/result/version identities。baseline 变化或任何必需身份变化 SHALL 返回 STALE/BLOCK 并要求重新比较，不能原地修改历史报告。

#### Scenario: Establish the first baseline
- **WHEN** 项目无 active release，必检 simulation 与 threshold 条件满足且用户明确确认建立基线
- **THEN** 报告保持 `NO_BASELINE` 并提供确认审计，risk Gate 允许 #7 继续首发而不声称已完成基准差异比较

#### Scenario: Reject a report against the previous release
- **WHEN** 报告完成后 active release 已从 R1 切换为 R2，而该报告仍以 R1 为 baseline
- **THEN** Gate 将报告标记为 stale 并阻止它满足发布，要求以 R2 重新生成比较

### Requirement: Immutable risk reports and result contract
成功风险 calculation report及其 risk items SHALL insert-only，并 SHALL 保存 candidate/baseline identities、`NO_BASELINE` 状态、policy、threshold、场景、simulation run/Metric identities/hashes、Metric values/units/置信区间或不可用原因、带符号绝对差异、可适用的相对差异、comparison status、severity、rule、evidence、assumptions、cohort、结构风险、Graph 补充证据引用、override classification和规范 calculation/report hash。允许的用户处理结论 SHALL 另存为引用原 calculation report/items 的insert-only decision report，保存用户说明、处理状态、decision hash及其后的#7二次确认/release audit引用；不得改写原items。报告 MUST 区分 `NO_BASELINE`、`COMPARABLE`、`NOT_COMPARABLE`、`UNAVAILABLE` 和 `STALE` 等状态，不得用同一空值表达不同原因。

报告读取 SHALL 返回创建时的事实；相对当前 candidate、release、policy、threshold、实现可用性的 freshness/Gate projection SHALL 在读取时另行计算，不得修改历史行或 result hash。相同规范输入、规则和实现版本 SHALL 产生相同有序 items 与 result hash，Job/report ID、时间、UI 状态和可选 Graph provider 状态不得参与确定结果 hash。

#### Scenario: Reopen an immutable historical report
- **WHEN** active release、threshold 或当前实现已在报告完成后变化
- **THEN** GET 仍返回原报告事实与 hash，并单独显示当前 stale/compatibility projection，而不按新阈值改写历史 severity

#### Scenario: Reproduce a deterministic report
- **WHEN** 相同 candidate/baseline、policy、threshold、simulation results 和规则版本以不同数据库枚举顺序重复复核
- **THEN** 系统产生相同 comparison statuses、ordered items、severities、evidence 和 result hash

### Requirement: Impact and Graph evidence remain explanatory only
系统 MAY 引用 #9 已保存且与同一 candidate/baseline/revision/Graph identities 匹配的确定路径或疑似证据作为报告解释。所有 Graph/影响证据 SHALL 明确标识来源、snapshot/hash、确定或疑似类别及 freshness；缺少 #9、影响报告 stale、Graph 服务降级、检索失败或疑似分数变化 MUST NOT 改变 Metric 差异、threshold comparison、severity、覆盖资格或 risk Gate 结论。

#### Scenario: Add a matching impact path as evidence
- **WHEN** #9 报告提供与同一 revision pair 精确匹配的确定路径
- **THEN** 风险报告可链接该路径帮助解释受影响对象，但其风险等级与没有该路径时完全相同

#### Scenario: Ignore suspected evidence for severity
- **WHEN** 疑似 Graph 关联的置信分数变化或检索能力不可用
- **THEN** 报告更新当前证据可用状态但不改写不可变风险项、result hash 或发布判断

### Requirement: Risk review API, Job, and idempotency contract
OpenAPI SHALL 定义 `POST /api/v1/risk-reviews` 的 tagged command。`evaluate` 请求明确绑定 candidate、可空但有显式首发语义的 baseline、release policy、按场景/Metric 指定的 candidate/baseline simulation result identities、可选匹配的 impact evidence refs，以及一个 tagged threshold selection：引用既有 enabled version，或以明确确认启用 starter/创建修改版并由服务端在同一幂等命令中固定新 version identity。`record_numeric_decision` 请求 SHALL 绑定一个仍匹配当前上下文的 calculation report/hash、允许覆盖的 item identities和非空用户说明，并创建新的不可变 decision report，不重新解释或修改原计算。两类请求均要求项目范围 `Idempotency-Key`；合法任务 SHALL 返回 `202`、Job URI 和最终 `result_type/result_id/result_url`，相同 key 与相同规范 input SHALL 返回同一 Job，相同 key 用于不同 input SHALL 返回稳定 409 幂等冲突。

系统 SHALL 复用 `GET /api/v1/jobs/{id}`、SSE `GET /api/v1/jobs/{id}/events` 和 `POST /api/v1/jobs/{id}/cancel` 暴露 `queued/running/succeeded/failed/canceled/interrupted`、阶段、进度、warning、断线续传和取消。只有全部确定比较完成并在短事务封存报告后 Job 才能 succeeded；失败、取消或中断不得暴露部分报告为成功。`GET /api/v1/risk-reviews/{id}` SHALL 返回不可变历史详情与当前 freshness/Gate projection。请求和工作错误 SHALL 使用 RFC 9457 Problem Details 与稳定 code，且不得暴露 SQL、堆栈、secret 或本机路径。

#### Scenario: Create and retrieve a risk report
- **WHEN** 合法复核请求被接受并完成
- **THEN** POST 返回的 Job 最终指向 GET 可读取的不可变报告，且报告的所有 identities、差异、状态、severity、证据和 hash 与实际固定输入一致

#### Scenario: Resume after a disconnected client
- **WHEN** SSE 在风险 Job 运行中断开或页面刷新
- **THEN** 客户端通过持久化 Job/event ordinal 与轮询恢复真实阶段和终态，不创建第二个 Job或从进度推断成功

### Requirement: Versioned risk Gate registration
系统 SHALL 向 #7 Gate Registry 注册稳定、版本化的 risk capability/Gate descriptor 和 VersionContributor。Gate evaluation SHALL 按 release policy 核对 candidate/config/version、当前 baseline、threshold enabled identity、required/optional scenes 与 Metrics、simulation/result identities、报告状态/hash、comparison statuses、结构规则、severity、override classification 和 freshness；Gate MUST 不启动 simulation、风险 Job、Graph 查询或发布副作用。

后续发布只有所有 required 风险输入完全匹配且报告未 stale、没有不可覆盖 BLOCK、没有 required `NOT_COMPARABLE`/`UNAVAILABLE` 时才可满足 risk Gate。optional 不可比较/不可用和普通 WARNING SHALL 作为可见 WARNING evidence；可覆盖数值 BLOCK SHALL 保持 BLOCK 并携带可覆盖分类供 #7 验证非空说明与二次确认。Gate evidence SHALL 只引用不可变 report/item/run/Metric identities 与 hashes。

#### Scenario: Pass an exact comparable report
- **WHEN** 后续 candidate 对当前 release 的报告完整匹配 policy/threshold/simulation identities，required Metrics 可比较且没有 BLOCK
- **THEN** risk Gate 返回 PASS 或带普通 WARNING 的可继续结果，并提供不可变 report/item evidence refs

#### Scenario: Block a required identity mismatch
- **WHEN** 报告缺少 required Metric、threshold 未启用、baseline 非当前 release或任一必需 result/version 不匹配
- **THEN** risk Gate 返回 BLOCK/STALE/UNAVAILABLE 的明确原因，不复用近似结果或接受说明替代证据

### Requirement: Accessible risk review and threshold workflow
`/risk-reviews` SHALL 提供 threshold 未配置/启用与历史版本、candidate/baseline 选择、policy required/optional 标识、同场景 Metric 对比、绝对/相对差异、置信区间或缺失原因、10%/25% 与绝对边界、cohort、结构风险、假设、Graph 解释证据、BLOCK/WARNING/INFO 文本标签、允许的覆盖说明/二次确认、Job 进度和不可变历史详情。`/versions/:id/diff` MAY 链接当前风险摘要，但 MUST 使用同一服务端报告/Gate projection，不得在客户端重新计算风险或发布资格。

页面 SHALL 明确覆盖首次加载、局部刷新、空态、`NO_BASELINE`、阈值未配置、Metric `UNAVAILABLE`、`NOT_COMPARABLE`、stale、缺失实现、提交中、SSE 断线轮询、canceled/interrupted、任务失败、可重试/不可重试失败和历史只读状态。颜色、边界线和图标必须有等价文本；键盘用户 SHALL 能选择输入、查看指标/证据、定位原因、填写允许的覆盖说明并完成二次确认，错误与终态变化后焦点 SHALL 移至可理解位置。

#### Scenario: Explain a non-comparable required metric
- **WHEN** 用户查看 baseline 为 0 且无绝对阈值的 required Metric
- **THEN** 页面显示 `NOT_COMPARABLE`、原因、数值/单位、threshold/Metric identity 和“阻止发布”，不显示 0% 或安全状态

#### Scenario: Hide override controls for structural blocks
- **WHEN** 报告包含静态公式环或无界规则 BLOCK
- **THEN** 页面显示不可覆盖原因和 validation evidence，不渲染可提交的覆盖控件

### Requirement: Deterministic boundary, Gate, and workflow verification
系统 SHALL 以版本化 fixtures/golden 覆盖三类 Metric 风险方向、相对 10%/25% 精确边界、正反方向、绝对阈值、zero baseline、required/optional `NOT_COMPARABLE` 与 `UNAVAILABLE`、cohort/stable-ID 选择、五类 Metric 置信区间/缺失原因、新乘区/重复乘算、三类不可覆盖结构错误、数值覆盖审计、`NO_BASELINE`、当前 release 变化、threshold/simulation stale、Graph evidence 隔离、API/Job/Gate 和 UI 状态。相同固定输入在不同 map/SQLite 顺序、独立进程和重试中 MUST 产生相同规范 items、severity、evidence 和 result hash。

#### Scenario: Repeat all threshold boundary fixtures
- **WHEN** 固定 fixture 在打乱对象、cohort、Metric 和存储枚举顺序后重复执行
- **THEN** 所有 comparable/NOT_COMPARABLE 状态、精确边界 severity、结构风险、Gate evidence 和 report hash 均保持相同

#### Scenario: Complete the first and subsequent release flows
- **WHEN** E2E 先完成无基准必检模拟并确认建立 baseline，再对后续 candidate 运行比较、调整配置或确认允许的数值风险
- **THEN** UI、报告与 Gate 分别遵守 `NO_BASELINE`、当前 release baseline、required/optional、覆盖与不可覆盖边界，且历史报告保持只读
