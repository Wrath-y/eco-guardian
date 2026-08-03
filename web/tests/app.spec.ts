import { render, screen } from '@testing-library/vue'
import { createPinia } from 'pinia'
import { VueQueryPlugin } from '@tanstack/vue-query'
import { createRouter, createMemoryHistory } from 'vue-router'
import App from '../src/App.vue'

it('renders the application shell', async () => {
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/', component: { template: '<h1>Eco Guardian</h1>' } }] })
  router.push('/')
  await router.isReady()
  render(App, { global: { plugins: [createPinia(), VueQueryPlugin, router] } })
  expect(screen.getByRole('heading', { name: 'Eco Guardian' })).toBeTruthy()
})
