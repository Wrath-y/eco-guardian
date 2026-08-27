import { cleanup } from '@testing-library/vue'
import { afterEach, beforeEach } from 'vitest'

beforeEach(() => {
  Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1024 })
  Object.defineProperty(window, 'innerHeight', { configurable: true, value: 768 })
  window.dispatchEvent(new Event('resize'))
})
afterEach(cleanup)
