<script setup lang="ts">
import { reactive, ref, watch } from 'vue'
import { useQueryClient } from '@tanstack/vue-query'
import { aiKeys, clearProviderCredential, patchAISettings, setProviderCredential, useAICapability, useAISettings } from './api'

const props = defineProps<{ projectID: string }>()
const client = useQueryClient()
const settings = useAISettings(() => props.projectID)
const capability = useAICapability(() => props.projectID)
const form = reactive({ enabled: false, endpoint: '', model: '', request_timeout_seconds: 60, allow_cloud: false })
const credential = ref('')
const busy = ref<'settings' | 'credential' | 'clear' | 'test' | ''>('')
const message = ref('')
const error = ref('')

watch(() => settings.data.value?.ai, value => {
  if (!value) return
  Object.assign(form, { enabled: value.enabled, endpoint: value.endpoint, model: value.model, request_timeout_seconds: value.request_timeout_seconds, allow_cloud: value.allow_cloud })
}, { immediate: true })

async function saveSettings() {
  busy.value = 'settings'; error.value = ''; message.value = ''
  try {
    const value = await patchAISettings({ ai: { ...form } })
    client.setQueryData(aiKeys.settings(props.projectID), value.settings)
    const reconnect = value.effects.some(effect => effect.disposition === 'reconnect_required')
    message.value = reconnect ? '非敏感 Provider 设置已保存；请重新连接并测试能力。' : '非敏感 Provider 设置已保存。请测试结构化输出、工具调用与流式能力。'
    await capability.refetch()
  } catch (cause) { error.value = cause instanceof Error ? cause.message : '无法保存 Provider 设置' } finally { busy.value = '' }
}
async function saveCredential() {
	if (!credential.value) return
	const submitted = credential.value
	credential.value = ''
	busy.value = 'credential'; error.value = ''; message.value = ''
  try {
    await setProviderCredential(submitted)
    message.value = '凭据已写入系统凭据管理器；页面不保留其内容。'
    await Promise.all([settings.refetch(), capability.refetch()])
  } catch (cause) { error.value = cause instanceof Error ? cause.message : '无法写入 Provider 凭据' } finally {
    credential.value = ''
    busy.value = ''
  }
}
async function removeCredential() {
  busy.value = 'clear'; error.value = ''; message.value = ''
  try { await clearProviderCredential(); message.value = '持久化凭据已清除；若配置了环境变量，运行时可能回退到环境凭据。'; await Promise.all([settings.refetch(), capability.refetch()]) }
  catch (cause) { error.value = cause instanceof Error ? cause.message : '无法清除 Provider 凭据' } finally { busy.value = '' }
}
async function testCapability() {
  busy.value = 'test'; error.value = ''; message.value = ''
  const result = await capability.refetch()
  if (result.error) error.value = result.error.message
  else message.value = `能力测试完成：${result.data?.state ?? 'unavailable'}。`
  busy.value = ''
}
</script>

<template>
  <section class="ai-card" aria-labelledby="provider-heading">
    <h2 id="provider-heading">Provider 设置与就绪状态</h2>
    <p v-if="settings.isPending.value || capability.isPending.value" role="status">正在读取非敏感设置与能力…</p>
    <p v-if="error" role="alert">{{ error }}</p>
    <p v-if="message" role="status">{{ message }}</p>
    <template v-if="settings.data.value">
      <form class="form-grid" aria-label="Provider 非敏感设置" @submit.prevent="saveSettings">
        <label><input v-model="form.enabled" type="checkbox"> 启用 AI 设计</label>
        <label>OpenAI-compatible endpoint <input v-model="form.endpoint" type="url" autocomplete="url" placeholder="http://127.0.0.1:11434/v1"></label>
        <label>Model <input v-model="form.model" autocomplete="off"></label>
        <label>请求超时（秒） <input v-model.number="form.request_timeout_seconds" type="number" min="1" max="600"></label>
        <label><input v-model="form.allow_cloud" type="checkbox"> 明确允许 cloud endpoint</label>
        <aside v-if="form.allow_cloud" class="cloud-warning" role="note"><strong>Cloud 数据披露：</strong>冻结目标、目标/约束及有界证据 citation 会发送到配置的 HTTPS Provider；凭据、完整项目数据库和隐藏推理不会发送。</aside>
        <button type="submit" :disabled="Boolean(busy)">{{ busy === 'settings' ? '正在保存…' : '保存非敏感设置' }}</button>
      </form>
      <form class="credential-row" aria-label="写入 Provider 凭据" @submit.prevent="saveCredential">
        <label>新凭据（仅写入） <input v-model="credential" type="password" autocomplete="new-password"></label>
        <button type="submit" :disabled="Boolean(busy) || !credential">{{ busy === 'credential' ? '正在写入…' : '设置/替换凭据' }}</button>
        <button type="button" :disabled="Boolean(busy) || !settings.data.value.ai.credential_present" @click="removeCredential">{{ busy === 'clear' ? '正在清除…' : '清除持久化凭据' }}</button>
      </form>
      <p>endpoint 分类：{{ settings.data.value.ai.endpoint_classification ?? '未配置' }}；凭据：{{ settings.data.value.ai.credential_present ? '已存在' : '不存在' }}。</p>
    </template>
    <section v-if="capability.data.value" aria-label="Provider 能力结果">
      <p>状态：<strong>{{ capability.data.value.state }}</strong>；structured output {{ capability.data.value.structured_output ? '支持' : '不支持' }}；tools {{ capability.data.value.tool_calls ? '支持' : '不支持' }}；streaming {{ capability.data.value.streaming ? '支持' : '不支持' }}。</p>
      <ul v-if="capability.data.value.reasons.length"><li v-for="reason in capability.data.value.reasons" :key="reason">{{ reason }}</li></ul>
      <p>服务端固定预算：修复 {{ capability.data.value.limits.max_format_repairs }} 轮，Provider turns {{ capability.data.value.limits.max_provider_turns }}，tools {{ capability.data.value.limits.max_tool_calls }}，search candidates {{ capability.data.value.limits.max_search_candidates }}，总时长 {{ capability.data.value.limits.max_duration_millis }}ms。</p>
    </section>
    <button type="button" :disabled="Boolean(busy)" @click="testCapability">{{ busy === 'test' ? '正在测试…' : '测试 Provider 能力' }}</button>
  </section>
</template>
