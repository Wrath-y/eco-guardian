<script setup lang="ts">
import type { components } from '@/api/generated'
type Capability = components['schemas']['RuntimeCapabilities']['graph']
defineProps<{ capability?: Capability }>()
</script>
<template>
  <aside v-if="capability" aria-label="图谱运行时状态" role="status">
    <strong>Graph runtime：</strong>{{ capability.available ? (capability.compatible ? '可用且兼容' : '可用但不兼容') : '不可用' }}
    <span v-if="capability.degradations.length"> · 降级：{{ capability.degradations.join('、') }}</span>
    <span v-if="capability.disabled_reasons.length"> · 原因：{{ capability.disabled_reasons.join('、') }}</span>
    <span v-if="capability.observed_at"> · {{ capability.observed_at }}</span>
  </aside>
</template>
