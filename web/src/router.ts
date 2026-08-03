import { createRouter, createWebHistory } from 'vue-router'

const Home = { template: '<h1>Eco Guardian</h1>' }

export default createRouter({ history: createWebHistory(), routes: [{ path: '/', component: Home }] })
