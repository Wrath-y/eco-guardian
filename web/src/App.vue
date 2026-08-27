<script setup lang="ts">
import { computed } from 'vue'
import MaintenanceOverlay from '@/components/MaintenanceOverlay.vue'
import RuntimeStatusBanner from '@/components/RuntimeStatusBanner.vue'
import { useRuntimeStore } from '@/stores/runtime'

const runtime = useRuntimeStore()
const maintenanceBlocked = computed(() => ['maintenance', 'recovering', 'recovery_required'].includes(runtime.runtimeCapabilities?.backup.restore_state ?? 'idle'))
</script>
<template>
  <main>
    <RuntimeStatusBanner />
    <div :inert="maintenanceBlocked || undefined" :aria-hidden="maintenanceBlocked || undefined">
      <nav aria-label="主导航">
        <RouterLink to="/projects">项目</RouterLink>
        <RouterLink to="/settings">运行设置</RouterLink>
        <RouterLink to="/backups">备份与恢复</RouterLink>
        <RouterLink to="/impact">影响分析</RouterLink>
      </nav>
      <RouterView />
    </div>
    <MaintenanceOverlay />
  </main>
</template>
<style>
:focus-visible { outline: 3px solid #005fcc; outline-offset: 2px; }
button, input, textarea, select { font: inherit; }
main { max-width: 1200px; margin: 0 auto; padding: 1rem; }
nav { display: flex; gap: 1rem; margin: 1rem 0; }
@media (max-width: 1024px) {
  main { max-width: 100%; padding: 1rem; }
  label, textarea, input { max-width: 100%; }
  pre { max-width: 100%; overflow-x: auto; }
}
</style>
