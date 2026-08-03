<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import { onBeforeRouteLeave, useRoute } from 'vue-router'
import StructuredPayloadForm from '@/components/StructuredPayloadForm.vue'
import { emptyDraft, rendererMap, validateDraft, type Entity, type EntityKind, type EntitySchema, type FieldIssue } from '@/forms/registry'

const route = useRoute()
const kind = computed(() => String(route.params.kind) as EntityKind)
const id = computed(() => String(route.params.id))
const entity = ref<Record<string, unknown> | null>(null)
const schema = ref<EntitySchema | null>(null)
const error = ref(''); const issues = ref<FieldIssue[]>([]); const errorSummary = ref<HTMLElement | null>(null)
const etag = ref(''); const dirty = ref(false); const saving = ref(false); const saved = ref(''); const conflict = ref(false); const switchDialog = ref<HTMLDialogElement | null>(null); const closeResolver = ref<((value: boolean) => void) | null>(null)
const fieldId = (path: string) => `field-${path.replace(/[^a-z0-9]+/gi, '-')}`
const issuesFor = (path: string) => issues.value.filter(issue => issue.path === path)
function markDirty() { dirty.value = true; saved.value = ''; issues.value = validateDraft(entity.value ?? {}, kind.value) }

async function load() {
  error.value = ''; issues.value = []
  const schemaResponse = await fetch(`/api/v1/schemas/entities/${kind.value}`)
  if (schemaResponse.ok) schema.value = await schemaResponse.json() as EntitySchema
  if (id.value === 'new') { entity.value = emptyDraft(kind.value) as unknown as Record<string, unknown>; return }
  const r = await fetch(`/api/v1/entities/${kind.value}/${id.value}`)
  if (!r.ok) { error.value = '无法加载对象'; return }
  etag.value = r.headers.get('ETag') ?? ''; entity.value = await r.json() as Entity
}
function warn(e: BeforeUnloadEvent) { if (dirty.value) { e.preventDefault(); e.returnValue = '' } }
async function focusErrors() { await nextTick(); errorSummary.value?.focus() }
function command(creating: boolean) {
  if (creating) return entity.value
  const source = entity.value ?? {}; const out: Record<string, unknown> = {}
  for (const key of ['key', 'name', 'description', 'tag_ids', 'balance_group', 'payload', 'extensions']) if (key in source) out[key] = source[key]
  return out
}
async function save(): Promise<boolean> {
  if (!entity.value) return false
  issues.value = validateDraft(entity.value, kind.value)
  if (issues.value.length) { error.value = '请修正以下字段后再保存。'; await focusErrors(); return false }
  saving.value = true; error.value = ''; conflict.value = false; saved.value = ''
  const creating = id.value === 'new'; const url = creating ? `/api/v1/entities/${kind.value}` : `/api/v1/entities/${kind.value}/${id.value}`
  try {
    const r = await fetch(url, { method: creating ? 'POST' : 'PATCH', headers: { 'Content-Type': 'application/json', ...(creating ? {} : { 'If-Match': etag.value }) }, body: JSON.stringify(command(creating)) })
    if (r.status === 409) { conflict.value = true; return false }
    if (!r.ok) {
      const problem = await r.json().catch(() => null) as { title?: string; field_path?: string; details?: { issues?: Array<{ Path?: string; Message?: string; path?: string; message?: string }> }
      } | null
      error.value = problem?.title ?? '保存失败'
      issues.value = (problem?.details?.issues ?? []).map(issue => ({ path: issue.Path ?? issue.path ?? problem?.field_path ?? '', message: issue.Message ?? issue.message ?? '字段无效' }))
      await focusErrors(); return false
    }
    const data = await r.json() as { entity: Entity }; entity.value = data.entity; dirty.value = false; etag.value = r.headers.get('ETag') ?? etag.value; saved.value = '保存成功。已创建新的配置修订。'; return true
  } catch { error.value = '保存请求失败，请检查连接后重试。'; await focusErrors(); return false } finally { saving.value = false }
}
async function refreshServer() { await load(); dirty.value = false; conflict.value = false }
function copyInput() { void navigator.clipboard?.writeText(JSON.stringify(command(id.value === 'new'), null, 2)); saved.value = '已复制本地输入。' }
function finishCloseRequest(value: boolean) { switchDialog.value?.close(); closeResolver.value?.(value); closeResolver.value = null }
async function saveAndSwitch() { if (await save()) finishCloseRequest(true) }
function discardAndSwitch() { dirty.value = false; finishCloseRequest(true) }
async function handleCloseRequest(event: Event) {
  const request = event as CustomEvent<{ resolve: (value: boolean) => void }>
  if (!dirty.value) { request.detail.resolve(true); return }
  closeResolver.value = request.detail.resolve; switchDialog.value?.showModal()
}
onMounted(() => { void load(); window.addEventListener('beforeunload', warn); window.addEventListener('eco-guardian:request-close', handleCloseRequest) })
onBeforeUnmount(() => { window.removeEventListener('beforeunload', warn); window.removeEventListener('eco-guardian:request-close', handleCloseRequest) })
onBeforeRouteLeave(async () => {
  if (!dirty.value) return true
  return new Promise<boolean>(resolve => { closeResolver.value = resolve; switchDialog.value?.showModal() })
})
</script>
<template>
  <section v-if="entity"><RouterLink to="/projects">项目</RouterLink><h1>{{ kind }} 编辑器</h1>
    <p v-if="schema" class="schema-note">表单契约：{{ schema.schema_id }}（固定渲染器：{{ Object.keys(rendererMap).join('、') }}）</p>
    <div v-if="issues.length || error" ref="errorSummary" tabindex="-1" role="alert" aria-live="assertive" class="error-summary"><strong>保存未完成</strong><p v-if="error">{{ error }}</p><ul v-if="issues.length"><li v-for="issue in issues" :key="`${issue.path}:${issue.message}`"><a :href="`#${fieldId(issue.path)}`">{{ issue.path }}：{{ issue.message }}</a></li></ul></div>
    <div v-if="conflict" role="alert" aria-live="assertive">服务器版本已更新；本地输入已保留。<button @click="refreshServer">刷新服务器版本</button><button @click="copyInput">复制本地输入</button><button @click="conflict=false">保留输入继续编辑</button></div>
    <aside v-if="entity.extensions && Object.keys(entity.extensions as object).length" role="note">此对象包含当前版本不支持的扩展；它们将只读保留。<pre>{{ JSON.stringify(entity.extensions, null, 2) }}</pre></aside>
    <label for="field-key">Key <input id="field-key" :aria-invalid="Boolean(issuesFor('key').length)" :aria-describedby="issuesFor('key').length ? 'key-error' : undefined" :value="String(entity.key ?? '')" @input="entity.key=($event.target as HTMLInputElement).value; markDirty()"></label><span v-if="issuesFor('key').length" id="key-error">{{ issuesFor('key')[0].message }}</span>
    <label for="field-name">名称 <input id="field-name" :aria-invalid="Boolean(issuesFor('name').length)" :value="String(entity.name ?? '')" @input="entity.name=($event.target as HTMLInputElement).value; markDirty()"></label><span v-if="issuesFor('name').length">{{ issuesFor('name')[0].message }}</span>
    <label for="field-description">说明 <textarea id="field-description" :value="String(entity.description ?? '')" @input="entity.description=($event.target as HTMLTextAreaElement).value; markDirty()"></textarea></label>
    <StructuredPayloadForm :kind="kind" :model-value="entity.payload as never" @update:model-value="entity.payload=$event" @changed="markDirty" />
    <button :disabled="saving" @click="save">{{ saving ? '保存中…' : '保存' }}</button><p v-if="dirty" role="status">有未保存修改</p><p v-if="saved" role="status">{{ saved }}</p>
    <dialog ref="switchDialog" aria-labelledby="switch-title"><h2 id="switch-title">处理未保存修改</h2><p>切换或关闭项目前，请保存或放弃当前输入。</p><button :disabled="saving" @click="saveAndSwitch">保存并继续</button><button :disabled="saving" @click="discardAndSwitch">放弃修改</button><button :disabled="saving" @click="finishCloseRequest(false)">继续编辑</button></dialog>
  </section><p v-else role="status">正在加载编辑器…</p>
</template>
