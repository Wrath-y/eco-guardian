<script setup lang="ts">
import { computed, ref } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { useProjectStore } from '@/stores/project'
import { createCheckpoint, restoreRelease, useReleaseHistory, useRevisionDetail, useRevisionHistory } from '@/api/versions'

const project = useProjectStore(); const route = useRoute(); const router = useRouter()
const projectID = computed(() => project.current?.id ?? '')
const revisionCursor = ref(''); const releaseCursor = ref('')
const selectedID = computed(() => typeof route.query.revision === 'string' ? route.query.revision : '')
const candidateID = computed(() => typeof route.query.candidate === 'string' ? route.query.candidate : '')
const history = useRevisionHistory(() => projectID.value, () => revisionCursor.value)
const releases = useReleaseHistory(() => projectID.value, () => releaseCursor.value)
const selected = useRevisionDetail(() => projectID.value, () => selectedID.value)
const checkpointName = ref(''); const checkpointError = ref(''); const checkpointBusy = ref(false)
const restoreError = ref(''); const restoringID = ref('')
const workingRevisionID = computed(() => history.data.value?.items.find(value => value.status.includes('working'))?.id ?? '')
function select(id: string) { void router.replace({ query: { ...route.query, revision: id } }) }
function selectCandidate(id: string) { void router.replace({ query: { ...route.query, candidate: id } }) }
async function checkpoint() {
  if (!selectedID.value) return
  checkpointBusy.value = true; checkpointError.value = ''
  try { const created = await createCheckpoint(selectedID.value, checkpointName.value); await history.refetch(); await router.replace({ query: { ...route.query, revision: created.id, candidate: created.id } }); checkpointName.value = '' }
  catch (cause) { checkpointError.value = cause instanceof Error ? cause.message : '无法创建检查点' } finally { checkpointBusy.value = false }
}
async function restore(releaseID: string) {
  if (!workingRevisionID.value) { restoreError.value = '当前工作 revision 不可用，无法安全创建回滚候选。'; return }
  restoringID.value = releaseID; restoreError.value = ''
  try { const created = await restoreRelease(workingRevisionID.value, releaseID); await history.refetch(); await router.replace({ query: { ...route.query, revision: created.id, candidate: created.id } }) }
  catch (cause) { restoreError.value = cause instanceof Error ? cause.message : '无法从正式版本创建回滚候选' } finally { restoringID.value = '' }
}
function label(status: string[], id: string) { return [status.includes('working') ? '当前工作' : '', candidateID.value === id || status.includes('candidate') ? '候选' : '', status.includes('active_release') ? '当前正式版本' : '', '历史'].filter(Boolean).join(' · ') }
</script>
<template>
  <section>
    <RouterLink to="/projects">项目</RouterLink>
    <h1>版本历史</h1>
    <p v-if="history.isPending.value" role="status">正在加载版本历史…</p>
    <p v-else-if="history.isError.value" role="alert">{{ history.error.value?.message }} <button @click="history.refetch()">重试</button></p>
    <p v-else-if="!history.data.value?.items.length">尚无版本记录。</p>
    <ul v-else aria-label="配置版本历史">
      <li v-for="revision in history.data.value!.items" :key="revision.id">
        <button @click="select(revision.id)">版本 {{ revision.display_revision }}</button>
        <button type="button" @click="selectCandidate(revision.id)">选择候选</button>
        <RouterLink :to="{ path: `/versions/${revision.id}/diff`, query: { base: candidateID || undefined } }">查看差异</RouterLink>
        <span> · {{ revision.metadata.name || revision.config_hash.slice(0, 12) }} · {{ label(revision.status, revision.id) }}</span>
        <span v-if="revision.metadata.source_release_id"> · 从正式版本回滚</span><span v-else-if="revision.metadata.parent_revision_id"> · 检查点</span>
      </li>
    </ul>
    <button v-if="history.data.value?.next_cursor" @click="revisionCursor = history.data.value!.next_cursor!">加载更多版本</button>

    <section v-if="selectedID" aria-labelledby="timeline-heading">
      <h2 id="timeline-heading">状态时间线</h2>
      <p v-if="selected.isPending.value" role="status">正在加载时间线…</p>
      <p v-else-if="selected.isError.value" role="alert">{{ selected.error.value?.message }}</p>
      <template v-else-if="selected.data.value"><ol><li v-for="event in selected.data.value.timeline" :key="`${event.id}:${event.type}`">{{ event.occurred_at }} · {{ event.type }} <span v-if="event.status">· {{ event.status }}</span></li></ol><form aria-label="创建检查点" @submit.prevent="checkpoint"><label>检查点名称 <input v-model="checkpointName" aria-label="检查点名称"></label><button type="submit" :disabled="checkpointBusy">{{ checkpointBusy ? '正在创建检查点…' : '创建同内容检查点' }}</button></form><p v-if="checkpointError" role="alert">{{ checkpointError }}</p></template>
    </section>

    <section aria-labelledby="release-heading">
      <h2 id="release-heading">正式版本历史</h2>
      <p v-if="releases.isPending.value" role="status">正在加载正式版本…</p>
      <p v-else-if="releases.isError.value" role="alert">{{ releases.error.value?.message }} <button @click="releases.refetch()">重试</button></p>
      <p v-else-if="!releases.data.value?.items.length">尚无正式版本。</p>
      <ul v-else><li v-for="release in releases.data.value!.items" :key="release.id">正式版本 {{ release.id.slice(0, 8) }} · revision {{ release.revision_id.slice(0, 8) }} · {{ release.notes || '无发布说明' }} <button type="button" :disabled="Boolean(restoringID) || !workingRevisionID" @click="restore(release.id)">{{ restoringID === release.id ? '正在创建回滚候选…' : '从此正式版本创建回滚候选' }}</button></li></ul>
      <p v-if="restoreError" role="alert">{{ restoreError }}</p>
      <button v-if="releases.data.value?.next_cursor" @click="releaseCursor = releases.data.value!.next_cursor!">加载更多正式版本</button>
    </section>
  </section>
</template>
