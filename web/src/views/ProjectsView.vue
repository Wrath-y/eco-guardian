<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useProjectStore, type ActiveProject } from '@/stores/project'
const project = useProjectStore(); const loading = ref(false); const error = ref(''); const retryable = ref(false); const recent = ref<ActiveProject[]>([])
async function readProblem(response: Response, fallback: string) { const body = await response.json().catch(() => null) as { title?: string; detail?: string; retryable?: boolean } | null; retryable.value = Boolean(body?.retryable); return body?.detail || body?.title || fallback }
async function loadRecent() { const response = await fetch('/api/v1/projects/recent'); if (response.ok) recent.value = await response.json() as ActiveProject[] }
async function requestEditorClose() { return new Promise<boolean>(resolve => window.dispatchEvent(new CustomEvent('eco-guardian:request-close', { detail: { resolve } }))) }
async function closeCurrent() { const response = await fetch('/api/v1/projects/close', { method: 'POST' }); if (!response.ok) throw new Error(await readProblem(response, '无法关闭当前项目')); project.clear() }
async function choose(mode: 'create' | 'open') {
  loading.value = true; error.value = ''; retryable.value = false
  try {
    if (project.current) { if (!await requestEditorClose()) return; await closeCurrent() }
    const selection = await fetch('/api/v1/project-selections', { method: 'POST' }); if (!selection.ok) throw new Error(await readProblem(selection, '无法选择目录'))
    const { token } = await selection.json() as { token: string }
    const result = await fetch('/api/v1/projects', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ selection_token: token, mode }) }); if (!result.ok) throw new Error(await readProblem(result, '无法打开项目'))
    project.current = await result.json() as ActiveProject; await loadRecent()
  } catch (cause) { error.value = cause instanceof Error ? cause.message : '操作失败' } finally { loading.value = false }
}
async function openRecent(value: ActiveProject) {
  loading.value = true; error.value = ''; retryable.value = false
  try { if (project.current) { if (!await requestEditorClose()) return; await closeCurrent() }; const response = await fetch(`/api/v1/projects/recent/${value.id}`, { method: 'POST' }); if (!response.ok) throw new Error(await readProblem(response, '无法打开最近项目')); project.current = await response.json() as ActiveProject; await loadRecent() } catch (cause) { error.value = cause instanceof Error ? cause.message : '操作失败' } finally { loading.value = false }
}
async function close() { loading.value = true; error.value = ''; try { if (!await requestEditorClose()) return; await closeCurrent() } catch (cause) { error.value = cause instanceof Error ? cause.message : '无法关闭项目' } finally { loading.value = false } }
onMounted(async () => { await project.refresh(); await loadRecent() })
</script>
<template><section><h1>项目</h1><p v-if="project.current">当前项目：{{ project.current.name }} <button :disabled="loading" @click="close">关闭项目</button></p><p v-else>尚未打开项目。</p><button :disabled="loading" @click="choose('create')">创建项目</button><button :disabled="loading" @click="choose('open')">打开项目</button><p v-if="loading" role="status">正在处理项目…</p><div v-if="error" role="alert">{{ error }} <button v-if="retryable" @click="loadRecent">重试</button></div><section v-if="recent.length" aria-labelledby="recent-heading"><h2 id="recent-heading">最近项目</h2><ul><li v-for="value in recent" :key="value.id"><button :disabled="loading" @click="openRecent(value)">打开 {{ value.name }}</button></li></ul></section></section></template>
