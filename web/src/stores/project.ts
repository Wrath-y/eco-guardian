import { defineStore } from 'pinia'

export interface ActiveProject { id: string; name: string; db_schema_version: number }
export const useProjectStore = defineStore('project', {
  state: () => ({ current: null as ActiveProject | null, loaded: false }),
  actions: {
    async refresh() {
      const response = await fetch('/api/v1/projects/current')
      this.current = response.ok ? await response.json() as ActiveProject : null
      this.loaded = true
    },
    clear() { this.current = null; this.loaded = true },
  },
})
