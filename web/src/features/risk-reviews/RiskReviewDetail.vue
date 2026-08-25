<script setup lang="ts">
import { computed, ref } from 'vue'
import { RouterLink } from 'vue-router'
import type { RiskReview } from './api'

const props = defineProps<{ review: RiskReview }>()
const emit = defineEmits<{ decision: [items: { item_id: string; item_hash: string }[], reason: string] }>()
const reason = ref('')
const eligible = computed(() => props.review.items.filter(item => item.overridable && item.severity === 'BLOCK').map(item => ({ item_id: item.id, item_hash: item.item_hash })))
const gateText = computed(() => `${props.review.read_time.gate_state}${props.review.read_time.freshness === 'FRESH' ? '' : ` · ${props.review.read_time.freshness}`}`)
function value(value: string | null | undefined) { return value ?? '不适用（未提供）' }
function range(item: RiskReview['items'][number]) {
  const metric = item.metric_evidence
  if (!metric) return '不适用'
  const parts = [`relative warning/block 10%/25% 由固定 threshold version 提供`]
  if (metric.absolute_threshold) parts.push(`absolute ${metric.absolute_threshold} ${metric.unit}`)
  if (metric.target_range) parts.push(`[${metric.target_range.lower}, ${metric.target_range.upper}] ${metric.target_range.bounds}`)
  return parts.join(' · ')
}
function submitDecision() { if (eligible.value.length && reason.value.trim()) emit('decision', eligible.value, reason.value.trim()) }
</script>

<template>
  <article class="risk-review" aria-labelledby="risk-review-result-heading">
    <header class="risk-card">
      <h2 id="risk-review-result-heading" tabindex="-1">不可变风险报告</h2>
      <p>报告 {{ review.id }} · {{ review.report_kind }} · 创建于 {{ review.created_at }}</p>
      <p aria-live="polite">当前投影：<strong>{{ gateText }}</strong><span v-if="review.read_time.freshness_reasons.length"> · {{ review.read_time.freshness_reasons.join('；') }}</span></p>
      <p>候选 {{ review.candidate.revision_id }} · baseline：{{ review.baseline.type }}<template v-if="review.baseline.type === 'BASELINE'"> / {{ review.baseline.release_id }}</template></p>
      <p>calculation hash：<code>{{ review.calculation_hash }}</code> · report hash：<code>{{ review.report_hash }}</code></p>
      <RouterLink :to="{ path: `/versions/${review.candidate.revision_id}/diff`, query: { risk_review: review.id } }">转到版本差异与独立发布确认</RouterLink>
      <a v-if="review.read_time.release_audit_url" :href="review.read_time.release_audit_url">查看发布审计</a>
    </header>

    <section class="risk-card" aria-labelledby="threshold-heading">
      <h3 id="threshold-heading">固定 threshold version</h3>
      <p>{{ review.threshold.enabled ? '已启用' : '未启用，不能标记安全' }} · 来源 {{ review.threshold.source }} · {{ review.threshold.identity.id }}@{{ review.threshold.identity.version }}</p>
      <p>body hash：<code>{{ review.threshold.body_hash }}</code></p>
      <ul><li v-for="entry in review.threshold.entries" :key="`${entry.scene_id}:${entry.metric_id}:${entry.balance_group}`">{{ entry.scene_id }}@{{ entry.scene_version }} / {{ entry.metric_id }}@{{ entry.metric_version }} · {{ entry.direction }} · {{ entry.unit }} · relative warning {{ entry.relative_warning }} / block {{ entry.relative_block }}<template v-if="entry.absolute_warning"> · absolute warning {{ entry.absolute_warning }} / block {{ entry.absolute_block }}</template><template v-if="entry.metric_id === 'metric-resource'"> · 目标区间 [0.8, 1.2] inclusive</template></li></ul>
      <p v-if="review.threshold.assumptions.length">假设：{{ review.threshold.assumptions.join('；') }}</p>
    </section>

    <section class="risk-card" aria-labelledby="comparison-heading">
      <h3 id="comparison-heading">比较结果</h3>
      <p v-if="!review.items.length" role="status">NO_BASELINE 首发报告没有伪造差异项；仍需全部 required 事实和建立基线确认。</p>
      <div class="risk-table-wrap" v-else tabindex="0" aria-label="可横向滚动的风险比较表">
        <table><caption>状态与严重度分列；缺失值不会显示为 0</caption><thead><tr><th>Scene / Metric</th><th>角色</th><th>比较状态</th><th>严重度</th><th>Candidate</th><th>Baseline</th><th>Signed / risk / relative delta</th><th>Boundary / direction</th><th>Cohort / assumptions</th></tr></thead>
          <tbody><tr v-for="item in review.items" :key="item.id"><td>{{ item.metric_evidence ? `${item.metric_evidence.scene_id}@${item.metric_evidence.scene_version} / ${item.metric_evidence.metric_id}@${item.metric_evidence.metric_version}` : item.rule.id }}</td><td>{{ item.role }}</td><td>{{ item.comparison_status }}<span v-if="item.reason"> · {{ item.reason }}</span></td><td>{{ item.severity ?? '无（不可比较）' }}</td><td><template v-if="item.metric_evidence">{{ value(item.metric_evidence.candidate.value) }} {{ item.metric_evidence.unit }} · CI {{ value(item.metric_evidence.candidate.confidence_low) }}–{{ value(item.metric_evidence.candidate.confidence_high) }}<span v-if="item.metric_evidence.candidate.unavailable"> · {{ item.metric_evidence.candidate.unavailable.code }}：{{ item.metric_evidence.candidate.unavailable.message }}</span></template><template v-else>结构证据</template></td><td><template v-if="item.metric_evidence?.baseline">{{ value(item.metric_evidence.baseline.value) }} {{ item.metric_evidence.unit }} · CI {{ value(item.metric_evidence.baseline.confidence_low) }}–{{ value(item.metric_evidence.baseline.confidence_high) }}</template><template v-else>NO_BASELINE / 不适用</template></td><td>{{ item.metric_evidence ? `${value(item.metric_evidence.signed_delta)} / ${value(item.metric_evidence.risk_delta)} / ${value(item.metric_evidence.relative_risk)}` : '不适用' }}</td><td>{{ range(item) }}<template v-if="item.metric_evidence"> · {{ item.metric_evidence.direction }}</template></td><td><template v-if="item.metric_evidence">{{ item.metric_evidence.subject.type }} · {{ item.metric_evidence.subject.balance_group || item.metric_evidence.subject.stable_id }} · {{ item.metric_evidence.assumptions.join('；') || '无额外假设' }}</template><template v-else>结构规则</template></td></tr></tbody>
        </table>
      </div>
    </section>

    <section class="risk-card" aria-labelledby="evidence-heading">
      <h3 id="evidence-heading">Cohort 与结构证据</h3>
      <details v-for="item in review.items" :key="`evidence:${item.id}`"><summary>{{ item.id }} · 展开证据</summary>
        <template v-if="item.metric_evidence"><p>{{ item.metric_evidence.subject.type }} / {{ item.metric_evidence.subject.entity_kind }} / {{ item.metric_evidence.subject.balance_group || item.metric_evidence.subject.stable_id }}</p><ol><li v-for="member in item.metric_evidence.subject.members" :key="member">{{ member }}</li></ol><p>candidate run {{ item.metric_evidence.candidate.run_id }} / {{ item.metric_evidence.candidate.result_hash }}</p><p v-if="item.metric_evidence.baseline">baseline run {{ item.metric_evidence.baseline.run_id }} / {{ item.metric_evidence.baseline.result_hash }}</p></template>
        <template v-else-if="item.structural_evidence"><p>{{ item.structural_evidence.type }} · fingerprint {{ item.structural_evidence.fingerprint }}</p><RouterLink :to="{ path: `/config/character/${item.structural_evidence.entity_id}`, query: { field_path: item.structural_evidence.field_path } }">打开 #6 issue 对应字段 {{ item.structural_evidence.field_path }}</RouterLink></template>
      </details>
      <p v-if="!review.read_time.impact_evidence_refs.length">Graph/影响证据不可用；这是解释层降级，不改变上方 severity 或 Gate。</p>
      <ul v-else aria-label="解释性 Graph 证据"><li v-for="identity in review.read_time.impact_evidence_refs" :key="identity.id">Graph/影响证据（确定或疑似分类由来源报告给出）：{{ identity.id }}@{{ identity.version }} · {{ identity.hash }}。仅用于解释，不改变 severity。</li></ul>
    </section>

    <section v-if="eligible.length" class="risk-card" aria-labelledby="decision-heading">
      <h3 id="decision-heading">数值 BLOCK 说明</h3>
      <p>原始 Gate 仍为 BLOCK；这里仅创建新的不可变 decision report。发布时仍需第二次独立确认。</p>
      <form @submit.prevent="submitDecision"><label>说明 <textarea v-model="reason" required maxlength="2000" aria-describedby="decision-help"></textarea></label><small id="decision-help">适用项：{{ eligible.map(item => item.item_id).join('、') }}</small><button type="submit" :disabled="!reason.trim()">创建 immutable decision report</button></form>
    </section>
    <p v-else-if="review.items.some(item => item.severity === 'BLOCK')">当前 BLOCK 属于不可覆盖结构/验证类别，未显示数值说明控件。</p>
  </article>
</template>
