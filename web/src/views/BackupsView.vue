<script setup lang="ts">
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import { useQueryClient } from '@tanstack/vue-query'
import BackupRestoreJobProgress from '@/components/BackupRestoreJobProgress.vue'
import { backupKeys, createManualBackup, createRestore, createRestorePreflight, listBackups, useBackups, type BackupRecord, type RestorePreflight } from '@/api/backups'
import { useProjectStore } from '@/stores/project'

const project = useProjectStore()
const client = useQueryClient()
const projectID = computed(() => project.current?.id ?? '')
const query = useBackups(projectID)
const more = ref<BackupRecord[]>([])
const nextCursor = ref('')
const loadingMore = ref(false)
const reason = ref('manual backup')
const submitting = ref(false)
const actionError = ref('')
const backupJobID = ref('')
const restoreJobID = ref('')
const selected = ref<BackupRecord>()
const preflight = ref<RestorePreflight>()
const preflighting = ref(false)
const confirmation = ref('')
const restoring = ref(false)
const errorSummary = ref<HTMLElement>()
const confirmationHeading = ref<HTMLElement>()
const items = computed(() => [...(query.data.value?.items ?? []), ...more.value])
const restorable = (item: BackupRecord) => item.validation_state === 'valid' && (item.compatibility_state === 'current' || item.compatibility_state === 'older') && item.project_uuid === projectID.value
const formatBytes = (bytes: number | null) => bytes === null ? '未知' : new Intl.NumberFormat('zh-CN', { style: 'unit', unit: 'megabyte', maximumFractionDigits: 1 }).format(bytes / 1024 / 1024)
const short = (value: string | null) => value === null ? '未知' : `${value.slice(0, 8)}…${value.slice(-6)}`
const sourceSummary = (item: BackupRecord) => item.source.revision_id ? `修订 ${short(item.source.revision_id)}` : item.source.release_id ? `发布 ${short(item.source.release_id)}` : item.source.migration_id ? `迁移 ${item.source.migration_id}` : item.source.caller_job_id ? `任务 ${short(item.source.caller_job_id)}` : '当前项目快照'
const newKey = () => crypto.randomUUID()
async function manualBackup() { submitting.value = true; actionError.value = ''; try { const accepted = await createManualBackup(reason.value.trim() || 'manual backup', newKey()); backupJobID.value = accepted.job.id; sessionStorage.setItem(`backup-job:${projectID.value}`, accepted.job.id) } catch (cause) { actionError.value = cause instanceof Error ? cause.message : '无法提交手动备份' } finally { submitting.value = false } }
async function loadMore() { const cursor = query.data.value?.next_cursor || nextCursor.value; if (!cursor) return; loadingMore.value = true; try { const page = await listBackups(cursor); more.value = [...more.value, ...page.items]; nextCursor.value = page.next_cursor || '' } catch (cause) { actionError.value = cause instanceof Error ? cause.message : '无法读取更多备份' } finally { loadingMore.value = false } }
async function inspectRestore(item: BackupRecord) { selected.value = item; preflight.value = undefined; confirmation.value = ''; preflighting.value = true; actionError.value = ''; try { preflight.value = await createRestorePreflight(item.backup_id) } catch (cause) { actionError.value = cause instanceof Error ? cause.message : '恢复预检失败' } finally { preflighting.value = false } }
async function confirmRestore() { if (!preflight.value || confirmation.value !== 'RESTORE') return; restoring.value = true; actionError.value = ''; try { const accepted = await createRestore(preflight.value, newKey()); restoreJobID.value = accepted.job.id; sessionStorage.setItem(`restore-job:${projectID.value}`, accepted.job.id); preflight.value = undefined; confirmation.value = '' } catch (cause) { actionError.value = cause instanceof Error ? cause.message : '无法提交恢复' } finally { restoring.value = false } }
async function terminal(kind: 'backup' | 'restore') { await client.invalidateQueries({ queryKey: backupKeys.inventory(projectID.value) }); if (kind === 'backup') sessionStorage.removeItem(`backup-job:${projectID.value}`); else sessionStorage.removeItem(`restore-job:${projectID.value}`) }
watch(actionError, async value => { if (value) { await nextTick(); errorSummary.value?.focus() } })
watch(preflight, async value => { if (value) { await nextTick(); confirmationHeading.value?.focus() } })
onMounted(() => { backupJobID.value = sessionStorage.getItem(`backup-job:${projectID.value}`) ?? ''; restoreJobID.value = sessionStorage.getItem(`restore-job:${projectID.value}`) ?? '' })
</script>

<template>
  <section aria-labelledby="backups-heading">
    <h1 id="backups-heading">备份与恢复</h1>
    <p>列表和任务状态始终来自服务端；页面不接受文件路径，也不会把恢复当作复制或生成新项目。</p>
    <p v-if="actionError" ref="errorSummary" role="alert" tabindex="-1">{{ actionError }}</p>
    <form class="manual" @submit.prevent="manualBackup"><h2>创建手动备份</h2><label>原因 <input v-model="reason" maxlength="256"></label><button type="submit" :disabled="submitting">{{ submitting ? '正在提交…' : '立即备份' }}</button></form>
    <BackupRestoreJobProgress v-if="backupJobID" :job-id="backupJobID" kind="backup" @terminal="terminal('backup')" />
    <BackupRestoreJobProgress v-if="restoreJobID" :job-id="restoreJobID" kind="restore" @terminal="terminal('restore')" />
    <h2>备份清单</h2>
    <p v-if="query.isPending.value" role="status">正在读取备份清单…</p>
    <p v-else-if="query.isError.value" role="alert">{{ query.error.value?.message }} <button type="button" @click="query.refetch()">重试</button></p>
    <p v-else-if="!items.length">尚无可显示的备份。</p>
    <template v-else>
      <p>日常备份保留 {{ query.data.value?.retention.daily_count }} 个；发布/迁移共享保留 {{ query.data.value?.retention.release_migration_count }} 个。手动和恢复前备份不自动清理。</p>
      <div class="table-wrap"><table><thead><tr><th>时间</th><th>类型/来源</th><th>版本</th><th>大小</th><th>完整性</th><th>身份摘要</th><th>操作</th></tr></thead><tbody><tr v-for="item in items" :key="item.backup_id"><td>{{ new Date(item.created_at).toLocaleString() }}</td><td>{{ item.type }}<br>{{ sourceSummary(item) }}</td><td>App {{ item.app_version ?? '未知' }} / Schema {{ item.schema_version ?? '未知' }}</td><td>{{ formatBytes(item.db_bytes) }}</td><td>{{ item.validation_state }} / {{ item.compatibility_state }}<br><span class="hash">SHA-256 {{ short(item.db_sha256) }}</span></td><td>项目 {{ short(item.project_uuid) }}<br>备份 {{ short(item.backup_id) }}</td><td><button type="button" :disabled="!restorable(item) || preflighting" :aria-label="`预检恢复备份 ${item.backup_id}`" @click="inspectRestore(item)">恢复预检</button><span v-if="!restorable(item)"> 不可安全恢复</span></td></tr></tbody></table></div>
      <button v-if="query.data.value?.next_cursor || nextCursor" type="button" :disabled="loadingMore" @click="loadMore">{{ loadingMore ? '正在读取…' : '加载更多' }}</button>
    </template>
    <section v-if="preflight" class="confirm" aria-labelledby="restore-confirm-heading">
      <h2 id="restore-confirm-heading" ref="confirmationHeading" tabindex="-1">破坏性恢复确认</h2>
      <p><strong>目标：</strong>当前活动项目 {{ short(preflight.backup.project_uuid) }}；不会创建副本或新 UUID。</p>
      <ul><li>校验：{{ preflight.backup.validation_state }}；兼容性：{{ preflight.backup.compatibility_state }}</li><li>目标可写：{{ preflight.writable ? '是' : '否' }}；空间充足：{{ preflight.free_space_sufficient ? '是' : '否' }}</li><li>恢复前强制备份：{{ preflight.confirmation.restore_pre_backup_required ? '需要' : '不适用' }}</li><li>维护锁：需要；旧 Schema 迁移：{{ preflight.confirmation.migration_required ? '需要' : '不需要' }}</li><li>恢复后 Graph：待重新验证，不会把外部索引当作已就绪。</li></ul>
      <label>输入 <code>RESTORE</code> 以确认 <input v-model="confirmation" autocomplete="off" aria-describedby="restore-warning"></label>
      <p id="restore-warning">提交后会在不可中断边界关闭连接、替换数据库，并只在验证成功或安全回滚后解除维护状态。</p>
      <button type="button" :disabled="confirmation !== 'RESTORE' || restoring" @click="confirmRestore">{{ restoring ? '正在提交…' : '确认恢复当前项目' }}</button>
      <button type="button" @click="preflight = undefined; confirmation = ''">取消</button>
    </section>
  </section>
</template>

<style scoped>
.manual { display: flex; align-items: end; gap: .75rem; flex-wrap: wrap; padding: 1rem; border: 1px solid #aab5c0; }.manual h2 { flex-basis: 100%; }.manual label { display: grid; gap: .25rem; }.table-wrap { overflow-x: auto; } table { width: 100%; border-collapse: collapse; } th, td { border: 1px solid #aab5c0; padding: .6rem; text-align: left; vertical-align: top; }.hash { overflow-wrap: anywhere; }.confirm { margin-top: 1.5rem; padding: 1rem; border: 3px solid #9b2c2c; }.confirm label { display: grid; gap: .35rem; max-width: 28rem; }.confirm button { margin: .75rem .75rem 0 0; }
</style>
