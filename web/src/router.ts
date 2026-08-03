import { createRouter, createWebHistory } from 'vue-router'
import { useProjectStore } from './stores/project'
import ProjectsView from './views/ProjectsView.vue'
import EntityListView from './views/EntityListView.vue'
import EntityEditorView from './views/EntityEditorView.vue'

const router = createRouter({ history: createWebHistory(), routes: [{ path: '/', redirect: '/projects' }, { path: '/projects', component: ProjectsView }, { path: '/config/:kind', component: EntityListView, meta: { project: true } }, { path: '/config/:kind/:id', component: EntityEditorView, meta: { project: true } }] })
router.beforeEach(async to => { const project=useProjectStore(); if(!project.loaded) await project.refresh(); if(to.meta.project&&!project.current)return '/projects' })
export default router
