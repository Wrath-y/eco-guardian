## Why

固定场景模拟能够产出可复现指标，但策划仍缺少一套把 candidate 与当前 release 基准按同场景、同 Metric 和版本化阈值进行比较，并保存风险原因与处理结论的确定性流程。需要在发布能力开放前冻结风险报告、阈值、覆盖边界和 Gate 契约，避免把无基准、不可比较、陈旧结果或未启用阈值误报为“安全”。

## What Changes

- 增加按项目、场景、Metric 与 `balance_group` 作用域版本化的阈值；提供相对变化 10% WARNING、25% BLOCK 的起始模板，首次使用必须明确启用或创建修改版，未启用时明确显示“阈值未配置”。
- 增加 candidate-vs-baseline 风险比较：绑定不可变 candidate、当前 release baseline、匹配的 simulation runs、release policy 和 threshold version；同一 `balance_group` 形成 cohort，缺少该字段时只比较同 stable ID。
- 按 Metric Module 固化的 `higher_is_risk`、`lower_is_risk` 或 `target_range` 方向计算规范绝对/相对差异与阈值边界；baseline 为 0 时只使用 Metric 的绝对阈值，没有绝对阈值则返回 `NOT_COMPARABLE`。
- 增加 `NO_BASELINE` 首发语义、必需/可选 Metric 处理、`BLOCK`/`WARNING`/`INFO` 风险项、结构性不可覆盖错误和仅限数值 BLOCK 的覆盖说明及二次确认；风险 Gate 与 #7 release policy/审计契约一致。
- 增加新乘区、重复乘算、静态公式环、无界事件与无上限叠加等结构风险汇总；复用 #6 的确定校验事实，公式环和无界规则不可覆盖，不建立第二套公式或循环判定。
- 增加不可变风险报告，保存 candidate/baseline、场景、simulation result/Metric、置信区间或缺失原因、阈值、比较状态、severity、规则、证据、假设、Graph 补充证据和覆盖状态；陈旧或身份不匹配的结果不得复用。
- 增加 `POST /api/v1/risk-reviews`、`GET /api/v1/risk-reviews/{id}`，复用统一 Job GET/SSE/cancel 契约，并增加 `/risk-reviews` 的阈值、比较、结构风险、处理与历史报告界面。
- 向 #7 Gate Registry 注册版本化 risk capability/Gate 和 VersionContributor；#9 影响报告只可作为解释证据，缺失、疑似或分数变化均不得改变确定风险等级。
- 不提供脱离场景的绝对平衡结论，不自动替用户接受风险，也不实现模拟、影响分析、发布 saga 或 AI 设计本身。

## Capabilities

### New Capabilities

- `balance-risk-assessment`: 版本化阈值、candidate-vs-baseline 指标/cohort 与结构风险比较、不可变风险报告、覆盖审计、API/Job/Gate 和风险复核界面。

### Modified Capabilities

无。

## Impact

- 当前仓库新增绿地落点 `internal/risk`，扩展 `internal/app`、`internal/storage/sqlite`、`internal/httpapi`、`api/openapi.yaml`、#7 Gate/VersionContributor 注册，以及 `web` 的 `/risk-reviews` 页面和 `/versions/:id/diff` 风险摘要入口。
- 项目数据库新增逻辑数据 `threshold_versions`、`risk_reviews`、`risk_items` 与覆盖审计字段；完成报告和阈值版本 insert-only，candidate、baseline、simulation 与影响报告仅保存精确引用和 hash，不复制配置或模拟事实。
- 硬实现依赖 #6 的 FULL `ValidationGate` 与结构安全 issue、#7 的 revision/release policy/Gate Registry/Job 契约，以及 #10 的不可变 simulation run、版本化 Metric 方向/绝对阈值/置信区间/不可用原因；#9 仅为可选只读证据依赖。
- 仓库当前没有 Go/Vue 业务源码、迁移或测试，相关 #6/#7/#9/#10 OpenSpec artifacts 也尚未 apply；实现时缺少必需边界必须停止，不能建立平行 revision、validation、simulation、Job、Gate 或影响模型。
