<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import type { components } from '@/api/generated'
import { useRuntimeStore } from '@/stores/runtime'

type Settings = components['schemas']['SettingsResource']
type Patch = components['schemas']['PatchSettingsRequest']
type Result = components['schemas']['SettingsUpdateResult']
type Problem = Partial<components['schemas']['Problem']>

const runtime = useRuntimeStore()
const settings = ref<Settings>()
const form = reactive({
  browser: { auto_open: false },
  graph: { mode: 'disabled' as Settings['graph']['mode'], endpoint: '', health_timeout_seconds: 5, startup_timeout_seconds: 20, restart_limit: 3 },
  ai: { enabled: false, endpoint: '', model: '', request_timeout_seconds: 120, allow_cloud: false },
  logs: { max_bytes: 5 << 20, max_files: 5 },
  backup: { retention_days: 30 },
} satisfies Patch)
const loading = ref(false)
const saving = ref(false)
const message = ref('')
const error = ref('')

async function load() {
  loading.value = true; error.value = ''
  try {
    const response = await fetch('/api/v1/settings')
    if (!response.ok) throw new Error('无法读取运行设置')
    const value = await response.json() as Settings
    settings.value = value
    Object.assign(form, {
      browser: { ...value.browser }, graph: { ...value.graph },
      ai: { enabled: value.ai.enabled, endpoint: value.ai.endpoint, model: value.ai.model, request_timeout_seconds: value.ai.request_timeout_seconds, allow_cloud: value.ai.allow_cloud },
      logs: { ...value.logs }, backup: { ...value.backup },
    })
  } catch (cause) { error.value = cause instanceof Error ? cause.message : '无法读取运行设置' } finally { loading.value = false }
}

async function save() {
  saving.value = true; error.value = ''; message.value = ''
  try {
    const response = await fetch('/api/v1/settings', { method: 'PATCH', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(form) })
    if (!response.ok) {
      const problem = await response.json().catch(() => null) as Problem | null
      throw new Error(problem?.field_path ? `${problem.field_path}：${problem.title ?? '设置无效'}` : problem?.title || '无法保存运行设置')
    }
    const result = await response.json() as Result
    settings.value = result.settings
    const groups = new Map<string, string[]>()
    for (const effect of result.effects) groups.set(effect.disposition, [...(groups.get(effect.disposition) ?? []), effect.field])
    message.value = [...groups].map(([kind, fields]) => `${kind}：${fields.join('、')}`).join('；') || '设置已保存。'
    await runtime.refresh()
  } catch (cause) { error.value = cause instanceof Error ? cause.message : '无法保存运行设置' } finally { saving.value = false }
}

onMounted(load)
</script>

<template>
  <section aria-labelledby="settings-heading">
    <h1 id="settings-heading">运行设置</h1>
    <p>这里仅保存本机非敏感设置；凭据使用系统凭据管理器，项目事实仍保存在所选项目中。</p>
    <p v-if="loading" role="status">正在读取设置…</p>
    <p v-if="error" role="alert">{{ error }}</p>
    <p v-if="message" role="status" tabindex="-1">{{ message }}</p>
    <form v-if="settings" class="settings-form" @submit.prevent="save">
      <fieldset><legend>启动与浏览器</legend><label><input v-model="form.browser.auto_open" type="checkbox"> 服务可达后自动打开默认浏览器</label></fieldset>
      <fieldset>
        <legend>Graph / local-rag</legend>
        <label>模式 <select v-model="form.graph.mode"><option value="disabled">disabled</option><option value="external">external</option><option value="bundled">bundled</option></select></label>
        <label>Loopback endpoint <input v-model="form.graph.endpoint" type="url" placeholder="http://127.0.0.1:8080"></label>
        <label>健康超时（秒） <input v-model.number="form.graph.health_timeout_seconds" type="number" min="1" max="60"></label>
        <label>启动超时（秒） <input v-model.number="form.graph.startup_timeout_seconds" type="number" min="1" max="300"></label>
        <label>重启上限 <input v-model.number="form.graph.restart_limit" type="number" min="0" max="10"></label>
      </fieldset>
      <fieldset>
        <legend>AI Provider（非敏感）</legend>
        <label><input v-model="form.ai.enabled" type="checkbox"> 启用 AI 设计</label>
        <label>Endpoint <input v-model="form.ai.endpoint" type="url"></label>
        <label>Model <input v-model="form.ai.model" autocomplete="off"></label>
        <label>请求超时（秒） <input v-model.number="form.ai.request_timeout_seconds" type="number" min="1" max="600"></label>
        <label><input v-model="form.ai.allow_cloud" type="checkbox"> 明确允许 HTTPS cloud endpoint</label>
        <p>凭据状态：{{ settings.ai.credential_present ? '已配置' : '未配置' }}。<RouterLink to="/ai-design#provider-heading">前往只写凭据控件</RouterLink></p>
      </fieldset>
      <fieldset>
        <legend>诊断日志</legend>
        <label>单文件上限（bytes） <input v-model.number="form.logs.max_bytes" type="number" min="65536" max="1073741824"></label>
        <label>保留文件数 <input v-model.number="form.logs.max_files" type="number" min="1" max="20"></label>
        <p>日志位置：<code>{{ runtime.status?.log_location ?? '等待运行状态' }}</code></p>
      </fieldset>
      <button type="submit" :disabled="saving">{{ saving ? '正在保存…' : '保存设置' }}</button>
    </form>
  </section>
</template>

<style scoped>
.settings-form { display: grid; gap: 1rem; max-width: 52rem; }
fieldset { display: grid; gap: .75rem; border: 1px solid #aab5c0; }
label { display: grid; gap: .25rem; }
input, select { min-height: 2.25rem; }
code { overflow-wrap: anywhere; }
</style>
