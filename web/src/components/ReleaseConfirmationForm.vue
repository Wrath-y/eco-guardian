<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import type { components } from '@/api/generated'
import { submitRelease, VersioningApiError } from '@/api/versions'

type Detail = components['schemas']['RevisionDetail']; type Policy = components['schemas']['ReleasePolicy']
const props = withDefaults(defineProps<{ candidate: Detail; policy: Policy; enabled: boolean; warningRequired?: boolean; allowNumericOverride?: boolean; initialNumericReason?: string; suggestedBaseline?: string }>(), { warningRequired: false, allowNumericOverride: false, initialNumericReason: '', suggestedBaseline: '' })
const emit = defineEmits<{ queued: [location: string]; 'baseline-conflict': []; 'focus-checklist': [] }>()
const baseline = ref(''); const notes = ref(''); const establish = ref(false); const acknowledgeWarning = ref(false); const overrideReason = ref(props.initialNumericReason); const overrideConfirmed = ref(false); const loading = ref(false); const error = ref(''); const baselineConflict = ref(false); const copied = ref(''); const errorSummary = ref<HTMLElement | null>(null)
const firstBaseline = computed(() => !baseline.value.trim())
const canSubmit = computed(() => props.enabled && (!firstBaseline.value || establish.value) && (!props.warningRequired || acknowledgeWarning.value) && (!props.allowNumericOverride || (Boolean(overrideReason.value.trim()) && overrideConfirmed.value)))
watch(() => props.initialNumericReason, value => { if (!overrideConfirmed.value) overrideReason.value = value })
async function submit() {
  if (!canSubmit.value) return
  loading.value = true; error.value = ''; baselineConflict.value = false; copied.value = ''
  const confirmations: components['schemas']['ReleaseConfirmation'][] = []
  if (firstBaseline.value) confirmations.push({ kind: 'establish_baseline', confirmed: true })
  if (props.warningRequired) confirmations.push({ kind: 'acknowledge_warning', confirmed: true })
  if (props.allowNumericOverride && overrideReason.value.trim() && overrideConfirmed.value) confirmations.push({ kind: 'numeric_override', confirmed: true, reason: overrideReason.value.trim() })
  try {
    const job = await submitRelease({ candidate_revision_id: props.candidate.id, config_hash: props.candidate.config_hash, version_manifest_hash: props.candidate.metadata.version_manifest.hash, policy_id: props.policy.id, expected_baseline_release_id: baseline.value.trim() || null, notes: notes.value, confirmations }, crypto.randomUUID())
    emit('queued', job.location)
  } catch (cause) {
    if (cause instanceof VersioningApiError && cause.code === 'RELEASE_BASE_CONFLICT') {
      baselineConflict.value = true; error.value = '正式版本基线已变化；候选、发布说明、确认选项和当前工作表单均已保留。'
      emit('baseline-conflict'); await nextTick(); errorSummary.value?.focus()
    } else { error.value = cause instanceof Error ? cause.message : '发布预检未通过'; emit('focus-checklist') }
  } finally { loading.value = false }
}
function refreshBaseline() { if (!props.suggestedBaseline) return; baseline.value = props.suggestedBaseline; baselineConflict.value = false; error.value = '' }
function copyInput() {
  const draft = { candidate_revision_id: props.candidate.id, expected_baseline_release_id: baseline.value.trim() || null, policy_id: props.policy.id, notes: notes.value, establish_baseline: establish.value, acknowledge_warning: acknowledgeWarning.value, numeric_override_reason: overrideReason.value, numeric_override_confirmed: overrideConfirmed.value }
  void navigator.clipboard?.writeText(JSON.stringify(draft, null, 2)); copied.value = '已复制发布输入；不会自动重放陈旧发布。'
}
</script>
<template>
  <form aria-label="发布确认" @submit.prevent="submit">
    <h2>发布确认</h2>
    <p>候选 {{ candidate.id }} · {{ candidate.config_hash }}</p><p>策略 {{ policy.id }} · {{ policy.policy_hash }}</p>
    <label>预期正式版本 ID（首次发布留空）<input v-model="baseline" aria-label="预期正式版本 ID"></label>
    <label>发布说明<textarea v-model="notes" aria-label="发布说明" maxlength="10000" /></label>
    <label v-if="firstBaseline"><input v-model="establish" type="checkbox"> 我确认建立首个正式版本基线</label>
    <label v-if="warningRequired"><input v-model="acknowledgeWarning" type="checkbox"> 我已阅读并确认所有 WARNING</label>
    <template v-if="allowNumericOverride"><label>允许的数值风险覆盖说明<textarea v-model="overrideReason" aria-label="数值风险覆盖说明" maxlength="10000" /></label><label><input v-model="overrideConfirmed" type="checkbox"> 我确认该数值风险覆盖</label></template>
    <p v-else>没有服务端声明的可覆盖数值 BLOCK；确定校验、结构、策略、能力与备份阻断均不可覆盖。</p>
    <div v-if="baselineConflict" ref="errorSummary" tabindex="-1" role="alert" aria-live="assertive">正式版本基线冲突：本地发布输入未丢失。<button type="button" :disabled="!suggestedBaseline" @click="refreshBaseline">{{ suggestedBaseline ? '刷新为当前正式版本基线' : '正在刷新当前正式版本基线…' }}</button><button type="button" @click="copyInput">复制发布输入</button><p>刷新仅替换预期基线；不会自动提交、重放陈旧发布或合并配置。</p></div>
    <p v-else-if="error" ref="errorSummary" tabindex="-1" role="alert">{{ error }}</p><button type="submit" :disabled="!canSubmit || loading">{{ loading ? '正在提交预检…' : '提交服务端发布预检' }}</button>
    <p v-if="copied" role="status">{{ copied }}</p>
  </form>
</template>
