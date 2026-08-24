## Why

策划需要把不可变配置 revision 放入少量固定场景中，得到能够跨重试、重启和并行执行复核的指标；当前仓库只有配置、校验与版本工作流的规划契约，尚无模拟能力。该 change 用受限、确定性的模拟器补齐这条离线分析链，而不把产品扩张为通用战斗引擎。

## What Changes

- 增加四个内置、可克隆、版本化的场景模板：30 秒单目标、180 秒单目标、60 秒三目标、60 秒极限叠层；场景显式固定参与对象、动作序列、初始状态、允许参数、seed 与资源预算。
- 增加绑定不可变 config revision 的确定性离散事件模拟：复用 #6 的版本化 DSL/evaluator 与 decimal128 数值策略，使用引擎自有版本化 PRNG，并按时间、规则优先级、来源 ID、插入序号稳定调度事件。
- 增加样本级并行执行与稳定归并，默认随机场景 1000 样本；同一规范输入产生相同 input/result hash，不受 worker 数、完成顺序或进程重启影响。
- 增加独立、版本化的 DPS、治疗、生存、资源和控制 Metric Module，输出指标、置信区间与假设；缺少结构化输入时返回明确不可用原因，不以零值或估算值替代。
- 增加持久化、幂等、可取消和可恢复的 simulation Job，以及不可变历史结果；预算超限、取消与进程中断使用稳定状态，确定性任务可按原指纹安全重跑。
- 增加 `POST /api/v1/simulation-jobs`、`GET /api/v1/simulation-runs/{id}`，并复用统一 Job GET/SSE/cancel；结果回显 revision、场景、引擎、数值策略、evaluator/Metric、样本数、seed、input/result hash 和版本指纹。
- 增加 `/simulations` 页面，覆盖版本/场景选择、参数校验、进度、取消、超时、失败重试、指标不可用、历史结果、stale 标记和复现验证。
- 向 #7 Gate Registry 注册版本化 simulation Gate；缺少项目引用的 evaluator 时只读保留已存结果，但禁止声称已复现或用于新发布。
- 不增加通用战斗引擎、任意脚本事件、动态插件、未建模机制猜测或 Graph/AI 依赖。

## Capabilities

### New Capabilities

- `reproducible-simulation`: 固定场景、确定性事件/PRNG/数值执行、稳定样本归并、版本化 Metric、可恢复 Job、不可变结果、API/Gate 与模拟界面。

### Modified Capabilities

无。

## Impact

- 当前仓库新增绿地落点 `internal/simulation`，扩展 `internal/app`、`internal/storage/sqlite`、`internal/httpapi`、`api/openapi.yaml`、#7 Gate/VersionContributor 注册，以及 `web` 的 `/simulations` 页面和固定 fixtures。
- 项目数据库新增逻辑数据 `scenario_definitions`、`simulation_runs`、Metric 结果与复现元数据，复用 #7 的 `jobs`/`job_events`；配置 revision 和已完成模拟结果保持不可变。
- 硬依赖 #5 的 revision materialization、#6 的精确 FULL `ValidationGate`/DSL/NumericPolicy/evaluator 边界和 #7 的 VersionManifest、Gate Registry、统一 Job/OpenAPI；#8 Graph 与 #9 影响分析不是硬依赖。
- 当前仓库没有 Go/Vue 业务源码、迁移或测试；apply 时若 #5–#7 的上述边界尚未实现，必须停止而不是建立平行模型。
