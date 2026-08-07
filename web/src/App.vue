<template>
  <main>
    <RouterView />
  </main>
</template>
<style>
:focus-visible { outline: 3px solid #005fcc; outline-offset: 2px; }
button, input, textarea, select { font: inherit; }
@media (max-width: 1024px) {
  main { max-width: 100%; padding: 1rem; }
  label, textarea, input { max-width: 100%; }
  pre { max-width: 100%; overflow-x: auto; }
}
</style>
