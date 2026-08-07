<script setup lang="ts">
import { ref } from 'vue'
import type { components } from '@/api/generated'

type Policy = components['schemas']['ReleasePolicy']; type Capability = components['schemas']['RuntimeCapabilities']['release']
const props = defineProps<{ policy: Policy; capability?: Capability }>()
const section = ref<HTMLElement | null>(null)
function reason(capabilityID: string, gateID: string) { return props.capability?.disabled_reasons.find(value => value.capability_id === capabilityID && value.gate_id === gateID) }
function state(capabilityID: string, gateID: string) { const value = reason(capabilityID, gateID); return value ? `${value.code}: ${value.detail || '不可用'}` : props.capability?.enabled ? 'PASS（服务端能力已就绪）' : 'UNAVAILABLE：等待服务端能力结果' }
defineExpose({ focus: () => section.value?.focus() })
</script>
<template>
  <section ref="section" tabindex="-1" aria-labelledby="gate-heading">
    <h2 id="gate-heading">发布 Gate 清单</h2>
    <p>策略版本 {{ policy.display_version }} · {{ policy.policy_hash }} · 阈值 {{ policy.threshold_id }}</p>
    <p>样本数 {{ policy.samples }}；阈值{{ policy.threshold_enabled ? '已启用' : '未启用' }}。</p>
    <ul aria-label="场景和指标"><li v-for="scene in policy.scenes" :key="scene.id">{{ scene.required ? '必需' : '可选' }}场景 {{ scene.id }}：<span v-for="metric in scene.metrics" :key="metric.id">{{ metric.required ? '必需' : '可选' }}指标 {{ metric.id }}；</span></li></ul>
    <ul aria-label="Gate 状态"><li v-for="gate in policy.capabilities" :key="`${gate.capability_id}:${gate.gate_id}`"><strong>{{ state(gate.capability_id, gate.gate_id) }}</strong> · capability {{ gate.capability_id }} · Gate {{ gate.gate_id }} · contract {{ gate.contract_version }}<span v-if="gate.implementation_version"> · implementation {{ gate.implementation_version }}</span><p v-if="reason(gate.capability_id, gate.gate_id)">下一步：注册或恢复此服务端 Gate，然后重新运行当前候选的完整检查。</p></li></ul>
  </section>
</template>
