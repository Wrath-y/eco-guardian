## Context

参见 [proposal.md](./proposal.md) 的动机与 [spec.md](./specs/balance-risk-assessment/spec.md) 的行为契约。当前仓库仍是规划基线：没有 `go.mod`、`package.json`、Go/Vue 业务源码、迁移或测试；#6、#7、#9、#10 的 apply-ready OpenSpec artifacts 只是接口约束，不代表实现已经存在。

#11 的硬实现依赖是 #6、#7、#10。#6 冻结精确 FULL `ValidationGate`、版本化 DSL/AST/index、decimal128/NumericPolicy 以及静态公式环、无界事件和无上限叠加的不可覆盖诊断；#7 冻结 revision/current release/release policy、VersionContributor、Gate Registry、统一 Job/SSE/cancel 和发布确认审计；#10 冻结不可变 simulation run、完整复现指纹、Metric 方向/绝对阈值/置信区间/不可用原因。apply 时这些边界缺失或不兼容必须停止，不能在 risk 模块建立平行 revision、validator、decimal、simulation、Job 或 Gate 模型。

#9 只提供可选、只读的解释证据。风险数值和 severity 是纯本地确定性结果，不依赖 Graph、local-rag、Embedding、Rerank、LLM 或 AI Provider；这些能力缺失或降级不得改变 Gate。

## Goals / Non-Goals

**Goals:**

- 用一个规范 `RiskInputV1` 同时驱动 Job 幂等、报告 hash、stale 判定和 #7 risk Gate，避免 UI、worker 与发布流程各自定义“同一次比较”。
- 把 threshold version、确定计算、不可变报告、可追加人工审计和当前 Gate/freshness projection 分开，使历史可复现又能正确反映 active release 变化。
- 复用 #6/#7/#10 的稳定身份、版本与结果，只在 risk 边界实现 cohort admission、阈值比较和结构风险汇总。
- 对 10%/25%、Metric 方向、zero baseline、绝对阈值、required/optional、`NO_BASELINE` 和覆盖边界给出可 golden 测试的唯一 v1 语义。

**Non-Goals:**

- 不实现通用平衡评分、机器学习异常检测、自动阈值调参、跨场景综合排名或脱离场景的“平衡/安全”结论。
- 不修改 #6 validation severity，不重算 #10 Metric/置信区间，不运行 #9 Graph 查询，也不在 risk Gate 内启动模拟、发布或备份副作用。
- 不让影响路径、疑似关系、AI 文本或人工说明进入数值比较或 severity hash。
- 不为 threshold/report 提供原地 PATCH/DELETE，不把 risk report 变成配置、simulation 或 release 的第二事实源。

## Decisions

### 1. Add a risk module behind existing validation, versioning, simulation, Job, and Gate ports

新增绿地边界：

```text
internal/risk/
  contract/       # threshold/input/report/item/version canonical DTO and hashes
  threshold/      # immutable versions, starter fixture, scope resolution
  cohort/         # balance_group/stable-ID selection and run participant admission
  comparison/     # direction, zero-baseline, relative/absolute boundary rules
  structure/      # versioned multiplier rules plus #6 evidence adapters
  orchestration/  # admission, Job, cancellation/recovery, immutable sealing
  gate/           # #7 descriptor/evaluation and VersionContributor adapters
internal/storage/sqlite/  # threshold/report/item repositories; reuse #7 jobs/events
internal/httpapi/         # generated DTO and Problem Details adapters
internal/app/             # Registry, worker, Gate/VersionContributor composition
api/openapi.yaml
web/src/features/risk-reviews/
tests/fixtures/risk-v1/
e2e/
```

`contract/threshold/cohort/comparison/structure` 只依赖不可变领域 DTO、#6 decimal/validation evidence 和 #10 Metric result ports，不依赖 Gin、SQLite、Vue、Graph client、clock 或 working state。`orchestration` 依赖 revision/release/policy readers、ValidationGate、simulation run reader、threshold/report stores、#7 JobStore/EventStore 和 bounded executor。`gate` 适配 #7 Gate/VersionContributor，但不导入 release worker。

选择模块化单体内的纯计算 core，是因为比较规模受 policy 的场景/Metric/cohort 限制，不需要 sidecar 或分布式队列。把比较放在 HTTP handler、Vue 或 #7 release worker 中会复制 hash/阈值/错误语义并难以历史复现，因此不采用。

### 2. Freeze one canonical risk input before creating work

application service 在一个一致读中解析 candidate、current active release、policy 与 threshold，并读取精确 simulation runs。后续发布的 `RiskInputV1` 至少包含：

```text
project_uuid
candidate revision/config/version-manifest identities
baseline release/revision/config/version-manifest identities, or explicit NO_BASELINE
release policy identity/hash and required/optional scene/Metric declarations
threshold identity/body hash/enabled state and rule Registry versions
ordered comparison subjects: balance_group or singleton stable ID
ordered candidate/baseline scene, run, input/result and Metric identities/hashes
Metric value/unit/direction/target range/absolute-threshold/CI/unavailability facts
validation run/result identity and structural issue evidence
comparison/cohort/structural/report schema versions
optional matching impact evidence references outside the deterministic core hash
```

所有默认值在 hash 前展开；对象 key、set 和 registry identities 按规范 UTF-8 bytes 排序，场景/Metric/cohort 顺序由 policy 与规范 identity 决定，数值只使用 #6 canonical decimal，空 baseline 是带标签的 union 而不是缺失字段。`input_hash = SHA-256(domain-separated canonical RiskInputV1)`。Job/report/time/UI/worker identity、人类说明和 Graph 可用性不进入确定 risk input；Graph refs 另有 evidence manifest hash，不能影响 calculation hash。

创建 Job 前同步检查：candidate 与 baseline 精确 FULL validation、policy/threshold enabled、run 成功/可复现、revision/scene/sample/seed/Metric/实现/hash 匹配、cohort participant closure。相同项目 `Idempotency-Key + input_hash` 复用原 Job，不同 input 冲突。选择一个完全展开的输入，而不是只 hash revision pair + threshold ID，是为了让 stale 和 Gate 能定位具体 run/Metric/版本漂移。

### 3. Store immutable threshold versions and seed an inactive starter fixture

`ThresholdRegistry` 编译期冻结 threshold schema、comparison/structural rule manifests 和 10%/25% 起始 fixture。additive migration 只幂等 seed 一个 `enabled=false` 的 starter template identity；首次明确启用会复制完整 canonical body 并创建新的 `enabled=true` threshold version。修改边界、方向覆盖、绝对值、场景、Metric、group、结构 rule 或启用状态一律创建新 UUIDv7/display version，不更新旧 row。

threshold entry 的唯一作用域为 `(scene_id/version selector, metric_id/version selector, balance_group nullable)`。有非空 group 时先解析完全匹配 entry，再解析同 scene/Metric 的 `balance_group=null` 项目默认；无 group 的 singleton 只使用 null 默认。不存在唯一匹配、出现同优先级冲突或 Metric direction/单位不兼容都使该项 `THRESHOLD_NOT_CONFIGURED/INVALID`，不能随意选一个。实际解析链与 entry identity 保存到 item evidence。

起始 fixture 为每个已启用 Metric 的风险方向提供相对 WARNING `0.10`、BLOCK `0.25`；绝对边界来自 Metric Module/显式 threshold entry，模板不猜测单位值。选择 inactive seed 而不是启动时自动启用，是为了满足“首次明确启用或修改”并防止默认值被误认为项目结论。

### 4. Resolve cohorts from revision facts and require simulations to cover the same subject set

`CohortResolver` 只读取 candidate/baseline revision 的 entity envelope 与被绑定 scene/run 的 participant stable IDs：

- entity 有非空 `balance_group` 时，候选集合是同 project/revision、同 kind、同 group 且参与对应 scene 的稳定 ID 集；baseline 用同 group 和 kind 独立解析；
- entity 没有 group 时，subject 是 singleton stable ID，baseline 必须存在相同 stable ID；
- 每个集合按 stable ID raw UTF-8 bytes 排序，保存 kind、group、members、scene participant mapping 与 selection evidence；名称、标签和 Graph 相似度不参与；
- group 为空、成员缺失、scene participant 不覆盖、kind/group 漂移或运行输入与解析集合不同，产生显式 comparison status，不静默排除。

风险模块不自创跨实体 Metric reducer。group 比较要求 #10 的 candidate/baseline run 分别以解析出的 cohort 成员作为同场景 participant set，直接比较各 run 已封存的 Metric aggregate；singleton 则要求同 stable ID participant。这样 `balance_group` 决定同定位模拟输入集合，Metric/样本聚合仍由 #10 唯一拥有。若现有 #10 run projection 不能证明 participant closure，结果不可用于 #11，而不是按名称或当前 revision 补齐。

选择“验证 run participant set”而不是在 risk 中聚合任意成员指标，是为了避免第二套 Metric/CI 语义，也使 cohort 选择可由 run input hash 审计。

### 5. Define one decimal comparison transform for all v1 directions

比较器先保存带符号 raw delta `candidate - baseline`，再计算非负 `risk_delta`：

```text
higher_is_risk: max(0, candidate - baseline)
lower_is_risk:  max(0, baseline - candidate)
target_range:   max(0, distance(candidate, inclusive range)
                        - distance(baseline, inclusive range))
distance(x,[lo,hi]) = lo-x if x<lo; x-hi if x>hi; otherwise 0
```

baseline aggregate 非零时 `relative_risk = risk_delta / abs(baseline)`，使用 #6 decimal128、单位校验和 canonical ratio string。baseline 为规范零时不进入除法，只能解析单位兼容的绝对 WARNING/BLOCK。对于 `target_range`，raw values、inclusive range、两侧 distance 与 risk_delta 都入 evidence；这避免跨越区间时只看 raw delta 的错误方向。candidate 沿非风险方向移动时 risk_delta 为 0，但 raw delta 仍可审计。

boundary comparator 使用 `>=`：BLOCK 优先于 WARNING，达到 `0.25` 为 BLOCK，达到 `0.10` 为 WARNING，其余可比较结果为 INFO。绝对边界使用相同优先级。比较只消费 #10 canonical aggregate；candidate/baseline CI 与 assumptions 原样进入 evidence，但 v1 不用 CI 概率性调整 severity。若未来改变变换、normalization 或使用 CI，必须增加 comparison rule version 与新 golden，不能修改 v1。

这个变换比 `(candidate-baseline)/baseline` 更安全：分母用绝对 baseline，负 baseline 不会反转方向；zero baseline 有明确分支；target range 使用“离目标更远”而不是猜测高/低方向。使用 float epsilon 或 UI 百分比舍入会破坏精确边界，故不采用。

### 6. Model comparison status separately from severity and overall Gate resolution

每个 `RiskItemV1` 使用 tagged `comparison_status`，至少包括 `COMPARABLE`、`NOT_COMPARABLE`、`UNAVAILABLE`、`STALE`；报告另有 `NO_BASELINE`。severity 只在有规则结论时为 BLOCK/WARNING/INFO，不能用 INFO 表示“没算”。item 同时保存 policy role required/optional 与 `overridable` 分类。

总体规则固定：

- threshold/policy 缺失、STALE、required UNAVAILABLE/NOT_COMPARABLE、不可覆盖 validation/structural BLOCK → Gate BLOCK/UNAVAILABLE/STALE；
- optional UNAVAILABLE/NOT_COMPARABLE → Gate WARNING；
- comparable item → 取规则 severity；多个项按 BLOCK > WARNING > INFO 汇总，但保留每项；
- `NO_BASELINE` 不是 INFO/PASS；只有 required simulation/Metric/threshold/结构检查完成且建立基线确认存在时，#7 才能继续首发；
- 可覆盖数值 BLOCK 仍为 BLOCK，Gate descriptor 只标记 `overridable=true` 与允许的 confirmation schema，#7 验证 reason/second confirmation 后决定是否继续。

分离 status、severity、policy role 和 resolution，避免 `NOT_COMPARABLE` 被渲染为 0/INFO，也避免 override 改写原 severity。

### 7. Reuse #6 structural facts and add only versioned diff-based risk rules

`structure` 有两类 adapter：

1. `ValidationEvidenceAdapter` 读取与 candidate 精确匹配的 FULL run，引用 `STATIC_FORMULA_CYCLE`、`EVENT_LOOP_UNBOUNDED`、`STACK_UNBOUNDED` 等 code/fingerprint/cycle evidence，固定映射为不可覆盖 BLOCK；不重跑 parser、SCC 或 rule-safety 算法。
2. `StructuralRiskRegistryV1` 在 #7 canonical diff 与 #6 revision-scoped typed AST/formula/stack indexes上运行纯规则，检测 PRD 要求的新乘区和重复乘算。每条 rule 固定 stable ID/version、输入 index versions、匹配模式、severity、evidence schema 与 golden；结果按 rule/entity/path/ordinal 排序。

v1 “新乘区”只在 diff 新增或改变为 Multiply stack layer/已注册乘法组合且 baseline 无等价 canonical node 时命中；“重复乘算”只在同一规范 output/target path 的依赖链中，同一 canonical multiplier source 在 candidate 的有界链计数高于 baseline 且超过 rule 声明上限时命中。任何无法由 typed AST/index 与 canonical paths 证明的自然语言说明只可成为人工/Graph evidence，不能命中结构 rule。

选择消费 diff/index 是为了定位“新增/重复”相对变化；重新解析 payload 或通过 Graph 路径猜测会复制 #6 事实并受 provider 状态影响，故不采用。rule severity 的改变必须新版本，历史报告不按新规则重解释。

### 8. Separate deterministic report facts, optional Graph evidence, and immutable human decisions

报告封存分三层：

- `calculation`: 由 RiskInput、ordered comparison/structural items、threshold resolution 与 assumptions 形成 canonical `calculation_hash`；完全确定且 insert-only。
- `explanation evidence`: 可选引用 #9 report/path/suspected evidence identity、revision pair、Graph snapshot/hash 与 freshness；只允许附加在 sealing 前已验证匹配的 refs，单独 hash，不进入 severity/calculation hash。provider 后续状态只作为 GET projection。
- `decision report`: 用户查看 calculation report 后，可通过同一 risk-review collection 的 tagged `record_numeric_decision` command提交 source report/hash、允许覆盖的 item identities和非空 reason。服务端重新核对当前上下文、item override classification与原hash后，插入一个引用 source calculation/items 的 immutable decision report，保存 reason、decision status/hash；不复制或改写原 items，也不改变 calculation hash。
- `release confirmation`: #7 release command 取得二次确认，保存 candidate/baseline/policy/threshold、decision report与原 calculation/item identities的override audit；风险详情可关联展示该release audit，但不更新任一risk report。

这沿用 #7 已冻结的“覆盖说明随 candidate/baseline/policy/threshold/risk result 保存在 release 审计”契约，同时满足风险历史保存策划说明：第一步是immutable decision report，第二步是`POST /api/v1/releases`中的二次确认。没有mutable override API，也不能仅凭decision report绕过发布预检。

选择三层模型，是因为把 Graph 分数或用户说明写进确定 result hash 会让同一数值输入不再可复现；原地更新 report 的 override 状态又会违反历史不可变性。

### 9. Keep mutable Job orchestration separate from immutable threshold and report data

在 #5–#10 项目 DB 上增加以下逻辑数据；物理表可在保持约束、备份与迁移兼容的前提下拆分：

| Logical data | Responsibility / constraints |
|---|---|
| `threshold_versions` | threshold UUIDv7/display version、starter origin、enabled、canonical scope/rules/body/hash、Registry versions；insert-only |
| `risk_reviews` | report UUIDv7、kind=calculation/decision、Job unique、source calculation FK、input/calculation/evidence/decision/report schema identities、candidate/baseline/policy/threshold、用户说明/处理状态、status/summary、canonical report；成功 row insert-only |
| `risk_items` | report + stable ordinal、scene/Metric/cohort、run/value/CI/missing reason、deltas/status/severity/rule/evidence/overridable/hash；insert-only |
| `jobs` / `job_events` | 复用 #7 的 kind/status/phase/progress/warning/cancel/result/error/event ordinal |

成功封存使用短事务：检查 Job ownership/input hash/cancel generation，插入 review/items，设置 Job `result_type/result_id/result_url` 与 succeeded。唯一约束保证一个 Job 至多一个 report、item ordinal/identity 不重复。失败、取消或崩溃不能使半成品被 report GET 读取；risk 计算成本有界且只读，因此重启在完整 input/Registry 仍匹配时重跑整个计算，不持久化中间比较 checkpoint。

report canonical payload 保存源 identities 与必要证据，不复制 revision entity blobs、simulation sample/event 数据、#9 路径正文或 release。SQLite trigger/repository 不暴露 completed threshold/report/item update/delete。

### 10. Reuse #7 Job semantics for idempotency, cancellation, and recovery

POST flow 固定为 materialize identities → validation/policy/threshold/run/cohort admission → normalize/hash → idempotency lookup/insert Job → bounded deterministic calculation → seal。Job phases建议：

```text
QUEUED → MATERIALIZED → COMPARING → STRUCTURE_CHECKING
→ SEALING → SUCCEEDED
```

状态只使用 `queued/running/succeeded/failed/canceled/interrupted`。worker 在每个 scene/Metric/cohort/rule 边界和 seal 事务内检查 persisted cancellation generation；用户取消后不封存部分 report。应用中断把未完成 Job 标为 interrupted；重开时重新 materialize所有 identity/Registry/hash，完全相同才恢复原 Job并重跑，任何 active baseline、threshold、run或实现变化都以 recovery mismatch 结束，不混合版本。若 seal 已提交但 Job 终态未提交，按 Job unique report核对hash并补齐终态。

选择整次重算而非 sample checkpoint，是因为 #10 已完成高成本模拟，risk比较是受限 decimal/index遍历；额外 checkpoint 会增加可变事实却无显著收益。

### 11. Expose only the specified risk resources and reuse shared release confirmation

扩展 `api/openapi.yaml`：

- `POST /api/v1/risk-reviews`：tagged `evaluate` command携带candidate、explicit baseline/NO_BASELINE union、policy、ordered simulation result refs、optional matching impact refs和tagged threshold selection；tagged `record_numeric_decision` command携带source calculation report/hash、eligible item identities和non-empty reason。服务端分别创建immutable calculation或decision report；required `Idempotency-Key`；成功202/Location与统一Job DTO。
- `GET /api/v1/risk-reviews/{id}`：不可变 input/calculation/items/cohort/Metric value+CI or missing reason/deltas/status/severity/rule/evidence/assumptions/override classification/hash，以及 read-time freshness/Gate/release-audit links。
- 复用 `GET /api/v1/jobs/{id}`、`GET /api/v1/jobs/{id}/events`、`POST /api/v1/jobs/{id}/cancel`。
- GET 报告返回其 threshold version 完整只读投影；风险页面从当前 policy 引用和历史报告读取可选版本。MVP 不新增独立 threshold CRUD 路径，未被复核引用的自由阈值目录管理不在范围；每次 starter 启用/修改都随幂等 risk-review command 创建新版本，不提供 PATCH/DELETE。

风险覆盖不新增 mutable endpoint；reason保存在immutable decision report，二次确认与最终override audit随#7 `POST /api/v1/releases`处理。

同步错误至少区分 candidate/baseline/policy/threshold invalid、validation required/blocked、simulation missing/stale/unavailable、cohort mismatch、idempotency conflict；worker错误区分numeric/rule contract、cancel/interrupted/recovery mismatch/storage。全部使用 RFC 9457 并过滤 SQL、stack、secret、path和未知原始payload。handler只做 generated DTO/header/Problem Details/application adapter。

### 12. Register a strict risk Gate without turning it into an executor

Risk Gate descriptor 固定 capability/gate/contract version、required input identities、result schema 与 numeric override classification。evaluator读取 #7 `CandidateContext` 和 policy，重新解析 current active release与threshold，然后逐项核对 report candidate/config/version、baseline、policy、threshold enabled identity、required/optional scenes/Metrics、simulation/run/Metric/input/result hashes、cohort、rule versions、report hash和freshness。

Gate只返回 #7 已定义的 PASS/WARNING/BLOCK/UNAVAILABLE/STALE与 canonical evidence refs：required缺失/不可比较/不可用/stale阻断，optional项产生WARNING；不可覆盖结构 BLOCK 永远阻断；可覆盖 numeric BLOCK仍返回BLOCK并带`overridable=true`/confirmation schema。#7 GateSetEvaluator验证用户说明和二次确认并保留原result identity。Gate不创建Job、不自动启用threshold、不运行simulation/Graph/backup，也不读取working state。

首发时 evaluator要求 report=`NO_BASELINE`、所有 required simulation/Metric/threshold/结构检查完整及 #7 establish-baseline确认；它返回首发特定 evidence，但不伪造差异item。

### 13. Build `/risk-reviews` as a server-resource projection

Vue feature使用 generated client 与 TanStack Vue Query；Pinia不保存 report 或 Gate事实。页面拆为 threshold status/version selector、candidate/baseline/policy summary、scene/Metric comparison table、boundary/CI panel、cohort drawer、structural issues、assumptions/Graph evidence、Job progress、history、Gate checklist和release confirmation handoff。

Metric行把 `comparison_status` 与 severity 分列，始终显示 value/unit、CI或missing reason、signed delta、可用relative delta、threshold和required/optional。`NO_BASELINE`、threshold未配置、NOT_COMPARABLE、UNAVAILABLE、STALE、BLOCK/WARNING/INFO都用文本而非仅颜色；结构 BLOCK不渲染override控件。允许numeric override的reason在risk页面可编辑，但二次确认提交调用#7 release flow，服务端不接受客户端把Gate算成PASS。

202后按event ordinal订阅SSE，断线/刷新轮询Job并从result URL加载report；progress=100不推断成功。1024px桌面布局提供表格的移动端非目标但可缩放读法、键盘筛选/展开/证据链接/错误摘要/确认，焦点在validation错误、Job终态和stale变化后可预测移动。

### 14. Verify calculation, evidence isolation, Gate, persistence, and accessibility separately

- **Registry/golden**：starter threshold canonical body/hash与inactive启用；scope precedence；comparison/cohort/structural manifests；10%/25%与绝对边界；direction/target-range/zero/negative baseline canonical bytes。
- **Comparison/property**：shuffle map/row/Metric/cohort顺序；required/optional、UNAVAILABLE/NOT_COMPARABLE、CI passthrough；断言ordered items/calculation hash不变且Graph/user text无影响。
- **Structural contracts**：#6三个不可覆盖codes/fingerprints透传；new multiplier/repeated multiplication typed AST/diff fixtures；自然语言/疑似Graph不命中。
- **SQLite/fault injection**：threshold/report/item immutable，same/different idempotency，cancel每个safe point，seal事务前后crash，one Job/one report，recovery identity mismatch与migration rollback。
- **HTTP/OpenAPI/Gate**：202/Location、immutable GET、SSE resume/cancel、Problem Details/sanitization、NO_BASELINE/current baseline、threshold enabled、required/optional、stale、numeric override classification、generated client drift。
- **Vue/E2E**：首启threshold→首发NO_BASELINE确认→后续candidate比较→调整或提交numeric override；另覆盖zero baseline、missing Metric、structural block、stale、Graph evidence offline、restart、keyboard/focus/non-color labels。

## Risks / Trade-offs

- **[target-range 与负 baseline 容易产生方向反转]** → v1 使用明确 risk-distance transform 与 `abs(baseline)` denominator，所有边界以 decimal golden冻结；不使用 raw signed denominator或float epsilon。
- **[balance_group 的多个成员可能被误做第二套 Metric 聚合]** → risk只验证scene participant cohort并消费#10 run aggregate；无法证明participant closure即不可用，不在#11聚合样本或成员。
- **[旧 revision/run 缺少 #11 VersionContributor 或participant投影]** → 历史仍可读，但新risk Job/Gate明确UNAVAILABLE；从当前working state产生新revision/run，不回填虚假版本。
- **[starter template被误认为项目安全标准]** → seed为inactive，显式启用创建新version，UI持续展示template来源/threshold identity/假设。
- **[结构规则与#6 validation重复或severity漂移]** → 三类安全错误只消费#6 evidence；#11只拥有diff-based risk Registry，同version manifest/golden禁止漂移。
- **[Graph evidence晚到或provider降级导致报告看似变化]** → evidence与calculation hashes分离，GET显示availability projection，Gate只引用deterministic report facts。
- **[numeric override被误呈现为PASS]** → report/item永久保留BLOCK，Gate只声明overridable，真正reason/second confirmation由#7发布审计验证并链接原identity。
- **[阈值没有独立 CRUD 路径，无法预先维护未使用的版本目录]** → MVP 在幂等 risk-review command 内显式启用/创建并立即绑定 immutable version，GET 报告提供完整历史投影；独立目录管理留待有明确需求的后续 change，避免扩大当前接口范围。

## Migration Plan

1. apply 前验证 #6/#7/#10 真实实现已提供 exact FULL ValidationGate与issue evidence、Revision/Release/Policy readers、VersionContributor/Gate Registry、JobStore/EventStore/SSE/cancel、simulation run/Metric/CI/unavailability/participant/version projection和migration ledger；任一缺失即停止，不建临时替代。
2. 先实现 risk canonical contracts、comparison/cohort/structural Registry manifests与golden，再扩展 OpenAPI并冻结 risk POST 中的 tagged threshold selection、risk GET、Job和Problem Details fixtures；generated Go/TypeScript drift通过后才接handler/UI。
3. 使用#7 migration preflight加入additive `threshold_versions`、`risk_reviews`、`risk_items`；新项目幂等seed inactive starter fixture。已有真实项目在写migration前沿用不可绕过的Online Backup/integrity能力；能力缺失时停止升级，不把#14变成risk算法依赖。
4. 以feature disabled状态接入只读revision/policy/simulation adapters，完成scope/cohort/admission与deterministic calculation/fault tests；注册VersionContributor后仅新revision固定risk versions，旧revision未知身份保持unavailable。
5. 接入Job worker、report sealing、API和`/risk-reviews`；完成NO_BASELINE、stale、zero baseline、Graph隔离、cancel/recovery和可访问性验收后注册risk Gate。Gate启用不自动运行Job、启用threshold或修改历史。
6. 回滚应用只允许旧二进制声明支持当前DB Schema与项目引用的risk versions；不得数据库降级或删除threshold/report/item。功能回滚可停止新Job/Gate注册并保留历史GET。migration/启用失败时恢复迁移前兼容备份，任何旧report/severity/hash不原地改写。

当前没有会改变规格、架构或任务拆分的遗留问题。
