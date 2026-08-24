## Context

参见 [proposal.md](./proposal.md) 的动机和 [spec.md](./specs/reproducible-simulation/spec.md) 的行为契约。当前仓库仍是规划基线：没有 `go.mod`、`package.json`、Go/Vue 业务源码、迁移或测试；已有 #5–#9 apply-ready OpenSpec artifacts 只是后续实现边界，不代表接口已经落地。

#10 的硬实现依赖只有 #5–#7。#5 冻结单项目 `project.db`、不可变完整 revision manifest、Schema Registry、materialization 和事务边界；#6 冻结 FULL `ValidationGate`、版本化 DSL/AST/evaluator、NumericPolicy、单位与规则安全语义；#7 冻结 VersionManifest/VersionContributor、Gate Registry、统一 `jobs`/`job_events`、Job/SSE/cancel、幂等和 OpenAPI 生成边界。apply 时这些接口缺失或不兼容必须停止，不能创建平行 revision、validator、decimal、Job 或 Gate 模型。

#8 复用 #7 Job/Gate 的方式验证了扩展点，#9 复用 revision/diff/Job 的方式验证了分析模块边界；两者都不被模拟算法调用。模拟是纯本地确定性计算，不依赖 Graph、local-rag、Embedding、Rerank、LLM 或 AI Provider，它们的状态不得改变结果。

## Goals / Non-Goals

**Goals:**

- 在 transport、SQLite、worker 数、完成顺序与机器时钟之外形成纯、版本化、可 golden 测试的 simulation core。
- 用一个规范 input fingerprint 同时驱动 Job 幂等、checkpoint 恢复、result hash、历史复现验证和 #7 simulation Gate，避免多个“相同输入”定义。
- 复用 #6 的 parser/AST/evaluator/NumericPolicy，增加场景、事件、PRNG、Metric 和 aggregation 的窄版本化 Registry，而不复制公式或单位语义。
- 把可变 Job/checkpoint 与不可变 scene/run/Metric result 分开，使取消、崩溃恢复和只读历史都可解释。
- 以 OpenAPI 生成 DTO/client 和同一个 run projection 驱动 API、Job result、版本门禁与 `/simulations` 页面。

**Non-Goals:**

- 不实现通用战斗/技能脚本引擎、任意事件脚本、动态插件、运行时代码加载或未建模机制猜测。
- 不修改 #5–#7 的 entity/revision、validation severity、release policy、Job 状态或 Gate 语义；只通过现有 ports 注册。
- 不读取或写入 Graph，不运行影响分析、风险阈值比较、AI 设计、发布 saga 或备份流程。
- 不保存完整逐事件 trace 作为长期业务事实，不提供通用场景编程器、通用概率实验室或分布式 worker。
- 不用性能优化改变样本数、NumericPolicy、事件顺序、Metric 定义或结果规范编码。

## Decisions

### 1. Add a simulation module behind existing revision, validation, Job, and Gate ports

新增绿地边界：

```text
internal/simulation/
  contract/       # input/result/version manifests、canonical encoding、hash
  scenario/       # 四模板、clone/parameter schema、immutable definitions
  engine/         # state、event queue、time progression、action/effect execution
  random/         # engine-owned PRNG v1 and per-sample stream derivation
  metric/         # compile-time Metric Registry、sample collectors、aggregation
  orchestration/  # admission、Job、checkpoint、cancel/recovery、verification
  gate/           # #7 descriptor/evaluation and VersionContributor adapters
internal/storage/sqlite/  # scene/run/metric/checkpoint repositories；复用 #7 jobs/events
internal/httpapi/         # generated DTO、Problem Details、SSE/Job adapters
internal/app/             # Registry、worker、Gate/VersionContributor 组合
api/openapi.yaml
web/src/features/simulations/
tests/fixtures/simulation-v1/
e2e/
```

`contract/scenario/engine/random/metric` 只依赖不可变领域 DTO、#6 formula/evaluator/numeric interfaces 和纯 Registry，不依赖 Gin、SQLite、Vue、clock 或 working state。`orchestration` 依赖 `RevisionReader`、`ValidationGate`、scene/run/checkpoint stores、#7 `JobStore/EventStore`、clock/ID 与 bounded executor ports。`gate` 适配 #7 Gate descriptor/evaluation 与 VersionContributor，不导入 release worker。

选择模块化单体内的纯 core，而不是 sidecar 或队列服务，是因为默认 1000 样本目标小于 30 秒、事实与 Job 都在单项目 SQLite，跨进程协议只会增加版本/恢复面。把模拟放进 HTTP handler 或 revision save 事务会持有写锁、无法取消恢复并复制门禁，因此不采用。

### 2. Freeze one canonical simulation identity before creating work

application service 先在一个一致读中解析 source：直接 revision，或把 release 一次解析为其 revision；随后 materialize #5 immutable manifest，检查 #6 exact FULL Gate，并解析场景/版本实现。它构造 `SimulationInputV1`：

```text
project_uuid
config_revision_id, config_hash, version_manifest_hash
scene_id, scene_version, scene_schema_version, scene_body_hash
normalized parameters, participant IDs, action sequence, initial state
sample_count, seed, resource budgets
engine/event/time/prng versions
schema/dsl/evaluator-registry/numeric-policy versions
ordered metric module identities/versions
aggregation/result-schema versions
```

所有默认值在 hash 前展开；map/object key 按 UTF-8 bytes 排序，set 按规范 bytes 排序去重，动作/事件等有语义的数组保留定义顺序，数值使用 #6 规范十进制字符串，Duration 使用整数毫秒。编码器使用带 domain separator 和 contract version 的规范 JSON bytes，`input_hash = SHA-256(canonical input)`。project UUID 参与 Job 隔离；result 语义仍回显 revision/config identity。`release_id`、请求时间、Job/run ID、worker 数和 UI 字段不参与 input hash。

VersionManifest 的 simulation contributor 固定 scene contract、engine、event/time、PRNG、Metric/aggregation/result schema identities；revision 未注册 simulation contributor 时 Gate unavailable，不能用当前实现补历史缺口。请求中的版本字段只可作为并发前置条件，实际版本由 revision/Registry/scene definition 解析，客户端不能任意声明一个实现。

备选的“只 hash revision + scene 名称 + seed”无法区分 evaluator、Metric 或参数漂移；直接 hash transport JSON 会让省略默认值和字段顺序产生假差异，因此使用完全展开的领域输入。

### 3. Store immutable versioned scenarios and seed exactly four templates

`ScenarioRegistry` 编译期注册 scene schema/contract 和四个 template manifests。additive migration 在项目尚无对应 identity 时幂等 seed 30 秒单目标、180 秒单目标、60 秒三目标、60 秒极限叠层到 `scenario_definitions`；每条保存 canonical body/hash、template origin、schema/version 和 immutable timestamps。clone 复制完整定义并分配新 scene/version identity；任何编辑实际创建新版本，旧 run 始终解析原 body/hash。

场景 schema 是固定结构而非 AST/脚本：participants/targets、ordered actions、initial attributes/resources/effects、duration、seed default、typed parameter declarations、event/step/sample/runtime budgets。参数 overlay 只能写 declared path，并通过服务端 Schema、类型、单位、范围和引用校验；overlay 后再规范化进 input。参与对象必须来自绑定 revision，显示名称不参与身份。

四模板共用受限 event/action vocabulary 和 #5 v1 event registry。新增事件或 evaluator 必须编译期注册并提升 contract/version，不能把未知 extension、自然语言或 JSON code 当作行为。选择不可变 version rows 而不是原地更新，是为了历史 run 可独立读取；选择 seed 到项目 DB 而不是只在二进制临时生成，是为了备份和历史输入完整。

### 4. Implement a monotonic discrete-event engine with one total order

每个 sample 建立独立 `SimulationState` 与 binary heap event queue。规范 event 至少含：

```text
time_ms, rule_priority, source_stable_id, insertion_ordinal,
event_kind/version, target identity, canonical payload/evaluator identity
```

比较器严格按 `time_ms → rule_priority → source ID raw UTF-8 bytes → insertion_ordinal`。`insertion_ordinal` 是 sample-local 单调无符号整数，初始事件按 scene action/participant 规范顺序入队，后续事件在当前 evaluator 的确定派生顺序中分配；永不使用 map iteration、goroutine order 或数据库 row order。重复完整 key 属于实现错误并以稳定 internal contract failure 停止，不能任意打破平局。

主循环弹出最小事件，把 `now` 单调推进到该事件的整数毫秒时间，再通过注册 evaluator 更新 state、发出 Metric observations 与后续事件。同一时间仍逐事件执行；超过 scene end 的事件保留为未执行边界，不做最后一帧近似。向过去入队、ordinal 溢出、零时间无界派生、event/step budget 用尽都产生稳定失败。墙钟只用于 Job timeout/cancel，不参与领域状态或 result hash。

备选的固定帧/tick 会增加未定义的帧宽、边界舍入和空步骤；按插入容器自然顺序会受实现影响。因此采用下一事件时间推进和完整 total order。

### 5. Reuse #6 evaluator and NumericPolicy instead of creating simulation arithmetic

engine 通过 #6 的版本化 typed AST/evaluator port 执行 FormulaBinding，传入只读 `self/source/target/scenario` snapshot context。场景动作/TriggerRule/Modifier/StackRule adapters 只把 #5 结构映射为 #6 已验证的 evaluator inputs；它们不重新 parse、不接受客户端 AST，也不降低 #6 的 scope/type/unit/cycle结论。FULL Gate 必须先通过，运行期仍对缺失引用、contract drift 和数值 trap 做防御性失败。

所有数值由 #6 decimal wrapper 处理：precision 34、exponent -6143..6144、HALF_EVEN、finite-only、canonical string；单位运算只走 v1 Unit Registry。engine/state/Metric interface 不暴露 float64。Duration 在持久/事件边界使用整数毫秒，Percentage 使用 ratio。显示转换只在 Vue adapter。

新增 simulation evaluator（例如一个注册 action/event 的结构化适配器）必须声明 stable evaluator ID、input/output schema、纯度和 implementation version，进入完整 fingerprint 与 golden。相同 version 的代码/fixture不可原地改变；#6 evaluator retention 与本 change 的 runtime manifest共同保证历史语义。

选择复用而不是复制 evaluator，是因为两个 decimal/DSL 实现即使当前结果一致，也会在边界和升级时破坏 Gate。使用 float 做聚合再转 decimal同样被禁止。

### 6. Own and freeze the PRNG stream at the engine boundary

`internal/simulation/random` 暴露窄接口 `Stream(version, seed, sampleOrdinal)` 和固定-width integer/uniform draw；底层实现与 seed expansion 写入 `simulation-prng-v1` manifest、source golden和跨进程 fixture。它不包装 Go 标准库默认 generator，也不共享全局 state。seed 在 API/domain 边界规范为固定宽度无符号整数的十进制表示。

每个 sample stream 仅由 `(prng_version, seed, sample_ordinal)` 通过版本化 domain-separated derivation产生；worker/chunk identity 不参与。draw 消耗只发生在 evaluator contract 的显式随机节点，按事件 total order推进；短路/未执行分支不预消费，批量优化不能改变 draw 次序。PRNG state只存在于 sample/checkpoint内部，不进入 Metric API。

具体 v1 算法、常量、seed expansion、integer rejection sampling 与 `[0,1)` 映射必须作为首个实现任务一次冻结，并用公开 fixed-vector fixture验证；之后只能以新 PRNG version增加。这里选择自有小型、可移植、固定整数语义实现，而不是依赖库类型，是为了避免 Go/依赖升级改变流。

### 7. Parallelize only complete samples and merge by ordinal

orchestrator先建立稳定 sample ordinals `0..sample_count-1`，用有界 worker pool运行独立 samples。worker只返回 immutable `SampleResult`：ordinal、input/fingerprint hash、terminal status、ordered Metric observations/accumulators、sample hash和诊断。结果先放到 ordinal-indexed buffer/checkpoint，不在完成 goroutine中更新全局 aggregate。

所有请求 sample成功后，单一 deterministic reducer按 ordinal升序把 sample accumulator交给每个 Metric Module。Module manifest固定 collector schema、aggregate/置信区间算法、confidence assumptions、rounding/output schema与direction；归并只使用 #6 decimal/integer。五个内置 module（DPS、治疗、生存、资源、控制）有独立输入声明和状态，按 metric ID raw bytes规范排序写结果。缺输入产生 `UNAVAILABLE(code, missing refs)`，不进入零值累计，也不阻止其他 module封存。

result canonical bytes包含完整 input fingerprint、按 ordinal语义产生的 aggregate、ordered Metric values/confidence intervals/unavailability、assumptions/warnings/result schema；不包含 sample scheduling、Job/run ID、时间或机器。`result_hash` 在持久事务前和读取验证时计算。需要逐样本证据的 Metric只保存有界 accumulator/hash或版本化摘要，不长期保存完整事件 trace。

选择“样本并行、结果顺序归并”而不是并行 reduce tree，是因为后者的树形和非结合舍入会随 worker数改变。默认1000个 sample足以有界缓冲；容量测试决定batch/checkpoint大小，但不能改变逻辑归并顺序。

### 8. Separate mutable orchestration/checkpoints from immutable results

在 #5–#7 项目 DB 上增加以下逻辑数据；物理表可在保持约束和备份兼容的前提下拆分：

| Logical data | Responsibility / constraints |
|---|---|
| `scenario_definitions` | scene/version/template origin/schema/canonical body/hash；insert-only，被 run引用后永不改写 |
| `simulation_runs` | run UUIDv7、Job unique、revision/config/input/fingerprint/result identities、terminal capability、canonical result；成功 row insert-only |
| `simulation_metric_results` | run + ordered metric ID/version、status/value/CI/unit/direction/assumptions/unavailability/hash；insert-only |
| `simulation_job_checkpoints` | Job/input/fingerprint、completed sample ranges/ordinals、sample accumulator/hash、phase/generation；可恢复 staging，不作为成功结果 |
| `simulation_verifications` | source run、reproduction run、compared fingerprint/result hashes、verified/failed outcome；insert-only |
| `jobs` / `job_events` | 复用 #7 状态、phase/progress/warning/cancel/result/error 与 event ordinal |

成功封存使用短事务：检查 Job ownership/input/fingerprint/cancel generation和全部 sample集合；insert run/Metric rows；insert可选 verification；设置 Job result_type/id/url与succeeded；最后清理或标记checkpoint完成。唯一约束保证一个 Job至多一个 run，Metric identity不重复。任何失败/取消/崩溃都不能让 staging被 `GET simulation-runs`看到。

checkpoint以完成 sample为最小可复用单位，保存canonical sample accumulator + hash，不保存进行中event heap。恢复时逐条核对input/fingerprint/sample ordinal/hash；不匹配则丢弃该staging sample并以同ordinal重算。这样避免序列化任意 evaluator内部状态，同时仍支持1000样本的有效恢复。

备选的原地更新 run progress会让历史资源半成品可见；只存在内存则进程中断必须从头且无法证明取消边界，因此采用staging/final分离。

### 9. Reuse #7 Job semantics for admission, cancellation, and recovery

POST application flow固定为：materialize/validate → normalize/hash → synchronous capability/budget/evaluator checks → idempotency lookup/insert Job → worker execution。`(project_uuid, idempotency_key)`复用#7唯一语义；same key + input hash返回原Job，different hash返回`IDEMPOTENCY_CONFLICT`。policy自动运行可使用domain-separated deterministic key并按完整input hash收敛；人工用新key重跑可产生新run identity和相同result hash。

Job phases建议固定为：

```text
QUEUED → MATERIALIZED → SAMPLES_RUNNING → AGGREGATING
→ SEALING → SUCCEEDED
```

`cancel_requested`作为Job generation/intent持久化。worker在调度sample前、event loop有界检查点、sample提交前、aggregate前和seal事务内检查；已开始sample可以停止且其不完整状态不复用。重复cancel幂等。budget/timeout为failed，用户cancel为canceled；进程/项目关闭时未完成Job标记interrupted并保留可验证checkpoint。

启动/项目重开 recovery扫描本模块的queued/running/interrupted Job：重新materialize revision/scene与完整实现fingerprint，完全相等才在同Job generation下复用完成sample并运行缺失ordinal；任何实现缺失/漂移都保持interrupted/failed diagnostic，不混合版本。重放封存事务若run已存在，则核对hash并补齐Job终态；无法证明相等进入recovery-required failure，绝不猜测成功。

选择同Job恢复而不是创建“resume Job”，是为了SSE/result URL和idempotency保持单一身份。取消是用户终止而非暂停，之后若要运行必须明确新POST；这避免取消后后台悄然继续。

### 10. Make compatibility and reproducibility verification explicit

组合根维护compile-time `SimulationImplementationRegistry`，启动时拒绝重复engine/event/PRNG/evaluator/Metric/aggregation identity、版本/hash冲突、缺失fixture或同version语义漂移。v1 major build manifest列出该major曾写入项目的全部 evaluator；CI用兼容fixture阻止删除。读取项目时把run fingerprint与registry匹配为`reproducible`或`missing implementations[]`，该projection不更新历史row。

复现验证仍使用同一个POST endpoint：请求可选携带`verify_run_id`作为审计目标。服务端读取source run，要求请求的semantic input/fingerprint与source完全相等（或直接从source构造规范input），但`verify_run_id`本身不进入simulation input hash。新Job/新run独立存在；seal时服务端比较source/new input hash、fingerprint和result hash，并insert `simulation_verifications`。只有三者完全相同才显示verified。使用相同Idempotency-Key只返回原Job，UI为实际重跑生成新key。

缺实现时只允许通过GET读取stored canonical result/fingerprint；POST、verification和Gate返回稳定unavailable。未来移除只能在新major中同时提供compatibility runner或显式迁移；迁移不得用新版本重写旧result hash或把未运行结果标为verified。

备选的“只在客户端比较hash”无法为Gate/审计提供可信关系；原地给旧run加verified字段违反insert-only，因此使用独立verification record。

### 11. Expose one OpenAPI resource model and strict simulation Gate

扩展`api/openapi.yaml`：

- `POST /api/v1/simulation-jobs`：tagged revision/release source、scene/version、parameter overlay、ordered Metric set、sample count、seed、budget、optional verify target和required `Idempotency-Key`；成功为202/Location与统一Job DTO。
- `GET /api/v1/simulation-runs/{id}`：不可变input/result/fingerprint/Metric/CI/assumption/unavailable/hash、verification refs、read-time stale/reproducibility projection。
- 复用`GET /api/v1/jobs/{id}`、`GET /api/v1/jobs/{id}/events`、`POST /api/v1/jobs/{id}/cancel`；SSE用persisted ordinal/Last-Event-ID。

历史入口不增加另一套结果协议：#7 revision timeline/Job result links列出simulation result，详情统一走run GET；`/simulations`按所选revision/Job result links加载历史。若实现#7时其timeline projection尚未暴露所需result links，先扩展该通用只读projection而不新增未在技术方案定义的simulation写动作。

同步preflight错误至少区分revision/scene/parameter invalid、FULL validation required/blocked、metric/evaluator/version unavailable、budget invalid与idempotency conflict；worker错误区分budget exceeded、timeout、numeric/evaluator contract、canceled/interrupted/recovery mismatch。全部映射RFC 9457并过滤SQL、stack、secret、path与原始未知payload。handler只做generated DTO/header/Problem Details/application adapter。

Simulation Gate descriptor固定capability/gate/contract version与required identities。evaluator读取#7 policy，逐项匹配candidate revision/config/version、required/optional scenes、sample count、seed policy、engine/evaluator/numeric/Metric/aggregation/input/result hashes、run success与reproducible state。required缺失/unavailable/stale BLOCK，optional unavailable WARNING；evidence只引用run/Metric/verification identity/hash。Gate不启动Job，也不接受override文本。

### 12. Build `/simulations` as a server-resource projection

Vue feature使用generated client与TanStack Vue Query；Pinia不保存run事实。页面拆为source/scene selector、parameter form、Metric/sample/seed/budget summary、Job progress/cancel、result/CI/assumption panel、unavailable explanation、history cards和fingerprint/verification drawer。scene parameter renderer只消费受支持schema和固定components，不下载可执行UI。

submit时客户端生成一次idempotency key并在网络重试中复用；202后订阅Job event ordinals，断线/刷新/重启后poll Job并从result_url加载run。取消、retry和verify都是明确用户动作；retry沿用原规范input但使用新key，verify还绑定source run。UI只显示服务端Gate/reproducibility/stale结论，不自行计算hash/Metric或因progress=100推断成功。

状态模型显式覆盖spec要求的loading/empty/validation blocked/metric unavailable/queued/running/canceling/canceled/interrupted/budget/timeout/retryable/non-retryable/missing evaluator/read-only/stale/verified/mismatch。Metric value、unit、CI、sample count、assumptions与unavailable reason共同展示；颜色/图标都有文本，参数error summary与Job终态使用可管理焦点和aria-live，1024px桌面布局可键盘完成主流程。

选择server resource projection而不是把engine放到浏览器，是为了避免JS numeric/PRNG版本、标签页生命周期和客户端篡改产生第二结果。

### 13. Verify pure determinism, recovery, contracts, and accessibility separately

- **Registry/golden**：四scene canonical body/hash；event comparator/time boundaries；PRNG fixed vectors；每个evaluator/Metric/aggregationmanifest；decimal/unit边界；input/result canonical bytes。
- **Engine/property**：打乱map/row/participant insertion，生成同时间/同priority/多source事件、bounded loop/stack与预算边界，断言event/sample hash不变；fuzz确保未知event/payload不panic、不执行动态代码且有界退出。
- **Parallel/Metric**：1..N worker、随机completion、chunk/retry/interrupt组合；ordinal reducer、五Metric、UNAVAILABLE、CI/assumption与result hash完全相同。
- **SQLite/fault injection**：scene/run/Metric immutable，Job/checkpoint generation，cancel各safe point，sample/aggregate/seal事务前后crash，restart/reopen，hash mismatch丢弃，one Job/one run与migrationrollback。
- **HTTP/OpenAPI/Gate**：202/Location、same/different idempotency replay、revision/release resolution、all admission errors、SSE resume/cancel、immutableGET、sanitization、required/optional/stale/missing evaluator与generatedclientdrift。
- **Vue/E2E**：选择revision/template→1000 samples→progress/result/history→same-input verify；另覆盖metric unavailable、cancel、budget、restart recovery、missing evaluatorread-only、keyboard/focus/non-color text。
- **Capacity**：固定generator在4核/16GB/NVMe/Windows10/11x64运行默认1000samples，目标<30秒，记录fingerprint/worker/memory/events；验证cancel latency与有界payload，性能失败不放宽语义。

## Risks / Trade-offs

- **[Registry/版本指纹维度多，历史兼容成本高]** → 使用一个canonical manifest和启动唯一性检查，所有run只引用该manifest；v1 major CI维护“曾写入实现”fixture，禁止无兼容方案删除。
- **[checkpoint复用可能混入旧实现结果]** → 每个sample checkpoint绑定完整input/fingerprint/ordinal与sample hash；恢复逐条验证，不一致即丢弃重算，绝不跨version归并。
- **[decimal置信区间实现比float慢]** → 每个Metric使用版本化有界accumulator和batch读取，先以1000sample/30秒fixture测量；优化worker/IO，不改变NumericPolicy或reduce order。
- **[同时间event数量导致heap/CPU爆炸]** → scene固定event/step/resource budget，engine在每次enqueue/pop检查；budget结果明确失败，不截断为成功。
- **[取消在长sample中响应不及时]** → event loop按有界间隔检查persistedcancel generation；只checkpoint完整sample，因此提高响应不污染结果。
- **[seed/PRNG实现升级造成静默漂移]** → 自有fixed-width实现、fixed-vector fixture、manifest source hash与新version策略；不暴露依赖库random type。
- **[历史scene/evaluator增多扩大二进制和DB]** → v1只保留四模板与曾写入的窄evaluator；结果和scene使用canonical blob/hash去重，移除只允许新major兼容计划。
- **[UI把UNAVAILABLE或stale误读为0/PASS]** → generated tagged DTO、独立view states、non-color labels和Gate/e2e tests；客户端不做fallback计算。

## Migration Plan

1. apply前验证#5–#7真实实现已提供RevisionReader/materialization、exact FULL ValidationGate/DSL/evaluator/NumericPolicy、VersionContributor/Gate Registry、JobStore/EventStore/SSE/cancel、migration ledger和generatedOpenAPI；任一缺失即停止，不建立临时替代。
2. 先落地simulation contract/Registry manifests、四scene与PRNG/event/Metric golden，再增加additive SQLite migration。新项目直接seed四模板；已有真实项目必须沿用#7迁移preflight，在写migration前完成不可绕过的Online Backup/integrity capability，否则停止升级；这不把#14变成simulation业务依赖。
3. 以feature-disabled状态读取已有revision并运行Registry/fixture audit；注册VersionContributor后，新revision才能固定simulation version。旧revision缺simulation identity保持unavailable，不用当前版本回填。
4. 接入engine、Metric、checkpoint/worker与fault tests，再启用POST/GET和`/simulations`；确认cancel/recovery、same-input cross-processhash与missing-evaluatorread-only后注册simulation Gate。
5. 发布v1 major时把所有可写evaluator/engine/PRNG/Metric manifests和golden嵌入`eco-guardian.exe`，并在CI比较“本major曾写入项目”的兼容清单。Gate启用不会自动运行或修改历史revision/run。
6. 回滚应用版本仅在旧应用声明支持当前DB Schema与项目引用实现时允许；不得数据库降级或删除scene/run/Metricrows。功能回滚可禁用新Job/Gate注册，同时保留已有result只读。若migration/启用失败，恢复迁移前兼容备份并保留原project.db；任何旧run/hash不原地改写。

当前没有会改变行为契约、架构或任务拆分的遗留问题。
