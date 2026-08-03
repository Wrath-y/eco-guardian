import vue from 'eslint-plugin-vue'

export default [
  ...vue.configs['flat/recommended'],
  { rules: { 'vue/multi-word-component-names': 'off' } },
  { ignores: ['dist/', 'playwright-report/', 'test-results/'] },
]
