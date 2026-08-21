<script setup lang="ts">
import { computed } from 'vue'
import { useGraphStatus } from '@/api/graph'

const props = defineProps<{ projectID: string; revisionID: string; compact?: boolean }>()
const query = useGraphStatus(() => props.projectID, () => props.revisionID)
const statusText = computed(() => ({ saved: '已保存', validating: '正在校验', blocked_validation: '校验已阻断', graph_queued: '图谱已排队', graph_building: '图谱构建中', graph_ready: '图谱已就绪', graph_failed: '图谱失败' } as Record<string, string>)[query.data.value?.pipeline_state ?? ''] ?? '图谱状态未知')
const components = computed(() => query.data.value?.provider?.components ?? [])
</script>
<template>
  <section class="graph-status" :aria-label="`revision ${revisionID} 的图谱状态`">
    <h3 v-if="!compact">图谱状态</h3>
    <p v-if="query.isPending.value" role="status">正在加载图谱状态…</p>
    <p v-else-if="query.isError.value" role="alert">无法加载图谱状态：{{ query.error.value?.message }} <button type="button" @click="query.refetch()">重试</button></p>
    <template v-else-if="query.data.value">
      <p><strong>{{ statusText }}</strong> · 新鲜度：{{ query.data.value.freshness ?? 'unknown' }}<span v-if="query.data.value.freshness_reasons?.length">（{{ query.data.value.freshness_reasons.join('、') }}）</span></p>
      <template v-if="!compact"><p v-if="query.data.value.projection">projector {{ query.data.value.projection.projection_schema_version }}/{{ query.data.value.projection.projector_version }} · Nodes {{ query.data.value.projection.node_count }} · Edges {{ query.data.value.projection.edge_count }} · <code>{{ query.data.value.projection.graph_manifest_hash }}</code></p><p v-else>尚无可用投影摘要。</p><p v-if="query.data.value.job">Job {{ query.data.value.job.status }} · <a :href="query.data.value.job.events_url">事件与进度</a></p><p v-if="query.data.value.provider">Graph/FTS/Vector：<span v-for="component in components" :key="component.name">{{ component.name }}={{ component.state }}；</span>观测于 {{ query.data.value.provider.observed_at }}</p></template>
      <ul v-if="query.data.value.warnings?.length" aria-label="图谱警告"><li v-for="warning in query.data.value.warnings" :key="warning.code">WARNING：{{ warning.code }}</li></ul>
      <p v-if="query.data.value.impact_state === 'queued'">影响分析已排队，等待下游模块处理。</p>
      <p v-if="query.data.value.error" role="alert">{{ query.data.value.error.code }}<span v-if="query.data.value.error.request_id"> · request {{ query.data.value.error.request_id }}</span></p>
      <p v-if="query.data.value.actions?.includes('retry')">可在详情中使用显式重试；业务配置编辑不受图谱失败影响。</p>
    </template>
    <p v-else>该 revision 尚无图谱状态。</p>
  </section>
</template>
