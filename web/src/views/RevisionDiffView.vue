<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { useProjectStore } from '@/stores/project'
import { usePolicyHistory, useReleaseHistory, useRevisionDetail, useRevisionDiff, useRuntimeCapabilities } from '@/api/versions'
import ReleaseGateChecklist from '@/components/ReleaseGateChecklist.vue'
import ReleaseConfirmationForm from '@/components/ReleaseConfirmationForm.vue'
import ReleaseJobProgress from '@/components/ReleaseJobProgress.vue'
import GraphStatusPanel from '@/components/GraphStatusPanel.vue'
import GraphRuntimeBanner from '@/components/GraphRuntimeBanner.vue'

const route = useRoute(); const router = useRouter(); const project = useProjectStore()
const projectID = computed(() => project.current?.id ?? '')
const targetID = computed(() => String(route.params.id ?? ''))
const baseID = computed(() => typeof route.query.base === 'string' ? route.query.base : '')
const baseDraft = ref(baseID.value); watch(baseID, value => { baseDraft.value = value })
const detail = useRevisionDetail(() => projectID.value, () => targetID.value)
const diff = useRevisionDiff(() => projectID.value, () => baseID.value, () => targetID.value)
const policies = usePolicyHistory(() => projectID.value); const capabilities = useRuntimeCapabilities(() => projectID.value)
const releases = useReleaseHistory(() => projectID.value)
const policy = computed(() => policies.data.value?.items?.[0])
const suggestedBaseline = computed(() => releases.data.value?.items?.[0]?.id ?? '')
const gateChecklist = ref<{ focus: () => void } | null>(null)
const errorSummary = ref<HTMLElement | null>(null)
const queuedLocation = ref('')
const activeReleaseID = ref('')
const queuedJobID = computed(() => queuedLocation.value.match(/\/jobs\/([^/?#]+)/)?.[1] ?? '')
function compare() { void router.replace({ query: baseDraft.value.trim() ? { ...route.query, base: baseDraft.value.trim() } : { ...route.query, base: undefined } }) }
function formatValue(value: unknown) { return value === undefined ? '—' : typeof value === 'string' ? value : JSON.stringify(value, null, 2) }
function ordinal(change: { old_ordinal?: number; new_ordinal?: number }) { return change.old_ordinal === undefined && change.new_ordinal === undefined ? '' : `位置 ${change.old_ordinal ?? '—'} → ${change.new_ordinal ?? '—'}` }
function focusError() { void nextTick(() => errorSummary.value?.focus()) }
watch(() => [detail.isError.value, diff.isError.value, policies.isError.value, capabilities.isError.value], states => { if (states.some(Boolean)) focusError() })
</script>
<template>
  <section>
    <RouterLink to="/versions">版本历史</RouterLink>
    <h1>版本差异</h1>
    <p v-if="detail.isPending.value" role="status">正在加载版本…</p>
    <p v-else-if="detail.isError.value" ref="errorSummary" tabindex="-1" role="alert">{{ detail.error.value?.message }}</p>
    <template v-else-if="detail.data.value">
      <p>候选版本：{{ detail.data.value.display_revision }} · {{ detail.data.value.config_hash }}</p>
      <GraphRuntimeBanner :capability="capabilities.data.value?.graph" />
      <GraphStatusPanel :project-i-d="projectID" :revision-i-d="targetID" />
      <form @submit.prevent="compare"><label>基准版本 ID <input v-model="baseDraft" aria-label="基准版本 ID"></label><button type="submit">比较</button></form>
      <p v-if="!baseID">NO_BASELINE：尚未选择不可变基准。首次发布不是“无变化”或通过比较，仍需完成全部门禁并明确建立基线。</p>
      <p v-else-if="diff.isPending.value" role="status">正在计算差异…</p>
      <p v-else-if="diff.isError.value" ref="errorSummary" tabindex="-1" role="alert">无效或不可读取的基准版本：{{ diff.error.value?.message }}。请选择同一项目的不可变 revision。</p>
      <p v-else-if="diff.data.value?.baseline_state === 'NO_BASELINE'">NO_BASELINE：当前项目没有正式版本基准；请完成首次基线确认。</p>
      <p v-else-if="!diff.data.value?.changes.length">与所选基准内容相同。</p>
      <ul v-else aria-label="字段差异列表">
        <li v-for="change in diff.data.value!.changes" :key="`${change.entity_id}:${change.path}:${change.kind}`">
          <strong>{{ change.kind }}</strong> · <RouterLink :to="{ path: `/config/${change.entity_kind}/${change.entity_id}`, query: { field_path: change.path } }">{{ change.entity_kind }} {{ change.entity_id }}</RouterLink> · <code>{{ change.path }}</code> <span v-if="ordinal(change)">· {{ ordinal(change) }}</span>
          <details><summary :aria-label="`展开 ${change.kind} ${change.path} 的前后值`">查看前后值</summary><p>旧值</p><pre>{{ formatValue(change.old_value) }}</pre><p>新值</p><pre>{{ formatValue(change.new_value) }}</pre></details>
        </li>
      </ul>
      <section aria-labelledby="release-heading">
        <h2 id="release-heading">发布检查</h2>
        <p v-if="policies.isPending.value || capabilities.isPending.value" role="status">正在加载发布策略与 Gate 能力…</p>
        <p v-else-if="policies.isError.value || capabilities.isError.value" ref="errorSummary" tabindex="-1" role="alert">无法加载发布 Gate；请刷新后重试。</p>
        <p v-else-if="!policy">没有可用发布策略，无法发布当前候选。</p>
        <template v-else><ReleaseGateChecklist ref="gateChecklist" :policy="policy" :capability="capabilities.data.value?.release" /><ReleaseConfirmationForm :candidate="detail.data.value" :policy="policy" :enabled="Boolean(capabilities.data.value?.release.enabled)" :suggested-baseline="suggestedBaseline" @queued="queuedLocation = $event" @baseline-conflict="releases.refetch()" @focus-checklist="gateChecklist?.focus()" /><ReleaseJobProgress v-if="queuedJobID" :project-i-d="projectID" :job-i-d="queuedJobID" @release-committed="activeReleaseID = $event.id" /><p v-if="activeReleaseID">当前正式版本：{{ activeReleaseID }}（已确认指针提交）</p><p v-if="!capabilities.data.value?.release.enabled">发布按钮由服务端 Gate 状态禁用；客户端不会自行判定 PASS。</p></template>
      </section>
    </template>
  </section>
</template>
