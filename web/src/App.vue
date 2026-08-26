<script setup lang="ts">
import RuntimeStatusBanner from '@/components/RuntimeStatusBanner.vue'
</script>
<template>
  <main>
    <RuntimeStatusBanner />
    <nav aria-label="主导航">
      <RouterLink to="/projects">项目</RouterLink>
      <RouterLink to="/settings">运行设置</RouterLink>
      <RouterLink to="/impact">影响分析</RouterLink>
    </nav>
    <RouterView />
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
