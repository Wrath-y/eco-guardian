<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { useRuntimeStore } from '@/stores/runtime'

const runtime = useRuntimeStore()
const heading = ref<HTMLElement>()
const state = computed(() => runtime.runtimeCapabilities?.backup.restore_state ?? 'idle')
const visible = computed(() => ['maintenance', 'recovering', 'recovery_required'].includes(state.value))
const recoveryRequired = computed(() => state.value === 'recovery_required' || runtime.runtimeCapabilities?.backup.recovery_required === true)

watch(visible, async active => {
  if (!active) return
  await nextTick()
  heading.value?.focus()
}, { immediate: true })
</script>

<template>
  <section v-if="visible" class="maintenance-overlay" role="alertdialog" aria-modal="true" aria-labelledby="maintenance-heading" aria-describedby="maintenance-description">
    <div class="maintenance-card">
      <h2 id="maintenance-heading" ref="heading" tabindex="-1">{{ recoveryRequired ? '项目需要安全恢复' : '项目正在执行不可中断维护' }}</h2>
      <p id="maintenance-description">
        {{ recoveryRequired ? '项目修改、切换和关闭保持禁用，直到服务端恢复诊断进入安全终态。' : '数据库替换边界已经进入；为避免留下无法判断的数据库状态，此时不能取消、修改、切换或关闭项目。' }}
      </p>
      <p>请不要移动或修改项目文件。页面只依据服务端维护与恢复状态解除此遮罩，不会从本地计时或页面刷新推断成功。</p>
      <div class="safe-actions" aria-label="维护期间安全操作">
        <button type="button" :disabled="runtime.loading" @click="runtime.refresh">{{ runtime.loading ? '正在刷新' : '刷新服务端状态' }}</button>
        <RouterLink to="/backups">查看恢复与 Job 状态</RouterLink>
        <RouterLink to="/settings">查看设置与日志位置</RouterLink>
      </div>
      <p v-if="runtime.error" role="alert">{{ runtime.error }}</p>
    </div>
  </section>
</template>

<style scoped>
.maintenance-overlay { position: fixed; inset: 0; z-index: 1000; display: grid; place-items: center; padding: 1rem; background: rgb(15 23 42 / 78%); }
.maintenance-card { width: min(42rem, 100%); border: 3px solid #8a4b00; border-radius: .75rem; padding: 1.25rem; background: #fff8e8; color: #281600; box-shadow: 0 1rem 3rem rgb(0 0 0 / 35%); }
.maintenance-card h2 { margin-top: 0; }
.safe-actions { display: flex; flex-wrap: wrap; gap: .75rem; align-items: center; }
</style>
