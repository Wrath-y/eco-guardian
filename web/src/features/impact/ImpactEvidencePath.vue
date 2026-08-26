<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import cytoscape, { type Core } from 'cytoscape'
import type { ImpactPath } from './api'

const props = defineProps<{ path?: ImpactPath }>()
const graph = ref<HTMLElement>()
const selectedEdge = ref(0)
let instance: Core | undefined
const edge = computed(() => props.path?.edges[selectedEdge.value])

function render() {
  instance?.destroy(); instance = undefined
  if (!graph.value || !props.path) return
  instance = cytoscape({ container: graph.value, elements: [...props.path.nodes.map(node => ({ data: { id: node.id, label: node.label || node.type } })), ...props.path.edges.map(item => ({ data: { id: item.id, source: item.from, target: item.to, label: item.type } }))], layout: { name: 'breadthfirst', directed: true, padding: 16 }, style: [{ selector: 'node', style: { 'background-color': '#165d86', color: '#111827', label: 'data(label)', 'text-valign': 'bottom', 'text-margin-y': 6, 'font-size': 12 } }, { selector: 'edge', style: { width: 2, 'line-color': '#596579', 'target-arrow-color': '#596579', 'target-arrow-shape': 'triangle', label: 'data(label)', 'font-size': 10, 'text-background-color': '#fff', 'text-background-opacity': 1 } }] })
}
function move(delta: number) { if (!props.path?.edges.length) return; selectedEdge.value = (selectedEdge.value + delta + props.path.edges.length) % props.path.edges.length; void nextTick(() => document.getElementById(`impact-edge-${selectedEdge.value}`)?.focus()) }
watch(() => props.path, () => { selectedEdge.value = 0; void nextTick(render) }, { deep: true })
onMounted(render)
onBeforeUnmount(() => instance?.destroy())
</script>

<template>
  <section v-if="path" aria-labelledby="evidence-path-title">
    <h3 id="evidence-path-title">证据路径（{{ path.hop_count }} 跳）</h3>
    <p>图与下方有序文本使用同一组已保存 Node/Edge；箭头始终表示存储方向。</p>
    <div ref="graph" class="graph" role="img" :aria-label="`${path.source_node_id} 到 ${path.target_node_id} 的 ${path.hop_count} 跳证据图`" />
    <ol class="path-list" aria-label="信息等价的有序文本路径">
      <li v-for="(node, index) in path.nodes" :key="node.id">
        <strong>{{ node.label || node.id }}</strong> <span>（{{ node.type }}，Node {{ node.id }}）</span>
        <button v-if="path.edges[index]" :id="`impact-edge-${index}`" type="button" :aria-current="selectedEdge === index ? 'true' : undefined" @click="selectedEdge = index" @keydown.left.prevent="move(-1)" @keydown.right.prevent="move(1)">Edge {{ path.edges[index].type }}：{{ path.edges[index].from }} → {{ path.edges[index].to }}</button>
      </li>
    </ol>
    <dl v-if="edge" class="edge-detail"><dt>关系</dt><dd>{{ edge.type }} / {{ edge.relation_kind }}</dd><dt>置信度</dt><dd>{{ edge.confidence }}</dd><dt>字段与 provenance</dt><dd><pre>{{ JSON.stringify(edge.provenance, null, 2) }}</pre></dd><dt>属性</dt><dd><pre>{{ JSON.stringify(edge.properties, null, 2) }}</pre></dd></dl>
    <p v-if="path.truncated" role="status">此路径集合已截断：{{ path.truncation_reasons.join('、') }}。缩小筛选或调整有界限制后重新分析。</p>
  </section>
  <p v-else role="status">该对象没有可用路径；这不等同于“没有影响”。</p>
</template>

<style scoped>
.graph { min-height: 280px; border: 1px solid #7a8597; border-radius: .5rem; background: #f8fafc; }
.path-list li { margin: .75rem 0; }
.path-list button { display: block; margin-top: .4rem; }
.edge-detail { display: grid; grid-template-columns: 9rem minmax(0, 1fr); gap: .4rem .8rem; }
.edge-detail dt { font-weight: 700; }
pre { white-space: pre-wrap; overflow-wrap: anywhere; }
</style>
