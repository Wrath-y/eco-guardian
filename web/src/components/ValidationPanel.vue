<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import type { components } from '@/api/generated'

type Severity = components['schemas']['ValidationSeverity']
type ValidationIssue = components['schemas']['ValidationIssue']
type ValidationRun = components['schemas']['ValidationRun']

const props = defineProps<{ run: ValidationRun | null; loading: boolean; error: string; stale: boolean }>()
const emit = defineEmits<{ retry: []; activate: [issue: ValidationIssue] }>()
const severities: Severity[] = ['ERROR', 'BLOCK', 'WARNING', 'INFO']
const visible = ref<Set<Severity>>(new Set(severities))
const showProgress = ref(false)
const filterButtons = ref<HTMLButtonElement[]>([])
let progressTimer: number | undefined

watch(() => props.loading, loading => {
  window.clearTimeout(progressTimer)
  showProgress.value = false
  if (loading) progressTimer = window.setTimeout(() => { showProgress.value = true }, 500)
}, { immediate: true })
onBeforeUnmount(() => window.clearTimeout(progressTimer))

const displayedIssues = computed(() => props.run?.issues.filter(issue => visible.value.has(issue.severity)) ?? [])
function toggle(severity: Severity) {
  const next = new Set(visible.value)
  if (next.has(severity)) next.delete(severity)
  else next.add(severity)
  visible.value = next
}
function setFilterButton(element: unknown, index: number) {
  if (element instanceof HTMLButtonElement) filterButtons.value[index] = element
}
function moveFilterFocus(event: KeyboardEvent, index: number) {
  let next = index
  if (event.key === 'ArrowRight' || event.key === 'ArrowDown') next = (index + 1) % severities.length
  else if (event.key === 'ArrowLeft' || event.key === 'ArrowUp') next = (index + severities.length - 1) % severities.length
  else if (event.key === 'Home') next = 0
  else if (event.key === 'End') next = severities.length - 1
  else return
  event.preventDefault()
  filterButtons.value[next]?.focus()
}
function summaryLabel(severity: Severity) {
  const summary = props.run?.summary
  return `${severity} ${summary?.[severity.toLowerCase() as keyof typeof summary] ?? 0}`
}
</script>

<template>
  <section aria-labelledby="validation-title" class="validation-panel">
    <h2 id="validation-title">校验结果</h2>
    <p v-if="loading && showProgress" role="status">校验仍在进行中…</p>
    <div v-else-if="loading" aria-live="polite">正在启动校验…</div>
    <div v-else-if="error" role="alert">{{ error }} <button type="button" @click="emit('retry')">重试</button></div>
    <p v-else-if="!run">尚未运行校验。</p>
    <template v-else>
      <p v-if="stale" role="status">结果已过期：当前表单或工作状态已改变，请重新校验。</p>
      <p v-else-if="run.scope === 'FULL' && run.summary.error === 0 && run.summary.block === 0" role="status">当前 FULL 校验通过。</p>
      <p>完整摘要：<span v-for="severity in severities" :key="severity">{{ summaryLabel(severity) }} </span></p>
      <div role="group" aria-label="按严重级别筛选">
        <button v-for="(severity, index) in severities" :key="severity" :ref="element => setFilterButton(element, index)" type="button" :aria-label="`显示或隐藏 ${severity} 问题`" :aria-pressed="visible.has(severity)" @keydown="moveFilterFocus($event, index)" @click="toggle(severity)">{{ severity }}</button>
      </div>
      <p v-if="displayedIssues.length === 0">没有符合当前筛选条件的问题。</p>
      <ol v-else>
        <li v-for="issue in displayedIssues" :key="issue.fingerprint">
          <button type="button" :aria-label="`定位 ${issue.severity} 问题 ${issue.code}`" @click="emit('activate', issue)"><strong>{{ issue.severity }}</strong> {{ issue.code }}：{{ issue.message }}</button>
          <p v-if="issue.fix_hint">修复提示：{{ issue.fix_hint }}</p>
          <details v-if="issue.evidence && Object.keys(issue.evidence).length"><summary>证据</summary><pre>{{ JSON.stringify(issue.evidence, null, 2) }}</pre></details>
        </li>
      </ol>
    </template>
  </section>
</template>
